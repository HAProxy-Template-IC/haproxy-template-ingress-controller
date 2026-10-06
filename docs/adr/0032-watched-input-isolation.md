# ADR-0032: Keep rejected watched inputs out of independent updates

## Status

Accepted 2026-10-06.

## Problem

A render reads one combined view of watched Kubernetes resources. A template
error or invalid HAProxy fragment in one resource prevents that view from
producing deployable output. Retaining only the previous output also retains its
endpoint addresses. Subsequent endpoint changes encounter the same bad input,
and admission encounters it when checking unrelated proposals.

## Decision

Separate observed resource revisions from accepted resource revisions. A revision
becomes accepted only after the complete render, HAProxy validation, and configured
auxiliary-output validators succeed. The asynchronous render gate and agent-side
validation remain in place.

Reconciliation first checks all observed changes together. If that fails, it
revalidates the accepted baseline and checks smaller groups of changes against
that baseline. Valid groups advance; invalid groups retain their previous accepted
revision. A new invalid object has no accepted revision and remains inactive.
Captured changes to aliases and API versions of one Kubernetes object are selected
as a group. Rejected groups are retried after other groups advance. If dependent changes straddle a split, the selector
also validates the remaining changes with each rejected object withheld. Every
accepted combination still passes complete validation.

At startup, the complete observed view is tried first. An empty view can seed
isolation only if it passes every validator. When required dependencies make the
empty view invalid, the selector also checks the observed view with each individual
object withheld. If no validated baseline can be established, reconciliation
reports the failure and publishes nothing. A template or global configuration
error that remains without the offending object is not a resource-local failure.

Admission checks the observed view first. If that fails, it substitutes retained
revisions only for the exact changes already rejected by reconciliation. It keeps
every other observed change, including valid updates that have not reconciled yet
and repaired revisions of previously rejected objects. The proposed object must
pass complete validation against this view; matching errors or unchanged invalid
output cannot authorize it. An unrelated request can succeed without a full API
refresh, while a conflict with a newly observed valid object remains rejected.
The existing fresh-API retry still handles missing dependencies. HTTP-content
validation uses accepted watched inputs once they are available.

## Resource snapshots

`SnapshotBranch` retains selected immutable resource revisions with its own exact
revision journal. Its range protocol verifies ancestry at both endpoints, so
atomic batches and unselected sibling trials do not force a complete render.
When admission holds an older or divergent branch, the renderer compares the
actual immutable roots from the same source. This preserves change-local evaluation
without claiming that a sibling is a journal ancestor.
Full-store values share owned immutable data. On-demand values
remain lazy; once read, their exact bytes remain available even after the live
object changes. Selecting inputs does not list Secret bodies or eagerly fetch
other on-demand resources. Reads still return detached values to callers.

A trial that newly references an unread retained revision can find that the API
no longer serves it. That trial fails validation; other groups can still advance
against the validated baseline. A snapshot race while checking the complete
observed view or the baseline still aborts selection and requires fresh inputs.

Input snapshots are pinned under ordered store fences. Trial branches cannot
mutate the observed stores or the accepted branch. A sibling trial is not a valid
journal ancestor. Admission projects observed inputs into the same revision family
as reconciliation so it can reuse the incremental renderer's existing machinery.

The selector contains no resource kinds or routing paths. Templates continue to
define resource-specific behavior, including what a missing dependency means.

## Validation delta

Previously, reconciliation could send a newly observed input's rendered output to
agents before the asynchronous HAProxy verdict arrived. The accepted-input boundary
requires synchronous success from every output validator before that input can
advance. No validator, admission rule, assertion, or agent check is removed.

The previous accepted behavior can remain active while a replacement or deletion
is rejected. This includes credentials and access policies. Rejection is not a
successful rollout of the intended change. Operators can inspect the rejected
resources through `inputRejections` and alert on `haptic_rejected_watched_inputs`.

Accepted input state is local to a controller iteration. Startup must establish
a validated view again; it does not reconstruct old resource objects from rendered
HAProxy output. A cold controller can omit routes that depend on a rejected
resource while independent valid routes continue updating.
