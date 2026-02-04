package stores

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		if m, ok := r.(map[string]interface{}); ok {
			if metadata, ok := m["metadata"].(map[string]interface{}); ok {
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

func TestCompositeStore_WithKeyExtractor(t *testing.T) {
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cm1",
			Labels:    map[string]string{"app": "frontend"},
		},
	}

	base := newMockStore()
	_ = base.Add(cm1, []string{"default", "cm1"})

	// Add a new ConfigMap via overlay
	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "staging",
			Name:      "cm3",
			Labels:    map[string]string{"app": "api"},
		},
	}

	overlay := NewStoreOverlayForCreate(newCM)

	// Key extractor for namespace/name indexing (handles both runtime.Object and map)
	keyExtractor := func(resource interface{}) ([]string, error) {
		// Try runtime.Object first (handles typed objects from tests and base store)
		if accessor, ok := resource.(interface {
			GetNamespace() string
			GetName() string
		}); ok {
			return []string{accessor.GetNamespace(), accessor.GetName()}, nil
		}
		// Handle pre-converted map[string]interface{} (for unstructured resources)
		if m, ok := resource.(map[string]interface{}); ok {
			if metadata, ok := m["metadata"].(map[string]interface{}); ok {
				ns, _ := metadata["namespace"].(string)
				name, _ := metadata["name"].(string)
				return []string{ns, name}, nil
			}
		}
		return nil, nil
	}

	composite := NewCompositeStoreWithKeyExtractor(base, overlay, keyExtractor)

	// Query for staging/cm3 should find the addition
	resources, err := composite.Get("staging", "cm3")
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	// Since tests use typed objects (not unstructured), the result is *corev1.ConfigMap
	resultCM, ok := resources[0].(*corev1.ConfigMap)
	require.True(t, ok, "resource should be *corev1.ConfigMap (tests use typed objects)")
	assert.Equal(t, "cm3", resultCM.Name)
	assert.Equal(t, "staging", resultCM.Namespace)

	// Query for default/cm1 should find the base resource
	resources, err = composite.Get("default", "cm1")
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, cm1, resources[0])

	// Query for wrong namespace/name should return empty
	resources, err = composite.Get("default", "cm3")
	require.NoError(t, err)
	assert.Empty(t, resources)
}

func TestReadOnlyStoreError_Error(t *testing.T) {
	err := &ReadOnlyStoreError{Operation: "Add"}
	assert.Equal(t, "composite store is read-only: Add not supported", err.Error())
}
