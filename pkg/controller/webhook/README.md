# pkg/controller/webhook

Event adapter that mounts the pure HTTPS server from `pkg/webhook` on the controller's lifecycle, registers one `ValidationFunc` per webhook rule, and routes each admission request through the dry-run validator.

Certificates are supplied ready-to-use — cert-manager provisions the Secret, `pkg/controller/certloader` watches it and pushes the PEM bytes into this component's `Config`. This package does **not** manage certs, CA bundles, or `ValidatingWebhookConfiguration` resources (the Helm chart provisions those).

## Minimal Usage

```go
import (
    "context"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/webhook"
    pkgwebhook "gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

cfg := &webhook.Config{
    Port:            9443,                    // default
    Path:            "/validate",             // default
    CertPEM:         cert,                    // from cert-manager via certloader
    KeyPEM:          key,
    Rules:           rules,                   // built by ExtractWebhookRules(cfg)
    DryRunValidator: dryRunValidator,         // pkg/controller/dryrunvalidator.Component
}

comp := webhook.New(logger, cfg, restMapper, metricsRecorder)
if err := comp.Start(ctx); err != nil {
    return err
}
```

`Start` blocks until the server returns an error or `ctx` is cancelled; on cancellation it shuts the HTTPS listener down gracefully. The component's reinitialisation lifecycle is owned by the controller — when the CRD changes, the controller cancels the iteration context and `Start` returns cleanly.

## Config

| Field | Notes |
|-------|-------|
| `Port` | TCP port for the HTTPS listener (default `9443`) |
| `Path` | URL path that handles `POST /…` AdmissionReview calls (default `/validate`) |
| `CertPEM` / `KeyPEM` | PEM-encoded TLS material. Empty values cause `Start` to return an error. Rotate by restarting the component with new bytes — the server reads them once at `Start` time. |
| `Rules` | `[]pkg/webhook.WebhookRule`, one per kind to register. Built from the CRD via `webhook.ExtractWebhookRules(cfg *config.Config)`. |
| `DryRunValidator` | Interface with a single method: `ValidateDirect(ctx, gvk, namespace, name, object, operation) (allowed bool, reason string, warnings []string)`. Satisfied by `pkg/controller/dryrunvalidator.Component`. Warnings flow through to `AdmissionResponse.Warnings` on both allow and deny paths (e.g. soft diagnostics from pluggable validator sidecars). If `nil`, the component fails open (accepts everything) — useful only in tests. |

`restMapper` is used to resolve `(APIGroup, APIVersion, Resource)` → `Kind` when wiring rules into `"group/version.Kind"` registration keys that the underlying `pkg/webhook.Server` expects. A live `meta.RESTMapper` from the controller's cluster connection is required.

`metrics` implements two methods: `RecordWebhookRequest(gvk, result, durationSec)` and `RecordWebhookValidation(gvk, result)`. `pkg/controller/metrics` satisfies this directly; pass `nil` to skip metrics entirely.

## Validator Flow

Registration happens once at `Start`:

1. For each `Rule`, resolve `Kind` via `restMapper` and build a `group/version.Kind` key.
2. Register a thin wrapper `ValidationFunc` that:
   - Performs basic structural sanity (`validateBasicStructure`) and short-circuits on failure before touching the validator.
   - Wraps the call in a 5-second `context.WithTimeout` — kept shorter than the chart's `timeoutSeconds: 10` so a stuck render returns a structured deny rather than an HTTP transport failure.
   - Delegates to `DryRunValidator.ValidateDirect(ctx, gvk, namespace, name, object, operation)`.
   - Records metrics and logs the outcome.

`ValidateDirect` renders the config against an overlay store that includes the proposed change (via `pkg/stores.StoreOverlay`) and runs the full three-phase HAProxy validation. If any phase fails, the webhook denies with the simplified error message; if all pass, it allows.

## Integration Points

- **Upstream** — `pkg/controller/certloader` watches the cert-manager-provisioned Secret and publishes `CertParsedEvent` with `CertPEM`/`KeyPEM`. The controller iterates on cert updates by restarting this component with the new bytes.
- **Downstream** — `pkg/controller/dryrunvalidator` is the only implementation of the `DryRunValidator` interface in the tree. It in turn delegates the actual render+validate to `pkg/controller/proposalvalidator`, which is the same pipeline the leader-side reconciler uses — so anything that passes admission will also pass at deploy time.
- **Chart** — `charts/haptic/templates/validatingwebhookconfiguration.yaml` defines the `ValidatingWebhookConfiguration`: `failurePolicy: Fail`, `timeoutSeconds: 10`, and (when `webhook.certManager.enabled`) cert-manager's `cert-manager.io/inject-ca-from` annotation for CA bundle injection. The chart does **not** set an `objectSelector`; multi-controller isolation comes from each release deploying its own webhook configuration whose `clientConfig.service` points at that release's controller `Service`.

## See Also

- [`pkg/webhook`](../../webhook/) — pure HTTPS server + AdmissionReview protocol
- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) — the `DryRunValidator` implementation this component calls into
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — the speculative render+validate pipeline shared by `dryrunvalidator` (synchronous, this webhook path) and the background HTTP-content refresh in `pkg/controller/httpstore` (asynchronous)
- [`pkg/controller/certloader`](../certloader/) — supplies `CertPEM`/`KeyPEM`
- `docs/controller/docs/development/crd-validation-design.md` — why the webhook fails closed, lives in the controller pod, and runs the same render/validate code as the reconciler

## License

Apache-2.0 — see root `LICENSE`.
