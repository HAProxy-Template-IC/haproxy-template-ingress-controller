# haproxy-fleet-discovery Specification

## Purpose

Defines how the controller discovers the HAProxy fleet it deploys to: the auto-injected self-watch over its own HAProxy pods, the readiness and version-probe admission gates that decide which pods receive configuration, and the event contract (discovery, termination, rejection) that feeds the deployment scheduler. Discovery is the controller observing its own fleet — operational plumbing, deliberately separate from the resources templates consume.

## Requirements

### Requirement: Auto-Injected haproxy-pods Self-Watch

The controller SHALL always inject a `haproxy-pods` watcher — a Pods watch matching `spec.podSelector.matchLabels`, indexed by `metadata.namespace` and `metadata.name` — regardless of what `watchedResources` contains; a user-configured entry of the same name is overridden. This watcher SHALL be scoped to the controller's own namespace, while all other watched resources are watched cluster-wide.

#### Scenario: Watcher exists without configuration

- **WHEN** a HAProxyTemplateConfig declares no `haproxy-pods` entry in `watchedResources`
- **THEN** the controller SHALL still create the `haproxy-pods` watcher from `spec.podSelector.matchLabels`.

#### Scenario: Self-watch is namespace-scoped

- **WHEN** HAProxy pods matching the pod selector exist in a different namespace
- **THEN** they SHALL NOT appear in the `haproxy-pods` store; only pods in the controller's own namespace are watched.

### Requirement: Endpoint Admission Conditions

Discovery SHALL build a Dataplane API endpoint of the form `http://<podIP>:<dataplanePort>/v3` for a pod only when all of the following hold: the pod is not terminating (no deletionTimestamp — a terminating pod may still report Running and ready while its ports shut down), the pod has a podIP, the pod phase is `Running`, and the specific container exposing the dataplane port reports ready. Pods failing any condition SHALL be skipped without error.

#### Scenario: Terminating pod excluded despite readiness

- **WHEN** a pod has a deletionTimestamp but still reports phase Running and a ready dataplane container
- **THEN** discovery SHALL exclude it from the candidate set.

#### Scenario: Dataplane container readiness is the gate

- **WHEN** a Running pod's dataplane-port container is not ready
- **THEN** discovery SHALL exclude the pod even if other containers are ready.

### Requirement: Major-Version Probe Admission

Each newly discovered candidate pod SHALL be version-probed via its Dataplane API `/v3/info` endpoint with a 10-second probe timeout. A pod SHALL be admitted only when the remote Dataplane API major version equals the major version of the controller's local HAProxy binary. The comparison SHALL be major-only: from HAProxy 3.4 the Dataplane API minor decouples from the HAProxy binary minor, so a major.minor match would wrongly reject correctly paired fleets, while a different major is genuinely unsupported.

A major-version mismatch SHALL be permanently rejected — no retry, because Kubernetes pods are replaced on upgrade, not mutated — publishing a HAProxyPodRejectedEvent with reason `version_mismatch_older` or `version_mismatch_newer`. A transient probe failure SHALL publish a HAProxyPodRejectedEvent with reason `version_check_failed` and schedule a retry with exponential backoff starting at 5 seconds, doubling per attempt, capped at 1 minute. Admitted pods SHALL be cached with their detected version and never re-probed while they remain candidates; state for pods that leave the candidate set SHALL be evicted from both the admitted cache and the pending-retry set.

#### Scenario: Version mismatch is permanent

- **WHEN** a pod's Dataplane API reports a different major version than the controller's local HAProxy binary
- **THEN** the pod SHALL be rejected with a mismatch reason and SHALL NOT be scheduled for retry.

#### Scenario: Transient probe failure retries with backoff

- **WHEN** the `/v3/info` probe of a new pod fails with a connection error three times
- **THEN** the retry intervals SHALL be 5 s, 10 s, and 20 s respectively, and retries SHALL continue (capped at 1 minute apart) until the probe succeeds or the pod leaves the candidate set.

#### Scenario: Admitted pods skip re-probing

- **WHEN** a previously admitted pod appears in a subsequent discovery cycle
- **THEN** discovery SHALL reuse its cached endpoint and version without contacting `/v3/info` again.

### Requirement: Once-Gated Initial Discovery

Exactly one initial discovery SHALL run at startup, and only after all four preconditions hold: the pod store is set, credentials have been loaded, the dataplane port is known from a validated config, and the `haproxy-pods` watcher's initial sync is complete. Multiple event handlers (config validated, credentials updated, resource sync complete, pod index updated) MAY each attempt the initial discovery; an atomic check-and-set gate SHALL ensure only the first attempt with all preconditions satisfied performs it.

#### Scenario: No duplicate initial discovery

- **WHEN** the config-validated and sync-complete handlers race to trigger the initial discovery
- **THEN** exactly one discovery SHALL run.

#### Scenario: Discovery blocked on missing input

- **WHEN** the `haproxy-pods` sync completes before credentials have been loaded
- **THEN** no discovery SHALL run until the credentials arrive, at which point the initial discovery fires.

### Requirement: Discovery Publication and Termination Tracking

After each discovery cycle the component SHALL publish a HAProxyPodTerminatedEvent for every pod present in the previous admitted set but absent from the new one, then publish a single HAProxyPodsDiscoveredEvent carrying the full admitted endpoint set. HAProxyPodsDiscoveredEvent SHALL be a coalescible full-state notification: consumers that only need the latest fleet view (the deployment scheduler) MAY collapse consecutive discovery events under churn to the newest one. The latest discovered event SHALL be cached in a state replayer, and on BecameLeaderEvent the component SHALL re-publish the cached event — without re-running discovery — so the leader-only deployment scheduler receives current endpoint state despite subscribing late.

#### Scenario: Removed pod emits a termination event

- **WHEN** a previously admitted pod is absent from the current admitted set
- **THEN** a HAProxyPodTerminatedEvent naming that pod SHALL be published before the new HAProxyPodsDiscoveredEvent.

#### Scenario: Leadership replay without re-discovery

- **WHEN** a replica becomes leader after a discovery has already completed
- **THEN** the cached HAProxyPodsDiscoveredEvent SHALL be re-published as-is, and no new probe or store scan SHALL run for the replay.

#### Scenario: No state to replay

- **WHEN** a replica becomes leader before any discovery has completed
- **THEN** the replay SHALL be skipped silently.

### Requirement: Re-Discovery Triggers

After the initial discovery, the component SHALL re-run discovery on each of: a validated config change (which also updates the dataplane port), a credentials update, a non-initial-sync `haproxy-pods` index change, and the pending-probe retry timer firing. Each trigger SHALL be skipped when any required input (credentials, port, pod store) is missing.

#### Scenario: Pod churn triggers re-discovery

- **WHEN** the `haproxy-pods` index reports a created or deleted pod after initial sync
- **THEN** a discovery cycle SHALL run against the current store state.

#### Scenario: Port change re-evaluates the fleet

- **WHEN** a validated config changes the dataplane port
- **THEN** discovery SHALL rebuild endpoints against the new port.
