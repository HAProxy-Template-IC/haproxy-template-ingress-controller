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

// Each test below builds a [spec.Schema] by hand rather than parsing
// JSON. That keeps the test inputs self-documenting and free of the
// kube-openapi JSON-parser's quirks (it treats some omitted fields as
// nil pointers and others as zero values, which obscures intent).

func TestConverter_Scalars(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want reflect.Kind
	}{
		{name: "string", typ: "string", want: reflect.String},
		{name: "integer", typ: "integer", want: reflect.Int64},
		{name: "boolean", typ: "boolean", want: reflect.Bool},
		{name: "number", typ: "number", want: reflect.Float64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConverter(nil)
			got, err := c.Convert(&spec.Schema{
				SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{tt.typ}},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Kind())
		})
	}
}

// TestConverter_ObjectMeta is the load-bearing smoke test: building a
// scaled-down ObjectMeta and verifying the generated struct has fields
// that Scriggo will reach via `.Metadata.Name`-style paths. If this
// breaks, every typed-resource access in the chart breaks.
func TestConverter_ObjectMeta(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			// `name` is marked required → tag emitted without omitempty.
			// The other three properties are optional → tags carry
			// omitempty so digStructField normalises zero values back
			// to nil (matching the untyped-map "missing key = nil"
			// semantic the chart relies on).
			Required: []string{"name"},
			Properties: map[string]spec.Schema{
				"name": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"namespace": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{"string"},
				}},
				"generation": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{"integer"},
				}},
				"labels": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{"object"},
					AdditionalProperties: &spec.SchemaOrBool{
						Schema: &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
					},
				}},
			},
		},
	}

	c := NewConverter(nil)
	got, err := c.Convert(schema)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, got.Kind())

	// Field order is deterministic (sorted) — exercise that too,
	// because the chart's reflection-based access doesn't care but
	// memoised consumers do.
	//
	// Generation is reflect.Pointer (Kind, not Int64) because optional
	// integer / bool / float fields get pointer-wrapped for the issue
	// #52 tristate fix: chart's `dig | fallback` couldn't distinguish
	// "missing" from "explicitly zero" without it (HTTPRouteWeight
	// conformance gating on backendRef.weight=0 being treated as
	// excluded, not defaulted to 1). digStructField dereferences these
	// pointers automatically so chart code keeps reading back plain
	// int64 / bool values via dig().
	wantFields := []struct {
		name string
		kind reflect.Kind
		// elemKind is non-zero when the field is pointer-wrapped —
		// asserts the value type behind the pointer.
		elemKind reflect.Kind
	}{
		{"Generation", reflect.Pointer, reflect.Int64},
		{"Labels", reflect.Map, 0},
		{"Name", reflect.String, 0},
		{"Namespace", reflect.String, 0},
	}
	require.Equal(t, len(wantFields), got.NumField())
	for i, wf := range wantFields {
		f := got.Field(i)
		assert.Equal(t, wf.name, f.Name, "field index %d", i)
		assert.Equal(t, wf.kind, f.Type.Kind(), "field %q kind", wf.name)
		if wf.elemKind != 0 {
			assert.Equal(t, wf.elemKind, f.Type.Elem().Kind(), "field %q elem kind", wf.name)
		}
	}

	// Required field → tag without omitempty. encoding/json still
	// reads/writes by name; the absence of omitempty is a marker
	// digStructField uses to decide whether to surface a zero value.
	nameField, _ := got.FieldByName("Name")
	assert.Equal(t, `json:"name"`, string(nameField.Tag))

	// Optional field → tag with omitempty so dig() normalises absent
	// values back to nil. Without this, `dig(obj, "namespace") |
	// fallback(parentNs)` returns "" (the zero value), fallback
	// doesn't fire, and downstream key composition silently produces
	// malformed strings like "/<name>" instead of "<parent>/<name>".
	nsField, _ := got.FieldByName("Namespace")
	assert.Equal(t, `json:"namespace,omitempty"`, string(nsField.Tag))
}

// TestConverter_FreeFormMap covers the two AdditionalProperties shapes
// K8s schemas actually emit: the bool-true permissive form (used for
// annotations, labels in some shape, and a few JSON-blob fields) and
// the schema-restricted form (used everywhere labels appear because
// label values are typed as string).
func TestConverter_FreeFormMap(t *testing.T) {
	t.Run("additionalProperties true → map[string]any", func(t *testing.T) {
		schema := &spec.Schema{
			SchemaProps: spec.SchemaProps{
				Type:                 spec.StringOrArray{"object"},
				AdditionalProperties: &spec.SchemaOrBool{Allows: true},
			},
		}
		got, err := NewConverter(nil).Convert(schema)
		require.NoError(t, err)
		require.Equal(t, reflect.Map, got.Kind())
		assert.Equal(t, reflect.String, got.Key().Kind())
		assert.Equal(t, reflect.Interface, got.Elem().Kind())
	})

	t.Run("additionalProperties schema → map[string]T", func(t *testing.T) {
		schema := &spec.Schema{
			SchemaProps: spec.SchemaProps{
				Type: spec.StringOrArray{"object"},
				AdditionalProperties: &spec.SchemaOrBool{
					Schema: &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				},
			},
		}
		got, err := NewConverter(nil).Convert(schema)
		require.NoError(t, err)
		require.Equal(t, reflect.Map, got.Kind())
		assert.Equal(t, reflect.String, got.Elem().Kind(),
			"map values must be strongly typed when schema.additionalProperties.schema is set")
	})
}

// TestConverter_PreserveUnknownFields keeps the regression that put
// digstr() on the map in the first place. Kubernetes uses this
// extension on RawExtension, on a handful of CRDSpec fields, and on
// status subresources whose shape is intentionally unconstrained.
// Templates must keep dig()'ing those — emitting any is the right call.
func TestConverter_PreserveUnknownFields(t *testing.T) {
	schema := &spec.Schema{
		VendorExtensible: spec.VendorExtensible{Extensions: spec.Extensions{
			preserveUnknownExt: true,
		}},
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"shouldNotAppear": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
			},
		},
	}
	got, err := NewConverter(nil).Convert(schema)
	require.NoError(t, err)
	assert.Equal(t, reflect.Interface, got.Kind(),
		"preserve-unknown-fields must collapse to any so dig() still navigates")
}

func TestConverter_Array(t *testing.T) {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"array"},
			Items: &spec.SchemaOrArray{
				Schema: &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
			},
		},
	}
	got, err := NewConverter(nil).Convert(schema)
	require.NoError(t, err)
	require.Equal(t, reflect.Slice, got.Kind())
	assert.Equal(t, reflect.String, got.Elem().Kind())

	// Array with no Items must still produce []any so the template's
	// range loop doesn't trip on a missing element type.
	bare := &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"array"}}}
	got, err = NewConverter(nil).Convert(bare)
	require.NoError(t, err)
	require.Equal(t, reflect.Slice, got.Kind())
	assert.Equal(t, reflect.Interface, got.Elem().Kind())
}

// TestConverter_Ref covers $ref resolution against a Components map.
// This is the integration point with kube-openapi: real K8s schemas
// almost never inline ObjectMeta — they reference it. Without working
// ref resolution, every typed resource degrades to a single root struct
// with `Metadata any`.
func TestConverter_Ref(t *testing.T) {
	components := map[string]spec.Schema{
		"io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
			SchemaProps: spec.SchemaProps{
				Type: spec.StringOrArray{"object"},
				Properties: map[string]spec.Schema{
					"name":      {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
					"namespace": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				},
			},
		},
	}

	root := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"metadata": {SchemaProps: spec.SchemaProps{
					Ref: mustRef(t, "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"),
				}},
			},
		},
	}

	c := NewConverter(components)
	got, err := c.Convert(root)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, got.Kind())
	metaField, ok := got.FieldByName("Metadata")
	require.True(t, ok)
	require.Equal(t, reflect.Struct, metaField.Type.Kind())
	// The two ObjectMeta fields must round-trip through ref resolution.
	require.Equal(t, 2, metaField.Type.NumField())
}

func TestConverter_Ref_Missing(t *testing.T) {
	c := NewConverter(map[string]spec.Schema{}) // empty components
	_, err := c.Convert(&spec.Schema{
		SchemaProps: spec.SchemaProps{Ref: mustRef(t, "#/components/schemas/io.k8s.does.not.Exist")},
	})
	// Missing refs are real bugs (the schema list is fetched from the
	// cluster's own OpenAPI v3 endpoint, so an unresolved ref means
	// the spec is internally inconsistent). Surface the error rather
	// than degrade to any.
	require.Error(t, err)
}

// TestConverter_RefCache exercises the memoisation path. ObjectMeta is
// shared by every K8s object; without caching, every Convert(top-level)
// would re-build the same struct. The test asserts not just that the
// types are equal, but that they're the *same* reflect.Type — Scriggo
// uses pointer identity in places, so silent duplication would surface
// later as confusing "type X is not assignable to type X" errors.
func TestConverter_RefCache(t *testing.T) {
	components := map[string]spec.Schema{
		"meta": {
			SchemaProps: spec.SchemaProps{
				Type: spec.StringOrArray{"object"},
				Properties: map[string]spec.Schema{
					"name": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				},
			},
		},
	}

	root := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"a": {SchemaProps: spec.SchemaProps{Ref: mustRef(t, "#/components/schemas/meta")}},
				"b": {SchemaProps: spec.SchemaProps{Ref: mustRef(t, "#/components/schemas/meta")}},
			},
		},
	}

	c := NewConverter(components)
	got, err := c.Convert(root)
	require.NoError(t, err)
	aField, _ := got.FieldByName("A")
	bField, _ := got.FieldByName("B")
	assert.Same(t, ptrFor(aField.Type), ptrFor(bField.Type),
		"two refs to the same component must return the same reflect.Type instance")
}

func TestConverter_DepthCap(t *testing.T) {
	// A schema whose object nesting exceeds the cap. We use inline
	// nesting rather than refs because refs cache at the first visit
	// and never actually recurse deeper than that single resolution.
	deep := &spec.Schema{
		SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"object"}, Properties: map[string]spec.Schema{}},
	}
	cur := deep
	for i := 0; i < 5; i++ {
		next := spec.Schema{
			SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"object"}, Properties: map[string]spec.Schema{}},
		}
		cur.Properties["nested"] = next
		// Re-fetch the inserted entry as a pointer so the next
		// iteration mutates the map's stored value (Go map values
		// aren't addressable; the assignment above clones).
		v := cur.Properties["nested"]
		cur = &v
		cur.Properties = map[string]spec.Schema{}
	}
	// Pull the chain back together. (Go's map-value cloning above
	// means we have to rebuild the parents — easier to construct the
	// chain top-down with the converter set to a very low MaxDepth.)
	shallow := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"inner": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{"object"},
					Properties: map[string]spec.Schema{
						"deeper": {SchemaProps: spec.SchemaProps{
							Type: spec.StringOrArray{"object"},
							Properties: map[string]spec.Schema{
								"deepest": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
							},
						}},
					},
				}},
			},
		},
	}
	c := &Converter{MaxDepth: 2, typeCache: map[string]reflect.Type{}}
	got, err := c.Convert(shallow)
	require.NoError(t, err)
	innerField, _ := got.FieldByName("Inner")
	require.Equal(t, reflect.Struct, innerField.Type.Kind())
	deeperField, _ := innerField.Type.FieldByName("Deeper")
	// Walk: root@depth0 → inner@depth1 → deeper@depth2 → deepest
	// would recurse at depth3 which triggers depth>MaxDepth(2) and
	// degrades that property to any. Deeper itself remains a struct
	// because the cap fires on the next recursion, not the current.
	require.Equal(t, reflect.Struct, deeperField.Type.Kind())
	deepestField, _ := deeperField.Type.FieldByName("Deepest")
	require.Equal(t, reflect.Interface, deepestField.Type.Kind(),
		"property past MaxDepth must degrade to any")
}

func TestGoFieldName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"name", "Name"},
		{"apiVersion", "ApiVersion"},
		{"namespace", "Namespace"},
		// Uppercase acronyms in the source are preserved as-is — only rune 0
		// is uppercased, every other letter is written unchanged. So
		// clusterIP → ClusterIP (not ClusterIp). Pins the convention the
		// typed-field table in charts/CLAUDE.md documents.
		{"clusterIP", "ClusterIP"},
		{"loadBalancerIP", "LoadBalancerIP"},
		// Hyphens / dots aren't normal in K8s JSON keys but show up
		// in annotation key parsing; we still tolerate them.
		{"my-field", "My_field"},
		{"prefix.something", "Prefix_something"},
		// Empty input shouldn't make reflect.StructOf panic.
		{"", "_"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, GoFieldName(tt.in))
		})
	}
}

// mustRef centralises the spec.Ref construction so tests don't drown in
// error-handling boilerplate. The Ref type wraps a JSON pointer parser
// that's documented as returning an error only on malformed input —
// hard-coded literal refs never trip it.
func mustRef(t *testing.T, s string) spec.Ref {
	t.Helper()
	r, err := spec.NewRef(s)
	require.NoError(t, err)
	return r
}

// ptrFor extracts a comparable identity for a reflect.Type so
// assert.Same can compare two types via their underlying pointer.
// reflect.Type is an interface; testify's Same checks the interface
// pointer equality which is exactly what we need.
func ptrFor(t reflect.Type) any {
	return t
}

// TestConverter_AllOfWithSingleRef pins the K8s aggregated
// OpenAPI v3 canonical pattern for shared-type references. The
// metadata property on every built-in resource takes this shape:
//
//	metadata:
//	  allOf:
//	  - $ref: "#/components/schemas/.../ObjectMeta"
//	  default: {}
//
// The converter must unwrap the single-element allOf and resolve
// the inner ref through the components map. Without this handler
// metadata collapses to interface{}, which surfaces as
// "gw.Metadata.Name undefined (type interface {} has no field)"
// at template-compile time — the exact bug Phase 11 fixes.
func TestConverter_AllOfWithSingleRef(t *testing.T) {
	components := map[string]spec.Schema{
		"ObjectMeta": {SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"name":      {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"namespace": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
			},
		}},
	}
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"metadata": {
					SchemaProps: spec.SchemaProps{
						AllOf: []spec.Schema{{
							SchemaProps: spec.SchemaProps{Ref: mustRef(t, "#/components/schemas/ObjectMeta")},
						}},
					},
				},
			},
		},
	}
	got, err := NewConverter(components).Convert(schema)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, got.Kind())
	meta, ok := got.FieldByName("Metadata")
	require.True(t, ok)
	require.Equal(t, reflect.Struct, meta.Type.Kind(),
		"allOf-with-ref must resolve to a typed sub-struct, NOT degrade to any")
	name, ok := meta.Type.FieldByName("Name")
	require.True(t, ok, "ObjectMeta.Name must reach through the allOf wrapper")
	assert.Equal(t, reflect.String, name.Type.Kind())
}

// TestConverter_GoFieldNameCollisionDegradesToAny pins the
// fail-open path for the case where two JSON property names
// collapse to the same Go field identifier under GoFieldName's
// capitalise-and-sanitise rule. reflect.StructOf would panic on
// the duplicate; the converter detects the collision and returns
// `any` for the whole object so callers can still render via
// dig() at runtime.
//
// Mainstream K8s schemas don't trigger this (property names are
// camelCase), but CRDs are user-authored — a malformed CRD
// shouldn't crash the controller at boot.
func TestConverter_GoFieldNameCollisionDegradesToAny(t *testing.T) {
	// "name" and "Name" both produce the Go identifier "Name".
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"name": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
				"Name": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
			},
		},
	}
	got, err := NewConverter(nil).Convert(schema)
	require.NoError(t, err, "collision must NOT bubble up as an error — the converter must degrade to any")
	assert.Equal(t, reflect.Interface, got.Kind(),
		"colliding object schema must degrade to any so dig() can still navigate at render time")
}
