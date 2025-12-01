# Leader Election for High Availability

## Overview

This document describes the leader election system for the HAProxy Template Ingress Controller, which enables running multiple controller replicas for high availability while preventing conflicting updates to HAProxy instances.

## Problem Statement

The controller currently runs as a single instance. Running multiple replicas without coordination would cause:

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

### Recommended Configuration

```go
LeaderElectionConfig{
    LeaseDuration: 60 * time.Second,  // How long leader holds lock
    RenewDeadline: 15 * time.Second,  // Renewal deadline before losing leadership
    RetryPeriod:   5 * time.Second,   // Interval between renewal attempts
    ReleaseOnCancel: true,            // Cleanup on graceful shutdown
}
```

**Tolerance formula**: `LeaseDuration / RenewDeadline = clock skew tolerance ratio`

With 60s/15s settings, the system tolerates nodes progressing 4x faster than others.

## Architecture Changes

### Component Classification

**All replicas run** (read-only or validation operations):
- ConfigWatcher - Monitors ConfigMap changes
- CredentialsLoader - Monitors Secret changes
- ResourceWatcher - Watches Kubernetes resources (Ingress, Service, etc.)
- Reconciler - Debounces changes and triggers reconciliation
- Renderer - Generates HAProxy configurations from templates
- HAProxyValidator - Validates generated configurations
- Executor - Orchestrates reconciliation workflow
- Discovery - Discovers HAProxy pod endpoints
- ConfigValidators - Validates controller configuration
- WebhookValidators - Validates admission webhook requests
- Commentator - Logs events for observability
- Metrics - Records Prometheus metrics
- StateCache - Maintains debug state

**Leader-only components** (write operations to dataplane API):
- **Deployer** - Deploys configurations to HAProxy instances
- **DeploymentScheduler** - Rate-limits and queues deployments
- **DriftMonitor** - Monitors and corrects configuration drift

### New Component: LeaderElector

**Package**: `pkg/controller/leaderelection/`

**Responsibilities**:
- Create and manage Lease lock in controller namespace
- Use pod name as unique identity (via POD_NAME env var)
- Publish leader election events to EventBus
- Provide `IsLeader()` method for status queries
- Handle graceful leadership release on shutdown

**Event integration**:
```go
type LeaderElector struct {
    eventBus *events.EventBus
    elector  *leaderelection.LeaderElector
    isLeader atomic.Bool
}

// Callbacks publish events
OnStartedLeading: func(ctx context.Context) {
    e.isLeader.Store(true)
    e.eventBus.Publish(events.NewBecameLeaderEvent())
}

OnStoppedLeading: func() {
    e.isLeader.Store(false)
    e.eventBus.Publish(events.NewLostLeadershipEvent())
}

OnNewLeader: func(identity string) {
    e.eventBus.Publish(events.NewNewLeaderObservedEvent(identity))
}
```

### New Events

**Leader election events** (`pkg/controller/events/types.go`):

```go
// LeaderElectionStartedEvent is published when leader election begins
type LeaderElectionStartedEvent struct {
    Identity      string
    LeaseName     string
    LeaseNamespace string
}

// BecameLeaderEvent is published when this replica becomes leader
type BecameLeaderEvent struct {
    Identity   string
    Timestamp  time.Time
}

// LostLeadershipEvent is published when this replica loses leadership
type LostLeadershipEvent struct {
    Identity   string
    Timestamp  time.Time
    Reason     string  // graceful_shutdown, lease_expired, etc.
}

// NewLeaderObservedEvent is published when a new leader is observed
type NewLeaderObservedEvent struct {
    NewLeaderIdentity string
    PreviousLeader    string
    Timestamp         time.Time
}
```

These events enable:
- **Observability**: Commentator logs all transitions
- **Metrics**: Track leadership duration, transition count
- **Debugging**: Understand which replica is active

### Controller Startup Changes

**Modified startup sequence** (`pkg/controller/controller.go`):

```
Stage 0: Leader Election Initialization (NEW)
  - Read POD_NAME from environment
  - Create LeaderElector with Lease lock
  - Start leader election loop in background goroutine
  - Continue startup (don't block on becoming leader)

Stage 1: Config Management Components
  - ConfigWatcher (all replicas)
  - ConfigValidator (all replicas)
  - EventBus.Start()

Stage 2: Wait for Valid Config
  - All replicas block here

Stage 3: Resource Watchers
  - Create ResourceWatcher (all replicas)
  - Start IndexSynchronizationTracker (all replicas)

Stage 4: Wait for Index Sync
  - All replicas block here

Stage 5: Reconciliation Components
  - Reconciler (all replicas)
  - Renderer (all replicas)
  - HAProxyValidator (all replicas)
  - Executor (all replicas)
  - Discovery (all replicas)
  - Deployer (LEADER ONLY - NEW)
  - DeploymentScheduler (LEADER ONLY - NEW)
  - DriftMonitor (LEADER ONLY - NEW)

Stage 6: Webhook Validation
  - Webhook component (all replicas)
  - DryRunValidator (all replicas)

Stage 7: Debug Infrastructure
  - Debug server (all replicas)
  - Metrics server (all replicas)
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

    // Create and start leader-only components
    leaderComponents.deployer = deployer.New(bus, logger)
    leaderComponents.deploymentScheduler = deployer.NewDeploymentScheduler(bus, logger, minInterval)
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

**New configuration section** (`pkg/core/config/config.go`):

```yaml
controller:
  # ... existing fields ...

  leaderElection:
    enabled: true  # Enable leader election (default: true)
    leaseName: "haproxy-template-ic-leader"
    leaseDuration: 60s
    renewDeadline: 15s
    retryPeriod: 5s
```

**Backwards compatibility**:
- `enabled: false` → Run without leader election (single replica mode)
- Existing single-replica deployments work unchanged

## RBAC Requirements

**New permissions** (`charts/haproxy-template-ic/templates/rbac.yaml`):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haproxy-template-ic
rules:
  # ... existing rules ...

  # Leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]
```

The controller creates a Lease in its own namespace (not cluster-wide).

## Deployment Changes

**Environment variables** (`charts/haproxy-template-ic/templates/deployment.yaml`):

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
// controller_leader_transitions_total
// Counter of leadership changes (acquire + lose)
controller_leader_transitions_total counter

// controller_is_leader
// Gauge indicating current leadership status (1=leader, 0=follower)
controller_is_leader{pod="<pod-name>"} gauge

// controller_leader_election_duration_seconds
// Histogram of time to acquire leadership after startup
controller_leader_election_duration_seconds histogram

// controller_time_as_leader_seconds
// Counter of cumulative seconds spent as leader
controller_time_as_leader_seconds counter
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

case NewLeaderObservedEvent:
    c.logger.Info("new leader observed",
        "new_leader", e.NewLeaderIdentity,
        "previous_leader", e.PreviousLeader)
```

### Debug Endpoints

**Lease status** (via debug server):

```json
GET /debug/vars

{
  "leader_election": {
    "enabled": true,
    "is_leader": true,
    "identity": "haproxy-template-ic-7f8d9c5b-abc123",
    "lease_name": "haproxy-template-ic-leader",
    "lease_holder": "haproxy-template-ic-7f8d9c5b-abc123",
    "time_as_leader": "45m32s",
    "transitions": 2
  }
}
```

## Testing Strategy

### Unit Tests

**LeaderElector tests** (`pkg/controller/leaderelection/elector_test.go`):

```go
// Test leader election configuration
func TestLeaderElector_Config(t *testing.T)

// Test event publishing on leadership changes
func TestLeaderElector_EventPublishing(t *testing.T)

// Test IsLeader() method accuracy
func TestLeaderElector_IsLeaderStatus(t *testing.T)

// Test graceful shutdown
func TestLeaderElector_GracefulShutdown(t *testing.T)
```

### Integration Tests

**Multi-replica tests** (`tests/integration/leader_election_test.go`):

```go
// Deploy 2 replicas, verify only one deploys configs
func TestLeaderElection_OnlyLeaderDeploys(t *testing.T)

// Kill leader pod, verify follower takes over
func TestLeaderElection_Failover(t *testing.T)

// Verify both replicas watch resources
func TestLeaderElection_BothReplicasWatchResources(t *testing.T)

// Verify both replicas render configs
func TestLeaderElection_BothReplicasRenderConfigs(t *testing.T)
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
# Deploy with 3 replicas
kubectl scale deployment haproxy-template-ic --replicas=3

# Check lease status
kubectl get lease -n haproxy-system haproxy-template-ic-leader -o yaml

# Verify leader via metrics
kubectl port-forward deployment/haproxy-template-ic 9090:9090
curl http://localhost:9090/metrics | grep controller_is_leader

# Check logs for leadership events
kubectl logs -l app=haproxy-template-ic --tail=100 | grep -i leader

# Simulate failover
kubectl delete pod <leader-pod>

# Verify new leader takes over
watch kubectl get lease -n haproxy-system haproxy-template-ic-leader

# Check HAProxy configs only deployed once per change
kubectl logs -l app=haproxy-template-ic | grep "deployment completed"
```

## Failure Scenarios

### Leader Pod Crashes

**Behavior**:
1. Leader lease expires (15s after last renewal)
2. Followers detect expired lease
3. First follower to update lease becomes new leader
4. New leader starts deployment components
5. Reconciliation continues from hot cache

**Downtime**: ~15-20 seconds (RenewDeadline + startup time)

### Network Partition

**Scenario**: Leader pod loses connectivity to Kubernetes API

**Behavior**:
1. Leader cannot renew lease
2. After RenewDeadline (15s), leader voluntarily releases leadership
3. Leader stops deployment components
4. Connected replica acquires lease
5. System continues with new leader

**Protection**: Split-brain prevented by Kubernetes API acting as coordination point

### Clock Skew

**Scenario**: Nodes have different clock speeds

**Tolerance**: Configured ratio of LeaseDuration/RenewDeadline
- With 60s/15s: Tolerates 4x clock speed difference
- If exceeded: May experience frequent leadership changes

**Mitigation**: Run NTP on cluster nodes (Kubernetes best practice)

### All Replicas Down

**Behavior**:
1. Lease expires
2. No deployments occur (expected behavior)
3. HAProxy continues serving with last known configuration
4. When replica starts, acquires lease and reconciles

**Impact**: No new configuration updates until controller recovers

## Migration Path

### Phase 1: Code Implementation
1. Implement LeaderElector package
2. Add leader election events
3. Modify controller startup for conditional components
4. Add configuration options
5. Update RBAC manifests

### Phase 2: Testing
1. Unit tests for LeaderElector
2. Integration tests with multi-replica setup
3. Chaos testing (kill leaders, network partitions)
4. Performance testing (ensure no regression)

### Phase 3: Documentation
1. Update deployment guide for HA setup
2. Document troubleshooting procedures
3. Update architecture diagrams
4. Create runbooks for common scenarios

### Phase 4: Rollout
1. Release with `enabled: false` default
2. Document opt-in HA setup
3. Collect feedback from early adopters
4. After validation, change default to `enabled: true`

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
