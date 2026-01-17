package stores

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
				o.Additions = append(o.Additions, &corev1.ConfigMap{})
			},
			isEmpty: false,
		},
		{
			name: "has modification",
			setup: func(o *StoreOverlay) {
				o.Modifications = append(o.Modifications, &corev1.ConfigMap{})
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

	overlay := NewStoreOverlay()
	overlay.Additions = []runtime.Object{cm}

	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	assert.Len(t, resources, 2)
	assert.Contains(t, resources, "resource1")
	assert.Contains(t, resources, cm)
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

	overlay := NewStoreOverlay()
	overlay.Modifications = []runtime.Object{modified}

	composite := NewCompositeStore(base, overlay)

	resources, err := composite.List()
	require.NoError(t, err)
	assert.Len(t, resources, 1)

	// Should contain modified version, not original
	resultCM, ok := resources[0].(*corev1.ConfigMap)
	require.True(t, ok)
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
	overlay := NewStoreOverlay()
	overlay.Additions = []runtime.Object{newCM}

	composite := NewCompositeStore(base, overlay)

	// Query for the added resource
	resources, err := composite.Get("default", "new-cm")
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, newCM, resources[0])
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

	overlay := NewStoreOverlay()
	overlay.Modifications = []runtime.Object{modified}

	composite := NewCompositeStore(base, overlay)

	resources, err := composite.Get("default", "cm1")
	require.NoError(t, err)
	assert.Len(t, resources, 1)

	resultCM, ok := resources[0].(*corev1.ConfigMap)
	require.True(t, ok)
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

	overlay := NewStoreOverlay()
	overlay.Additions = []runtime.Object{newCM}

	// Key extractor for namespace/name indexing
	keyExtractor := func(resource interface{}) ([]string, error) {
		if accessor, ok := resource.(interface {
			GetNamespace() string
			GetName() string
		}); ok {
			return []string{accessor.GetNamespace(), accessor.GetName()}, nil
		}
		return nil, nil
	}

	composite := NewCompositeStoreWithKeyExtractor(base, overlay, keyExtractor)

	// Query for staging/cm3 should find the addition
	resources, err := composite.Get("staging", "cm3")
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, newCM, resources[0])

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
