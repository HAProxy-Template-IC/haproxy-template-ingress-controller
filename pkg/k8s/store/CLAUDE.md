# pkg/k8s/store - Resource Storage

Development context for Kubernetes resource storage implementations.

**API Documentation**: See `pkg/k8s/store/README.md`
**Architecture**: See `/docs/controller/docs/development/design.md` (Resource Indexing section)

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
// pkg/k8s/types/types.go
type Store interface {
    Get(keys ...string) ([]any, error)
    List() ([]any, error)
    Add(resource any, keys []string) error
    Update(resource any, keys []string) error
    Delete(keys ...string) error
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
    data     map[string][]any // Composite key -> pre-sorted resource slice
    numKeys  int              // Expected key count
    modCount uint64           // Incremented on every mutation; used by ModCounter
}
```

**Why this structure:**

- `map[string][]any`: handles non-unique keys naturally; per-bucket slice is kept
  sorted at insert time so reads can return a direct reference (zero-copy).
- `modCount`: lets caching layers detect mutations without re-walking the store.
  Stores that expose this counter implement the optional `stores.ModCounter`
  interface.
- `sync.RWMutex`: multiple concurrent readers, single writer.

There is **no** `allItems` cache or `dirty` flag — `List()` walks the data map
on every call and sorts the aggregated result. The optimization is "buckets
are pre-sorted, so per-bucket reads are zero-copy", not "the whole list is
memoized".

### CachedStore Design

**Data structures:**

```go
type resourceRef struct {
    namespace string   // For API fetching
    name      string   // For API fetching
    indexKeys []string // For key matching
}

type CachedStore struct {
    mu        sync.RWMutex
    refs      map[string][]resourceRef            // Composite key -> references
    cache     *lru.Cache[string, *cacheEntry]     // LRU cache: "ns/name" -> cached resource
    numKeys   int
    cacheTTL  time.Duration
    client    dynamic.Interface
    gvr       schema.GroupVersionResource
    namespace string
    indexer   *indexer.Indexer
    logger    *slog.Logger
    modCount  uint64
}
```

**Why this structure:**

- `refs` map: stores only metadata (`namespace + name + index keys`) so the
  in-memory footprint per resource is tiny compared to a full Secret body.
- `cache` is an actual `lru.Cache`, not a plain map — entries beyond
  `MaxCacheSize` (default `DefaultMaxCacheSize = 256`) are evicted LRU. TTL
  (`cfg.CacheTTL`, default `2m10s`) is checked on read.
- `dynamic.Interface`: fetches any resource type without compiled-in schemas.
- `modCount`: same role as in `MemoryStore` — a monotonic counter for downstream
  cache invalidation.

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
func (s *MemoryStore) Get(keys ...string) ([]any, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    // Read from data map
}

// Write operations (exclusive)
func (s *MemoryStore) Add(resource any, keys []string) error {
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

### MemoryStore Add / Update Pattern

`Add` is a pure append — it does **not** dedupe. The dedupe (replace-by-namespace+name)
lives in `Update`, which keeps the per-key bucket sorted so subsequent `Get`s
can return the slice as-is. The shared key-validation helper is `validateKeyCount`,
not an inline check, and the `StoreError` field is `Cause`, not `Err`.

```go
func (s *MemoryStore) Add(resource any, keys []string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if err := validateKeyCount("add", keys, s.numKeys); err != nil {
        return err  // *StoreError{Operation, Keys, Cause}
    }

    keyStr := makeKeyString(keys)
    s.data[keyStr] = append(s.data[keyStr], resource)
    sortResourceSlice(s.data[keyStr]) // zero-copy reads later
    s.modCount++
    return nil
}

func (s *MemoryStore) Update(resource any, keys []string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if err := validateKeyCount("update", keys, s.numKeys); err != nil {
        return err
    }

    keyStr := makeKeyString(keys)
    bucket, ok := s.data[keyStr]
    if !ok {
        s.data[keyStr] = []any{resource}
        s.modCount++
        return nil
    }

    ns, name := extractNamespaceName(resource)
    for i, existing := range bucket {
        existingNs, existingName := extractNamespaceName(existing)
        if existingNs == ns && existingName == name {
            bucket[i] = resource // ns/name unchanged → sort order preserved
            s.modCount++
            return nil
        }
    }

    s.data[keyStr] = append(bucket, resource)
    sortResourceSlice(s.data[keyStr])
    s.modCount++
    return nil
}
```

**Key points:**

- `Add` is unconditional append — duplicates are possible if the watcher's
  delta logic is wrong. That's by design: cheap insert, dedupe lives in `Update`.
- Per-bucket sort happens at write time so `Get(exact-key)` can return the
  internal slice directly (see the Immutability Contract in `MemoryStore.Get`).
- Mutation increments `modCount` so cached layers can invalidate without polling.

### CachedStore Fetch Pattern

```go
func (s *CachedStore) Get(keys ...string) ([]any, error) {
    // 1. Find matching references (under read lock)
    s.mu.RLock()
    keyStr := makeKeyString(keys)
    refs := s.refs[keyStr]
    s.mu.RUnlock()

    var results []any
    for _, ref := range refs {
        cacheKey := ref.namespace + "/" + ref.name

        // 2. Check cache via LRU API (Peek doesn't bump recency; Get does)
        if entry, ok := s.cache.Peek(cacheKey); ok && time.Now().Before(entry.expiresAt) {
            results = append(results, entry.resource)
            continue
        }

        // 3. Fetch from API (no lock held — fetchResource does its own
        //    locking around s.cache.Add).
        resource, err := s.fetchResource(ref)
        if err != nil {
            s.logger.Warn("failed to fetch resource", "ref", ref, "error", err)
            continue
        }

        // 4. Cache (LRU's Add evicts the oldest entry if over MaxCacheSize)
        s.cache.Add(cacheKey, &cacheEntry{
            resource:  resource,
            expiresAt: time.Now().Add(s.cacheTTL),
        })

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

### Resource Identity

There is no `resourcesEqual` helper. The store identifies "same resource" by
`(namespace, name)` via `extractNamespaceName` (see `common.go:55`) and uses
that comparison inside `Update` and `Delete`. UID is **not** consulted, so a
deleted-and-recreated resource looks identical to its predecessor as far as
this store is concerned — that's correct, because the watcher generates
`Update` (not `Add`+`Delete`) on a re-create.

```go
nsA, nameA := extractNamespaceName(a)
nsB, nameB := extractNamespaceName(b)
sameResource := nsA == nsB && nameA == nameB
```

## Testing Strategies

### Unit Tests for MemoryStore

```go
func TestMemoryStore_AddGet(t *testing.T) {
    store := NewMemoryStore(2)

    resource := map[string]any{
        "metadata": map[string]any{
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
    resource1 := map[string]any{"id": "1"}
    resource2 := map[string]any{"id": "2"}

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
            resource := map[string]any{"id": id}
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
func (s *CachedStore) Get(keys ...string) ([]any, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    // API call while holding lock - blocks everything!
    resource, _ := s.client.Resource(s.gvr).Get(ctx, name, metav1.GetOptions{})

    return []any{resource}, nil
}
```

**Solution**: Release lock before API calls (as shown in CachedStore implementation).

## Performance Optimization

### Zero-copy reads, not memoized List()

Per-bucket slices are kept sorted at insert time, so `Get(exactKey)` returns
the internal slice directly — callers must respect the Immutability Contract
(no mutation, no append, no aliasing past the call). `List()` does **not**
memoize across calls; it rebuilds the aggregate slice every time and sorts it
by namespace/name. That's an explicit choice — the watcher path is the hot
path, and it almost never calls `List()`. The cache that benefits from
modCount lives one layer up in `pkg/k8s/store/cached.go`, not inside
`MemoryStore` itself.

If you find yourself wanting a memoized `List()`, look at `CachedStoreWrapper`
or read the modCount via `(stores.ModCounter).ModCount()` and rebuild only
when the counter changes — don't add a `dirty` flag inside `MemoryStore`.

### CachedStore Memory Bounds

The cache is an `lru.Cache[string, *cacheEntry]`, sized by `CachedStoreConfig.MaxCacheSize`
(default `DefaultMaxCacheSize = 256`). Entries beyond the limit are evicted in
LRU order automatically, so memory is bounded by `MaxCacheSize × per-resource-size`.

Tuning:

- Increase `MaxCacheSize` if your hot working set is larger than 256 entries
  (cache thrash shows up as repeated API fetches in the watcher logs).
- Lower `CacheTTL` if you need fresher reads at the cost of more API traffic;
  raise it to soak more reads against the in-memory copy.

## Future Improvements

### Potential Enhancements

1. **Metrics**: Cache hit/miss rates, API latency, memory usage. CachedStore
   currently has no internal hit/miss counters — adding them on the struct
   and exposing through a stats accessor (or directly through Prometheus
   collectors) is the prerequisite. The Troubleshooting section below
   documents the size accessors that exist today (`Size()` / `CacheSize()`).
2. **Sharded Maps**: Reduce lock contention for high-concurrency scenarios.
3. **Batch Fetch**: Fetch multiple resources in a single API call instead of
   per-reference Get loops.
4. **Predictive Caching**: Pre-fetch resources likely to be accessed.

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

1. Check TTL configuration — defaults to ~2m10s; if zero is being passed in
   somewhere upstream, every read will look stale.
2. Verify the LRU isn't undersized (`MaxCacheSize` defaults to 256). If the
   working set is larger, entries get evicted before they can be reused.
3. Check for clock skew (TTL is wall-clock based).

```go
// Inspect via the actual CachedStore API (no GetCacheStats() helper exists).
log.Info("cache state",
    "size",        cachedStore.Size(),       // total references the store knows about
    "cached",      cachedStore.CacheSize(),  // entries currently in the LRU cache
    "max",         cfg.MaxCacheSize,         // configured upper bound
    "ttl_seconds", cfg.CacheTTL.Seconds(),
)
```

If you need hit/miss counters, add them as Prometheus metrics on the
`CachedStore` itself — there's no built-in stats accessor today.

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
- User guide: `docs/controller/docs/watching-resources.md`
- Store interface: `pkg/k8s/types/types.go` (search for `type Store interface`)
