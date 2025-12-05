# Resource Storage Implementations

## Overview

This package provides storage backends for indexed Kubernetes resources. The controller uses stores to maintain fast-access collections of watched resources for template rendering. You choose between two storage strategies depending on your resource access patterns and memory constraints.

**When to use this package:**

- Building custom resource watchers that need indexed storage
- Implementing resource caching strategies
- Optimizing memory usage for large resource collections
- Creating high-performance resource lookup mechanisms

The package offers two complementary store types:

- **MemoryStore**: Complete in-memory storage for fast access and iteration
- **CachedStore**: Reference-based storage with on-demand API fetching and TTL caching

Both implement the `types.Store` interface, allowing transparent switching between storage strategies.

## Features

- **Multiple Storage Strategies**: Choose between full memory or cached on-demand
- **Indexed Lookups**: O(1) access using composite keys
- **Non-Unique Keys**: Multiple resources can share the same index key
- **Thread-Safe**: Concurrent access from multiple goroutines
- **Field Filtering**: Integration with indexer for memory optimization
- **TTL Caching**: Automatic cache expiration in CachedStore

## Quick Start

### Memory Store

```go
package main

import (
    "haproxy-template-ic/pkg/k8s/store"
)

func main() {
    // Create memory store with 2 index keys (namespace, name)
    memStore := store.NewMemoryStore(2)

    // Add resource with index keys
    resource := map[string]interface{}{
        "metadata": map[string]interface{}{
            "namespace": "default",
            "name":      "my-ingress",
        },
        "spec": map[string]interface{}{
            "rules": []interface{}{/* ... */},
        },
    }

    keys := []string{"default", "my-ingress"}
    memStore.Add(resource, keys)

    // Retrieve by keys
    resources, _ := memStore.Get("default", "my-ingress")
    // resources contains [resource]

    // Retrieve all in namespace
    resources, _ = memStore.Get("default")
    // resources contains all resources in "default" namespace
}
```

### Cached Store

```go
package main

import (
    "time"
    "haproxy-template-ic/pkg/k8s/store"
    "haproxy-template-ic/pkg/k8s/indexer"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/client-go/dynamic"
)

func main() {
    // Setup (assumes you have client and indexer)
    cfg := &store.CachedStoreConfig{
        NumKeys:   2,
        CacheTTL:  10 * time.Minute,
        Client:    dynamicClient,
        GVR: schema.GroupVersionResource{
            Group:    "",
            Version:  "v1",
            Resource: "secrets",
        },
        Namespace: "",  // All namespaces
        Indexer:   myIndexer,
    }

    cachedStore, _ := store.NewCachedStore(cfg)

    // Add reference (from watcher)
    keys := []string{"default", "my-secret"}
    cachedStore.Add(nil, keys)  // Only stores reference, not resource

    // Get triggers API fetch and caching
    resources, _ := cachedStore.Get("default", "my-secret")
    // First call: fetches from API
    // Subsequent calls within TTL: returns cached

    // Clear cache when needed
    cachedStore.ClearCache()
}
```

## Storage Strategy Comparison

| Aspect | MemoryStore | CachedStore |
|--------|-------------|-------------|
| **Storage** | Full resources in memory | Only references + TTL cache |
| **Lookup Speed** | O(1), instant | O(1) on cache hit, API latency on miss |
| **Memory Usage** | ~1KB per resource (varies by type) | Minimal (refs) + bounded cache |
| **API Calls** | Initial list only | Initial list + fetch on cache miss |
| **Best For** | Iterating all resources | Selective access to resources |
| **Template Usage** | `resources.<type>` (list all) | `resources.<type>.Fetch(ns, name)` |
| **Typical Resources** | Ingress, Service, EndpointSlice | Secret, ConfigMap with large data |

## MemoryStore

### Overview

MemoryStore keeps complete resource objects in memory after field filtering. This provides instant access for template rendering at the cost of higher memory usage.

### How It Works

```
1. Watcher receives resource from Kubernetes API
2. Indexer extracts index keys (e.g., ["default", "my-ingress"])
3. Indexer filters unnecessary fields (managedFields, etc.)
4. MemoryStore stores complete resource at composite key "default/my-ingress"
5. Template accesses resource via Get("default", "my-ingress") → instant return
```

### API Reference

#### Creating a MemoryStore

```go
func NewMemoryStore(numKeys int) *MemoryStore
```

**Parameters:**

- `numKeys`: Number of index keys (must match indexer configuration)

**Example:**

```go
// For indexing by [namespace, name]
store := store.NewMemoryStore(2)

// For indexing by [namespace, name, label]
store := store.NewMemoryStore(3)
```

#### Adding Resources

```go
func (s *MemoryStore) Add(resource interface{}, keys []string) error
```

Stores a resource with the given index keys.

**Parameters:**

- `resource`: The resource object (typically `*unstructured.Unstructured` or map)
- `keys`: Index key values extracted from the resource

**Returns:** Error if key count doesn't match `numKeys`

**Example:**

```go
keys := []string{"default", "my-ingress"}
err := store.Add(ingressResource, keys)
```

#### Retrieving Resources

```go
func (s *MemoryStore) Get(keys ...string) ([]interface{}, error)
```

Retrieves resources matching the provided keys. Supports partial key matching.

**Parameters:**

- `keys`: One or more index keys to match

**Returns:**

- Slice of matching resources
- Error if too many keys provided

**Examples:**

```go
// Get specific resource (all keys)
resources, _ := store.Get("default", "my-ingress")
// Returns: resources with namespace=default AND name=my-ingress

// Get all in namespace (partial keys)
resources, _ := store.Get("default")
// Returns: all resources with namespace=default

// Get all resources (no keys)
resources, _ := store.List()
```

#### Updating Resources

```go
func (s *MemoryStore) Update(resource interface{}, keys []string) error
```

Updates an existing resource. If not found, adds it.

**Example:**

```go
keys := []string{"default", "my-ingress"}
err := store.Update(updatedResource, keys)
```

#### Deleting Resources

```go
func (s *MemoryStore) Delete(keys ...string) error
```

Removes resources matching the keys.

**Example:**

```go
// Delete specific resource
err := store.Delete("default", "my-ingress")
```

#### Listing All Resources

```go
func (s *MemoryStore) List() ([]interface{}, error)
```

Returns all resources in the store.

**Example:**

```go
allResources, _ := store.List()
for _, res := range allResources {
    // Process each resource
}
```

#### Clearing Store

```go
func (s *MemoryStore) Clear() error
```

Removes all resources from the store.

### Memory Usage

Approximate memory per resource (after field filtering):

- **Ingress**: 1-2 KB
- **Service**: 1-5 KB (depends on endpoints)
- **EndpointSlice**: 2-5 KB
- **ConfigMap**: 1 KB + data size
- **Secret**: 1 KB + data size

**Example calculation:**

```
1000 Ingress × 1.5 KB = 1.5 MB
500 Services × 2 KB = 1 MB
2000 EndpointSlices × 3 KB = 6 MB
Total: ~8.5 MB for 3500 resources
```

### When to Use MemoryStore

Use MemoryStore when:

- You iterate over most/all resources during template rendering
- Template uses `{% for ingress in resources.ingresses %}`
- Fast template rendering is critical
- Resource count is reasonable (< 10,000 per type)
- Resources are small to medium size

## CachedStore

### Overview

CachedStore stores only resource references (namespace + name + index keys) in memory and fetches complete resources from the Kubernetes API on demand. Fetched resources are cached with a TTL to reduce API calls.

### How It Works

```
1. Watcher receives resource from Kubernetes API
2. Indexer extracts index keys (e.g., ["default", "my-secret"])
3. CachedStore stores reference {namespace: "default", name: "my-secret", keys: [...]
4. Template calls Fetch("default", "my-secret")
5. CachedStore checks TTL cache
   - Cache hit → return cached resource
   - Cache miss → fetch from API, cache with TTL, return
```

### API Reference

#### Creating a CachedStore

```go
func NewCachedStore(cfg *CachedStoreConfig) (*CachedStore, error)
```

**Configuration:**

```go
type CachedStoreConfig struct {
    NumKeys   int                         // Number of index keys
    CacheTTL  time.Duration               // Cache entry TTL
    Client    dynamic.Interface           // Kubernetes client
    GVR       schema.GroupVersionResource // Resource type
    Namespace string                      // Namespace filter (empty = all)
    Indexer   *indexer.Indexer            // Field filter
    Logger    *slog.Logger                // Optional logger
}
```

**Example:**

```go
cfg := &store.CachedStoreConfig{
    NumKeys:  2,
    CacheTTL: 10 * time.Minute,
    Client:   dynamicClient,
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "secrets",
    },
    Namespace: "",
    Indexer:   indexer.New([]string{"metadata.namespace", "metadata.name"}, nil),
}

store, err := store.NewCachedStore(cfg)
```

#### Adding References

```go
func (s *CachedStore) Add(resource interface{}, keys []string) error
```

Stores a resource reference. The `resource` parameter is typically `nil` since only keys matter.

**Example:**

```go
// Add reference (from watcher)
keys := []string{"default", "tls-cert"}
err := store.Add(nil, keys)
```

#### Fetching Resources

```go
func (s *CachedStore) Get(keys ...string) ([]interface{}, error)
```

Fetches resources matching keys. Triggers API fetch on cache miss.

**Behavior:**

1. Finds matching references by keys
2. For each reference:
   - Check TTL cache using "namespace/name" key
   - Cache hit: return cached resource
   - Cache miss: fetch from API, cache with TTL
3. Return all fetched resources

**Example:**

```go
// Fetch specific secret
resources, err := store.Get("default", "tls-cert")
// First call: API fetch + cache
// Calls within TTL: cached return

// Fetch all secrets in namespace
resources, err := store.Get("default")
// Fetches each matching secret individually
```

#### Cache Management

```go
func (s *CachedStore) ClearCache() error
```

Clears the TTL cache, forcing fresh fetches.

**Example:**

```go
// Force fresh fetch on next Get()
store.ClearCache()
```

```go
func (s *CachedStore) GetCacheStats() (hits, misses int)
```

Returns cache hit/miss statistics for monitoring.

### Memory Usage

CachedStore memory usage is bounded:

```
Base memory = (reference count) × 200 bytes
Cache memory = (cached entries) × (resource size)
Total = Base + min(Cache, MaxCacheSize × AvgResourceSize)
```

**Example:**

```
1000 Secret references × 200 bytes = 200 KB
Cache: 100 entries × 5 KB = 500 KB
Total: ~700 KB (vs 5 MB for MemoryStore)
```

### When to Use CachedStore

Use CachedStore when:

- Resources are large (Secrets with certificates, ConfigMaps with big data)
- Template accesses only a few specific resources (not iteration)
- Memory is constrained
- Resources change infrequently (TTL helps)
- Template uses `resources.<type>.Fetch(namespace, name)`

### TTL Configuration

Choose TTL based on access patterns:

- **Short TTL** (1-5 min): Frequently changing resources
- **Medium TTL** (10-30 min): Moderate change rate
- **Long TTL** (1 hour+): Rarely changing resources

**Trade-off:** Longer TTL = less API load but potentially stale data

## Error Handling

Both stores return `StoreError` for operation failures:

```go
type StoreError struct {
    Operation string   // "add", "get", "update", "delete"
    Keys      []string // Index keys involved
    Err       error    // Underlying error
}
```

**Example:**

```go
resources, err := store.Get("default", "missing")
if err != nil {
    var storeErr *store.StoreError
    if errors.As(err, &storeErr) {
        log.Error("store operation failed",
            "operation", storeErr.Operation,
            "keys", storeErr.Keys,
            "error", storeErr.Err)
    }
}
```

## Integration with Watcher

Stores are typically created and managed by watchers:

```go
// In pkg/k8s/watcher
config := k8s.WatcherConfig{
    GVR: ingressGVR,
    IndexBy: []string{"metadata.namespace", "metadata.name"},
    StoreType: k8s.StoreTypeMemory,  // or StoreTypeCached
    // ...
}

watcher := watcher.NewWatcher(client, config)
// Watcher creates appropriate store internally
```

See `pkg/k8s/watcher` for integration details.

## Performance Characteristics

### MemoryStore Performance

- **Add/Update**: O(1) amortized
- **Get (all keys)**: O(1)
- **Get (partial keys)**: O(N) where N = matching resources
- **List**: O(N) where N = total resources
- **Delete**: O(1)

### CachedStore Performance

- **Add/Update**: O(1)
- **Get (cache hit)**: O(1)
- **Get (cache miss)**: O(1) + API latency (~10-50ms)
- **List**: Not recommended (fetches each individually)
- **Delete**: O(1)

## Thread Safety

Both stores use `sync.RWMutex` for concurrent access:

```go
// Safe to call from multiple goroutines
go func() {
    store.Add(resource1, keys1)
}()

go func() {
    resources, _ := store.Get("default")
}()
```

**Read operations** (Get, List) use read locks, allowing concurrent reads.
**Write operations** (Add, Update, Delete) use write locks, blocking all access.

## Best Practices

### Choosing Store Type

```go
// Use MemoryStore for:
watched_resources:
  ingresses:
    store: full  # Iterate in templates
  services:
    store: full  # Frequently accessed
  endpoints:
    store: full  # Small, many accesses

// Use CachedStore for:
  secrets:
    store: on-demand  # Large, selective access
    cache_ttl: 15m
  configmaps:
    store: on-demand  # Potentially large data
    cache_ttl: 10m
```

### Index Key Selection

Choose index keys based on access patterns:

```go
// Common pattern: namespace + name
index_by:
  - metadata.namespace
  - metadata.name

// Advanced: namespace + label
index_by:
  - metadata.namespace
  - metadata.labels['app']
```

### Memory Optimization

For MemoryStore, filter unnecessary fields:

```go
// In indexer configuration
remove_fields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
```

See `pkg/k8s/indexer` for field filtering details.

## Testing

### Unit Testing with MemoryStore

```go
func TestMemoryStore(t *testing.T) {
    store := store.NewMemoryStore(2)

    resource := map[string]interface{}{
        "metadata": map[string]interface{}{
            "namespace": "default",
            "name":      "test",
        },
    }

    err := store.Add(resource, []string{"default", "test"})
    require.NoError(t, err)

    resources, err := store.Get("default", "test")
    require.NoError(t, err)
    assert.Len(t, resources, 1)
}
```

### Testing CachedStore

```go
func TestCachedStore(t *testing.T) {
    fakeClient := fake.NewSimpleDynamicClient(runtime.NewScheme())

    cfg := &store.CachedStoreConfig{
        NumKeys:  2,
        CacheTTL: 1 * time.Minute,
        Client:   fakeClient,
        GVR:      secretGVR,
        Indexer:  indexer.New([]string{"metadata.namespace", "metadata.name"}, nil),
    }

    store, err := store.NewCachedStore(cfg)
    require.NoError(t, err)

    // Add reference
    err = store.Add(nil, []string{"default", "my-secret"})
    require.NoError(t, err)

    // Create resource in fake client
    secret := &v1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Namespace: "default",
            Name:      "my-secret",
        },
    }
    fakeClient.Resource(secretGVR).Namespace("default").Create(ctx, toUnstructured(secret), metav1.CreateOptions{})

    // Fetch should succeed
    resources, err := store.Get("default", "my-secret")
    require.NoError(t, err)
    assert.Len(t, resources, 1)
}
```

## See Also

- [Kubernetes Package](../README.md) - K8s integration overview
- [Watcher Package](../watcher/README.md) - Resource watching with stores
- [Indexer Package](../indexer/README.md) - Index key extraction and field filtering
- [Types Package](../types/README.md) - Store interface definition
- [Watching Resources Guide](../../../docs/watching-resources.md) - User guide for store configuration
