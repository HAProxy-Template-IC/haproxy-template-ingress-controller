// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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

	"gitlab.com/haproxy-haptic/scriggo/native"
)

func TestScriggoEngineGlobalUsage(t *testing.T) {
	tests := map[string]struct {
		templates   map[string]string
		entryPoints []string
		wantUsed    bool
	}{
		"unused": {
			templates:   map[string]string{"main": "static"},
			entryPoints: []string{"main"},
		},
		"used by one entry point": {
			templates: map[string]string{
				"main":  "static",
				"other": `{{ currentConfig["value"] }}`,
			},
			entryPoints: []string{"main", "other"},
			wantUsed:    true,
		},
		"used by imported dependency": {
			templates: map[string]string{
				"main":    `{% import "library" for Value %}{{ Value() }}`,
				"library": `{% macro Value() string %}{{ currentConfig["value"] }}{% end %}`,
			},
			entryPoints: []string{"main"},
			wantUsed:    true,
		},
		"used by rendered dependency": {
			templates: map[string]string{
				"main":    `{{ render "partial" }}`,
				"partial": `{{ currentConfig["value"] }}`,
			},
			entryPoints: []string{"main"},
			wantUsed:    true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			engine, err := New(test.templates, &Options{
				EntryPoints: test.entryPoints,
				Declarations: map[string]any{
					"currentConfig": (*map[string]any)(nil),
				},
			})
			require.NoError(t, err)

			used, known := engine.GlobalUsage("currentConfig")
			assert.True(t, known)
			assert.Equal(t, test.wantUsed, used)
		})
	}
}

func TestScriggoEngineGlobalUsageIsUnknownForNativeDeclaration(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ indirectCurrentConfig() }}`,
	}, &Options{
		EntryPoints: []string{"main"},
		Declarations: map[string]any{
			"currentConfig": (*map[string]any)(nil),
			"indirectCurrentConfig": func(env native.Env) string {
				renderContext := env.Context().Value(RenderContextContextKey).(map[string]any)
				currentConfig := renderContext["currentConfig"].(*map[string]any)
				return (*currentConfig)["value"].(string)
			},
		},
	})
	require.NoError(t, err)

	used, known := engine.GlobalUsage("currentConfig")
	assert.False(t, used)
	assert.False(t, known)
	currentConfig := map[string]any{"value": "captured"}
	output, err := engine.Render(t.Context(), "main", map[string]any{"currentConfig": &currentConfig})
	require.NoError(t, err)
	assert.Equal(t, "captured\n", output)
}

func TestScriggoEngineGlobalUsageRemainsKnownWithCustomFunctionsAndFilters(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ pure() }}{{ "value" | pure_filter() }}`,
	}, &Options{
		EntryPoints: []string{"main"},
		Declarations: map[string]any{
			"currentConfig": (*map[string]any)(nil),
		},
		Functions: map[string]GlobalFunc{
			"pure": func(...any) (any, error) { return "pure", nil },
		},
		Filters: map[string]FilterFunc{
			"pure_filter": func(value any, _ ...any) (any, error) { return value, nil },
		},
	})
	require.NoError(t, err)

	used, known := engine.GlobalUsage("currentConfig")
	assert.False(t, used)
	assert.True(t, known)
}
