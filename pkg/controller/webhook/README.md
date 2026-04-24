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
| `DryRunValidator` | Interface with a single method: `ValidateDirect(ctx, gvk, namespace, name, object, operation) (allowed bool, reason string)`. Satisfied by `pkg/controller/dryrunvalidator.Component`. If `nil`, the component fails open (accepts everything) — useful only in tests. |

`restMapper` is used to resolve `(APIGroup, APIVersion, Resource)` → `Kind` when wiring rules into `"group/version.Kind"` registration keys that the underlying `pkg/webhook.Server` expects. A live `meta.RESTMapper` from the controller's cluster connection is required.

`metrics` implements two methods: `RecordWebhookRequest(gvk, result, durationSec)` and `RecordWebhookValidation(gvk, result)`. `pkg/controller/metrics` satisfies this directly; pass `nil` to skip metrics entirely.

## Validator Flow

Registration happens once at `Start`:

1. For each `Rule`, resolve `Kind` via `restMapper` and build a `group/version.Kind` key.
2. Register a thin wrapper `ValidationFunc` that:
   - Extracts `namespace`, `name`, `operation` from the `AdmissionRequest`.
   - Performs basic structural sanity (`validateBasicStructure`).
   - Delegates to `DryRunValidator.ValidateDirect`.
   - Records metrics and logs the outcome.

`ValidateDirect` renders the config against an overlay store that includes the proposed change (via `pkg/stores.StoreOverlay`) and runs `haproxy -c`. If either fails, the webhook denies with the simplified error message; if both pass, it allows.

## Integration Points

- **Upstream** — `pkg/controller/certloader` emits `CertParsedEvent` with `CertPEM`/`KeyPEM`. The controller iterates on cert updates by restarting this component.
- **Downstream** — `pkg/controller/dryrunvalidator` is the only implementation of the `DryRunValidator` interface in the tree.
- **Sibling** — `pkg/controller/proposalvalidator` handles validation for the CRD itself (the `HAProxyTemplateConfig` kind) rather than user resources.
- **Chart** — `charts/haptic/templates/validatingwebhookconfiguration.yaml` defines the `ValidatingWebhookConfiguration`, including `failurePolicy: Fail`, `objectSelector` matching `app.kubernetes.io/instance`, and cert-manager's `cert-manager.io/inject-ca-from` annotation for CA bundle injection.

## See Also

- [`pkg/webhook`](../../webhook/) — pure HTTPS server + AdmissionReview protocol
- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) — the `DryRunValidator` implementation this component calls into
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — validates the controller's own CRD (complementary webhook path)
- [`pkg/controller/certloader`](../certloader/) — supplies `CertPEM`/`KeyPEM`
- `docs/controller/docs/development/crd-validation-design.md` — why the webhook fails closed, lives in the controller pod, and runs the same render/validate code as the reconciler

## License

Apache-2.0 — see root `LICENSE`.
