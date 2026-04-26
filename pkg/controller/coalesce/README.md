# pkg/controller/coalesce

Generic "latest-wins" channel drain helper used by event-driven components that produce work faster than they can consume it.

## Overview

When an upstream burst of events floods a component's subscription channel, processing each one individually wastes work — only the most recent event is meaningful for downstream consumers. `DrainLatest` lets a component finish processing the *current* event, then non-blockingly drain the rest of the channel and pick the latest coalescible event to process next, while routing non-coalescible events through the component's regular handler.

This is the same pattern several components implement; pulling it into one place avoids re-implementing the type assertion + channel-drain dance everywhere.

## API

```go
func DrainLatest[T busevents.Event](
    eventChan <-chan busevents.Event,
    handleOther func(busevents.Event),
) (latest T, supersededCount int)
```

Returns `(zeroValue, 0)` if there's nothing coalescible queued. Non-matching event types and matching events whose `Coalescible() bool` returns `false` are routed to `handleOther` immediately.

## Coalescibility

Only events that implement the `pkg/events.CoalescibleEvent` interface and return `Coalescible() == true` are eligible for coalescing. The flag is set by the publisher per event — e.g. `ReconciliationTriggeredEvent` is coalescible when triggered by a debounced resource change but *not* when triggered by drift prevention (a missed drift-prevention pass would silently swallow the corrective action).

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/coalesce"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

func (c *Component) handleSomeEvent(event *events.SomeEvent) {
    c.performWork(event)

    // After work completes, drain to find the most recent coalescible
    // event of the same type that arrived while we were busy.
    for {
        latest, superseded := coalesce.DrainLatest[*events.SomeEvent](
            c.eventChan,
            c.handleEvent, // route non-coalescible / other types here
        )
        if latest == nil {
            return
        }
        c.logger.Debug("processing coalesced event", "superseded_count", superseded)
        c.performWork(latest)
    }
}
```

## See Also

- [`pkg/events`](../../events/) — defines the `CoalescibleEvent` interface
- [`pkg/controller/reconciler`](../reconciler/) — primary *producer*: marks `ReconciliationTriggeredEvent` coalescible (or not, depending on the trigger reason) before publishing
- Consumers (grep `coalesce.DrainLatest[`): `pkg/controller/renderer`, `pkg/controller/validator`, `pkg/controller/deployer`

## License

Apache-2.0 — see root `LICENSE`.
