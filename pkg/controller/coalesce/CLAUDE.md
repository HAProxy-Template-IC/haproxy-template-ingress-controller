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

Provides centralized "latest wins" coalescing for controller components. When events arrive faster than they can be processed, intermediate events are skipped and only the latest is processed.

## Key Function: DrainLatest

```go
func DrainLatest[T busevents.Event](
    eventChan <-chan busevents.Event,
    handleOther func(busevents.Event),
) (latest T, supersededCount int)
```

**What it does:**

1. Non-blocking drain of the event channel
2. Returns the latest event of type T that is coalescible
3. Passes non-coalescible events and other types to handleOther
4. Returns nil if no coalescible events found

**Coalescibility rules:**

1. Event must match type T
2. Event must implement `CoalescibleEvent` interface
3. `Coalescible()` must return true

## Usage Pattern

```go
func (c *Component) handleTrigger(event *events.TriggerEvent) {
    c.performWork(event)

    // After work completes, drain for latest coalescible event
    for {
        latest, superseded := coalesce.DrainLatest[*events.TriggerEvent](
            c.eventChan,
            c.handleEvent,
        )
        if latest == nil {
            return
        }
        c.logger.Debug("Processing coalesced event",
            "superseded_count", superseded)
        c.performWork(latest)
    }
}
```

## Components Using Coalescing

| Component | Event Type | Purpose |
|-----------|------------|---------|
| Renderer | `ReconciliationTriggeredEvent` | Skip intermediate triggers during rendering |
| Validator | `TemplateRenderedEvent` | Skip intermediate renders during validation |
| Deployer | `DeploymentScheduledEvent` | Deploy only latest config |

## Design Principles

This package follows SOLID principles:

- **Interface Segregation**: Uses `CoalescibleEvent` interface, not all events
- **Dependency Inversion**: Depends on interface, not concrete types
- **Single Responsibility**: Only handles coalescing logic

## Resources

- Event interfaces: `pkg/events/bus.go`
- Domain events: `pkg/controller/events/`
