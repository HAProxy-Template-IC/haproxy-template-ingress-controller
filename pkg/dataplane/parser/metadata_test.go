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

package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeMetadata(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{name: "nil yields nil", in: nil, want: nil},
		{name: "empty yields nil", in: map[string]any{}, want: nil},
		{
			name: "already-flat map unchanged",
			in:   map[string]any{"comment": "Pod: echo-server", "tag": "demo"},
			want: map[string]any{"comment": "Pod: echo-server", "tag": "demo"},
		},
		{
			name: "nested with value field flattened",
			in:   map[string]any{"comment": map[string]any{"value": "Pod: echo-server"}},
			want: map[string]any{"comment": "Pod: echo-server"},
		},
		{
			name: "multiple nested fields flattened",
			in: map[string]any{
				"comment": map[string]any{"value": "c1"},
				"custom":  map[string]any{"value": "c2"},
			},
			want: map[string]any{"comment": "c1", "custom": "c2"},
		},
		{
			name: "mixed flat and nested",
			in: map[string]any{
				"flat":   "stays",
				"nested": map[string]any{"value": "lifted"},
			},
			want: map[string]any{"flat": "stays", "nested": "lifted"},
		},
		{
			name: "nested without value field is left as-is",
			in:   map[string]any{"comment": map[string]any{"other": "x"}},
			want: map[string]any{"comment": map[string]any{"other": "x"}},
		},
		{
			name: "non-string value type is preserved",
			in:   map[string]any{"count": map[string]any{"value": 42}},
			want: map[string]any{"count": 42},
		},
		{
			name: "non-map value (already a string) unchanged",
			in:   map[string]any{"comment": "already a string"},
			want: map[string]any{"comment": "already a string"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeMetadata(tt.in))
		})
	}
}

// TestNormalizeMetadata_MutatesInPlace pins the documented contract that the
// helper rewrites the input map rather than allocating a new one. A future
// refactor that switches to copy semantics would silently change memory
// behaviour for callers that share the input across goroutines.
func TestNormalizeMetadata_MutatesInPlace(t *testing.T) {
	m := map[string]any{
		"comment": map[string]any{"value": "lifted"},
		"flat":    "stays",
	}
	got := NormalizeMetadata(m)

	// Same backing map is returned and the lift was applied in place.
	assert.Equal(t, "lifted", m["comment"])
	assert.Equal(t, "lifted", got["comment"])
}
