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

### Requirement: Pipeline Authority Is Fail-Closed

The pipeline SHALL check `context.Cause` before and after each render and
validation stage and immediately before returning success. Cancellation SHALL
return a phase-tagged `PipelineError` that wraps the context cause. The
Coordinator SHALL publish no success or failure result events after its leader
term context is canceled.

#### Scenario: Output validator cancels its context

- **WHEN** a rendered-output validator cancels the pipeline context and returns no diagnostic error
- **THEN** the pipeline SHALL return a validation-phase error wrapping the cancellation cause.

#### Scenario: Admission baseline comparison loses authority

- **WHEN** proposal validation fails and the context is canceled while checking whether the live baseline has identical invalid content
- **THEN** admission SHALL preserve the cancellation cause and deny the proposal instead of applying the unchanged-invalid exception.

#### Scenario: Leader term ends during pipeline execution

- **WHEN** the Coordinator's pipeline returns after the leader term context is canceled
- **THEN** the Coordinator SHALL discard both successful and failed results without publishing result events.

### Requirement: Leader-Term Current Files Authority

The Coordinator SHALL render with an immutable `currentFiles` snapshot owned by its current leader term. Before the term accepts a render, the snapshot SHALL come from one completely resolved auxiliary reference set committed in the watched `HAProxyCfg` status. Resolution SHALL verify every referenced child's name, namespace, and set ID, including certificate Secret metadata, without reading Secret data into `currentFiles`. Secret metadata SHALL retain a checksum and resource-version mutation identity so an in-place legacy Secret update is detectable without retaining its data. Individually written children and incomplete modern publications SHALL remain invisible, and the last complete snapshot SHALL remain authoritative until every child named by a newer set ID is available. An initial complete publication without a set ID MAY be accepted for rolling-upgrade compatibility, but any later change to its referenced children or references SHALL make published `currentFiles` unavailable until a complete set-ID publication is committed. After any complete set-ID publication has been accepted, a missing parent or a parent without a set ID SHALL fail closed and SHALL NOT restore legacy mode. Reconciliation, admission, and other proposal validation SHALL fail closed while published `currentFiles` is unavailable, even when the leader has locally accepted newer auxiliary bytes. After a render passes validation, the Coordinator SHALL synchronously promote that render's map, general-file, and crt-list output before publishing result events. A failed render SHALL NOT advance the snapshot, and output completing for a retired leader term SHALL NOT replace the active term's snapshot. Admission and other all-replica proposal validation SHALL pin one published auxiliary-file snapshot across the complete decision and SHALL NOT depend on leader-only accepted state. User extra context SHALL NOT replace the authoritative `currentFiles` value. StateCache is observability-only and SHALL NOT provide reconciliation input.

#### Scenario: Back-to-back renders use the accepted output

- **WHEN** a second trigger is handled before observers consume the first render's events
- **THEN** the second render SHALL receive the first successfully validated auxiliary output in `currentFiles`.

#### Scenario: Retired term cannot advance currentFiles

- **WHEN** an old leader's pipeline returns after a newer leader term begins
- **THEN** its auxiliary output SHALL be discarded from the active term's `currentFiles` authority.

#### Scenario: Admission ignores leader-local accepted output

- **WHEN** a leader has accepted auxiliary output that differs from the latest published output CRDs
- **THEN** watched-resource admission on every replica SHALL evaluate `currentFiles` from the published snapshot.

#### Scenario: Partial publication retains the committed snapshot

- **WHEN** any child from a newer auxiliary set is written or a newer committed reference set cannot be completely resolved
- **THEN** `currentFiles` SHALL retain the preceding complete committed snapshot.

#### Scenario: Complete publication advances atomically

- **WHEN** the watched `HAProxyCfg` commits a new set ID and every referenced child, including certificate Secret metadata, carries that set ID in the same namespace
- **THEN** `currentFiles` SHALL advance to all referenced map, general-file, and crt-list content as one snapshot.

#### Scenario: A legacy publication changes after bootstrap

- **WHEN** a referenced child or reference changes after a publication without a set ID supplied the initial snapshot
- **THEN** reconciliation and proposal validation SHALL reject rendering until a complete publication with a set ID is committed.

#### Scenario: Published authority fails after a leader-local acceptance

- **WHEN** the leader has accepted auxiliary output and the published legacy snapshot then becomes unavailable
- **THEN** reconciliation SHALL reject rendering rather than use the leader-local bytes.

#### Scenario: A modern publication loses its set ID

- **WHEN** a complete set-ID publication was accepted and the watched parent is later absent or has no set ID
- **THEN** published `currentFiles` SHALL remain unavailable until another complete set-ID publication is committed.

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

The controller SHALL build two render-validate pipelines sharing one render service but differing in validation strictness. The strict pipeline SHALL run full semantic validation (`haproxy -c`) and SHALL drive the watched-resource admission webhook, HTTP-store content promotion, and the FIRST render of each iteration's Coordinator. The fast pipeline SHALL skip semantic validation (saving roughly 94 ms per render) and SHALL drive every later leader-side render — safe because a watched-resource change reaching the leader has already passed strict validation at admission, while a config change restarts the iteration and therefore lands on the strict first render. (Config changes have no admission gate since ADR-0016 — a per-object webhook cannot judge a multi-object change set — and the chart's default `validateConfig: false` renders the Dataplane API's own check as `/bin/true`, so the strict first render is the semantic gate for them, not a redundancy.) Both pipelines SHALL skip DNS validation, because hostname resolution is independently flaky and recovers at runtime (HAProxy starts unresolved servers DOWN and brings them up when a later health check resolves).

#### Scenario: First render of an iteration uses the strict pipeline

- **WHEN** the Coordinator executes its first reconciliation after iteration start — which is what a config change, a controller start, or a leader transition produces
- **THEN** that render SHALL pass full semantic validation (`haproxy -c`), and the outcome SHALL NOT change which pipeline later renders use.

#### Scenario: Later leader renders use the fast pipeline

- **WHEN** the Coordinator executes any subsequent reconciliation in the same iteration
- **THEN** semantic validation (`haproxy -c`) SHALL be skipped for that render.

#### Scenario: Admission and promotion use the strict pipeline

- **WHEN** a watched-resource admission request or an HTTP-store content promotion is validated
- **THEN** the render SHALL pass full semantic validation before being accepted.

#### Scenario: DNS validation skipped everywhere

- **WHEN** either pipeline validates a rendered configuration containing unresolvable hostnames
- **THEN** the validation SHALL not fail on DNS resolution.
