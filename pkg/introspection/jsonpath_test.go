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

package introspection

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractField(t *testing.T) {
	testData := map[string]any{
		"version": "1.2.3",
		"config": map[string]any{
			"templates": map[string]string{
				"main": "template-content",
			},
			"enabled": true,
		},
		"items": []any{
			map[string]any{"name": "item1"},
			map[string]any{"name": "item2"},
		},
	}

	t.Run("empty expression returns original data", func(t *testing.T) {
		result, err := ExtractField(testData, "")

		require.NoError(t, err)
		assert.Equal(t, testData, result)
	})

	t.Run("simple field extraction", func(t *testing.T) {
		result, err := ExtractField(testData, "{.version}")

		require.NoError(t, err)
		assert.Equal(t, "1.2.3", result)
	})

	t.Run("nested field extraction", func(t *testing.T) {
		result, err := ExtractField(testData, "{.config.enabled}")

		require.NoError(t, err)
		assert.Equal(t, true, result)
	})

	t.Run("deeply nested field extraction", func(t *testing.T) {
		result, err := ExtractField(testData, "{.config.templates.main}")

		require.NoError(t, err)
		assert.Equal(t, "template-content", result)
	})

	t.Run("array index extraction", func(t *testing.T) {
		result, err := ExtractField(testData, "{.items[0].name}")

		require.NoError(t, err)
		assert.Equal(t, "item1", result)
	})

	t.Run("invalid jsonpath expression", func(t *testing.T) {
		_, err := ExtractField(testData, "{.invalid[")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid jsonpath")
	})

	t.Run("missing field with AllowMissingKeys", func(t *testing.T) {
		result, err := ExtractField(testData, "{.nonexistent}")

		require.NoError(t, err)
		// Should return nil or empty for missing keys
		assert.Nil(t, result)
	})

	t.Run("works with struct-like data", func(t *testing.T) {
		type Config struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}
		data := Config{Name: "test", Version: 2}

		result, err := ExtractField(data, "{.name}")

		require.NoError(t, err)
		assert.Equal(t, "test", result)
	})

	t.Run("works with slice data", func(t *testing.T) {
		data := []string{"a", "b", "c"}

		result, err := ExtractField(data, "{[1]}")

		require.NoError(t, err)
		assert.Equal(t, "b", result)
	})
}

func TestParseFieldQuery(t *testing.T) {
	t.Run("extracts field parameter", func(t *testing.T) {
		req := &http.Request{
			URL: &url.URL{
				RawQuery: "field={.version}",
			},
		}

		result := ParseFieldQuery(req)

		assert.Equal(t, "{.version}", result)
	})

	t.Run("returns empty for missing parameter", func(t *testing.T) {
		req := &http.Request{
			URL: &url.URL{
				RawQuery: "",
			},
		}

		result := ParseFieldQuery(req)

		assert.Equal(t, "", result)
	})

	t.Run("handles other parameters", func(t *testing.T) {
		req := &http.Request{
			URL: &url.URL{
				RawQuery: "other=value&field={.config}&another=param",
			},
		}

		result := ParseFieldQuery(req)

		assert.Equal(t, "{.config}", result)
	})

	t.Run("handles URL-encoded field", func(t *testing.T) {
		req := &http.Request{
			URL: &url.URL{
				RawQuery: "field=%7B.version%7D", // URL-encoded {.version}
			},
		}

		result := ParseFieldQuery(req)

		assert.Equal(t, "{.version}", result)
	})
}
