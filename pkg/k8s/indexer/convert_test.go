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

package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestConvertResource(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want map[string]any
	}{
		{
			name: "*unstructured.Unstructured",
			in: &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
			}},
			want: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
			},
		},
		{
			name: "map[string]any",
			in: map[string]any{
				"foo": "bar",
				"n":   float64(5.0),
			},
			want: map[string]any{
				"foo": "bar",
				"n":   int64(5),
			},
		},
		{name: "unsupported scalar type", in: "not a resource", want: nil},
		{name: "unsupported nil", in: nil, want: nil},
		{name: "unsupported int", in: 42, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ConvertResource(tt.in))
		})
	}
}

func TestConvertFloatsToInts(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{name: "whole float becomes int64", in: 80.0, want: int64(80)},
		{name: "negative whole float", in: -3.0, want: int64(-3)},
		{name: "zero float", in: 0.0, want: int64(0)},
		{name: "fractional float preserved", in: 3.14, want: 3.14},
		{name: "large whole float", in: 1e9, want: int64(1000000000)},
		{name: "string unchanged", in: "hello", want: "hello"},
		{name: "int unchanged", in: 5, want: 5},
		{name: "bool unchanged", in: true, want: true},
		{name: "nil unchanged", in: nil, want: nil},
		{
			name: "nested map",
			in: map[string]any{
				"port":     80.0,
				"weight":   2.5,
				"name":     "api",
				"replicas": 3.0,
			},
			want: map[string]any{
				"port":     int64(80),
				"weight":   2.5,
				"name":     "api",
				"replicas": int64(3),
			},
		},
		{
			name: "slice of floats",
			in:   []any{80.0, 443.0, 3.14},
			want: []any{int64(80), int64(443), 3.14},
		},
		{
			name: "deeply nested",
			in: map[string]any{
				"spec": map[string]any{
					"ports": []any{
						map[string]any{"containerPort": 80.0, "weight": 1.5},
						map[string]any{"containerPort": 443.0},
					},
				},
			},
			want: map[string]any{
				"spec": map[string]any{
					"ports": []any{
						map[string]any{"containerPort": int64(80), "weight": 1.5},
						map[string]any{"containerPort": int64(443)},
					},
				},
			},
		},
		{
			name: "empty map preserved",
			in:   map[string]any{},
			want: map[string]any{},
		},
		{
			name: "empty slice preserved",
			in:   []any{},
			want: []any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertFloatsToInts(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestConvertFloatsToInts_MutatesInPlace pins the documented contract that
// convertFloatsToInts mutates maps and slices in place rather than allocating
// new ones — a future refactor that switches to a copy-on-write strategy
// would break the per-render sharing pattern callers rely on.
func TestConvertFloatsToInts_MutatesInPlace(t *testing.T) {
	t.Run("map mutated", func(t *testing.T) {
		m := map[string]any{"port": 80.0}
		got := convertFloatsToInts(m)
		// Same map header, same backing — and the value was rewritten in place.
		assert.Equal(t, int64(80), m["port"])
		assert.Equal(t, m, got)
	})

	t.Run("slice mutated", func(t *testing.T) {
		s := []any{80.0, 443.0}
		got := convertFloatsToInts(s)
		assert.Equal(t, int64(80), s[0])
		assert.Equal(t, int64(443), s[1])
		assert.Equal(t, s, got)
	})
}
