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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore implements Store for testing.
type mockStore struct {
	resources []interface{}
}

func (m *mockStore) Get(keys ...string) ([]interface{}, error) {
	// Simple matching by namespace/name
	if len(keys) >= 2 {
		for _, r := range m.resources {
			ns, name := extractMetadata(r)
			if ns == keys[0] && name == keys[1] {
				return []interface{}{r}, nil
			}
		}
	}
	return []interface{}{}, nil
}

func (m *mockStore) List() ([]interface{}, error) {
	return m.resources, nil
}

func (m *mockStore) Add(resource interface{}, keys []string) error {
	m.resources = append(m.resources, resource)
	return nil
}

func (m *mockStore) Update(resource interface{}, keys []string) error {
	// For testing, just append
	return m.Add(resource, keys)
}

func (m *mockStore) Delete(keys ...string) error {
	return nil
}

func (m *mockStore) Clear() error {
	m.resources = nil
	return nil
}

// newMockResource creates a mock Kubernetes resource as map[string]interface{}.
func newMockResource(namespace, name string) map[string]interface{} {
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": namespace,
			"name":      name,
		},
	}
}

// TestNewManager tests the Manager constructor.
func TestNewManager(t *testing.T) {
	manager := NewManager()

	assert.NotNil(t, manager)
	assert.NotNil(t, manager.stores)
	assert.Equal(t, 0, manager.ResourceCount())
}

// TestManager_RegisterAndGetStore tests store registration and retrieval.
func TestManager_RegisterAndGetStore(t *testing.T) {
	manager := NewManager()
	store := &mockStore{}

	// Register store
	manager.RegisterStore("ingresses", store)

	// Verify retrieval
	retrieved, exists := manager.GetStore("ingresses")
	assert.True(t, exists)
	assert.Equal(t, store, retrieved)

	// Verify non-existent store
	_, exists = manager.GetStore("nonexistent")
	assert.False(t, exists)
}

// TestManager_RegisterStore_Replace tests that registering replaces existing store.
func TestManager_RegisterStore_Replace(t *testing.T) {
	manager := NewManager()
	store1 := &mockStore{resources: []interface{}{"first"}}
	store2 := &mockStore{resources: []interface{}{"second"}}

	// Register first store
	manager.RegisterStore("ingresses", store1)
	retrieved, _ := manager.GetStore("ingresses")
	assert.Equal(t, store1, retrieved)

	// Replace with second store
	manager.RegisterStore("ingresses", store2)
	retrieved, _ = manager.GetStore("ingresses")
	assert.Equal(t, store2, retrieved)

	// Count should still be 1
	assert.Equal(t, 1, manager.ResourceCount())
}

// TestManager_GetAllStores tests retrieving all registered stores.
func TestManager_GetAllStores(t *testing.T) {
	manager := NewManager()
	ingressStore := &mockStore{}
	serviceStore := &mockStore{}
	endpointStore := &mockStore{}

	manager.RegisterStore("ingresses", ingressStore)
	manager.RegisterStore("services", serviceStore)
	manager.RegisterStore("endpointslices", endpointStore)

	stores := manager.GetAllStores()

	assert.Len(t, stores, 3)
	assert.Equal(t, ingressStore, stores["ingresses"])
	assert.Equal(t, serviceStore, stores["services"])
	assert.Equal(t, endpointStore, stores["endpointslices"])
}

// TestManager_GetAllStores_ReturnsShallowCopy tests that GetAllStores returns a copy.
func TestManager_GetAllStores_ReturnsShallowCopy(t *testing.T) {
	manager := NewManager()
	manager.RegisterStore("ingresses", &mockStore{})

	stores := manager.GetAllStores()

	// Modify returned map
	stores["modified"] = &mockStore{}

	// Original should be unchanged
	_, exists := manager.GetStore("modified")
	assert.False(t, exists)
	assert.Equal(t, 1, manager.ResourceCount())
}

// TestManager_ResourceCount tests counting registered stores.
func TestManager_ResourceCount(t *testing.T) {
	manager := NewManager()

	assert.Equal(t, 0, manager.ResourceCount())

	manager.RegisterStore("ingresses", &mockStore{})
	assert.Equal(t, 1, manager.ResourceCount())

	manager.RegisterStore("services", &mockStore{})
	assert.Equal(t, 2, manager.ResourceCount())
}

// TestManager_CreateOverlay tests creating an overlay store.
func TestManager_CreateOverlay(t *testing.T) {
	manager := NewManager()
	baseStore := &mockStore{
		resources: []interface{}{
			newMockResource("default", "existing"),
		},
	}
	manager.RegisterStore("ingresses", baseStore)

	// Create overlay for a new resource
	newResource := newMockResource("default", "new-ingress")
	overlay, err := manager.CreateOverlay("ingresses", "default", "new-ingress", newResource, OperationCreate)

	require.NoError(t, err)
	assert.NotNil(t, overlay)

	// Verify overlay is an OverlayStore
	_, ok := overlay.(*OverlayStore)
	assert.True(t, ok)
}

// TestManager_CreateOverlay_NonexistentStore tests error for non-existent store.
func TestManager_CreateOverlay_NonexistentStore(t *testing.T) {
	manager := NewManager()

	_, err := manager.CreateOverlay("nonexistent", "default", "test", nil, OperationDelete)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no store registered for resource type")
}

// TestManager_CreateOverlayMap tests creating an overlay map.
func TestManager_CreateOverlayMap(t *testing.T) {
	manager := NewManager()
	ingressStore := &mockStore{resources: []interface{}{newMockResource("default", "ing1")}}
	serviceStore := &mockStore{resources: []interface{}{newMockResource("default", "svc1")}}

	manager.RegisterStore("ingresses", ingressStore)
	manager.RegisterStore("services", serviceStore)

	// Create overlay map with ingress updated
	updatedIngress := newMockResource("default", "ing1")
	stores, err := manager.CreateOverlayMap("ingresses", "default", "ing1", updatedIngress, OperationUpdate)

	require.NoError(t, err)
	require.Len(t, stores, 2)

	// Verify ingresses store is overlaid
	_, ok := stores["ingresses"].(*OverlayStore)
	assert.True(t, ok, "ingresses store should be overlaid")

	// Verify services store is unchanged
	assert.Equal(t, serviceStore, stores["services"])
}

// TestManager_CreateOverlayMap_NonexistentStore tests error for non-existent store in overlay map.
func TestManager_CreateOverlayMap_NonexistentStore(t *testing.T) {
	manager := NewManager()
	manager.RegisterStore("services", &mockStore{})

	_, err := manager.CreateOverlayMap("ingresses", "default", "test", nil, OperationDelete)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no store registered for resource type")
}
