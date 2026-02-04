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

package rendercontext

import (
	"context"
	"fmt"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
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

// StoreWrapper wraps a stores.Store to provide template-friendly methods
// that don't return errors (errors are logged instead).
//
// This allows templates to call methods like List() and Get() directly
// without having to handle Go's multi-return values.
//
// Resources in stores are already converted (floats to ints) at storage time,
// so StoreWrapper simply passes through the data without additional processing.
type StoreWrapper struct {
	Store        stores.Store
	ResourceType string
	Logger       *slog.Logger
}

// List returns all resources in the store.
//
// This method is intended for template iteration:
//
//	{% for ingress in resources.ingresses.List() %}
//	  {{ ingress.metadata.name }}
//	{% endfor %}
//
// Resources are already converted (floats to ints) at storage time,
// so this method simply returns the store contents directly.
//
// If an error occurs, it's logged and an empty slice is returned.
func (w *StoreWrapper) List() []interface{} {
	items, err := w.Store.List()
	if err != nil {
		w.Logger.Warn("failed to list resources from store",
			"resource_type", w.ResourceType,
			"error", err)
		return []interface{}{}
	}

	w.Logger.Log(context.Background(), logging.LevelTrace, "store list called",
		"resource_type", w.ResourceType,
		"count", len(items))

	return items
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

	w.Logger.Log(context.Background(), logging.LevelTrace, "store fetch called",
		"resource_type", w.ResourceType,
		"keys", keys,
		"found_count", len(items))

	return items
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

	w.Logger.Log(context.Background(), logging.LevelTrace, "store GetSingle called",
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
	return items[0]
}
