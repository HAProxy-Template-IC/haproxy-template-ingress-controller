package stores

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockStore is a simple in-memory store for testing.
type mockStore struct {
	resources map[string]any
}

func newMockStore() *mockStore {
	return &mockStore{
		resources: make(map[string]any),
	}
}

func (s *mockStore) Get(keys ...string) ([]any, error) {
	key := keyString(keys)
	if res, ok := s.resources[key]; ok {
		return []any{res}, nil
	}
	return nil, nil
}

func (s *mockStore) List() ([]any, error) {
	result := make([]any, 0, len(s.resources))
	for _, res := range s.resources {
		result = append(result, res)
	}
	return result, nil
}

func (s *mockStore) Add(resource any, keys []string) error {
	s.resources[keyString(keys)] = resource
	return nil
}

func (s *mockStore) Update(resource any, keys []string) error {
	s.resources[keyString(keys)] = resource
	return nil
}

func (s *mockStore) Delete(keys ...string) error {
	delete(s.resources, keyString(keys))
	return nil
}

func (s *mockStore) Clear() error {
	s.resources = make(map[string]any)
	return nil
}

func keyString(keys []string) string {
	var result strings.Builder
	for i, k := range keys {
		if i > 0 {
			result.WriteString("/")
		}
		result.WriteString(k)
	}
	return result.String()
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
