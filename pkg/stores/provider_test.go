package stores

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore is a simple in-memory store for testing.
type mockStore struct {
	resources map[string]interface{}
}

func newMockStore() *mockStore {
	return &mockStore{
		resources: make(map[string]interface{}),
	}
}

func (s *mockStore) Get(keys ...string) ([]interface{}, error) {
	key := keyString(keys)
	if res, ok := s.resources[key]; ok {
		return []interface{}{res}, nil
	}
	return nil, nil
}

func (s *mockStore) List() ([]interface{}, error) {
	result := make([]interface{}, 0, len(s.resources))
	for _, res := range s.resources {
		result = append(result, res)
	}
	return result, nil
}

func (s *mockStore) Add(resource interface{}, keys []string) error {
	s.resources[keyString(keys)] = resource
	return nil
}

func (s *mockStore) Update(resource interface{}, keys []string) error {
	s.resources[keyString(keys)] = resource
	return nil
}

func (s *mockStore) Delete(keys ...string) error {
	delete(s.resources, keyString(keys))
	return nil
}

func (s *mockStore) Clear() error {
	s.resources = make(map[string]interface{})
	return nil
}

func keyString(keys []string) string {
	result := ""
	for i, k := range keys {
		if i > 0 {
			result += "/"
		}
		result += k
	}
	return result
}

func TestRealStoreProvider_GetStore(t *testing.T) {
	store1 := newMockStore()
	store2 := newMockStore()

	stores := map[string]Store{
		"ingresses":      store1,
		"endpointslices": store2,
	}

	provider := NewRealStoreProvider(stores)

	tests := []struct {
		name      string
		storeName string
		want      Store
	}{
		{
			name:      "existing store",
			storeName: "ingresses",
			want:      store1,
		},
		{
			name:      "another existing store",
			storeName: "endpointslices",
			want:      store2,
		},
		{
			name:      "non-existent store",
			storeName: "services",
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.GetStore(tt.storeName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRealStoreProvider_StoreNames(t *testing.T) {
	stores := map[string]Store{
		"ingresses":      newMockStore(),
		"endpointslices": newMockStore(),
		"services":       newMockStore(),
	}

	provider := NewRealStoreProvider(stores)
	names := provider.StoreNames()

	assert.Len(t, names, 3)
	assert.Contains(t, names, "ingresses")
	assert.Contains(t, names, "endpointslices")
	assert.Contains(t, names, "services")
}

func TestRealStoreProvider_NilStores(t *testing.T) {
	provider := NewRealStoreProvider(nil)

	assert.NotNil(t, provider)
	assert.Nil(t, provider.GetStore("anything"))
	assert.Empty(t, provider.StoreNames())
}

func TestCompositeStoreProvider_GetStore_NoOverlay(t *testing.T) {
	baseStore := newMockStore()
	_ = baseStore.Add("resource1", []string{"default", "res1"})

	base := NewRealStoreProvider(map[string]Store{
		"ingresses": baseStore,
	})

	provider := NewCompositeStoreProvider(base, nil)

	// Without overlay, should return base store directly
	store := provider.GetStore("ingresses")
	require.NotNil(t, store)

	resources, err := store.List()
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, "resource1", resources[0])
}

func TestCompositeStoreProvider_GetStore_WithOverlay(t *testing.T) {
	baseStore := newMockStore()
	_ = baseStore.Add("resource1", []string{"default", "res1"})

	base := NewRealStoreProvider(map[string]Store{
		"ingresses": baseStore,
	})

	overlay := NewStoreOverlay()

	provider := NewCompositeStoreProvider(base, map[string]*StoreOverlay{
		"ingresses": overlay,
	})

	// With overlay, should return CompositeStore
	store := provider.GetStore("ingresses")
	require.NotNil(t, store)

	// CompositeStore is read-only
	err := store.Add("new", []string{"default", "new"})
	assert.Error(t, err)
	assert.IsType(t, &ReadOnlyStoreError{}, err)
}

func TestCompositeStoreProvider_GetStore_NonExistent(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{
		"ingresses": newMockStore(),
	})

	provider := NewCompositeStoreProvider(base, nil)

	store := provider.GetStore("nonexistent")
	assert.Nil(t, store)
}

func TestCompositeStoreProvider_StoreNames(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{
		"ingresses": newMockStore(),
		"services":  newMockStore(),
	})

	// Overlays can reference existing stores or add new ones
	overlays := map[string]*StoreOverlay{
		"ingresses": NewStoreOverlay(), // existing
		"secrets":   NewStoreOverlay(), // new (overlay-only)
	}

	provider := NewCompositeStoreProvider(base, overlays)
	names := provider.StoreNames()

	assert.Len(t, names, 3)
	assert.Contains(t, names, "ingresses")
	assert.Contains(t, names, "services")
	assert.Contains(t, names, "secrets")
}

func TestCompositeStoreProvider_Validate(t *testing.T) {
	base := NewRealStoreProvider(map[string]Store{
		"ingresses": newMockStore(),
	})

	tests := []struct {
		name     string
		overlays map[string]*StoreOverlay
		wantErr  bool
	}{
		{
			name:     "no overlays",
			overlays: nil,
			wantErr:  false,
		},
		{
			name: "valid overlay",
			overlays: map[string]*StoreOverlay{
				"ingresses": NewStoreOverlay(),
			},
			wantErr: false,
		},
		{
			name: "overlay references non-existent store",
			overlays: map[string]*StoreOverlay{
				"nonexistent": NewStoreOverlay(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewCompositeStoreProvider(base, tt.overlays)
			err := provider.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "non-existent store")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
