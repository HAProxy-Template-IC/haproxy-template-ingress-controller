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

package resourcestore

import (
	"fmt"
)

// OverlayStore wraps a base store and simulates a single resource change.
//
// This implements the shadow/overlay pattern for memory-efficient dry-run
// validation. Instead of copying the entire store, it only tracks the delta:
// - For CREATE/UPDATE: stores the single changed resource
// - For DELETE: marks the resource as deleted
//
// Memory usage: O(1) - only the single changed resource (~10KB)
// Performance: O(1) for Get with matching key, O(n) for List where n = base store size
//
// The overlay is immutable after creation and thread-safe for reads.
type OverlayStore struct {
	baseStore Store
	namespace string
	name      string
	object    interface{} // The changed resource (nil for DELETE)
	operation Operation
}

// NewOverlayStore creates a new overlay store.
//
// Parameters:
//   - baseStore: The underlying store to wrap (not copied)
//   - namespace: Namespace of the changed resource
//   - name: Name of the changed resource
//   - obj: The resource object (nil for DELETE operations)
//   - op: The operation type
//
// Returns:
//   - An immutable overlay store
func NewOverlayStore(baseStore Store, namespace, name string, obj interface{}, op Operation) *OverlayStore {
	return &OverlayStore{
		baseStore: baseStore,
		namespace: namespace,
		name:      name,
		object:    obj,
		operation: op,
	}
}

// Get retrieves resources matching the provided index keys.
//
// It checks the overlay first and falls back to the base store if needed.
//
// Behavior:
// - If keys match the changed resource: returns the overlay object (or empty for DELETE)
// - Otherwise: falls back to the base store.
//
// Performance: O(1) for overlay hit, O(k) for base store where k = number of matches
func (o *OverlayStore) Get(keys ...string) ([]interface{}, error) {
	// Check if the keys match our overlay resource
	// Assumes index_by: ["metadata.namespace", "metadata.name"]
	if len(keys) >= 2 && keys[0] == o.namespace && keys[1] == o.name {
		switch o.operation {
		case OperationDelete:
			// Resource is deleted in overlay - return empty
			return []interface{}{}, nil

		case OperationCreate, OperationUpdate:
			// Return the overlay object
			return []interface{}{o.object}, nil
		}
	}

	// Fall back to base store
	return o.baseStore.Get(keys...)
}

// List returns all resources with the overlay change applied.
//
// It combines resources from the base store with the overlay modification.
//
// Behavior:
// - For DELETE: excludes the deleted resource
// - For UPDATE: replaces the resource with the overlay version
// - FOR CREATE: adds the new resource.
//
// Performance: O(n) where n = number of resources in base store
func (o *OverlayStore) List() ([]interface{}, error) {
	baseResources, err := o.baseStore.List()
	if err != nil {
		return nil, err
	}

	result := make([]interface{}, 0, len(baseResources)+1)
	foundInBase := false

	// Process base store resources
	for _, resource := range baseResources {
		ns, name := extractMetadata(resource)

		// Check if this is the overlay resource
		if ns == o.namespace && name == o.name {
			foundInBase = true

			switch o.operation {
			case OperationDelete:
				// Skip deleted resource
				continue

			case OperationUpdate:
				// Replace with overlay version
				result = append(result, o.object)
				continue

			case OperationCreate:
				// Shouldn't exist in base store, but include overlay if it does
				result = append(result, o.object)
				continue
			}
		}

		// Include unmodified resource
		result = append(result, resource)
	}

	// For CREATE, add the new resource if not already found in base store
	if o.operation == OperationCreate && !foundInBase {
		result = append(result, o.object)
	}

	return result, nil
}

// Add is not supported on overlay stores (read-only).
func (o *OverlayStore) Add(resource interface{}, keys []string) error {
	return fmt.Errorf("overlay store is read-only: Add not supported")
}

// Update is not supported on overlay stores (read-only).
func (o *OverlayStore) Update(resource interface{}, keys []string) error {
	return fmt.Errorf("overlay store is read-only: Update not supported")
}

// Delete is not supported on overlay stores (read-only).
func (o *OverlayStore) Delete(keys ...string) error {
	return fmt.Errorf("overlay store is read-only: Delete not supported")
}

// Clear is not supported on overlay stores (read-only).
func (o *OverlayStore) Clear() error {
	return fmt.Errorf("overlay store is read-only: Clear not supported")
}

// extractMetadata extracts namespace and name from a Kubernetes resource.
//
// Handles both Unstructured objects (from base store) and map[string]interface{} (from overlay).
//
// Parameters:
//   - resource: The resource object (Unstructured or map[string]interface{})
//
// Returns:
//   - namespace: The resource namespace (empty string if not found)
//   - name: The resource name (empty string if not found)
func extractMetadata(resource interface{}) (namespace, name string) {
	// First, try to get the content map from Unstructured
	var content map[string]interface{}

	// Type assert to interface with UnstructuredContent method (handles k8s Unstructured)
	type unstructuredInterface interface {
		UnstructuredContent() map[string]interface{}
	}

	switch v := resource.(type) {
	case unstructuredInterface:
		content = v.UnstructuredContent()
	case map[string]interface{}:
		// Already a map (overlay object)
		content = v
	default:
		// Unknown type
		return "", ""
	}

	// Extract metadata from the content map
	metadata, ok := content["metadata"].(map[string]interface{})
	if !ok {
		return "", ""
	}

	if ns, ok := metadata["namespace"].(string); ok {
		namespace = ns
	}

	if n, ok := metadata["name"].(string); ok {
		name = n
	}

	return namespace, name
}
