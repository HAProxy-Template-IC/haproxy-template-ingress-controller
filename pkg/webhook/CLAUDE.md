# pkg/webhook - Admission Webhook Library

Development context for the admission webhook library.

**API Documentation**: See `pkg/webhook/README.md`
**Architecture**: See `/docs/site/docs/development/design.md` (design documentation index)

## When to Work Here

Modify this package when:

- Improving certificate generation or rotation logic
- Enhancing webhook server performance
- Adding new webhook configuration options
- Fixing AdmissionReview parsing issues
- Improving error handling in validation flow

**DO NOT** modify this package for:

- Validation business logic → Use `pkg/controller/webhook`
- Event handling → Use `pkg/controller/webhook`
- Configuration types → Use `pkg/core/config`
- Metrics → Use `pkg/controller/webhook`

## Key Design Principle

This is a **pure library** with NO dependencies on other project packages. It could be extracted and used in any Kubernetes controller project.

Dependencies: Only standard library + k8s.io/api + k8s.io/apimachinery + k8s.io/client-go

## Package Structure

```
pkg/webhook/
├── types.go         # ServerConfig, ValidationContext, ValidationFunc
├── server.go        # HTTPS server, validator generations, AdmissionReview dispatch
├── server_test.go   # Server tests
├── README.md        # User documentation
└── CLAUDE.md        # This file
```

This package contains *only* the HTTPS server. Certificate *provisioning* (issuance, the Secret, the CA bundle) and `ValidatingWebhookConfiguration` management live elsewhere — see "Out of Scope" below — so don't go looking for `certs.go` or `config.go`; they don't exist here. The server does serve and hot-reload the mounted cert files it's pointed at via `ServerConfig.CertDir`, but it never fetches or issues them.

## Core Concepts

### Out of Scope (Lives Elsewhere)

| Concern | Where it lives |
|---------|----------------|
| TLS certificate provisioning (issuance, the Secret) | cert-manager + the Helm chart that provisions and mounts the cert Secret; the controller passes the mounted directory in via `ServerConfig.CertDir` |
| Injecting the CA bundle into `ValidatingWebhookConfiguration` | controller startup code (`pkg/controller`) — uses `client-go` directly |
| Validator implementations and overlay-store rendering | `pkg/controller/dryrunvalidator` |

The server *does* serve and hot-reload the mounted cert files via `ServerConfig.CertDir` (re-reads `tls.crt`/`tls.key` on content change). What it must never do is *fetch* or *issue* certs — if you find yourself reaching for a Kubernetes client or a CSR flow here, the boundary has slipped; keep `pkg/webhook` as a pure HTTPS+AdmissionReview transport that serves the files it's handed.

### Webhook Server

**HTTPS Only:**
Kubernetes requires webhooks to use HTTPS with valid certificates. This is enforced by the API server.

**AdmissionReview Handling:**

1. Receive POST request with AdmissionReview (v1)
2. Extract AdmissionRequest
3. Parse resource object from request.Object.Raw
4. Call registered validator for resource type
5. Build AdmissionResponse with result
6. Return AdmissionReview with response

**GVK Mapping:**
Resources are identified by "group/version.Kind" strings:

- Core types: "v1.Pod", "v1.Service"
- Named groups: "networking.k8s.io/v1.Ingress", "apps/v1.Deployment"

### 3. ValidatingWebhookConfiguration Lives Outside This Package

`pkg/webhook` does **not** create, update, or own a `ValidatingWebhookConfiguration`
resource — there's no `CreateOrUpdate`, no rule-builder, no CA-bundle injector
here. The Helm chart ships the `ValidatingWebhookConfiguration` and provisions and
mounts the cert Secret (with cert-manager's CA-bundle injection); the controller
passes that mounted directory to this server via `ServerConfig.CertDir`, and the
server reads and hot-reloads the cert files from it on rotation.

If you change the rules (which resources to validate, failure policy, timeouts),
that change goes in `charts/haptic` — not here. This package only sees the
AdmissionReview that the API server already routed based on those rules.

## Testing Approach

### Unit Tests

Tests in this package focus on the server's transport behavior — TLS handshake, AdmissionReview encode/decode, validator dispatch — not certificate or webhook-config logic (which isn't here).

```go
func TestServer_RegisterValidator(t *testing.T) {
    server := NewServer(&ServerConfig{
        Port:    9443,
        CertPEM: testCertPEM, // generated in test setup with crypto/x509
        KeyPEM:  testKeyPEM,
    })

    called := false
    server.RegisterValidator("networking.k8s.io/v1.Ingress",
        func(ctx *ValidationContext) (bool, string, []string, error) {
            called = true
            return true, "", nil, nil
        })

    // Then send a fake AdmissionReview through the HTTPS handler
    // and assert called == true. See server_test.go for the full pattern.
}
```

### Integration Tests

Test interactions between components:

```go
func TestWebhookEndToEnd(t *testing.T) {
    // Tests in this package generate self-signed certs inline with crypto/x509;
    // there's no NewCertificateManager helper here. See server_test.go for
    // the helper used across tests.
    certPEM, keyPEM := generateSelfSignedCert(t)

    server := NewServer(&ServerConfig{
        Port:    9443,
        CertPEM: certPEM,
        KeyPEM:  keyPEM,
    })

    called := false
    server.RegisterValidator("v1.ConfigMap",
        func(ctx *ValidationContext) (bool, string, error) {
            called = true
            return true, "", nil
        })

    runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    go server.Start(runCtx)

    // Make a TLS request that POSTs an AdmissionReview to the configured Path
    // ("/validate" by default). See server_test.go for the request helper.

    assert.True(t, called)
}
```

## Common Pitfalls

### Wrong ValidationFunc Signature

**Problem**: Defining the validator with the wrong shape (e.g., `func(obj any) ...`). The server hands the validator a `*ValidationContext` whose `Object` field is the parsed `*unstructured.Unstructured` plus the `AdmissionRequest` metadata.

```go
// Good — ValidationContext has flat fields: Object / OldObject / Operation /
// Namespace / Name / UID / UserInfo. There is no ctx.Request wrapper and no
// ctx.Context — a caller that needs cancellation captures its lifecycle context
// in the validator closure and derives the request deadline there.
func validateIngress(ctx *webhook.ValidationContext) (bool, string, []string, error) {
    if ctx.Object == nil {
        return false, "object missing", nil, nil
    }
    if ctx.Operation == "DELETE" {
        // No body for DELETE; reject immediately or fall through depending on policy.
    }
    return true, "", nil, nil
}
```

### Blocking Validation

**Problem**: Validation function takes longer than the webhook timeout (default 10s) and the API server times out, treating the request as failed-open or failed-closed depending on policy.

```go
// Bad
func validate(ctx *webhook.ValidationContext) (bool, string, []string, error) {
    time.Sleep(15 * time.Second) // exceeds default timeoutSeconds
    return true, "", nil, nil
}
```

**Solution**: Keep validation under ~1s. Anything slower needs a different design (e.g., precompute state in a controller and consult it during validation).

### Not Handling Nil Values

**Problem**: Validator panics when a field path is missing in the unstructured object.

```go
// Bad - panics if spec is missing
spec := ctx.Object.Object["spec"].(map[string]any)
```

**Solution**: Use the typed `unstructured` helpers (`unstructured.NestedMap`, `NestedString`, etc.) — they return `(value, found, err)` and never panic on missing paths.

### Forgetting Path / BindAddress / Port Defaults

`NewServer` applies defaults for `Port=9443`, `BindAddress=0.0.0.0`, `Path=/validate`, and 10s read/write timeouts. If you override `Path`, the `ValidatingWebhookConfiguration`'s `clientConfig.service.path` must match — otherwise the API server hits 404 and the webhook never sees the request.

## Performance Optimization

### Validator Latency Budget

The Kubernetes API server applies the configured `timeoutSeconds` (default 10s) to each call. Anything above ~1s makes admission decisions perceptibly slow for users running `kubectl apply`. Watch for slow paths in the validator (template rendering, large overlay-store builds) and short-circuit cheap rejections first.

### Concurrent Validations

The server handles concurrent requests. Avoid shared state in validators:

```go
// Bad - shared state
var validationCache = make(map[string]bool)

func validateWithCache(ctx *webhook.ValidationContext) (bool, string, error) {
    key := computeKey(ctx.Object)
    if valid, exists := validationCache[key]; exists {  // Race condition!
  return valid, "", nil
 }
 // ...
}

// Good - no shared state or use mutex
var (
 validationCache = make(map[string]bool)
 cacheMu         sync.RWMutex
)

func validateWithCache(ctx *webhook.ValidationContext) (bool, string, error) {
    key := computeKey(ctx.Object)

    cacheMu.RLock()
    valid, exists := validationCache[key]
    cacheMu.RUnlock()

    if exists {
        return valid, "", nil
    }
    // ...
}
```

The server acquires one validator generation per request under its table lock. `ReplaceValidatorGeneration` swaps the complete table and waits for requests using the retired generation.

## Troubleshooting

This package is just the HTTPS server, so most operational issues sit one layer down (certificates, `ValidatingWebhookConfiguration`, network) or one layer up (validator slowness). The checklist below focuses on what you can diagnose by reading this package's logs and code.

### Debug Checklist

1. **Validator dispatch** — does the GVK string passed to `RegisterValidator` match what the server computes from the AdmissionReview? Common mistake: registering `"v1.Ingress"` when the resource is actually `"networking.k8s.io/v1.Ingress"`.
2. **Path** — does the `ValidatingWebhookConfiguration`'s `clientConfig.service.path` match `ServerConfig.Path` (default `/validate`)?
3. **Validator latency** — server logs the duration of each validation. Anything close to the API server's `timeoutSeconds` (default 10s) is a fail.
4. **TLS handshake** — `tls: bad certificate` means the API server's CA bundle doesn't match the certificate served here; that's a cert Secret / CA-bundle / `ValidatingWebhookConfiguration` problem (chart-provisioned, served from `ServerConfig.CertDir`), not a `pkg/webhook` problem.

### Where to Look for Each Class of Error

| Symptom | Likely package |
|---------|----------------|
| `x509: certificate signed by unknown authority` | The cert Secret (chart-provisioned, mounted and served from `ServerConfig.CertDir`) and the CA-bundle injection on the `ValidatingWebhookConfiguration` |
| `no such host` | The Service object in the chart, not this package |
| `context deadline exceeded` mid-request | The validator closure (usually `pkg/controller/dryrunvalidator`) |
| `connection refused` | The webhook Pod isn't ready, or the configured Port doesn't match the Service's targetPort |
| Validator never fires for a registered GVK | GVK key mismatch — see #1 above |

## Extension Considerations

### Adding Mutating Webhooks

The current `Server` only handles `AdmissionReview` requests for validation (`response.Allowed`). Mutating support would need:

1. A second handler path (or a flag on `RegisterValidator`) that returns a `*MutationFunc` whose response includes a JSONPatch.
2. The corresponding `MutatingWebhookConfiguration` plumbing on the chart side (this package does not own that resource).

### Composing Validators

The server registers exactly one `ValidationFunc` per GVK. To run multiple checks per GVK, compose them in your own closure before passing it to `RegisterValidator`:

```go
server.RegisterValidator("networking.k8s.io/v1.Ingress",
    func(ctx *webhook.ValidationContext) (bool, string, error) {
        for _, check := range []webhook.ValidationFunc{checkA, checkB, checkC} {
            allowed, reason, err := check(ctx)
            if err != nil || !allowed {
                return allowed, reason, err
            }
        }
        return true, "", nil
    })
```

The `ValidationFunc` signature is `func(ctx *ValidationContext) (allowed bool, reason string, warnings []string, err error)`.
Everything lives on `ctx` as a flat field — no `ctx.Request`, no `ctx.Context`. Available
fields: `Object` / `OldObject` (`*unstructured.Unstructured`), `Operation` (string),
`Namespace`, `Name`, `UID`, `UserInfo`. The `warnings` return value surfaces as `AdmissionResponse.Warnings`.

### Async Validation

For checks that need to call external APIs, derive a deadline yourself from
`context.Background()` (or whatever request-scoped context you have access to via
package-level state) — the validator does not receive a `context.Context` argument:

```go
func asyncValidator(ctx *webhook.ValidationContext) (bool, string, error) {
    cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    resultCh := make(chan validationResult, 1)
    go func() {
        valid, reason := checkExternalAPI(cctx, ctx.Object)
        resultCh <- validationResult{valid, reason}
    }()

    select {
    case result := <-resultCh:
        return result.valid, result.reason, nil
    case <-cctx.Done():
        return false, "validation timeout", nil
    }
}
```

Avoid spawning goroutines that outlive the request — the API server has already returned to the user by the time a leftover goroutine finishes.

## Resources

- API documentation: `pkg/webhook/README.md`
- Kubernetes webhook docs: <https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/>
- AdmissionReview reference: <https://kubernetes.io/docs/reference/config-api/apiserver-webhooks.v1/>
- TLS with Go: <https://pkg.go.dev/crypto/tls>
