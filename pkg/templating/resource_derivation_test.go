// Copyright 2026 Philipp Hossner
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

type resourceDeriverFunc func(string, any, string, any) (any, error)

func (f resourceDeriverFunc) DeriveResource(resource string, item any, path string, value any) (any, error) {
	return f(resource, item, path, value)
}

func TestScriggoDeriveResource(t *testing.T) {
	engine, err := New(map[string]string{
		"test": `{%% var derived = deriveResource("objects", item, "spec.value", "published") %%}{{ jsonpathGet(derived, "spec.value") }}`,
	}, nil)
	require.NoError(t, err)
	item := map[string]any{"metadata": map[string]any{"name": "item"}}
	deriver := resourceDeriverFunc(func(resource string, got any, path string, value any) (any, error) {
		assert.Equal(t, "objects", resource)
		assert.Equal(t, item, got)
		return DeriveResourceJSONPath(got, path, value)
	})

	output, err := engine.Render(t.Context(), "test", map[string]any{
		"item":                     item,
		ResourceDeriverContextName: deriver,
	})
	require.NoError(t, err)
	assert.Equal(t, "published\n", output)
	assert.Nil(t, scriggoJSONPathGet(item, "spec.value"))
}

func TestScriggoDeriveResourceDetachesNativeInputs(t *testing.T) {
	engine, err := New(map[string]string{
		"test": `{%% var derived = deriveResource("objects", item, "spec.value", item) %%}{{ jsonpathGet(derived, "kind") }}`,
	}, nil)
	require.NoError(t, err)
	item := map[string]any{
		"kind":     "Original",
		"metadata": map[string]any{"name": "item"},
	}
	deriver := resourceDeriverFunc(func(_ string, got any, _ string, value any) (any, error) {
		got.(map[string]any)["kind"] = "MutatedInput"
		value.(map[string]any)["kind"] = "MutatedValue"
		return got, nil
	})

	output, err := engine.Render(t.Context(), "test", map[string]any{
		"item":                     item,
		ResourceDeriverContextName: deriver,
	})
	require.NoError(t, err)
	assert.Equal(t, "MutatedInput\n", output)
	assert.Equal(t, "Original", item["kind"])
}
