# pkg/controller/pipeline

Pure render-validate pipeline composing `renderer.RenderService`, `validation.ValidationService`, and an optional rendered-output validator into a single workflow.

## Overview

Leader reconciliation and the proposal validator used by watched-resource admission and HTTP-store promotion feed the same code path through this package, so they enforce the same output gates.

`*Pipeline` has no event-bus dependency — it's a synchronous service that takes a `stores.StoreProvider`, returns a `*PipelineResult`. Callers wrap it in their own event handling.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
)

renderSvc := renderer.NewRenderService(/* engine, config, paths, capabilities, ... */)
validateSvc := validation.NewValidationService(/* paths, capabilities, ... */)

pl := pipeline.New(&pipeline.PipelineConfig{
    Renderer:        renderSvc,
    Validator:       validateSvc,
    OutputValidator: pluggableValidators, // optional
    Logger:          logger,
})

result, err := pl.Execute(ctx, storeProvider, rendercontext.RenderModeReconcile)
```

The `*PipelineResult` carries everything downstream consumers need without
re-running render or validate:

- `HAProxyConfig` (string), `AuxiliaryFiles` (`*dataplane.AuxiliaryFiles`)
- `StatusPatches []templating.StatusPatch` — registered by templates during the render
- `AuxFileCount int` — convenience aggregate
- `ContentChecksum string` — pre-computed checksum of config + aux files (see below)
- `RenderDurationMs`, `ValidateDurationMs`, `TotalDurationMs` — phase timings
- `ValidationPhase string` — last completed validation phase (empty if all passed)
- `ValidationWarnings []string` — non-fatal rendered-output diagnostics
- `ParsedConfig *parser.StructuredConfig` — see "Pre-Parsed Config Optimisation"

`pipeline.New` panics if `Renderer` or `Validator` is nil — these are required dependencies and a missing one is a configuration bug, not a runtime error to be surfaced later.

### Entry Points

| Method | Use case |
|--------|----------|
| `Execute(ctx, provider, mode, extraOpts...) (*PipelineResult, error)` | Standard reconciliation / proposal validation. Render + validate. |
| `ExecuteWithResult(ctx, provider, mode, extraOpts...) (*PipelineResult, *validation.ValidationResult, error)` | Same render+validate flow but also returns the raw `*validation.ValidationResult` so callers can inspect warnings / phase details without parsing the wrapped error. Used by the proposal validator's webhook path. |

## What `Execute` Does

1. **Render** — calls the render service with the supplied store provider. The render mode (production vs validation) is auto-detected: if the provider is an `*OverlayStoreProvider` with overlays, it's a validation render; otherwise it's a production render.
2. **Compute checksum** — `dataplane.ComputeContentChecksum(config, auxFiles)` runs once and is propagated through `PipelineResult.ContentChecksum`. Downstream consumers (publishing, deployment) reuse it instead of re-hashing.
3. **Validate** — calls the three-phase validation service with the rendered config + aux files + pre-computed checksum (so the validation cache key is consistent across callers).
4. **Validate rendered output** — after the built-in phases pass, calls the optional validator with the complete rendered file set. Errors use validation phase `external`; warnings remain on the result.
5. **Wrap errors** — failures come back as `*PipelineError` with `Phase` (`render` / `validation`) and, for validation, `ValidationPhase` (`syntax` / `schema` / `semantic` / `external`). Use `errors.As` to pull the phase out instead of string-matching the message.

## Pre-Parsed Config Optimisation

`PipelineResult.ParsedConfig` carries the `*parser.StructuredConfig` produced during syntax validation. Downstream sync operations (`pkg/dataplane.Client.Sync`) accept it as input to skip a redundant parse. When the validation cache hits, this field is nil — the cached entry doesn't carry the parsed structure.

## See Also

- [`pkg/controller/renderer`](../renderer/) — the render service this pipeline drives (event adapter and pure service both live there)
- [`pkg/controller/validation`](../validation/) — the three-phase validation service this pipeline drives
- [`pkg/controller/pluggablevalidator`](../pluggablevalidator/) — the production rendered-output validator
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — webhook-side caller (sync mode)
- [`pkg/controller/reconciler`](../reconciler/) — reconciliation-side caller (driven by `Coordinator`)

## License

Apache-2.0 — see root `LICENSE`.
