# pkg/k8s - Kubernetes Integration

Development context for Kubernetes resource watching and indexing.

**API Documentation**: See `pkg/k8s/README.md`
**Architecture**: See `/docs/controller/docs/development/design.md` (Kubernetes Integration section)

## When to Work Here

Modify this package when:

- Adding support for new Kubernetes resource types
- Changing indexing or storage strategies
- Modifying watcher behavior (debouncing, callbacks)
- Fixing Kubernetes client issues
- Adding new JSONPath expressions

**DO NOT** modify this package for:

- Event type definitions → Use `pkg/controller/events`
- Controller coordination → Use `pkg/controller`
- Template rendering → Use `pkg/templating`
- HAProxy sync → Use `pkg/dataplane`

## Package Structure

```
pkg/k8s/
├── types/           # Core interfaces (Store, WatcherConfig)
├── client/          # Kubernetes client wrapper
├── indexer/         # JSONPath evaluation and field filtering
├── store/           # MemoryStore and CachedStore implementations
├── watcher/         # Resource watching (Watcher and SingleWatcher)
└── leaderelection/  # Pure leader election component (no events)
```

## Key Concepts

### Two Watcher Types

**Watcher**: For bulk resource watching (Ingress, Service, EndpointSlice)

- Watches collections of resources
- Debounces rapid changes
- Indexed storage for fast lookups
- Tracks initial sync state

**SingleWatcher**: For single resource watching (ConfigMap, Secret)

- Watches one specific resource (namespace + name)
- Immediate callbacks (no debouncing)
- No indexing or store overhead
- Ideal for configuration and credentials

### Store Types

**MemoryStore**: Fast in-memory storage

- Complete resources stored in memory
- O(1) lookups via composite index keys
- Default choice for most resources
- Memory usage: ~1KB per resource

**CachedStore**: API-backed with TTL cache

- Only caches recently accessed resources
- Falls back to Kubernetes API for cache misses
- Use for large resources (Secrets, ConfigMaps with big data)
- Memory usage: Bounded by cache size

### Initial Sync Handling

Distinguishes between initial bulk load and real-time changes:

```go
// Watcher tracks sync state
watcher.IsSynced()  // Returns bool

// Callbacks receive context
func onChange(store Store, stats ChangeStats) {
    if stats.IsInitialSync {
        // Resource exists at startup, don't trigger reconciliation yet
        log.Debug("resource loaded", "count", stats.Created)
    } else {
        // Real-time change, trigger reconciliation
        log.Info("resource changed", "created", stats.Created)
        triggerReconciliation()
    }
}
```

## Sub-Package Guidelines

### types/ - Core Interfaces

Defines interfaces used across k8s package:

```go
// Store interface for indexed resource storage
type Store interface {
    Get(keys ...string) ([]interface{}, error)
    List() ([]interface{}, error)
    Add(resource interface{}, keys []string) error
    Update(resource interface{}, keys []string) error
    Delete(keys ...string) error
    Clear() error
}

// Watcher configuration
type WatcherConfig struct {
    GVR           schema.GroupVersionResource
    Namespace     string
    LabelSelector string
    IndexBy       []string
    StoreType     StoreType
    Callbacks     ...
}
```

**When to modify:**

- Adding new Store method
- Changing watcher configuration options
- Adding new callback types

### client/ - Kubernetes Client Wrapper

Wraps client-go for simplified usage:

```go
// Auto-detects in-cluster vs out-of-cluster
client, err := client.New()

// Provides both typed and dynamic clients
typedClient := client.Clientset()
dynamicClient := client.Dynamic()
```

**Common tasks:**

Adding custom client configuration:

```go
// client/client.go
type Config struct {
    QPS       float32
    Burst     int
    UserAgent string
}

func NewWithConfig(cfg Config) (*Client, error) {
    // Custom configuration
}
```

### indexer/ - JSONPath and Field Filtering

Extracts index keys and filters unnecessary fields:

```go
// Extract index keys from resource
keys, err := indexer.ExtractKeys(resource, []string{
    "metadata.namespace",
    "metadata.labels['app']",
})
// Result: ["default", "myapp"]

// Remove unnecessary fields
filtered := indexer.FilterFields(resource, []string{
    "metadata.managedFields",
    "metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']",
})
```

**JSONPath Validation:**

```go
// Validates at startup (fail-fast)
err := indexer.ValidateJSONPath("metadata.namespace")  // OK
err := indexer.ValidateJSONPath("invalid..path")       // Error
```

**When to modify:**

- Supporting new JSONPath expressions
- Optimizing field filtering
- Adding field validation

**Common pitfall**: JSONPath expressions are case-sensitive and must match exact field names.

### store/ - Storage Implementations

**MemoryStore** - Default choice:

```go
store := store.NewMemoryStore()

// Add with index keys
keys := []string{"default", "myapp"}
store.Add(resource, keys)

// O(1) lookup using any combination of keys
resources, _ := store.Get("default")          // All in namespace
resources, _ := store.Get("default", "myapp") // Specific resource
```

**CachedStore** - For large resources:

```go
store := store.NewCachedStore(
    client,
    schema.GroupVersionResource{...},
    10*time.Minute,  // TTL
    100,             // Max cache size
)

// First access: fetches from API
resource, _ := store.Get("default", "my-secret")

// Subsequent accesses within TTL: uses cache
resource, _ := store.Get("default", "my-secret")  // Cached
```

**When to use CachedStore:**

- Resources with large data fields (Secrets with certificates)
- Resources accessed infrequently
- Memory pressure from many resources

**When to use MemoryStore:**

- Most resources (Ingress, Service, EndpointSlice)
- Resources accessed frequently
- Need for fast iteration (List() operations)

### watcher/ - Resource Watching

**Watcher** - Bulk resource watching:

```go
config := k8s.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "networking.k8s.io",
        Version:  "v1",
        Resource: "ingresses",
    },
    Namespace:     "",  // All namespaces
    LabelSelector: "app=myapp",
    IndexBy: []string{
        "metadata.namespace",
        "metadata.name",
    },
    StoreType: k8s.StoreTypeMemory,
    DebounceInterval: 500 * time.Millisecond,

    OnChange: func(store Store, stats ChangeStats) {
        if !stats.IsInitialSync {
            // Real-time change
            handleChange(stats)
        }
    },

    OnSyncComplete: func(store Store, count int) {
        log.Info("initial sync complete", "count", count)
    },

    CallOnChangeDuringSync: false,  // Wait for full sync before calling OnChange
}

watcher := watcher.NewWatcher(client, config)
go watcher.Run(ctx)

// Wait for initial sync
watcher.WaitForSync(ctx)
```

**SingleWatcher** - Single resource watching:

```go
config := k8s.SingleWatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "configmaps",
    },
    Namespace: "default",
    Name:      "haproxy-config",

    OnResourceChange: func(resource interface{}) {
        // Immediate callback (no debouncing)
        handleConfigChange(resource)
    },
}

watcher := watcher.NewSingleWatcher(client, config)
go watcher.Run(ctx)

// Wait for initial load
watcher.WaitForSync(ctx)
```

**Debouncing:**

```go
// Rapid changes batched
// t=0ms:   Create Ingress A → debounce starts
// t=100ms: Update Ingress A → debounce reset
// t=200ms: Create Ingress B → debounce reset
// t=700ms: No more changes → OnChange called once with stats
```

## Testing Strategies

### Unit Tests with Fake Client

```go
import "k8s.io/client-go/kubernetes/fake"

func TestWatcher(t *testing.T) {
    // Create fake Kubernetes client
    fakeClient := fake.NewSimpleClientset()

    // Add test resources
    ingress := &networkingv1.Ingress{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-ingress",
            Namespace: "default",
        },
    }
    fakeClient.NetworkingV1().Ingresses("default").Create(ctx, ingress, metav1.CreateOptions{})

    // Create watcher with fake client
    config := k8s.WatcherConfig{...}
    watcher := k8s.NewWatcher(fakeClient, config)

    // Test watcher behavior
    go watcher.Run(ctx)
    watcher.WaitForSync(ctx)

    // Verify resource was indexed
    resources, _ := store.Get("default", "test-ingress")
    assert.Len(t, resources, 1)
}
```

### Testing Initial Sync vs Real-Time Changes

```go
func TestWatcher_InitialSync(t *testing.T) {
    var syncCompleted bool
    var changesDuringSync []ChangeStats
    var changesAfterSync []ChangeStats

    config := k8s.WatcherConfig{
        OnChange: func(store Store, stats ChangeStats) {
            if !syncCompleted {
                changesDuringSync = append(changesDuringSync, stats)
            } else {
                changesAfterSync = append(changesAfterSync, stats)
            }
        },
        OnSyncComplete: func(store Store, count int) {
            syncCompleted = true
        },
        CallOnChangeDuringSync: true,  // Test incremental processing
    }

    watcher := k8s.NewWatcher(fakeClient, config)
    go watcher.Run(ctx)

    // Add resources before sync completes
    addTestResource()

    watcher.WaitForSync(ctx)

    // Add resources after sync
    addTestResource()

    // Verify IsInitialSync flag
    assert.True(t, changesDuringSync[0].IsInitialSync)
    assert.False(t, changesAfterSync[0].IsInitialSync)
}
```

## Common Pitfalls

### Not Waiting for Initial Sync

**Problem**: Processing resources before all watchers synced.

```go
// Bad
watcher := k8s.NewWatcher(client, config)
go watcher.Run(ctx)

// Immediately access store - might be empty!
resources, _ := store.List()
reconcile(resources)  // Missing resources!
```

**Solution**: Always wait for sync.

```go
// Good
watcher := k8s.NewWatcher(client, config)
go watcher.Run(ctx)

// Wait for initial sync
if err := watcher.WaitForSync(ctx); err != nil {
    return err
}

// Now store has all pre-existing resources
resources, _ := store.List()
reconcile(resources)
```

### Incorrect JSONPath Expressions

**Problem**: Runtime errors from invalid JSONPath.

```go
// Bad - invalid expression not caught
config.IndexBy = []string{
    "metadata..namespace",  // Double dot - invalid
}
```

**Solution**: Validate at startup.

```go
// Good - validate before creating watcher
for _, expr := range config.IndexBy {
    if err := indexer.ValidateJSONPath(expr); err != nil {
        return fmt.Errorf("invalid JSONPath %s: %w", expr, err)
    }
}

watcher := k8s.NewWatcher(client, config)
```

### Blocking in Callbacks

**Problem**: Long-running work in callbacks blocks watcher.

```go
// Bad - blocks watcher thread
OnChange: func(store Store, stats ChangeStats) {
    resources, _ := store.List()
    // Complex processing (5 seconds)
    processResources(resources)
}
```

**Solution**: Spawn goroutine or use channels.

```go
// Good - non-blocking
OnChange: func(store Store, stats ChangeStats) {
    // Trigger via channel
    select {
    case reconcileChan <- struct{}{}:
    default:
        // Already pending, skip
    }
}

// Separate goroutine handles reconciliation
go func() {
    for range reconcileChan {
        processResources()
    }
}()
```

### Using Watcher for Single Resources

**Problem**: Overhead from indexing and debouncing for single resource.

```go
// Bad - unnecessary complexity
config := k8s.WatcherConfig{
    GVR:       configMapGVR,
    Namespace: "default",
    IndexBy:   []string{"metadata.name"},  // Only one resource
    OnChange:  func(store Store, stats ChangeStats) { ... },
}
watcher := k8s.NewWatcher(client, config)
```

**Solution**: Use SingleWatcher.

```go
// Good - optimized for single resource
config := k8s.SingleWatcherConfig{
    GVR:       configMapGVR,
    Namespace: "default",
    Name:      "haproxy-config",
    OnResourceChange: func(resource interface{}) { ... },
}
watcher := k8s.NewSingleWatcher(client, config)
```

### Field Selector Limitations

**Problem**: Not all fields support field selectors.

```go
// Bad - field selector not supported
config.FieldSelector = "metadata.labels['app']=myapp"  // Won't work!
```

**Solution**: Use label selectors instead.

```go
// Good
config.LabelSelector = "app=myapp"
```

## Adding New Resource Types

### Checklist

1. Define GVR (GroupVersionResource)
2. Choose index expressions (what lookups do you need?)
3. Determine store type (Memory vs Cached)
4. Set debounce interval (default 500ms is usually fine)
5. Implement callbacks
6. Add validation for index expressions
7. Write tests with fake client

### Example: Adding ConfigMap Watching

```go
// Step 1: Define GVR
configMapGVR := schema.GroupVersionResource{
    Group:    "",
    Version:  "v1",
    Resource: "configmaps",
}

// Step 2: Choose index expressions
// Need to look up by namespace and name
indexBy := []string{
    "metadata.namespace",
    "metadata.name",
}

// Step 3: Choose store type
// ConfigMaps can be large, but we need fast access → MemoryStore

// Step 4: Create config
config := k8s.WatcherConfig{
    GVR:              configMapGVR,
    Namespace:        "",  // All namespaces
    LabelSelector:    "app=myapp",
    IndexBy:          indexBy,
    StoreType:        k8s.StoreTypeMemory,
    DebounceInterval: 500 * time.Millisecond,
    OnChange: func(store Store, stats ChangeStats) {
        if !stats.IsInitialSync {
            handleConfigMapChange(stats)
        }
    },
    OnSyncComplete: func(store Store, count int) {
        log.Info("configmaps synced", "count", count)
    },
}

// Step 5: Create and start watcher
watcher := k8s.NewWatcher(client, config)
go watcher.Run(ctx)

// Step 6: Wait for sync
if err := watcher.WaitForSync(ctx); err != nil {
    return err
}
```

## Performance Considerations

### Memory Usage

**MemoryStore:**

- Per resource: ~1KB (Ingress) to ~10KB (Service with many endpoints)
- 1000 ingresses ≈ 1MB memory
- Suitable for most use cases

**CachedStore:**

- Per cached entry: Same as resource size
- TTL eviction reduces memory pressure
- Cache size limit prevents unbounded growth

### CPU Usage

**Indexing:**

- O(N) where N = number of index expressions
- Typically <1ms per resource
- Runs during Add/Update operations

**Lookups:**

- O(1) with proper indexing
- Map lookup with composite key
- Typically <100ns

**Debouncing:**

- Single timer per watcher
- Minimal CPU overhead
- Reduces reconciliation frequency

### Network Usage

**Watcher:**

- Initial list: Downloads all resources
- Watch: Receives only changes
- Efficient for long-running processes

**CachedStore:**

- Fetches on cache miss
- Adds API calls vs pure MemoryStore
- Trade-off: Memory vs network

## Troubleshooting

### Watcher Not Receiving Events

**Diagnosis:**

1. Verify RBAC permissions
2. Check GVR is correct (group, version, resource)
3. Verify namespace and label selectors
4. Check if resources exist matching selectors

```bash
# Test RBAC
kubectl auth can-i list ingresses --all-namespaces

# Verify GVR
kubectl api-resources | grep ingress

# Check resources exist
kubectl get ingresses -A -l app=myapp
```

### Store Returns Empty Results

**Diagnosis:**

1. Check if WaitForSync() was called
2. Verify index expressions match query keys
3. Check if resources match watch selectors
4. Inspect store contents

```go
// Debug store contents
resources, _ := store.List()
log.Info("store contents", "count", len(resources))

// Verify indexing
for _, res := range resources {
    keys, _ := indexer.ExtractKeys(res, config.IndexBy)
    log.Info("resource indexed", "keys", keys)
}
```

### High Memory Usage

**Diagnosis:**

1. Count resources in each watcher
2. Check for memory leaks (growing without bound)
3. Consider CachedStore for large resources
4. Review field filtering

```go
// Monitor store size
ticker := time.NewTicker(1 * time.Minute)
go func() {
    for range ticker.C {
        count, _ := store.List()
        log.Info("store size", "count", len(count))
    }
}()
```

## Resources

- API documentation: `pkg/k8s/README.md`
- Architecture: `/docs/controller/docs/development/design.md`
- Leader election: `pkg/k8s/leaderelection/CLAUDE.md`
- client-go documentation: <https://github.com/kubernetes/client-go>
- JSONPath syntax: <https://kubernetes.io/docs/reference/kubectl/jsonpath/>
