package store

import (
	"fmt"
	"sort"
	"sync"

	"haproxy-template-ic/pkg/k8s/types"
)

// MemoryStore stores complete Kubernetes resources in memory using nested maps.
//
// This provides O(1) lookup performance at the cost of higher memory usage.
// Resources are stored with their full specification after field filtering.
//
// Supports non-unique index keys by storing multiple resources per composite key.
//
// Thread-safe for concurrent access.
type MemoryStore struct {
	mu       sync.RWMutex
	data     map[string][]interface{} // Flat map: composite key -> slice of resources
	numKeys  int                      // Number of index keys
	allItems []interface{}            // Cache of all items for List()
	dirty    bool                     // True if allItems needs rebuilding
}

// NewMemoryStore creates a new memory-backed store.
//
// Parameters:
//   - numKeys: Number of index keys (must match indexer configuration)
func NewMemoryStore(numKeys int) *MemoryStore {
	if numKeys < 1 {
		numKeys = 1
	}

	return &MemoryStore{
		data:     make(map[string][]interface{}),
		numKeys:  numKeys,
		allItems: make([]interface{}, 0),
		dirty:    false,
	}
}

// Get retrieves all resources matching the provided index keys.
func (s *MemoryStore) Get(keys ...string) ([]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(keys) == 0 {
		return nil, &StoreError{
			Operation: "get",
			Keys:      keys,
			Cause:     fmt.Errorf("at least one key required"),
		}
	}

	if len(keys) > s.numKeys {
		return nil, &StoreError{
			Operation: "get",
			Keys:      keys,
			Cause:     fmt.Errorf("too many keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	// Exact match: return all resources with this exact key
	if len(keys) == s.numKeys {
		keyStr := makeKeyString(keys)
		if items, ok := s.data[keyStr]; ok {
			// Return copy to prevent external modification
			result := make([]interface{}, len(items))
			copy(result, items)
			return result, nil
		}
		return []interface{}{}, nil
	}

	// Partial match: return all resources matching prefix
	prefix := makeKeyString(keys) + "/"
	var results []interface{}

	for key, items := range s.data {
		// Check if key starts with prefix
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, items...)
		}
	}

	return results, nil
}

// List returns all resources in the store.
func (s *MemoryStore) List() ([]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return cached list if not dirty
	if !s.dirty && s.allItems != nil {
		return s.allItems, nil
	}

	// Rebuild cache
	s.mu.RUnlock()
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		s.mu.RLock()
	}()

	// Double-check after acquiring write lock
	if !s.dirty && s.allItems != nil {
		return s.allItems, nil
	}

	// Flatten all resource slices into single list
	items := make([]interface{}, 0)
	for _, resourceSlice := range s.data {
		items = append(items, resourceSlice...)
	}

	// Sort items by namespace and name for deterministic order
	sort.Slice(items, func(i, j int) bool {
		nsI, nameI := extractNamespaceName(items[i])
		nsJ, nameJ := extractNamespaceName(items[j])

		// Sort by namespace first, then by name
		if nsI != nsJ {
			return nsI < nsJ
		}
		return nameI < nameJ
	})

	s.allItems = items
	s.dirty = false

	return items, nil
}

// Add inserts a new resource into the store.
// If resources with the same index keys already exist, the new resource is appended.
func (s *MemoryStore) Add(resource interface{}, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(keys) != s.numKeys {
		return &StoreError{
			Operation: "add",
			Keys:      keys,
			Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	keyStr := makeKeyString(keys)
	s.data[keyStr] = append(s.data[keyStr], resource)
	s.dirty = true

	return nil
}

// Update modifies an existing resource or adds it if it doesn't exist.
// For non-unique index keys, it finds the resource by namespace+name and replaces it.
func (s *MemoryStore) Update(resource interface{}, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(keys) != s.numKeys {
		return &StoreError{
			Operation: "update",
			Keys:      keys,
			Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	keyStr := makeKeyString(keys)
	resources, ok := s.data[keyStr]
	if !ok {
		// No resources with these keys - add new
		s.data[keyStr] = []interface{}{resource}
		s.dirty = true
		return nil
	}

	// Try to find existing resource by namespace+name
	ns, name := extractNamespaceName(resource)
	for i, existing := range resources {
		existingNs, existingName := extractNamespaceName(existing)
		if existingNs == ns && existingName == name {
			// Replace existing resource
			resources[i] = resource
			s.data[keyStr] = resources
			s.dirty = true
			return nil
		}
	}

	// Resource not found - append
	s.data[keyStr] = append(resources, resource)
	s.dirty = true
	return nil
}

// Delete removes a resource from the store.
// NOTE: With non-unique index keys, this method cannot identify which specific resource
// to delete when multiple resources have the same index keys. It removes ALL resources
// matching the provided keys. The watcher should call this with the resource's actual
// namespace+name as the index keys to delete a specific resource.
func (s *MemoryStore) Delete(keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(keys) != s.numKeys {
		return &StoreError{
			Operation: "delete",
			Keys:      keys,
			Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	keyStr := makeKeyString(keys)
	if _, ok := s.data[keyStr]; ok {
		delete(s.data, keyStr)
		s.dirty = true
	}

	return nil
}

// Clear removes all resources from the store.
func (s *MemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string][]interface{})
	s.allItems = make([]interface{}, 0)
	s.dirty = false

	return nil
}

// Size returns the number of resources in the store.
func (s *MemoryStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, resources := range s.data {
		count += len(resources)
	}
	return count
}

// Ensure MemoryStore implements types.Store interface.
var _ types.Store = (*MemoryStore)(nil)
