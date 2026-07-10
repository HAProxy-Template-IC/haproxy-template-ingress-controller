# Leader Election for High Availability

## Overview

This document describes the leader election system for HAPTIC, which enables running multiple controller replicas for high availability while preventing conflicting updates to HAProxy instances.

## Problem Statement

The controller is a Kubernetes operator that pushes configuration to HAProxy via the Dataplane API. Running multiple replicas in parallel without coordination would cause:

1. **Resource waste**: Multiple replicas performing identical dataplane API calls
2. **Potential conflicts**: Race conditions when multiple controllers push updates simultaneously
3. **Unnecessary HAProxy reloads**: Multiple deployments of the same configuration

However, all replicas should:

- Watch Kubernetes resources (to maintain hot cache for failover)
- Render templates (to have configurations ready)
- Validate configurations (to share the workload)
- Handle webhook requests (for high availability)

Only **deployment operations** (pushing configurations to HAProxy Dataplane API) need exclusivity.

## State-of-the-Art Solution

Use `k8s.io/client-go/tools/leaderelection` with Lease-based resource locks, the industry standard for Kubernetes operator high availability.

### Why Lease-based Locks?

- **Lower overhead**: Leases create less watch traffic than ConfigMaps or Endpoints
- **Purpose-built**: Designed specifically for leader election
- **Reliable**: Used by core Kubernetes components (kube-controller-manager, kube-scheduler)
- **Clock skew tolerant**: Configurable tolerance for node clock differences

### Default Configuration

The defaults applied by `pkg/core/config` (used unless the CRD's
`spec.controller.leaderElection` overrides them):

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

## Architecture Changes

### Component Classification

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

### New Component: LeaderElector

**Package**: `pkg/controller/leaderelection/`

**Responsibilities**:

- Create and manage Lease lock in controller namespace
- Use pod name as unique identity (via POD_NAME env var)
- Publish leader election events to EventBus
- Handle graceful leadership release on shutdown

**Event integration**:

The real adapter wraps callbacks to publish events *before* invoking the user-supplied callback (see `pkg/controller/leaderelection/component.go`); below is a sketch of the publish side:

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

### New Events

**Leader election events** (`pkg/controller/events/leader.go`):

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

These events enable:

- **Observability**: Commentator logs all transitions
- **Metrics**: Track leadership duration, transition count
- **Debugging**: Understand which replica is active

### Controller Startup Changes

**Startup sequence** (`pkg/controller/iteration.go` + `pkg/controller/infrastructure.go`):

```
Stage 1: Config Management Components
  - ConfigLoader, CredentialsLoader (all replicas)
  - BasicValidator + TemplateValidator + JSONPathValidator
    (all replicas, scatter-gather over ConfigValidationRequest)
  - ConfigChangeHandler (orchestrates the scatter-gather)

Stage 2: Wait for Valid Config
  - All replicas block here

Stage 3: Resource Watchers
  - Create ResourceWatcher (all replicas)
  - Start IndexSynchronizationTracker (all replicas)

Stage 4: Wait for Index Sync
  - All replicas block here

Stage 5: Reconciliation Components (all components subscribe in their
constructors before EventBus.Start() is called)
  - Reconciler (all replicas)
  - Coordinator (LEADER ONLY) — runs the synchronous render+validate Pipeline
  - Discovery, HTTPStore, ProposalValidator (all replicas)
  - DeploymentScheduler, Deployer, DriftPreventionMonitor,
    ConfigPublisher (LEADER ONLY)
  - StatusApplier (all replicas — subscribes to pipeline lifecycle events carrying status patches; only the leader applies writes)
  - Metrics, Commentator (all replicas)
  → EventBus.Start() runs here, releasing the pre-start buffer to every
    subscriber that registered during construction.

Stage 6: Leader Election (lease-backed elector started after EventBus.Start();
  on BecameLeaderEvent the LEADER ONLY components above start their goroutines
  and subscribe via SubscribeTypesLeaderOnly).

Stage 7: Webhook Validation (only when at least one watched resource has
enableValidationWebhook: true)
  - Webhook HTTPS server (all replicas)
  - DryRunValidator was already created in Stage 5 so its subscriptions
    were in place before EventBus.Start().

Stage 8: Debug & Metrics Wiring
  - Register debug variables with the introspection server
  - Start the pre-created EventBuffer goroutine
  - Swap the bootstrap health checker for one backed by the lifecycle registry
```

### Conditional Component Startup

**Implementation pattern**:

```go
// Create separate context for leader-only components
leaderCtx, leaderCancel := context.WithCancel(iterCtx)

// Track leader-only components
var leaderComponents struct {
    sync.Mutex
    deployer            *deployer.Component
    deploymentScheduler *deployer.DeploymentScheduler
    driftMonitor        *deployer.DriftPreventionMonitor
    cancel              context.CancelFunc
}

// Leadership callbacks
OnStartedLeading: func(ctx context.Context) {
    logger.Info("Became leader, starting deployment components")

    leaderComponents.Lock()
    defer leaderComponents.Unlock()

    // Create fresh context for leader components
    leaderComponents.cancel = leaderCancel

    // Create and start leader-only components (real signatures take more knobs;
    // see pkg/controller/deployer for the production constructors).
    leaderComponents.deployer = deployer.New(bus, logger, syncOpts)
    leaderComponents.deploymentScheduler = deployer.NewDeploymentScheduler(bus, logger, minInterval, deploymentTimeout)
    leaderComponents.driftMonitor = deployer.NewDriftPreventionMonitor(bus, logger, driftInterval)

    go leaderComponents.deployer.Start(leaderCtx)
    go leaderComponents.deploymentScheduler.Start(leaderCtx)
    go leaderComponents.driftMonitor.Start(leaderCtx)
}

OnStoppedLeading: func() {
    logger.Warn("Lost leadership, stopping deployment components")

    leaderComponents.Lock()
    defer leaderComponents.Unlock()

    if leaderComponents.cancel != nil {
        leaderComponents.cancel()
        leaderComponents.cancel = nil
    }
}
```

**Graceful transition**:

1. Old leader loses lease → stops deployment components
2. Brief pause (lease expiry time)
3. New leader acquires lease → starts deployment components
4. New leader has hot cache and rendered config → immediate reconciliation

## Configuration

**New configuration section** (`LeaderElectionConfig` in `pkg/core/config/types.go`, defaults in `pkg/core/config/defaults.go`):

```yaml
controller:
  # ... existing fields ...

  leaderElection:
    enabled: true   # Enable leader election (default: true)
    leaseName: ""   # Empty = controller default "haptic-leader"; Helm rewrites empty to the release fullname
    leaseDuration: 30s   # chart default; 2x client-go's convention for starvation headroom
    renewDeadline: 20s   # must be < leaseDuration
    retryPeriod: 5s      # must be < renewDeadline
```

**Backwards compatibility**:

- `enabled: false` → Run without leader election (single replica mode)
- Existing single-replica deployments work unchanged

## RBAC Requirements

**New permissions** (`charts/haptic/templates/clusterrole.yaml`, bound by `clusterrolebinding.yaml`):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haptic
rules:
  # ... existing rules ...

  # Leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]
```

The controller creates a Lease in its own namespace (not cluster-wide).

## Deployment Changes

**Environment variables** (`charts/haptic/templates/deployment.yaml`):

```yaml
spec:
  template:
    spec:
      containers:
      - name: controller
        env:
        # ... existing env vars ...

        # Pod identity for leader election
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
```

**Multiple replicas**:

```yaml
spec:
  replicas: 2  # Change from 1 to 2+ for HA
```

**Resource adjustments**:

No changes needed - non-leader replicas consume similar resources since they perform all read-only work.

## Observability

### Metrics

**New Prometheus metrics** (`pkg/controller/metrics/metrics.go`):

```go
// haptic_leader_election_transitions_total
// Counter of leadership changes (acquire + lose)
haptic_leader_election_transitions_total counter

// haptic_leader_election_is_leader
// Gauge indicating current leadership status (1=leader, 0=follower)
haptic_leader_election_is_leader gauge

// haptic_leader_election_time_as_leader_seconds_total
// Counter of cumulative seconds spent as leader
haptic_leader_election_time_as_leader_seconds_total counter
```

**Usage**:

- Alert on frequent transitions (indicates instability)
- Dashboard showing current leader identity
- Track leadership duration distribution

### Logging

**Commentator enhancements** (`pkg/controller/commentator/commentator.go`):

```go
case LeaderElectionStartedEvent:
    c.logger.Info("leader election started",
        "identity", e.Identity,
        "lease", e.LeaseName,
        "namespace", e.LeaseNamespace)

case BecameLeaderEvent:
    c.logger.Info("became leader",
        "identity", e.Identity)

case LostLeadershipEvent:
    c.logger.Warn("lost leadership",
        "identity", e.Identity,
        "reason", e.Reason)

case *NewLeaderObservedEvent:
    c.logger.Info("new leader observed",
        "leader_identity", e.NewLeaderIdentity,
        "is_self", e.IsSelf)
```

### Debug Endpoints

**Lease status** (via debug server):

```json
GET /debug/vars

{
  "leader_election": {
    "enabled": true,
    "is_leader": true,
    "identity": "haptic-7f8d9c5b-abc123",
    "lease_name": "my-controller",
    "lease_holder": "haptic-7f8d9c5b-abc123",
    "time_as_leader": "45m32s",
    "transitions": 2
  }
}
```

## Testing Strategy

### Unit Tests

**LeaderElector tests** — pure-component tests live in `pkg/k8s/leaderelection/elector_test.go`; the event-adapter wrapping is covered by `pkg/controller/leaderelection/component_test.go`:

```go
// Test leader election configuration
func TestLeaderElector_Config(t *testing.T)

// Test event publishing on leadership changes
func TestLeaderElector_EventPublishing(t *testing.T)

// Test graceful shutdown
func TestLeaderElector_GracefulShutdown(t *testing.T)
```

### Integration Tests

**Multi-replica tests** (`tests/acceptance/leader_election_test.go` — these run against a real Kind cluster, not the unit-style fixtures in `tests/integration/`):

```go
// Deploy 2 replicas, verify leader election behavior
func TestLeaderElection_TwoReplicas(t *testing.T)

// Kill leader pod, verify follower takes over
func TestLeaderElection_Failover(t *testing.T)

// Verify behavior when leader election is disabled
func TestLeaderElection_DisabledMode(t *testing.T)
```

**Test setup**:

- Use kind cluster with multi-node setup
- Deploy controller with 3 replicas
- Create test Ingress resources
- Verify deployment behavior
- Simulate pod failures

### Manual Testing

**Verification steps**:

```bash
# Set your Helm release name once; both the deployment and the lease are
# derived from the chart fullname (deployment: "$RELEASE-controller", lease:
# "$RELEASE"). These short forms hold because the release name contains
# "haptic" — otherwise the chart prefixes names as "<release>-haptic".
RELEASE=haptic

# Deploy with 3 replicas
kubectl scale deployment "$RELEASE-controller" --replicas=3

# Check lease status
kubectl get lease -n haptic "$RELEASE" -o yaml

# Verify leader via metrics
kubectl port-forward deployment/"$RELEASE-controller" 9090:9090
curl http://localhost:9090/metrics | grep haptic_leader_election_is_leader

# Check logs for leadership events
kubectl logs -l app.kubernetes.io/name=haptic --tail=100 | grep -i leader

# Simulate failover
kubectl delete pod <leader-pod>

# Verify new leader takes over
watch kubectl get lease -n haptic "$RELEASE"

# Check HAProxy configs only deployed once per change
kubectl logs -l app.kubernetes.io/name=haptic | grep "deployment completed"
```

## Failure Scenarios

### Leader Pod Crashes

**Behavior**:

1. Leader lease expires (30s after last renewal)
2. Followers detect expired lease
3. First follower to update lease becomes new leader
4. New leader starts deployment components
5. Reconciliation continues from hot cache

**Downtime**: ~30-35 seconds (LeaseDuration + startup time)

### Network Partition

**Scenario**: Leader pod loses connectivity to Kubernetes API

**Behavior**:

1. Leader cannot renew lease
2. After RenewDeadline (20s), leader voluntarily releases leadership
3. Leader stops deployment components
4. Connected replica acquires lease
5. System continues with new leader

**Protection**: Split-brain prevented by Kubernetes API acting as coordination point

### Clock Skew

**Scenario**: Nodes have different clock speeds

**Tolerance**: Configured ratio of LeaseDuration/RenewDeadline

- With the default 30s/20s: Tolerates a 1.5× clock-speed difference between nodes
- If exceeded: May experience frequent leadership changes

**Mitigation**: Run NTP on cluster nodes (Kubernetes best practice)

### All Replicas Down

**Behavior**:

1. Lease expires
2. No deployments occur (expected behavior)
3. HAProxy continues serving with last known configuration
4. When replica starts, acquires lease and reconciles

**Impact**: No new configuration updates until controller recovers

## Status

Leader election is fully implemented and enabled by default in the Helm chart (`controller.config.controller.leaderElection.enabled: true`, 2 replicas by default). The opt-in/opt-out switch lives on the CRD; the chart RBAC ships with the lease permissions required. Operator-facing setup, tuning, and troubleshooting live in [High Availability](../../operations/high-availability.md).

## Alternatives Considered

### Single Active Replica with Pod Disruption Budget

**Rejected**: Doesn't provide HA, just prevents voluntary disruptions

### Active-Active with Distributed Locking per HAProxy Instance

**Rejected**: More complex, potential deadlocks, not idiomatic for Kubernetes

### External Coordination (etcd, Consul)

**Rejected**: Adds operational complexity, Kubernetes API sufficient

### Config Generation Only (No Deployment)

**Rejected**: Requires external system to deploy, doesn't solve core problem

## References

- [Kubernetes client-go Leader Election](https://pkg.go.dev/k8s.io/client-go/tools/leaderelection)
- [Kubernetes Coordinated Leader Election (beta)](https://kubernetes.io/docs/concepts/cluster-administration/coordinated-leader-election/)
- [Official client-go example](https://github.com/kubernetes/client-go/tree/master/examples/leader-election)
- [Leader Election in Kubernetes Controllers (blog post)](https://sklar.rocks/kubernetes-leader-election/)
