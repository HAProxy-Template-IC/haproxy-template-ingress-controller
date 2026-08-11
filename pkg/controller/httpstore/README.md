# pkg/controller/httpstore

Event adapter around `pkg/httpstore.HTTPStore` plus the template-callable `HTTPStoreWrapper` that exposes `http.Fetch()` to Scriggo templates.

## Overview

Templates can pull external content via `{% var blocklist = http.Fetch("https://example.com/list.txt", {"delay": "5m"}) %}`. The pure store in `pkg/httpstore` handles fetching, caching, and the two-version pending/accepted lifecycle without knowing about the controller's event bus. This package is the event adapter that wraps the pure store with:

- A refresh timer per registered URL (driven by `interval` in the `http.Fetch` options).
- Source reconciliation on authoritative live-render calls, so credential changes refetch and interval changes replace or stop the timer.
- A render-local input transaction for cold or replaced sources. It memoizes each source for the render and accepts the complete candidate set only after the exact output passes the full pipeline.
- Proposal-validation handling: the component validates one immutable pending-content batch at a time. A matching completion finalizes only the URL versions in that batch; content refreshed during validation is queued in the next batch.
- Periodic eviction of cache entries that templates haven't touched recently (`evictionMaxAge`, typically `2 × dataplane.driftPreventionInterval`).
- Publishing `HTTPResourceUpdatedEvent` / `HTTPResourceAcceptedEvent` for accepted-state observability. Rejections are logged directly with the validation error, URL, and rejected and retained checksums.

The `HTTPStoreWrapper` is the template-side view: it implements the methods Scriggo calls into (`Fetch`, `Status`) and bridges them to the underlying component. An authoritative wrapper owns one input transaction. The renderer hands that transaction to the pipeline, which aborts it on any render, validation, cancellation, or commit-fence failure and commits it only after built-in and configured output validation succeed.

One wrapper represents one render and accepts only one authentication and option set per URL. Live reconciliation wrappers are authoritative: a later live render may replace a declaration, and cache and timer generations fence work from the retired declaration. A cold or replaced response remains outside shared accepted and pending state until the exact complete pipeline commits every candidate atomically. Validation and source-map wrappers are read-only. They reuse matching overlay or accepted bytes and fetch other declarations into a store owned by that render.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"

evictionMaxAge := 2 * cfg.Dataplane.GetDriftPreventionInterval()
hsComponent := httpstore.New(eventBus, logger, evictionMaxAge)
go hsComponent.Start(ctx)

// Then pass the component to the renderer via RenderServiceConfig:
// renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
//     ...
//     HTTPStoreComponent: hsComponent,
// })
```

The component runs on every replica (not leader-only). Live reconciliation establishes shared source authority. A successful pipeline commit accepts new bytes and arms their timers; validation on any replica can fetch a cache miss without mutating shared state.

## Events

- Subscribes: `ProposalValidationCompletedEvent` (the only subscription — matches the active immutable batch by request ID, then promotes or rejects only its URL versions).
- Publishes: `ProposalValidationRequestedEvent` (asks the proposal pipeline to validate pending content), `HTTPResourceUpdatedEvent` (sibling observability event when a refresh produces new pending content), `HTTPResourceAcceptedEvent` (after validation promotes pending → accepted), and `ReconciliationTriggeredEvent("http_content_validated")` after a successful promotion so HAProxy picks up the new content. A rejected proposal is logged and discarded without publishing another event.

These events belong only to periodic refreshes. Initial authoritative candidates are finalized synchronously by their own pipeline transaction.

## Render source modes

The renderer assigns source authority explicitly for each operation:

| Mode | Returned content | Shared source and timer |
|------|------------------|-------------------------|
| Live reconciliation | Matching accepted content, otherwise one render-local candidate | Source reconciled immediately; candidate accepted and timer armed only after full pipeline success |
| Validation | Matching pending overlay, then matching accepted content, then a render-local fetch | Unchanged |
| Source-map introspection | Matching overlay when supplied, then accepted content, then a render-local fetch | Unchanged |

This keeps a rejected hypothetical resource from replacing the live fetch credentials, accepted body, or refresh timer. It also keeps a cold live response out of shared accepted state until the exact rendered output succeeds. HTTP periodic-refresh validation still sees the exact pending version through its overlay.

## See Also

- [`pkg/httpstore`](../../httpstore/) — pure candidate admission and two-version refresh cache
- [`pkg/controller/renderer`](../renderer/) — production caller; the renderer sets the wrapper on the rendering context
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — uses the validation-mode behaviour for admission checks
- `pkg/controller/httpstore/CLAUDE.md` — refresh-timer design, eviction tuning

## License

Apache-2.0 — see root `LICENSE`.
