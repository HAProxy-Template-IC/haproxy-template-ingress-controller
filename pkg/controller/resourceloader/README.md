# pkg/controller/resourceloader

Thin event-loop wrapper around `pkg/controller/component.Base` for resource-loading components (configloader, credentialsloader).

## Overview

Both Stage-1 loaders share an identical pattern: subscribe to one resource-changed event type, type-assert the payload, parse / validate it, publish a "parsed" event. Before this package they each duplicated the subscribe-on-construction + dispatch-with-recover scaffold; now they share `BaseLoader`.

`BaseLoader` is just `*component.Base` with one extra layer: it accepts an `EventProcessor` interface (a single `ProcessEvent(event)` method) instead of `component.EventHandler`. The split exists because the loaders predate the consolidated `component` package — keeping the older method name avoids a cross-package rename. New components should use `component.New` directly; the existing loaders use this wrapper for source-stability.

## Quick Start

```go
import (
    "log/slog"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/resourceloader"
    busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type MyLoader struct {
    *resourceloader.BaseLoader
}

func (l *MyLoader) ProcessEvent(event busevents.Event) {
    // type-assert, parse, publish a "parsed" event
}

func New(bus *busevents.EventBus, logger *slog.Logger) *MyLoader {
    l := &MyLoader{}
    l.BaseLoader = resourceloader.NewBaseLoader(
        bus, logger, "my-loader", 100, l,
        events.EventTypeMyResourceChanged,
    )
    return l
}
```

The variadic `eventTypes` argument at the end is the typed-subscription filter — pass the specific event types the loader cares about so the bus filters at the source.

## See Also

- [`pkg/controller/component`](../component/) — underlying `Base` + `EventHandler` scaffold this package wraps
- [`pkg/controller/configloader`](../configloader/) / [`credentialsloader`](../credentialsloader/) — the loaders built on this base

## License

Apache-2.0 — see root `LICENSE`.
