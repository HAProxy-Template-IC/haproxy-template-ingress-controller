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
)

func TestOrdinaryEntryPointCannotRenderOrImportPrivateEntryPoint(t *testing.T) {
	privateTemplates := map[string]string{
		"component": `{% macro Hidden %}component{% end %}`,
		"planner":   `{% macro Hidden %}planner{% end %}`,
	}
	tests := map[string]func(string) string{
		"render": func(name string) string { return `{% render "` + name + `" %}` },
		"import": func(name string) string {
			return `{% import "` + name + `" for Hidden %}{{ Hidden() }}`
		},
	}
	for operation, source := range tests {
		for privateName := range privateTemplates {
			t.Run(operation+"/"+privateName, func(t *testing.T) {
				templates := map[string]string{
					"main":      source(privateName),
					"component": privateTemplates["component"],
					"planner":   privateTemplates["planner"],
				}
				_, err := New(templates, privateIsolationOptions("component", "planner"))
				require.Error(t, err)
				assert.Contains(t, err.Error(), privateName)
			})
		}
	}
}

func TestOrdinaryRenderGlobExcludesPrivateEntryPoints(t *testing.T) {
	engine, err := New(map[string]string{
		"main":              `{{ render_glob "private-*" }}`,
		"private-component": "component",
		"private-planner":   "{}",
	}, privateIsolationOptions("private-component", "private-planner"))
	require.NoError(t, err)

	output, err := engine.Render(t.Context(), "main", nil)
	require.NoError(t, err)
	assert.Equal(t, "\n", output)
}

func TestPrivateEntryPointsCanImportOrdinaryLibrary(t *testing.T) {
	engine, err := New(map[string]string{
		"main":      `{% import "library" for Value %}{{ Value() }}`,
		"component": `{% import "library" for Value %}{{ Value() }}`,
		"planner":   `{% import "library" for Value %}{"value":"{{ Value() }}"}`,
		"library":   `{% macro Value %}value{% end %}`,
	}, privateIsolationOptions("component", "planner"))
	require.NoError(t, err)

	ordinary, err := engine.Render(t.Context(), "main", nil)
	require.NoError(t, err)
	assert.Equal(t, "value\n", ordinary)

	component, err := engine.RenderIncrementalComponent(t.Context(), "component", incrementalComponentContext(nil))
	require.NoError(t, err)
	assert.Equal(t, "value", component)

	planner, err := engine.RenderIncrementalBindings(t.Context(), "planner", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"value":"value"}`, string(planner))
}

func TestPrivateEntryPointCannotImportAnotherPrivateEntryPoint(t *testing.T) {
	_, err := New(map[string]string{
		"main":      "main",
		"component": `{% import "planner" for Hidden %}{{ Hidden() }}`,
		"planner":   `{% macro Hidden %}planner{% end %}`,
	}, privateIsolationOptions("component", "planner"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "planner")
}

func privateIsolationOptions(component, planner string) *Options {
	return &Options{
		EntryPoints:                   []string{"main", component, planner},
		IncrementalEntryPoints:        []string{component},
		IncrementalBindingEntryPoints: []string{planner},
	}
}
