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

package config

import (
	"strings"
	"testing"
)

func TestValidateExtraContext(t *testing.T) {
	tests := []struct {
		name        string
		ctx         map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name:    "nil map",
			ctx:     nil,
			wantErr: false,
		},
		{
			name:    "empty map",
			ctx:     map[string]any{},
			wantErr: false,
		},
		{
			name: "primitive types",
			ctx: map[string]any{
				"string_val":  "hello",
				"float_val":   float64(3.14),
				"bool_val":    true,
				"nil_val":     nil,
				"integer_val": float64(42), // YAML integers become float64
			},
			wantErr: false,
		},
		{
			name: "nested map",
			ctx: map[string]any{
				"config": map[string]any{
					"enabled": true,
					"rate":    float64(100),
					"name":    "test",
				},
			},
			wantErr: false,
		},
		{
			name: "array",
			ctx: map[string]any{
				"items": []any{
					"item1",
					"item2",
					float64(3),
				},
			},
			wantErr: false,
		},
		{
			name: "deeply nested",
			ctx: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": []any{
							map[string]any{
								"value": float64(42),
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "function type rejected",
			ctx: map[string]any{
				"bad": func() {},
			},
			wantErr:     true,
			errContains: "unsupported type func()",
		},
		{
			name: "channel type rejected",
			ctx: map[string]any{
				"bad": make(chan int),
			},
			wantErr:     true,
			errContains: "unsupported type chan int",
		},
		{
			name: "nested unsupported type",
			ctx: map[string]any{
				"outer": map[string]any{
					"inner": func() {},
				},
			},
			wantErr:     true,
			errContains: "extra_context.outer",
		},
		{
			name: "unsupported type in array",
			ctx: map[string]any{
				"items": []any{
					"valid",
					func() {}, // invalid
				},
			},
			wantErr:     true,
			errContains: "[1]",
		},
		{
			name: "integer types allowed",
			ctx: map[string]any{
				"int_val":   int(42),
				"int64_val": int64(42),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExtraContext(tt.ctx)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateExtraContext() expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateExtraContext() error = %q, want error containing %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Errorf("ValidateExtraContext() unexpected error: %v", err)
			}
		})
	}
}
