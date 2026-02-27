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
	t.Run("nil resource returns now", func(t *testing.T) {
		before := time.Now().UTC()
		result := scriggoTransitionTime(nil, "Accepted", "True")
		after := time.Now().UTC()

		parsed, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
		assert.False(t, parsed.Before(before.Truncate(time.Second)))
		assert.False(t, parsed.After(after.Add(time.Second)))
	})

	t.Run("status unchanged preserves existing time", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":               "Accepted",
						"status":             "True",
						"lastTransitionTime": "2025-01-15T10:30:00Z",
					},
				},
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True")
		assert.Equal(t, "2025-01-15T10:30:00Z", result)
	})

	t.Run("status changed returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":               "Accepted",
						"status":             "True",
						"lastTransitionTime": "2025-01-15T10:30:00Z",
					},
				},
			},
		}

		before := time.Now().UTC()
		result := scriggoTransitionTime(resource, "Accepted", "False")
		after := time.Now().UTC()

		assert.NotEqual(t, "2025-01-15T10:30:00Z", result)
		parsed, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
		assert.False(t, parsed.Before(before.Truncate(time.Second)))
		assert.False(t, parsed.After(after.Add(time.Second)))
	})

	t.Run("condition not found returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":               "Ready",
						"status":             "True",
						"lastTransitionTime": "2025-01-15T10:30:00Z",
					},
				},
			},
		}

		before := time.Now().UTC()
		result := scriggoTransitionTime(resource, "Accepted", "True")
		after := time.Now().UTC()

		parsed, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
		assert.False(t, parsed.Before(before.Truncate(time.Second)))
		assert.False(t, parsed.After(after.Add(time.Second)))
	})

	t.Run("no status field returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test",
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True")
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("empty conditions returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{},
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True")
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("status unchanged but no lastTransitionTime returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Accepted",
						"status": "True",
					},
				},
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True")
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("multiple conditions finds matching one", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":               "Ready",
						"status":             "True",
						"lastTransitionTime": "2025-01-10T00:00:00Z",
					},
					map[string]interface{}{
						"type":               "Accepted",
						"status":             "True",
						"lastTransitionTime": "2025-01-15T10:30:00Z",
					},
					map[string]interface{}{
						"type":               "ResolvedRefs",
						"status":             "True",
						"lastTransitionTime": "2025-01-12T00:00:00Z",
					},
				},
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True")
		assert.Equal(t, "2025-01-15T10:30:00Z", result)
	})
}

func TestScriggoTransitionTime_ParentConditions(t *testing.T) {
	t.Run("finds condition in parent by index", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"parents": []interface{}{
					map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":               "Accepted",
								"status":             "True",
								"lastTransitionTime": "2025-02-01T00:00:00Z",
							},
						},
					},
					map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":               "Accepted",
								"status":             "False",
								"lastTransitionTime": "2025-03-01T00:00:00Z",
							},
						},
					},
				},
			},
		}

		// Parent 0: status unchanged → preserve
		result := scriggoTransitionTime(resource, "Accepted", "True", 0)
		assert.Equal(t, "2025-02-01T00:00:00Z", result)

		// Parent 1: status changed → now
		result = scriggoTransitionTime(resource, "Accepted", "True", 1)
		assert.NotEqual(t, "2025-03-01T00:00:00Z", result)
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("parent index out of range returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"parents": []interface{}{
					map[string]interface{}{
						"conditions": []interface{}{},
					},
				},
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True", 5)
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("negative parent index returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"parents": []interface{}{},
			},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True", -1)
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
	})

	t.Run("no parents field returns now", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{},
		}

		result := scriggoTransitionTime(resource, "Accepted", "True", 0)
		_, err := time.Parse(time.RFC3339, result)
		require.NoError(t, err)
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
		result := scriggoToJSON(map[string]interface{}{
			"key": "value",
		})
		assert.Contains(t, result, `"key"`)
		assert.Contains(t, result, `"value"`)
	})

	t.Run("slice", func(t *testing.T) {
		result := scriggoToJSON([]interface{}{"a", "b", "c"})
		assert.Equal(t, `["a","b","c"]`, result)
	})

	t.Run("nested structure", func(t *testing.T) {
		result := scriggoToJSON(map[string]interface{}{
			"loadBalancer": map[string]interface{}{
				"ingress": []interface{}{
					map[string]interface{}{"ip": "10.0.0.1"},
				},
			},
		})
		assert.Contains(t, result, `"ip":"10.0.0.1"`)
		assert.Contains(t, result, `"loadBalancer"`)
	})

	t.Run("empty map", func(t *testing.T) {
		assert.Equal(t, "{}", scriggoToJSON(map[string]interface{}{}))
	})

	t.Run("empty slice", func(t *testing.T) {
		assert.Equal(t, "[]", scriggoToJSON([]interface{}{}))
	})
}

func TestFindTopLevelConditions(t *testing.T) {
	t.Run("extracts conditions from status", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Ready"},
				},
			},
		}

		conditions := findTopLevelConditions(resource)
		require.Len(t, conditions, 1)
	})

	t.Run("returns nil for missing status", func(t *testing.T) {
		resource := map[string]interface{}{}
		assert.Nil(t, findTopLevelConditions(resource))
	})

	t.Run("returns nil for missing conditions", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{},
		}
		assert.Nil(t, findTopLevelConditions(resource))
	})

	t.Run("returns nil for non-slice conditions", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": "not-a-slice",
			},
		}
		assert.Nil(t, findTopLevelConditions(resource))
	})
}

func TestFindParentConditions(t *testing.T) {
	t.Run("extracts conditions from parent by index", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"parents": []interface{}{
					map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{"type": "Accepted"},
						},
					},
				},
			},
		}

		conditions := findParentConditions(resource, 0)
		require.Len(t, conditions, 1)
	})

	t.Run("returns nil for out of range index", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"parents": []interface{}{},
			},
		}
		assert.Nil(t, findParentConditions(resource, 0))
	})

	t.Run("returns nil for missing parents", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{},
		}
		assert.Nil(t, findParentConditions(resource, 0))
	})

	t.Run("returns nil for non-slice parents", func(t *testing.T) {
		resource := map[string]interface{}{
			"status": map[string]interface{}{
				"parents": "not-a-slice",
			},
		}
		assert.Nil(t, findParentConditions(resource, 0))
	})
}
