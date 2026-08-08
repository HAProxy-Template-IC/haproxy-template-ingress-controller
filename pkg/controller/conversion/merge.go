// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package conversion

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"dario.cat/mergo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

const (
	// migrationCoverageKey is one of the two spec fields that accumulate across
	// a merged set instead of being overwritten. It is a list of per-source
	// declarations, one per contributing template library, so an overwrite
	// would keep only the last library's entry and silently make the
	// migration report under-report.
	migrationCoverageKey = "migrationCoverage"

	// validationTestsKey is the other accumulating field. Tests are unioned
	// per source rather than mergo-merged: a deep map merge of two tests
	// sharing a name produces a hybrid neither author wrote (one side's
	// assertions over the other side's surviving fixtures) with no error and
	// nothing logged — and both gates that run the suite merge through this
	// function, so the reduced suite would pass them silently.
	validationTestsKey = "validationTests"

	templateSnippetsKey = "templateSnippets"
	mapsKey             = "maps"
	filesKey            = "files"
	sslCertificatesKey  = "sslCertificates"
	k8sResourcesKey     = "k8sResources"
	haproxyConfigKey    = "haproxyConfig"

	specKey = "spec"
)

// guardedSections are the named-map spec sections where a name defined by two
// sources is a silent last-writer-wins under mergo. Within these, a duplicate
// among the referenced snippets is an error; only the last source may override
// anything, reported as a SpecOverride.
//
// The last source is always the HAProxyTemplateConfig itself — assembly appends
// it after the snippets it references — so the exemption lands exactly on the
// object an operator edits, and nowhere else.
var guardedSections = []string{templateSnippetsKey, mapsKey, filesKey, sslCertificatesKey, k8sResourcesKey}

// SpecOverride records one guarded-section name defined by more than one
// source. An operator overriding a bundled entry is the documented escape
// hatch and the expected case; it is reported so the override is visible
// rather than silent.
type SpecOverride struct {
	// Section is the spec section the name lives in (templateSnippets, maps,
	// files, sslCertificates, k8sResources, or haproxyConfig).
	Section string
	Name    string
	// PreviousSource last defined the name before it was replaced.
	PreviousSource string
	// WinningSource defines the entry that ends up in the merged config.
	WinningSource string
}

// MergeSpecs merges the .spec of each HAProxyTemplateConfig into one, in
// argument order, later wins.
//
// The merge primitive is mergo.MergeWithOverwrite — literally the call sprig's
// `mustMergeOverwrite` makes (vendor/github.com/Masterminds/sprig/v3/dict.go),
// against the same vendored mergo, starting from the same empty accumulator.
//
// Two spec fields never reach mergo:
//
//   - migrationCoverage accumulates (a list of per-source declarations);
//   - validationTests are unioned per source through UnionValidationTests —
//     error on a non-`_global` duplicate, `_global` contributions accumulate —
//     because a mergo deep-merge of two same-named tests silently fabricates a
//     hybrid test neither author wrote.
//
// Within guardedSections, a name defined by two of the first N-1 sources is an
// error; the LAST source may override anything, each such override is
// returned, and the override REPLACES the entry — a mergo deep-merge of two
// same-named entries would blend the operator's fields with library leftovers.
// haproxyConfig gets the same guard on the whole section: it is one template,
// not a named map, and a second source setting it replaces the main config
// outright.
//
// Identity and metadata come from the LAST source — by convention the
// operator's own config — so status write-back and events target the object an
// operator edits.
//
// The returned object is safe to hand to ParseCRD; sources are not modified.
func MergeSpecs(sources []*unstructured.Unstructured) (*unstructured.Unstructured, []SpecOverride, error) {
	if len(sources) == 0 {
		return nil, nil, errors.New("no HAProxyTemplateConfig sources to merge")
	}

	merged := map[string]any{}
	coverage := []any{}
	definedBy := map[string]map[string]string{} // section -> name -> source
	var testSources []ValidationTestSource
	var overrides []SpecOverride

	for i, source := range sources {
		spec, err := prepareSourceSpec(source, &coverage, &testSources)
		if err != nil {
			return nil, nil, err
		}

		isLast := i == len(sources)-1
		sectionOverrides, err := guardSections(definedBy, spec, source.GetName(), isLast)
		if err != nil {
			return nil, nil, err
		}
		overrides = append(overrides, sectionOverrides...)

		// An override REPLACES the entry. Left to mergo, two same-named
		// entries deep-merge: an operator overriding a library file but
		// omitting a sub-field the library set would inherit that field
		// silently — the same hybrid hazard validationTests are routed
		// around above. Dropping the losing entry first makes the merge
		// below insert the winning one whole.
		for _, o := range sectionOverrides {
			if o.Section == haproxyConfigKey {
				delete(merged, haproxyConfigKey)
			} else if section, ok := merged[o.Section].(map[string]any); ok {
				delete(section, o.Name)
			}
		}

		if err := mergo.MergeWithOverwrite(&merged, spec); err != nil {
			return nil, nil, fmt.Errorf("merging spec of %s: %w", source.GetName(), err)
		}
	}

	if len(coverage) > 0 {
		merged[migrationCoverageKey] = coverage
	}

	if len(testSources) > 0 {
		union, err := UnionValidationTests(testSources)
		if err != nil {
			return nil, nil, err
		}
		unionMap, err := validationTestsToUnstructured(union)
		if err != nil {
			return nil, nil, err
		}
		merged[validationTestsKey] = unionMap
	}

	last := sources[len(sources)-1]
	result := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(last.Object)}
	result.Object[specKey] = merged
	return result, overrides, nil
}

// CompositeVersion identifies the state of a whole merged set as one string.
//
// The merged object carries the LAST source's metadata, so that object's version
// alone would not change when only a library changed — and the redundant-reinit
// guard compares versions for equality, so such a change would be silently
// filtered out. Naming every member makes the version change whenever any
// member's SPEC does.
//
// Keyed on metadata.generation, NOT resourceVersion. resourceVersion moves on
// every write, including ones that change no configuration: the controller's own
// ownerReference stamping on each library, and its status writes. Each of those
// then looked like a config change and triggered a full validationTests run —
// under load the live gate timed out, the change was rejected, and template
// status patches (Ingress status among them) never applied.
//
// Generation is the right key because the apiserver bumps it on spec changes
// only: an operator's in-place edit to a library still reinitialises, while a
// metadata-only patch does not.
func CompositeVersion(sources []*unstructured.Unstructured) string {
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, source.GetName()+"="+strconv.FormatInt(source.GetGeneration(), 10))
	}
	return strings.Join(parts, ",")
}

// validateResourceType rejects anything that isn't an HAProxyTemplateConfig of
// the expected API version. The MERGED object is one, so this stays strict —
// a bare snippets object is not a configuration and must never parse as one.
func validateResourceType(resource *unstructured.Unstructured) error {
	if kind := resource.GetKind(); kind != expectedKind {
		return fmt.Errorf("expected %s, got %s", expectedKind, kind)
	}
	return validateAPIVersion(resource)
}

// validateMergeSourceType accepts either kind, because a merged set is the
// HAProxyTemplateLibrary a config references followed by the config itself.
func validateMergeSourceType(resource *unstructured.Unstructured) error {
	if kind := resource.GetKind(); kind != expectedKind && kind != libraryKind {
		return fmt.Errorf("expected %s or %s, got %s", expectedKind, libraryKind, kind)
	}
	return validateAPIVersion(resource)
}

func validateAPIVersion(resource *unstructured.Unstructured) error {
	if apiVersion := resource.GetAPIVersion(); apiVersion != expectedAPIVersion {
		return fmt.Errorf("expected apiVersion %s, got %s", expectedAPIVersion, apiVersion)
	}
	return nil
}

// prepareSourceSpec validates one source's type, pulls the two accumulating
// fields out of its spec (migrationCoverage into coverage, validationTests
// into testSources), and returns the remaining spec for mergo.
func prepareSourceSpec(source *unstructured.Unstructured, coverage *[]any, testSources *[]ValidationTestSource) (map[string]any, error) {
	if err := validateMergeSourceType(source); err != nil {
		return nil, fmt.Errorf("%s: %w", source.GetName(), err)
	}

	spec, err := extractSpec(source)
	if err != nil {
		return nil, err
	}

	if entries, ok := spec[migrationCoverageKey].([]any); ok {
		*coverage = append(*coverage, entries...)
		delete(spec, migrationCoverageKey)
	}

	tests, ok, err := extractValidationTests(spec, source.GetName())
	if err != nil {
		return nil, err
	}
	if ok {
		*testSources = append(*testSources, tests)
	}
	return spec, nil
}

// extractSpec returns a deep copy of the source's spec, or an empty map when
// the object carries none.
func extractSpec(source *unstructured.Unstructured) (map[string]any, error) {
	spec, found, err := unstructured.NestedMap(source.Object, specKey)
	if err != nil {
		return nil, fmt.Errorf("reading spec of %s: %w", source.GetName(), err)
	}
	if !found {
		return map[string]any{}, nil
	}
	return spec, nil
}

// extractValidationTests removes the source's validationTests from its spec
// (so mergo never sees them) and returns them as a typed union source.
//
// The unstructured→typed round trip is deliberate: UnionValidationTests on the
// API types is the single implementation of the union semantics, including the
// `_global` conflict rules, and this path must not drift from it. Fields the
// type does not know are dropped by the round trip, which is a no-op in
// practice — the structural CRD schema already prunes them, and ParseCRD is
// the next consumer either way.
func extractValidationTests(spec map[string]any, sourceName string) (ValidationTestSource, bool, error) {
	raw, ok := spec[validationTestsKey].(map[string]any)
	delete(spec, validationTestsKey)
	if !ok || len(raw) == 0 {
		return ValidationTestSource{}, false, nil
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return ValidationTestSource{}, false, fmt.Errorf("encoding validationTests of %s: %w", sourceName, err)
	}
	var tests map[string]v1alpha1.ValidationTest
	if err := json.Unmarshal(encoded, &tests); err != nil {
		return ValidationTestSource{}, false, fmt.Errorf("parsing validationTests of %s: %w", sourceName, err)
	}
	return ValidationTestSource{
		Origin: expectedKind + "/" + sourceName,
		Tests:  tests,
	}, true, nil
}

// validationTestsToUnstructured converts the union result back into the
// map-shaped spec field of the merged object.
func validationTestsToUnstructured(tests map[string]v1alpha1.ValidationTest) (map[string]any, error) {
	encoded, err := json.Marshal(tests)
	if err != nil {
		return nil, fmt.Errorf("encoding merged validationTests: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("re-parsing merged validationTests: %w", err)
	}
	// An empty runtime.RawExtension marshals to JSON null (omitempty does not
	// apply to structs), which no real input carries — the apiserver prunes
	// nulls on write. Drop them so the round trip is shape-faithful.
	for _, test := range out {
		fields, ok := test.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range fields {
			if value == nil {
				delete(fields, key)
			}
		}
	}
	return out, nil
}

// guardSections enforces the duplicate-name rule over guardedSections and the
// whole-section rule over haproxyConfig, recording the last source's
// overrides and rejecting everyone else's.
func guardSections(definedBy map[string]map[string]string, spec map[string]any, source string, isLast bool) ([]SpecOverride, error) {
	var overrides []SpecOverride

	record := func(section, name string) error {
		owners := definedBy[section]
		if owners == nil {
			owners = map[string]string{}
			definedBy[section] = owners
		}
		previous, exists := owners[name]
		if exists && !isLast {
			return fmt.Errorf(
				"%s %q is defined by both %s and %s: only the HAProxyTemplateConfig may override an entry a "+
					"snippet defines, otherwise one definition silently replaces the other",
				section, name, previous, source)
		}
		if exists {
			overrides = append(overrides, SpecOverride{
				Section:        section,
				Name:           name,
				PreviousSource: previous,
				WinningSource:  source,
			})
		}
		owners[name] = source
		return nil
	}

	for _, section := range guardedSections {
		entries, ok := spec[section].(map[string]any)
		if !ok {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(entries)) {
			if err := record(section, name); err != nil {
				return nil, err
			}
		}
	}

	// haproxyConfig is one template, not a named map: a second source setting
	// it replaces the main config outright, so the section itself is the name.
	if cfg, ok := spec[haproxyConfigKey].(map[string]any); ok && len(cfg) > 0 {
		if err := record(haproxyConfigKey, haproxyConfigKey); err != nil {
			return nil, err
		}
	}

	return overrides, nil
}

// LibraryRef is one spec.libraryRefs entry: the snippets object a config pulls
// in, and the revision it expects that object to report.
type LibraryRef struct {
	Name     string
	Revision string
}

// LibraryRefsOf reads spec.libraryRefs in declared order. Declared order IS
// merge order, so this is the only place that order comes from.
func LibraryRefsOf(config *unstructured.Unstructured) ([]LibraryRef, error) {
	raw, found, err := unstructured.NestedSlice(config.Object, "spec", "libraryRefs")
	if err != nil {
		return nil, fmt.Errorf("reading spec.libraryRefs: %w", err)
	}
	if !found {
		return nil, nil
	}

	refs := make([]LibraryRef, 0, len(raw))
	for i, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("spec.libraryRefs[%d] is not an object", i)
		}
		name, _ := fields["name"].(string)
		revision, _ := fields["revision"].(string)
		if name == "" || revision == "" {
			return nil, fmt.Errorf("spec.libraryRefs[%d] needs both name and revision", i)
		}
		refs = append(refs, LibraryRef{Name: name, Revision: revision})
	}
	return refs, nil
}

// RevisionOf reads a snippets object's spec.revision, returning "" when absent
// so it cannot accidentally equal a reference.
func RevisionOf(snippets *unstructured.Unstructured) string {
	revision, _, _ := unstructured.NestedString(snippets.Object, "spec", "revision")
	return revision
}

// AssembleSources orders a flat set of documents into merge order: the
// snippets the config references, in the order it declares them, followed by
// the config itself so its inline content wins.
//
// Document order is deliberately NOT used. Helm sorts rendered manifests by
// kind, so a `helm template` stream lists every HAProxyTemplateConfig before
// any HAProxyTemplateLibrary — the reverse of merge order. spec.libraryRefs is
// the only ordering authority.
func AssembleSources(documents []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	var config *unstructured.Unstructured
	snippets := make(map[string]*unstructured.Unstructured, len(documents))
	for _, document := range documents {
		switch document.GetKind() {
		case expectedKind:
			if config != nil {
				return nil, fmt.Errorf("expected one %s, got both %q and %q",
					expectedKind, config.GetName(), document.GetName())
			}
			config = document
		case libraryKind:
			snippets[document.GetName()] = document
		}
	}
	if config == nil {
		return nil, fmt.Errorf("no %s among the documents", expectedKind)
	}

	refs, err := LibraryRefsOf(config)
	if err != nil {
		return nil, err
	}

	ordered := make([]*unstructured.Unstructured, 0, len(refs)+1)
	for _, ref := range refs {
		observed, found := snippets[ref.Name]
		if !found {
			return nil, fmt.Errorf("%s %q references %s %q, which is not among the documents",
				expectedKind, config.GetName(), libraryKind, ref.Name)
		}
		if got := RevisionOf(observed); got != ref.Revision {
			return nil, fmt.Errorf("%s %q expects %s %q at revision %q, but it reports %q",
				expectedKind, config.GetName(), libraryKind, ref.Name, ref.Revision, got)
		}
		ordered = append(ordered, observed)
	}
	return append(ordered, config), nil
}

// ConfigOf returns the HAProxyTemplateConfig among a merged set, or nil.
//
// Selected by KIND, not by position. Assembly appends the config after the
// libraries it references, but nothing enforces that, and a reorder would
// otherwise make callers silently act on a library — stamping status onto a
// library's identity, or treating the config as one of its own dependencies.
func ConfigOf(sources []*unstructured.Unstructured) *unstructured.Unstructured {
	for _, source := range sources {
		if source.GetKind() == expectedKind {
			return source
		}
	}
	return nil
}

// LibrariesOf returns the HAProxyTemplateLibrary sources, in merge order.
func LibrariesOf(sources []*unstructured.Unstructured) []*unstructured.Unstructured {
	libraries := make([]*unstructured.Unstructured, 0, len(sources))
	for _, source := range sources {
		if source.GetKind() == libraryKind {
			libraries = append(libraries, source)
		}
	}
	return libraries
}
