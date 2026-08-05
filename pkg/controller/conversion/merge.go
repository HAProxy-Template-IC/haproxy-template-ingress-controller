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
// among all but the last source is an error; the last source — by convention
// the operator's own config — may override anything, reported as a
// SpecOverride. The exemption is positional rather than marker-based because
// every chart-rendered object is partial (base owns haproxyConfig, the main
// object owns podSelector), so no marker distinguishes "library shard" from
// "the object whose overrides are intentional".
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
// The merged object carries the LAST source's metadata, so its resourceVersion
// alone would not change when only a library config changed — and the
// redundant-reinit guard compares versions for equality, so such a change would
// be silently filtered out. Naming every member makes the version change
// whenever any member does.
func CompositeVersion(sources []*unstructured.Unstructured) string {
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, source.GetName()+"="+source.GetResourceVersion())
	}
	return strings.Join(parts, ",")
}

// validateResourceType rejects anything that isn't an HAProxyTemplateConfig of
// the expected API version.
func validateResourceType(resource *unstructured.Unstructured) error {
	if kind := resource.GetKind(); kind != expectedKind {
		return fmt.Errorf("expected %s, got %s", expectedKind, kind)
	}
	if apiVersion := resource.GetAPIVersion(); apiVersion != expectedAPIVersion {
		return fmt.Errorf("expected apiVersion %s, got %s", expectedAPIVersion, apiVersion)
	}
	return nil
}

// prepareSourceSpec validates one source's type, pulls the two accumulating
// fields out of its spec (migrationCoverage into coverage, validationTests
// into testSources), and returns the remaining spec for mergo.
func prepareSourceSpec(source *unstructured.Unstructured, coverage *[]any, testSources *[]ValidationTestSource) (map[string]any, error) {
	if err := validateResourceType(source); err != nil {
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
				"%s %q is defined by both %s and %s: only the last config in the merge order may override an entry, "+
					"otherwise one definition silently replaces the other",
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
