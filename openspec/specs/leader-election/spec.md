# Leader Election

## Purpose

Kubernetes Lease-based leader election for HA deployments with automatic failover, hot-standby replicas, and configurable timing parameters.

## Requirements

### Requirement: Lease-Based Leader Election

The leader election mechanism SHALL use Kubernetes Lease resources via `k8s.io/client-go/tools/leaderelection`. The elected leader SHALL hold the Lease and renew it periodically. Only one replica SHALL be the active leader at any given time.

#### Scenario: Single replica becomes leader

WHEN a single controller replica starts and no existing Lease is held
THEN the replica SHALL acquire the Lease and become the leader.

#### Scenario: Only one leader among multiple replicas

WHEN multiple controller replicas are running
THEN exactly one replica SHALL hold the Lease at any given time.

### Requirement: Configurable Timing Parameters

Leader election SHALL support configurable timing parameters: LeaseDuration (default 30s), RenewDeadline (default 20s), and RetryPeriod (default 5s). LeaseDuration controls how long a non-leader waits before attempting to acquire the Lease. RenewDeadline controls how long the leader retries renewing. RetryPeriod controls the interval between acquisition/renewal attempts. The defaults are deliberately 2x the client-go convention so that the leader rides out multi-second apiserver or CPU starvation stalls (observed at 10s+ on loaded nodes) without losing the Lease, at the cost of slower failover after a leader crash that does not release the Lease.

#### Scenario: Default timing parameters applied

WHEN leader election is started without explicit timing configuration
THEN LeaseDuration SHALL be 30 seconds, RenewDeadline SHALL be 20 seconds, and RetryPeriod SHALL be 5 seconds.

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

### Requirement: Context-Based Leadership Signaling

Leadership state SHALL be signaled exclusively through the callback context and callbacks, never through snapshot accessor methods. The leader election component SHALL NOT expose IsLeader() or GetLeader() accessors, because polling such accessors instead of deriving leadership from the context is racy. Leader-scoped work SHALL be tied to the context passed to OnStartedLeading, which is cancelled the moment leadership is lost.

#### Scenario: Leadership derived from callback context

WHEN a replica becomes leader
THEN leader-scoped work SHALL derive its lifetime from the context passed to OnStartedLeading rather than querying a leadership accessor.

#### Scenario: No snapshot accessors exposed

WHEN a caller needs to know whether this replica is the leader
THEN the component SHALL NOT provide an IsLeader() or GetLeader() method, and the caller SHALL rely on the leadership callbacks and their context instead.

### Requirement: Re-Election After Lost Lease

The controller SHALL treat an election loop that exits while the controller is still running as a fatal iteration error and SHALL reinitialize, restarting the election loop, so the replica can re-acquire leadership. The election loop (client-go's `LeaderElector.Run`) returns permanently once an acquired Lease is lost; without this supervision the replica would remain a follower with a dead elector until the next configuration change or pod restart — a permanent deployment stall on single-replica deployments.

#### Scenario: Lost lease triggers reinitialization

- **WHEN** the leader misses its Lease renewal (e.g. apiserver unavailability or CPU starvation longer than RenewDeadline) and the election loop exits
- **THEN** the controller SHALL reinitialize and start a new election loop with the same identity rather than continuing without an elector.

#### Scenario: Graceful shutdown does not trigger reinitialization

- **WHEN** the election loop exits because the controller's context was cancelled (shutdown or configuration-change reinitialization)
- **THEN** the exit SHALL NOT be treated as an error.

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

When the current leader becomes unavailable (crash, network partition), a standby replica SHALL acquire the Lease after approximately 30-35 seconds (LeaseDuration + RetryPeriod). Standby replicas SHALL maintain a ready state by continuously attempting to acquire the Lease at RetryPeriod intervals.

#### Scenario: Standby replica acquires Lease after leader failure

WHEN the leader stops renewing the Lease
THEN a standby replica SHALL acquire the Lease within approximately LeaseDuration + RetryPeriod.

#### Scenario: Standby replicas continuously attempt acquisition

WHEN a standby replica is running
THEN it SHALL attempt to acquire the Lease at RetryPeriod intervals.

### Requirement: All-Replica and Leader-Only Component Sets

Controller components SHALL be split into two lifecycle sets. The all-replica set — Reconciler, Discovery, HTTPStore, ProposalValidator, StatusApplier, ResourceApplier — SHALL run on every replica. The leader-only set — Coordinator, DriftPreventionMonitor, Deployer, DeploymentScheduler, ConfigPublisher, StatusUpdater — SHALL be started only after leadership is acquired and stopped when leadership is lost or the iteration ends. Stopping leader-only components SHALL cancel their dedicated context and pause briefly (100 ms graceful-stop delay) before returning.

#### Scenario: Followers run only the all-replica set

- **WHEN** a replica has not acquired the Lease
- **THEN** the leader-only components SHALL not be started on it, and the all-replica components SHALL run normally.

#### Scenario: Leadership loss stops leader-only components

- **WHEN** the leader loses the Lease
- **THEN** its leader-only components SHALL be stopped via their leader-scoped context.

### Requirement: Pause-Publish-Start Leadership Handoff

The leadership transition SHALL follow a strict ordering to prevent late-subscriber event loss: (1) pause the EventBus so subsequent publishes buffer, (2) publish BecameLeaderEvent (buffered), (3) run the leadership callback, which constructs and starts the leader-only components and blocks until every one of them signals that its event subscription is in place, (4) restart the EventBus, replaying the buffered BecameLeaderEvent (and anything else buffered during the transition) to all subscribers including the newly subscribed leader-only components.

#### Scenario: BecameLeader reaches late-started components

- **WHEN** a replica acquires leadership
- **THEN** the leader-only components SHALL receive the BecameLeaderEvent even though they subscribed after it was published, because the bus was paused across their startup.

#### Scenario: Handoff blocks on subscription readiness

- **WHEN** the leadership callback starts the leader-only components
- **THEN** the EventBus SHALL NOT be restarted until all of them have signalled subscription readiness.

### Requirement: Leader-Only Subscription Lifecycle

Leader-only components SHALL subscribe to their input events inside `Start()` — after leadership — using `SubscribeTypesLeaderOnly` (which suppresses the late-subscription warning), and SHALL unsubscribe when their event loop exits. Subscribing at construction would fill follower-side buffers with events published on every replica and log critical drops continuously; skipping the unsubscribe would stack an orphaned subscription on every leadership re-acquisition within the same process, whose full channel logs drops forever. One exception is permitted: a leader-only component whose input events are published only by another leader-only component (the Deployer, fed solely by the DeploymentScheduler) MAY subscribe at construction, because follower buffers for such types stay empty.

#### Scenario: Re-acquisition does not stack subscriptions

- **WHEN** the same replica loses and re-acquires leadership
- **THEN** the previous term's subscriptions SHALL have been removed on loop exit, leaving exactly one live subscription per leader-only component.

#### Scenario: Deployer construction-time subscription is safe

- **WHEN** a follower replica constructs the Deployer
- **THEN** its subscription SHALL receive no events, because the DeploymentScheduler publishing its input types runs only on the leader.

### Requirement: State Replay on Leadership Transitions

All-replica components that hold state consumed by leader-only components SHALL cache their latest state event in a StateReplayer and re-publish it on BecameLeaderEvent, so leader-only components that subscribe late still receive current state: Discovery SHALL replay its last HAProxyPodsDiscoveredEvent and the ConfigChangeHandler SHALL replay its last ConfigValidatedEvent. A component with no cached state SHALL skip the replay silently. Leader-only components SHALL clear their in-progress flags and pending work on LostLeadershipEvent so a later term cannot deadlock on stale in-flight state; historical data such as last-completion timestamps MAY be retained for rate limiting.

#### Scenario: New leader receives pre-leadership state

- **WHEN** pods were discovered and a config was validated before this replica acquired leadership
- **THEN** after BecameLeaderEvent the replayed HAProxyPodsDiscoveredEvent and ConfigValidatedEvent SHALL reach the freshly subscribed leader-only components.

#### Scenario: Lost leadership clears transient state

- **WHEN** a leader with an in-flight deployment loses the Lease
- **THEN** the deployment scheduler SHALL clear its in-progress and pending state so the next leadership term starts clean.

### Requirement: Disabled-Election Standalone Path

When leader election is disabled in the configuration, the controller SHALL start the leader-only components immediately using the same Pause-Publish-Start pattern: pause the EventBus, publish a BecameLeaderEvent with the identity `standalone`, start the leader-only components (blocking on subscription readiness), then restart the bus. This keeps the replay and subscription contracts identical whether or not an election ran.

#### Scenario: Standalone startup replays BecameLeader

- **WHEN** the controller starts with leader election disabled
- **THEN** the leader-only components SHALL start immediately and receive a BecameLeaderEvent carrying the identity `standalone` via the paused-bus replay.
