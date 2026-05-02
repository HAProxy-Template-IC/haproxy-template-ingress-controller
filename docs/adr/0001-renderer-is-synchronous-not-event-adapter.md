# ADR-0001: Renderer is synchronous, not an event adapter

## Status

Accepted.

## Context

Most controller components follow the event-adapter pattern: subscribe to a trigger event, call into a pure component, publish a result event. The renderer once followed this shape — a `renderer.Component` subscribed to `ReconciliationTriggeredEvent` and published `TemplateRenderedEvent`.

That shape was abandoned in favour of a synchronous `renderer.RenderService` invoked directly by the render-validate pipeline (`pkg/controller/pipeline.Pipeline`). The leader-only `reconciler.Coordinator` drives the pipeline and publishes `TemplateRenderedEvent` itself once the synchronous render returns.

## Decision

The renderer is a **library**, not a component. New code calls `RenderService.Render(ctx, storeProvider)` directly. There is no event hop between "I want to render" and "I have rendered output."

The legacy `renderer.Component` and its test files have been removed. The validator tests that previously used it as a fixture publish `TemplateRenderedEvent` directly with synthetic content.

## Consequences

- The render-validate pipeline runs as a single synchronous unit on the leader. There is no race between `TemplateRenderedEvent` and `ValidationCompletedEvent`; they are published in causal order by the Coordinator.
- Locality wins: the "render → validate → publish results" sequence lives in one orchestrator (`Coordinator`), not three event handlers chasing each other across the bus.
- The renderer cannot be observed via the event bus before validation. If you want observability of just-rendered output, subscribe to `TemplateRenderedEvent` (still published) — but understand it fires after both render and validation have completed.

## Do not re-suggest

A future architecture review may notice that "every other component is an event adapter, why is the renderer a library?" and propose re-introducing `renderer.Component` for symmetry. **Do not.** The synchronous pipeline is deliberate. Symmetry is not load-bearing; the event hop was removed because it added latency on the hot path and made the render-validate sequence harder to reason about. If you have a use case that genuinely needs an event-adapted renderer, propose a new ADR superseding this one with the concrete requirement.

## Related

- The dry-run validator (`pkg/controller/dryrunvalidator`) followed the same shape: a `WebhookValidationRequest` scatter-gather subscription was kept "for future scatter-gather observability" with no production publisher. It was deleted on the same principle — no event hop where there is no second participant. Production calls `ValidateDirect` synchronously.
