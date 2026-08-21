# pkg/k8s/types

Shared types between `pkg/k8s` packages and their consumers — the `Store` interface, watcher configuration, change-statistics struct, and the callback signatures.

## Overview

This package exists to break import cycles. `pkg/k8s/watcher` needs to know about the `Store` interface (to push results into a backing store), `pkg/k8s/store` provides implementations of that interface, and `pkg/controller` consumes both. Putting the interface and a handful of small structs here lets both halves of `pkg/k8s` import the same definition without depending on each other.

## Key Types

| Type | Purpose |
|------|---------|
| `Store` interface | `Get` / `List` / `Add` / `Update` / `Delete` / `Clear` — implemented by `MemoryStore` and `CachedStore` in `pkg/k8s/store` |
| `StoreType` enum | `StoreTypeMemory` (default) or `StoreTypeCached`, used by config to pick a backend |
| `WatcherConfig` | All options for `pkg/k8s/watcher.New`: GVR, namespace, label selectors, indexer config, debounce interval |
| `SingleWatcherConfig` | Variant for `pkg/k8s/watcher.NewSingle` — watches one specific named resource (used for the CRD itself + the credentials Secret) |
| `ChangeStats` | Created/Modified/Deleted counts plus `IsInitialSync`, surfaced to `OnChange` callbacks so consumers can distinguish bulk-load events from real changes |
| `OnChangeCallback` | Watcher → consumer signature: `func(store Store, stats ChangeStats)` |
| `OnSyncCompleteCallback` | Fired once per watcher when initial list completes |
| `OnResourceChangeCallback` | The `SingleWatcher` immediate-callback signature: `func(obj any) error` |
| `ConfigError` | Typed error returned by the watchers when their config is malformed (`Field` + `Message`). Callers can `errors.As` / `errors.AsType[*ConfigError]` to recover the offending field name |

`DefaultDebounceInterval` (100 ms) lives in this package as the canonical default for `WatcherConfig.DebounceInterval` — referenced from both `pkg/k8s` callers and the user-facing performance docs. It is the only *watcher* debounce default in the codebase: the reconciler fires immediately with no refractory window, so there is no longer a `pkg/core/config` counterpart to keep in sync. `configchange.DefaultReinitDebounceInterval` is a separate, deliberately lenient constant for informer teardown.

## See Also

- [`pkg/k8s/watcher`](../watcher/) — owns `WatcherConfig` / `SingleWatcherConfig` consumers
- [`pkg/k8s/store`](../store/) — implements the `Store` interface declared here

## License

Apache-2.0 — see root `LICENSE`.
