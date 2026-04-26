# pkg/k8s/leaderelection - Leader Election

Development context for the leader election pure component.

**API Documentation**: See `pkg/k8s/leaderelection/README.md`

## When to Work Here

Modify this package when:

- Changing leader election behavior or configuration
- Adding new leader election features
- Fixing bugs in election logic
- Improving lease management

**DO NOT** modify this package for:

- Event publishing → Use `pkg/controller/leaderelection`
- Controller coordination → Use `pkg/controller`
- Metrics/observability → Use controller event adapter

## Package Purpose

Pure library for Kubernetes leader election using Lease resources. Wraps `k8s.io/client-go/tools/leaderelection` with a clean, testable interface.

**Key principle**: This is a pure component with NO event bus dependencies. All coordination logic belongs in `pkg/controller/leaderelection`.

## Architecture

```
Pure Component                          Event Adapter
(pkg/k8s/leaderelection)               (pkg/controller/leaderelection)
         ↓                                      ↓
    Elector ──────wrapped by────→    LeaderElectionComponent
  - Pure logic                        - Publishes events
  - No events                         - Observability
  - Reusable                          - Metrics
```

## Design Patterns

### Pure Callbacks

Callbacks are provided by the caller (controller), allowing pure component to remain event-agnostic:

```go
// Controller provides callbacks
callbacks := leaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        // Controller-specific logic
        startLeaderOnlyComponents()
        publishBecameLeaderEvent()
    },
    OnStoppedLeading: func() {
        // Controller-specific logic
        stopLeaderOnlyComponents()
        publishLostLeadershipEvent()
    },
}

// Pure elector just invokes callbacks
elector, _ := leaderelection.New(config, clientset, callbacks, logger)
elector.Start(ctx)
```

### Thread-Safe State

Internal state (isLeader, leader identity) protected by RWMutex for concurrent access:

```go
func (e *Elector) IsLeader() bool {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.isLeader
}
```

## Testing Strategy

Pure components are easier to test without event infrastructure:

```go
func TestElector_BecomesLeader(t *testing.T) {
    // Use fake Kubernetes clientset
    clientset := fake.NewSimpleClientset()

    becameLeader := false
    callbacks := leaderelection.Callbacks{
        OnStartedLeading: func(ctx context.Context) {
            becameLeader = true
        },
    }

    elector, err := leaderelection.New(config, clientset, callbacks, logger)
    require.NoError(t, err)

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    go elector.Start(ctx)

    // Wait for election
    time.Sleep(2 * time.Second)

    assert.True(t, elector.IsLeader())
    assert.True(t, becameLeader)
}
```

## Common Patterns

### Graceful Shutdown

Leader election releases lease when context cancelled (if `ReleaseOnCancel: true`):

```go
ctx, cancel := context.WithCancel(context.Background())

go elector.Start(ctx)

// Later: trigger graceful shutdown
cancel()  // Elector stops, releases lease
```

### Identity from Environment

Typically use pod name from Downward API:

```go
config := &leaderelection.Config{
    Identity: os.Getenv("POD_NAME"),
    // ...
}
```

## Common Pitfalls

### Ignoring the Callback's Context

**Problem**: Long-running work in `OnStartedLeading` keeps going after the
elector has lost the lease. Leadership *is* exclusive at the instant the
callback fires — the race is between "leadership lost" and "in-flight work
keeps writing as if it's still leader".

```go
// Bad — the goroutine outlives leadership
callbacks.OnStartedLeading = func(ctx context.Context) {
    go func() {
        for {
            deployToHAProxy() // blissfully unaware leadership was lost
            time.Sleep(time.Minute)
        }
    }()
}
```

**Solution**: Respect the context the callback receives. The elector cancels
it as soon as leadership is lost, so any work that derives from it stops
naturally.

```go
// Good — work tied to the leadership-scoped context
callbacks.OnStartedLeading = func(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(time.Minute)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                deployToHAProxy()
            case <-ctx.Done(): // fires the moment leadership is lost
                return
            }
        }
    }()
}
```

`IsLeader()` and `GetLeader()` are useful for logging/observability, but
spamming them inside hot paths instead of using the context is a smell —
they're snapshot accessors, not synchronisation primitives.

### Forgetting to Handle OnStoppedLeading

**Problem**: Leader-only components keep running after losing leadership.

```go
// Bad - no cleanup
callbacks.OnStoppedLeading = func() {
    // Empty - components still running!
}
```

**Solution**: Always stop leader-only components.

```go
// Good - cleanup
callbacks.OnStoppedLeading = func() {
    stopLeaderOnlyComponents()
}
```

### Wrong Lease Duration

**Problem**: Too short lease duration causes thrashing.

```go
// Bad - 1 second too short
LeaseDuration: 1 * time.Second,
```

**Solution**: Use recommended durations.

```go
// Good - standard durations
LeaseDuration: 15 * time.Second,
RenewDeadline: 10 * time.Second,
RetryPeriod:   2 * time.Second,
```

## Resources

- Kubernetes leader election: <https://pkg.go.dev/k8s.io/client-go/tools/leaderelection>
- Controller event adapter: `pkg/controller/leaderelection/CLAUDE.md`
- Configuration: `pkg/core/config/types.go` — `LeaderElectionConfig` struct, exposed on `Config.Controller.LeaderElection` (CRD path `spec.controller.leaderElection`; the snake_case `leader_election` form is the internal YAML the controller round-trips through, not what users put in the CR)
