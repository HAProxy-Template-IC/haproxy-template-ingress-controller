# pkg/controller/indextracker

Initial-sync tracker for the configured resource watchers.

## Overview

`IndexSynchronizationTracker` waits until every watched resource type has reported its initial list-and-watch sync, then fires a single `IndexSynchronizedEvent`. The reconciler uses that event as the cue to start reconciling — it prevents partial renders that would happen if some informer caches weren't populated yet.

The tracker is constructed with the list of resource-type names that must sync (typically the keys of `spec.watchedResources` from the CRD plus any controller-internal types like `haproxy-pods`). Each `ResourceSyncCompleteEvent` ticks one entry; once they're all checked off, the event fires exactly once.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/indextracker"

tracker := indextracker.New(bus, logger, []string{
    "ingresses", "services", "endpoints", // from Config.WatchedResources
    "haproxy-pods",                       // controller-internal
})
go tracker.Start(ctx)
```

The constructor returns `*IndexSynchronizationTracker` (not `IndexTracker`); the function name is `New`, not `NewIndexTracker`.

## Events

- Subscribes: `ResourceSyncCompleteEvent`
- Publishes: `IndexSynchronizedEvent` (once, when the last expected resource finishes its initial sync)

## License

Apache-2.0 — see root `LICENSE`.
