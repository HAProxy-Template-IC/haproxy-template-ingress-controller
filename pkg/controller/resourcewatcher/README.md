# pkg/controller/resourcewatcher

Resource watcher fan-out for the controller.

## Overview

`ResourceWatcherComponent` materialises the configuration's `spec.watchedResources` map into a set of running Kubernetes informers (one `pkg/k8s/watcher.Watcher` per resource type). It also auto-injects an `haproxy-pods` watcher built from the CRD's `spec.podSelector`, so the rest of the controller never has to care whether the user listed it explicitly.

For each watcher the component:

- Resolves `apiVersion`/`resources` to a GVR
- Merges the global `watchedResourcesIgnoreFields` list with any per-resource overrides
- Indexes resources using the configured `indexBy` JSONPath expressions
- Applies the per-resource `debounceInterval` override (or the 5s `pkg/k8s/types.DefaultDebounceInterval` when empty) to the watcher's leading-edge refractory window
- Forwards add/update/delete events as `ResourceIndexUpdatedEvent` and emits `ResourceSyncCompleteEvent` once the informer's initial list completes

The exposed stores are read by `pkg/controller/renderer` and `pkg/controller/dryrunvalidator` to build template contexts.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"

watcher, err := resourcewatcher.New(cfg, k8sClient, bus, logger)
if err != nil { /* ... */ }
go watcher.Start(ctx)

// Access stores after IndexSynchronizedEvent has fired
stores := watcher.GetAllStores() // map[string]types.Store
```

The constructor returns `*ResourceWatcherComponent` (not `Watcher`); the function name is `New`, not `NewResourceWatcherComponent`. The CRD config and a fully constructed `*pkg/k8s/client.Client` must be passed in — the component does not parse the CRD itself.

## Events

- Subscribes: none directly (the underlying watchers are driven by Kubernetes)
- Publishes: `ResourceIndexUpdatedEvent` (one per change), `ResourceSyncCompleteEvent` (one per resource type, once)

## License

Apache-2.0 — see root `LICENSE`.
