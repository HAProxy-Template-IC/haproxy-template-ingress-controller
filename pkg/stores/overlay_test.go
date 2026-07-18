package stores

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ktypes "k8s.io/apimachinery/pkg/types"
)

func TestStoreOverlay_NewStoreOverlay(t *testing.T) {
	overlay := NewStoreOverlay()

	assert.NotNil(t, overlay)
	assert.Empty(t, overlay.Additions)
	assert.Empty(t, overlay.Modifications)
	assert.Empty(t, overlay.Deletions)
}

func TestStoreOverlay_IsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*StoreOverlay)
		isEmpty bool
	}{
		{
			name:    "empty overlay",
			setup:   func(o *StoreOverlay) {},
			isEmpty: true,
		},
		{
			name: "has addition",
			setup: func(o *StoreOverlay) {
				o.AddAddition(&corev1.ConfigMap{})
			},
			isEmpty: false,
		},
		{
			name: "has modification",
			setup: func(o *StoreOverlay) {
				o.AddModification(&corev1.ConfigMap{})
			},
			isEmpty: false,
		},
		{
			name: "has deletion",
			setup: func(o *StoreOverlay) {
				o.Deletions = append(o.Deletions, ktypes.NamespacedName{
					Namespace: "default",
					Name:      "test",
				})
			},
			isEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overlay := NewStoreOverlay()
			tt.setup(overlay)
			assert.Equal(t, tt.isEmpty, overlay.IsEmpty())
		})
	}
}

func TestCompositeStore_List_NoChanges(t *testing.T) {
	base := newMockStore()
	_ = base.Add("resource1", []string{"default", "res1"})
	_ = base.Add("resource2", []string{"default", "res2"})

	overlay := NewStoreOverlay()
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	assert.Len(t, resources, 2)
}

func TestCompositeStore_List_WithAdditions(t *testing.T) {
	base := newMockStore()
	_ = base.Add("resource1", []string{"default", "res1"})

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "new-configmap",
		},
	}

	overlay := NewStoreOverlayForCreate(cm)
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	assert.Len(t, resources, 2)
	assert.Contains(t, resources, "resource1")

	// The ConfigMap is pre-converted, but since it's not *unstructured.Unstructured,
	// it remains as *corev1.ConfigMap (convertOverlayResource only converts unstructured)
	found := false
	for _, r := range resources {
		// Check for typed ConfigMap (test uses typed objects, not unstructured)
		if typedCM, ok := r.(*corev1.ConfigMap); ok {
			if typedCM.Name == "new-configmap" && typedCM.Namespace == "default" {
				found = true
				break
			}
		}
		// Also check for map in case test changes to use unstructured
		if m, ok := r.(map[string]any); ok {
			if metadata, ok := m["metadata"].(map[string]any); ok {
				if metadata["name"] == "new-configmap" && metadata["namespace"] == "default" {
					found = true
					break
				}
			}
		}
	}
	assert.True(t, found, "should find the added ConfigMap")
}

func TestCompositeStore_List_WithDeletions(t *testing.T) {
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
	}
	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm2",
		},
	}

	base := newMockStore()
	_ = base.Add(cm1, []string{"default", "cm1"})
	_ = base.Add(cm2, []string{"default", "cm2"})

	overlay := NewStoreOverlay()
	overlay.Deletions = []ktypes.NamespacedName{
		{Namespace: "default", Name: "cm1"},
	}

	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Contains(t, resources, cm2)
}

func TestCompositeStore_List_WithModifications(t *testing.T) {
	original := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
		Data: map[string]string{"key": "original"},
	}

	modified := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
		Data: map[string]string{"key": "modified"},
	}

	base := newMockStore()
	_ = base.Add(original, []string{"default", "cm1"})

	overlay := NewStoreOverlayForUpdate(modified)
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	assert.Len(t, resources, 1)

	// Since tests use typed objects (not unstructured), the result is *corev1.ConfigMap
	resultCM, ok := resources[0].(*corev1.ConfigMap)
	require.True(t, ok, "resource should be *corev1.ConfigMap (tests use typed objects)")
	assert.Equal(t, "modified", resultCM.Data["key"])
}

// TestCompositeStore_List_MapBackedBaseModification pins the dry-run overlay
// against map[string]any base items — the shape the watcher actually stores
// (pkg/k8s/indexer converts to map before Add). If getResourceKey can't read
// namespace/name from a map, isModified() never matches and List() returns
// BOTH the base copy and the overlaid modification for the same object, which
// under admission dry-run surfaced as a duplicate-resource render failure.
func TestCompositeStore_List_MapBackedBaseModification(t *testing.T) {
	original := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"namespace": "default", "name": "ing1"},
		"spec":       map[string]any{"rules": "original"},
	}
	modified := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"namespace": "default", "name": "ing1"},
		"spec":       map[string]any{"rules": "modified"},
	}}

	base := newMockStore()
	_ = base.Add(original, []string{"default", "ing1"})

	overlay := NewStoreOverlayForUpdate(modified)
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	require.Len(t, resources, 1, "map-backed base copy must be replaced by the overlay, not returned alongside it")
}

// TestCompositeStore_List_MapBackedBaseDeletion pins the same getResourceKey
// fallback for the deletion path: a deleted map-backed base item must be
// filtered from List(), not leak through as an un-deleted copy.
func TestCompositeStore_List_MapBackedBaseDeletion(t *testing.T) {
	original := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"namespace": "default", "name": "ing1"},
	}

	base := newMockStore()
	_ = base.Add(original, []string{"default", "ing1"})

	overlay := NewStoreOverlayForDelete("default", "ing1")
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	require.Empty(t, resources, "map-backed base copy marked deleted must be filtered from List()")
}

func TestCompositeStore_Get_NoChanges(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
	}

	base := newMockStore()
	_ = base.Add(cm, []string{"default", "cm1"})

	overlay := NewStoreOverlay()
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.Get("default", "cm1")
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, cm, resources[0])
}

func TestCompositeStore_Get_WithAddition(t *testing.T) {
	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "new-cm",
		},
	}

	base := newMockStore()
	overlay := NewStoreOverlayForCreate(newCM)
	composite := NewCompositeStore(base, overlay)

	// Query for the added resource
	resources, err := composite.Get("default", "new-cm")
	require.NoError(t, err)
	assert.Len(t, resources, 1)

	// Since tests use typed objects (not unstructured), the result is *corev1.ConfigMap
	resultCM, ok := resources[0].(*corev1.ConfigMap)
	require.True(t, ok, "resource should be *corev1.ConfigMap (tests use typed objects)")
	assert.Equal(t, "new-cm", resultCM.Name)
	assert.Equal(t, "default", resultCM.Namespace)
}

func TestCompositeStore_Get_WithDeletion(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
	}

	base := newMockStore()
	_ = base.Add(cm, []string{"default", "cm1"})

	overlay := NewStoreOverlay()
	overlay.Deletions = []ktypes.NamespacedName{
		{Namespace: "default", Name: "cm1"},
	}

	composite := NewCompositeStore(base, overlay)

	resources, err := composite.Get("default", "cm1")
	require.NoError(t, err)
	assert.Empty(t, resources)
}

func TestCompositeStore_Get_WithModification(t *testing.T) {
	original := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
		Data: map[string]string{"key": "original"},
	}

	modified := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
		},
		Data: map[string]string{"key": "modified"},
	}

	base := newMockStore()
	_ = base.Add(original, []string{"default", "cm1"})

	overlay := NewStoreOverlayForUpdate(modified)
	composite := NewCompositeStore(base, overlay)

	resources, err := composite.Get("default", "cm1")
	require.NoError(t, err)
	assert.Len(t, resources, 1)

	// Since tests use typed objects (not unstructured), the result is *corev1.ConfigMap
	resultCM, ok := resources[0].(*corev1.ConfigMap)
	require.True(t, ok, "resource should be *corev1.ConfigMap (tests use typed objects)")
	assert.Equal(t, "modified", resultCM.Data["key"])
}

func TestCompositeStore_ReadOnlyOperations(t *testing.T) {
	base := newMockStore()
	overlay := NewStoreOverlay()
	composite := NewCompositeStore(base, overlay)

	tests := []struct {
		name      string
		operation func() error
	}{
		{
			name: "Add",
			operation: func() error {
				return composite.Add("resource", []string{"key"})
			},
		},
		{
			name: "Update",
			operation: func() error {
				return composite.Update("resource", []string{"key"})
			},
		},
		{
			name: "Delete",
			operation: func() error {
				return composite.Delete("key")
			},
		},
		{
			name: "Clear",
			operation: func() error {
				return composite.Clear()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.operation()
			assert.Error(t, err)

			var readOnlyErr *ReadOnlyStoreError
			assert.ErrorAs(t, err, &readOnlyErr)
			assert.Equal(t, tt.name, readOnlyErr.Operation)
		})
	}
}

func TestReadOnlyStoreError_Error(t *testing.T) {
	err := &ReadOnlyStoreError{Operation: "Add"}
	assert.Equal(t, "composite store is read-only: Add not supported", err.Error())
}
