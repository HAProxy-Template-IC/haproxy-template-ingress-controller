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
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
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

// buildConditionedResourceType produces a typegen reflect.Type mirroring
// the minimal shape every Gateway API status carries: `.status.conditions[]`
// of metav1.Condition. Property names and required flags mirror the real
// GatewayClass CRD schema (tests/schemas/gateway.networking.k8s.io_gatewayclasses.yaml)
// so the generated struct matches what production hands to templates when
// schemas are loaded live from the kube-apiserver.
func buildConditionedResourceType(t *testing.T) reflect.Type {
	t.Helper()
	stringType := spec.StringProperty()
	intType := spec.Int64Property()
	conditionSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type:     spec.StringOrArray{"object"},
			Required: []string{"lastTransitionTime", "message", "reason", "status", "type"},
			Properties: map[string]spec.Schema{
				"type":               *stringType,
				"status":             *stringType,
				"reason":             *stringType,
				"message":            *stringType,
				"observedGeneration": *intType,
				"lastTransitionTime": *stringType,
			},
		},
	}
	statusSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"conditions": {
					SchemaProps: spec.SchemaProps{
						Type:  spec.StringOrArray{"array"},
						Items: &spec.SchemaOrArray{Schema: &conditionSchema},
					},
				},
			},
		},
	}
	resourceSchema := spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"status": statusSchema,
			},
		},
	}
	converter := typegen.NewConverter(nil)
	rt, err := converter.Convert(&resourceSchema)
	require.NoError(t, err, "typegen.Convert must succeed for the synthetic conditioned-resource schema")
	return rt
}

// TestScriggoTransitionTime_TypedConditions is the reproduction for issue
// #63: in production, watched resources arrive as typegen-built typed
// structs, so `dig(resource, "status", "conditions")` returns a typed
// struct slice — not the `[]any` of `map[string]any` the fixture-only
// offline path produces. scriggoTransitionTime must preserve the existing
// lastTransitionTime against BOTH shapes; when it only handled the map
// shape it silently returned `now` on every render, re-stamping the
// GatewayClass Accepted condition ~1/s (each SSA write bumped
// resourceVersion, fed a watch event back in, and re-triggered
// reconciliation).
func TestScriggoTransitionTime_TypedConditions(t *testing.T) {
	rt := buildConditionedResourceType(t)
	obj := map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{
					"type":               "Ready",
					"status":             "True",
					"reason":             "Ready",
					"message":            "ready",
					"observedGeneration": int64(1),
					"lastTransitionTime": "2019-06-01T00:00:00Z",
				},
				map[string]any{
					"type":               "Accepted",
					"status":             "True",
					"reason":             "Accepted",
					"message":            "accepted",
					"observedGeneration": int64(1),
					"lastTransitionTime": "2020-01-01T00:00:00Z",
				},
			},
		},
	}
	v, err := typegen.WrapInto(obj, rt)
	require.NoError(t, err, "typegen.WrapInto must succeed for the sample resource")
	ptr := reflect.New(rt)
	ptr.Elem().Set(v)
	typed := ptr.Interface() // *T — the shape production hands to templates

	// This is exactly what the chart's TopLevelTransitionTime macro passes:
	// dig(resource, "status", "conditions"). Against a typed struct the
	// result is the typegen struct slice, NOT []any.
	conds := scriggoDig(typed, "status", "conditions")
	require.NotNil(t, conds, "dig must navigate the typed struct to the conditions slice")
	_, isAnySlice := conds.([]any)
	require.False(t, isAnySlice,
		"precondition: dig on the typed struct must yield a typed slice — otherwise this test is not exercising the production shape")

	t.Run("typed conditions slice preserves existing time when status unchanged", func(t *testing.T) {
		got := scriggoTransitionTime(conds, "Accepted", "True")
		assert.Equal(t, "2020-01-01T00:00:00Z", got,
			"transitionTime must preserve the existing lastTransitionTime for an unchanged status on the typed shape (TopLevelTransitionTime path)")
	})

	t.Run("toSlice-wrapped typed elements preserve existing time", func(t *testing.T) {
		// The chart's ListenerTransitionTime / RouteParentTransitionTime
		// macros pipe through toSlice(), producing []any whose elements
		// are still typed structs.
		got := scriggoTransitionTime(scriggoToSlice(conds), "Accepted", "True")
		assert.Equal(t, "2020-01-01T00:00:00Z", got,
			"transitionTime must preserve the existing lastTransitionTime when the conditions arrive as []any of typed structs (listener / route-parent path)")
	})

	t.Run("typed conditions status changed returns now", func(t *testing.T) {
		got := scriggoTransitionTime(conds, "Accepted", "False")
		assert.NotEqual(t, "2020-01-01T00:00:00Z", got)
		_, err := time.Parse(time.RFC3339, got)
		require.NoError(t, err)
	})

	t.Run("typed conditions type not found returns now", func(t *testing.T) {
		got := scriggoTransitionTime(conds, "Programmed", "True")
		assert.NotEqual(t, "2020-01-01T00:00:00Z", got)
		assert.NotEqual(t, "2019-06-01T00:00:00Z", got)
		_, err := time.Parse(time.RFC3339, got)
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

	t.Run("marshal failure aborts", func(t *testing.T) {
		assert.Panics(t, func() { scriggoToJSON(make(chan int)) })
	})
}

// Note: the previous TestFindTopLevelConditions / TestFindParentConditions
// tests are gone with the helpers themselves. They were resource-shape-coupled
// (knew about .status.conditions, .status.parents[i].conditions) — which
// violates pkg/templating's resource-agnostic contract. Equivalent navigation
// now lives in chart-side macros (charts/haptic/libraries/gateway.yaml's
// `util-condition-transition-time` library) where Gateway-API knowledge
// belongs; the Go helper just compares a supplied conditions list.
