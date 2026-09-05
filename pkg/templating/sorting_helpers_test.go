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
)

func TestEvaluateExpression(t *testing.T) {
	item := map[string]any{
		"name": "alice",
		"age":  42,
		"address": map[string]any{
			"city": "berlin",
		},
	}

	tests := []struct {
		name string
		item any
		expr string
		want any
	}{
		{name: "empty expression returns nil", item: item, expr: "", want: nil},
		{name: "$ returns the item itself", item: item, expr: "$", want: item},
		{name: "field with $. prefix", item: item, expr: "$.name", want: "alice"},
		{name: "field without prefix is treated as field name", item: item, expr: "age", want: 42},
		{name: "nested field", item: item, expr: "$.address.city", want: "berlin"},
		{name: "missing field yields nil", item: item, expr: "$.missing", want: nil},
		{name: "nested missing yields nil", item: item, expr: "$.address.zip", want: nil},
		{name: "expression with whitespace", item: item, expr: "  $.name  ", want: "alice"},
		{name: "nil item with field path", item: nil, expr: "$.name", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evaluateExpression(tt.item, tt.expr))
		})
	}
}

func TestNavigateJSONPath(t *testing.T) {
	item := map[string]any{
		"name": "alice",
		"items": []any{
			"first",
			"second",
			map[string]any{"nested": "value"},
		},
	}

	tests := []struct {
		name string
		item any
		path string
		want any
	}{
		{name: "$ returns item", item: item, path: "$", want: item},
		{name: "empty path after $. returns item", item: item, path: "$.", want: item},
		{name: "field access", item: item, path: "$.name", want: "alice"},
		{name: "array index 0", item: item, path: "$.items.[0]", want: "first"},
		{name: "array index 1", item: item, path: "$.items.[1]", want: "second"},
		{name: "array index out of range", item: item, path: "$.items.[10]", want: nil},
		{name: "negative array index", item: item, path: "$.items.[-1]", want: nil},
		{name: "non-numeric array index", item: item, path: "$.items.[abc]", want: nil},
		{name: "array index into nested map", item: item, path: "$.items.[2].nested", want: "value"},
		{name: "missing field yields nil", item: item, path: "$.missing", want: nil},
		{name: "nil propagates through path", item: nil, path: "$.field", want: nil},
		{name: "field on non-map yields nil", item: 42, path: "$.field", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, navigateJSONPath(tt.item, tt.path))
		})
	}
}

func TestGetField(t *testing.T) {
	type structType struct {
		Name string
		Age  int
	}
	// typegenLike mirrors the shape typegen produces for K8s resources:
	// PascalCase Go field names with lowercase JSON tags. Sort criteria
	// like `$.match.method` carry lowercase JSONPath segments, so getField
	// must resolve them via the JSON tag — not the Go field name — when
	// the underlying value is a typed struct. Without this, every typed-
	// resource sort key extracts nil and route precedence collapses
	// (gateway-conformance HTTPRouteHeaderMatching / QueryParamMatching /
	// RewriteHost regression, pipeline 2549008743).
	type typegenLike struct {
		Match  map[string]any `json:"match,omitempty"`
		Method string         `json:"method,omitempty"`
	}
	mapData := map[string]any{"name": "alice", "age": 42}
	typedItem := typegenLike{
		Match:  map[string]any{"k": "v"},
		Method: "GET",
	}

	tests := []struct {
		name      string
		item      any
		fieldName string
		want      any
	}{
		{name: "nil item", item: nil, fieldName: "name", want: nil},
		{name: "map field exists", item: mapData, fieldName: "name", want: "alice"},
		{name: "map field missing", item: mapData, fieldName: "missing", want: nil},
		{name: "struct field exists", item: structType{Name: "bob", Age: 30}, fieldName: "Name", want: "bob"},
		{name: "struct field exists by exact name", item: structType{Name: "bob", Age: 30}, fieldName: "Age", want: 30},
		{name: "struct field missing", item: structType{Name: "bob"}, fieldName: "Missing", want: nil},
		{name: "pointer to struct", item: &structType{Name: "carol", Age: 25}, fieldName: "Name", want: "carol"},
		{name: "string is not addressable", item: "hello", fieldName: "Name", want: nil},
		// JSON-tag resolution — the typegen-shaped case sort_by needs.
		{name: "typed struct by JSON tag (lowercase)", item: typedItem, fieldName: "method", want: "GET"},
		{name: "typed struct nested map by JSON tag", item: typedItem, fieldName: "match", want: map[string]any{"k": "v"}},
		{name: "typed struct still resolvable by Go field name", item: typedItem, fieldName: "Method", want: "GET"},
		{name: "pointer-to-typed-struct by JSON tag", item: &typedItem, fieldName: "method", want: "GET"},
		// Omitempty zero-value normalisation: optional field with the
		// type's zero value reads back as nil, so sort_by's :exists
		// modifier correctly distinguishes "field present" from
		// "field unset" on typegen-produced structs. Without this,
		// every typed route would appear to "have" a method/headers/
		// queryParams etc. and route precedence collapses (e2e
		// TestHTTPRoutePrecedence/plain_GET_routes_to_v2 — catch-all
		// rule incorrectly beat the GET-only rule).
		{
			name:      "typed struct omitempty zero string reads as nil",
			item:      typegenLike{Match: map[string]any{"k": "v"}}, // Method left zero
			fieldName: "method",
			want:      nil,
		},
		{
			name:      "typed struct omitempty zero nested-map reads as nil",
			item:      typegenLike{Method: "GET"}, // Match left zero
			fieldName: "match",
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, getField(tt.item, tt.fieldName))
		})
	}
}

func TestCompareValues(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want int
	}{
		{name: "both nil", a: nil, b: nil, want: 0},
		{name: "a nil sorts greater", a: nil, b: 1, want: 1},
		{name: "b nil sorts greater", a: 1, b: nil, want: -1},
		{name: "equal numbers", a: 5, b: 5, want: 0},
		{name: "smaller number", a: 3, b: 5, want: -1},
		{name: "larger number", a: 7, b: 5, want: 1},
		{name: "mixed numeric types", a: 3.0, b: 5, want: -1},
		{name: "equal strings", a: "abc", b: "abc", want: 0},
		{name: "string less than", a: "abc", b: "abd", want: -1},
		{name: "string greater than", a: "abd", b: "abc", want: 1},
		{name: "string vs bool", a: "abc", b: true, want: -1},
		{name: "booleans compare by deterministic text", a: true, b: false, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareValues(tt.a, tt.b)
			// normalize to -1/0/1
			switch {
			case got < 0:
				got = -1
			case got > 0:
				got = 1
			}
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("rejects composite", func(t *testing.T) {
		assert.Panics(t, func() { compareValues(struct{}{}, struct{}{}) })
	})
}

func TestGetLength(t *testing.T) {
	type structType struct{ X int }

	tests := []struct {
		name string
		in   any
		want int
	}{
		{name: "nil", in: nil, want: 0},
		{name: "empty string", in: "", want: 0},
		{name: "string len", in: "hello", want: 5},
		{name: "[]any", in: []any{1, 2, 3}, want: 3},
		{name: "[]string via reflection", in: []string{"a", "b"}, want: 2},
		{name: "[]int via reflection", in: []int{1, 2, 3, 4}, want: 4},
		{name: "map[string]any", in: map[string]any{"a": 1, "b": 2}, want: 2},
		{name: "map[string]int via reflection", in: map[string]int{"x": 1}, want: 1},
		{name: "array via reflection", in: [3]int{1, 2, 3}, want: 3},
		{name: "scalar yields 0", in: 42, want: 0},
		{name: "struct yields 0", in: structType{X: 1}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, getLength(tt.in))
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		want   float64
		wantOK bool
	}{
		{name: "float64", in: 3.14, want: 3.14, wantOK: true},
		{name: "float32", in: float32(2.5), want: 2.5, wantOK: true},
		{name: "int", in: 42, want: 42, wantOK: true},
		{name: "int8", in: int8(7), want: 7, wantOK: true},
		{name: "int16", in: int16(7), want: 7, wantOK: true},
		{name: "int32", in: int32(7), want: 7, wantOK: true},
		{name: "int64", in: int64(7), want: 7, wantOK: true},
		{name: "uint", in: uint(7), want: 7, wantOK: true},
		{name: "uint8", in: uint8(7), want: 7, wantOK: true},
		{name: "uint16", in: uint16(7), want: 7, wantOK: true},
		{name: "uint32", in: uint32(7), want: 7, wantOK: true},
		{name: "uint64", in: uint64(7), want: 7, wantOK: true},
		{name: "string is not numeric", in: "42", wantOK: false},
		{name: "bool is not numeric", in: true, wantOK: false},
		{name: "nil is not numeric", in: nil, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat64(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.InDelta(t, tt.want, got, 0.0001)
			}
		})
	}
}

func TestConvertToSlice(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		want   []any
		wantOK bool
	}{
		{name: "nil", in: nil, wantOK: false},
		{name: "[]any passes through", in: []any{1, 2}, want: []any{1, 2}, wantOK: true},
		{name: "[]string via reflection", in: []string{"a", "b"}, want: []any{"a", "b"}, wantOK: true},
		{name: "[]int via reflection", in: []int{1, 2}, want: []any{1, 2}, wantOK: true},
		{name: "array via reflection", in: [2]string{"a", "b"}, want: []any{"a", "b"}, wantOK: true},
		{name: "scalar returns false", in: 42, wantOK: false},
		{name: "string returns false", in: "abc", wantOK: false},
		{name: "map returns false", in: map[string]int{"a": 1}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := convertToSlice(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestConvertToMap(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		want   map[string]any
		wantOK bool
	}{
		{name: "nil", in: nil, wantOK: false},
		{name: "map[string]any passes through", in: map[string]any{"a": 1}, want: map[string]any{"a": 1}, wantOK: true},
		{name: "map[string]int via reflection", in: map[string]int{"a": 1, "b": 2}, want: map[string]any{"a": 1, "b": 2}, wantOK: true},
		{name: "pointer to map via reflection", in: &map[string]any{"a": 1}, want: map[string]any{"a": 1}, wantOK: true},
		{name: "non-string keys return false", in: map[int]string{1: "a"}, wantOK: false},
		{name: "scalar returns false", in: 42, wantOK: false},
		{name: "slice returns false", in: []int{1, 2}, wantOK: false},
		{name: "struct returns false", in: struct{ X int }{X: 1}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := convertToMap(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
