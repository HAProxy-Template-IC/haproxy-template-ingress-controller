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

Discovery SHALL build a Dataplane API endpoint of the form `http://<podIP>:<dataplanePort>/v3` for a pod only when all of the following hold: the pod is not terminating (no deletionTimestamp — a terminating pod may still report Running and ready while its ports shut down), the pod has a podIP, the pod phase is `Running`, and the specific container exposing the dataplane port reports ready. IPv6 literals SHALL be enclosed in brackets. Pods failing any condition SHALL be skipped without error.

#### Scenario: Terminating pod excluded despite readiness

- **WHEN** a pod has a deletionTimestamp but still reports phase Running and a ready dataplane container
- **THEN** discovery SHALL exclude it from the candidate set.

#### Scenario: Dataplane container readiness is the gate

- **WHEN** a Running pod's dataplane-port container is not ready
- **THEN** discovery SHALL exclude the pod even if other containers are ready.

### Requirement: Endpoint Version-Proof Admission

Each newly discovered candidate pod SHALL be probed through its Dataplane API with a 10-second overall timeout. `/v3/info` SHALL prove the Dataplane API version used for client selection and edition detection. `/v3/services/haproxy/runtime/info` SHALL independently prove the remote HAProxy binary version. A pod SHALL be admitted only when its Dataplane API major version equals the controller's supported major and its HAProxy binary major.minor equals the controller's local HAProxy binary series. Dataplane API minor versions SHALL NOT be compared with HAProxy binary minor versions: HAProxy 3.4 ships Dataplane API v3.3, so those axes legitimately differ.

A version mismatch SHALL be permanently rejected for the same candidate identity, publishing a HAProxyPodRejectedEvent with reason `version_mismatch_older` or `version_mismatch_newer`. A transient failure of either probe SHALL publish a HAProxyPodRejectedEvent with reason `version_check_failed` and schedule a retry with exponential backoff starting at 5 seconds, doubling per attempt, capped at 1 minute. A pending candidate SHALL NOT be probed before its own retry deadline; once that deadline is armed, unrelated discovery cycles SHALL NOT postpone it, while an earlier candidate deadline MAY re-arm the timer earlier. A credentials change SHALL reset pending backoff and probe immediately. A successful admission SHALL cache both version proofs against the candidate's namespace, name, pod UID, container execution epoch, and URL. A matching candidate SHALL reuse those proofs while rebuilding the endpoint from its current URL and credentials. A different pod UID, container execution epoch, or URL SHALL require both probes again. State for candidates that leave the set SHALL be evicted from the admission-proof, permanent-rejection, and pending-retry caches.

#### Scenario: Version mismatch is permanent

- **WHEN** a pod's Dataplane API reports an unsupported major version
- **THEN** the pod SHALL be rejected with a mismatch reason and the same candidate identity SHALL NOT be scheduled or probed again.

#### Scenario: HAProxy binary series mismatch is permanent

- **WHEN** the runtime endpoint reports an HAProxy major.minor series different from the controller's local binary
- **THEN** the pod SHALL be rejected with a mismatch reason and SHALL NOT receive configuration validated for another series.

#### Scenario: Transient probe failure retries with backoff

- **WHEN** a version probe of a new pod fails with a connection error three times
- **THEN** the retry intervals SHALL be 5 s, 10 s, and 20 s respectively, and retries SHALL continue (capped at 1 minute apart) until the probe succeeds or the pod leaves the candidate set.

#### Scenario: Admitted pods skip re-probing

- **WHEN** a previously admitted pod appears in a subsequent discovery cycle
- **THEN** discovery SHALL reuse both version proofs without contacting either probe endpoint again and SHALL rebuild the endpoint with current credentials.

#### Scenario: Credentials rotate after admission

- **WHEN** Dataplane API credentials change for a candidate whose namespace, name, UID, container execution epoch, and URL still match its admission proof
- **THEN** the next discovery event SHALL contain the new credentials without another version probe.

#### Scenario: Credentials rotate during probe backoff

- **WHEN** Dataplane API credentials change while a candidate is waiting after a transient probe failure
- **THEN** discovery SHALL clear that candidate's backoff and probe it immediately with the new credentials.

#### Scenario: Older retry cannot overwrite new authority

- **WHEN** a retry discovery overlaps a credentials or dataplane-port update
- **THEN** complete discovery publications SHALL remain ordered so the older result cannot replace the updated endpoint state.

#### Scenario: Endpoint identity changes after admission

- **WHEN** an admitted pod is replaced under the same name, one of its containers restarts or changes image, or its Dataplane API URL changes
- **THEN** discovery SHALL prove both versions for the new runtime identity before admitting it.

#### Scenario: Discovery churn does not starve a pending probe

- **WHEN** unrelated discovery cycles occur more frequently than the pending candidate's retry timer delay
- **THEN** the existing earlier timer SHALL remain armed and the candidate SHALL be retried at its original deadline.

### Requirement: Once-Gated Initial Discovery

Exactly one initial discovery SHALL run at startup, and only after all four preconditions hold: the pod store is set, credentials have been loaded, the dataplane port is known from a validated config, and the `haproxy-pods` watcher's initial sync is complete. Multiple event handlers (config validated, credentials updated, resource sync complete, pod index updated) MAY each attempt the initial discovery; an atomic check-and-set gate SHALL ensure only the first attempt with all preconditions satisfied performs it.

#### Scenario: No duplicate initial discovery

- **WHEN** the config-validated and sync-complete handlers race to trigger the initial discovery
- **THEN** exactly one discovery SHALL run.

#### Scenario: Discovery blocked on missing input

- **WHEN** the `haproxy-pods` sync completes before credentials have been loaded
- **THEN** no discovery SHALL run until the credentials arrive, at which point the initial discovery fires.

### Requirement: Discovery Publication and Termination Tracking

Before probing a candidate, the component SHALL compare it with every previously admitted endpoint's complete authority: URL, credentials, pod namespace, pod name, pod UID, container execution epoch, and detected version. It SHALL immediately publish a HAProxyPodTerminatedEvent for every authority that is absent or changed, followed by an interim HAProxyPodsDiscoveredEvent retaining only exact previously proven authorities; a replacement SHALL therefore never remain deployable while its admission probe is blocked. After admission completes, the component SHALL publish the final full admitted endpoint set. HAProxyPodsDiscoveredEvent SHALL be a coalescible full-state notification: consumers that only need the latest fleet view (the deployment scheduler) MAY collapse consecutive discovery events under churn to the newest one. The latest discovered event SHALL be cached in a state replayer, and on BecameLeaderEvent the component SHALL re-publish the cached event — without re-running discovery — so the leader-only deployment scheduler receives current endpoint state despite subscribing late. Discovery publication and leadership replay SHALL be serialized so an older cached fleet cannot be published after a newer discovery result.

#### Scenario: Removed pod emits a termination event

- **WHEN** a previously admitted pod is absent from the current admitted set
- **THEN** a HAProxyPodTerminatedEvent naming that pod SHALL be published before the new HAProxyPodsDiscoveredEvent.

#### Scenario: Same-name replacement emits predecessor transition

- **WHEN** a pod name remains present but its UID or another endpoint-authority field changes
- **THEN** a HAProxyPodTerminatedEvent carrying the predecessor UID SHALL be published before the new HAProxyPodsDiscoveredEvent

#### Scenario: Replacement retires before admission probe

- **WHEN** a same-name replacement's version probe is blocked
- **THEN** the predecessor authority SHALL be absent from the interim full-state event before the probe completes.

#### Scenario: Leadership replay without re-discovery

- **WHEN** a replica becomes leader after a discovery has already completed
- **THEN** the cached HAProxyPodsDiscoveredEvent SHALL be re-published as-is, and no new probe or store scan SHALL run for the replay.

#### Scenario: Leadership replay overlaps discovery

- **WHEN** leadership replay overlaps publication of a newer discovery result
- **THEN** the replay SHALL publish before the newer result or replay that newer result; it SHALL NOT publish the older fleet afterward.

#### Scenario: No state to replay

- **WHEN** a replica becomes leader before any discovery has completed
- **THEN** the replay SHALL be skipped silently.

### Requirement: Re-Discovery Triggers

After the initial discovery, the component SHALL re-run discovery on each of: a validated config change (which also updates the dataplane port), a credentials update, a non-initial-sync `haproxy-pods` index change, a drift-prevention tick, and the pending-probe retry timer firing. Each trigger SHALL be skipped when any required input (credentials, port, pod store) is missing.

#### Scenario: Pod churn triggers re-discovery

- **WHEN** the `haproxy-pods` index reports a created or deleted pod after initial sync
- **THEN** a discovery cycle SHALL run against the current store state.

#### Scenario: Port change re-evaluates the fleet

- **WHEN** a validated config changes the dataplane port
- **THEN** discovery SHALL rebuild endpoints against the new port.
