package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
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
// Resource values are owned by the store and detached before public reads.
type MemoryStore struct {
	snapshotCommitFence stores.SnapshotCommitMutex
	mu                  sync.RWMutex
	data                map[string][]any            // Flat map: composite key -> slice of resources (pre-sorted)
	locations           map[resourceIdentity]string // Resource identity -> composite key
	numKeys             int                         // Number of index keys
	revisions           revisionState
	readRoot            atomic.Pointer[memoryReadRoot]
}

// NewMemoryStore creates a new memory-backed store.
//
// Parameters:
//   - numKeys: Number of index keys (must match indexer configuration)
func NewMemoryStore(numKeys int) *MemoryStore {
	if numKeys < 1 {
		numKeys = 1
	}

	store := &MemoryStore{
		data:      make(map[string][]any),
		locations: make(map[resourceIdentity]string),
		numKeys:   numKeys,
		revisions: newRevisionState(defaultRevisionJournalCapacity),
	}
	store.readRoot.Store(newMemoryReadRoot(numKeys, &store.revisions))
	return store
}

// Get retrieves all resources matching the provided index keys.
//
// Returned resources are detached from store-owned values.
func (s *MemoryStore) Get(keys ...string) ([]any, error) {
	s.mu.RLock()
	items, err := s.getLocked(keys)
	items = slices.Clone(items)
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return detachMemoryStoreReadItems(items)
}

func (s *MemoryStore) getLocked(keys []string) ([]any, error) {
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
		keyStr := indexer.EncodeKey(keys)
		if items, ok := s.data[keyStr]; ok {
			// Return direct reference - slice is pre-sorted at insert time
			// Callers must not modify (see Immutability Contract)
			return items, nil
		}
		return []any{}, nil
	}

	// Partial match: return all resources matching prefix
	// Must construct new slice as it aggregates from multiple internal slices
	var results []any
	encodedPrefix := indexer.EncodeKey(keys)

	for key, items := range s.data {
		if indexer.HasEncodedKeyPrefix(key, encodedPrefix) {
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
	items := s.listLocked()
	s.mu.RUnlock()
	return detachMemoryStoreReadItems(items)
}

func (s *MemoryStore) listLocked() []any {
	var items []any
	for _, resourceSlice := range s.data {
		items = append(items, resourceSlice...)
	}

	// Sort items by namespace and name for deterministic order
	slices.SortFunc(items, compareByNamespaceName)

	return items
}

// Add inserts a resource, replacing the same namespace/name if already present.
// Distinct resources with the same index keys share the bucket.
// The slice is kept sorted by namespace/name for deterministic Get() results.
func (s *MemoryStore) Add(resource any, keys []string) error {
	if err := validateKeyCount("add", keys, s.numKeys); err != nil {
		return err
	}
	owned, err := ownMemorySnapshotResource(resource)
	if err != nil {
		return &StoreError{Operation: "add", Keys: keys, Cause: err}
	}
	resource = owned

	s.snapshotCommitFence.Lock()
	defer s.snapshotCommitFence.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := indexer.EncodeKey(keys)
	identity, identified := identifyResource(resource)
	dataKeys := []string{keyStr}
	var identities []resourceIdentity
	var oldKeys []string
	if identified {
		identities = append(identities, identity)
		if s.identityUnchangedLocked(identity, keyStr, resource) {
			return nil
		}
		oldKeys = cloneStrings(s.revisions.identityKeys[identity])
		if oldKey, exists := s.locations[identity]; exists {
			dataKeys = append(dataKeys, oldKey)
		}
		s.removeIdentityLocked(identity)
		s.locations[identity] = keyStr
	}
	s.data[keyStr] = append(s.data[keyStr], resource)

	// Keep slice sorted for deterministic Get() results without runtime sorting
	sortResourceSlice(s.data[keyStr])
	s.revisions.recordUpsert(identity, identified, keys)
	s.publishReadRootLocked(dataKeys, identities, oldKeys, keys)

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
// A changed index key moves the namespace/name identity between buckets.
// The slice is kept sorted by namespace/name for deterministic Get() results.
func (s *MemoryStore) Update(resource any, keys []string) error {
	if err := validateKeyCount("update", keys, s.numKeys); err != nil {
		return err
	}
	owned, err := ownMemorySnapshotResource(resource)
	if err != nil {
		return &StoreError{Operation: "update", Keys: keys, Cause: err}
	}
	resource = owned

	s.snapshotCommitFence.Lock()
	defer s.snapshotCommitFence.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := indexer.EncodeKey(keys)
	identity, identified := identifyResource(resource)
	dataKeys := []string{keyStr}
	var identities []resourceIdentity
	var oldKeys []string
	if identified {
		identities = append(identities, identity)
		if s.identityUnchangedLocked(identity, keyStr, resource) {
			return nil
		}
		oldKeys = cloneStrings(s.revisions.identityKeys[identity])
		if oldKey, exists := s.locations[identity]; exists {
			dataKeys = append(dataKeys, oldKey)
		}
		s.removeIdentityLocked(identity)
		s.locations[identity] = keyStr
	} else {
		s.removeUntrackedIdentityLocked(keyStr, resource)
	}

	s.data[keyStr] = append(s.data[keyStr], resource)
	sortResourceSlice(s.data[keyStr])
	s.revisions.recordUpsert(identity, identified, keys)
	s.publishReadRootLocked(dataKeys, identities, oldKeys, keys)
	return nil
}

// Delete removes the single resource identified by namespace/name from its
// recorded bucket, leaving any siblings in place. The keys validate shape.
// Deleting a resource that is not present is a no-op.
//
// The bucket's map entry is removed once its last resource is deleted —
// leaving an empty slice behind would leak a map key per churned bucket and
// still be walked by the prefix scan in Get.
func (s *MemoryStore) Delete(namespace, name string, keys []string) error {
	s.snapshotCommitFence.Lock()
	defer s.snapshotCommitFence.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount(opDelete, keys, s.numKeys); err != nil {
		return err
	}
	if err := validateDeleteName(name, keys); err != nil {
		return err
	}

	identity := resourceIdentity{namespace: namespace, name: name}
	if _, exists := s.locations[identity]; !exists {
		return nil
	}
	keyStr := s.locations[identity]
	oldKeys := cloneStrings(s.revisions.identityKeys[identity])
	s.removeIdentityLocked(identity)
	s.revisions.recordDelete(identity)
	s.publishReadRootLocked([]string{keyStr}, []resourceIdentity{identity}, oldKeys)

	return nil
}

func (s *MemoryStore) removeIdentityLocked(identity resourceIdentity) {
	keyStr, ok := s.locations[identity]
	if !ok {
		return
	}
	s.removeIdentityFromBucketLocked(keyStr, identity)
	delete(s.locations, identity)
}

func (s *MemoryStore) identityUnchangedLocked(identity resourceIdentity, key string, resource any) bool {
	currentKey, exists := s.locations[identity]
	if !exists || currentKey != key {
		return false
	}
	for _, current := range s.data[currentKey] {
		currentNamespace, currentName := extractNamespaceName(current)
		if currentNamespace == identity.namespace && currentName == identity.name {
			return reflect.DeepEqual(current, resource)
		}
	}
	return false
}

func (s *MemoryStore) removeUntrackedIdentityLocked(keyStr string, resource any) {
	namespace, name := extractNamespaceName(resource)
	s.removeIdentityFromBucketLocked(keyStr, resourceIdentity{namespace: namespace, name: name})
}

func (s *MemoryStore) removeIdentityFromBucketLocked(keyStr string, identity resourceIdentity) {
	resources, ok := s.data[keyStr]
	if !ok {
		return
	}

	remaining := make([]any, 0, len(resources))
	for _, existing := range resources {
		namespace, name := extractNamespaceName(existing)
		if namespace == identity.namespace && name == identity.name {
			continue
		}
		remaining = append(remaining, existing)
	}

	if len(remaining) == 0 {
		delete(s.data, keyStr)
		return
	}
	s.data[keyStr] = remaining
}

// Clear removes all resources from the store.
func (s *MemoryStore) Clear() error {
	s.snapshotCommitFence.Lock()
	defer s.snapshotCommitFence.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	resourceCount := s.sizeLocked()
	if resourceCount == 0 {
		return nil
	}
	dataKeys := make([]string, 0, len(s.data))
	for key := range s.data {
		dataKeys = append(dataKeys, key)
	}
	identities := make([]resourceIdentity, 0, len(s.locations))
	keySets := make([][]string, 0, len(s.revisions.identityKeys))
	for identity := range s.locations {
		identities = append(identities, identity)
	}
	for _, keys := range s.revisions.identityKeys {
		keySets = append(keySets, cloneStrings(keys))
	}
	s.revisions.recordClear(resourceCount)
	s.data = make(map[string][]any)
	s.locations = make(map[resourceIdentity]string)
	s.publishReadRootLocked(dataKeys, identities, keySets...)

	return nil
}

// Size returns the number of resources in the store.
func (s *MemoryStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sizeLocked()
}

func (s *MemoryStore) sizeLocked() int {
	count := 0
	for _, resources := range s.data {
		count += len(resources)
	}
	return count
}

// ListRevision returns the revision for List results.
func (s *MemoryStore) ListRevision() stores.Revision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revisions.listRevision()
}

// GetRevision returns the revision for one exact or prefix Get result.
func (s *MemoryStore) GetRevision(keys ...string) stores.Revision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(keys) == 0 || len(keys) > s.numKeys {
		return ""
	}
	if s.revisions.exactUnsupported {
		return ""
	}
	return s.revisions.getRevision(keys)
}

// IdentityRevision returns the revision for one namespace/name identity.
func (s *MemoryStore) IdentityRevision(namespace, name string) stores.Revision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if name == "" || s.revisions.exactUnsupported {
		return ""
	}
	return s.revisions.identityRevision(resourceIdentity{namespace: namespace, name: name})
}

// ListSnapshot returns a detached List result and its pinned journal sequence.
func (s *MemoryStore) ListSnapshot() (items []any, sequence uint64, err error) {
	snapshot, err := s.Pin()
	if err != nil {
		return nil, s.memorySnapshotSequence(), err
	}
	items, err = snapshot.List()
	return items, snapshot.Sequence(), err
}

// ChangesSince returns retained mutations after sequence.
func (s *MemoryStore) ChangesSince(sequence uint64) (uint64, []stores.RevisionChange, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revisions.changesSince(sequence)
}

func (s *MemoryStore) ExactRevisionJournalSource() stores.RevisionSource {
	return s.RevisionSource()
}

// GetIdentity returns one resource through the namespace/name location index.
func (s *MemoryStore) GetIdentity(namespace, name string) (item any, found bool, err error) {
	s.mu.RLock()
	item, found = s.getIdentityLocked(namespace, name)
	s.mu.RUnlock()
	if !found {
		return item, found, nil
	}
	item, err = detachMemoryStoreReadValue(item)
	return item, err == nil, err
}

func (s *MemoryStore) getIdentityLocked(namespace, name string) (item any, found bool) {
	key, exists := s.locations[resourceIdentity{namespace: namespace, name: name}]
	if !exists {
		return nil, false
	}
	for _, resource := range s.data[key] {
		resourceNamespace, resourceName := extractNamespaceName(resource)
		if resourceNamespace == namespace && resourceName == name {
			return resource, true
		}
	}
	return nil, false
}

// RevisionSource returns the store's stable cache identity.
func (s *MemoryStore) RevisionSource() stores.RevisionSource {
	return stores.RevisionSource(s.revisions.source)
}

// GetSnapshot binds a keyed result to its revision and journal sequence.
func (s *MemoryStore) GetSnapshot(
	keys ...string,
) (items []any, revision stores.Revision, sequence uint64, err error) {
	snapshot, err := s.Pin()
	if err != nil {
		return nil, "", s.memorySnapshotSequence(), err
	}
	items, err = snapshot.Get(keys...)
	return items, snapshot.GetRevision(keys...), snapshot.Sequence(), err
}

// IdentitySnapshot binds an identity result to its revision and journal sequence.
func (s *MemoryStore) IdentitySnapshot(
	namespace, name string,
) (item any, found bool, revision stores.Revision, sequence uint64, err error) {
	snapshot, err := s.Pin()
	if err != nil {
		return nil, false, "", s.memorySnapshotSequence(), err
	}
	item, found, err = snapshot.GetIdentity(namespace, name)
	return item, found, snapshot.IdentityRevision(namespace, name), snapshot.Sequence(), err
}

func (s *MemoryStore) memorySnapshotSequence() uint64 {
	root := s.readRoot.Load()
	if root == nil {
		return 0
	}
	return root.sequence
}

func (s *MemoryStore) AcquireSnapshotCommitFence(ctx context.Context) (func(), error) {
	return s.snapshotCommitFence.Acquire(ctx)
}

// Ensure MemoryStore implements types.Store interface.
var (
	_ types.Store                 = (*MemoryStore)(nil)
	_ stores.Revisioned           = (*MemoryStore)(nil)
	_ stores.RevisionJournal      = (*MemoryStore)(nil)
	_ stores.ExactRevisionJournal = (*MemoryStore)(nil)
	_ stores.IdentityGetter       = (*MemoryStore)(nil)
	_ stores.SnapshotReader       = (*MemoryStore)(nil)
	_ stores.SnapshotCommitFencer = (*MemoryStore)(nil)
)
