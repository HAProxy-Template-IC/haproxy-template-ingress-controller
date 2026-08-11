# pkg/webhook

A focused HTTPS server for [Kubernetes validating admission webhooks](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/). Given TLS credentials and a set of validator functions keyed by resource GVK, it speaks the `AdmissionReview v1` protocol, dispatches to the right validator, and shuts down cleanly on context cancellation.

The package intentionally does *not* handle certificate *provisioning* — issuance, the Secret, the CA bundle, or `ValidatingWebhookConfiguration` management — those are the controller's and Helm chart's concern (the chart provisions the Secret plus `validatingwebhookconfiguration.yaml`). It *does* serve and hot-reload the mounted cert files it's pointed at: when `ServerConfig.CertDir` is set, the server re-reads `tls.crt`/`tls.key` from that directory on content change, so a cert-manager renewal is picked up without a restart. Keeping this library narrow means it can be reused by any controller that already has a cert pipeline.

Module path: `gitlab.com/haproxy-haptic/haptic`. Source is authoritative (`go doc ./pkg/webhook`); this README is a short map.

## Minimal Example

```go
import (
    "context"
    "log"

    "gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

func main() {
    // In production, point the server at the directory where the TLS Secret
    // is mounted (cert-manager writes tls.crt/tls.key there). The server
    // re-reads them per handshake and hot-reloads on rotation — no restart.
    srv, err := webhook.NewServer(&webhook.ServerConfig{
        Port:    9443,
        CertDir: "/etc/webhook/certs",
        // For unit tests without a mounted Secret, set CertPEM/KeyPEM instead
        // to serve a fixed cert.
        // BindAddress defaults to 0.0.0.0, Path to /validate,
        // ReadTimeout and WriteTimeout to 10s.
    })
    if err != nil {
        log.Fatal(err)
    }

    srv.RegisterValidator("networking.k8s.io/v1.Ingress", validateIngress)
    srv.RegisterValidator("gateway.networking.k8s.io/v1.HTTPRoute", validateHTTPRoute)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    if err := srv.Start(ctx); err != nil {
        log.Fatal(err)
    }
}

func validateIngress(ctx *webhook.ValidationContext) (bool, string, []string, error) {
    // ctx.Object / ctx.OldObject are *unstructured.Unstructured.
    // ctx.Operation is "CREATE" / "UPDATE" / "DELETE" / "CONNECT".
    // ctx.UserInfo carries the requester identity.
    return true, "", nil, nil
}
```

`RegisterValidator` is thread-safe and suits a static table. Reconfiguring callers use `ReplaceValidatorGeneration` to swap a complete table atomically; it waits for requests using the previous generation before calling that generation's retirement callback.

## Types at a Glance

- **`ServerConfig`** — port (default `9443`), bind address, path (default `/validate`), read/write timeouts, and the TLS source. In production set `CertDir` to the directory holding `tls.crt`/`tls.key` (typically the mounted cert Secret); the server resolves the cert per handshake via a `tls.Config.GetCertificate` callback and a `certReloader` that re-parses the files on content change — a cert-manager renewal is served without a restart. When `CertDir` is unset (e.g. unit tests), it serves a fixed cert from the PEM-encoded `CertPEM`/`KeyPEM` instead.
- **`Server`** — wraps an `http.Server` and atomically dispatches each request through one validator generation.
- **`ValidationContext`** — everything the server extracts from an `AdmissionRequest`: `Object`, `OldObject`, `Operation`, `Namespace`, `Name`, `UID`, `UserInfo`. `Object` and `OldObject` are `*unstructured.Unstructured` so validators don't need their own typed decoders.
- **`ValidationFunc`** — `func(*ValidationContext) (allowed bool, reason string, warnings []string, err error)`. The `allowed`/`reason` pair maps to `AdmissionResponse.Allowed` and `.Status.Message`; `warnings` surfaces as `AdmissionResponse.Warnings` (visible to `kubectl` users even when `allowed` is true). A non-nil error is treated as a *denied* decision (`Allowed: false`, `Result.Code: 500`, `Result.Message: "validation error: <err>"`) — *not* a transport-layer HTTP 500. The HTTP response itself is always 200 OK with a well-formed `AdmissionReview`. The API server's `failurePolicy` only kicks in when the webhook fails to *respond* (TLS handshake failure, malformed body, etc.); a `ValidationFunc` returning an error never triggers it.

## GVK Keys

Keys are `version.Kind` for core types and `group/version.Kind` otherwise:

```go
"v1.Pod"
"v1.ConfigMap"
"apps/v1.Deployment"
"networking.k8s.io/v1.Ingress"
"gateway.networking.k8s.io/v1.HTTPRoute"
```

Unknown GVKs are rejected with a denial message — the server never calls an unregistered validator.

## Endpoints Exposed

- `POST <Path>` — admission endpoint.
- `GET /healthz` — 200 OK when the server is running. Useful as a liveness probe without interacting with the cert pipeline.

## Graceful Shutdown

`Start(ctx)` blocks until either the HTTP serve loop fails or `ctx` is cancelled. On cancellation it calls `http.Server.Shutdown` with a 30-second deadline and joins the serve loop. Any in-flight admission calls run to completion subject to that deadline.

## What's NOT In This Package

Deliberate omissions — and where to look instead in this repo:

| Concern | Lives in |
|---------|----------|
| Issuing/renewing the TLS cert | cert-manager (chart provisions a `Certificate` and mounts the Secret). The server serves and hot-reloads the mounted files via `ServerConfig.CertDir`, but does not fetch or issue them. |
| Creating / updating `ValidatingWebhookConfiguration` | `charts/haptic/templates/validatingwebhookconfiguration.yaml` |
| Multi-controller isolation | Each Helm release deploys its own `ValidatingWebhookConfiguration` whose `clientConfig.service` points at that release's controller `Service`. The chart does **not** set `objectSelector` — add one if you need label-based scoping on top of that. |
| Fail-policy, scope, operations on each rule | Chart values + `validatingwebhookconfiguration.yaml` |
| Registering validators that render templates with overlay stores | `pkg/controller/dryrunvalidator`, `pkg/controller/proposalvalidator` |
| Metrics / events for webhook activity | `pkg/controller/webhook`, `pkg/controller/metrics` |

## Testing

```bash
go test ./pkg/webhook/...           # unit tests
go test ./pkg/webhook/... -race     # race detector
```

Server-level tests bring up a real TLS listener on a random port with a self-signed cert and exercise the AdmissionReview request/response path. Validator-level tests should call the `ValidationFunc` directly with a crafted `ValidationContext` — the server's dispatch logic is exercised once in the server tests, not in every validator's test suite.

## See Also

- [Kubernetes admission webhooks reference](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
- [AdmissionReview v1 API](https://kubernetes.io/docs/reference/config-api/apiserver-webhooks.v1/)
- `pkg/controller/webhook` — event adapter that owns the `Server` lifecycle inside the controller
- The controller passes the mounted Secret directory to the server via `ServerConfig.CertDir`, which the server reads and hot-reloads on rotation; the Helm chart provisions and mounts that Secret
- `docs/site/docs/development/crd-validation-design.md` — why the webhook lives in the controller pod and fails closed

## License

Apache-2.0 — see root `LICENSE`.
