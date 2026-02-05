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

package indexer

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// ConvertResource converts an unstructured resource to a map with floats converted to ints.
//
// This function is used during resource storage to ensure all resources are stored
// in a template-friendly format. The conversion happens once when resources are
// added/updated in stores, rather than on every access during template rendering.
//
// The returned map can be used directly in templates for field access like:
//
//	resource["metadata"]["name"]
//	resource["spec"]["rules"]
//
// Returns nil if the resource type is not supported.
func ConvertResource(resource interface{}) map[string]interface{} {
	switch r := resource.(type) {
	case *unstructured.Unstructured:
		return convertFloatsToInts(r.Object).(map[string]interface{})
	case map[string]interface{}:
		return convertFloatsToInts(r).(map[string]interface{})
	default:
		return nil
	}
}

// convertFloatsToInts recursively converts float64 values to int64 where they
// have no fractional part. Mutation is performed in-place to avoid allocating
// new maps and slices at each nesting level. This is safe because resources are
// freshly deserialized from K8s watch events and owned by us.
//
// This is necessary because JSON unmarshaling converts all numbers to float64
// when the target type is interface{}. For Kubernetes resources, this causes
// integer fields like ports (80) to appear as floats (80.0) in templates.
//
// HAProxy configuration syntax requires integers (port 80), not floats (port 80.0).
// This conversion ensures valid HAProxy configs are generated.
//
// The conversion is safe for Kubernetes resources because:
//   - Integer fields (ports, replicas, counts) won't have fractional parts
//   - Float fields typically use resource.Quantity (string-based, e.g., "0.5 CPU")
//   - Converting 3.0 → 3 doesn't break any semantic meaning
//
// Examples:
//   - 80.0 → 80 (port number)
//   - 3.14 → 3.14 (preserved as-is)
//   - "string" → "string" (unchanged)
//   - nested maps/slices processed recursively
func convertFloatsToInts(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		// Mutate map values in-place (safe: resources are freshly deserialized and owned by us)
		for k, val := range v {
			v[k] = convertFloatsToInts(val)
		}
		return v

	case []interface{}:
		// Mutate slice elements in-place
		for i, val := range v {
			v[i] = convertFloatsToInts(val)
		}
		return v

	case float64:
		// Convert to int64 if it's a whole number
		if v == float64(int64(v)) {
			return int64(v)
		}
		return v

	default:
		// Return other types unchanged
		return v
	}
}
