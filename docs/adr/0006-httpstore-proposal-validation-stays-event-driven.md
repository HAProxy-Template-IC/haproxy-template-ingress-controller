# ADR-0006: HTTP store ↔ proposal validator stays event-driven

## Status

Accepted.

## Context

`pkg/controller/httpstore.Component` runs a refresh-timer loop: per registered URL, when the refresh interval elapses it fetches the URL, and on a content change publishes `ProposalValidationRequestedEvent`. `pkg/controller/proposalvalidator.Component` consumes that event, runs render-validate against the proposed content via its `HTTPOverlay`, and publishes `ProposalValidationCompletedEvent`. `httpstore` correlates the response by `RequestID` against its `pendingValidationID`, then either promotes the pending content and publishes `HTTPResourceAcceptedEvent`, or discards it and logs the rejection directly.

That shape is suspicious on first inspection — one publisher, one subscriber-back, closed loop. ADR-0001 removed an analogous event hop for the renderer. A future architecture review will likely propose collapsing this one too: drop the events, have `httpstore` call `proposalValidator.ValidateSync(ctx, overlays)` directly the way `dryrunvalidator` already does.

## Decision

The event hop stays. `httpstore` continues to publish `ProposalValidationRequestedEvent` and react to `ProposalValidationCompletedEvent`; it does not call `ValidateSync` directly.

## Consequences

- The refresh-timer goroutine never blocks on validation. Render+validate can take seconds (template rendering plus three-phase HAProxy validation); converting to a direct call would either block the timer loop for that duration or require a goroutine + result channel — i.e. reinventing the bus.
- `httpstore` is already deeply on the bus for unrelated reasons: it publishes `HTTPResourceUpdated` / `HTTPResourceAccepted` / `HTTPResourceRejected` (consumed by the commentator and metrics) and `ReconciliationTriggered` (consumed by the reconciler). Carving out only the proposal-validator hop would create local inconsistency inside the component, which is the opposite of what we want.
- The cost is real: ~50 LOC of `pendingValidationID` mutex juggling, plus three event types kept alive. We accept that cost in exchange for the non-blocking refresh loop.

## Do not re-suggest

A future architecture review applying ADR-0001's "no event hop without a second participant" principle will notice that the publisher/subscriber loop here has exactly one of each. **Do not** propose collapsing it to a synchronous call. The async coupling is load-bearing — it decouples refresh-timer cadence from validation latency. ADR-0001 applies to event hops that exist for symmetry; this hop exists for asynchrony. Different problem.

If you have a concrete use case that requires synchronous coupling here (e.g. a redesign that eliminates the refresh-timer loop), propose a new ADR superseding this one with the requirement.

## Related

- ADR-0001 — establishes the "no event hop without a second participant" principle that this ADR explicitly carves an exception to.
- The manual `pendingValidationID` correlation in `httpstore.handleProposalValidationCompleted` reinvents request/response correlation. `EventBus.Request()` is the wrong tool (it's synchronous-blocking); what's missing from `pkg/events` is an *async* correlation primitive — "publish a request, react to its response on my own event loop." A future change introducing such a primitive would simplify `httpstore` without touching this ADR's decision.
