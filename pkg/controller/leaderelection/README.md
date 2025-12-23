# pkg/controller/leaderelection

Event adapter for leader election that wraps the pure `pkg/k8s/leaderelection` component.

## Overview

This package provides an event-driven wrapper around the pure leader election component, publishing observability events to the controller's EventBus for logging, metrics, and debugging.

## Architecture

```
Pure Component                Event Adapter
(pkg/k8s/leaderelection)     (pkg/controller/leaderelection)
         ↓                            ↓
    Elector ──────wrapped by────→ Component
  - Pure logic                  - Publishes events
  - No events                   - Observability
  - Reusable                    - Metrics
```

## Usage

```go
import (
    "context"
    "log/slog"

    "k8s.io/client-go/kubernetes"

    busevents "haptic/pkg/events"
    "haptic/pkg/controller/leaderelection"
    k8sleaderelection "haptic/pkg/k8s/leaderelection"
)

// Create pure leader election config
config := &k8sleaderelection.Config{
    Enabled:         true,
    Identity:        podName,
    LeaseName:       "my-app-leader",
    LeaseNamespace:  "default",
    LeaseDuration:   15 * time.Second,
    RenewDeadline:   10 * time.Second,
    RetryPeriod:     2 * time.Second,
    ReleaseOnCancel: true,
}

// Define callbacks for leadership transitions
callbacks := k8sleaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        log.Println("Started leading - start leader-only components")
        startLeaderOnlyComponents(ctx)
    },
    OnStoppedLeading: func() {
        log.Println("Stopped leading - stop leader-only components")
        stopLeaderOnlyComponents()
    },
    OnNewLeader: func(identity string) {
        log.Printf("New leader observed: %s", identity)
    },
}

// Create event adapter component
component, err := leaderelection.New(
    config,
    clientset,
    eventBus,
    callbacks,
    logger,
)
if err != nil {
    panic(err)
}

// Run leader election (blocks until context cancelled)
ctx := context.Background()
component.Run(ctx)
```

## API

### Component

Main leader election event adapter type.

#### New

```go
func New(
    config *k8sleaderelection.Config,
    clientset kubernetes.Interface,
    eventBus *busevents.EventBus,
    callbacks k8sleaderelection.Callbacks,
    logger *slog.Logger,
) (*Component, error)
```

Creates a new leader election component. The provided callbacks are wrapped to publish events before executing the callback.

**Parameters:**

- `config`: Pure leader election configuration (see `pkg/k8s/leaderelection`)
- `clientset`: Kubernetes clientset for Lease management
- `eventBus`: EventBus for publishing observability events
- `callbacks`: User-provided callbacks for leadership transitions
- `logger`: Structured logger (uses slog.Default() if nil)

**Returns:**

- Component instance or error if validation fails

#### Run

```go
func (c *Component) Run(ctx context.Context) error
```

Starts the leader election loop. Blocks until the context is cancelled. Should be run in a goroutine.

#### IsLeader

```go
func (c *Component) IsLeader() bool
```

Returns true if this instance is currently the leader.

#### GetLeader

```go
func (c *Component) GetLeader() string
```

Returns the identity of the current leader (empty string if no leader observed yet).

## Events Published

The component publishes the following events to the EventBus:

### LeaderElectionStartedEvent

Published when leader election starts (at component startup).

**Fields:**

- `Identity`: This instance's identity (pod name)
- `LeaseName`: Name of the Lease resource
- `LeaseNamespace`: Namespace of the Lease resource

### BecameLeaderEvent

Published when this instance becomes the leader (before OnStartedLeading callback).

**Fields:**

- `Identity`: This instance's identity

### LostLeadershipEvent

Published when this instance loses leadership (before OnStoppedLeading callback).

**Fields:**

- `Identity`: This instance's identity
- `Reason`: Why leadership was lost (e.g., "lease_lost")

### NewLeaderObservedEvent

Published when a new leader is observed (may be self or another instance).

**Fields:**

- `NewLeaderIdentity`: Identity of the new leader
- `IsSelf`: Whether this instance is the new leader

See `pkg/controller/events/types.go` for event type definitions.

## Leadership Transitions

### Becoming Leader

1. Replica acquires lease
2. `OnStartedLeading` callback fired
3. **BecameLeaderEvent** published
4. Leader-only components start (Deployer, DeploymentScheduler, DriftMonitor)
5. Replica begins deploying configurations

### Losing Leadership

1. Lease expires or context cancelled
2. `OnStoppedLeading` callback fired
3. **LostLeadershipEvent** published
4. Leader-only components stop
5. Replica continues watching/rendering/validating (hot standby)

### Failover

1. Old leader loses lease (pod crash, network partition, etc.)
2. After RenewDeadline (15s), lease becomes available
3. First follower to update lease becomes new leader
4. New leader starts deployment components
5. Reconciliation resumes with hot cache (~15-20s downtime)

## Failure Scenarios

### Leader Pod Crashes

- Lease expires after RenewDeadline (15s)
- Follower acquires lease and becomes leader
- Downtime: ~15-20 seconds

### Network Partition

- Leader cannot renew lease
- Leader voluntarily releases leadership after RenewDeadline
- Connected replica becomes leader
- Split-brain prevented by Kubernetes API coordination

### Clock Skew

- Tolerance: LeaseDuration / RenewDeadline (4x with defaults)
- If exceeded: Frequent leadership changes
- Mitigation: Run NTP on cluster nodes

## Testing

### Unit Tests

```go
func TestLeaderElector_IsLeader(t *testing.T)
func TestLeaderElector_EventPublishing(t *testing.T)
func TestLeaderElector_GracefulShutdown(t *testing.T)
```

### Integration Tests

```go
func TestLeaderElection_OnlyLeaderDeploys(t *testing.T)
func TestLeaderElection_Failover(t *testing.T)
func TestLeaderElection_BothReplicasWatchResources(t *testing.T)
```

## Configuration Example

```yaml
controller:
  leader_election:
    enabled: true
    lease_name: "haptic-leader"
    lease_duration: "60s"
    renew_deadline: "15s"
    retry_period: "5s"
```

## RBAC Requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haptic
rules:
  # Leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]
```

## Observability

### Metrics

- `controller_is_leader`: Gauge indicating current leadership (1=leader, 0=follower)
- `controller_leader_transitions_total`: Counter of leadership changes
- `controller_time_as_leader_seconds`: Counter of cumulative seconds as leader

### Logging

All leadership transitions are logged with structured context:

```
INFO  leader election started identity=pod-abc123 lease=haproxy-leader
INFO  became leader identity=pod-abc123
WARN  lost leadership identity=pod-abc123 reason=lease_expired time_as_leader=5m32s
INFO  new leader observed new_leader=pod-xyz789
```

### Debug Endpoint

```json
GET /debug/vars

{
  "leader_election": {
    "enabled": true,
    "is_leader": true,
    "identity": "haptic-7f8d9c5b-abc123",
    "lease_holder": "haptic-7f8d9c5b-abc123",
    "time_as_leader": "45m32s",
    "transitions": 2
  }
}
```

## References

- [Design Document](../../../docs/development/design/leader-election.md)
- [client-go leaderelection](https://pkg.go.dev/k8s.io/client-go/tools/leaderelection)
- [Kubernetes Coordinated Leader Election](https://kubernetes.io/docs/concepts/cluster-administration/coordinated-leader-election/)
