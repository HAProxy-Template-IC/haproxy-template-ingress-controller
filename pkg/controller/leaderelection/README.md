# pkg/controller/leaderelection

Event adapter around [`pkg/k8s/leaderelection`](../../k8s/leaderelection/). The pure elector runs the Kubernetes `Lease`-based election; this adapter forwards every state change to the controller's `EventBus` so the commentator, metrics adapter, and leader-only components can react.

## Minimal Usage

```go
import (
    "context"
    "time"

    busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/leaderelection"
    k8sle "gitlab.com/haproxy-haptic/haptic/pkg/k8s/leaderelection"
)

cfg := &k8sle.Config{
    Enabled:         true,
    Identity:        podName,
    LeaseName:       "haptic",
    LeaseNamespace:  namespace,
    LeaseDuration:   15 * time.Second,
    RenewDeadline:   10 * time.Second,
    RetryPeriod:     2 * time.Second,
    ReleaseOnCancel: true,
}

callbacks := k8sle.Callbacks{
    OnStartedLeading: func(ctx context.Context) { /* start leader-only work */ },
    OnStoppedLeading: func()                    { /* stop leader-only work */ },
    OnNewLeader:      func(identity string)     { /* optional */ },
}

comp, err := leaderelection.New(cfg, clientset, eventBus, callbacks, logger)
if err != nil {
    return err
}

go comp.Start(ctx)    // blocks inside until ctx is cancelled
```

`Start(ctx)` is the entry point — the README previously used `Run(ctx)`, which doesn't exist. `IsLeader()` and `GetLeader()` are exposed for components that need to check state synchronously (e.g., webhook validators that behave differently on the leader).

## Events Published

Published before the matching user callback runs, so listeners see the transition before side-effects kick in:

| Event | When |
|-------|------|
| `LeaderElectionStartedEvent` | At the top of `Start` |
| `BecameLeaderEvent` | Inside the wrapper for `OnStartedLeading`, while the bus is paused (see below) |
| `LostLeadershipEvent` | Just before `OnStoppedLeading` |
| `NewLeaderObservedEvent` | Every time `OnNewLeader` fires (including when this replica loses) |

The publishing happens in the callback wrappers — user callbacks don't need to emit events themselves, and wouldn't be able to guarantee the "before the effect" ordering if they tried.

### Pause/Start contract on becoming leader

The `OnStartedLeading` wrapper runs `EventBus.Pause()` → publish `BecameLeaderEvent` → user callback → `EventBus.Start()`. This is deliberate: the user callback is where leader-only components are constructed and call `Subscribe()`, and the bus must replay the buffered `BecameLeaderEvent` to them *after* they've subscribed. Without the pause, leader-only components miss the very signal meant to wake them up. See "Late Subscriber Problem" in `pkg/controller/CLAUDE.md` for the full rationale.

## Why an Adapter At All

The pure elector in `pkg/k8s/leaderelection` has no dependency on the event bus — that keeps it reusable outside this controller. All bus-coupled observability (logging via commentator, metrics via `pkg/controller/metrics`, leader-only gating via `pkg/controller/leadership`) is concentrated here.

`Start` delegates directly to the pure elector's `Start`. `IsLeader` and `GetLeader` pass through the same way. If you find yourself adding business logic to this adapter, it probably belongs in the pure elector or a dedicated controller component instead.

## See Also

- [`pkg/k8s/leaderelection`](../../k8s/leaderelection/) — the pure elector and its `Config` / `Callbacks` shapes
- [`pkg/controller/leadership`](../leadership/) — gating utilities for leader-only components
- [`pkg/controller/events`](../events/) — event type definitions
- [`docs/controller/docs/operations/high-availability.md`](../../../docs/controller/docs/operations/high-availability.md) — operator-facing configuration reference

## License

Apache-2.0 — see root `LICENSE`.
