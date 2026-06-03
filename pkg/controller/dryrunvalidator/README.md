# pkg/controller/dryrunvalidator

Webhook-side validator: implements the `DryRunValidator` interface that `pkg/controller/webhook` calls into when an admission request arrives.

## Overview

The validating admission webhook needs a synchronous answer to "would this proposed change render and validate cleanly?" — not via events, just a direct function call returning `allowed bool, reason string`. This component bridges that synchronous call into the controller's render-validate pipeline by:

1. Receiving the proposed object from the webhook adapter (`ValidateDirect`).
2. Building a `*stores.StoreOverlay` per admission verb (`NewStoreOverlayForCreate` / `…Update` / `…Delete`) and wrapping it in a `map[string]*stores.StoreOverlay` keyed by the resource type.
3. Delegating render+validate to `pkg/controller/proposalvalidator.Component`'s `ValidateSync(ctx, overlays)`, which merges the overlay on top of the live stores for the duration of the call.
4. Optionally dispatching the rendered file set to any configured pluggable validators (e.g. the SPOA hub in `--validate-socket` mode).
5. Returning a flat allow/deny + simplified reason string (plus soft warnings) for the webhook response.

The component does not subscribe to any events. It does **not** run the chart's embedded `validationTests` — those are chart-author scenarios with their own fixtures, run in CI via `haptic-controller validate` / `make test-templates`, not per admission request.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/dryrunvalidator"
)

component := dryrunvalidator.New(&dryrunvalidator.ComponentConfig{
    ProposalValidator:  proposalValidator,  // sync-mode *proposalvalidator.Component
    RESTMapper:         restMapper,
    Logger:             logger,
    PluggableValidator: pluggableValidator, // optional; nil disables sidecar dispatch
})

// pkg/controller/webhook hands this component as DryRunValidator and
// calls ValidateDirect synchronously per admission request. There is
// no Start() — the validator is a library, not a lifecycle component.
```

## Webhook Wiring

```go
// In iteration.go (sketch)
webhookComp := webhook.New(logger, &webhook.Config{
    // ... TLS material from the cert Secret loaded at controller startup,
    //     rules from ExtractWebhookRules ...
    DryRunValidator: dryRunComp, // <- this package's *Component
}, restMapper, metricsRecorder)
```

The webhook adapter then registers a `webhook.ValidationFunc` per GVK that pulls `(namespace, name, operation)` from the AdmissionRequest and calls `dryRunComp.ValidateDirect(ctx, gvk, namespace, name, object, operation)`.

## Failure Modes

- `(false, reason, nil)` — proposed change failed render or validation; the webhook denies and the API server forwards `reason` to the user.
- `(true, "", nil)` — proposed change is admissible with no warnings.
- `(true, "", warnings)` — proposed change is admissible; `warnings` are soft diagnostics from pluggable validators, surfaced via `AdmissionResponse.Warnings`.
- `ValidateDirect` returns `(allowed bool, reason string, warnings []string)`. Internal errors (e.g. a render panic) are logged and surface as a deny with a descriptive reason — the webhook never returns HTTP 500 from this path. Configure `failurePolicy: Fail` in the `ValidatingWebhookConfiguration` if you want the API server to reject on transport failures (TLS handshake, dial errors); deny-with-reason is for application-level rejections.

## See Also

- [`pkg/controller/webhook`](../webhook/) — HTTPS adapter that calls `ValidateDirect`
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — render-validate pipeline driven in sync mode
- [`pkg/stores`](../../stores/) — `NewStoreOverlayForCreate` / `NewStoreOverlayForUpdate` / `NewStoreOverlayForDelete` are what `createOverlay` actually calls per admission verb
- `pkg/controller/dryrunvalidator/CLAUDE.md` — design notes (overlay-store pattern, why direct calls are acceptable here)

## License

Apache-2.0 — see root `LICENSE`.
