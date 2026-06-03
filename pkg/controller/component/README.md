# pkg/controller/component

Shared event-loop scaffold consumed by every controller component that subscribes on construction and dispatches one event at a time.

## Overview

The pattern most controller components share — subscribe to the EventBus during `New(...)` so events buffered during startup aren't lost, run a single goroutine that dispatches one event at a time, recover from panics inside the handler, and shut down cleanly when the context is cancelled — used to be duplicated in `pkg/controller/resourceloader.BaseLoader` and `pkg/controller/validator.BaseValidator`. This package consolidates it. The two `Base*` types still exist as thin wrappers for familiarity, but new components should embed `*Base` directly.

`*ReadySignal` (in `ready.go`) is a small one-shot helper for components that need to signal "I'm ready" exactly once — used by the deployer, coordinator, config publisher, and a few others.

## Quick Start

```go
import (
    "log/slog"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
    busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type MyComponent struct {
    *component.Base
}

func (m *MyComponent) HandleEvent(event busevents.Event) {
    // process exactly one event; panics are caught and logged by Base
}

func New(bus *busevents.EventBus, logger *slog.Logger) *MyComponent {
    c := &MyComponent{}
    c.Base = component.New(&component.Config{
        EventBus:   bus,
        Logger:     logger,
        Name:       "my-component",
        BufferSize: 100,
        Handler:    c,
        EventTypes: []string{events.EventTypeFoo, events.EventTypeBar}, // empty = all events
    })
    return c
}

// Then in iteration.go:
c := New(bus, logger)
go c.Start(ctx)
```

`EventTypes` is the typed-subscription filter — leave empty for components like the commentator that consume everything; populate it for everyone else so the bus filters at the source instead of dispatching every event to your channel.

## Key Interfaces

```go
// Required: dispatch one event at a time.
type EventHandler interface {
    HandleEvent(event busevents.Event)
}

// Optional: a scatter-gather responder that wants to publish a failure
// response if the handler panicked, instead of just logging.
type PanicHandler interface {
    HandlePanic(recovered any, event busevents.Event)
}
```

The base always logs the panic and keeps the loop alive regardless of whether the component implements `PanicHandler` — the interface is purely an opt-in extension point.

## Lifecycle

| Method | Purpose |
|--------|---------|
| `New(*Config)` | Subscribes to the EventBus and returns a `*Base`. Subscription happens in the constructor so the component is ready to receive events the moment `bus.Start()` is called. |
| `Start(ctx)` | Runs the event loop until `ctx` is cancelled or `Stop()` is called. Blocks. |
| `Stop()` | Idempotent shutdown signal. Useful for tests; production code typically just cancels the iteration context. |

## See Also

- [`pkg/controller/resourceloader`](../resourceloader/) — `BaseLoader` thin wrapper used by configloader / credentialsloader
- [`pkg/controller/validator`](../validator/) — `BaseValidator` thin wrapper used by the scatter-gather validators
- [`pkg/events`](../../events/) — the bus this scaffold subscribes to
- `ready.go` in this package — `ReadySignal` helper for one-shot ready signalling

## License

Apache-2.0 — see root `LICENSE`.
