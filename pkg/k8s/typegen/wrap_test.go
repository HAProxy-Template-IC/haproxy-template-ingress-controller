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

// TestWrapInto_RoundTrip is the load-bearing test for the map→typed
// conversion: a realistic K8s-shaped unstructured object goes through
// the converter and the wrapper, and all fields land in the right
// places. If this breaks, the whole Tier-2 chain is broken — Scriggo
// templates would see zero values for every field.
func TestWrapInto_RoundTrip(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"apiVersion": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"kind":       {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"metadata": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{"object"},
					Properties: map[string]spec.Schema{
						"name":       {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
						"namespace":  {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
						"generation": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"integer"}}},
						"labels": {SchemaProps: spec.SchemaProps{
							Type: spec.StringOrArray{"object"},
							AdditionalProperties: &spec.SchemaOrBool{
								Schema: &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
							},
						}},
					},
				}},
			},
		},
	}

	typ, err := NewConverter(nil).Convert(schema)
	require.NoError(t, err)

	// The exact shape an unstructured.Unstructured.UnstructuredContent()
	// would yield for a Gateway-with-labels resource. Integer fields
	// come through as int64 in K8s' decoder, which is what we use too.
	obj := map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":       "public",
			"namespace":  "ingress",
			"generation": int64(7),
			"labels": map[string]any{
				"team": "platform",
				"tier": "prod",
			},
		},
	}

	v, err := WrapInto(obj, typ)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, v.Kind())

	assert.Equal(t, "gateway.networking.k8s.io/v1", v.FieldByName("ApiVersion").String())
	assert.Equal(t, "Gateway", v.FieldByName("Kind").String())
	meta := v.FieldByName("Metadata")
	require.Equal(t, reflect.Struct, meta.Kind())
	assert.Equal(t, "public", meta.FieldByName("Name").String())
	assert.Equal(t, "ingress", meta.FieldByName("Namespace").String())
	// Generation is *int64 under the issue #52 tristate rules — optional
	// numeric / bool fields get pointer-wrapped so json.Unmarshal can
	// distinguish "absent" (nil pointer) from "explicit zero" (non-nil
	// pointer to 0). The chart never observes the pointer because
	// digStructField dereferences before returning.
	genPtr := meta.FieldByName("Generation")
	require.Equal(t, reflect.Pointer, genPtr.Kind())
	require.False(t, genPtr.IsNil(), "generation present in fixture must round-trip non-nil")
	assert.Equal(t, int64(7), genPtr.Elem().Int())
	labels := meta.FieldByName("Labels")
	require.Equal(t, reflect.Map, labels.Kind())
	assert.Equal(t, "platform", labels.MapIndex(reflect.ValueOf("team")).String())
	assert.Equal(t, "prod", labels.MapIndex(reflect.ValueOf("tier")).String())
}

// TestWrapInto_MissingKeys covers the realistic case where the
// unstructured object doesn't carry every property the schema declares
// (a Gateway with no status, an Ingress with no defaultBackend, ...).
// Missing keys should leave the field at its zero value, matching what
// encoding/json does for any other Go struct. The chart side already
// handles zero values via dig() / digstr() / direct ==-checks, so this
// keeps the contract.
func TestWrapInto_MissingKeys(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"present":   {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"absent":    {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"absentInt": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"integer"}}},
			},
		},
	}
	typ, err := NewConverter(nil).Convert(schema)
	require.NoError(t, err)

	v, err := WrapInto(map[string]any{"present": "yes"}, typ)
	require.NoError(t, err)
	assert.Equal(t, "yes", v.FieldByName("Present").String())
	assert.Equal(t, "", v.FieldByName("Absent").String())
	// AbsentInt is *int64 (issue #52 tristate). Missing key must
	// round-trip as a nil pointer — this is the whole point of the
	// pointer wrapping: it distinguishes "absent" (nil) from
	// "explicit zero" (non-nil pointer to 0).
	absentInt := v.FieldByName("AbsentInt")
	require.Equal(t, reflect.Pointer, absentInt.Kind())
	assert.True(t, absentInt.IsNil(), "missing optional int field must round-trip as nil pointer, not zero-value pointer")
}

// TestWrapInto_PreserveUnknownPassThrough is the round-trip equivalent
// of TestConverter_PreserveUnknownFields: schemas with the
// preserve-unknown extension produce `any` fields, and WrapInto must
// pass the raw map through to that field unchanged so dig() can still
// navigate at render time.
func TestWrapInto_PreserveUnknownPassThrough(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"spec": {
					VendorExtensible: spec.VendorExtensible{Extensions: spec.Extensions{
						preserveUnknownExt: true,
					}},
					SchemaProps: spec.SchemaProps{
						Type:       spec.StringOrArray{"object"},
						Properties: map[string]spec.Schema{},
					},
				},
			},
		},
	}
	typ, err := NewConverter(nil).Convert(schema)
	require.NoError(t, err)

	obj := map[string]any{
		"spec": map[string]any{
			"anything": "goes",
			"nested":   map[string]any{"deeply": []any{"a", "b"}},
		},
	}
	v, err := WrapInto(obj, typ)
	require.NoError(t, err)
	specField := v.FieldByName("Spec")
	require.Equal(t, reflect.Interface, specField.Kind())
	asMap, ok := specField.Interface().(map[string]any)
	require.True(t, ok, "preserve-unknown subtree must pass through as map[string]any")
	assert.Equal(t, "goes", asMap["anything"])
}

func TestWrapImmutableIntoMatchesJSONProjection(t *testing.T) {
	itemType := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeFor[string](), Tag: `json:"name"`},
		{Name: "Port", Type: reflect.TypeFor[int64](), Tag: `json:"port"`},
	})
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeFor[string](), Tag: `json:"name"`},
		{Name: "Count", Type: reflect.TypeFor[*int64](), Tag: `json:"count,omitempty"`},
		{Name: "Labels", Type: reflect.TypeFor[map[string]string](), Tag: `json:"labels"`},
		{Name: "Items", Type: reflect.SliceOf(itemType), Tag: `json:"items"`},
		{Name: "Opaque", Type: reflect.TypeFor[any](), Tag: `json:"opaque"`},
	})
	registerImmutableProjectionType(typ)
	source := map[string]any{
		"name":   "route",
		"count":  int64(3),
		"labels": map[string]any{"team": "edge"},
		"items":  []any{map[string]any{"name": "backend", "port": int64(8080)}},
		"opaque": map[string]any{"number": int64(7), "nested": []any{"value"}},
	}

	want, err := WrapInto(source, typ)
	require.NoError(t, err)
	got, err := WrapImmutableInto(source, typ)
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(want.Interface(), got.Interface()))

	source["labels"].(map[string]any)["team"] = "poison"
	source["opaque"].(map[string]any)["nested"].([]any)[0] = "poison"
	assert.Equal(t, "edge", got.FieldByName("Labels").MapIndex(reflect.ValueOf("team")).String())
	assert.Equal(t, "value", got.FieldByName("Opaque").Interface().(map[string]any)["nested"].([]any)[0])
}

func TestWrapImmutableIntoFallsBackForNonCanonicalInput(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Labels", Type: reflect.TypeFor[map[string]string](), Tag: `json:"labels"`},
	})
	registerImmutableProjectionType(typ)
	source := map[string]any{"labels": map[string]string{"team": "edge"}}
	want, err := WrapInto(source, typ)
	require.NoError(t, err)
	got, err := WrapImmutableInto(source, typ)
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(want.Interface(), got.Interface()))
}

func TestWrapImmutableIntoPointerOwnsGeneratedAndFallbackValues(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeFor[string](), Tag: `json:"name"`},
		{Name: "Labels", Type: reflect.TypeFor[map[string]string](), Tag: `json:"labels"`},
		{Name: "Opaque", Type: reflect.TypeFor[any](), Tag: `json:"opaque"`},
	})
	registerImmutableProjectionType(typ)
	canonical := map[string]any{
		"name":   "arbitrary-crd",
		"labels": map[string]any{"team": "edge"},
		"opaque": map[string]any{"nested": []any{"value"}},
	}
	want, err := WrapInto(canonical, typ)
	require.NoError(t, err)
	pointer, err := WrapImmutableIntoPointer(canonical, typ)
	require.NoError(t, err)
	require.Equal(t, reflect.PointerTo(typ), pointer.Type())
	assert.True(t, reflect.DeepEqual(want.Interface(), pointer.Elem().Interface()))

	canonical["labels"].(map[string]any)["team"] = "source-poison"
	canonical["opaque"].(map[string]any)["nested"].([]any)[0] = "source-poison"
	assert.Equal(t, "edge", pointer.Elem().FieldByName("Labels").MapIndex(reflect.ValueOf("team")).String())
	assert.Equal(t, "value", pointer.Elem().FieldByName("Opaque").Interface().(map[string]any)["nested"].([]any)[0])

	fallback := map[string]any{
		"name":   "fallback",
		"labels": map[string]string{"team": "edge"},
		"opaque": map[string]any{"nested": []any{"value"}},
	}
	pointer, err = WrapImmutableIntoPointer(fallback, typ)
	require.NoError(t, err)
	fallback["labels"].(map[string]string)["team"] = "source-poison"
	assert.Equal(t, "edge", pointer.Elem().FieldByName("Labels").MapIndex(reflect.ValueOf("team")).String())

	invalid, err := WrapImmutableIntoPointer(map[string]any{}, nil)
	require.Error(t, err)
	assert.False(t, invalid.IsValid())
}

func BenchmarkWrapImmutableInto(b *testing.B) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeFor[string](), Tag: `json:"name"`},
		{Name: "Count", Type: reflect.TypeFor[*int64](), Tag: `json:"count"`},
		{Name: "Labels", Type: reflect.TypeFor[map[string]string](), Tag: `json:"labels"`},
		{Name: "Values", Type: reflect.TypeFor[[]string](), Tag: `json:"values"`},
	})
	source := map[string]any{
		"name": "route", "count": int64(3),
		"labels": map[string]any{"team": "edge"},
		"values": []any{"one", "two", "three"},
	}
	registerImmutableProjectionType(typ)
	b.ReportAllocs()
	for range b.N {
		if _, err := WrapImmutableInto(source, typ); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrapImmutableIntoPointer(b *testing.B) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeFor[string](), Tag: `json:"name"`},
		{Name: "Count", Type: reflect.TypeFor[*int64](), Tag: `json:"count"`},
		{Name: "Labels", Type: reflect.TypeFor[map[string]string](), Tag: `json:"labels"`},
		{Name: "Values", Type: reflect.TypeFor[[]string](), Tag: `json:"values"`},
	})
	source := map[string]any{
		"name": "route", "count": int64(3),
		"labels": map[string]any{"team": "edge"},
		"values": []any{"one", "two", "three"},
	}
	registerImmutableProjectionType(typ)
	b.ReportAllocs()
	for range b.N {
		if _, err := WrapImmutableIntoPointer(source, typ); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrapInto(b *testing.B) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeFor[string](), Tag: `json:"name"`},
		{Name: "Count", Type: reflect.TypeFor[*int64](), Tag: `json:"count"`},
		{Name: "Labels", Type: reflect.TypeFor[map[string]string](), Tag: `json:"labels"`},
		{Name: "Values", Type: reflect.TypeFor[[]string](), Tag: `json:"values"`},
	})
	source := map[string]any{
		"name": "route", "count": int64(3),
		"labels": map[string]any{"team": "edge"},
		"values": []any{"one", "two", "three"},
	}
	b.ReportAllocs()
	for range b.N {
		if _, err := WrapInto(source, typ); err != nil {
			b.Fatal(err)
		}
	}
}
