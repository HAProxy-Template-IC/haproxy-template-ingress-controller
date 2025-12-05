# pkg/k8s/store - Resource Storage

Development context for Kubernetes resource storage implementations.

**API Documentation**: See `pkg/k8s/store/README.md`
**Architecture**: See `/docs/development/design.md` (Resource Indexing section)

## When to Work Here

Modify this package when:

- Adding new storage strategies
- Optimizing memory usage or performance
- Fixing storage bugs
- Adding cache eviction policies
- Improving thread safety

**DO NOT** modify this package for:

- Resource watching → Use `pkg/k8s/watcher`
- Index key extraction → Use `pkg/k8s/indexer`
- Event coordination → Use `pkg/controller`

## Package Purpose

This package provides storage backends for indexed Kubernetes resources. Before this package existed, all resources were stored in memory. Now we have two strategies:

1. **MemoryStore**: Complete in-memory storage (original behavior)
2. **CachedStore**: Reference-based storage with on-demand API fetching (new)

This separation allows:

- Memory-constrained environments to use CachedStore for large resources
- High-performance environments to use MemoryStore for fast iteration
- Mixed strategies (MemoryStore for Ingress, CachedStore for Secrets)

## Architecture

### Storage Strategy Pattern

Both stores implement `types.Store` interface for transparent switching:

```go
// pkg/k8s/types/store.go
type Store interface {
    Add(resource interface{}, keys []string) error
    Get(keys ...string) ([]interface{}, error)
    Update(resource interface{}, keys []string) error
    Delete(keys ...string) error
    List() ([]interface{}, error)
    Clear() error
}
```

**Why this pattern:**

- Watchers don't need to know which store type they use
- Store type can be changed via configuration
- Testing with fake stores is straightforward
- Future store types can be added without changing watchers

### Composite Key Design

Both stores use composite keys for indexing:

```go
// Example with index_by: ["metadata.namespace", "metadata.name"]
keys := []string{"default", "my-ingress"}
compositeKey := "default/my-ingress"
```

**Why composite keys:**

- O(1) lookup using single map key
- Partial matching (Get("default") finds all in namespace)
- Simple implementation
- Efficient memory usage

### Non-Unique Keys

Stores support multiple resources with the same composite key:

```go
// Multiple resources can share keys
store.Add(resource1, []string{"default", "common-label"})
store.Add(resource2, []string{"default", "common-label"})

// Get returns both
resources, _ := store.Get("default", "common-label")
// len(resources) == 2
```

**Why non-unique keys:**

- Indexing by labels or other non-unique fields
- Partial key matching returns multiple results naturally
- Simplifies watcher logic (no uniqueness validation)

## Key Concepts

### MemoryStore Design

**Data structure:**

```go
type MemoryStore struct {
    mu       sync.RWMutex
    data     map[string][]interface{}  // Composite key -> resources
    numKeys  int                       // Expected key count
    allItems []interface{}             // Cached List() result
    dirty    bool                      // allItems needs rebuild
}
```

**Why this structure:**

- `map[string][]interface{}`: Handles non-unique keys naturally
- `allItems` cache: List() doesn't rebuild on every call
- `dirty` flag: Lazy rebuilding of List() cache
- `sync.RWMutex`: Multiple concurrent readers, single writer

### CachedStore Design

**Data structures:**

```go
type resourceRef struct {
    namespace string   // For API fetching
    name      string   // For API fetching
    indexKeys []string // For key matching
}

type cacheEntry struct {
    resource  interface{}
    expiresAt time.Time
}

type CachedStore struct {
    mu        sync.RWMutex
    refs      map[string][]resourceRef    // Composite key -> references
    cache     map[string]*cacheEntry      // "namespace/name" -> cached resource
    cacheTTL  time.Duration
    client    dynamic.Interface
    gvr       schema.GroupVersionResource
    // ...
}
```

**Why this structure:**

- `refs` map: Stores only metadata (200 bytes vs 1-5 KB per resource)
- `cache` map: Separate cache keyed by namespace/name
- TTL-based expiration: Automatic cache invalidation
- Dynamic client: Fetches any resource type

**Cache key vs Index key:**

- **Index key** (composite): Used for matching (e.g., "default/common-label")
- **Cache key** (namespace/name): Used for caching fetched resources (e.g., "default/my-secret")

This separation allows:

- Multiple references with same index key
- Unique cache entries per resource
- Efficient cache lookups by namespace/name

### Thread Safety Strategy

**Read-write lock pattern:**

```go
// Read operations (concurrent)
func (s *MemoryStore) Get(keys ...string) ([]interface{}, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    // Read from data map
}

// Write operations (exclusive)
func (s *MemoryStore) Add(resource interface{}, keys []string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    // Write to data map
}
```

**Why RWMutex:**

- Read-heavy workload (Get/List called frequently)
- Multiple watchers can read concurrently
- Only blocks during Add/Update/Delete

**Lock granularity:**

- Entire store is locked (not per-key)
- Simple, correct implementation
- Performance sufficient for expected load

If profiling shows lock contention, consider:

- Sharding maps by key prefix
- Lock-free data structures (complexity vs benefit trade-off)

## Common Patterns

### MemoryStore Add Pattern

```go
func (s *MemoryStore) Add(resource interface{}, keys []string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if len(keys) != s.numKeys {
        return &StoreError{
            Operation: "add",
            Keys:      keys,
            Err:       fmt.Errorf("expected %d keys, got %d", s.numKeys, len(keys)),
        }
    }

    keyStr := makeKeyString(keys)

    // Check if resource already exists
    for i, existing := range s.data[keyStr] {
        if resourcesEqual(existing, resource) {
            // Replace existing
            s.data[keyStr][i] = resource
            s.dirty = true
            return nil
        }
    }

    // Add new resource
    s.data[keyStr] = append(s.data[keyStr], resource)
    s.dirty = true

    return nil
}
```

**Key points:**

- Validates key count
- Checks for duplicates using `resourcesEqual`
- Appends to slice for non-unique keys
- Marks List() cache as dirty

### CachedStore Fetch Pattern

```go
func (s *CachedStore) Get(keys ...string) ([]interface{}, error) {
    // 1. Find matching references (under read lock)
    s.mu.RLock()
    keyStr := makeKeyString(keys)
    refs := s.refs[keyStr]
    s.mu.RUnlock()

    var results []interface{}
    for _, ref := range refs {
        // 2. Check cache (under read lock)
        s.mu.RLock()
        cacheKey := ref.namespace + "/" + ref.name
        entry, cached := s.cache[cacheKey]
        s.mu.RUnlock()

        if cached && time.Now().Before(entry.expiresAt) {
            // Cache hit
            results = append(results, entry.resource)
            continue
        }

        // 3. Fetch from API (no lock held)
        resource, err := s.fetchResource(ref)
        if err != nil {
            s.logger.Warn("failed to fetch resource", "ref", ref, "error", err)
            continue
        }

        // 4. Update cache (under write lock)
        s.mu.Lock()
        s.cache[cacheKey] = &cacheEntry{
            resource:  resource,
            expiresAt: time.Now().Add(s.cacheTTL),
        }
        s.mu.Unlock()

        results = append(results, resource)
    }

    return results, nil
}
```

**Key points:**

- Lock is not held during API calls (prevents blocking)
- Multiple lock/unlock cycles (fine-grained locking)
- TTL check before using cached entry
- Silent failures on fetch errors (logged as warnings)

### Resource Equality Check

```go
func resourcesEqual(a, b interface{}) bool {
    // Try GetUID() method first (fastest)
    type uidGetter interface {
        GetUID() types.UID
    }

    if aUID, ok := a.(uidGetter); ok {
        if bUID, ok := b.(uidGetter); ok {
            return aUID.GetUID() == bUID.GetUID()
        }
    }

    // Fall back to namespace/name comparison
    nsA, nameA := extractNamespaceName(a)
    nsB, nameB := extractNamespaceName(b)

    return nsA == nsB && nameA == nameB
}
```

**Why UID first:**

- UID is unique across cluster lifetime
- Faster than namespace/name extraction
- Handles edge cases (deleted and recreated resources)

## Testing Strategies

### Unit Tests for MemoryStore

```go
func TestMemoryStore_AddGet(t *testing.T) {
    store := NewMemoryStore(2)

    resource := map[string]interface{}{
        "metadata": map[string]interface{}{
            "namespace": "default",
            "name":      "test",
        },
    }

    // Test Add
    err := store.Add(resource, []string{"default", "test"})
    require.NoError(t, err)

    // Test Get (exact match)
    resources, err := store.Get("default", "test")
    require.NoError(t, err)
    assert.Len(t, resources, 1)

    // Test Get (partial match)
    resources, err = store.Get("default")
    require.NoError(t, err)
    assert.Len(t, resources, 1)
}

func TestMemoryStore_NonUniqueKeys(t *testing.T) {
    store := NewMemoryStore(2)

    // Add two resources with same keys
    resource1 := map[string]interface{}{"id": "1"}
    resource2 := map[string]interface{}{"id": "2"}

    store.Add(resource1, []string{"default", "label"})
    store.Add(resource2, []string{"default", "label"})

    // Both should be returned
    resources, _ := store.Get("default", "label")
    assert.Len(t, resources, 2)
}
```

### Testing CachedStore with Fake Client

```go
func TestCachedStore_Fetch(t *testing.T) {
    scheme := runtime.NewScheme()
    v1.AddToScheme(scheme)
    fakeClient := fake.NewSimpleDynamicClient(scheme)

    indexer := indexer.New([]string{"metadata.namespace", "metadata.name"}, nil)

    cfg := &CachedStoreConfig{
        NumKeys:  2,
        CacheTTL: 1 * time.Minute,
        Client:   fakeClient,
        GVR:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
        Indexer:  indexer,
    }

    store, err := NewCachedStore(cfg)
    require.NoError(t, err)

    // Add reference
    err = store.Add(nil, []string{"default", "my-secret"})
    require.NoError(t, err)

    // Create secret in fake client
    secret := &v1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Namespace: "default",
            Name:      "my-secret",
        },
        Data: map[string][]byte{"key": []byte("value")},
    }
    unstr, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
    fakeClient.Resource(cfg.GVR).Namespace("default").Create(
        context.Background(),
        &unstructured.Unstructured{Object: unstr},
        metav1.CreateOptions{},
    )

    // Fetch should succeed
    resources, err := store.Get("default", "my-secret")
    require.NoError(t, err)
    require.Len(t, resources, 1)

    // Verify it's in cache (second call shouldn't hit API)
    resources, err = store.Get("default", "my-secret")
    require.NoError(t, err)
    assert.Len(t, resources, 1)
}
```

### Concurrent Access Tests

```go
func TestMemoryStore_ConcurrentAccess(t *testing.T) {
    store := NewMemoryStore(2)

    var wg sync.WaitGroup
    errors := make(chan error, 100)

    // Concurrent writes
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            resource := map[string]interface{}{"id": id}
            if err := store.Add(resource, []string{"default", fmt.Sprintf("res-%d", id)}); err != nil {
                errors <- err
            }
        }(i)
    }

    // Concurrent reads
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if _, err := store.List(); err != nil {
                errors <- err
            }
        }()
    }

    wg.Wait()
    close(errors)

    // No errors should occur
    for err := range errors {
        t.Errorf("concurrent access error: %v", err)
    }

    // All 10 resources should be stored
    resources, _ := store.List()
    assert.Len(t, resources, 10)
}
```

## Common Pitfalls

### Mismatched Key Counts

**Problem**: Index has 2 keys, but Add called with 3.

```go
// Bad
store := NewMemoryStore(2)
store.Add(resource, []string{"default", "my-resource", "extra"})
// Error: expected 2 keys, got 3
```

**Solution**: Validate key count matches index configuration.

```go
// Good
numKeys := len(indexBy)
store := NewMemoryStore(numKeys)
keys := indexer.ExtractKeys(resource, indexBy)
store.Add(resource, keys)
```

### Not Handling Partial Matches

**Problem**: Expecting single result from partial key match.

```go
// Bad - assumes single result
resources, _ := store.Get("default")
resource := resources[0]  // Panic if empty or multiple!
```

**Solution**: Handle slice results properly.

```go
// Good
resources, err := store.Get("default")
if err != nil || len(resources) == 0 {
    return fmt.Errorf("no resources found")
}

for _, resource := range resources {
    // Process each resource
}
```

### CachedStore API Latency

**Problem**: Using CachedStore with List() or iteration.

```go
// Bad - triggers API call for each resource
cachedStore.Add(nil, []string{"default", "secret-1"})
cachedStore.Add(nil, []string{"default", "secret-2"})
// ...100 more references...

resources, _ := cachedStore.List()
// Triggers 100 API calls!
```

**Solution**: Use MemoryStore for iteration, CachedStore for selective access.

```go
// Good - MemoryStore for iteration
watched_resources:
  ingresses:
    store: full  # Will iterate in template

// CachedStore for selective access
  secrets:
    store: on-demand  # Access specific secrets via Fetch()
```

### Ignoring Cache TTL Expiration

**Problem**: Expecting cache to be valid forever.

```go
// Bad - cache might be stale
resources, _ := cachedStore.Get("default", "secret")
// If TTL expired, this triggers API fetch
```

**Solution**: Configure appropriate TTL for your use case.

```go
// Good - choose TTL based on change frequency
cfg := &CachedStoreConfig{
    CacheTTL: 10 * time.Minute,  // Secrets change infrequently
}

// For frequently changing resources
cfg := &CachedStoreConfig{
    CacheTTL: 1 * time.Minute,  // Short TTL
}
```

### Holding Lock During API Calls

**Problem**: Blocking all store operations during API fetch.

```go
// Bad - don't do this!
func (s *CachedStore) Get(keys ...string) ([]interface{}, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    // API call while holding lock - blocks everything!
    resource, _ := s.client.Resource(s.gvr).Get(ctx, name, metav1.GetOptions{})

    return []interface{}{resource}, nil
}
```

**Solution**: Release lock before API calls (as shown in CachedStore implementation).

## Performance Optimization

### MemoryStore List() Caching

List() rebuilds result only when `dirty` flag is set:

```go
func (s *MemoryStore) List() ([]interface{}, error) {
    s.mu.RLock()

    if !s.dirty {
        // Return cached result (fast path)
        defer s.mu.RUnlock()
        return s.allItems, nil
    }

    s.mu.RUnlock()

    // Rebuild cache (slow path)
    s.mu.Lock()
    defer s.mu.Unlock()

    // Double-check dirty flag (might have been rebuilt)
    if !s.dirty {
        return s.allItems, nil
    }

    // Rebuild allItems from data map
    s.allItems = make([]interface{}, 0)
    for _, resources := range s.data {
        s.allItems = append(s.allItems, resources...)
    }

    s.dirty = false
    return s.allItems, nil
}
```

**Why this matters:**

- List() called frequently during template rendering
- Rebuilding from map every time is expensive O(N)
- Cache makes List() O(1) when no changes occurred

### CachedStore Memory Bounds

Cache size is unbounded currently. For production use, consider adding eviction:

```go
// TODO: Potential improvement
type CachedStore struct {
    // ...
    maxCacheSize int
}

func (s *CachedStore) evictOldest() {
    if len(s.cache) <= s.maxCacheSize {
        return
    }

    // Find and remove oldest entry
    var oldest string
    var oldestTime time.Time

    for key, entry := range s.cache {
        if oldestTime.IsZero() || entry.expiresAt.Before(oldestTime) {
            oldest = key
            oldestTime = entry.expiresAt
        }
    }

    delete(s.cache, oldest)
}
```

## Future Improvements

### Potential Enhancements

1. **LRU Cache for CachedStore**: Bounded cache with least-recently-used eviction
2. **Metrics**: Cache hit/miss rates, API latency, memory usage
3. **Sharded Maps**: Reduce lock contention for high-concurrency scenarios
4. **Batch Fetch**: Fetch multiple resources in single API call
5. **Predictive Caching**: Pre-fetch resources likely to be accessed

### When to Refactor

Consider refactoring if:

- Lock contention appears in profiling (unlikely with current workload)
- Cache memory usage becomes problematic (add eviction)
- API call rate becomes excessive (batch fetches, longer TTL)
- New storage backends needed (e.g., Redis-backed store)

## Troubleshooting

### Store Returns Empty Results

**Diagnosis:**

1. Check if resources were added
2. Verify key count matches
3. Check for key extraction errors

```go
// Debug store contents
resources, _ := store.List()
log.Info("store contents", "count", len(resources))

// Verify keys
for _, res := range resources {
    keys, _ := indexer.ExtractKeys(res, indexBy)
    log.Info("resource keys", "resource", res, "keys", keys)
}
```

### CachedStore Always Hits API

**Diagnosis:**

1. Check TTL configuration
2. Verify cache isn't being cleared
3. Check for clock skew

```go
// Debug cache state
hits, misses := cachedStore.GetCacheStats()
log.Info("cache stats", "hits", hits, "misses", misses)

// Check if cache is being populated
s.mu.RLock()
cacheSize := len(s.cache)
s.mu.RUnlock()
log.Info("cache size", "entries", cacheSize)
```

### Race Conditions

**Diagnosis:**

1. Run with race detector: `go test -race ./pkg/k8s/store`
2. Check for missing lock statements
3. Verify defer unlock patterns

```bash
# Run tests with race detector
go test -race -v ./pkg/k8s/store

# Run integration tests
go test -race -tags=integration ./tests/...
```

## Resources

- API documentation: `pkg/k8s/store/README.md`
- Watcher integration: `pkg/k8s/watcher/README.md`
- Indexer usage: `pkg/k8s/indexer/README.md`
- User guide: `/docs/watching-resources.md`
- Store interface: `pkg/k8s/types/store.go`
