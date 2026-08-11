# pkg/controller/proposalvalidator

Speculative render+validate of a hypothetical configuration change without deploying it. Composes a `BaseStoreProvider` (the live watched-resource stores) with caller-supplied overlays, drives `pkg/controller/pipeline.Pipeline` against the merged view, and reports the outcome.

## Overview

Two production paths need this:

| Caller | Mode | What's overlaid |
|--------|------|-----------------|
| `pkg/controller/dryrunvalidator` (admission webhook) | Sync — direct `ValidateSync` call | One `*stores.StoreOverlay` per affected resource type, built from the admission verb (CREATE / UPDATE / DELETE) |
| `pkg/controller/httpstore` (background HTTP content refresh) | Async — `ProposalValidationRequestedEvent` | An `HTTPContentOverlay` containing newly fetched pending content; no K8s overlays |

Both flows go through the same `Pipeline.Execute`, so anything that passes here will also pass leader-side reconciliation. Reconciliation itself does **not** use this component — the leader-only Coordinator calls `Pipeline.Execute` directly without going through proposalvalidator.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
)

pl := pipeline.New(/* renderer + validator services + paths */)

// Sync mode (no event subscription; caller invokes ValidateSync directly).
sync := proposalvalidator.New(&proposalvalidator.ComponentConfig{
    Pipeline:          pl,
    BaseStoreProvider: storeProvider,
    Logger:            logger,
    SyncOnly:          true,
})

overlays := map[string]*stores.StoreOverlay{
    "ingresses": stores.NewStoreOverlayForCreate(newIngressObj),
}
pipelineResult, result := sync.ValidateSync(ctx, overlays) // (*pipeline.PipelineResult, *validation.ValidationResult)

// Async mode (subscribes to ProposalValidationRequestedEvent during construction).
async := proposalvalidator.New(&proposalvalidator.ComponentConfig{
    EventBus:          eventBus,
    Pipeline:          pl,
    BaseStoreProvider: storeProvider,
    Logger:            logger,
})
go async.Start(ctx)
```

`ValidateSync` returns `(*pipeline.PipelineResult, *validation.ValidationResult)`. The `PipelineResult` carries the rendered HAProxy config and auxiliary files (populated only on success); the `*validation.ValidationResult` carries the validation outcome:

```go
type ValidationResult struct {
    Valid        bool
    Error        error
    Phase        string                   // failing pipeline subphase; empty when valid
    DurationMs   int64
    ParsedConfig *parser.StructuredConfig // pre-parsed; downstream sync can skip the parse
}
```

The async path publishes `ProposalValidationCompletedEvent` (or its failure variant) keyed by the request ID so multiple in-flight proposals don't get correlated incorrectly.

Cancellation denies the proposal and retains the most specific pipeline phase.
It cannot use the unchanged-invalid recovery exception.

## How It Works

1. The caller hands over `overlays map[string]*stores.StoreOverlay` (one per resource type they want to perturb), and optionally an HTTP overlay for pending HTTP content.
2. The component wraps `BaseStoreProvider` in an `OverlayStoreProvider` so the pipeline sees the merged view: live store + overlay on top.
3. `Pipeline.ExecuteWithResult(ctx, mergedProvider)` runs the full render + validation pipeline against that view.
4. Failures come back as `*PipelineError` (with `Phase` + `Cause`); the simplification helpers in `pkg/dataplane` (`SimplifyRenderingError` / `SimplifyValidationError`) turn the underlying library error into something a webhook user can act on.

Because the merged view exists only for the duration of the call, a successful proposal validation never mutates live store state.

## See Also

- [`pkg/controller/pipeline`](../pipeline/) — the underlying render-validate composition this component drives
- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) — sync-mode caller (admission webhook)
- [`pkg/controller/httpstore`](../httpstore/) — async-mode caller (background HTTP content refresh)
- [`pkg/stores`](../../stores/) — `NewStoreOverlayForCreate` / `…Update` / `…Delete` overlay constructors
- [`pkg/controller/events`](../events/) — `ProposalValidationRequestedEvent` / `ProposalValidationCompletedEvent`

## License

Apache-2.0 — see root `LICENSE`.
