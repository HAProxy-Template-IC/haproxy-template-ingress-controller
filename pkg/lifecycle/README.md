# pkg/lifecycle

Component registry, startup coordination, and health tracking for the controller's iteration.

## Overview

The controller iterates by spinning up ~20 components in a coordinated startup sequence. `*Registry` is where they all register; it then drives `StartAll(ctx, isLeader)` (all-replica), then `StartLeaderOnlyComponentsAsync(ctx)` once leadership is acquired, tracking the per-component status (`Pending` / `Starting` / `Running` / `Failed` / `Stopped` / `Standby` (Standby = registered leader-only component on a follower replica — see `pkg/lifecycle/CLAUDE.md`)).

Components implement the `Component` interface (`Name() string` + `Start(ctx) error`); optionally they can also implement `HealthChecker` for active health probes and `SubscriptionReadySignaler` for "I've subscribed and am ready to receive events" signalling.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"

registry := lifecycle.NewRegistry().WithLogger(logger)

// Idiomatic registration uses the fluent Build() API — that's what the
// production controller (pkg/controller/reconciliation.go) does:
registry.Build().
    AllReplica(reconcilerComponent, discoveryComponent, httpStoreComponent, ...).
    LeaderOnly(coordinatorComponent, deployerComponent, schedulerComponent, ...).
    Done()

// Register(...) directly is the lower-level alternative:
registry.Register(schedulerComponent, lifecycle.LeaderOnly())

// Boot
if err := registry.StartAll(ctx, isLeader); err != nil { /* ... */ }
if isLeader {
    errCh, err := registry.StartLeaderOnlyComponentsAsync(ctx)
    if err != nil { /* ... */ }
    go func() {
        if err := <-errCh; err != nil { /* ... */ }
    }()
}

// Inspect
for name, info := range registry.Status() {
    if info.Status == lifecycle.StatusFailed {
        log.Error("component failed", "name", name, "err", info.Error)
    }
}
```

## Registration Options

| Option | Effect |
|--------|--------|
| `LeaderOnly()` | Component is only started inside `StartLeaderOnlyComponentsAsync`, not in `StartAll` |

`StartLeaderOnlyComponentsAsync` returns once all leader-only components are subscription-ready and hands back an error channel so the caller can track failures asynchronously — this is what makes the EventBus Pause/Start replay pattern safe.

## Status

`Registry.Status()` returns `map[string]ComponentInfo` for every registered component. `ComponentInfo` carries:

- `Name`, `Status` (`Pending` / `Starting` / `Running` / `Failed` / `Stopped` / `Standby` (Standby = registered leader-only component on a follower replica — see `pkg/lifecycle/CLAUDE.md`))
- `LeaderOnly bool`
- `Error string` (last error if `Status == Failed`)
- `Healthy *bool` (nil if the component doesn't implement `HealthChecker`)

Production code should walk `Status()` and apply its own health policy.

## See Also

- `pkg/lifecycle/CLAUDE.md` — design notes (subscription-ready signalling, health tracking)
- [`pkg/controller/leadership`](../controller/leadership/) — `StateReplayer[T]` for components that need to receive cached events on `BecameLeaderEvent`
- [`pkg/controller/component`](../controller/component/) — most components embed `*component.Base` to satisfy the `Component` interface

## License

Apache-2.0 — see root `LICENSE`.
