# pkg/lifecycle - Component Lifecycle Management

Development context for the lifecycle management package.

## When to Work Here

Work in this package when:

- Modifying component lifecycle management
- Adding new registration options
- Enhancing health check support
- Implementing dependency ordering

**DO NOT** work here for:

- Event bus infrastructure → Use `pkg/events`
- Component business logic → Use relevant component package
- Controller orchestration → Use `pkg/controller`

## Package Purpose

Pure infrastructure package providing component lifecycle management. Components can be registered with the registry and started with configurable options like leader-only, dependencies, and criticality levels.

## Architecture

```
Registry
    │
    ├── Register(component, opts...)
    │       ├── LeaderOnly()
    │       ├── DependsOn(...)
    │       ├── Criticality(...)
    │       └── OnError(handler)
    │
    ├── StartAll(ctx, isLeader) error
    │       └── Starts all registered components
    │
    ├── StartLeaderOnlyComponents(ctx) error
    │       └── Starts only leader-only components
    │
    ├── Status() map[string]ComponentInfo
    │       └── Returns status of all components
    │
    └── isHealthy() bool  (unexported, test-only)
            └── Checks critical component health
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
close before treating the component as ready, so dependents that `DependsOn`
this one don't race against the late subscription.

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

### DependsOn

Component waits for the listed components to reach `StatusRunning` (and, if
they implement `SubscriptionReadySignaler`, for their `SubscriptionReady`
channel to close) before its own `Start()` is invoked. Used by the controller
iteration to enforce the order between, e.g., the validator/renderer pair and
the deployer that consumes their events.

```go
registry.Register(deployer, lifecycle.DependsOn("validator", "renderer"))
```

### Criticality

Controls how component affects system health:

```go
registry.Register(metrics, lifecycle.Criticality(lifecycle.CriticalityOptional))
```

Levels:

- `CriticalityCritical` - System fails if component fails (default)
- `CriticalityDegradable` - System works with reduced capability
- `CriticalityOptional` - System works normally without

### OnError

Custom error handler for component failures:

```go
registry.Register(reconciler, lifecycle.OnError(func(name string, err error) {
    alerting.Send(fmt.Sprintf("Component %s failed: %v", name, err))
}))
```

## Usage Pattern

```go
// Create registry
registry := lifecycle.NewRegistry()

// Idiomatic registration uses the fluent Build() API — that's what the
// production controller wires up in pkg/controller/reconciliation.go.
// Build() registers each component with default options (no DependsOn,
// CriticalityCritical, no OnError handler).
registry.Build().
    AllReplica(reconcilerComponent, rendererComponent, coordinatorComponent).
    LeaderOnly(deployerComponent, schedulerComponent).
    Done()

// When you need per-component options (DependsOn, Criticality, OnError),
// drop down to Register(...) directly:
registry.Register(schedulerComponent,
    lifecycle.LeaderOnly(),
    lifecycle.DependsOn("deployer"),
)

// Start all-replica components (followers stop here)
if err := registry.StartAll(ctx, isLeader); err != nil {
    return fmt.Errorf("failed to start components: %w", err)
}

// Later, when becoming leader
if err := registry.StartLeaderOnlyComponents(ctx); err != nil {
    return fmt.Errorf("failed to start leader components: %w", err)
}

// Health is exposed via Registry.Status() (the unexported isHealthy() helper
// is only reachable from inside this package).
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
