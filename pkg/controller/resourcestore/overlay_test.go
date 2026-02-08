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

func TestNewOverlayStore(t *testing.T) {
	baseStore := &mockStore{}
	obj := newMockResource("default", "test")

	overlay := NewOverlayStore(baseStore, "default", "test", obj, OperationCreate)

	assert.NotNil(t, overlay)
	assert.Equal(t, baseStore, overlay.baseStore)
	assert.Equal(t, "default", overlay.namespace)
	assert.Equal(t, "test", overlay.name)
	assert.Equal(t, obj, overlay.object)
	assert.Equal(t, OperationCreate, overlay.operation)
}

func TestOverlayStore_Get_OverlayHit_Create(t *testing.T) {
	baseStore := &mockStore{
		resources: []interface{}{
			newMockResource("default", "existing"),
		},
	}
	newResource := newMockResource("default", "new-resource")

	overlay := NewOverlayStore(baseStore, "default", "new-resource", newResource, OperationCreate)

	// Get the new resource from overlay
	results, err := overlay.Get("default", "new-resource")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, newResource, results[0])
}

func TestOverlayStore_Get_OverlayHit_Update(t *testing.T) {
	originalResource := newMockResource("default", "test-resource")
	baseStore := &mockStore{
		resources: []interface{}{originalResource},
	}
	updatedResource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "test-resource",
		},
		"spec": map[string]interface{}{
			"updated": true,
		},
	}

	overlay := NewOverlayStore(baseStore, "default", "test-resource", updatedResource, OperationUpdate)

	// Get should return the updated version from overlay
	results, err := overlay.Get("default", "test-resource")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, updatedResource, results[0])
}

func TestOverlayStore_Get_OverlayHit_Delete(t *testing.T) {
	existingResource := newMockResource("default", "to-delete")
	baseStore := &mockStore{
		resources: []interface{}{existingResource},
	}

	overlay := NewOverlayStore(baseStore, "default", "to-delete", nil, OperationDelete)

	// Get should return empty for deleted resource
	results, err := overlay.Get("default", "to-delete")

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestOverlayStore_Get_Fallback(t *testing.T) {
	existingResource := newMockResource("default", "existing")
	baseStore := &mockStore{
		resources: []interface{}{existingResource},
	}
	newResource := newMockResource("default", "new-resource")

	overlay := NewOverlayStore(baseStore, "default", "new-resource", newResource, OperationCreate)

	// Get existing resource should fall back to base store
	results, err := overlay.Get("default", "existing")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, existingResource, results[0])
}

func TestOverlayStore_List_Delete(t *testing.T) {
	resource1 := newMockResource("default", "keep-me")
	resource2 := newMockResource("default", "delete-me")
	baseStore := &mockStore{
		resources: []interface{}{resource1, resource2},
	}

	overlay := NewOverlayStore(baseStore, "default", "delete-me", nil, OperationDelete)

	results, err := overlay.List()

	require.NoError(t, err)
	require.Len(t, results, 1)

	ns, name := extractMetadata(results[0])
	assert.Equal(t, "default", ns)
	assert.Equal(t, "keep-me", name)
}

func TestOverlayStore_List_Update(t *testing.T) {
	resource1 := newMockResource("default", "unchanged")
	resource2 := newMockResource("default", "to-update")
	baseStore := &mockStore{
		resources: []interface{}{resource1, resource2},
	}
	updatedResource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "to-update",
		},
		"spec": map[string]interface{}{
			"updated": true,
		},
	}

	overlay := NewOverlayStore(baseStore, "default", "to-update", updatedResource, OperationUpdate)

	results, err := overlay.List()

	require.NoError(t, err)
	require.Len(t, results, 2)

	// Find the updated resource
	var foundUpdated bool
	for _, r := range results {
		ns, name := extractMetadata(r)
		if ns == "default" && name == "to-update" {
			// Verify it's the updated version
			resMap := r.(map[string]interface{})
			spec, ok := resMap["spec"].(map[string]interface{})
			require.True(t, ok)
			assert.True(t, spec["updated"].(bool))
			foundUpdated = true
		}
	}
	assert.True(t, foundUpdated, "updated resource should be in list")
}

func TestOverlayStore_List_Create(t *testing.T) {
	existingResource := newMockResource("default", "existing")
	baseStore := &mockStore{
		resources: []interface{}{existingResource},
	}
	newResource := newMockResource("default", "new-resource")

	overlay := NewOverlayStore(baseStore, "default", "new-resource", newResource, OperationCreate)

	results, err := overlay.List()

	require.NoError(t, err)
	require.Len(t, results, 2)

	// Verify both resources are present
	names := make([]string, 0, 2)
	for _, r := range results {
		_, name := extractMetadata(r)
		names = append(names, name)
	}
	assert.Contains(t, names, "existing")
	assert.Contains(t, names, "new-resource")
}

func TestOverlayStore_ReadOnlyOperations(t *testing.T) {
	overlay := NewOverlayStore(&mockStore{}, "default", "test", nil, OperationDelete)

	t.Run("Add returns error", func(t *testing.T) {
		err := overlay.Add(nil, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})

	t.Run("Update returns error", func(t *testing.T) {
		err := overlay.Update(nil, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})

	t.Run("Delete returns error", func(t *testing.T) {
		err := overlay.Delete("default", "test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})

	t.Run("Clear returns error", func(t *testing.T) {
		err := overlay.Clear()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})
}

func TestExtractMetadata(t *testing.T) {
	t.Run("map[string]interface{}", func(t *testing.T) {
		resource := newMockResource("test-ns", "test-name")

		ns, name := extractMetadata(resource)

		assert.Equal(t, "test-ns", ns)
		assert.Equal(t, "test-name", name)
	})

	t.Run("missing metadata", func(t *testing.T) {
		resource := map[string]interface{}{
			"spec": map[string]interface{}{},
		}

		ns, name := extractMetadata(resource)

		assert.Empty(t, ns)
		assert.Empty(t, name)
	})

	t.Run("unknown type", func(t *testing.T) {
		resource := "not a resource"

		ns, name := extractMetadata(resource)

		assert.Empty(t, ns)
		assert.Empty(t, name)
	})

	t.Run("cluster-scoped resource (no namespace)", func(t *testing.T) {
		resource := map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "cluster-resource",
			},
		}

		ns, name := extractMetadata(resource)

		assert.Empty(t, ns)
		assert.Equal(t, "cluster-resource", name)
	})
}

func TestOverlayStore_MultipleNamespaces(t *testing.T) {
	resource1 := newMockResource("ns1", "resource")
	resource2 := newMockResource("ns2", "resource")
	resource3 := newMockResource("ns3", "resource")
	baseStore := &mockStore{
		resources: []interface{}{resource1, resource2, resource3},
	}

	// Delete resource in ns2 only
	overlay := NewOverlayStore(baseStore, "ns2", "resource", nil, OperationDelete)

	results, err := overlay.List()

	require.NoError(t, err)
	require.Len(t, results, 2)

	// Verify ns1 and ns3 resources remain
	namespaces := make([]string, 0, 2)
	for _, r := range results {
		ns, _ := extractMetadata(r)
		namespaces = append(namespaces, ns)
	}
	assert.Contains(t, namespaces, "ns1")
	assert.Contains(t, namespaces, "ns3")
	assert.NotContains(t, namespaces, "ns2")
}
