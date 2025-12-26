# pkg/k8s

Kubernetes resource watching and indexing package for HAPTIC.

## Overview

The `pkg/k8s` package provides a high-level interface for watching Kubernetes resources with:

- **Indexed Storage**: O(1) lookups using configurable JSONPath expressions
- **Field Filtering**: Remove unnecessary fields to reduce memory usage
- **Initial Sync Tracking**: Distinguish bulk loading from real-time changes
- **Debounced Callbacks**: Batch rapid changes to prevent overwhelming consumers
- **Two Store Types**: Memory (fast) or Cached (memory-efficient with API-backed fetches)
- **Thread-Safe**: Safe for concurrent access from multiple goroutines

## Features

- Watch any Kubernetes resource type using GroupVersionResource (GVR)
- Filter by namespace and label selectors
- Index resources using JSONPath expressions for fast lookups
- Remove unnecessary fields (e.g., `metadata.managedFields`) before storage
- Track initial synchronization with callbacks and status methods
- Debounce rapid resource changes with configurable intervals
- Choose between memory and cached storage strategies
- Thread-safe operations with proper locking

## Installation

```go
import (
    "haptic/pkg/k8s/client"
    "haptic/pkg/k8s/types"
    "haptic/pkg/k8s/watcher"
)
```

## Quick Start

### Watching Collections (Multiple Resources)

```go
package main

import (
    "context"
    "fmt"
    "time"

    "haptic/pkg/k8s/client"
    "haptic/pkg/k8s/types"
    "haptic/pkg/k8s/watcher"

    "k8s.io/apimachinery/pkg/runtime/schema"
)

func main() {
    // Create Kubernetes client
    k8sClient, err := client.New(client.Config{})
    if err != nil {
        panic(err)
    }

    // Configure watcher for multiple ingresses
    cfg := types.WatcherConfig{
        GVR: schema.GroupVersionResource{
            Group:    "networking.k8s.io",
            Version:  "v1",
            Resource: "ingresses",
        },
        IndexBy:          []string{"metadata.namespace", "metadata.name"},
        IgnoreFields:     []string{"metadata.managedFields"},
        DebounceInterval: 500 * time.Millisecond,
        OnChange: func(store types.Store, stats types.ChangeStats) {
            fmt.Printf("Resources changed: +%d -%d ~%d\n",
                stats.Created, stats.Deleted, stats.Modified)
        },
    }

    // Create and start watcher
    w, err := watcher.New(cfg, k8sClient)
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    if err := w.Start(ctx); err != nil {
        panic(err)
    }
}
```

### Watching Single Resource

```go
package main

import (
    "context"
    "fmt"

    "haptic/pkg/k8s/client"
    "haptic/pkg/k8s/types"
    "haptic/pkg/k8s/watcher"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/runtime/schema"
)

func main() {
    // Create Kubernetes client
    k8sClient, err := client.New(client.Config{})
    if err != nil {
        panic(err)
    }

    // Configure watcher for specific ConfigMap
    cfg := types.SingleWatcherConfig{
        GVR: schema.GroupVersionResource{
            Group:    "",
            Version:  "v1",
            Resource: "configmaps",
        },
        Namespace: "default",
        Name:      "haproxy-config",
        OnChange: func(obj interface{}) error {
            cm := obj.(*corev1.ConfigMap)
            fmt.Printf("ConfigMap changed: %s\n", cm.Name)
            // Parse and validate configuration
            return nil
        },
    }

    // Create and start watcher
    w, err := watcher.NewSingle(cfg, k8sClient)
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    if err := w.Start(ctx); err != nil {
        panic(err)
    }
}
```

## Choosing Between Watchers

The package provides two types of watchers for different use cases:

### Watcher (Collections)

Use `watcher.New()` when watching **multiple resources** (collections):

**Best for:**

- Many ingresses across namespaces
- All services in a cluster
- Multiple endpoint slices
- Any collection where you need O(1) lookup by index keys

**Features:**

- Indexed storage for fast lookups
- Debouncing to batch rapid changes
- Field filtering to reduce memory
- Aggregated change statistics

**Example use cases:**

- Watching all Ingress resources to generate HAProxy configuration
- Watching Services and EndpointSlices for load balancing
- Monitoring Pods with specific labels

### SingleWatcher (Single Resource)

Use `watcher.NewSingle()` when watching **one specific resource**:

**Best for:**

- Configuration in a specific ConfigMap
- Credentials in a specific Secret
- A particular Deployment or Service you depend on

**Features:**

- No indexing overhead (only one resource)
- Immediate callbacks (no debouncing)
- Simpler API (resource object in callback)
- Lighter weight implementation

**Example use cases:**

- Watching ConfigMap containing HAProxy templates
- Watching Secret containing dataplane credentials
- Monitoring controller's own Deployment

## Core Concepts

### Store Types

The package provides two store implementations:

#### Memory Store (Default)

Stores complete resources in memory for fastest access.

```go
cfg := types.WatcherConfig{
    StoreType: types.StoreTypeMemory, // Default
    // ... other config
}
```

**Use when:**

- Resources are small to medium sized
- Fast access is critical
- Memory is not constrained

#### Cached Store

Stores only keys in memory, fetches full resources from Kubernetes API on demand with TTL-based caching.

```go
cfg := types.WatcherConfig{
    StoreType: types.StoreTypeCached,
    CacheTTL:  2 * time.Minute, // Cache duration
    // ... other config
}
```

**Use when:**

- Resources are large (e.g., Secrets with certificate data)
- Memory usage must be minimized
- Some API latency on cache misses is acceptable

### Indexing with JSONPath

Resources are indexed using JSONPath expressions for O(1) lookups:

```go
// Standard indexing by namespace and name
IndexBy: []string{"metadata.namespace", "metadata.name"}

// Custom indexing by label for O(1) service-to-endpoints lookup
IndexBy: []string{"metadata.labels['kubernetes.io/service-name']"}
```

**JSONPath expressions:**

- `metadata.namespace` - Resource namespace
- `metadata.name` - Resource name
- `metadata.labels['key']` - Label value
- Any valid JSONPath expression supported by k8s.io/client-go/util/jsonpath

**Lookup performance:**

- Multiple index keys create composite keys: `{key1}:{key2}:...`
- Store.Get() performs O(1) lookup using provided keys
- Store.List() returns all resources: O(n)

### Field Filtering

Remove unnecessary fields before storage to reduce memory usage:

```go
IgnoreFields: []string{
    "metadata.managedFields",  // Verbose, rarely needed
    "metadata.annotations",    // If annotations aren't needed
}
```

Fields are removed using JSONPath patterns before indexing and storage.

### Initial Synchronization Handling

The watcher distinguishes between initial bulk loading of pre-existing resources and real-time changes:

#### OnSyncComplete Callback

Called once after initial sync completes with the fully populated store:

```go
OnSyncComplete: func(store types.Store, initialCount int) {
    fmt.Printf("Initial sync complete: %d resources loaded\n", initialCount)
    // Now safe to act on complete data
    // Example: render HAProxy config
}
```

#### IsInitialSync Flag

ChangeStats includes `IsInitialSync` to distinguish bulk load from real-time changes:

```go
OnChange: func(store types.Store, stats types.ChangeStats) {
    if stats.IsInitialSync {
        fmt.Println("Processing pre-existing resources during sync")
    } else {
        fmt.Println("Processing real-time change")
    }
}
```

#### CallOnChangeDuringSync

Control whether OnChange is called during initial sync:

```go
// Default: suppress callbacks during sync, only call OnSyncComplete
CallOnChangeDuringSync: false

// Enable: receive callbacks during sync with IsInitialSync=true
CallOnChangeDuringSync: true
```

#### WaitForSync()

Block until initial sync completes (useful for staged startup):

```go
w, _ := watcher.New(cfg, k8sClient)

// Start watcher in background
go w.Start(ctx)

// Wait for sync before continuing
count, err := w.WaitForSync(ctx)
if err != nil {
    panic(err)
}
fmt.Printf("Watcher synced: %d resources loaded\n", count)

// Now safe to proceed with operations requiring complete data
```

#### IsSynced()

Non-blocking sync status check:

```go
if w.IsSynced() {
    fmt.Println("Watcher is synced")
} else {
    fmt.Println("Still syncing...")
}
```

### Debouncing

Rapid resource changes are batched into a single callback invocation:

```go
DebounceInterval: 500 * time.Millisecond // Default
```

The callback receives aggregated statistics about all changes within the debounce window.

## Usage Patterns

### Basic Resource Watching

Watch Ingress resources with standard indexing:

```go
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "networking.k8s.io",
        Version:  "v1",
        Resource: "ingresses",
    },
    IndexBy:          []string{"metadata.namespace", "metadata.name"},
    IgnoreFields:     []string{"metadata.managedFields"},
    StoreType:        types.StoreTypeMemory,
    DebounceInterval: 500 * time.Millisecond,
    OnChange: func(store types.Store, stats types.ChangeStats) {
        ingresses, _ := store.List()
        fmt.Printf("Total ingresses: %d\n", len(ingresses))
    },
}

w, _ := watcher.New(cfg, k8sClient)
w.Start(ctx)
```

### Namespaced Watch

Watch resources only in the controller's own namespace:

```go
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "pods",
    },
    NamespacedWatch: true, // Auto-detect namespace from service account
    LabelSelector: &metav1.LabelSelector{
        MatchLabels: map[string]string{
            "app":       "haproxy",
            "component": "loadbalancer",
        },
    },
    IndexBy: []string{"metadata.name"},
    OnChange: func(store types.Store, stats types.ChangeStats) {
        fmt.Printf("HAProxy pods changed: %+v\n", stats)
    },
}

w, _ := watcher.New(cfg, k8sClient)
w.Start(ctx)
```

### Cached Store for Large Resources

Use cached store for Secrets to reduce memory usage:

```go
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "secrets",
    },
    IndexBy:      []string{"metadata.namespace", "metadata.name"},
    StoreType:    types.StoreTypeCached,
    CacheTTL:     2 * time.Minute,
    IgnoreFields: []string{"metadata.managedFields"},
    OnChange: func(store types.Store, stats types.ChangeStats) {
        // Secrets fetched from API only when accessed
        secrets, _ := store.Get("default")
        fmt.Printf("Secrets in default namespace: %d\n", len(secrets))
    },
}

w, _ := watcher.New(cfg, k8sClient)
w.Start(ctx)
```

### Custom Indexing for O(1) Lookups

Index EndpointSlices by service name for fast service-to-endpoints lookup:

```go
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "discovery.k8s.io",
        Version:  "v1",
        Resource: "endpointslices",
    },
    // Index by service name label for O(1) lookup
    IndexBy: []string{"metadata.labels['kubernetes.io/service-name']"},
    OnChange: func(store types.Store, stats types.ChangeStats) {
        // Fast O(1) lookup: get all endpoint slices for a service
        endpointSlices, _ := store.Get("nginx-service")
        fmt.Printf("Endpoints for nginx-service: %d\n", len(endpointSlices))
    },
}

w, _ := watcher.New(cfg, k8sClient)
w.Start(ctx)
```

### Sync Handling - Wait for Complete Data

Recommended pattern: suppress callbacks during sync, act only when complete:

```go
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "networking.k8s.io",
        Version:  "v1",
        Resource: "ingresses",
    },
    IndexBy:      []string{"metadata.namespace", "metadata.name"},
    IgnoreFields: []string{"metadata.managedFields"},

    // Suppress OnChange during initial sync (default)
    CallOnChangeDuringSync: false,

    // Called once after initial sync completes
    OnSyncComplete: func(store types.Store, initialCount int) {
        fmt.Printf("Initial sync complete: %d ingresses loaded\n", initialCount)
        // Now safe to act on complete data
        // Example: renderHAProxyConfig(store)
    },

    // Called only for real-time changes after sync
    OnChange: func(store types.Store, stats types.ChangeStats) {
        // IsInitialSync is always false here
        fmt.Printf("Real-time change: +%d -%d ~%d\n",
            stats.Created, stats.Deleted, stats.Modified)
        // Re-render config for incremental changes
    },
}

w, _ := watcher.New(cfg, k8sClient)
w.Start(ctx)
```

### Sync Handling - Incremental Processing

Process resources incrementally during sync:

```go
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "services",
    },
    IndexBy: []string{"metadata.namespace", "metadata.name"},

    // Enable callbacks during initial sync
    CallOnChangeDuringSync: true,

    OnChange: func(store types.Store, stats types.ChangeStats) {
        if stats.IsInitialSync {
            // Processing pre-existing resources during bulk load
            fmt.Printf("Initial sync progress: %d services loaded so far\n", stats.Created)
        } else {
            // Processing real-time changes
            fmt.Printf("Service changed: +%d -%d ~%d\n",
                stats.Created, stats.Deleted, stats.Modified)
        }
        // Process resources (both during sync and after)
    },

    OnSyncComplete: func(store types.Store, initialCount int) {
        fmt.Printf("Initial sync complete: %d total services\n", initialCount)
        // Mark system as "ready"
    },
}

w, _ := watcher.New(cfg, k8sClient)
w.Start(ctx)
```

### Single Resource Watching - ConfigMap

Watch a specific ConfigMap containing configuration:

```go
cfg := types.SingleWatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "configmaps",
    },
    Namespace: "kube-system",
    Name:      "haproxy-templates",
    OnChange: func(obj interface{}) error {
        cm := obj.(*corev1.ConfigMap)

        // Parse configuration from ConfigMap data
        config, err := parseConfig(cm.Data["haproxy.yaml"])
        if err != nil {
            return fmt.Errorf("failed to parse config: %w", err)
        }

        // Validate configuration
        if err := validateConfig(config); err != nil {
            return fmt.Errorf("invalid config: %w", err)
        }

        // Apply configuration (render HAProxy config, reload, etc.)
        return applyConfig(config)
    },
}

w, _ := watcher.NewSingle(cfg, k8sClient)

// Start watcher in background
go w.Start(context.Background())

// Wait for initial sync before proceeding
if err := w.WaitForSync(context.Background()); err != nil {
    panic(err)
}
fmt.Println("ConfigMap watcher synced and ready")
```

### Single Resource Watching - Secret

Watch a specific Secret containing credentials:

```go
cfg := types.SingleWatcherConfig{
    GVR: schema.GroupVersionResource{
        Group:    "",
        Version:  "v1",
        Resource: "secrets",
    },
    Namespace: "kube-system",
    Name:      "haproxy-credentials",
    OnChange: func(obj interface{}) error {
        secret := obj.(*corev1.Secret)

        // Extract credentials from Secret data
        dataplane_username := string(secret.Data["dataplane_username"])
        dataplane_password := string(secret.Data["dataplane_password"])

        // Validate credentials are present
        if dataplane_username == "" || dataplane_password == "" {
            return fmt.Errorf("missing required credentials")
        }

        // Update HAProxy dataplane API client with new credentials
        return updateDataplaneCredentials(dataplane_username, dataplane_password)
    },
}

w, _ := watcher.NewSingle(cfg, k8sClient)
w.Start(context.Background())
```

## API Reference

### SingleWatcherConfig

Configuration for watching a single named resource:

```go
type SingleWatcherConfig struct {
    // GroupVersionResource identifies the Kubernetes resource type to watch
    GVR schema.GroupVersionResource

    // Namespace containing the resource (required)
    Namespace string

    // Name of the specific resource to watch (required)
    Name string

    // OnChange is called when the resource changes (add, update, delete)
    OnChange OnResourceChangeCallback

    // Context for cancellation (default: context.Background())
    Context context.Context
}
```

### SingleWatcher Methods

```go
// NewSingle creates a new single-resource watcher
func NewSingle(cfg SingleWatcherConfig, k8sClient *client.Client) (*SingleWatcher, error)

// Start begins watching the resource (blocks until context is cancelled)
func (w *SingleWatcher) Start(ctx context.Context) error

// Stop stops watching the resource
func (w *SingleWatcher) Stop() error

// WaitForSync blocks until initial synchronization is complete
func (w *SingleWatcher) WaitForSync(ctx context.Context) error

// IsSynced returns true if initial synchronization has completed
func (w *SingleWatcher) IsSynced() bool
```

### SingleWatcher Callbacks

```go
// OnResourceChangeCallback is invoked when a single watched resource changes
// Returns an error if processing fails
type OnResourceChangeCallback func(obj interface{}) error
```

## API Reference (Bulk Watcher)

### WatcherConfig

Configuration for resource watching:

```go
type WatcherConfig struct {
    // GroupVersionResource identifies the Kubernetes resource type to watch
    GVR schema.GroupVersionResource

    // Namespace restricts watching to a specific namespace (empty = all namespaces)
    Namespace string

    // NamespacedWatch restricts to controller's own namespace (auto-detected)
    NamespacedWatch bool

    // LabelSelector filters resources by labels
    LabelSelector *metav1.LabelSelector

    // IndexBy specifies JSONPath expressions for index keys
    IndexBy []string

    // IgnoreFields specifies JSONPath expressions for fields to remove
    IgnoreFields []string

    // StoreType determines storage implementation (Memory or Cached)
    StoreType StoreType

    // CacheTTL sets cache duration for StoreTypeCached (default: 2m10s)
    CacheTTL time.Duration

    // DebounceInterval sets minimum time between callbacks (default: 500ms)
    DebounceInterval time.Duration

    // OnChange is called when resources change (debounced)
    OnChange OnChangeCallback

    // OnSyncComplete is called once after initial sync completes
    OnSyncComplete OnSyncCompleteCallback

    // CallOnChangeDuringSync enables callbacks during initial sync (default: false)
    CallOnChangeDuringSync bool

    // Context for cancellation (default: context.Background())
    Context context.Context
}
```

### Store Interface

Thread-safe storage for indexed resources:

```go
type Store interface {
    // Get retrieves all resources matching the provided index keys
    Get(keys ...string) ([]interface{}, error)

    // List returns all resources in the store
    List() ([]interface{}, error)

    // Add inserts a new resource with the provided index keys
    Add(resource interface{}, keys []string) error

    // Update modifies an existing resource
    Update(resource interface{}, keys []string) error

    // Delete removes a resource using its index keys
    Delete(keys ...string) error

    // Clear removes all resources from the store
    Clear() error
}
```

### Watcher Methods

```go
// New creates a new resource watcher
func New(cfg WatcherConfig, k8sClient *client.Client) (*Watcher, error)

// Start begins watching resources (blocks until context is cancelled)
func (w *Watcher) Start(ctx context.Context) error

// Stop stops watching resources
func (w *Watcher) Stop() error

// Store returns the underlying store for direct access
func (w *Watcher) Store() types.Store

// WaitForSync blocks until initial synchronization is complete
func (w *Watcher) WaitForSync(ctx context.Context) (int, error)

// IsSynced returns true if initial synchronization has completed
func (w *Watcher) IsSynced() bool

// ForceSync forces an immediate callback with current statistics
func (w *Watcher) ForceSync()
```

### Callbacks

```go
// OnChangeCallback is invoked when resources change (debounced)
type OnChangeCallback func(store Store, stats ChangeStats)

// OnSyncCompleteCallback is invoked once after initial sync completes
type OnSyncCompleteCallback func(store Store, initialCount int)

// ChangeStats tracks aggregated resource changes
type ChangeStats struct {
    Created       int  // Number of resources added
    Modified      int  // Number of resources updated
    Deleted       int  // Number of resources removed
    IsInitialSync bool // True during initial synchronization
}
```

## Package Structure

The package is organized into focused subpackages:

- **types/**: Core interfaces and types
  - `Store` interface
  - `WatcherConfig` configuration
  - Callback types and statistics
- **client/**: Kubernetes client wrapper
  - Wraps kubernetes.Interface and dynamic.Interface
  - Auto-detects in-cluster vs out-of-cluster configuration
- **indexer/**: JSONPath evaluation and field filtering
  - Extracts index keys from resources
  - Removes unnecessary fields
  - Fail-fast validation of JSONPath expressions
- **store/**: Store implementations
  - `MemoryStore`: Fast in-memory storage
  - `CachedStore`: Memory-efficient API-backed storage
- **watcher/**: Resource watching orchestration
  - Uses SharedInformerFactory
  - Debouncing logic
  - Sync tracking and callbacks
- **leaderelection/**: Leader election using Kubernetes Leases
  - Pure component (no event dependencies)
  - See `pkg/k8s/leaderelection/README.md` for details

## Thread Safety

All store implementations are thread-safe for concurrent access:

- Multiple goroutines can safely call Get(), List(), Add(), Update(), Delete()
- Internal RWMutex ensures proper synchronization
- Callbacks are invoked serially (never concurrently)

## Testing

See `example_test.go` for comprehensive usage examples:

- Basic resource watching
- Namespaced watch
- Cached store usage
- Custom indexing patterns
- Sync handling patterns

Run examples:

```bash
go test -v -run Example
```

## Error Handling

The package uses fail-fast validation:

- JSONPath expressions are validated at watcher creation (not runtime)
- Invalid configuration returns error from `watcher.New()`
- Runtime errors (e.g., store access failures) are logged but don't crash the watcher

## Performance Considerations

**Memory Usage:**

- Memory store: Proportional to resource count and size
- Cached store: Only keys in memory, ~O(n) where n = number of resources
- Field filtering significantly reduces memory usage

**Lookup Performance:**

- Store.Get() with index keys: O(1)
- Store.List(): O(n) where n = number of resources
- Debouncing reduces callback frequency during rapid changes

**API Calls (Cached Store):**

- Initial sync: 1 LIST call per resource type
- Real-time updates: Watch events via SharedInformer (no additional calls)
- Cache misses: GET call to Kubernetes API (TTL-based caching reduces frequency)

## License

Part of haproxy-template-ingress-controller project.
