// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package stores

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// convertOverlayResource is the gate that decides whether the float→int
// pass runs. The contract is:
//   - *unstructured.Unstructured → unwrap .Object map and recurse
//     (overlay resources from admission webhooks need this so port 80
//     doesn't surface as port 80.0 in templates)
//   - any other runtime.Object → pass through verbatim (typed resources
//     already have proper integer fields).
//
// Both branches are exercised indirectly via the StoreOverlay
// constructor tests, but the type-switch behaviour is the contract that
// keeps the float→int normalization safe — pin it directly.
func TestConvertOverlayResource(t *testing.T) {
	t.Run("unstructured triggers float→int conversion on the underlying map", func(t *testing.T) {
		u := &unstructured.Unstructured{
			Object: map[string]any{
				"spec": map[string]any{
					"port":    float64(80),  // float with no fractional part
					"timeout": float64(1.5), // genuine float
				},
			},
		}

		got := convertOverlayResource(u)

		// The underlying map is returned (NOT the *Unstructured wrapper).
		m, ok := got.(map[string]any)
		if assert.True(t, ok, "must return underlying map[string]any, not the wrapper") {
			spec := m["spec"].(map[string]any)
			assert.Equal(t, int64(80), spec["port"], "integral float must convert to int64")
			assert.Equal(t, float64(1.5), spec["timeout"], "non-integral float must stay as float64")
		}
	})

	t.Run("typed resource passes through verbatim (no conversion)", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "x"},
		}

		got := convertOverlayResource(cm)

		// Returned as-is — no map extraction, no recursion.
		assert.Same(t, cm, got, "typed runtime.Object must pass through verbatim")
	})
}

// convertFloatsToInts is recursive: it must descend into both maps AND
// slices, converting integral float64 to int64 in place while leaving
// genuine floats and non-numeric values untouched.
func TestConvertFloatsToInts(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{
			name: "integral float at top level converts",
			in:   float64(42),
			want: int64(42),
		},
		{
			name: "non-integral float at top level stays a float",
			in:   1.5,
			want: 1.5,
		},
		{
			name: "string passes through",
			in:   "hello",
			want: "hello",
		},
		{
			name: "nil passes through",
			in:   nil,
			want: nil,
		},
		{
			name: "negative integral float converts (preserves sign)",
			in:   float64(-7),
			want: int64(-7),
		},
		{
			name: "map values are converted recursively",
			in: map[string]any{
				"port":    float64(80),
				"timeout": float64(1.5),
				"name":    "api",
			},
			want: map[string]any{
				"port":    int64(80),
				"timeout": float64(1.5),
				"name":    "api",
			},
		},
		{
			name: "slice elements are converted recursively",
			in:   []any{float64(1), float64(2), float64(2.5), "x"},
			want: []any{int64(1), int64(2), float64(2.5), "x"},
		},
		{
			name: "deeply nested mix is converted at every level",
			in: map[string]any{
				"spec": map[string]any{
					"ports": []any{
						map[string]any{"port": float64(80), "protocol": "TCP"},
						map[string]any{"port": float64(443), "protocol": "TCP"},
					},
				},
			},
			want: map[string]any{
				"spec": map[string]any{
					"ports": []any{
						map[string]any{"port": int64(80), "protocol": "TCP"},
						map[string]any{"port": int64(443), "protocol": "TCP"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertFloatsToInts(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("conversion mutates maps in place", func(t *testing.T) {
		// The function returns the same map reference and mutates it
		// directly — overlay resources are freshly deserialized and
		// owned by the caller, so in-place mutation is safe and the
		// preferred performance choice. Pin both halves of that contract:
		//   1. the input map is mutated (the float64 became an int64)
		//   2. the returned `any` is backed by the SAME map header as the
		//      input, not a freshly-allocated copy
		original := map[string]any{"port": float64(8080)}
		got := convertFloatsToInts(original)

		gotMap, ok := got.(map[string]any)
		require.True(t, ok, "convertFloatsToInts must return a map[string]any for map input")

		// Maps in Go aren't comparable with == directly, but distinct map
		// values ARE distinct keys. Use a sentinel: write through the
		// returned reference and observe the mutation on the input
		// reference. If the function had returned a fresh allocation, the
		// original would not see the new key.
		gotMap["__sentinel__"] = "x"
		assert.Equal(t, "x", original["__sentinel__"],
			"mutating the returned map must be visible through the input — they must alias")
		delete(original, "__sentinel__")

		assert.Equal(t, int64(8080), original["port"], "input map must be mutated")
		assert.Equal(t, int64(8080), gotMap["port"], "returned map mirrors mutation")
	})
}
