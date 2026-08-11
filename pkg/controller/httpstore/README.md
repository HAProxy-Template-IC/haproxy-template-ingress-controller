# pkg/controller/httpstore

Event adapter around `pkg/httpstore.HTTPStore` plus the template-callable `HTTPStoreWrapper` that exposes `http.Fetch()` to Scriggo templates.

## Overview

Templates can pull external content via `{% var blocklist = http.Fetch("https://example.com/list.txt", {"delay": "5m"}) %}`. The pure store in `pkg/httpstore` handles fetching, caching, and the two-version pending/accepted lifecycle without knowing about the controller's event bus. This package is the event adapter that wraps the pure store with:

- A refresh timer per registered URL (driven by `delay` in the `http.Fetch` options).
- Proposal-validation handling: the component validates one immutable pending-content batch at a time. A matching completion finalizes only the URL versions in that batch; content refreshed during validation is queued in the next batch.
- Periodic eviction of cache entries that templates haven't touched recently (`evictionMaxAge`, typically `2 × dataplane.driftPreventionInterval`).
- Publishing `HTTPResourceUpdatedEvent` / `HTTPResourceAcceptedEvent` for accepted-state observability. Rejections are logged directly with the validation error, URL, and rejected and retained checksums.

The `HTTPStoreWrapper` is the template-side view: it implements the methods Scriggo calls into (`Fetch`, `Status`) and bridges them to the underlying component, including the `HTTPContentOverlay` used by the proposal validator to inject hypothetical content during admission.

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

The component runs on every replica (not leader-only) so all replicas have warm HTTP caches and the proposal validator on any pod can render templates that depend on remote content.

## Events

- Subscribes: `ProposalValidationCompletedEvent` (the only subscription — matches the active immutable batch by request ID, then promotes or rejects only its URL versions).
- Publishes: `ProposalValidationRequestedEvent` (asks the proposal pipeline to validate pending content), `HTTPResourceUpdatedEvent` (sibling observability event when a refresh produces new pending content), `HTTPResourceAcceptedEvent` (after validation promotes pending → accepted), and `ReconciliationTriggeredEvent("http_content_validated")` after a successful promotion so HAProxy picks up the new content. A rejected proposal is logged and discarded without publishing another event.

## Production vs Validation Render

The wrapper behaves differently depending on which render mode the engine is in:

| Mode | Returned content |
|------|------------------|
| Production render (reconciliation deploy path) | Accepted content only — never anything that hasn't been validated yet |
| Validation render (proposal pipeline / dry-run validator) | Pending content if available, otherwise accepted — so admission is testing the *proposed* state |

This is what makes the two-version cache useful: templates always render against either the safe-to-deploy state or the about-to-be-validated state, never a mix.

## See Also

- [`pkg/httpstore`](../../httpstore/) — pure two-version cache with `Fetch` / `RefreshURL` / `PromotePending` / `RejectPending`
- [`pkg/controller/renderer`](../renderer/) — production caller; the renderer sets the wrapper on the rendering context
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — uses the validation-mode behaviour for admission checks
- `pkg/controller/httpstore/CLAUDE.md` — refresh-timer design, eviction tuning

## License

Apache-2.0 — see root `LICENSE`.
