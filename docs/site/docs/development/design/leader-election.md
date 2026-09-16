# Leader election

HAPTIC runs multiple controller replicas for high availability. This page explains the mechanism: which components run on every replica, which run only on the leader, and how a new leader starts with warm state. Operator-facing setup, tuning, and troubleshooting live in [High Availability](../../operations/high-availability.md).

## Why only the leader deploys

The controller pushes configuration to each HAProxy pod's HAPTIC agent. Multiple replicas doing that in parallel without coordination would cause:

1. **Resource waste**: multiple replicas sending identical applies
2. **Potential conflicts**: race conditions when multiple controllers push updates simultaneously
3. **Unnecessary HAProxy reloads**: multiple deployments of the same configuration

All replicas, however, do useful work:

- Watch Kubernetes resources, keeping a hot cache for failover
- Handle admission webhook requests, so the webhook stays available through a failover

Every replica renders, but only the leader's render goes anywhere: the leader-only Coordinator drives the synchronous render pipeline and hands the result to the RenderGate (HAProxy's own check) and the deployment components, while the all-replica Warmer runs the same render on a follower purely to keep its incremental graph warm and discards the output. A new leader's first reconciliation is therefore a warm render, not a cold one. Only **deployment operations** strictly need exclusivity, but co-locating rendering with deployment keeps the pipeline synchronous and simple. A new leader starts the gate optimistic: the agents' own last-known-good file sets already protect the fleet.

## Lease-based election

HAPTIC uses `k8s.io/client-go/tools/leaderelection` with a
`coordination.k8s.io` Lease lock in the controller namespace.

### Timing defaults

The defaults applied by `pkg/core/config` (used unless the CRD's `spec.controller.leaderElection` overrides them):

```go
LeaderElectionConfig{
    LeaseDuration: 30 * time.Second, // DefaultLeaderElectionLeaseDuration
    RenewDeadline: 20 * time.Second, // DefaultLeaderElectionRenewDeadline
    RetryPeriod:   5 * time.Second,  // DefaultLeaderElectionRetryPeriod
    // ReleaseOnCancel is enabled by the controller during graceful shutdown
}
```

The renewal deadline allows brief API-server or CPU stalls. Losing the lease
stops the leader-only components and restarts election; stores and admission
validators keep running. After a crash, election also depends on lease expiry,
retry jitter, and API latency. Voluntary handoffs release the lease immediately.

Clock-rate tolerance is approximately `LeaseDuration / RenewDeadline`, or `1.5`
with these defaults. This concerns clock speed, not the difference between clock
readings. See [Timing parameters](../../operations/high-availability.md#timing-parameters)
for tuning and the client-go contract.

## Component classification

The actual classification lives in `pkg/controller/reconciliation.go` (search for `registerLifecycleComponents`, which registers all-replica components via `reg.Register(c, false)` and leader-only ones via `reg.Register(c, true)`); this section reflects that registration list.

**All replicas run** (components that write to Kubernetes check leadership):

- ConfigLoader (`pkg/controller/configloader`) — Parses `HAProxyTemplateConfig` CRD updates from a SingleWatcher
- CredentialsLoader (`pkg/controller/credentialsloader`) — Parses credentials Secret updates from a SingleWatcher
- ResourceWatcher (`pkg/controller/resourcewatcher`) — Watches Kubernetes resources (Ingress, Service, etc.)
- Reconciler (`pkg/controller/reconciler`) — Publishes `ReconciliationTriggeredEvent` without adding a debounce timer
- Discovery (`pkg/controller/discovery`) — Discovers HAProxy pod endpoints; caches `HAProxyPodsDiscoveredEvent` for replay
- HTTPStore (`pkg/controller/httpstore`) — Periodic HTTP refresh + two-version cache for content used in templates
- ProposalValidator (`pkg/controller/proposalvalidator`) — Speculative render+validate driven by HTTPStore (async) and DryRunValidator (sync)
- StatusApplier (`pkg/controller/statusapplier`) — Applies template-driven status patches via Server-Side Apply (SSA) (only the leader actually writes; followers cache state for takeover)
- ResourceApplier (`pkg/controller/resourceapplier`) — Reconciles `spec.k8sResources`-declared resources via Server-Side Apply with field manager `haptic` (all-replica subscriber; only the leader writes)
- EventEmitter (`pkg/controller/eventemitter`) — The leader emits Kubernetes Events requested through `recordEvent()`
- Validators (`pkg/controller/validator`) — Basic / Template / JSONPath validators participating in the config-validation scatter-gather
- DryRunValidator (`pkg/controller/dryrunvalidator`) — Bridges admission-webhook requests into the proposal validator
- Commentator (`pkg/controller/commentator`) — Logs events for observability
- Metrics (`pkg/controller/metrics`) — Records Prometheus metrics
- StateCache (`pkg/controller/statecache.go`) — Maintains live state snapshot for debug introspection
- DebugServer (`pkg/introspection`) — Serves `/debug/vars` and `/debug/pprof` endpoints

The renderer **isn't** a registered component. It lives in `pkg/controller/renderer` as the synchronous `RenderService` that the leader-only Coordinator drives via `pkg/controller/pipeline`. On a follower the all-replica Warmer (`pkg/controller/warmer`) drives the same service and pipeline for each trigger, committing the render for its incremental graph and publishing nothing else.

**Leader-only components** (lifecycle registry's `LeaderOnly(...)` group; only constructed and started while leadership is held, torn down on `LostLeadershipEvent`):

- **Coordinator** (`pkg/controller/reconciler`) — Drives the render pipeline (calls `Pipeline.Execute` which in turn calls `RenderService.Render`)
- **RenderGate** (`pkg/controller/rendergate`) — Runs `haproxy -c -dr` asynchronously for plans without a cached verdict, reverts the pods that took a refused plan without loading it, and holds later renders until one passes
- **Deployer** (`pkg/controller/deployer`) — Sends every HAProxy pod its apply in parallel via `pkg/dataplane/agent/client`
- **DeploymentScheduler** (`pkg/controller/deployer`) — Rate-limits and queues deployments; coalesces back-to-back deployment requests via `pkg/controller/coalesce`
- **DriftPreventionMonitor** (`pkg/controller/deployer`) — Periodic redeploy when nothing has changed for `driftPreventionInterval`, so a pod whose file tree drifted gets the controller's last-known-good set back
- **ConfigPublisher** (`pkg/controller/configpublisher`) — Publishes rendered config + per-pod status as `HAProxyCfg` / `HAProxyMapFile` / `HAProxyGeneralFile` / `HAProxyCRTListFile` CRDs
- **StatusUpdater** (`pkg/controller/configchange`) — Writes validation results back onto the `HAProxyTemplateConfig` CRD's status subresource

## The `LeaderElector` component

**Package**: `pkg/controller/leaderelection/`

**Responsibilities**:

- Create and manage the Lease lock in the controller namespace
- Use the pod name as unique identity (via the `POD_NAME` env var)
- Publish leader election events to the EventBus
- Handle graceful leadership release on shutdown

The adapter wraps client-go's callbacks to publish events *before* invoking the user-supplied callback (see `pkg/controller/leaderelection/component.go`); a sketch of the publish side:

```go
// Inside the event-adapter's wrapped callbacks (real signatures):
OnStartedLeading: func(ctx context.Context) {
    e.eventBus.Publish(events.NewBecameLeaderEvent(identity))
    // then invoke the user OnStartedLeading
}

OnStoppedLeading: func() {
    e.eventBus.Publish(events.NewLostLeadershipEvent(identity, reason))
    // then invoke the user OnStoppedLeading
}

OnNewLeader: func(observed string) {
    e.eventBus.Publish(events.NewNewLeaderObservedEvent(observed, observed == identity))
}
```

## Events

Leader election events live in `pkg/controller/events/leader.go`:

```go
// LeaderElectionStartedEvent is published when leader election begins
type LeaderElectionStartedEvent struct {
    Identity       string
    LeaseName      string
    LeaseNamespace string
    timestamped    // shared mixin: provides Timestamp() time.Time
}

// BecameLeaderEvent is published when this replica becomes leader
type BecameLeaderEvent struct {
    Identity string
    timestamped
}

// LostLeadershipEvent is published when this replica loses leadership
type LostLeadershipEvent struct {
    Identity string
    Reason   string  // graceful_shutdown, lease_expired, etc.
    timestamped
}

// NewLeaderObservedEvent is published when a new leader is observed
type NewLeaderObservedEvent struct {
    NewLeaderIdentity string
    IsSelf            bool  // true if this replica is the new leader
    timestamped
}
```

`Timestamp()` is supplied by the embedded `timestamped` mixin, not by an exported field — so `evt.Timestamp` in code is a method call, not a struct read. There is no `PreviousLeader` field on `NewLeaderObservedEvent`; the adapter only knows the *new* leader's identity.

The Commentator logs all transitions, Metrics tracks leadership duration and transition count (`haptic_leader_election_is_leader`, `haptic_leader_election_transitions_total`, `haptic_leader_election_time_as_leader_seconds_total` — reference and alerting in [Monitoring](../../operations/monitoring.md#leader-election-metrics)), and the debug server exposes lease status under `/debug/vars`.

## Startup and leadership transitions

All-replica components subscribe before `EventBus.Start()` releases buffered
events. Leader-only components subscribe when their leadership term starts;
state replay supplies the inputs they need. See
[Sequence diagrams](./sequence-diagrams.md) for staged initialization.

**Becoming leader.** On `BecameLeaderEvent`, the leader-only components start their goroutines and subscribe via `SubscribeTypesLeaderOnly` (which suppresses the late-subscription warning that normally guards against missed events). They don't start cold: all-replica components cache their latest state and replay it to the new leader — Discovery re-publishes the discovered HAProxy pod set for the new leader's DeploymentScheduler, the StatusApplier clears its checksum cache, and the Reconciler treats `BecameLeaderEvent` as an immediate trigger so the new leader produces a fresh render right away. Replaying cached state lets the new leader reconcile without waiting for another resource change.

**Losing leadership.** On `LostLeadershipEvent`, the lifecycle registry cancels the leader-only components' context and tears them down. The replica keeps watching resources and serving webhooks as a follower.

**Graceful transition** (rolling update, voluntary handoff):

1. Old leader releases the lease on shutdown (`ReleaseOnCancel`) and stops deployment components
2. A follower acquires the released lease on its next successful election attempt
3. New leader starts deployment components with hot cache and replayed state → immediate reconciliation

After a leader crashes, followers wait for lease expiry before taking over. Election retries, API latency, and component startup also contribute to recovery time. See [High availability](../../operations/high-availability.md#troubleshooting).

## Testing

Pure-component tests live in `pkg/k8s/leaderelection/elector_test.go`; the event-adapter wrapping is covered by `pkg/controller/leaderelection/component_test.go`. Multi-replica behaviour (two replicas, failover, disabled mode) runs against a real kind cluster in `tests/acceptance/leader_election_test.go`.

## Alternatives considered

- **Single active replica with PodDisruptionBudget** — rejected: doesn't provide HA, just prevents voluntary disruptions
- **Active-active with distributed locking per HAProxy instance** — rejected: more complex, potential deadlocks, not idiomatic for Kubernetes
- **External coordination (etcd, Consul)** — rejected: adds operational complexity, the Kubernetes API is sufficient
- **Config generation only (no deployment)** — rejected: requires an external system to deploy, doesn't solve the core problem

## References

- [Kubernetes client-go Leader Election](https://pkg.go.dev/k8s.io/client-go/tools/leaderelection)
- [Kubernetes Coordinated Leader Election (beta)](https://kubernetes.io/docs/concepts/cluster-administration/coordinated-leader-election/)
- [Official client-go example](https://github.com/kubernetes/client-go/tree/master/examples/leader-election)
- [Leader Election in Kubernetes Controllers (blog post)](https://sklar.rocks/kubernetes-leader-election/)
