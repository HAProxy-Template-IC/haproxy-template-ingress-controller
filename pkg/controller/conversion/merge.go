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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dario.cat/mergo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// migrationCoverageKey is the one spec field that accumulates across a
	// merged set instead of being overwritten. It is a list of per-source
	// declarations, one per contributing template library, so an overwrite
	// would keep only the last library's entry and silently make the
	// migration report under-report.
	migrationCoverageKey = "migrationCoverage"

	// templateSnippetsKey names the merged namespace where a duplicate key is
	// worth reporting. Snippets are addressed by name from `render_glob`
	// patterns, so two sources defining the same name resolve to the later one
	// with nothing to show for it.
	templateSnippetsKey = "templateSnippets"

	specKey = "spec"
)

// SnippetOverride records one templateSnippets name defined by more than one
// source. An operator overriding a bundled snippet is the documented escape
// hatch and the expected case; two libraries colliding is a bug, and before the
// merge moved into the controller there was nothing to notice it.
type SnippetOverride struct {
	Name string
	// PreviousSource last defined the snippet before it was replaced.
	PreviousSource string
	// WinningSource defines the snippet that ends up in the merged config.
	WinningSource string
}

// MergeSpecs merges the .spec of each HAProxyTemplateConfig into one, in
// argument order, later wins.
//
// The merge primitive is mergo.MergeWithOverwrite — literally the call sprig's
// `mustMergeOverwrite` makes (vendor/github.com/Masterminds/sprig/v3/dict.go),
// against the same vendored mergo, starting from the same empty accumulator.
// The Helm chart prepares one config per template library and this assembles
// them, so the two sides have to agree exactly; sharing the primitive is what
// guarantees that, where a reimplementation would drift.
//
// Identity and metadata come from the LAST source — by convention the
// operator's own config — so status write-back and events target the object an
// operator edits.
//
// The returned object is safe to hand to ParseCRD; sources are not modified.
func MergeSpecs(sources []*unstructured.Unstructured) (*unstructured.Unstructured, []SnippetOverride, error) {
	if len(sources) == 0 {
		return nil, nil, errors.New("no HAProxyTemplateConfig sources to merge")
	}

	merged := map[string]any{}
	coverage := []any{}
	definedBy := map[string]string{}
	var overrides []SnippetOverride

	for _, source := range sources {
		if err := validateResourceType(source); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", source.GetName(), err)
		}

		spec, err := extractSpec(source)
		if err != nil {
			return nil, nil, err
		}

		if entries, ok := spec[migrationCoverageKey].([]any); ok {
			coverage = append(coverage, entries...)
			delete(spec, migrationCoverageKey)
		}

		overrides = append(overrides, snippetOverrides(definedBy, spec, source.GetName())...)

		if err := mergo.MergeWithOverwrite(&merged, spec); err != nil {
			return nil, nil, fmt.Errorf("merging spec of %s: %w", source.GetName(), err)
		}
	}

	if len(coverage) > 0 {
		merged[migrationCoverageKey] = coverage
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

// snippetOverrides records which snippet names this source redefines and
// updates definedBy to name it as the current owner. Sorted so the report is
// stable across runs.
func snippetOverrides(definedBy map[string]string, spec map[string]any, source string) []SnippetOverride {
	snippets, ok := spec[templateSnippetsKey].(map[string]any)
	if !ok {
		return nil
	}

	var overrides []SnippetOverride
	for _, name := range slices.Sorted(maps.Keys(snippets)) {
		if previous, exists := definedBy[name]; exists {
			overrides = append(overrides, SnippetOverride{
				Name:           name,
				PreviousSource: previous,
				WinningSource:  source,
			})
		}
		definedBy[name] = source
	}
	return overrides
}
