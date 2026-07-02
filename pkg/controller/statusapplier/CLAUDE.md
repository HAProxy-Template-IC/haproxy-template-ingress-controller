# pkg/controller/statusapplier - Status Patch Applier

Development context for the component that writes Kubernetes status
conditions for chart-managed resources via Server-Side Apply.

**API documentation**: see the package doc-comment in `component.go`.

## When to Work Here

Work in this package when:

- Changing how status patches are dispatched (which event triggers which variant)
- Adjusting the SSA field-manager scheme (the `haptic-rendered` /
  `haptic-deployed` / `haptic-deployFailed` / `haptic-renderFailed` split)
- Adjusting the checksum cache that deduplicates redundant SSA calls

**DO NOT** work here for:

- Deciding which status conditions to emit for a resource → that's the
  chart's templates (`charts/haptic/libraries/gateway/70-status-gateway.yaml`,
  `71-status-route.yaml`, etc.)
- Deciding when to emit a patch's "rendered" vs "deployed" variant →
  that's the chart's job too (per-variant payload keys in `statusPatch()`
  template-function calls)
- Adding a new event type the applier should consume → first add it to
  `pkg/controller/events` with `StatusPatches []templating.StatusPatch`
  on the payload, then wire the consumer here

## Architectural Contract

The applier is **stateless on the success path**. Patches travel on the
event that triggers each apply:

- `ResourcesAppliedEvent.StatusPatches` → `rendered` variant (published by
  the ResourceApplier AFTER the same render's `spec.k8sResources` were
  applied, so conditions never precede the infrastructure they describe)
- `DeploymentCompletedEvent.StatusPatches` → `deployed` variant
- `DeploymentSkippedEvent.StatusPatches` → `deployed` variant
- `ReconciliationFailedEvent.StatusPatches` → `renderFailed` or
  `deployFailed` variant (chosen by `event.Phase`)

There is no `cachedPatches` field or any other side-channel cache. Patches
on a deploy event are tautologically the patches for the configuration that
deploy carried — no LATEST-vs-deployed race is possible.

### Why this matters

The previous implementation kept a `cachedPatches` field overwritten on
every `TemplateRenderedEvent` and read on every deploy/failure event.
Under sustained render churn (e.g. the conformance suite's parallel-test
fixture churn), render N+1's patches could land in the cache before deploy
N's completion event fired — the applier then wrote `Programmed=True` for
resources whose config the just-completed deploy did NOT carry. The
one-shot mTLS conformance test `GatewayFrontendClientCertificateValidation
/Validate_default_configuration` triggered this race deterministically in
CI: tests dialled after seeing `Programmed=True`, hit a stale `*:443` bind
without per-listener `verify required`, and accepted the wrong client cert.

The fix moved patches onto the events themselves. The
`DeploymentScheduler` caches `lastValidatedStatusPatches` symmetric with
its existing `lastRenderedConfig` / `lastAuxiliaryFiles` cache and
forwards it onto every `DeploymentScheduledEvent` / `DeploymentSkippedEvent`.
The `Deployer` forwards `DeploymentScheduledEvent.StatusPatches` unchanged
onto `DeploymentCompletedEvent`. The `Coordinator` forwards
`lastSuccessfulPatches` onto `ReconciliationFailedEvent`. The applier
reads from each event and applies — no state, no race.

### Leader-only / leader-election

Only the leader applies patches (`c.isLeader` gate inside
`leaderRLocked()`). `handleBecameLeader` clears the SSA checksum cache so
the new leader writes at least once for every active resource on the next
reconciliation, but **does not replay anything**: the `Reconciler` triggers
an immediate reconciliation on `BecameLeaderEvent` (see
`pkg/controller/reconciler/CLAUDE.md`), producing a fresh
`TemplateRenderedEvent` carrying the patches the new leader needs. Replay
would be wrong here — at the moment of becoming leader, the applier has no
state to replay from (by design).

`handleLostLeadership` just flips the flag off; in-flight handler calls
re-check via `leaderRLocked()` on their next pass.

## SSA Field-Manager Scheme

Each phase owns its own SSA field manager (`fieldManagerPrefix` +
`"-" + phaseKey`):

- `haptic-rendered` — owns the conditions in the rendered variant
  (typically `Accepted`, `ResolvedRefs` — data-plane-independent)
- `haptic-deployed` — owns the conditions in the deployed variant
  (typically `Programmed` — data-plane-dependent)
- `haptic-renderFailed` — owns failure conditions when render failed
- `haptic-deployFailed` — owns failure conditions when deploy failed

The phase-scoped split is what lets the chart's rendered + deployed
variants coexist on the same resource without an SSA tug-of-war: each
phase claims ownership of disjoint condition entries (SSA's `listType=map`
keyed by `type`), so adding `Programmed` in the deployed variant doesn't
relinquish `Accepted` written by the rendered variant. Sharing a single
field manager across phases breaks this — the
`fieldManagerPrefix` constant doc-comment in `component.go` has the long
explanation.

## Checksum Cache

`Component.checksumCache` maps `"namespace/name/gvr"` to the SHA-256 of
the last successfully-applied patch payload. Before each SSA call, the
applier computes the payload checksum and skips if it matches; this
avoids cluster-amplification when the same patches are re-emitted on
every reconcile.

The cache is cleared on `BecameLeaderEvent` (the previous leader may have
applied different checksums under its own field manager), and on apply
failures (so the next reconcile retries instead of silently skipping).

## Tests

Patterns:

- **Per-event handler tests** (`TestHandleDeploymentCompleted_…`,
  `TestHandleDeploymentSkipped_…`, etc.): construct the event with
  inline patches, call the handler directly, assert
  `StatusUpdateCompletedEvent` is or isn't published with the expected
  phase + applied/skipped counts. Tests are deliberately small and
  per-branch (one event shape, one expected behaviour) so a regression
  in one branch surfaces with a clear failure name.
- **Defensive-copy tests** live in the events package, not here — see
  `pkg/controller/events/deployment_completed_test.go` /
  `deployment_skipped_test.go` / `deployment_scheduled_test.go` /
  `reconciliation_failed_test.go`. The applier doesn't need to defend
  against caller mutation because it doesn't store patches.
- **Leader-transition tests** (`TestHandleBecameLeader_…`,
  `TestLeadershipTransition_FullCycle`): pin that becoming leader does
  NOT replay patches (the Reconciler's fresh-reconcile trigger does the
  equivalent), and that losing leadership stops further applies.

## Resources

- Event payload contracts: `pkg/controller/events/CLAUDE.md`
- Chart-side patch shape: `charts/haptic/libraries/gateway/70-status-gateway.yaml`
  (Gateway) and `71-status-route.yaml` (HTTPRoute/GRPCRoute/TLSRoute)
- Reconciler's BecameLeader-fresh-reconcile contract:
  `pkg/controller/reconciler/CLAUDE.md`
