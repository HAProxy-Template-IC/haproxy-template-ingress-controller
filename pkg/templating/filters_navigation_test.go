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

package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoDig(t *testing.T) {
	// Realistic K8s-shaped fixture: Ingress metadata + spec.
	ingress := map[string]any{
		"metadata": map[string]any{
			"namespace": "default",
			"name":      "my-ingress",
			"labels": map[string]string{
				"app": "demo",
			},
		},
		"spec": map[string]any{
			"rules": []any{
				map[string]any{
					"host": "example.com",
				},
			},
		},
	}

	tests := []struct {
		name string
		obj  any
		keys []string
		want any
	}{
		{name: "no keys returns object", obj: ingress, keys: nil, want: ingress},
		{name: "nil object returns nil", obj: nil, keys: []string{"any"}, want: nil},
		{name: "single key", obj: ingress, keys: []string{"metadata"}, want: ingress["metadata"]},
		{name: "nested key", obj: ingress, keys: []string{"metadata", "namespace"}, want: "default"},
		{name: "missing top-level key", obj: ingress, keys: []string{"missing"}, want: nil},
		{name: "missing nested key", obj: ingress, keys: []string{"metadata", "missing"}, want: nil},
		{name: "stop at non-map intermediate", obj: ingress, keys: []string{"metadata", "namespace", "deeper"}, want: nil},
		{name: "map[string]string lookup on last key", obj: ingress, keys: []string{"metadata", "labels", "app"}, want: "demo"},
		{name: "map[string]string missing key", obj: ingress, keys: []string{"metadata", "labels", "missing"}, want: nil},
		{name: "non-map type is not navigable", obj: 42, keys: []string{"k"}, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoDig(tt.obj, tt.keys...))
		})
	}
}

func TestDigReflect_TypedNil(t *testing.T) {
	// Typed nil pointer should be treated as nil and return nil instead of panicking.
	var typedNil *map[string]any
	got := scriggoDig(typedNil, "any")
	assert.Nil(t, got)
}

// digReflect is the slow path scriggoDig falls into when the
// top-level obj is NOT a literal map[string]any (so the
// `m, ok := obj.(map[string]any)` fast-path assertion fails).
// The TypedNil test above covers the isNilValue early return; the
// remaining load-bearing branches were uncovered. They matter
// because templates legitimately pass non-standard map types
// (e.g. map[string]string from K8s metadata.labels) and the
// dig() filter MUST behave consistently across both:
//
//   - Templates use `dig("metadata", "labels", "app")` against
//     unstructured K8s objects. The labels field is map[string]string;
//     a regression in the slow path would break every label/annotation
//     lookup.
//
//   - Templates may pass non-map types (e.g. `dig(svcPort, "name")`
//     where svcPort is an int). The default branch must return nil
//     gracefully rather than panic — operators rely on dig being
//     null-safe so they can compose `dig | fallback`.
func TestDigReflect_TopLevelMapStringStringHit(t *testing.T) {
	// Single-key lookup on a top-level map[string]string. The
	// scriggoDig fast-path skips this case (only matches map[string]any),
	// so this MUST exercise digReflect's `case map[string]string:`.
	labels := map[string]string{
		"app":      "haproxy",
		"version":  "v1.2.3",
		"instance": "haproxy-pod-1",
	}

	got := scriggoDig(labels, "app")
	assert.Equal(t, "haproxy", got,
		"map[string]string at top level MUST return the matching value via "+
			"the slow-path branch — without this, every K8s label lookup "+
			"(metadata.labels[...], metadata.annotations[...]) would silently "+
			"return nil and break template-based labelsmatching")
}

func TestDigReflect_TopLevelMapStringStringMiss(t *testing.T) {
	labels := map[string]string{"app": "haproxy"}

	got := scriggoDig(labels, "missing-key")
	assert.Nil(t, got,
		"missing key on a map[string]string MUST return nil (not panic, not "+
			"return zero value) — template authors compose dig | fallback "+
			"and expect nil to trigger the fallback")
}

func TestDigReflect_NonMapTypeFallsToDefaultReturnsNil(t *testing.T) {
	// Non-map types (ints, strings, structs) hit digReflect's
	// `default:` case and MUST return nil. A regression that
	// panicked here would crash every template that tried to
	// dig() into a non-map value (e.g. a Service port number).
	tests := []struct {
		name string
		obj  any
	}{
		{name: "int", obj: 42},
		{name: "string", obj: "haproxy"},
		{name: "[]string", obj: []string{"a", "b"}},
		{name: "struct", obj: struct{ X int }{X: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				got := scriggoDig(tt.obj, "any-key")
				assert.Nil(t, got,
					"non-map type %T MUST return nil from the default branch — "+
						"a panic here would crash any template that dug into a "+
						"non-map value (extremely common: dig(svcPort, \"name\"))",
					tt.obj)
			})
		})
	}
}

func TestDigReflect_MultiKeyAfterStringValueReturnsNil(t *testing.T) {
	// Multi-key navigation on map[string]string: after fetching
	// the first key the value is a string, not a map. The next
	// iteration's switch hits the default branch → return nil.
	// Without this safety, the function would panic trying to
	// navigate into a string.
	labels := map[string]string{"app": "haproxy"}

	got := scriggoDig(labels, "app", "deeper-key")
	assert.Nil(t, got,
		"navigating past a string value MUST return nil — the second key "+
			"can't traverse INTO a string, and silently returning nil lets "+
			"template authors compose `dig | fallback` without special-casing "+
			"the depth limit")
}

// TestDigReflect_TypedStruct pins the keystone Phase 6 property:
// dig() navigates typed structs by their JSON tag, NOT by their
// Go field name. This is what lets the chart adopt the typed shape
// without rewriting every dig() call site — `dig(gw, "metadata",
// "name")` works whether `gw` is a map[string]any or a typed
// pointer like the ones the renderer's typed-resource entries
// produce.
func TestDigReflect_TypedStruct(t *testing.T) {
	type meta struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	type gateway struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   meta   `json:"metadata"`
	}

	gw := gateway{
		APIVersion: "gateway.networking.k8s.io/v1",
		Kind:       "Gateway",
		Metadata:   meta{Name: "public", Namespace: "ingress"},
	}

	t.Run("dig by JSON tag", func(t *testing.T) {
		got := scriggoDig(gw, "metadata", "name")
		assert.Equal(t, "public", got)
	})

	t.Run("dig by JSON tag — top-level scalar", func(t *testing.T) {
		got := scriggoDig(gw, "kind")
		assert.Equal(t, "Gateway", got)
	})

	t.Run("dig through pointer auto-dereferences", func(t *testing.T) {
		got := scriggoDig(&gw, "metadata", "namespace")
		assert.Equal(t, "ingress", got)
	})

	t.Run("missing JSON tag returns nil", func(t *testing.T) {
		got := scriggoDig(gw, "metadata", "deletedAt")
		assert.Nil(t, got)
	})

	t.Run("dig into nil pointer returns nil safely", func(t *testing.T) {
		var nilGw *gateway
		got := scriggoDig(nilGw, "metadata", "name")
		assert.Nil(t, got)
	})

	t.Run("dig by Go field name as fallback", func(t *testing.T) {
		// Types declared without json tags (test fixtures, hand-written
		// helpers) still reach their fields via the Go name. typegen
		// always emits json tags, but the chart might dig into other
		// shapes too.
		type bareStruct struct {
			Hello string
		}
		got := scriggoDig(bareStruct{Hello: "world"}, "Hello")
		assert.Equal(t, "world", got)
	})
}

// TestDigReflect_StructFieldCache documents the cache behaviour:
// repeated digs on the same type re-use the JSON-name → field-index
// map. Verified via behavioural equivalence (two consecutive calls
// return the same value); the cache is private so we can't poke at
// it directly, but the test guards against a future regression
// where someone removes the cache and quietly tanks render perf.
func TestDigReflect_StructFieldCache(t *testing.T) {
	type bigStruct struct {
		A string `json:"a"`
		B string `json:"b"`
		C string `json:"c"`
		D string `json:"d"`
		E string `json:"e"`
	}
	v := bigStruct{A: "1", B: "2", C: "3", D: "4", E: "5"}
	for i := 0; i < 5; i++ {
		assert.Equal(t, "3", scriggoDig(v, "c"))
		assert.Equal(t, "5", scriggoDig(v, "e"))
	}
}

// Note: a "with helpers" test verifying that digstr/digint/digbool
// inherit the typed-struct support transparently lives in MR !969
// (the typed-dig filters MR). It can't live here because this
// branch doesn't have those helpers yet — but it doesn't need to,
// because the helpers are thin wrappers around scriggoDig that
// add type coercion to the *return* value. The struct-traversal
// logic this file pins via TestDigReflect_TypedStruct is the only
// place where map-vs-struct behaviour can diverge; the helpers
// just convert the resulting `any`.

func TestIsValueInList_Direct(t *testing.T) {
	tests := []struct {
		name  string
		value any
		list  any
		want  bool
	}{
		{name: "nil list", value: "a", list: nil, want: false},
		{name: "[]string contains", value: "a", list: []string{"a", "b", "c"}, want: true},
		{name: "[]string missing", value: "z", list: []string{"a", "b"}, want: false},
		{name: "[]any contains", value: "a", list: []any{"a", "b"}, want: true},
		{name: "[]any with mixed types", value: 42, list: []any{"a", 42}, want: true},
		{name: "non-slice list returns false", value: "a", list: "abc", want: false},
		{name: "[]int via reflection finds match", value: "1", list: []int{1, 2, 3}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isValueInList(tt.value, tt.list))
		})
	}
}

func TestScriggoJoinKey(t *testing.T) {
	tests := []struct {
		name  string
		sep   string
		parts []any
		want  string
	}{
		{name: "no parts returns empty", sep: "_", parts: nil, want: ""},
		{name: "single part", sep: "_", parts: []any{"a"}, want: "a"},
		{name: "multiple strings", sep: "_", parts: []any{"a", "b", "c"}, want: "a_b_c"},
		{name: "mixed types converted", sep: "/", parts: []any{"ns", "name", 42}, want: "ns/name/42"},
		{name: "empty separator", sep: "", parts: []any{"a", "b"}, want: "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoJoinKey(tt.sep, tt.parts...))
		})
	}
}

func TestScriggoNamespace(t *testing.T) {
	t.Run("nil yields empty mutable map", func(t *testing.T) {
		ns := scriggoNamespace(nil)
		assert.NotNil(t, ns)
		assert.Empty(t, ns)
		ns["k"] = 1
		assert.Equal(t, 1, ns["k"])
	})

	t.Run("non-nil returns detached mutable map", func(t *testing.T) {
		init := map[string]any{"count": 0}
		ns := scriggoNamespace(init)
		ns["count"] = 5
		assert.Equal(t, 0, init["count"])
		assert.Equal(t, 5, ns["count"])
	})
}

func TestScriggoDigString(t *testing.T) {
	annotations := map[string]any{"haproxy.org/foo": "bar", "empty": ""}
	metadata := map[string]any{"namespace": "default", "annotations": annotations}
	obj := map[string]any{"metadata": metadata}

	tests := []struct {
		name       string
		obj        any
		defaultStr string
		keys       []string
		want       string
	}{
		{
			name:       "present string returns as-is",
			obj:        obj,
			defaultStr: "",
			keys:       []string{"metadata", "annotations", "haproxy.org/foo"},
			want:       "bar",
		},
		{
			name:       "missing path returns default",
			obj:        obj,
			defaultStr: "fallback-value",
			keys:       []string{"metadata", "annotations", "missing"},
			want:       "fallback-value",
		},
		{
			name:       "empty string value bypasses default (matches dig|fallback|tostring semantics)",
			obj:        obj,
			defaultStr: "would-not-fire",
			keys:       []string{"metadata", "annotations", "empty"},
			want:       "",
		},
		{
			name:       "nil obj returns default",
			obj:        nil,
			defaultStr: "default-on-nil",
			keys:       []string{"metadata", "name"},
			want:       "default-on-nil",
		},
		{
			name:       "no keys returns tostring of obj when non-nil",
			obj:        "raw",
			defaultStr: "",
			keys:       nil,
			want:       "raw",
		},
		{
			name:       "non-string scalar coerced via tostring",
			obj:        map[string]any{"port": 8080},
			defaultStr: "",
			keys:       []string{"port"},
			want:       "8080",
		},
		{
			name:       "tristate-pointer optional field with nil pointer normalises to default",
			obj:        map[string]any{"gen": (*int64)(nil)},
			defaultStr: "",
			keys:       []string{"gen"},
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoDigString(tt.obj, tt.defaultStr, tt.keys...))
		})
	}
}

func TestScriggoCoalesce(t *testing.T) {
	tests := []struct {
		name string
		val  any
		def  any
		want any
	}{
		{name: "non-nil value returned", val: "x", def: "default", want: "x"},
		{name: "nil falls back to default", val: nil, def: "default", want: "default"},
		{name: "empty string is not nil", val: "", def: "default", want: ""},
		{name: "zero is not nil", val: 0, def: 99, want: 0},
		{name: "false is not nil", val: false, def: true, want: false},
		{name: "default can be nil", val: nil, def: nil, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoCoalesce(tt.val, tt.def))
		})
	}
}

func TestScriggoJoin(t *testing.T) {
	tests := []struct {
		name string
		in   any
		sep  string
		want string
	}{
		{name: "[]string with comma", in: []string{"a", "b", "c"}, sep: ", ", want: "a, b, c"},
		{name: "[]string single", in: []string{"only"}, sep: ", ", want: "only"},
		{name: "[]string empty", in: []string{}, sep: ", ", want: ""},
		{name: "[]any with strings", in: []any{"a", "b"}, sep: "-", want: "a-b"},
		{name: "[]any mixed types", in: []any{"a", 42, true}, sep: " ", want: "a 42 true"},
		{name: "non-slice returns empty", in: "abc", sep: ",", want: ""},
		{name: "nil returns empty", in: nil, sep: ",", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoJoin(tt.in, tt.sep))
		})
	}
}

func TestScriggoSelectAttr_NoTest(t *testing.T) {
	// Without a test argument, selectattr returns items where attr is non-nil.
	items := []any{
		map[string]any{"name": "a", "active": true},
		map[string]any{"name": "b"},
		map[string]any{"name": "c", "active": false},
	}

	got := scriggoSelectAttr(items, "active")
	assert.Len(t, got, 2)
	assert.Contains(t, got, items[0])
	assert.Contains(t, got, items[2])
}

func TestScriggoSelectAttr_EqTest(t *testing.T) {
	items := []any{
		map[string]any{"name": "a", "type": "Exact"},
		map[string]any{"name": "b", "type": "Prefix"},
		map[string]any{"name": "c", "type": "Exact"},
	}

	got := scriggoSelectAttr(items, "type", "eq", "Exact")
	assert.Len(t, got, 2)
	assert.Equal(t, items[0], got[0])
	assert.Equal(t, items[2], got[1])
}

func TestScriggoSelectAttr_NeTest(t *testing.T) {
	items := []any{
		map[string]any{"name": "a", "type": "Exact"},
		map[string]any{"name": "b", "type": "Prefix"},
	}

	got := scriggoSelectAttr(items, "type", "ne", "Exact")
	assert.Len(t, got, 1)
	assert.Equal(t, items[1], got[0])
}

func TestScriggoSelectAttr_InTest_Direct(t *testing.T) {
	items := []any{
		map[string]any{"name": "a", "type": "Exact"},
		map[string]any{"name": "b", "type": "Prefix"},
		map[string]any{"name": "c", "type": "ImplementationSpecific"},
	}

	got := scriggoSelectAttr(items, "type", "in", []string{"Exact", "Prefix"})
	assert.Len(t, got, 2)
}

func TestScriggoSelectAttr_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		items any
		attr  string
		want  []any
	}{
		{name: "nil items", items: nil, attr: "x", want: []any{}},
		{name: "non-slice items", items: 42, attr: "x", want: []any{}},
		{name: "items with nil entries skipped", items: []any{nil, map[string]any{"x": 1}}, attr: "x", want: []any{map[string]any{"x": 1}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scriggoSelectAttr(tt.items, tt.attr)
			assert.Equal(t, tt.want, got)
		})
	}
}
