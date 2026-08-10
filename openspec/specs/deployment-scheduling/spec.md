# deployment-scheduling Specification

## Purpose

Defines the fleet-level deployment layer that decides when and how validated HAProxy configurations reach the discovered HAProxy pods: the leader-only DeploymentScheduler (single deploy loop, lane classification, rate limiting, skip-unchanged gating, validation fallback), the Deployer executor that fans a scheduled deployment out to all endpoints, the runtime-raw bypass that applies runtime-eligible server changes without a reload, and the DriftPreventionMonitor that forces periodic resyncs. The single-endpoint fetch-compare-apply mechanics live in the dataplane-sync capability; this capability governs everything above that per-endpoint call.

## Requirements

### Requirement: Single Deploy Loop with Latest-Wins Pending Slot

The DeploymentScheduler SHALL own all deployment rate-limit timing in a single deploy-loop goroutine per leadership term. Event handlers SHALL NOT sleep, spawn timing goroutines, or publish deployment events themselves; they SHALL only overwrite a single pending-deployment slot (latest wins) and signal the loop via a non-blocking capacity-1 channel. The loop SHALL coalesce to the newest pending deployment at grab time, making "at most one structural deploy per minimum deployment interval" a structural property rather than an emergent one. On lost leadership the scheduler SHALL clear the pending slot, the in-flight flag, and the dispatch baseline, and SHALL retain the last-deployment-end timestamp so a quickly re-acquired leadership does not burst deploys.

#### Scenario: Newer render supersedes a queued one

- **WHEN** three validated renders arrive while a structural deploy is in flight
- **THEN** the pending slot SHALL hold only the newest render
- **AND** after the in-flight deploy completes the loop SHALL dispatch exactly that newest render

#### Scenario: Lost leadership clears transient state

- **WHEN** a LostLeadershipEvent arrives with a deploy in flight and a pending deployment queued
- **THEN** the scheduler SHALL clear the in-flight flag, the pending slot, and the dispatch diff baseline, and SHALL close the runtime bypass's persistent clients
- **AND** the last-deployment-end timestamp SHALL be preserved

### Requirement: Lane Classification Against the Last-Dispatched Config

Each scheduled render SHALL be classified into one of two apply lanes by diffing its parsed config against the last-DISPATCHED config (the render the most recent dispatch committed to — not the last completed deploy). The runtime-raw lane SHALL be chosen if and only if the baseline is non-nil, the diff computed without error, and the diff contains at least one runtime-eligible server change and zero structural operations. Every other case — nil baseline (cold start or new leader), diff error, or any structural operation — SHALL take the structural lane. The dispatch baseline SHALL advance at dispatch time for both lanes, and the precomputed runtime diff SHALL travel with the pending deployment so dispatch does not recompute it. If the baseline advances while a diff is being computed, the scheduler SHALL recompute against the new baseline before classifying.

#### Scenario: Pure server-field diff takes the runtime-raw lane

- **WHEN** a render's diff against the last-dispatched config contains only runtime-eligible server-field updates (for example a pod IP rotation)
- **THEN** the render SHALL be classified runtime-raw and applied via a single skip-reload raw push carrying the runtime actions, with no reload and no deployment-interval wait

#### Scenario: Cold start is structural

- **WHEN** a new leader schedules its first render (nil dispatch baseline)
- **THEN** the render SHALL be classified structural and the whole config SHALL be deployed via the scheduled-deployment path

#### Scenario: Structural pending absorbs later renders

- **WHEN** a structural render is pending and a later render arrives before dispatch
- **THEN** the later render SHALL be diffed against the same unchanged baseline, still contain the structural change, and remain structural — the two lanes never coexist in the pending slot

### Requirement: Minimum Deployment Interval Throttle

Structural deploys SHALL be rate-limited by a minimum deployment interval measured from the END of the previous structural deploy (its completion or its timeout). The controller default SHALL be 2 seconds; the bundled Helm chart configures 5 seconds via its values. A structural pending whose remaining interval is positive SHALL sleep out exactly the remaining time; the timer SHALL NOT be reset when newer renders arrive mid-sleep, so the structural reload fires at the original deadline and reloads cannot burst under churn. Runtime-raw dispatches SHALL ignore the interval entirely and SHALL NOT advance the interval anchor (they reload nothing).

#### Scenario: Structural deploy waits out the remaining interval

- **WHEN** a structural render is scheduled 500 ms after the previous structural deploy ended and the interval is 2 s
- **THEN** the loop SHALL wait the remaining 1.5 s before publishing the DeploymentScheduledEvent

#### Scenario: Runtime-raw dispatch is not interval-gated

- **WHEN** a runtime-raw render is the pending deployment during an interval window
- **THEN** it SHALL be dispatched immediately without waiting

### Requirement: Runtime-Subset Fast-Track During Waits

While the deploy loop is gated — sleeping out the deployment interval before a structural deploy, or blocked awaiting an in-flight structural deploy's completion — the scheduler SHALL immediately apply the latest pending render's runtime-eligible server subset to the live HAProxy workers via a partial runtime-raw apply. This fast-track SHALL be lane-independent (gated only on a non-empty runtime server subset), SHALL fire again for every newer render that arrives mid-wait, and SHALL NOT advance the dispatch baseline, so the eventual authoritative dispatch re-applies the same idempotent changes. A partial apply SHALL suppress the deploy-owning publications (per-pod status and deployed-config publish); the owning deploy publishes them after its reload. This is the mechanism that lets a newly-Ready pod's server slot fill in milliseconds even when the deploy loop is blocked behind an unrelated structural change.

#### Scenario: Endpoint change converges during an interval sleep

- **WHEN** a render carrying only a new pod's server address arrives while the loop is sleeping out the deployment interval before a structural deploy
- **THEN** the scheduler SHALL apply that render's runtime server subset to the live workers immediately
- **AND** the structural reload SHALL still fire at the original interval deadline

#### Scenario: Fast-track during an in-flight deploy

- **WHEN** a newer render arrives while a structural deploy is awaiting completion
- **THEN** its runtime-eligible server subset SHALL be applied partially without consuming the pending slot
- **AND** the loop SHALL dispatch the pending render authoritatively after the in-flight deploy completes

#### Scenario: Partial apply does not publish deploy state

- **WHEN** a runtime subset is fast-tracked while a structural deploy owns the cycle
- **THEN** no per-pod status event and no deployed-config publish request SHALL be emitted by the fast-track; only the runtime fast-path metric event fires

### Requirement: Structural Dispatch, Completion Await, and Timeout Recovery

A structural dispatch SHALL mark the deploy in flight, advance the dispatch baseline, publish exactly one DeploymentScheduledEvent, and block until the matching DeploymentCompletedEvent, a deployment timeout, or shutdown. The deployment timeout SHALL default to 30 seconds. On timeout the scheduler SHALL publish a DeploymentCancelRequestEvent carrying the active correlation ID (so the Deployer cancels the running deployment), clear the in-flight state, count the timeout as a deploy end for interval accounting, and publish a non-coalescible recovery ReconciliationTriggeredEvent. Any pending deployment SHALL be kept across a timeout and picked up on the loop's next cycle.

#### Scenario: Timeout cancels and recovers

- **WHEN** an in-flight structural deploy exceeds the 30 s deployment timeout
- **THEN** the scheduler SHALL publish a cancel request with the deploy's correlation ID, record the timeout as the deployment end time, and trigger a non-coalescible recovery reconciliation

#### Scenario: Completion releases the loop

- **WHEN** the DeploymentCompletedEvent for the in-flight deploy arrives
- **THEN** the scheduler SHALL clear the in-flight state, record the deployment end time, and let the loop dispatch any pending deployment on its next cycle

### Requirement: Skip-Unchanged Gate

Before scheduling a validation-driven deployment, the scheduler SHALL skip it when the render's content checksum equals the last successfully deployed config hash AND the hash of the current endpoint set (sorted endpoint URLs) equals the pod-set hash of the last successful deploy. Drift-prevention deployments SHALL always bypass this gate. A skipped deployment SHALL publish a DeploymentSkippedEvent carrying the render's status patches so downstream consumers can still mark the data plane converged. The last-deployed config hash SHALL be updated only from a DeploymentCompletedEvent whose content checksum is non-empty and whose failure count is zero; failed or partial deploys SHALL leave the cache at the last good hash so the next reconcile retries immediately. The content checksum compared and recorded SHALL be the one captured together with the config at schedule time and threaded through the deployment events — never re-read from mutable state at deploy or completion time.

#### Scenario: Unchanged config for the same pod set is skipped

- **WHEN** a validated render's content checksum and the current pod-set hash both match the last fully successful deploy
- **THEN** no deployment SHALL be scheduled and a DeploymentSkippedEvent SHALL be published with the render's status patches

#### Scenario: Drift prevention bypasses the gate

- **WHEN** a deployment is triggered with the drift-prevention reason
- **THEN** it SHALL execute even if checksum and pod-set hash are unchanged

#### Scenario: Failed deploy does not poison the cache

- **WHEN** a DeploymentCompletedEvent reports one or more failed endpoints
- **THEN** the last-deployed config hash SHALL NOT be updated, so the next render with the same checksum re-deploys

### Requirement: Pod-Discovery Deployments

On HAProxyPodsDiscoveredEvent the scheduler SHALL update its endpoint set and schedule a deployment of the last VALIDATED config (with the checksum, correlation ID, status patches, and coalescibility captured in the same lock window as that config) to the new endpoints. Consecutive coalescible pods-discovered events queued behind a running handler SHALL collapse to the latest. When no validated config exists yet or the endpoint set is empty, the event SHALL be recorded without scheduling.

#### Scenario: New pod set deploys the cached config

- **WHEN** pods are discovered after a config has been validated
- **THEN** the scheduler SHALL schedule the last validated config to the discovered endpoints with the checksum captured alongside that config

### Requirement: Validation-Failed Fallback

On ValidationFailedEvent the scheduler SHALL schedule its cached last-validated config (with the content checksum captured with it) to the current endpoints as a NON-coalescible fallback deployment, so HAProxy pods converge on the last known-good config while the latest render is invalid. When no validated config has been cached yet, or no endpoints exist, the fallback SHALL be skipped with a log entry.

#### Scenario: Invalid render falls back to last-known-good

- **WHEN** validation fails after at least one earlier render was validated
- **THEN** the scheduler SHALL schedule the cached last-validated config non-coalescibly, recording that config's own checksum — not the failed render's

#### Scenario: No cached config means no fallback

- **WHEN** validation fails before any render was ever validated
- **THEN** no deployment SHALL be scheduled

### Requirement: Deployer Executor

The Deployer SHALL be a stateless executor of DeploymentScheduledEvent: it deploys the event's config and auxiliary files to all endpoints in parallel (one goroutine per endpoint), publishing DeploymentStartedEvent first and one DeploymentCompletedEvent afterwards with the aggregated result (total, succeeded, failed, reloads, operations) and the event's own StatusPatches and ContentChecksum forwarded unchanged. Each endpoint sync SHALL be bounded by a per-endpoint sync timeout (default 2 minutes) and a reload-verification timeout (default 10 seconds). An atomic in-progress guard SHALL drop duplicate scheduled events while a deployment runs. Pending coalescible DeploymentScheduledEvents SHALL be coalesced latest-wins after each dispatch; DeploymentCompletedEvents SHALL never be coalesced. Each deployment SHALL run under a cancellable context so a DeploymentCancelRequestEvent matching the active correlation ID (or shutdown) aborts it. A zero-endpoint deployment SHALL publish a DeploymentCompletedEvent with an empty content checksum so the scheduler does not record it as a successful deploy.

The Deployer SHALL stamp each pod's per-pod status checksum with the deployment's CONTENT checksum (config plus auxiliary files — the same value the config publisher writes as the published spec checksum) and SHALL publish a ConfigAppliedToPodEvent for every endpoint UNCONDITIONALLY on success — including zero-operation no-op syncs — because skipping no-ops breaks the published-spec versus per-pod-status convergence invariant. Endpoint failures SHALL publish an InstanceDeploymentFailedEvent plus a ConfigAppliedToPodEvent carrying the error. After a deployment with at least one success that is not a drift-prevention check, the Deployer SHALL publish a DeployedConfigPublishRequest so the just-deployed bytes become observable as the published spec.

#### Scenario: No-op sync still publishes per-pod status

- **WHEN** an endpoint sync applies zero operations because the pod is already at the desired config
- **THEN** a ConfigAppliedToPodEvent with the deployment's content checksum SHALL still be published for that pod

#### Scenario: Completion event describes what was deployed

- **WHEN** a newer render lands mid-deployment
- **THEN** the DeploymentCompletedEvent SHALL still carry the checksum and status patches of the deployment that ran, not the newer render's

#### Scenario: Duplicate scheduled event dropped

- **WHEN** a DeploymentScheduledEvent arrives while a deployment is already in progress
- **THEN** the Deployer SHALL drop it with an error log instead of running two deployments concurrently

### Requirement: Per-Endpoint Version Cache

The Deployer SHALL keep a per-endpoint cache of the last-synced config version, parsed config, and content checksum, letting subsequent syncs skip the full fetch-and-parse when the pod's version is unchanged. The cached parsed config SHALL be the pod's ACTUAL post-sync state when the sync reports one (preferring the orchestrator's post-sync fetch over the caller's desired config), so per-pod divergence stays detectable. The cache entry SHALL be invalidated when a sync fails (pod state uncertain) and the whole cache SHALL be cleared on component start (leadership transitions). Drift-prevention deployments SHALL NOT pass the cached checksum as the last-deployed checksum, forcing full comparison.

#### Scenario: Cache stores actual post-sync state

- **WHEN** a sync applies operations and the post-sync fetch succeeds
- **THEN** the cache SHALL store the fetched post-sync parsed config rather than the desired input

#### Scenario: Failure invalidates the entry

- **WHEN** a sync to an endpoint fails
- **THEN** that endpoint's cache entry SHALL be invalidated so the next sync does a full fetch

### Requirement: Runtime-Raw Bypass

The runtime bypass SHALL apply runtime-eligible server changes to every endpoint concurrently via one skip-reload raw push per endpoint carrying the shared precomputed render diff, each bounded by a 5-second per-endpoint timeout. Dataplane clients (and their keep-alive HTTP connections) SHALL be persistent per endpoint URL — opened once, reused across applies, evicted when the endpoint disappears, and all closed on scheduler shutdown or lost leadership. Every per-endpoint failure and panic SHALL be swallowed to a debug log: the bypass is best-effort and the scheduled deploy is the correctness floor. Every apply SHALL be recorded to the runtime fast-path counters via the scheduler's injected recorder.

An AUTHORITATIVE apply (the pure runtime-raw lane dispatch, partial=false) IS the complete deploy: on each successful endpoint it SHALL publish a ConfigAppliedToPodEvent stamping the pod at the deployment's content checksum, and — once, when at least one endpoint succeeded — a DeployedConfigPublishRequest so the applied config is observable as the published spec. A PARTIAL apply (fast-track during a wait, partial=true) SHALL suppress both publications.

#### Scenario: Authoritative runtime-raw apply publishes deploy state

- **WHEN** the deploy loop dispatches a runtime-raw pending and the apply succeeds on at least one endpoint
- **THEN** each successful pod SHALL get a ConfigAppliedToPodEvent at the render's content checksum and one DeployedConfigPublishRequest SHALL be published

#### Scenario: Bypass failure never fails the pipeline

- **WHEN** a bypass push to an endpoint fails or its client cannot be opened
- **THEN** the failure SHALL be logged at debug and the scheduled deploy SHALL remain responsible for converging that pod

#### Scenario: Stale clients evicted

- **WHEN** an endpoint's pod is deleted and a later apply runs against the new endpoint set
- **THEN** the deleted endpoint's cached client SHALL be closed and dropped

### Requirement: Drift-Prevention Monitor

The leader-only DriftPreventionMonitor SHALL arm a timer for the drift-prevention interval (default 60 seconds) and, on expiry, publish a DriftPreventionTriggeredEvent carrying the time since the last deployment — driving a full reconcile-and-deploy that verifies and corrects out-of-band changes on the HAProxy pods. The timer SHALL reset on every DeploymentCompletedEvent and SHALL re-arm after each firing even if the resulting deployment fails. The component SHALL report unhealthy when no timer activity occurs for 1.5 times the interval (90 seconds at the default). The timer SHALL be stopped on LostLeadershipEvent and on shutdown; a new leader starts its own.

#### Scenario: Idle interval triggers a drift check

- **WHEN** no deployment completes within the drift-prevention interval
- **THEN** a DriftPreventionTriggeredEvent SHALL be published and the timer re-armed

#### Scenario: Deployments push the timer back

- **WHEN** a DeploymentCompletedEvent arrives before the timer expires
- **THEN** the timer SHALL be reset to a full interval

#### Scenario: Stalled timer is unhealthy

- **WHEN** the monitor records no timer activity for more than 1.5 times the interval
- **THEN** its health check SHALL report an error
