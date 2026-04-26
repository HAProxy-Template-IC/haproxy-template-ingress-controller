# pkg/controller/resourcestore

Registry of `pkg/k8s/types.Store` instances keyed by resource-type name.

## Overview

`*Manager` is the single registry that maps resource type names (`"ingresses"`, `"services"`, …) to their backing stores. The controller constructs one in `pkg/controller/controller.go`, the resource watcher fills it via `RegisterStore` once each watcher's initial sync completes, and downstream code reads from it through `GetStore` / `GetAllStores` or via the `stores.StoreProvider` adapter built in `pkg/controller/helpers.go`.

The package also defines `OverlayStore`, `CreateOverlay`, and `CreateOverlayMap`, but **these are not used by production code** — the dryrun/proposal validators build overlays via `pkg/stores.NewStoreOverlayForCreate` / `…Update` / `…Delete` instead. They're kept here for tests and for callers that want a one-shot overlay tied to a single store, but if you're routing through the validation pipeline you don't need them.

## Quick Start

### Registering and reading stores

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"

m := resourcestore.NewManager()
m.RegisterStore("ingresses", ingressStore)
m.RegisterStore("services", serviceStore)

// Look up a single store
ingresses, ok := m.GetStore("ingresses")
if !ok { /* not registered */ }

// Snapshot every store (shallow copy, safe to iterate without holding the manager lock)
all := m.GetAllStores() // map[string]Store
```

### Bridging to the templating side

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"
    "gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// pkg/controller/helpers.go wraps Manager in a stores.StoreProvider so the
// renderer / pipeline / overlay machinery don't need to know about the
// concrete Manager type.
provider := newStoreProviderFromManager(m) // returns stores.StoreProvider
```

## Concurrency

`*Manager` uses an `RWMutex` so reads (`GetStore`, `GetAllStores`, `ResourceCount`, `StoreNames`) don't contend with each other; only `RegisterStore` takes the write lock. There is no `UnregisterStore` — registrations live for the iteration's lifetime and are dropped when the controller rebuilds its `Manager` on reinit. Reads return either pre-existing references (no copy) or, in the case of `GetAllStores`, a fresh shallow-copy map — safe to iterate after the lock is dropped.

## See Also

- [`pkg/k8s/store`](../../k8s/store/) — the concrete `MemoryStore` / `CachedStore` implementations registered here
- [`pkg/k8s/types`](../../k8s/types/) — the `Store` interface this manager keys on
- [`pkg/stores`](../../stores/) — the **production** overlay machinery (`NewStoreOverlayForCreate` / `…Update` / `…Delete`) used by the dryrun + proposal validators
- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) / [`proposalvalidator`](../proposalvalidator/) — go through `pkg/stores`, not the `OverlayStore` defined here
- [`pkg/controller/helpers`](../helpers/) — `newStoreProviderFromManager` adapter

## License

Apache-2.0 — see root `LICENSE`.
