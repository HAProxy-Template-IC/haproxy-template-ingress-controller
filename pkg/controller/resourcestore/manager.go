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
	"sync"
)

// Manager provides centralized management of resource stores.
//
// This is a pure component with no event dependencies. It acts as a registry
// for all resource stores in the controller, allowing components to access
// stores and create overlay stores for dry-run operations.
//
// Thread-safe for concurrent access from multiple components.
type Manager struct {
	stores map[string]Store // Key: resource type (e.g., "ingresses", "services")
	mu     sync.RWMutex
}

// NewManager creates a new resource store manager.
func NewManager() *Manager {
	return &Manager{
		stores: make(map[string]Store),
	}
}

// RegisterStore registers a resource store for a given resource type.
//
// Parameters:
//   - resourceType: The resource type identifier (e.g., "ingresses", "services")
//   - store: The store implementation to register
//
// If a store for the resource type already exists, it will be replaced.
func (m *Manager) RegisterStore(resourceType string, store Store) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stores[resourceType] = store
}

// GetStore retrieves a registered store for a resource type.
//
// Parameters:
//   - resourceType: The resource type identifier
//
// Returns:
//   - The store for the resource type
//   - A boolean indicating whether the store exists
func (m *Manager) GetStore(resourceType string) (Store, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	store, exists := m.stores[resourceType]
	return store, exists
}

// GetAllStores returns a copy of all registered stores.
//
// Returns:
//   - A map of resource type to store
//
// The returned map is a shallow copy, safe for iteration without holding locks.
func (m *Manager) GetAllStores() map[string]Store {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create shallow copy
	result := make(map[string]Store, len(m.stores))
	for k, v := range m.stores {
		result[k] = v
	}

	return result
}

// CreateOverlay creates an overlay store that simulates a resource change.
//
// The overlay store wraps the base store and only tracks the delta (single
// changed resource). This is memory-efficient: O(1) space regardless of
// the number of resources in the base store.
//
// Parameters:
//   - resourceType: The resource type (e.g., "ingresses")
//   - namespace: Resource namespace (empty for cluster-scoped)
//   - name: Resource name
//   - obj: The resource object (for CREATE/UPDATE) or nil (for DELETE)
//   - op: The operation type (CREATE, UPDATE, DELETE)
//
// Returns:
//   - An overlay store wrapping the base store with the simulated change
//   - An error if the base store doesn't exist
//
// Example:
//
//	// Simulate updating an ingress
//	overlay, err := manager.CreateOverlay("ingresses", "default", "my-ing", updatedIngress, OperationUpdate)
//	if err != nil {
//	    return err
//	}
//
//	// Use overlay for dry-run validation
//	resources, _ := overlay.List() // Includes updated ingress
func (m *Manager) CreateOverlay(resourceType, namespace, name string, obj interface{}, op Operation) (Store, error) {
	baseStore, exists := m.GetStore(resourceType)
	if !exists {
		return nil, fmt.Errorf("no store registered for resource type: %s", resourceType)
	}

	return NewOverlayStore(baseStore, namespace, name, obj, op), nil
}

// CreateOverlayMap creates a map of stores with one replaced by an overlay.
//
// This is useful for dry-run reconciliation where all stores are needed but
// one should simulate a resource change.
//
// Parameters:
//   - resourceType: The resource type to overlay
//   - namespace: Resource namespace
//   - name: Resource name
//   - obj: The resource object
//   - op: The operation type
//
// Returns:
//   - A map of all stores with the specified one replaced by an overlay
//   - An error if the overlay creation fails
//
// Example:
//
//	// Get all stores with ingress store overlaid
//	stores, err := manager.CreateOverlayMap("ingresses", "default", "my-ing", updatedIngress, OperationUpdate)
//	if err != nil {
//	    return err
//	}
//
//	// Use for dry-run reconciliation
//	err = executor.DryRunReconcile(ctx, stores)
func (m *Manager) CreateOverlayMap(resourceType, namespace, name string, obj interface{}, op Operation) (map[string]Store, error) {
	// Get all stores
	stores := m.GetAllStores()

	// Create overlay for the specified resource type
	overlay, err := m.CreateOverlay(resourceType, namespace, name, obj, op)
	if err != nil {
		return nil, err
	}

	// Replace the store with the overlay
	stores[resourceType] = overlay

	return stores, nil
}

// ResourceCount returns the number of registered stores.
func (m *Manager) ResourceCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.stores)
}

// StoreNames returns the names of all registered stores.
func (m *Manager) StoreNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.stores))
	for name := range m.stores {
		names = append(names, name)
	}
	return names
}
