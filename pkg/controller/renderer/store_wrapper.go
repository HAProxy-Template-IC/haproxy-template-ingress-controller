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

package renderer

import (
	"fmt"
	"log/slog"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/types"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/templating"
)

// Verify StoreWrapper implements templating.ResourceStore at compile time.
// This enables Scriggo templates to call methods directly on resource stores:
//
//	{% for _, ing := range resources.ingresses.List() %}
var _ templating.ResourceStore = (*StoreWrapper)(nil)

// toString converts various types to string for template compatibility.
//
// This helper handles type conversions:
// - string: returned as-is
// - fmt.Stringer: any type with String() method
// - other types: formatted using fmt.Sprintf
//
// This allows template methods to accept interface{} arguments.
func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		// Fast path for regular strings
		return val
	case fmt.Stringer:
		// Handles types with String() method
		return val.String()
	default:
		// Fallback: format as string
		return fmt.Sprintf("%v", v)
	}
}

// StoreWrapper wraps a types.Store to provide template-friendly methods
// that don't return errors (errors are logged instead).
//
// This allows templates to call methods like List() and Get() directly
// without having to handle Go's multi-return values.
//
// The wrapper implements lazy-cached unwrapping:
//   - List() results are unwrapped once on first call and cached for the reconciliation
//   - Get() results are unwrapped on-demand (typically small result sets)
type StoreWrapper struct {
	Store        types.Store
	ResourceType string
	Logger       *slog.Logger

	// Lazy cache for List() results
	CachedList []interface{}
	ListCached bool
}

// List returns all resources in the store.
//
// This method is intended for template iteration:
//
//	{% for ingress in resources.ingresses.List() %}
//	  {{ ingress.metadata.name }}
//	{% endfor %}
//
// Resources are unwrapped from unstructured.Unstructured to maps on first call
// and cached for subsequent calls within the same reconciliation cycle.
//
// If an error occurs, it's logged and an empty slice is returned.
func (w *StoreWrapper) List() []interface{} {
	// Return cached result if already unwrapped
	if w.ListCached {
		w.Logger.Debug("returning cached list",
			"resource_type", w.ResourceType,
			"count", len(w.CachedList))
		return w.CachedList
	}

	// First call - fetch from store
	items, err := w.Store.List()
	if err != nil {
		w.Logger.Warn("failed to list resources from store",
			"resource_type", w.ResourceType,
			"error", err)
		return []interface{}{}
	}

	w.Logger.Debug("unwrapping and caching list",
		"resource_type", w.ResourceType,
		"count", len(items))

	// Unwrap unstructured resources to maps for template access
	unwrapped := make([]interface{}, len(items))
	for i, item := range items {
		unwrapped[i] = unwrapUnstructured(item)
	}

	// Cache for subsequent calls
	w.CachedList = unwrapped
	w.ListCached = true

	return unwrapped
}

// Fetch performs O(1) indexed lookup using the provided keys.
//
// This method enables efficient lookups in templates and supports non-unique index keys
// by returning all resources matching the provided keys:
//
//	{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
//	  {{ endpoint_slice.metadata.name }}
//	{% endfor %}
//
// The keys must match the index configuration for the resource type.
// For example, if EndpointSlices are indexed by service name:
//
//	index_by: ["metadata.labels['kubernetes.io/service-name']"]
//
// Then you can look them up with:
//
//	resources.endpoints.Fetch("my-service")
//
// This will return ALL EndpointSlices for that service (typically multiple).
//
// Accepts interface{} arguments for template compatibility.
//
// If an error occurs, it's logged and an empty slice is returned.
func (w *StoreWrapper) Fetch(keys ...interface{}) []interface{} {
	// Convert interface{} arguments to strings
	stringKeys := make([]string, len(keys))
	for i, key := range keys {
		stringKeys[i] = toString(key)
	}

	items, err := w.Store.Get(stringKeys...)
	if err != nil {
		w.Logger.Warn("failed to fetch indexed resources from store",
			"resource_type", w.ResourceType,
			"keys", keys,
			"error", err)
		return []interface{}{}
	}

	w.Logger.Debug("store fetch called",
		"resource_type", w.ResourceType,
		"keys", keys,
		"found_count", len(items))

	// Unwrap unstructured resources to maps for template access
	unwrapped := make([]interface{}, len(items))
	for i, item := range items {
		unwrapped[i] = unwrapUnstructured(item)
	}

	return unwrapped
}

// GetSingle performs O(1) indexed lookup and expects exactly one matching resource.
//
// This method is useful when you know the index keys uniquely identify a resource:
//
//	{% set ingress = resources.ingresses.GetSingle("default", "my-ingress") %}
//	{% if ingress %}
//	  {{ ingress.metadata.name }}
//	{% endif %}
//
//	{# Cross-namespace reference #}
//	{% set ref = "namespace/name".split("/") %}
//	{% set secret = resources.secrets.GetSingle(ref[0], ref[1]) %}
//
// Accepts interface{} arguments for template compatibility.
//
// Returns:
//   - nil if no resources match (this is NOT an error - allows templates to check existence)
//   - The single matching resource if exactly one matches
//   - nil + logs error if multiple resources match (ambiguous lookup)
//
// If an error occurs during the store operation, it's logged and nil is returned.
func (w *StoreWrapper) GetSingle(keys ...interface{}) interface{} {
	// Convert interface{} arguments to strings
	stringKeys := make([]string, len(keys))
	for i, key := range keys {
		stringKeys[i] = toString(key)
	}

	items, err := w.Store.Get(stringKeys...)
	if err != nil {
		w.Logger.Warn("failed to get single resource from store",
			"resource_type", w.ResourceType,
			"keys", keys,
			"error", err)
		return nil
	}

	w.Logger.Debug("store GetSingle called",
		"resource_type", w.ResourceType,
		"keys", keys,
		"found_count", len(items))

	if len(items) == 0 {
		// No resources found - this is valid, not an error
		return nil
	}

	if len(items) > 1 {
		// Ambiguous lookup - multiple resources match
		w.Logger.Error("GetSingle found multiple resources (ambiguous lookup)",
			"resource_type", w.ResourceType,
			"keys", keys,
			"count", len(items))
		return nil
	}

	// Exactly one resource found
	return unwrapUnstructured(items[0])
}

// unwrapUnstructured extracts the underlying data map from unstructured.Unstructured.
//
// Templates need to access resource fields like ingress.spec.rules, but Kubernetes
// resources are stored as unstructured.Unstructured objects. This function converts
// them to plain maps so templates can access fields naturally.
//
// It also converts float64 values without fractional parts to int64 for better
// template rendering (e.g., port 80.0 becomes 80 instead of rendering as "80.0").
func unwrapUnstructured(resource interface{}) interface{} {
	// Type assert to interface with UnstructuredContent method
	type unstructuredInterface interface {
		UnstructuredContent() map[string]interface{}
	}

	if u, ok := resource.(unstructuredInterface); ok {
		content := u.UnstructuredContent()
		return convertFloatsToInts(content)
	}

	// Not an unstructured object, return as-is
	return resource
}

// convertFloatsToInts recursively converts float64 values to int64 where they
// have no fractional part.
//
// This is necessary because JSON unmarshaling converts all numbers to float64
// when the target type is interface{}. For Kubernetes resources, this causes
// integer fields like ports (80) to appear as floats (80.0) in templates.
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
		// Recursively process map values
		result := make(map[string]interface{}, len(v))
		for k, val := range v {
			result[k] = convertFloatsToInts(val)
		}
		return result

	case []interface{}:
		// Recursively process slice elements
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = convertFloatsToInts(val)
		}
		return result

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
