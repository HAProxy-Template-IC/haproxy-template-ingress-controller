# pkg/controller/deployer

Three components that together get validated configurations onto HAProxy pods:

- **`DeploymentScheduler`** — decides *when* to deploy. Keeps the last validated config + last discovered endpoints, rate-limits to `minDeploymentInterval`, and queues at most one pending deployment ("latest wins"). Also times out deployments that take longer than `deploymentTimeout` so a dropped `DeploymentCompletedEvent` can't wedge the pipeline forever.
- **`Component`** (the deployer itself) — stateless executor. Consumes `DeploymentScheduledEvent` and deploys to every discovered HAProxy endpoint in parallel using `pkg/dataplane.Client`. Its per-sync timeouts (`reloadVerificationTimeout`, `syncTimeout`, passed to `New`) are forwarded to each `pkg/dataplane` sync.
- **`DriftPreventionMonitor`** — fires a synthetic `DriftPreventionTriggeredEvent` every `driftPreventionInterval` when nothing has deployed recently, so an out-of-band change applied directly via the Dataplane API gets overwritten by the controller's last-known-good config.

All three are leader-only — only the replica holding the `Lease` deploys, observers on other replicas stay idle.

## Minimal Usage

```go
import (
    "context"
    "time"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/deployer"
    "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

scheduler := deployer.NewDeploymentScheduler(
    bus, logger,
    2*time.Second,   // minDeploymentInterval
    30*time.Second,  // deploymentTimeout
)
exec := deployer.New(bus, logger,
    10*time.Second, // reloadVerificationTimeout
    2*time.Minute,  // syncTimeout
)
monitor := deployer.NewDriftPreventionMonitor(bus, logger, 60*time.Second)

go scheduler.Start(ctx)
go exec.Start(ctx)
go monitor.Start(ctx)
```

All durations / ints come from `spec.dataplane` and `spec.controller` on the CRD: `minDeploymentInterval`, `deploymentTimeout`, `driftPreventionInterval`, `reloadVerificationTimeout`, `syncTimeout`.

## Event Flow

```
TemplateRenderedEvent ───────┐
ValidationCompletedEvent ────┤
HAProxyPodsDiscoveredEvent ──┤
DriftPreventionTriggeredEvent┤
DeploymentCompletedEvent ────┤       (feedback edge)
                             ▼
                     DeploymentScheduler
                             │
                             ▼
                     DeploymentScheduledEvent
                             │
                             ▼
                         Component
                             │
             ▼
           DeploymentStartedEvent
           InstanceDeploymentFailedEvent (per endpoint)
           DeploymentCompletedEvent
                             │
                             ▼
                   DriftPreventionMonitor
                             │
                             ▼
                   DriftPreventionTriggeredEvent (if idle for > interval)
```

Notable details:

- The scheduler only deploys when it has *all three* inputs: a rendered config, a successful validation, and at least one discovered HAProxy endpoint. Partial state waits.
- "Latest wins" is a single slot — concurrent changes don't queue up as a FIFO, they coalesce to the most recent one.
- `DeploymentCompletedEvent` both closes the in-progress flag in the scheduler *and* resets the drift-monitor's idle timer, which is why it's on the feedback edge in the diagram.
- `deploymentTimeout` is a safety net, not an operational target — hitting it means a lost completion event or a stuck dataplane call, both of which are bugs to investigate.

## Leadership Transitions

On `LostLeadershipEvent` the scheduler drops any pending deployment and clears its in-progress flag (otherwise a new leader would wait on a deployment the dead leader was handling); the drift monitor stops its timer.

On `BecameLeaderEvent` the scheduler is bootstrapped from two sides:

- All-replica components that maintain state replay their last event so the new leader's scheduler doesn't have to wait. Currently that's `HAProxyPodsDiscoveredEvent` (from `pkg/controller/discovery`) and `ConfigValidatedEvent` (from `pkg/controller/configchange`). Grep for `leadership.NewStateReplayer[` to see the canonical list.
- Neither `TemplateRenderedEvent` nor `ValidationCompletedEvent` is replayed — both are published by the leader-only `reconciler.Coordinator` from inside `Pipeline.Execute` (ADR-0001), so they only exist on the leader to begin with. Instead, the reconciler triggers a fresh reconciliation on `BecameLeaderEvent` (see `pkg/controller/reconciler/reconciler.go:handleBecameLeader`), which produces fresh render+validate events rather than stale replays. The new leader's scheduler then assembles all three inputs naturally.

See `pkg/controller/LEADER_ONLY_COMPONENTS.md` for the full replay/clear contract every leader-only component implements.

## See Also

- [`pkg/dataplane`](../../dataplane/) — the `Client.Sync` call that the executor drives
- [`pkg/controller/discovery`](../discovery/) — publishes `HAProxyPodsDiscoveredEvent`
- [`pkg/controller/reconciler`](../reconciler/) — leader-only `Coordinator` that publishes `TemplateRenderedEvent` and `ValidationCompletedEvent` from inside `Pipeline.Execute` (the synchronous renderer + validator services live in [`pkg/controller/renderer`](../renderer/) and [`pkg/controller/validation`](../validation/), but are called directly, not subscribed to)
- [`pkg/controller/leadership`](../leadership/) — the gating helper these components use
- `pkg/controller/LEADER_ONLY_COMPONENTS.md` — leadership-transition patterns
- `docs/site/docs/operations/high-availability.md` — user-facing view of the leader-only deployment split

## License

Apache-2.0 — see root `LICENSE`.
