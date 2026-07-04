# reconciliation-pipeline Specification

## Purpose

Defines how resource changes become rendered, validated HAProxy configurations: the Reconciler's immediate trigger semantics, the leader-only Coordinator's trigger coalescing, the synchronous render-validate pipeline, and the event fan-out that drives deployment, publishing, and status updates. Also pins the dual validation-pipeline split that keeps the leader's reconcile loop fast while all external input stays strictly validated.

## Requirements

### Requirement: Immediate Reconciliation Triggering

The Reconciler SHALL publish a ReconciliationTriggeredEvent immediately on every event it handles — it SHALL hold no timer, no debounce, and no refractory state. Batching of rapid changes is delegated upstream to the per-watcher debounce window (default 2 seconds; EndpointSlice watchers typically configured to `"0"`), and reload throttling is delegated downstream to the deployment scheduler's minimum deployment interval. The Reconciler SHALL subscribe with a high-volume buffer of 100 events.

#### Scenario: No reconciler-level latency

- **WHEN** a watched resource's index changes after initial sync
- **THEN** the Reconciler SHALL publish a ReconciliationTriggeredEvent without waiting for any timer or quiet period.

#### Scenario: Burst batching happens upstream

- **WHEN** many changes to one resource kind arrive within its watcher's debounce window
- **THEN** the Reconciler SHALL receive one ResourceIndexUpdatedEvent for the batch and publish one trigger for it.

### Requirement: Trigger Taxonomy and Coalescibility

The Reconciler SHALL map input events to trigger reasons and coalescibility as follows: ResourceIndexUpdatedEvent → `resource_change` (coalescible), HTTPResourceUpdatedEvent → `http_resource_change` (coalescible), IndexSynchronizedEvent → `index_synchronized`, HTTPResourceAcceptedEvent → `http_resource_accepted`, DriftPreventionTriggeredEvent → `drift_prevention`, BecameLeaderEvent → `became_leader` — the latter four are commands and SHALL be non-coalescible so downstream stages never skip them. Two input filters SHALL apply to ResourceIndexUpdatedEvent: initial-sync events are skipped (the first reconciliation is driven by IndexSynchronizedEvent once all watchers have synced), and `haproxy-pods` changes are skipped because HAProxy pods are deployment targets, not configuration sources.

#### Scenario: Initial-sync events do not trigger reconciliation

- **WHEN** a ResourceIndexUpdatedEvent arrives with initial-sync change stats
- **THEN** the Reconciler SHALL not publish a trigger; the initial reconciliation SHALL fire on IndexSynchronizedEvent instead.

#### Scenario: HAProxy pod churn does not re-render

- **WHEN** a ResourceIndexUpdatedEvent arrives for the `haproxy-pods` resource type
- **THEN** the Reconciler SHALL not publish a trigger; pod changes reach the deployer via the fleet-discovery events.

#### Scenario: Command triggers are non-coalescible

- **WHEN** a drift-prevention or became-leader event is handled
- **THEN** the resulting ReconciliationTriggeredEvent SHALL be marked non-coalescible.

### Requirement: Correlation ID Propagation

Every ReconciliationTriggeredEvent SHALL carry a freshly generated correlation ID, and that ID SHALL be propagated through the entire reconciliation cycle — ReconciliationStarted, TemplateRendered, ValidationCompleted, ReconciliationCompleted, and the failure events — so a single cycle is traceable end to end across coordinator, scheduler, and deployer.

#### Scenario: One cycle, one correlation ID

- **WHEN** a trigger with correlation ID X completes its render and validation
- **THEN** the TemplateRenderedEvent, ValidationCompletedEvent, and ReconciliationCompletedEvent for that cycle SHALL all carry correlation ID X.

### Requirement: Coordinator Leader-Only Subscription and Trigger Coalescing

The Coordinator SHALL be a leader-only component: it SHALL subscribe to ReconciliationTriggeredEvent only when its `Start` runs after leadership is acquired (via `SubscribeTypesLeaderOnly`, with a buffer of 1000 events) and SHALL unsubscribe when its loop exits. Before processing a trigger, the Coordinator SHALL non-blockingly drain all queued triggers and merge the run into a single render — one render after N triggers is equivalent to N serial renders because a render always reads the latest store state, and the collapse bounds the render rate under churn. If the first trigger or any drained trigger is non-coalescible, the merged trigger SHALL remain non-coalescible so the downstream deploy is not skipped.

#### Scenario: Queued triggers collapse into one render

- **WHEN** ten coalescible triggers are queued when the Coordinator finishes its current cycle
- **THEN** it SHALL drain all ten and execute exactly one render for them.

#### Scenario: Non-coalescible trigger survives the merge

- **WHEN** a drained run contains one non-coalescible trigger among coalescible ones
- **THEN** the merged trigger handed to the pipeline SHALL be non-coalescible.

#### Scenario: Followers hold no coordinator subscription

- **WHEN** a replica is not the leader
- **THEN** its Coordinator SHALL have no active subscription, so trigger volume on followers fills no coordinator buffer.

### Requirement: Synchronous Pipeline Execution and Success Fan-Out

On each merged trigger the Coordinator SHALL publish ReconciliationStartedEvent, then call `Pipeline.Execute` synchronously — render followed by validate, with no event hop between them (ADR-0001). The pipeline SHALL compute the content checksum exactly once, covering the rendered configuration plus all auxiliary files, and thread it through the result so no downstream consumer re-hashes the content. On success the Coordinator SHALL publish, in order: TemplateRenderedEvent (config, auxiliary files, status patches, rendered resources, checksum), ValidationCompletedEvent (carrying the pre-parsed configuration for downstream sync optimization), and ReconciliationCompletedEvent (carrying the rendered resources and status patches so the resource and status appliers stay stateless on the success path). The success events SHALL inherit the merged trigger's coalescibility.

#### Scenario: Render and validate run without an event hop

- **WHEN** the Coordinator handles a trigger
- **THEN** rendering and validation SHALL complete within the synchronous `Execute` call before any result event is published.

#### Scenario: Content checksum computed once

- **WHEN** a render succeeds
- **THEN** the checksum in TemplateRenderedEvent SHALL be the one computed inside the pipeline, and downstream deployment and publishing SHALL reuse it rather than recomputing.

### Requirement: Phase-Tagged Failure Fan-Out

When `Pipeline.Execute` fails, the Coordinator SHALL extract the failing phase from the structured pipeline error and publish a phase-specific failure event first: ValidationFailedEvent when the validation phase failed, TemplateRenderFailedEvent otherwise. It SHALL then publish ReconciliationFailedEvent carrying the phase and the most recent successful render's status patches, so the status applier can apply the failure-variant conditions; when no successful render has happened yet, the patches are absent and the applier skips.

#### Scenario: Validation failure publishes ValidationFailed

- **WHEN** the pipeline fails in the validation phase
- **THEN** the Coordinator SHALL publish ValidationFailedEvent followed by a ReconciliationFailedEvent with phase `validation`.

#### Scenario: Render failure publishes TemplateRenderFailed

- **WHEN** the pipeline fails in the render phase
- **THEN** the Coordinator SHALL publish TemplateRenderFailedEvent followed by a ReconciliationFailedEvent with phase `render`.

#### Scenario: Failure event carries last-good status patches

- **WHEN** a pipeline failure follows an earlier successful render
- **THEN** the ReconciliationFailedEvent SHALL carry that earlier render's status patches.

### Requirement: Dual Validation Pipelines

The controller SHALL build two render-validate pipelines sharing one render service but differing in validation strictness. The fast pipeline SHALL skip semantic validation (`haproxy -c`, saving roughly 94 ms per render) and SHALL drive the leader-side reconcile Coordinator — this is safe because every input reaching the leader has already passed strict validation upstream (admission webhook or HTTP-store promotion), and the Dataplane API runs its own `haproxy -c` server-side before accepting a raw config push. The strict pipeline SHALL run full semantic validation and SHALL drive the watched-resource admission webhook, the HAProxyTemplateConfig admission webhook, and HTTP-store content promotion — the only entry points for operator or third-party input. Both pipelines SHALL skip DNS validation, because hostname resolution is independently flaky and recovers at runtime (HAProxy starts unresolved servers DOWN and brings them up when a later health check resolves).

#### Scenario: Leader reconcile uses the fast pipeline

- **WHEN** the Coordinator executes a reconciliation
- **THEN** semantic validation (`haproxy -c`) SHALL be skipped for that render.

#### Scenario: Admission and promotion use the strict pipeline

- **WHEN** a watched-resource admission request, a HAProxyTemplateConfig admission request, or an HTTP-store content promotion is validated
- **THEN** the render SHALL pass full semantic validation before being accepted.

#### Scenario: DNS validation skipped everywhere

- **WHEN** either pipeline validates a rendered configuration containing unresolvable hostnames
- **THEN** the validation SHALL not fail on DNS resolution.
