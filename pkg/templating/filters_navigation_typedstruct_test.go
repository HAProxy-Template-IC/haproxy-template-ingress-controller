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

// Tight reproducer for the chart-side conversion failure that blocks
// the typed-watched-resources migration.
//
// Background: when a gateway-library snippet was converted from
//
//	{%- for _, gw := range resources.gateways.List() %}
//	  ...
//	  {%- for _, l := range dig(gw, "spec", "listeners") | toSlice() %}
//
// to
//
//	{%- for _, gw := range gateways %}  // typed *[]*Gateway
//	  ...
//	  {%- for _, l := range dig(gw, "spec", "listeners") | toSlice() %}
//
// 13 chart tests broke — files that should have been generated inside
// the inner loop didn't get generated. The conversion's only change
// was the iteration source (untyped store -> typed top-level global);
// the inner dig() / toSlice() / helper-macro calls were unchanged.
//
// The dig-on-typed-struct contract in `digReflect` is supposed to
// make that drop-in conversion safe: same key strings, same return
// semantics, regardless of whether the input is map[string]any or a
// typegen-produced typed struct. The chart-side CLAUDE.md and the
// digReflect doc both state this contract.
//
// This file pins each rung of the contract against a typegen-derived
// type built from a synthetic schema that mirrors the Gateway shape
// the chart actually iterates (metadata + spec.listeners). If a rung
// breaks, the contract is broken and the chart conversion will fail
// until the rung is fixed.

package templating

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
)

// wrapSliceForTest converts []map[string]any items into a typed
// []*T reflect.Value the same way production does (rendercontext
// wraps each item through typegen.WrapInto).
func wrapSliceForTest(t *testing.T, items []any, elem reflect.Type) reflect.Value {
	t.Helper()
	out := reflect.MakeSlice(reflect.SliceOf(reflect.PointerTo(elem)), 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		require.True(t, ok, "test items must be map[string]any")
		v, err := typegen.WrapInto(m, elem)
		require.NoError(t, err, "typegen.WrapInto must succeed for the sample data")
		ptr := reflect.New(elem)
		ptr.Elem().Set(v)
		out = reflect.Append(out, ptr)
	}
	return out
}

// buildGatewayShapeType produces a reflect.Type that mirrors the
// minimal Gateway shape the failing chart snippet navigates. Used
// across the subtests so each rung is testing against the same type
// (not a different hand-rolled struct per case).
func buildGatewayShapeType(t *testing.T) reflect.Type {
	t.Helper()
	stringType := spec.StringProperty()
	intType := spec.Int64Property()
	listenerSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"name":     *stringType,
				"port":     *intType,
				"protocol": *stringType,
			},
		},
	}
	metaSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"name":      *stringType,
				"namespace": *stringType,
			},
		},
	}
	specSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"listeners": {
					SchemaProps: spec.SchemaProps{
						Type: spec.StringOrArray{"array"},
						Items: &spec.SchemaOrArray{
							Schema: &listenerSchema,
						},
					},
				},
			},
		},
	}
	gatewaySchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"metadata": metaSchema,
				"spec":     specSchema,
			},
		},
	}
	converter := typegen.NewConverter(nil)
	gwType, err := converter.Convert(&gatewaySchema)
	require.NoError(t, err, "typegen.Convert must succeed for the synthetic Gateway schema")
	return gwType
}

// buildSampleGateways constructs two *Gateway-shape instances populated
// with the fields the chart snippet reads. wrapSliceForTest produces the
// *[]*T shape the chart's typed top-level global uses.
func buildSampleGateways(t *testing.T, gwType reflect.Type) reflect.Value {
	t.Helper()
	items := []any{
		map[string]any{
			"metadata": map[string]any{"name": "edge", "namespace": "ns1"},
			"spec": map[string]any{
				"listeners": []any{
					map[string]any{"name": "https-default", "port": int64(443), "protocol": "HTTPS"},
					map[string]any{"name": "https-perport", "port": int64(8443), "protocol": "HTTPS"},
				},
			},
		},
		map[string]any{
			"metadata": map[string]any{"name": "internal", "namespace": "ns2"},
			"spec": map[string]any{
				"listeners": []any{
					map[string]any{"name": "http", "port": int64(80), "protocol": "HTTP"},
				},
			},
		},
	}
	wrapped := wrapSliceForTest(t, items, gwType)
	require.Equal(t, 2, wrapped.Len(), "wrapped slice must contain both sample gateways")
	return wrapped
}

// Rung 1: top-level field access. The smoke snippet already exercises
// this (`gw.Metadata.Namespace`); a regression here would break the
// smoke too, so this rung is primarily for completeness.
func TestDigContract_TypedPointer_TopLevelMetadata(t *testing.T) {
	gwType := buildGatewayShapeType(t)
	gateways := buildSampleGateways(t, gwType)

	gw := gateways.Index(0).Interface() // *Gateway

	gotNs := scriggoDig(gw, "metadata", "namespace")
	assert.Equal(t, "ns1", gotNs,
		"dig(*Gateway, 'metadata', 'namespace') must traverse the typed pointer and return the namespace string")

	gotName := scriggoDig(gw, "metadata", "name")
	assert.Equal(t, "edge", gotName,
		"dig(*Gateway, 'metadata', 'name') must traverse the typed pointer and return the name string")
}

// Rung 2: nested object access — `spec`. Returns the inner Spec
// struct as `any`. Chart code rarely treats this as a terminal value;
// the failing snippet uses it as an intermediate step to reach
// `spec.listeners`.
func TestDigContract_TypedPointer_NestedSpecReturnsStruct(t *testing.T) {
	gwType := buildGatewayShapeType(t)
	gateways := buildSampleGateways(t, gwType)

	gw := gateways.Index(0).Interface() // *Gateway

	specVal := scriggoDig(gw, "spec")
	require.NotNil(t, specVal, "dig(*Gateway, 'spec') must not return nil")

	// The result is the typegen-produced Spec struct value (not a
	// pointer — typegen embeds nested objects by value). dig should
	// further navigate it via JSON tags.
	innerListeners := scriggoDig(specVal, "listeners")
	require.NotNil(t, innerListeners,
		"dig(specStruct, 'listeners') must continue navigation into the nested struct")
}

// Rung 3: slice field access. This is the rung the failing chart
// snippet hits — it does `dig(gw, "spec", "listeners")` and expects a
// rangeable slice back. The chart pipes the result through `toSlice()`
// to convert to []any. If this rung breaks, no chart code can range
// over a typed-pointer's slice field.
func TestDigContract_TypedPointer_SliceField(t *testing.T) {
	gwType := buildGatewayShapeType(t)
	gateways := buildSampleGateways(t, gwType)

	gw := gateways.Index(0).Interface() // *Gateway

	listeners := scriggoDig(gw, "spec", "listeners")
	require.NotNil(t, listeners,
		"dig(*Gateway, 'spec', 'listeners') must not return nil; this is the rung the failing chart snippet hits")

	t.Run("toSlice converts the dig result to []any", func(t *testing.T) {
		converted, ok := toSlice(listeners)
		require.True(t, ok,
			"toSlice must accept the dig result; without this the chart's `dig(...) | toSlice()` pattern is broken for typed pointers")
		require.Equal(t, 2, len(converted),
			"toSlice must preserve the two listeners from the sample data")
	})

	t.Run("each listener is itself dig-navigable", func(t *testing.T) {
		converted, _ := toSlice(listeners)
		require.Equal(t, 2, len(converted))

		// Each element is the typegen Listener struct (value, not pointer,
		// because typegen.Convert produces struct fields by value for
		// array items unless the schema says otherwise).
		gotName := scriggoDig(converted[0], "name")
		assert.Equal(t, "https-default", gotName,
			"dig on the first listener must return its name; without this the chart's per-listener logic breaks")

		gotProto := scriggoDig(converted[0], "protocol")
		assert.Equal(t, "HTTPS", gotProto,
			"dig on the first listener must return its protocol")
	})
}

// Rung 3b: nested-struct-then-slice access — `listener.tls.certificateRefs`.
// This is the path the chart's ResolveCertRefKey macro takes:
//
//	dig(listener, "tls", "certificateRefs") | toSlice()
//	... then dig(certRefs[0], "namespace") / dig(certRefs[0], "name")
//
// The pre-existing Rung 3 only covers a top-level slice
// (`spec.listeners`); the failing chart conversion goes one rung deeper
// (typed listener -> typed Tls struct (value) -> []Ref slice -> Ref
// struct -> string fields). If this rung breaks, the 4 HTTPS chart
// tests that fail after the typed-iteration conversion of
// 16-crtlist-per-listener.yaml are explained — the cert-list file
// emission gate depends on this exact chain returning a non-empty key.
func TestDigContract_TypedPointer_NestedTLSCertificateRefs(t *testing.T) {
	stringType := spec.StringProperty()
	intType := spec.Int64Property()

	certRefSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"namespace": *stringType,
				"name":      *stringType,
				"group":     *stringType,
				"kind":      *stringType,
			},
		},
	}
	tlsSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"mode": *stringType,
				"certificateRefs": {
					SchemaProps: spec.SchemaProps{
						Type:  spec.StringOrArray{"array"},
						Items: &spec.SchemaOrArray{Schema: &certRefSchema},
					},
				},
			},
		},
	}
	listenerSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"name":     *stringType,
				"port":     *intType,
				"protocol": *stringType,
				"hostname": *stringType,
				"tls":      tlsSchema,
			},
		},
	}
	specSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"listeners": {
					SchemaProps: spec.SchemaProps{
						Type:  spec.StringOrArray{"array"},
						Items: &spec.SchemaOrArray{Schema: &listenerSchema},
					},
				},
			},
		},
	}
	gatewaySchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"metadata": {SchemaProps: spec.SchemaProps{
					Type: spec.StringOrArray{"object"},
					Properties: map[string]spec.Schema{
						"name":      *stringType,
						"namespace": *stringType,
					},
				}},
				"spec": specSchema,
			},
		},
	}
	converter := typegen.NewConverter(nil)
	gwType, err := converter.Convert(&gatewaySchema)
	require.NoError(t, err, "typegen must convert the TLS-aware Gateway schema")

	items := []any{
		map[string]any{
			"metadata": map[string]any{"name": "valid-cert-gw", "namespace": "default"},
			"spec": map[string]any{
				"listeners": []any{
					map[string]any{
						"name":     "https",
						"port":     int64(443),
						"protocol": "HTTPS",
						"tls": map[string]any{
							"mode": "Terminate",
							"certificateRefs": []any{
								map[string]any{
									"group": "",
									"kind":  "Secret",
									"name":  "wildcard-tls",
								},
							},
						},
					},
				},
			},
		},
	}
	wrapped := wrapSliceForTest(t, items, gwType)

	gw := wrapped.Index(0).Interface()

	listeners := scriggoDig(gw, "spec", "listeners")
	listenersSlice, ok := toSlice(listeners)
	require.True(t, ok, "spec.listeners must be slice-convertible")
	require.Equal(t, 1, len(listenersSlice), "one HTTPS listener in the sample")

	listener := listenersSlice[0]

	// The exact dig chain ResolveCertRefKey uses.
	certRefs := scriggoDig(listener, "tls", "certificateRefs")
	require.NotNil(t, certRefs,
		"dig(listener, 'tls', 'certificateRefs') must not return nil on a typed listener with cert refs; "+
			"if this fails, ResolveCertRefKey() returns '' for the typed listener and the chart's "+
			"per-listener crt-list file emission is silently skipped (4 HTTPS chart tests fail)")

	certRefsSlice, ok := toSlice(certRefs)
	require.True(t, ok,
		"toSlice on dig result must succeed; the chart pipes `dig(...) | toSlice()` and ranges the result")
	require.Equal(t, 1, len(certRefsSlice),
		"one cert ref in the sample listener")

	// And the per-ref dig that builds the lookup key.
	certRef := certRefsSlice[0]
	gotName := scriggoDig(certRef, "name")
	assert.Equal(t, "wildcard-tls", gotName,
		"dig(certRef, 'name') must return the secret name; this builds the certBySecret lookup key")

	// Rung 3c: absent-optional-field normalisation. The fixture above
	// OMITS `namespace` from the cert ref (the Gateway-API default
	// "same namespace as parent" semantic). On an untyped map, dig
	// returns nil for the missing key and `| fallback(defaultNs)`
	// substitutes the parent namespace; on a typed struct, the field
	// is the zero value `""` and naïve fallback skips. To keep the
	// chart's untyped→typed migration mechanical, dig MUST normalise
	// zero values of optional (`,omitempty`) fields back to nil so
	// fallback fires the same way it does for the untyped shape.
	// Anchored to the 4 HTTPS chart tests that fail when this contract
	// breaks (test-gateway-https-listener-with-valid-cert-binds and 3
	// siblings) — `ResolveCertRefKey` constructs an empty certKey of
	// "/<name>" instead of "<gwNs>/<name>", the certBySecret lookup
	// misses, the per-listener crt-list file isn't emitted, and the
	// bind line points at a file HAProxy can't open.
	t.Run("absent optional field normalised to nil", func(t *testing.T) {
		gotNs := scriggoDig(certRef, "namespace")
		require.Nil(t, gotNs,
			"dig(certRef, 'namespace') must return nil when the cert ref omits namespace; "+
				"if this returns the empty string, every `dig | fallback` chain in the chart silently fails "+
				"on optional string fields and the typed-watched-resources migration cannot be mechanical")
	})
}

// Rung 4: cross-shape equivalence — the dig contract claims the chart
// code is identical whether the input is the untyped map shape (the
// pre-conversion path) or the typed pointer shape (the post-conversion
// path). This test pins that invariant directly: same key strings,
// same return values.
func TestDigContract_TypedPointerMatchesUntypedMap(t *testing.T) {
	gwType := buildGatewayShapeType(t)
	gateways := buildSampleGateways(t, gwType)

	typedGW := gateways.Index(0).Interface() // *Gateway

	// Equivalent untyped shape — what `resources.gateways.List()`
	// returns. dig handles this via the map[string]any fast path.
	untypedGW := map[string]any{
		"metadata": map[string]any{"name": "edge", "namespace": "ns1"},
		"spec": map[string]any{
			"listeners": []any{
				map[string]any{"name": "https-default", "port": int64(443), "protocol": "HTTPS"},
				map[string]any{"name": "https-perport", "port": int64(8443), "protocol": "HTTPS"},
			},
		},
	}

	cases := []struct {
		name string
		keys []string
	}{
		{"metadata.namespace", []string{"metadata", "namespace"}},
		{"metadata.name", []string{"metadata", "name"}},
		{"spec.listeners (intermediate, both should be rangeable)", []string{"spec", "listeners"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fromTyped := scriggoDig(typedGW, tc.keys...)
			fromUntyped := scriggoDig(untypedGW, tc.keys...)

			// Terminal-string keys can be compared directly. The
			// slice case (spec.listeners) needs the toSlice
			// conversion both sides — typed yields a typegen []T,
			// untyped yields []any, and the equivalence claim is
			// "both are rangeable after toSlice()" not "both are
			// the same Go value."
			if _, isSlice := fromUntyped.([]any); isSlice {
				typedConv, ok := toSlice(fromTyped)
				require.True(t, ok, "typed result must convert to []any via toSlice")
				untypedConv, ok := toSlice(fromUntyped)
				require.True(t, ok, "untyped result must convert to []any via toSlice")
				assert.Equal(t, len(untypedConv), len(typedConv),
					"typed and untyped dig must agree on slice length for %q", tc.name)
				return
			}

			assert.Equal(t, fromUntyped, fromTyped,
				"dig must return the same value for the typed and untyped shapes for keys %v", tc.keys)
		})
	}
}
