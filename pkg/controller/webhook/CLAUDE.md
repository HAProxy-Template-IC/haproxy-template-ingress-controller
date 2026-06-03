# pkg/controller/webhook - Webhook Adapter Component

Development context for the webhook adapter component.

**API Documentation**: See `pkg/controller/webhook/README.md`
**Pure Library**: See `pkg/webhook/CLAUDE.md`

## When to Work Here

Modify this package when:

- Changing the bridge between the pure webhook server and the dry-run validator
- Adding or adjusting per-GVK registration logic
- Wiring metric or log signals around webhook requests

**DO NOT** modify this package for:

- HTTPS server / `RegisterValidator` / `Start` mechanics (including cert hot-reload from `CertDir`) → `pkg/webhook`
- TLS certificate provisioning → the chart's cert-manager `Certificate`/Secret plumbing and the mount the controller points `CertDir` at
- `ValidatingWebhookConfiguration` lifecycle → handled outside the controller (chart + cluster admin)
- Validation business logic (overlay stores, render+validate) → `pkg/controller/dryrunvalidator`

## Package Purpose

Thin glue between three things:

1. The pure HTTPS server in `pkg/webhook` (handles TLS, AdmissionReview decode/encode, validator dispatch).
2. The `DryRunValidator` interface (synchronous `ValidateDirect` call into the validation pipeline).
3. The controller's metrics surface (per-GVK request and decision counters).

It does **not** generate or fetch certificates, manage `ValidatingWebhookConfiguration`, or publish webhook-lifecycle events. The mounted cert directory comes into the component via `Config.CertDir`, and the pure server reads + hot-reloads the cert files from it; the chart owns the `ValidatingWebhookConfiguration` and the CA-bundle injection.

## Architecture Pattern

```
                   pkg/webhook (pure)            pkg/controller/webhook
                   ┌──────────────┐              ┌──────────────────────┐
TLS request ─────► │ Server       │ ──register── │ Component            │
                   │ - HTTPS+AR   │              │ - Per-GVK            │
                   │ - dispatch   │ ◄──validate──│   ValidationFunc     │
                   └──────────────┘              │ - calls DryRun-      │
                                                 │   Validator.Validate │
                                                 │   Direct(ctx, ...)   │
                                                 └──────────┬───────────┘
                                                            │
                                                            ▼
                                              pkg/controller/dryrunvalidator
                                              (overlay stores, render+validate
                                               via proposalvalidator)
```

## Component Lifecycle

### Construction

```go
component := webhook.New(logger, &webhook.Config{
    Port:            9443,
    Path:            "/validate",
    CertDir:         "/etc/webhook/certs", // mounted cert Secret; server reads + hot-reloads tls.crt/tls.key
    Rules:           rules,               // []webhook.WebhookRule -- per-GVK list
    DryRunValidator: dryRunComponent,     // implements ValidateDirect
}, restMapper, metricsRecorder)
```

There are no `Namespace`, `ServiceName`, or `CABundle` fields on the config — those concerns live in the Helm chart's `ValidatingWebhookConfiguration` and the chart-managed Secret.

### Start

`Start(ctx)` instantiates the underlying `*pkg/webhook.Server`, registers one bridge `ValidationFunc` per GVK in `Rules`, then blocks inside `server.Start(ctx)` until the context is cancelled. Cancelling triggers the pure server's graceful shutdown.

This component does not run its own rotation loop, but the cert is still hot-reloaded: the controller points `Config.CertDir` at the mounted cert Secret, and the underlying `pkg/webhook.Server` reads `tls.crt`/`tls.key` from that directory and re-parses them on content change. A cert-manager renewal written to the mounted Secret is served within ~a minute, with no iteration restart, pod restart, or dedicated cert event.

## Validator Bridge

The component creates one bridge per GVK via `createResourceValidator(gvk)`,
called from `RegisterValidator` once per `Rules` entry. The real shape:

```go
func (c *Component) createResourceValidator(gvk string) webhook.ValidationFunc {
    return func(valCtx *webhook.ValidationContext) (bool, string, error) {
        // 1. Inline structural validation runs first; if it fails, we
        //    short-circuit before even constructing the dryrun pipeline.
        if err := c.validateBasicStructure(valCtx.Object); err != nil {
            return false, err.Error(), nil
        }

        // 2. Fail-open if no DryRunValidator is configured (e.g. early in
        //    startup before the proposal pipeline is wired up).
        if c.dryRunValidator == nil {
            return true, "", nil
        }

        // 3. Hard internal deadline — the controller imposes 5s here, in
        //    addition to the API server's `timeoutSeconds` on the
        //    ValidatingWebhookConfiguration.
        parent := c.serverCtx  // allows iteration shutdown to cancel in-flight validations
        if parent == nil {
            parent = context.Background()  // nil only in unit tests that skip Start()
        }
        ctx, cancel := context.WithTimeout(parent, 5*time.Second)
        defer cancel()

        // 4. Direct synchronous call into the proposal pipeline.
        //    ValidationContext has flat fields — no Request / Context wrapper.
        allowed, reason, warnings := c.dryRunValidator.ValidateDirect(
            ctx, gvk,
            valCtx.Namespace,
            valCtx.Name,
            valCtx.Object,        // *unstructured.Unstructured
            valCtx.Operation,     // already a string ("CREATE" / "UPDATE" / "DELETE")
        )
        return allowed, reason, warnings, nil
    }
}
```

Two deadlines apply, in this order:

1. The component's hard 5-second `context.WithTimeout` around `ValidateDirect`.
2. The API server's `timeoutSeconds` on the `ValidatingWebhookConfiguration`
   (default 10s) cuts the whole HTTP request if either deadline doesn't fire first.

If you change the internal 5s, update both this section and the
`createResourceValidator` constant — keeping the inner timeout shorter than the
outer one means the controller can return a structured deny rather than letting
the API server treat the whole request as a transport failure.

## Metrics

`MetricsRecorder` (the small interface this package depends on, *not* the full `*pkg/controller/metrics.Component`) gets two calls per request:

```go
type MetricsRecorder interface {
    RecordWebhookRequest(gvk, result string, durationSeconds float64)
    RecordWebhookValidation(gvk, result string)
}
```

Both are no-ops when `metrics` is nil — the component degrades gracefully so unit tests don't have to wire a real recorder.

## Testing Strategy

Three layers, only the first two live in this package:

| Layer | Where | What it covers |
|-------|-------|----------------|
| Unit | `component_test.go` | Defaulting, `Config` validation, `bridgeForGVK` calls `DryRunValidator` with the right arguments |
| Behavioural | `component_test.go` | Mock `DryRunValidator` returning allow/deny/error → HTTP response shape via the embedded `pkg/webhook.Server` |
| Integration | `tests/acceptance` | End-to-end: API server fires admission request, full pipeline runs, response observed |

Use the test helpers in `pkg/webhook/server_test.go` (cert generation, AdmissionReview construction) rather than re-implementing them here.

## Common Pitfalls

### Treating `ValidateDirect` as Async

The webhook handler runs synchronously — the API server is holding a request open. Returning before `ValidateDirect` completes (via a goroutine, channel, or fire-and-forget event) means the response goes back as "allowed" with no actual check. Always block.

### Returning errors as deny

`(false, "internal error", nil)` denies the admission request. `(false, "", err)` causes the pure server to return HTTP 500, which the API server treats as failure-policy (`Fail` → reject, `Ignore` → admit). Pick deliberately based on whether the failure should block the user's `kubectl apply`.

### Passing the controller's full Metrics struct

The component depends on the small `MetricsRecorder` interface, not the heavy `*pkg/controller/metrics.Component`. Wire `metrics.Metrics()` (which returns `*pkg/controller/metrics.Metrics` — itself implementing `RecordWebhookRequest`/`RecordWebhookValidation`) so the component remains testable without dragging the whole event bus into unit tests.

## Resources

- Pure webhook library: `pkg/webhook/CLAUDE.md`
- DryRunValidator: `pkg/controller/dryrunvalidator/CLAUDE.md`
- Cert lifecycle: the chart-managed cert-manager Secret/`ValidatingWebhookConfiguration`, mounted and pointed at via `Config.CertDir`, then served + hot-reloaded by `pkg/webhook`
- Architecture: `/docs/controller/docs/development/design.md`
