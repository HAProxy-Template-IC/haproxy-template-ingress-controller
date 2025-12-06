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
    └── IsHealthy() bool
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

Optional interface for health checks:

```go
type HealthChecker interface {
    HealthCheck() error
}
```

## Registration Options

### LeaderOnly

Component only runs when instance is leader:

```go
registry.Register(deployer, lifecycle.LeaderOnly())
```

### DependsOn

Component depends on other components (future use):

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

// Register all-replica components
registry.Register(reconciler.New(bus, logger))
registry.Register(renderer.New(bus, cfg, stores))
registry.Register(executor.New(bus, logger))

// Register leader-only components
registry.Register(deployer.New(bus, logger), lifecycle.LeaderOnly())
registry.Register(scheduler.New(bus, logger), lifecycle.LeaderOnly())

// Start all components
if err := registry.StartAll(ctx, isLeader); err != nil {
    return fmt.Errorf("failed to start components: %w", err)
}

// Later, when becoming leader
if err := registry.StartLeaderOnlyComponents(ctx); err != nil {
    return fmt.Errorf("failed to start leader components: %w", err)
}

// Check health
if !registry.IsHealthy() {
    log.Error("System unhealthy")
}
```

## Component Status

Status values:

- `StatusPending` - Registered but not started
- `StatusStarting` - Currently starting
- `StatusRunning` - Running normally
- `StatusFailed` - Failed to start or crashed
- `StatusStopped` - Gracefully stopped

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
