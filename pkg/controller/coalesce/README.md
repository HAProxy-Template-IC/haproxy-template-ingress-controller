# pkg/controller/coalesce

Generic "latest-wins" channel drain helper used by event-driven components that produce work faster than they can consume it.

## Overview

When an upstream burst of events floods a component's subscription channel, processing each one individually wastes work — only the most recent event is meaningful for downstream consumers. `DrainLatest` lets a component finish processing the *current* event, then non-blockingly drain the rest of the channel: uninterrupted runs of coalescible events collapse to their latest element (delivered via the `flush` callback), while every other event is a run boundary — the held run is flushed first, then the event is routed to the component's regular handler, preserving arrival order across event types.

This is the same pattern several components implement; pulling it into one place avoids re-implementing the type assertion + channel-drain dance everywhere. Flushing at run boundaries (instead of holding the latest until the channel empties) is load-bearing: under sustained mixed traffic the channel may never empty, and a hold-until-empty drain starves the coalesced type for the entire burst while dispatching newer other-type events ahead of the older held one.

## API

```go
func DrainLatest[T busevents.Event](
    eventChan <-chan busevents.Event,
    handleOther func(busevents.Event),
    flush func(latest T, supersededCount int),
)
```

Drains until the channel is momentarily empty; `flush` is invoked once per run of coalescible events (with how many earlier events the delivered one superseded). Non-matching event types and matching events whose `Coalescible() bool` returns `false` are routed to `handleOther` in arrival order.

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

    // After work completes, drain queued events: consecutive coalescible
    // SomeEvents collapse to their latest, everything else is handled in
    // arrival order.
    coalesce.DrainLatest(
        c.eventChan,
        c.handleEvent, // route non-coalescible / other types here
        func(latest *events.SomeEvent, superseded int) {
            c.logger.Debug("processing coalesced event", "superseded_count", superseded)
            c.performWork(latest)
        },
    )
}
```

## See Also

- [`pkg/events`](../../events/) — defines the `CoalescibleEvent` interface
- [`pkg/controller/reconciler`](../reconciler/) — primary *producer*: marks `ReconciliationTriggeredEvent` coalescible (or not, depending on the trigger reason) before publishing
- Consumers (grep `coalesce.DrainLatest[`): only `pkg/controller/deployer`'s `DeploymentScheduler.handlePodsDiscovered` (hand-rolled loop that can't embed `component.Base`); Base-embedded components coalesce via Base's mailbox mode (`CoalescesOn() []string`) instead

## License

Apache-2.0 — see root `LICENSE`.
