package store

import (
	"fmt"
	"sort"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// MemoryStore stores complete Kubernetes resources in memory using nested maps.
//
// This provides O(1) lookup performance at the cost of higher memory usage.
// Resources are stored with their full specification after field filtering.
//
// Supports non-unique index keys by storing multiple resources per composite key.
//
// Thread-safe for concurrent access.
//
// # Immutability Contract
//
// Resources stored in MemoryStore are pre-converted (floats to ints) at storage time
// and MUST NOT be mutated by callers. The slices returned by Get() are direct
// references to internal data structures for performance. Callers MUST NOT:
//   - Modify elements of returned slices
//   - Append to or reslice returned slices
//   - Modify fields within returned resources
//
// Note: List() returns a fresh slice copy for thread safety, but the resource
// objects within are still references to internal data and must not be mutated.
type MemoryStore struct {
	mu       sync.RWMutex
	data     map[string][]interface{} // Flat map: composite key -> slice of resources (pre-sorted)
	numKeys  int                      // Number of index keys
	modCount uint64                   // Incremented on every mutation for cache invalidation
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
		data:    make(map[string][]interface{}),
		numKeys: numKeys,
	}
}

// Get retrieves all resources matching the provided index keys.
//
// Returns a direct reference to the internal slice for exact key matches.
// Callers MUST NOT modify the returned slice or its elements (see Immutability Contract).
//
// For partial key matches, a new slice is constructed from matching entries
// and sorted for deterministic order.
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

	// Exact match: return direct reference to pre-sorted internal slice
	if len(keys) == s.numKeys {
		keyStr := makeKeyString(keys)
		if items, ok := s.data[keyStr]; ok {
			// Return direct reference - slice is pre-sorted at insert time
			// Callers must not modify (see Immutability Contract)
			return items, nil
		}
		return []interface{}{}, nil
	}

	// Partial match: return all resources matching prefix
	// Must construct new slice as it aggregates from multiple internal slices
	prefix := makeKeyString(keys) + "/"
	var results []interface{}

	for key, items := range s.data {
		// Check if key starts with prefix
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, items...)
		}
	}

	// Sort for deterministic order (same as List())
	sort.Slice(results, func(i, j int) bool {
		nsI, nameI := extractNamespaceName(results[i])
		nsJ, nameJ := extractNamespaceName(results[j])
		if nsI != nsJ {
			return nsI < nsJ
		}
		return nameI < nameJ
	})

	return results, nil
}

// List returns all resources in the store.
// Returns a fresh copy of all resources to avoid race conditions.
func (s *MemoryStore) List() ([]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build fresh slice from data map - eliminates race condition from lock upgrade
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

	return items, nil
}

// Add inserts a new resource into the store.
// If resources with the same index keys already exist, the new resource is appended.
// The slice is kept sorted by namespace/name for deterministic Get() results.
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

	// Keep slice sorted for deterministic Get() results without runtime sorting
	sortResourceSlice(s.data[keyStr])

	s.modCount++

	return nil
}

// sortResourceSlice sorts a slice of resources by namespace and name.
// Used to maintain sorted order at insert time for zero-copy reads.
func sortResourceSlice(items []interface{}) {
	sort.Slice(items, func(i, j int) bool {
		nsI, nameI := extractNamespaceName(items[i])
		nsJ, nameJ := extractNamespaceName(items[j])
		if nsI != nsJ {
			return nsI < nsJ
		}
		return nameI < nameJ
	})
}

// Update modifies an existing resource or adds it if it doesn't exist.
// For non-unique index keys, it finds the resource by namespace+name and replaces it.
// The slice is kept sorted by namespace/name for deterministic Get() results.
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
		// No resources with these keys - add new (single element, already sorted)
		s.data[keyStr] = []interface{}{resource}
		s.modCount++
		return nil
	}

	// Try to find existing resource by namespace+name
	ns, name := extractNamespaceName(resource)
	for i, existing := range resources {
		existingNs, existingName := extractNamespaceName(existing)
		if existingNs == ns && existingName == name {
			// Replace existing resource (sort order unchanged since ns/name same)
			resources[i] = resource
			s.data[keyStr] = resources
			s.modCount++
			return nil
		}
	}

	// Resource not found - append and re-sort
	s.data[keyStr] = append(resources, resource)
	sortResourceSlice(s.data[keyStr])
	s.modCount++
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
		s.modCount++
	}

	return nil
}

// Clear removes all resources from the store.
func (s *MemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string][]interface{})
	s.modCount++

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

// ModCount returns the modification counter and whether tracking is supported.
// The counter is incremented on every mutation (Add, Update, Delete, Clear).
// This enables external caching layers to detect store changes without polling.
func (s *MemoryStore) ModCount() (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modCount, true
}

// Ensure MemoryStore implements types.Store interface.
var _ types.Store = (*MemoryStore)(nil)
