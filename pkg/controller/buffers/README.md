# pkg/controller/buffers

Memory-adaptive buffer-size calculation for EventBus subscriptions.

## Overview

`pkg/events.EventBus.Subscribe(name, bufferSize)` requires a fixed buffer size, but a sensible value depends on how much memory the container has — a 256 MiB pod and a 4 GiB pod shouldn't share the same number. This package picks a buffer size from `GOMEMLIMIT` (set by [`automemlimit`](https://github.com/KimMachineGun/automemlimit) from the cgroup memory limit at startup), so larger containers automatically get bigger buffers and drop fewer events under load.

Two presets cover every controller subscription:

| Function | Multiplier | Use for |
|----------|------------|---------|
| `buffers.Critical()` | 1× | Business-critical paths where drops would mean missed reconciliation work (reconciler, deployer, validator) |
| `buffers.Observability()` | 2× | Lossy paths where occasional drops are acceptable (commentator, metrics, debug ring buffer) |

Both clamp to the range `[100, 10000]` so a missing memory limit never produces a useless 0-slot channel and a 64 GiB container doesn't allocate a million slots.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
    busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

eventChan := bus.Subscribe("my-component", buffers.Critical())          // ~ memLimit / 1 MiB
debugChan := bus.Subscribe("debug-events", buffers.Observability())     // ~ memLimit / 512 KiB
```

`pkg/events` also exposes fixed-size tier constants (`LowVolumeSubscriberBuffer = 10`, `StandardSubscriberBuffer = 50`, `HighVolumeSubscriberBuffer = 100`, `PublishingSubscriberBuffer = 200`, `DebugSubscriberBuffer = 1000`). Those are easier to reason about for components where memory-adaptive scaling adds no value (tests, low-traffic loaders); the helpers in this package are for components whose buffer needs scale with container memory.

## Sizing Math

For each subscription, `calculateSize(multiplier)` returns:

```text
size = clamp(int(GOMEMLIMIT / 1 MiB * multiplier), [100, 10000])
```

If `GOMEMLIMIT` isn't set (or is effectively unlimited — anything above 1 PiB), it returns `clamp(int(100 * multiplier), [100, 10000])`. So an unconstrained binary still gets the floor of 100 slots, not zero.

## See Also

- [`pkg/events`](../../events/) — the bus that consumes these sizes
- [`automemlimit`](https://github.com/KimMachineGun/automemlimit) — sets `GOMEMLIMIT` from the cgroup memory limit at process startup (imported as a side-effect in `cmd/controller/main.go`)

## License

Apache-2.0 — see root `LICENSE`.
