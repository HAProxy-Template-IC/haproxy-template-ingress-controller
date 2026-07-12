# Leader Election

HAPTIC runs multiple controller replicas for high availability. This page explains the mechanism: which components run on every replica, which run only on the leader, and how a new leader starts with warm state. Operator-facing setup, tuning, and troubleshooting live in [High Availability](../../operations/high-availability.md).

## Why Only the Leader Deploys

The controller pushes configuration to HAProxy via the Dataplane API. Multiple replicas doing that in parallel without coordination would cause:

1. **Resource waste**: multiple replicas performing identical Dataplane API calls
2. **Potential conflicts**: race conditions when multiple controllers push updates simultaneously
3. **Unnecessary HAProxy reloads**: multiple deployments of the same configuration

All replicas, however, do useful work:

- Watch Kubernetes resources, keeping a hot cache for failover
- Handle admission webhook requests, so the webhook stays available through a failover

Rendering and config validation run only on the leader: the synchronous render-validate pipeline lives inside the leader-only Coordinator (see the component split below), and a new leader's first reconciliation produces a fresh render. Only **deployment operations** strictly need exclusivity, but co-locating rendering with deployment keeps the pipeline synchronous and simple.

## Lease-Based Election

HAPTIC uses `k8s.io/client-go/tools/leaderelection` with a `coordination.k8s.io` Lease lock — the industry standard for Kubernetes operator high availability:

- **Lower overhead**: Leases create less watch traffic than ConfigMaps or Endpoints
- **Purpose-built**: the Lease API exists exactly for coordination locks — no ConfigMap or Endpoints semantics repurposed as a lock
- **Reliable**: used by core Kubernetes components (kube-controller-manager, kube-scheduler)
- **Clock skew tolerant**: configurable tolerance for node clock differences

### Timing Defaults

The defaults applied by `pkg/core/config` (used unless the CRD's `spec.controller.leaderElection` overrides them):

```go
LeaderElectionConfig{
    LeaseDuration: 30 * time.Second, // DefaultLeaderElectionLeaseDuration
    RenewDeadline: 20 * time.Second, // DefaultLeaderElectionRenewDeadline
    RetryPeriod:   5 * time.Second,  // DefaultLeaderElectionRetryPeriod
    // ReleaseOnCancel is enabled by the controller during graceful shutdown
}
```

These are deliberately 2x the values `kube-controller-manager` and `kube-scheduler` ship with (15s/10s/2s). The renew deadline is the leader's budget for riding out apiserver unavailability or CPU starvation without losing the lease; multi-second stalls of 10s+ have been observed on loaded nodes, and a lost lease costs a full controller reinitialization before the replica can lead again (client-go's `LeaderElector.Run` returns permanently on a lost lease). The trade-off is hard-failover latency after a leader crash that never releases the lease: up to `LeaseDuration` (+ one `RetryPeriod`) instead of ~17s. Voluntary handoffs release the lease immediately and are unaffected.

**Tolerance formula**: `LeaseDuration / RenewDeadline = clock skew tolerance ratio`

With 30s/20s the system tolerates nodes progressing 1.5× faster than others. Workloads on hosts with large clock skew should override these via the CRD; controllers that need a longer warm-up after election can raise both numbers proportionally so the ratio stays close to 1.5.

## Component Classification

The actual classification lives in `pkg/controller/reconciliation.go` (search for `registerLifecycleComponents`, which registers all-replica components via `reg.Register(c, false)` and leader-only ones via `reg.Register(c, true)`); this section reflects that registration list.

**All replicas run** (read-only or validation operations):

- ConfigLoader (`pkg/controller/configloader`) — Parses `HAProxyTemplateConfig` CRD updates from a SingleWatcher
- CredentialsLoader (`pkg/controller/credentialsloader`) — Parses credentials Secret updates from a SingleWatcher
- ResourceWatcher (`pkg/controller/resourcewatcher`) — Watches Kubernetes resources (Ingress, Service, etc.)
- Reconciler (`pkg/controller/reconciler`) — Debounces changes and publishes `ReconciliationTriggeredEvent`
- Discovery (`pkg/controller/discovery`) — Discovers HAProxy pod endpoints; caches `HAProxyPodsDiscoveredEvent` for replay
- HTTPStore (`pkg/controller/httpstore`) — Periodic HTTP refresh + two-version cache for content used in templates
- ProposalValidator (`pkg/controller/proposalvalidator`) — Speculative render+validate driven by HTTPStore (async) and DryRunValidator (sync)
- StatusApplier (`pkg/controller/statusapplier`) — Applies template-driven status patches via SSA (only the leader actually writes; followers cache state to take over instantly)
- Validators (`pkg/controller/validator`) — Basic / Template / JSONPath validators participating in the config-validation scatter-gather
- DryRunValidator (`pkg/controller/dryrunvalidator`) — Bridges admission-webhook requests into the proposal validator
- Commentator (`pkg/controller/commentator`) — Logs events for observability
- Metrics (`pkg/controller/metrics`) — Records Prometheus metrics
- StateCache (`pkg/controller/statecache.go`) — Maintains live state snapshot for debug introspection
- DebugServer (`pkg/introspection`) — Serves `/debug/vars` and `/debug/pprof` endpoints

The renderer is **not** a registered component. It lives in `pkg/controller/renderer` as the synchronous `RenderService` that the leader-only Coordinator drives via `pkg/controller/pipeline`; rendering therefore runs only on the leader, even though the engine itself is a pure library.

**Leader-only components** (lifecycle registry's `LeaderOnly(...)` group; only constructed and started while leadership is held, torn down on `LostLeadershipEvent`):

- **Coordinator** (`pkg/controller/reconciler`) — Drives the render-validate pipeline (calls `Pipeline.Execute` which in turn calls `RenderService.Render`)
- **Deployer** (`pkg/controller/deployer`) — Pushes the validated config to every HAProxy endpoint in parallel via `pkg/dataplane.Client`
- **DeploymentScheduler** (`pkg/controller/deployer`) — Rate-limits and queues deployments; coalesces back-to-back deployment requests via `pkg/controller/coalesce`
- **DriftPreventionMonitor** (`pkg/controller/deployer`) — Periodic redeploy when nothing has changed for `driftPreventionInterval`, so out-of-band Dataplane API edits get overwritten by the controller's last-known-good config
- **ConfigPublisher** (`pkg/controller/configpublisher`) — Publishes rendered config + per-pod status as `HAProxyCfg` / `HAProxyMapFile` / `HAProxyGeneralFile` / `HAProxyCRTListFile` CRDs
- **StatusUpdater** (`pkg/controller/configchange`) — Writes validation results back onto the `HAProxyTemplateConfig` CRD's status subresource

## The LeaderElector Component

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

## Startup and Leadership Transitions

The controller starts in stages — components subscribe in their constructors, `EventBus.Start()` releases the pre-start buffer, and the lease-backed elector starts last. The full staged-startup walkthrough lives in [Sequence Diagrams](./sequence-diagrams.md); the leader-election-relevant part is the ordering guarantee: every component's subscriptions exist *before* the elector can publish `BecameLeaderEvent`, so no replica misses a leadership event.

**Becoming leader.** On `BecameLeaderEvent`, the leader-only components start their goroutines and subscribe via `SubscribeTypesLeaderOnly` (which suppresses the late-subscription warning that normally guards against missed events). They don't start cold: all-replica components cache their latest state and replay it to the new leader — Discovery re-publishes the discovered HAProxy pod set for the new leader's DeploymentScheduler, the StatusApplier clears its checksum cache, and the Reconciler treats `BecameLeaderEvent` as an immediate trigger so the new leader produces a fresh render right away. This bootstrap-replay pattern is what makes failover instant despite the leader-only components being constructed on demand.

**Losing leadership.** On `LostLeadershipEvent`, the lifecycle registry cancels the leader-only components' context and tears them down. The replica keeps watching resources and serving webhooks as a follower.

**Graceful transition** (rolling update, voluntary handoff):

1. Old leader releases the lease on shutdown (`ReleaseOnCancel`) and stops deployment components
2. New leader acquires the lease immediately — no lease-expiry wait
3. New leader starts deployment components with hot cache and replayed state → immediate reconciliation

A leader *crash* instead costs up to `LeaseDuration` + one `RetryPeriod` before a follower takes over; failure behaviour and recovery steps are covered in [High Availability](../../operations/high-availability.md#troubleshooting).

## Testing

Pure-component tests live in `pkg/k8s/leaderelection/elector_test.go`; the event-adapter wrapping is covered by `pkg/controller/leaderelection/component_test.go`. Multi-replica behaviour (two replicas, failover, disabled mode) runs against a real kind cluster in `tests/acceptance/leader_election_test.go`.

## Alternatives Considered

- **Single active replica with PodDisruptionBudget** — rejected: doesn't provide HA, just prevents voluntary disruptions
- **Active-active with distributed locking per HAProxy instance** — rejected: more complex, potential deadlocks, not idiomatic for Kubernetes
- **External coordination (etcd, Consul)** — rejected: adds operational complexity, the Kubernetes API is sufficient
- **Config generation only (no deployment)** — rejected: requires an external system to deploy, doesn't solve the core problem

## References

- [Kubernetes client-go Leader Election](https://pkg.go.dev/k8s.io/client-go/tools/leaderelection)
- [Kubernetes Coordinated Leader Election (beta)](https://kubernetes.io/docs/concepts/cluster-administration/coordinated-leader-election/)
- [Official client-go example](https://github.com/kubernetes/client-go/tree/master/examples/leader-election)
- [Leader Election in Kubernetes Controllers (blog post)](https://sklar.rocks/kubernetes-leader-election/)
