# ADR-0024: Resource application backpressures the next render

## Status

Accepted 2026-09-10.

## Context

Bulk deletion in the 5,000-route scale run made rendering faster than resource
application. The resource applier's independently drained mailbox avoided event
loss, but interleaved render-gate verdicts prevented consecutive completion
coalescing. Its queue repeatedly reached 256 entries while each cycle performed
live Kubernetes API operations.

Dropping gate verdicts would lose hold, release, and rollback transitions.
Skipping unchanged local resource payloads would miss drift or recreation of
unwatched targets. Neither is an acceptable queue bound.

## Decision

The coordinator publishes one render occurrence and waits until the resource
applier acknowledges handling it before starting another render. Trigger intake
continues independently, preserving the existing first-forced/latest-ordinary
coalescing rule and every gate boundary.

`ResourcesProcessedEvent` carries the authenticated occurrence, not a content
digest or public render proof. A receipt for another occurrence cannot release
the wait, even when both occurrences share the same snapshot. The coordinator
subscribes before publishing and removes the subscription when its leader term
ends. Cancellation ends the wait without producing another render.

The applier acknowledges successful, failed, and gate-held handling. Acknowledging
only success would deadlock the next reconciliation that retries a failed apply.
The acknowledgement does not replace `ResourcesAppliedEvent`: rendered status
still requires successful application and pruning of the same occurrence.

## Consequences

- At most one coordinator-produced resource cycle awaits processing.
- Every handled cycle retains live SSA, exact deletion lineage, and gate order.
- No validation moves or disappears. Admission, startup validation, asynchronous
  HAProxy checks, and gate rollback retain their existing contracts.
- The current cycle still renders and deploys without waiting for resource
  application. A subsequent change can wait for the preceding API pass; this is
  explicit backpressure rather than a timer, retry delay, or larger queue.
- Resource-application failure still reaches the existing error diagnostics and
  retries on the next reconciliation; it cannot produce success status.

## Verification

Tests pin blocked-apply pacing, continued trigger intake, exact occurrence
matching, failed and held receipts, successful-status separation, and cancellation.
The decisive integration check is a fresh 5,000-route run including bulk teardown;
its measured-window pass alone does not establish a clean lifecycle.
