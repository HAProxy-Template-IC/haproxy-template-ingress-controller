# pkg/lifecycle

Component registry, dependency-ordered startup, and health tracking for the controller's iteration.

## Overview

The controller iterates by spinning up ~20 components in a coordinated startup sequence. `*Registry` is where they all register; it then drives `StartAll(ctx, isLeader)` (all-replica), then `StartLeaderOnlyComponents(ctx)` once leadership is acquired, taking dependency order into account and tracking the per-component status (`Pending` / `Starting` / `Running` / `Failed` / `Stopped` / `Standby` (Standby = registered leader-only component on a follower replica — see `pkg/lifecycle/CLAUDE.md`)).

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

// Register(...) directly is the lower-level alternative when you need
// per-component options (DependsOn, Criticality, OnError) — Build() always
// uses the defaults.
registry.Register(schedulerComponent,
    lifecycle.LeaderOnly(),
    lifecycle.DependsOn("deployer"),
)
registry.Register(metricsComponent,
    lifecycle.Criticality(lifecycle.CriticalityOptional),
)
registry.Register(reconcilerComponent, lifecycle.OnError(func(name string, err error) {
    alerting.Send(fmt.Sprintf("%s: %v", name, err))
}))

// Boot
if err := registry.StartAll(ctx, isLeader); err != nil { /* ... */ }
if isLeader {
    if err := registry.StartLeaderOnlyComponents(ctx); err != nil { /* ... */ }
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
| `LeaderOnly()` | Component is only started inside `StartLeaderOnlyComponents`, not in `StartAll` |
| `DependsOn(names...)` | Component waits for the listed components to reach `Running` before its own `Start` is invoked |
| `Criticality(level)` | `CriticalityCritical` (default), `CriticalityDegradable`, or `CriticalityOptional` |
| `OnError(fn)` | Custom callback invoked when the component returns an error |

Async leader-only startup is also available via `StartLeaderOnlyComponentsAsync` — it returns an error channel so the caller can decide whether to block on subscription-readiness.

## Status

`Registry.Status()` returns `map[string]ComponentInfo` for every registered component. `ComponentInfo` carries:

- `Name`, `Status` (`Pending` / `Starting` / `Running` / `Failed` / `Stopped` / `Standby` (Standby = registered leader-only component on a follower replica — see `pkg/lifecycle/CLAUDE.md`))
- `LeaderOnly bool`
- `Error string` (last error if `Status == Failed`)
- `Healthy *bool` (nil if the component doesn't implement `HealthChecker`)

The `isHealthy()` helper used by `/healthz` is unexported (test-only); production code should walk `Status()` and apply its own policy.

## See Also

- `pkg/lifecycle/CLAUDE.md` — design notes (subscription-ready signalling, dependency cycles, health tracking)
- [`pkg/controller/leadership`](../controller/leadership/) — `StateReplayer[T]` for components that need to receive cached events on `BecameLeaderEvent`
- [`pkg/controller/component`](../controller/component/) — most components embed `*component.Base` to satisfy the `Component` interface

## License

Apache-2.0 — see root `LICENSE`.
