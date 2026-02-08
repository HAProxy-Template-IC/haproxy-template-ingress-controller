# Leader Election

Kubernetes Lease-based leader election for HA deployments with automatic failover, hot-standby replicas, and configurable timing parameters.

## ADDED Requirements

### Requirement: Lease-Based Leader Election

The leader election mechanism SHALL use Kubernetes Lease resources via `k8s.io/client-go/tools/leaderelection`. The elected leader SHALL hold the Lease and renew it periodically. Only one replica SHALL be the active leader at any given time.

#### Scenario: Single replica becomes leader

WHEN a single controller replica starts and no existing Lease is held
THEN the replica SHALL acquire the Lease and become the leader.

#### Scenario: Only one leader among multiple replicas

WHEN multiple controller replicas are running
THEN exactly one replica SHALL hold the Lease at any given time.

### Requirement: Configurable Timing Parameters

Leader election SHALL support configurable timing parameters: LeaseDuration (default 15s), RenewDeadline (default 10s), and RetryPeriod (default 2s). LeaseDuration controls how long a non-leader waits before attempting to acquire the Lease. RenewDeadline controls how long the leader retries renewing. RetryPeriod controls the interval between acquisition/renewal attempts.

#### Scenario: Default timing parameters applied

WHEN leader election is started without explicit timing configuration
THEN LeaseDuration SHALL be 15 seconds, RenewDeadline SHALL be 10 seconds, and RetryPeriod SHALL be 2 seconds.

#### Scenario: Custom timing parameters override defaults

WHEN leader election is configured with custom LeaseDuration, RenewDeadline, and RetryPeriod values
THEN the configured values SHALL be used instead of the defaults.

### Requirement: Leader Election Callbacks

Leader election SHALL invoke three callbacks: OnStartedLeading(ctx) when the replica becomes leader, OnStoppedLeading() when leadership is lost, and OnNewLeader(identity) when any replica observes a new leader identity (including itself).

#### Scenario: OnStartedLeading invoked on leadership acquisition

WHEN a replica acquires the Lease and becomes leader
THEN the OnStartedLeading callback SHALL be invoked with a context that remains valid for the duration of leadership.

#### Scenario: OnStoppedLeading invoked on leadership loss

WHEN the leader fails to renew the Lease before the RenewDeadline expires
THEN the OnStoppedLeading callback SHALL be invoked.

#### Scenario: OnNewLeader invoked for all replicas

WHEN a new leader is elected
THEN the OnNewLeader callback SHALL be invoked on all replicas (including the new leader) with the new leader's identity string.

### Requirement: Thread-Safe Leadership Queries

The leader election component SHALL provide thread-safe IsLeader() and GetLeader() methods. IsLeader() SHALL return true if the current replica holds the Lease. GetLeader() SHALL return the identity of the current leader.

#### Scenario: IsLeader returns true for the active leader

WHEN a replica holds the Lease
THEN IsLeader() SHALL return true.

#### Scenario: IsLeader returns false for non-leaders

WHEN a replica does not hold the Lease
THEN IsLeader() SHALL return false.

#### Scenario: GetLeader returns the current leader identity

WHEN a leader has been elected
THEN GetLeader() SHALL return the identity string of the current Lease holder.

### Requirement: Graceful Release on Context Cancellation

Leader election SHALL be configured with ReleaseOnCancel set to true. When the context is cancelled (e.g., during graceful shutdown), the leader SHALL release the Lease immediately rather than waiting for it to expire.

#### Scenario: Leader releases Lease on shutdown

WHEN the context passed to leader election is cancelled
THEN the leader SHALL release the Lease so that another replica can acquire it without waiting for LeaseDuration to expire.

### Requirement: Configuration Validation

Leader election configuration SHALL be validated at construction time. Identity, LeaseName, and LeaseNamespace are required fields. Construction SHALL fail with an error if any required field is missing.

#### Scenario: Missing Identity rejected

WHEN leader election is constructed with an empty Identity
THEN construction SHALL fail with a validation error.

#### Scenario: Missing LeaseName rejected

WHEN leader election is constructed with an empty LeaseName
THEN construction SHALL fail with a validation error.

#### Scenario: Missing LeaseNamespace rejected

WHEN leader election is constructed with an empty LeaseNamespace
THEN construction SHALL fail with a validation error.

### Requirement: Automatic Failover

When the current leader becomes unavailable (crash, network partition), a standby replica SHALL acquire the Lease after approximately 15-20 seconds (LeaseDuration + RetryPeriod). Standby replicas SHALL maintain a ready state by continuously attempting to acquire the Lease at RetryPeriod intervals.

#### Scenario: Standby replica acquires Lease after leader failure

WHEN the leader stops renewing the Lease
THEN a standby replica SHALL acquire the Lease within approximately LeaseDuration + RetryPeriod.

#### Scenario: Standby replicas continuously attempt acquisition

WHEN a standby replica is running
THEN it SHALL attempt to acquire the Lease at RetryPeriod intervals.
