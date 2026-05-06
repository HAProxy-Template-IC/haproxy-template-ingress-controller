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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoCondition(t *testing.T) {
	t.Run("basic field mapping", func(t *testing.T) {
		result := scriggoCondition("Accepted", "True", "Accepted", "Route accepted", int64(5), "2025-01-01T00:00:00Z")

		assert.Equal(t, "Accepted", result["type"])
		assert.Equal(t, "True", result["status"])
		assert.Equal(t, "Accepted", result["reason"])
		assert.Equal(t, "Route accepted", result["message"])
		assert.Equal(t, int64(5), result["observedGeneration"])
		assert.Equal(t, "2025-01-01T00:00:00Z", result["lastTransitionTime"])
	})

	t.Run("observedGeneration from int", func(t *testing.T) {
		result := scriggoCondition("Ready", "True", "Ready", "", 42, "2025-01-01T00:00:00Z")
		assert.Equal(t, int64(42), result["observedGeneration"])
	})

	t.Run("observedGeneration from float64 (JSON numbers)", func(t *testing.T) {
		result := scriggoCondition("Ready", "True", "Ready", "", float64(7), "2025-01-01T00:00:00Z")
		assert.Equal(t, int64(7), result["observedGeneration"])
	})

	t.Run("observedGeneration from float64 with rounding", func(t *testing.T) {
		result := scriggoCondition("Ready", "True", "Ready", "", 3.7, "2025-01-01T00:00:00Z")
		assert.Equal(t, int64(4), result["observedGeneration"])
	})

	t.Run("observedGeneration from nil", func(t *testing.T) {
		result := scriggoCondition("Ready", "True", "Ready", "", nil, "2025-01-01T00:00:00Z")
		assert.Equal(t, int64(0), result["observedGeneration"])
	})

	t.Run("observedGeneration from unsupported type defaults to zero", func(t *testing.T) {
		result := scriggoCondition("Ready", "True", "Ready", "", "not-a-number", "2025-01-01T00:00:00Z")
		assert.Equal(t, int64(0), result["observedGeneration"])
	})

	t.Run("all fields present in result", func(t *testing.T) {
		result := scriggoCondition("Programmed", "False", "Pending", "Not yet programmed", int64(1), "2025-06-01T12:00:00Z")
		require.Len(t, result, 6)
		assert.Contains(t, result, "type")
		assert.Contains(t, result, "status")
		assert.Contains(t, result, "reason")
		assert.Contains(t, result, "message")
		assert.Contains(t, result, "observedGeneration")
		assert.Contains(t, result, "lastTransitionTime")
	})
}

func TestScriggoTransitionTime(t *testing.T) {
	t.Run("nil conditions returns now", func(t *testing.T) {
		before := time.Now().UTC()
		result := scriggoTransitionTime(nil, "Accepted", "True")
		after := time.Now().UTC()

		parsed, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
		assert.False(t, parsed.Before(before.Truncate(time.Second)))
		assert.False(t, parsed.After(after.Add(time.Second)))
	})

	t.Run("status unchanged preserves existing time", func(t *testing.T) {
		conditions := []any{
			map[string]any{
				"type":               "Accepted",
				"status":             "True",
				"lastTransitionTime": "2025-01-15T10:30:00Z",
			},
		}

		result := scriggoTransitionTime(conditions, "Accepted", "True")
		assert.Equal(t, "2025-01-15T10:30:00Z", result)
	})

	t.Run("status changed returns now", func(t *testing.T) {
		conditions := []any{
			map[string]any{
				"type":               "Accepted",
				"status":             "True",
				"lastTransitionTime": "2025-01-15T10:30:00Z",
			},
		}

		before := time.Now().UTC()
		result := scriggoTransitionTime(conditions, "Accepted", "False")
		after := time.Now().UTC()

		assert.NotEqual(t, "2025-01-15T10:30:00Z", result)
		parsed, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
		assert.False(t, parsed.Before(before.Truncate(time.Second)))
		assert.False(t, parsed.After(after.Add(time.Second)))
	})

	t.Run("condition not found returns now", func(t *testing.T) {
		conditions := []any{
			map[string]any{
				"type":               "Ready",
				"status":             "True",
				"lastTransitionTime": "2025-01-15T10:30:00Z",
			},
		}

		before := time.Now().UTC()
		result := scriggoTransitionTime(conditions, "Accepted", "True")
		after := time.Now().UTC()

		parsed, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
		assert.False(t, parsed.Before(before.Truncate(time.Second)))
		assert.False(t, parsed.After(after.Add(time.Second)))
	})

	t.Run("non-slice conditions returns now", func(t *testing.T) {
		// Caller passed something that isn't a []any (e.g. dig hit a
		// string-typed leaf, or the resource hasn't been initialised) —
		// the helper should treat this exactly like nil.
		result := scriggoTransitionTime("not-a-slice", "Accepted", "True")
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("empty conditions returns now", func(t *testing.T) {
		result := scriggoTransitionTime([]any{}, "Accepted", "True")
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("status unchanged but no lastTransitionTime returns now", func(t *testing.T) {
		conditions := []any{
			map[string]any{
				"type":   "Accepted",
				"status": "True",
			},
		}

		result := scriggoTransitionTime(conditions, "Accepted", "True")
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("multiple conditions finds matching one", func(t *testing.T) {
		conditions := []any{
			map[string]any{
				"type":               "Ready",
				"status":             "True",
				"lastTransitionTime": "2025-01-10T00:00:00Z",
			},
			map[string]any{
				"type":               "Accepted",
				"status":             "True",
				"lastTransitionTime": "2025-01-15T10:30:00Z",
			},
			map[string]any{
				"type":               "ResolvedRefs",
				"status":             "True",
				"lastTransitionTime": "2025-01-12T00:00:00Z",
			},
		}

		result := scriggoTransitionTime(conditions, "Accepted", "True")
		assert.Equal(t, "2025-01-15T10:30:00Z", result)
	})
}

func TestScriggoToJSON(t *testing.T) {
	t.Run("nil returns null", func(t *testing.T) {
		assert.Equal(t, "null", scriggoToJSON(nil))
	})

	t.Run("string", func(t *testing.T) {
		assert.Equal(t, `"hello"`, scriggoToJSON("hello"))
	})

	t.Run("integer", func(t *testing.T) {
		assert.Equal(t, "42", scriggoToJSON(42))
	})

	t.Run("float", func(t *testing.T) {
		assert.Equal(t, "3.14", scriggoToJSON(3.14))
	})

	t.Run("boolean", func(t *testing.T) {
		assert.Equal(t, "true", scriggoToJSON(true))
	})

	t.Run("map", func(t *testing.T) {
		result := scriggoToJSON(map[string]any{
			"key": "value",
		})
		assert.Contains(t, result, `"key"`)
		assert.Contains(t, result, `"value"`)
	})

	t.Run("slice", func(t *testing.T) {
		result := scriggoToJSON([]any{"a", "b", "c"})
		assert.Equal(t, `["a","b","c"]`, result)
	})

	t.Run("nested structure", func(t *testing.T) {
		result := scriggoToJSON(map[string]any{
			"loadBalancer": map[string]any{
				"ingress": []any{
					map[string]any{"ip": "10.0.0.1"},
				},
			},
		})
		assert.Contains(t, result, `"ip":"10.0.0.1"`)
		assert.Contains(t, result, `"loadBalancer"`)
	})

	t.Run("empty map", func(t *testing.T) {
		assert.Equal(t, "{}", scriggoToJSON(map[string]any{}))
	})

	t.Run("empty slice", func(t *testing.T) {
		assert.Equal(t, "[]", scriggoToJSON([]any{}))
	})
}

// Note: the previous TestFindTopLevelConditions / TestFindParentConditions
// tests are gone with the helpers themselves. They were resource-shape-coupled
// (knew about .status.conditions, .status.parents[i].conditions) — which
// violates pkg/templating's resource-agnostic contract. Equivalent navigation
// now lives in chart-side macros (charts/haptic/libraries/gateway.yaml's
// `util-condition-transition-time` library) where Gateway-API knowledge
// belongs; the Go helper just compares a supplied conditions list.
