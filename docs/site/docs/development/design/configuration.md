# Configuration Model

The controller is headless. Operators interact with it through the following surfaces:

| Surface | Purpose |
|---------|---------|
| `HAProxyTemplateConfig` CRD | Primary configuration: templates, watched resources, validation, logging |
| `Secret` (credentialsSecretRef) | Dataplane API credentials (`dataplane_username`, `dataplane_password`) |
| `/metrics` (default `:9090`) | Prometheus metrics |
| `/healthz` (Helm chart default `:8080`) | Liveness/readiness probes. Shares the `--debug-port` listener, so disabled by default when running the binary directly (`--debug-port 0`). |
| `/debug/vars`, `/debug/pprof/` | Runtime introspection. Off by default when running the binary directly (`--debug-port 0`); the Helm chart turns it on by setting `controller.debugPort: 8080` (same port as `/healthz`). The two share a single listener — setting `controller.debugPort: 0` drops `/healthz` along with `/debug/*` and breaks Kubernetes probes, so restrict access via NetworkPolicy in production rather than disabling the port. Set `controller.debugPort` to a different non-zero port (for example `6060`) to move both endpoints off `8080`. |

Structured logfmt logs on stdout (via `slog.NewTextHandler`) round out the operational surface — the level is set by `LOG_LEVEL` at startup, then dynamically overridden at runtime by the CRD's `spec.logging.level` once the controller's configloader picks it up.

## What the CRD Covers

`HAProxyTemplateConfig.spec` is the single source of truth for controller behaviour. It has four top-level groups:

- **Runtime settings** — `controller` (including `controller.configPublishing`), `dataplane`, `logging`, `templatingSettings`.
- **Resource watching** — `podSelector`, `watchedResources`, `watchedResourcesIgnoreFields`. (HTTP fetching is driven by the `http.Fetch()` template function — URLs that appear in templates are auto-registered; there is no top-level `spec.httpResources` field, only `validationTests[].httpResources` (a sibling of `fixtures`, not nested inside it) for mocking responses during tests.)
- **Templates** — `haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`, `k8sResources` (declarative Kubernetes resources rendered and applied via Server-Side Apply).
- **Validation** — `validationTests`, the per-resource `enableValidationWebhook` flag, and `validators` (pluggable external validator sidecars).

The full field reference (types, defaults, validation rules) lives in [CRD Reference](../../crd-reference.md). This page focuses on how the pieces fit together; the reference page tells you what every field does.

## Minimal Example

A working configuration renders a single Ingress-driven backend and nothing else. The Helm chart ships a much larger default that covers Ingress and Gateway API via template libraries; see [Template Libraries](../../template-libraries.md).

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haptic
spec:
  credentialsSecretRef:
    name: haproxy-credentials

  podSelector:
    matchLabels:
      app.kubernetes.io/component: loadbalancer

  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]
      enableValidationWebhook: true
    endpoints:
      apiVersion: discovery.k8s.io/v1
      resources: endpointslices
      indexBy: ["metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"]

  haproxyConfig:
    template: |
      global
        log stdout len 4096 local0 info
        maxconn 4096

      defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

      frontend http
        bind *:80
        default_backend default

      {% for _, ingress := range resources.ingresses.List() %}
      backend ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}
        balance roundrobin
        # server lines generated from endpointslices...
      {% end %}

      backend default
        http-request return status 404
```

## Configuration Layers

Users commonly compose configuration from three layers, in order of precedence:

1. **Template libraries** shipped in the Helm chart (base, SSL, ingress, gateway, haproxytech). These are merged into a single rendered `HAProxyTemplateConfig`.
2. **`controller.config`** in Helm values — anything set here is merged on top of library output.
3. **Direct `HAProxyTemplateConfig` edits** (via `kubectl edit htplcfg`) for ad-hoc overrides.

Because templates are just strings inside a CRD, the chart layers and the user's own values can both contribute snippets and be composed at render time. See [Templating Guide](../../templating.md) for how snippets and extension points interact.

## Reloading Behaviour

Changes to the `HAProxyTemplateConfig` resource trigger an internal **reinitialization loop**: the controller cancels its current iteration, re-validates the new config, and restarts all components against it. No pod restart is required. The Secret referenced by `credentialsSecretRef` is watched the same way, so credential rotation is picked up live.
