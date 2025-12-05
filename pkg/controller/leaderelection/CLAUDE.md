# pkg/controller/leaderelection - Leader Election Event Adapter

Development context for the leader election event adapter.

## When to Work Here

Work in this package when:

- Modifying event publishing for leader election
- Adding observability around leadership transitions
- Changing how leader election integrates with the EventBus

**DO NOT** work here for:

- Pure leader election logic → Use `pkg/k8s/leaderelection`
- Controller startup logic → Use `pkg/controller`
- Event definitions → Use `pkg/controller/events`
- Configuration schema → Use `pkg/core/config`

## Package Purpose

Event adapter that wraps the pure leader election component (`pkg/k8s/leaderelection`) and publishes observability events to the controller's EventBus. Follows the clean architecture pattern where business logic lives in pure packages and controller packages only contain event coordination.

## Architecture

```
Pure Component                Event Adapter
(pkg/k8s/leaderelection)     (pkg/controller/leaderelection)
         ↓                            ↓
    Elector ──────wrapped by────→ Component
  - Pure logic                  - Publishes events
  - No events                   - Observability
  - Reusable                    - Metrics
```

## Key Design Pattern

### Event Wrapping

The component wraps user-provided callbacks to publish events before execution:

```go
wrappedCallbacks := k8sleaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        // Publish event BEFORE callback
        c.eventBus.Publish(events.NewBecameLeaderEvent(config.Identity))

        // Execute user callback
        if callbacks.OnStartedLeading != nil {
            callbacks.OnStartedLeading(ctx)
        }
    },
}
```

**Why wrap callbacks?**

- Ensures events always published regardless of user callback behavior
- Decouples observability from business logic
- User callbacks can fail without affecting event publishing

### Pure Component Delegation

All business logic delegates to the pure elector:

```go
type Component struct {
    elector  *k8sleaderelection.Elector  // Pure component
    eventBus *busevents.EventBus         // Event coordination
    // ...
}

func (c *Component) IsLeader() bool {
    return c.elector.IsLeader()  // Delegate to pure component
}
```

**Why delegate?**

- Event adapter is thin wrapper (no business logic)
- Pure component remains reusable
- Clear separation of concerns

## Events Published

The event adapter publishes four event types:

1. **LeaderElectionStartedEvent** - When Run() starts
2. **BecameLeaderEvent** - Before OnStartedLeading callback
3. **LostLeadershipEvent** - Before OnStoppedLeading callback
4. **NewLeaderObservedEvent** - When any leader observed

See `pkg/controller/events/types.go` for event definitions.

## Usage Pattern

```go
// Create pure config
config := &k8sleaderelection.Config{
    Enabled:         true,
    Identity:        podName,
    LeaseName:       "my-app-leader",
    LeaseNamespace:  namespace,
    LeaseDuration:   15 * time.Second,
    RenewDeadline:   10 * time.Second,
    RetryPeriod:     2 * time.Second,
    ReleaseOnCancel: true,
}

// Define callbacks
callbacks := k8sleaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        startLeaderOnlyComponents(ctx)
    },
    OnStoppedLeading: func() {
        stopLeaderOnlyComponents()
    },
}

// Create event adapter
component, _ := leaderelection.New(config, clientset, eventBus, callbacks, logger)

// Run (blocks until context cancelled)
go component.Run(ctx)
```

## Testing

Event adapter testing focuses on verifying event publishing:

```go
func TestComponent_PublishesEvents(t *testing.T) {
    bus := busevents.NewEventBus(100)
    config := &k8sleaderelection.Config{...}
    callbacks := k8sleaderelection.Callbacks{...}

    component, _ := New(config, clientset, bus, callbacks, logger)

    // Subscribe to events
    eventChan := bus.Subscribe(10)
    bus.Start()

    go component.Run(ctx)

    // Verify LeaderElectionStartedEvent published
    event := <-eventChan
    assert.IsType(t, &events.LeaderElectionStartedEvent{}, event)
}
```

For pure leader election logic testing, see `pkg/k8s/leaderelection/CLAUDE.md`.

## Common Pitfalls

### Forgetting to Wrap Callbacks

**Problem**: Publishing events manually in callbacks instead of using the event adapter.

```go
// Bad - duplicates event adapter functionality
callbacks := k8sleaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        eventBus.Publish(events.NewBecameLeaderEvent(...))  // Redundant!
        startComponents(ctx)
    },
}
```

**Solution**: Let event adapter handle event publishing.

```go
// Good - event adapter publishes automatically
callbacks := k8sleaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        startComponents(ctx)  // Just business logic
    },
}
```

### Testing Without EventBus

**Problem**: Trying to test leader election without event infrastructure.

**Solution**: Use pure component (`pkg/k8s/leaderelection`) for tests that don't need events.

## Resources

- API documentation: `./README.md`
- Pure component: `pkg/k8s/leaderelection/CLAUDE.md`
- Event types: `pkg/controller/events/types.go`
- Controller integration: `pkg/controller/CLAUDE.md`
- Commentator (event logging): `pkg/controller/commentator/CLAUDE.md`
