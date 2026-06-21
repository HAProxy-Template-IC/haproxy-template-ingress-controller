# pkg/controller/buffers

Fixed buffer sizes for EventBus subscriptions.

## Overview

`pkg/events.EventBus.Subscribe(name, bufferSize)` requires a fixed buffer size.
This package provides two presets covering every controller subscription:

| Constant | Size | Use for |
|----------|------|---------|
| `buffers.Critical` | 100 | Business-critical paths where drops would mean missed reconciliation work (reconciler, deployer, validator) |
| `buffers.Observability` | 200 | Lossy paths where occasional drops are acceptable (commentator, metrics, debug ring buffer) |

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
    busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

eventChan := bus.Subscribe("my-component", buffers.Critical)
debugChan := bus.Subscribe("debug-events", buffers.Observability)
```

`pkg/events` also exposes fixed-size tier constants (`LowVolumeSubscriberBuffer = 10`, `StandardSubscriberBuffer = 50`, `HighVolumeSubscriberBuffer = 100`, `PublishingSubscriberBuffer = 200`, `DebugSubscriberBuffer = 1000`) for components that wire their own sizes directly.

## See Also

- [`pkg/events`](../../events/) — the bus that consumes these sizes

## License

Apache-2.0 — see root `LICENSE`.
