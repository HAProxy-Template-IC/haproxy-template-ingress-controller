# pkg/lifecycle - Component Lifecycle Management

Development context for the lifecycle management package.

## When to Work Here

Work in this package when:

- Modifying component lifecycle management
- Adding new registration options
- Enhancing health check support

**DO NOT** work here for:

- Event bus infrastructure → Use `pkg/events`
- Component business logic → Use relevant component package
- Controller orchestration → Use `pkg/controller`

## Package Purpose

Pure infrastructure package providing component lifecycle management. Components can be registered with the registry and started, optionally gated on leader election.

## Architecture

```
Registry
    │
    ├── Register(component, opts...)
    │       └── LeaderOnly()
    │
    ├── StartAll(ctx, isLeader) error
    │       └── Starts all registered components
    │
    ├── StartLeaderOnlyComponentsAsync(ctx) (<-chan error, error)
    │       └── Starts only leader-only components; returns once they're
    │           subscription-ready, failures arrive on the error channel
    │
    └── Status() map[string]ComponentInfo
            └── Returns status of all components
```

## Interfaces

### Component Interface

Minimal interface for managed components:

```go
type Component interface {
    Name() string
    Start(ctx context.Context) error
}
```

### HealthChecker Interface

Optional interface for active health checks. Returning `nil` reports healthy;
any error makes the registry mark the component as unhealthy.

```go
type HealthChecker interface {
    HealthCheck() error
}
```

### SubscriptionReadySignaler Interface

Optional interface for components that can't subscribe to the bus during their
constructor (typically leader-only components that subscribe inside `Start()`
once they hold the lease). The registry waits for the returned channel to
close before treating the component as ready, so
`StartLeaderOnlyComponentsAsync` doesn't return (and the caller doesn't
restart the EventBus) before the late subscription is in place.

```go
type SubscriptionReadySignaler interface {
    SubscriptionReady() <-chan struct{}
}
```

## Registration Options

### LeaderOnly

Component only runs when instance is leader:

```go
registry.Register(deployer, lifecycle.LeaderOnly())
```

## Usage Pattern

```go
// Create registry
registry := lifecycle.NewRegistry()

// Idiomatic registration uses the fluent Build() API — that's what the
// production controller wires up in pkg/controller/reconciliation.go.
registry.Build().
    AllReplica(reconcilerComponent, rendererComponent, coordinatorComponent).
    LeaderOnly(deployerComponent, schedulerComponent).
    Done()

// Register(...) directly is the lower-level alternative:
registry.Register(schedulerComponent, lifecycle.LeaderOnly())

// Start all-replica components (followers stop here)
if err := registry.StartAll(ctx, isLeader); err != nil {
    return fmt.Errorf("failed to start components: %w", err)
}

// Later, when becoming leader
errCh, err := registry.StartLeaderOnlyComponentsAsync(ctx)
if err != nil {
    return fmt.Errorf("failed to start leader components: %w", err)
}
go func() {
    if err := <-errCh; err != nil {
        log.Error("Leader component failed", "err", err)
    }
}()

// Health is exposed via Registry.Status().
for name, info := range registry.Status() {
    if info.Status == lifecycle.StatusFailed {
        log.Error("Component failed", "name", name, "err", info.Error)
    }
}
```

## Component Status

Status values (declared in `pkg/lifecycle/component.go`):

- `StatusPending` — Registered but not yet started.
- `StatusStarting` — Currently starting (transient; flipped before the `Start()` call returns control).
- `StatusRunning` — Running normally; consumers should treat the component as live.
- `StatusStandby` — Intentionally inactive, waiting for an external condition. Used for leader-only components on followers: they're registered and ready, but won't be started until leadership is acquired. Distinct from `StatusPending`, which means "about to start".
- `StatusFailed` — Failed to start or encountered a fatal error.
- `StatusStopped` — Gracefully stopped (e.g. context cancellation).

Health roll-up in `Status()` ignores `StatusStandby` and `StatusPending` — they don't have a meaningful health state because the component hasn't been asked to do anything yet.

## Adding Name() to Components

When adding lifecycle support to existing components:

1. Add a constant for the component name:

   ```go
   const ComponentName = "my-component"
   ```

2. Implement the Name() method:

   ```go
   func (c *Component) Name() string {
       return ComponentName
   }
   ```

3. The component already has Start(ctx) method - no changes needed

## Testing

```go
func TestComponentLifecycle(t *testing.T) {
    registry := lifecycle.NewRegistry()

    comp := &mockComponent{name: "test"}
    registry.Register(comp)

    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    go registry.StartAll(ctx, false)

    time.Sleep(20 * time.Millisecond)
    assert.True(t, comp.IsStarted())

    cancel()
    // Component stops when context cancelled
}
```

## Resources

- Package organization: `pkg/CLAUDE.md`
- Controller orchestration: `pkg/controller/CLAUDE.md`
- Event infrastructure: `pkg/events/CLAUDE.md`
