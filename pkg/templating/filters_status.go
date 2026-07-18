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

// getEventCollector retrieves the EventCollector from the template render context.
func getEventCollector(env native.Env) *EventCollector {
	return getRenderContextValue[EventCollector](env, "recordEventCollector")
}

// scriggoRecordEvent registers a Kubernetes Warning Event during template
// rendering. It is resource-agnostic: the involved object's
// namespace/name/apiVersion/kind are read off the passed resource via the same
// dig navigation typed access uses, so it emits against any watched resource or
// CRD without a typed client (RULE #1). Duplicate (resource, reason, message)
// tuples collapse to one event.
//
// The resource argument is any watched-resource value — a typed
// `*resources.<name>.T`, a `map[string]any`, or an *unstructured.Unstructured;
// callers pass the object they already have (e.g. an item from
// resources.ingresses.List()) rather than restating its identity.
//
// Usage in Scriggo templates:
//
//	{% recordEvent(ingress, "RouteConflict",
//	    "host \"x\" path \"/\" is already served by another Ingress") %}
func scriggoRecordEvent(env native.Env, resource any, reason, message string) string {
	// recordEvent is a best-effort observability signal, so — unlike
	// statusPatch, whose status output is load-bearing — it never aborts the
	// render. A missing collector (engine wired without event support) or an
	// invalid argument (empty required field) simply drops the event rather
	// than failing the config render and taking down the data path for a
	// non-critical Event.
	collector := getEventCollector(env)
	if collector == nil {
		return ""
	}
	namespace := scriggoDigString(resource, "", "metadata", "namespace")
	name := scriggoDigString(resource, "", "metadata", "name")
	apiVersion := scriggoDigString(resource, "", "apiVersion")
	kind := scriggoDigString(resource, "", "kind")
	_ = collector.Register(namespace, name, apiVersion, kind, EventTypeWarning, reason, message)
	return "" // Side-effect only, no output
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
	// Record the call-site template + line for provenance (playground jump-to-source).
	// Best-effort: CallPath/CallLine are only meaningful on the main render goroutine.
	collector.SetSource(namespace, name, apiVersion, kind, env.CallPath(), env.CallLine())

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
// `dig` returns `any`, and the helper accepts every shape a conditions
// list arrives in:
//   - nil (no prior conditions)
//   - `[]any` of `map[string]any` (the unstructured / fixture shape)
//   - a typegen-built typed struct slice, or `[]any` whose elements are
//     typed structs (the production shape — `dig` on a typed watched
//     resource returns the typed slice, and the chart's `| toSlice()`
//     wrapping keeps the elements typed). Navigation into the entries
//     goes through the same dig contract as everywhere else (JSON-tag
//     field lookup on structs, key lookup on maps), so both shapes
//     behave identically. Type-asserting `map[string]any` here instead
//     silently returned `now` on every render for typed resources,
//     re-stamping lastTransitionTime and feeding a status-write →
//     watch-event → re-render loop (issue #63).
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

	condSlice, ok := toSlice(existingConditions)
	if !ok {
		return now
	}

	for _, c := range condSlice {
		ct, _ := scriggoDig(c, "type").(string)
		if ct != conditionType {
			continue
		}
		existingStatus, _ := scriggoDig(c, "status").(string)
		if existingStatus == newStatus {
			if existingTime, ok := scriggoDig(c, "lastTransitionTime").(string); ok && existingTime != "" {
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
