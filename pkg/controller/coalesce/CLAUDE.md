# pkg/controller/coalesce - Event Coalescing Utilities

Development context for event coalescing utilities.

## When to Work Here

Work in this package when:

- Modifying coalescing behavior
- Adding new coalescing utilities
- Fixing coalescing bugs

**DO NOT** work here for:

- Event types → Use `pkg/controller/events`
- Event bus infrastructure → Use `pkg/events`
- Component-specific logic → Use the component package

## Package Purpose

Provides centralized "latest wins" coalescing for controller components. When events arrive faster than they can be processed, intermediate events of the coalesced type are skipped within uninterrupted runs and only each run's latest is processed.

## Key Function: DrainLatest

```go
func DrainLatest[T busevents.Event](
    eventChan <-chan busevents.Event,
    handleOther func(busevents.Event),
    flush func(latest T, supersededCount int),
)
```

**What it does:**

1. Non-blocking drain of the event channel until momentarily empty
2. Collapses uninterrupted runs of coalescible T-events to their latest, delivered via `flush`
3. Any other event is a run boundary: the held run is flushed FIRST, then the event goes to `handleOther` — arrival order across event types is preserved
4. The trailing run is flushed before returning

**Coalescibility rules:**

1. Event must match type T
2. Event must implement `CoalescibleEvent` interface
3. `Coalescible()` must return true

**Why flush-at-boundary matters (starvation regression):** an earlier design
held the run's latest back until the channel drained empty. Under sustained
mixed traffic (each `handleOther` dispatch slower than the event arrival gap)
the channel never emptied, so the coalesced type was starved for the entire
burst — observed as rendered status patches stalling 54s in gateway-api
conformance while deployment-completed applies flowed. It also reordered
events: later other-type events were dispatched ahead of the older held event.

## Usage Pattern

```go
func (c *Component) handleTrigger(event *events.TriggerEvent) {
    c.performWork(event)

    // After work completes, drain queued events: consecutive coalescible
    // TriggerEvents collapse to their latest, everything else is handled
    // in arrival order.
    coalesce.DrainLatest(
        c.eventChan,
        c.handleEvent,
        func(latest *events.TriggerEvent, superseded int) {
            c.performWork(latest)
        },
    )
}
```

## Components Using Coalescing

| Component | Event Type | Purpose |
|-----------|------------|---------|
| DeploymentScheduler (`pkg/controller/deployer`) | `HAProxyPodsDiscoveredEvent` | Use only the most recent pod-discovery snapshot per scheduling decision |

This is deliberately the ONLY remaining `DrainLatest` consumer: the
DeploymentScheduler runs a hand-rolled event loop with extra `select` arms
(deploy-signal, ticker) that cannot embed `component.Base`. Every
`component.Base`-embedded component coalesces through Base's MAILBOX mode
instead (declare types via `CoalescesOn() []string` — see
`pkg/controller/component`): the Deployer's `DeploymentScheduledEvent`
coalescing moved there. A third, intentionally different mechanism lives in
`reconciler.Coordinator.coalesceQueuedTriggers` (merges a whole drained run
into ONE re-render, exploiting renders always reading current store state).

Grep for `coalesce.DrainLatest[` to find every call site — adding a new one is
the canonical sign you should also update this table and consider whether the
event type should implement `CoalescibleEvent` (or whether the component can
simply embed `component.Base` and use mailbox coalescing instead).

## Design Principles

This package follows SOLID principles:

- **Interface Segregation**: Uses `CoalescibleEvent` interface, not all events
- **Dependency Inversion**: Depends on interface, not concrete types
- **Single Responsibility**: Only handles coalescing logic

## Resources

- Event interfaces: `pkg/events/bus.go`
- Domain events: `pkg/controller/events/`
