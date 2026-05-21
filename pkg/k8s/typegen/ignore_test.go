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

package typegen

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// TestConverter_IgnoreFields covers the integration with
// HAProxyTemplateConfig.spec.watchedResourcesIgnoreFields: the typed
// view of a resource must agree with the watcher's field filter,
// otherwise templates would compile against a field that's reliably
// zero at render time.
//
// The pgo-relevant case in production is `metadata.managedFields` —
// every K8s object carries it but the chart never reads it, and
// stripping shaves a non-trivial chunk of memory. Without ignore-aware
// type generation, templates writing `.Metadata.ManagedFields` would
// compile and silently render empty. The test verifies that path now
// produces an undefined-field error at template compile time.
func TestConverter_IgnoreFields(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{typeObject},
			Properties: map[string]spec.Schema{
				"apiVersion": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
				"kind":       {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
				"metadata": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{typeObject},
					Properties: map[string]spec.Schema{
						"name":            {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
						"namespace":       {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
						"managedFields":   {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeArray}}},
						"resourceVersion": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
					},
				}},
			},
		},
	}

	c := NewConverter(nil)
	// Same shape the CRD ships with.
	c.IgnoreFields = []string{"metadata.managedFields", "metadata.resourceVersion"}

	got, err := c.Convert(schema)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, got.Kind())

	meta, ok := got.FieldByName("Metadata")
	require.True(t, ok)
	require.Equal(t, reflect.Struct, meta.Type.Kind())

	// The two ignore patterns must have removed exactly two fields.
	wantNames := []string{"Name", "Namespace"}
	gotNames := fieldNames(meta.Type)
	assert.ElementsMatch(t, wantNames, gotNames,
		"ignored properties must not appear on the generated struct")

	for _, stripped := range []string{"ManagedFields", "ResourceVersion"} {
		_, exists := meta.Type.FieldByName(stripped)
		assert.False(t, exists, "field %q must be stripped by IgnoreFields", stripped)
	}
}

// TestConverter_IgnoreFields_Roots verifies stripping a top-level
// property: `status` is the textbook case. The controller doesn't read
// status off watched resources during normal reconciliation — only the
// status applier writes it — so stripping it saves memory on every
// stored resource. After stripping the top-level field, templates
// reaching `.Status.…` fail at template compile time.
func TestConverter_IgnoreFields_Roots(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{typeObject},
			Properties: map[string]spec.Schema{
				"spec":   {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeObject}}},
				"status": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeObject}}},
			},
		},
	}
	c := NewConverter(nil)
	c.IgnoreFields = []string{"status"}
	got, err := c.Convert(schema)
	require.NoError(t, err)
	_, ok := got.FieldByName("Status")
	assert.False(t, ok, "top-level Status must be stripped")
	_, ok = got.FieldByName("Spec")
	assert.True(t, ok, "non-ignored peers must remain")
}

// TestConverter_IgnoreFields_BracketsIgnored covers the documented
// no-op: bracketed / indexed patterns (e.g.
// `metadata.annotations['foo']`, `spec.rules[0].host`) target VALUES
// inside a runtime container, not struct fields. They keep working in
// the watcher's field filter at storage time but they don't change the
// generated type's shape — the Annotations field is still
// `map[string]string`, Rules is still `[]Rule`. Templates that point
// at the same paths still compile against the typed shape; the runtime
// value just happens to be smaller.
func TestConverter_IgnoreFields_BracketsIgnored(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{typeObject},
			Properties: map[string]spec.Schema{
				"metadata": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{typeObject},
					Properties: map[string]spec.Schema{
						"annotations": {SchemaProps: spec.SchemaProps{
							Type: spec.StringOrArray{typeObject},
							AdditionalProperties: &spec.SchemaOrBool{
								Schema: &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
							},
						}},
					},
				}},
			},
		},
	}
	c := NewConverter(nil)
	c.IgnoreFields = []string{
		`metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']`,
		"spec.rules[0].host",
	}
	got, err := c.Convert(schema)
	require.NoError(t, err)
	meta, ok := got.FieldByName("Metadata")
	require.True(t, ok)
	anns, ok := meta.Type.FieldByName("Annotations")
	require.True(t, ok,
		"bracketed patterns must not strip the containing struct field")
	assert.Equal(t, reflect.Map, anns.Type.Kind(),
		"Annotations stays as map[string]string; runtime filter handles the per-key strip")
}

// TestConverter_IgnoreFields_StripAll covers the edge case where every
// property of a subtree is stripped. Emitting an empty struct would be
// technically correct but useless — Scriggo can't dot-access anything
// on a fieldless struct. Degrading to `any` matches the
// preserve-unknown case: templates can still reach the subtree via
// dig() at render time if they need to, with no compile error.
func TestConverter_IgnoreFields_StripAll(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{typeObject},
			Properties: map[string]spec.Schema{
				"nested": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{typeObject},
					Properties: map[string]spec.Schema{
						"a": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
						"b": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
					},
				}},
			},
		},
	}
	c := NewConverter(nil)
	c.IgnoreFields = []string{"nested.a", "nested.b"}
	got, err := c.Convert(schema)
	require.NoError(t, err)
	nested, _ := got.FieldByName("Nested")
	assert.Equal(t, reflect.Interface, nested.Type.Kind(),
		"a subtree whose every property is stripped degrades to any so dig() still works")
}

// TestConverter_IgnoreFields_SharedRef documents the deliberate
// behaviour of IgnoreFields on a $ref-shared schema: stripping a field
// from one occurrence's path also strips it from every other occurrence
// of the same ref. The typeCache is keyed on the ref string for type
// identity reasons (Scriggo uses pointer identity in places); per-
// occurrence stripping would require duplicating the type per use site.
//
// In practice this is exactly what HAPTIC operators want: the only
// thing they ever strip via IgnoreFields is `metadata.managedFields`,
// and they want it gone on EVERY resource. We document the behaviour
// here so any future "I expected per-resource stripping" surprise
// has a test pinning the trade-off.
func TestConverter_IgnoreFields_SharedRef(t *testing.T) {
	components := map[string]spec.Schema{
		"ObjectMeta": {
			SchemaProps: spec.SchemaProps{
				Type: spec.StringOrArray{typeObject},
				Properties: map[string]spec.Schema{
					"name":          {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeString}}},
					"managedFields": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{typeArray}}},
				},
			},
		},
	}
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{typeObject},
			Properties: map[string]spec.Schema{
				"first":  {SchemaProps: spec.SchemaProps{Ref: mustRef(t, "#/components/schemas/ObjectMeta")}},
				"second": {SchemaProps: spec.SchemaProps{Ref: mustRef(t, "#/components/schemas/ObjectMeta")}},
			},
		},
	}
	c := NewConverter(components)
	// Strip via the path-shape that the first occurrence would
	// expose. The cache hits on the second occurrence, so the shared
	// type already has ManagedFields removed.
	c.IgnoreFields = []string{"first.managedFields"}
	got, err := c.Convert(schema)
	require.NoError(t, err)
	first, _ := got.FieldByName("First")
	_, ok := first.Type.FieldByName("ManagedFields")
	assert.False(t, ok, "first.ManagedFields must be stripped by the first.managedFields pattern")
	// Both refs resolve to the same cached type — the stripping
	// applied to first also applies to second by design.
	second, _ := got.FieldByName("Second")
	assert.Same(t, first.Type, second.Type,
		"shared $ref must yield the same reflect.Type instance regardless of stripping")
}

func fieldNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}

// TestParseDottedJSONPath pins the contract between IgnoreFields
// patterns and type-level stripping. The path comes back joined with
// dots so the converter can compare it byte-for-byte against the
// current iteration path; ok=false signals a pattern the converter
// can't even consider for stripping (array index, filter, wildcard,
// recursive descent, parse error).
//
// Sharing the client-go JSONPath parser with pkg/k8s/indexer means
// typegen and the runtime field filter agree on syntax. K8s JSONPath
// deliberately treats `obj['key']` as a synonym for `obj.key`
// (parseArray dispatches quoted dict keys through the field-node
// path), so a bracketed map-key pattern parses to the same segment
// list as the dotted equivalent. That looks like a loss of
// information until you remember WHERE the converter does its
// disambiguation: it only checks ignoreSet during property iteration
// in convertObject, and it doesn't iterate into a map's value space.
// So `metadata.annotations['k']` lands in ignoreSet as
// `metadata.annotations.k`, never matches any iteration, and the
// generated map type stays intact. Whole-property strips like
// `metadata.managedFields` do match a real iteration and strip the
// field. The schema walk is the source of truth, not the syntax form.
func TestParseDottedJSONPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		path string
		ok   bool
	}{
		{"plain dotted path", "metadata.managedFields", "metadata.managedFields", true},
		{"top-level field", "status", "status", true},
		{"leading dot tolerated", ".metadata.name", "metadata.name", true},
		// Dict-key brackets flatten to a FieldNode chain — same shape
		// as the dotted form. Disambiguation is the converter's job;
		// see the doc comment above.
		{"dict-key bracket", `metadata.annotations['k']`, "metadata.annotations.k", true},
		// Numeric indices come back as ArrayNode and disqualify the
		// pattern from type-level stripping (the type identity of
		// `Listeners []Listener` can't drop a specific index).
		{"array index", "spec.rules[0].host", "", false},
		{"double dot recursive", "metadata..name", "", false},
		{"wildcard", "spec.*", "", false},
		{"empty input", "", "", false},
		{"unparseable input", "metadata.[bad", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, ok := parseDottedJSONPath(tt.in)
			assert.Equal(t, tt.ok, ok, "ok flag")
			assert.Equal(t, tt.path, path, "joined path")
		})
	}
}
