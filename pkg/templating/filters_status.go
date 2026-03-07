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
	"fmt"
	"math"
	"time"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// getStatusPatchCollector retrieves the StatusPatchCollector from the template render context.
func getStatusPatchCollector(env native.Env) *StatusPatchCollector {
	ctx := env.Context()
	if ctx == nil {
		return nil
	}
	renderCtx, ok := ctx.Value(RenderContextContextKey).(map[string]any)
	if !ok {
		return nil
	}
	collector, ok := renderCtx["statusPatchCollector"].(*StatusPatchCollector)
	if !ok {
		return nil
	}
	return collector
}

// scriggoStatusPatch registers a status patch during template rendering.
//
// Usage in Scriggo templates:
//
//	{% statusPatch("default", "my-ingress", "networking.k8s.io/v1", "Ingress",
//	    map[string]interface{}{
//	        "deployed": map[string]interface{}{
//	            "loadBalancer": map[string]interface{}{"ingress": addresses},
//	        },
//	    }) %}
func scriggoStatusPatch(env native.Env, namespace, name, apiVersion, kind string, variants map[string]any) string {
	collector := getStatusPatchCollector(env)
	if collector == nil {
		env.Stop(fmt.Errorf("statusPatch: statusPatchCollector not available in render context"))
		return ""
	}

	// Convert variants from map[string]interface{} to map[string]map[string]interface{}
	typedVariants := make(map[string]map[string]any, len(variants))
	for phase, val := range variants {
		statusMap, ok := val.(map[string]any)
		if !ok {
			env.Stop(fmt.Errorf("statusPatch: variant %q must be a map[string]interface{}, got %T", phase, val))
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
	// Normalize observedGeneration to int64 (JSON numbers from K8s come as float64)
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

// scriggoTransitionTime determines the correct lastTransitionTime for a condition.
// If the condition's status hasn't changed, the existing transition time is preserved.
// Otherwise, the current time is returned.
//
// Usage in Scriggo templates:
//
//	{% var tt = transitionTime(resource, "Accepted", "True") %}
//	{% var tt = transitionTime(resource, "Accepted", "True", 0) %}  // with parentIndex for route status
func scriggoTransitionTime(resource any, conditionType, newStatus string, parentIndex ...int) string {
	now := time.Now().UTC().Format(time.RFC3339)

	if resource == nil {
		return now
	}

	var conditions []any

	if len(parentIndex) > 0 {
		// Search in .status.parents[parentIndex].conditions
		conditions = findParentConditions(resource, parentIndex[0])
	} else {
		// Search in .status.conditions
		conditions = findTopLevelConditions(resource)
	}

	for _, c := range conditions {
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
		// Status changed or no existing time - return now
		return now
	}

	// Condition not found - new condition
	return now
}

// findTopLevelConditions extracts .status.conditions from a resource.
func findTopLevelConditions(resource any) []any {
	status := scriggoDig(resource, "status")
	if status == nil {
		return nil
	}
	conditions := scriggoDig(status, "conditions")
	if conditions == nil {
		return nil
	}
	condSlice, ok := conditions.([]any)
	if !ok {
		return nil
	}
	return condSlice
}

// findParentConditions extracts .status.parents[idx].conditions from a route resource.
func findParentConditions(resource any, idx int) []any {
	status := scriggoDig(resource, "status")
	if status == nil {
		return nil
	}
	parents := scriggoDig(status, "parents")
	if parents == nil {
		return nil
	}
	parentSlice, ok := parents.([]any)
	if !ok {
		return nil
	}
	if idx < 0 || idx >= len(parentSlice) {
		return nil
	}
	parent := parentSlice[idx]
	conditions := scriggoDig(parent, "conditions")
	if conditions == nil {
		return nil
	}
	condSlice, ok := conditions.([]any)
	if !ok {
		return nil
	}
	return condSlice
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
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}
