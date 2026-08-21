# pkg/k8s/watcher

Two complementary Kubernetes watchers built on top of `k8s.io/client-go` informers.

## Overview

| Watcher | Use case |
|---------|----------|
| `*Watcher` (created via `watcher.New`) | Bulk: subscribe to every object of a GVR (optionally filtered by label/namespace selectors), index them with `pkg/k8s/indexer`, hand them to a `pkg/k8s/store` backend, and call back per debounced batch of changes |
| `*SingleWatcher` (created via `watcher.NewSingle`) | Targeted: watch one specific named resource (the controller's `HAProxyTemplateConfig` CRD or the credentials Secret) and fire an immediate callback per change — no debouncing, no store |

Both types take their config via the structs in `pkg/k8s/types` so they can be wired uniformly from the controller.

## Quick Start

### Bulk watcher

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
    "gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
    "gitlab.com/haproxy-haptic/haptic/pkg/k8s/watcher"
)

c, _ := client.New(client.Config{})
cfg := types.WatcherConfig{
    GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
    IndexBy: []string{"metadata.namespace", "metadata.name"},
    OnChange: func(store types.Store, stats types.ChangeStats) {
        if stats.IsInitialSync { return } // skip the bulk-load event
        // react to real changes
    },
    OnSyncComplete: func(store types.Store, count int) {
        log.Info("ingresses initial sync complete", "count", count)
    },
    // DebounceInterval defaults to types.DefaultDebounceInterval (100ms)
}

w, err := watcher.New(cfg, c, slog.Default())
if err != nil { /* ... */ }
go w.Start(ctx)
n, err := w.WaitForSync(ctx) // returns the initial-list count
```

### Single watcher

```go
cfg := &types.SingleWatcherConfig{
    GVR:       schema.GroupVersionResource{Version: "v1", Resource: "secrets"},
    Namespace: "haptic",
    Name:      "haproxy-credentials",
    OnChange: func(obj any) error { // typed as types.OnResourceChangeCallback
        // obj is the live *unstructured.Unstructured (or nil on delete)
        return nil
    },
}

sw, err := watcher.NewSingle(cfg, c)
if err != nil { /* ... */ }
go sw.Start(ctx)
err = sw.WaitForSync(ctx)
```

## Behavioural Notes

- **Debouncing** is *leading-edge with a refractory period* (the first change in a quiet period fires immediately; further changes inside the window are batched). See `pkg/controller/reconciler/CLAUDE.md` for why this matters during rolling deploys.
- **Initial sync** behaviour is controlled by `CallOnChangeDuringSync` (default `false`): with the default, `OnChange` is *suppressed* during the bulk load and the consumer learns the load is finished from the parallel `OnSyncComplete` callback. With `CallOnChangeDuringSync: true`, `OnChange` fires for every change during the initial list — each call's `stats.IsInitialSync` is `true` so consumers can `if stats.IsInitialSync { return }` to skip them when they only care about post-sync deltas.
- **`SingleWatcher` is not debounced** — its `OnChange` callback (typed as `OnResourceChangeCallback`, distinct from the bulk watcher's `OnChangeCallback`) runs on every event, intentionally, because credential and CRD updates need to take effect immediately.

## See Also

- [`pkg/k8s/client`](../client/) — supplies the typed + dynamic clients required by both constructors
- [`pkg/k8s/store`](../store/) — bulk-watcher backing storage
- [`pkg/k8s/indexer`](../indexer/) — JSONPath key extraction + field filtering
- [`pkg/k8s/types`](../types/) — `WatcherConfig` / `SingleWatcherConfig` / `Store` interface

## License

Apache-2.0 — see root `LICENSE`.
