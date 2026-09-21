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

package diagnostics

import (
	"regexp"
	"slices"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var conditionIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,252}$`)

func resourceView(object *unstructured.Unstructured, watch string) Resource {
	result := Resource{
		Identity: Identity{
			APIVersion: object.GetAPIVersion(), Kind: object.GetKind(),
			Namespace: object.GetNamespace(), Name: object.GetName(),
			UID: string(object.GetUID()), Generation: object.GetGeneration(),
		},
		Watch: watch, Conditions: []Condition{},
	}
	collectConditions(object.Object["status"], "status", &result.Conditions)
	return result
}

func collectConditions(value any, path string, result *[]Condition) {
	switch node := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if !conditionIdentifier.MatchString(key) {
				continue
			}
			childPath := path + "." + key
			if key == "conditions" {
				appendConditions(node[key], childPath, result)
				continue
			}
			collectConditions(node[key], childPath, result)
		}
	case []any:
		for index, child := range node {
			collectConditions(child, path+"["+strconv.Itoa(index)+"]", result)
		}
	}
}

func appendConditions(value any, path string, result *[]Condition) {
	conditions, ok := value.([]any)
	if !ok {
		return
	}
	for index, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind, _, _ := unstructured.NestedString(condition, "type")
		status, _, _ := unstructured.NestedString(condition, "status")
		if !conditionIdentifier.MatchString(kind) || (status != "True" && status != "False" && status != "Unknown") {
			continue
		}
		reason, _, _ := unstructured.NestedString(condition, "reason")
		if !conditionIdentifier.MatchString(reason) {
			reason = ""
		}
		generation, _, _ := unstructured.NestedInt64(condition, "observedGeneration")
		transition, _, _ := unstructured.NestedString(condition, "lastTransitionTime")
		at, _ := time.Parse(time.RFC3339, transition)
		*result = append(*result, Condition{
			Path: path + "[" + strconv.Itoa(index) + "]", Type: kind,
			Status: status, Reason: reason, ObservedGeneration: generation, LastTransitionTime: at,
		})
	}
}
