package store

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

const opGet = "get"

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
	mu      sync.RWMutex
	data    map[string][]any // Flat map: composite key -> slice of resources (pre-sorted)
	numKeys int              // Number of index keys
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
		data:    make(map[string][]any),
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
func (s *MemoryStore) Get(keys ...string) ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(keys) == 0 {
		return nil, &StoreError{
			Operation: opGet,
			Keys:      keys,
			Cause:     errors.New("at least one key required"),
		}
	}

	if len(keys) > s.numKeys {
		return nil, &StoreError{
			Operation: opGet,
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
		return []any{}, nil
	}

	// Partial match: return all resources matching prefix
	// Must construct new slice as it aggregates from multiple internal slices
	prefix := makeKeyString(keys) + "/"
	var results []any

	for key, items := range s.data {
		// Check if key starts with prefix
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, items...)
		}
	}

	// Sort for deterministic order (same as List())
	slices.SortFunc(results, compareByNamespaceName)

	return results, nil
}

// List returns all resources in the store.
// Returns a fresh copy of all resources to avoid race conditions.
func (s *MemoryStore) List() ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build fresh slice from data map - eliminates race condition from lock upgrade
	var items []any
	for _, resourceSlice := range s.data {
		items = append(items, resourceSlice...)
	}

	// Sort items by namespace and name for deterministic order
	slices.SortFunc(items, compareByNamespaceName)

	return items, nil
}

// Add inserts a new resource into the store.
// If resources with the same index keys already exist, the new resource is appended.
// The slice is kept sorted by namespace/name for deterministic Get() results.
func (s *MemoryStore) Add(resource any, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount("add", keys, s.numKeys); err != nil {
		return err
	}

	keyStr := makeKeyString(keys)
	s.data[keyStr] = append(s.data[keyStr], resource)

	// Keep slice sorted for deterministic Get() results without runtime sorting
	sortResourceSlice(s.data[keyStr])

	return nil
}

// sortResourceSlice sorts a slice of resources by namespace and name.
// Used to maintain sorted order at insert time for zero-copy reads.
func sortResourceSlice(items []any) {
	slices.SortFunc(items, compareByNamespaceName)
}

// compareByNamespaceName compares two resources by namespace then name.
func compareByNamespaceName(a, b any) int {
	nsA, nameA := extractNamespaceName(a)
	nsB, nameB := extractNamespaceName(b)
	if c := cmp.Compare(nsA, nsB); c != 0 {
		return c
	}
	return cmp.Compare(nameA, nameB)
}

// Update modifies an existing resource or adds it if it doesn't exist.
// For non-unique index keys, it finds the resource by namespace+name and replaces it.
// The slice is kept sorted by namespace/name for deterministic Get() results.
func (s *MemoryStore) Update(resource any, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount("update", keys, s.numKeys); err != nil {
		return err
	}

	keyStr := makeKeyString(keys)
	resources, ok := s.data[keyStr]
	if !ok {
		// No resources with these keys - add new (single element, already sorted)
		s.data[keyStr] = []any{resource}
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
			return nil
		}
	}

	// Resource not found - append and re-sort
	s.data[keyStr] = append(resources, resource)
	sortResourceSlice(s.data[keyStr])
	return nil
}

// Delete removes the single resource identified by namespace/name from the
// bucket addressed by keys, leaving any siblings that share the bucket in
// place. Deleting a resource that is not present is a no-op.
//
// The bucket's map entry is removed once its last resource is deleted —
// leaving an empty slice behind would leak a map key per churned bucket and
// still be walked by the prefix scan in Get.
func (s *MemoryStore) Delete(namespace, name string, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount("delete", keys, s.numKeys); err != nil {
		return err
	}

	keyStr := makeKeyString(keys)
	resources, ok := s.data[keyStr]
	if !ok {
		return nil
	}

	// A fresh slice, never an in-place compaction: Get returns the bucket by
	// reference (see the Immutability Contract on Get), so compacting would
	// mutate a slice a render may still be holding.
	remaining := make([]any, 0, len(resources))
	for _, existing := range resources {
		existingNs, existingName := extractNamespaceName(existing)
		if existingNs == namespace && existingName == name {
			continue
		}
		remaining = append(remaining, existing)
	}

	if len(remaining) == 0 {
		delete(s.data, keyStr)
		return nil
	}

	// Filtering preserves the existing order, so the bucket stays sorted.
	s.data[keyStr] = remaining

	return nil
}

// Clear removes all resources from the store.
func (s *MemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string][]any)

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
