# ADR-0019: Domain events require a payload consumer

## Status

Accepted.

## Context

The controller's commentator and debug buffer subscribe to the full event
stream. Their universal subscriptions made events with no functional consumer
look used even when they only repeated a log already emitted by the publisher.
That ambiguity caused recurring catalog audits without a stable deletion rule.

## Decision

A domain event requires a concrete production subscriber that uses its payload,
timing, or correlation. Generic tracing that records only the event type does
not qualify.

An observability subscriber qualifies when it produces an operator-visible log,
metric, or debug state that the publisher does not already emit. Otherwise the
publisher logs directly, increments the counter directly, or calls the single
callee directly.

An asynchronous boundary between components remains coordination even with one
publisher and one subscriber when the asynchrony is load-bearing, as in
ADR-0006.

## Consequences

- Catalog reviews inspect concrete consumers and the information they add.
- `CredentialsInvalidEvent`, `InstanceDeployedEvent`, and
  `HTTPResourceRejectedEvent` are removed because their publishers already emit
  stronger diagnostics.
- Events whose observability consumers derive cross-event context or expose
  otherwise unavailable payload remain valid coordination.

## Related

- ADR-0001 records the synchronous renderer boundary.
- ADR-0006 records the asynchronous HTTP proposal-validation boundary.
