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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// getStatusPatchCollector retrieves the StatusPatchCollector from the template render context.
func getStatusPatchCollector(env native.Env) *StatusPatchCollector {
	return getRenderContextValue[StatusPatchCollector](env, "statusPatchCollector")
}

// scriggoStatusPatch registers a status patch during template rendering.
// The function is resource-agnostic: apiVersion/kind/namespace/name and the
// variant payload are all supplied by the template, so it works identically for
// any watched resource or CRD.
//
// Usage in Scriggo templates (apiVersion/kind are placeholders — substitute
// whatever resource the chart is patching):
//
//	{% statusPatch("default", "my-object", "example.com/v1", "Widget",
//	    map[string]any{
//	        "deployed": map[string]any{
//	            "conditions": conditions,
//	        },
//	    }) %}
func scriggoStatusPatch(env native.Env, namespace, name, apiVersion, kind string, variants map[string]any) string {
	collector := getStatusPatchCollector(env)
	if collector == nil {
		env.Stop(errors.New("statusPatch: statusPatchCollector not available in render context"))
		return ""
	}

	// Convert variants from map[string]any to map[string]map[string]any
	typedVariants := make(map[string]map[string]any, len(variants))
	for phase, val := range variants {
		statusMap, ok := val.(map[string]any)
		if !ok {
			env.Stop(fmt.Errorf("statusPatch: variant %q must be a map[string]any, got %T", phase, val))
			return ""
		}
		typedVariants[phase] = statusMap
	}

	if err := collector.Register(namespace, name, apiVersion, kind, typedVariants); err != nil {
		env.Stop(fmt.Errorf("statusPatch: %w", err))
		return ""
	}

	return "" // Side-effect only, no output
}

// scriggoCondition constructs a map matching the metav1.Condition structure.
//
// Usage in Scriggo templates:
//
//	{% var cond = condition("Accepted", "True", "Accepted", "Route accepted", 5, "2025-01-01T00:00:00Z") %}
func scriggoCondition(condType, status, reason, message string, observedGeneration any, lastTransitionTime string) map[string]any {
	// Normalize observedGeneration to int64. Source can be:
	//   - int / int64 / float64: legacy untyped path + chart-built literals
	//   - *int64 / *int / *int32 / *float64: typegen tristate (issue #52)
	//     — optional numeric fields like ObjectMeta.Generation pointer-
	//     wrap so the chart's `dig | fallback` pattern can distinguish
	//     absent from explicit zero. Direct typed access like
	//     `gateway.Metadata.Generation` hands the raw pointer here;
	//     derefTristateScalar unwraps it the same way digStructField does.
	if d, ok := derefTristateScalar(observedGeneration); ok {
		observedGeneration = d
	}
	var gen int64
	switch v := observedGeneration.(type) {
	case int:
		gen = int64(v)
	case int64:
		gen = v
	case float64:
		gen = int64(math.Round(v))
	case nil:
		gen = 0
	default:
		gen = 0
	}

	return map[string]any{
		"type":               condType,
		"status":             status,
		"reason":             reason,
		"message":            message,
		"observedGeneration": gen,
		"lastTransitionTime": lastTransitionTime,
	}
}

// scriggoTransitionTime determines the correct lastTransitionTime for a
// metav1.Condition-shaped entry given the existing list of conditions for
// the target field. Resource-agnostic: the caller is responsible for
// navigating to the correct conditions list with `dig` / index lookups —
// the templating layer never assumes any specific resource shape.
//
// Behaviour:
//   - if existingConditions contains an entry whose `type` matches
//     conditionType and whose `status` matches newStatus, the existing
//     `lastTransitionTime` is returned (preserves the original transition
//     timestamp through unchanged renders, which is what the
//     metav1.Condition spec requires and what status-patch dedup needs).
//   - otherwise the current time is returned (status changed, condition
//     is new, or the resource has no prior conditions).
//
// existingConditions is typed as `any` for caller ergonomics — Scriggo's
// `dig` returns `any`, and the helper accepts both `nil` (no prior
// conditions) and `[]any` (the conventional shape for unstructured
// metav1.Condition lists).
//
// Usage in Scriggo templates:
//
//	{# top-level: .status.conditions #}
//	{%% var tt = transitionTime(dig(resource, "status", "conditions"),
//	                           "Accepted", "True") %%}
//
//	{# nested: .status.listeners[i].conditions — caller navigates first #}
//	{%% var tt = transitionTime(dig(listener, "conditions"),
//	                           "Programmed", "True") %%}
func scriggoTransitionTime(existingConditions any, conditionType, newStatus string) string {
	now := time.Now().UTC().Format(time.RFC3339)

	condSlice, ok := existingConditions.([]any)
	if !ok {
		return now
	}

	for _, c := range condSlice {
		condMap, ok := c.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := condMap["type"].(string)
		if ct != conditionType {
			continue
		}
		existingStatus, _ := condMap["status"].(string)
		if existingStatus == newStatus {
			if existingTime, ok := condMap["lastTransitionTime"].(string); ok && existingTime != "" {
				return existingTime
			}
		}
		// Status changed or no existing time — return now.
		return now
	}

	// Condition not found — treat as newly added.
	return now
}

// scriggoToJSON serializes any Go value to a JSON string.
//
// Usage in Scriggo templates:
//
//	{{ myMap | toJSON }}
//	{{ toJSON("hello") }}
func scriggoToJSON(value any) string {
	if value == nil {
		return "null"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
