# Configuration Model

The controller is headless. Operators interact with it through four surfaces:

| Surface | Purpose |
|---------|---------|
| `HAProxyTemplateConfig` CRD | Primary configuration: templates, watched resources, validation, logging |
| `Secret` (credentialsSecretRef) | Dataplane API and validation credentials |
| `/metrics` (default `:9090`) | Prometheus metrics |
| `/healthz` (default `:8080`) | Liveness/readiness probes |
| `/debug/vars`, `/debug/pprof/` (disabled by default) | Runtime introspection; enable with `--debug-port` or `DEBUG_PORT` |

Structured JSON logs on stdout round out the operational surface.

## What the CRD Covers

`HAProxyTemplateConfig.spec` is the single source of truth for controller behaviour. It has four top-level groups:

- **Runtime settings** — `controller`, `dataplane`, `logging`, `templatingSettings`, `configPublishing`.
- **Resource watching** — `podSelector`, `watchedResources`, `watchedResourcesIgnoreFields`, `httpResources`.
- **Templates** — `haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`.
- **Validation** — `validationTests` plus the per-resource `enableValidationWebhook` flag.

The full field reference (types, defaults, validation rules) lives in [CRD Reference](../../crd-reference.md). This page focuses on how the pieces fit together; the reference page tells you what every field does.

## Minimal Example

A working configuration renders a single Ingress-driven backend and nothing else. The Helm chart ships a much larger default that covers Ingress and Gateway API via template libraries; see [Template Libraries](https://haproxy-haptic.org/helm-chart/latest/template-libraries/).

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
    endpointslices:
      apiVersion: discovery.k8s.io/v1
      resources: endpointslices
      indexBy: ["metadata.labels['kubernetes.io/service-name']"]

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
