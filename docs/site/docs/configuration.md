# Configuration

## Overview

This page covers the key configuration options for the HAPTIC Helm chart, including controller settings, ingress class filtering, and template library management.

For the complete list of all Helm values, see the [Configuration Reference](./reference.md).

## Key Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas (2+ recommended for HA) | `2` |
| `image.repository` | Controller image repository | `registry.gitlab.com/haproxy-haptic/haptic` |
| `image.tag` | Controller image tag (empty = `<chart appVersion>-haproxy<haproxyVersion>`) | `""` |
| `controller.templateLibraries.ingress.enabled` | Enable Ingress resource support | `true` |
| `controller.templateLibraries.gateway.enabled` | Enable Gateway API support (HTTPRoute, GRPCRoute, TLSRoute) | `true` |
| `ingressClass.enabled` | Create IngressClass resource | `true` |
| `ingressClass.name` | IngressClass name | `haptic` |
| `gatewayClass.enabled` | Create GatewayClass resource | `true` |
| `gatewayClass.name` | GatewayClass name | `haptic` |
| `controller.debugPort` | Introspection HTTP server port (provides /healthz and /debug/*) | `8080` |
| `controller.config.podSelector` | Labels to match HAProxy pods | `{app.kubernetes.io/component: loadbalancer}` |
| `controller.logLevel` | Initial log level (`LOG_LEVEL` env var: TRACE, DEBUG, INFO, WARN, ERROR — case-insensitive) | `INFO` |
| `controller.config.logging.level` | Log level from the `HAProxyTemplateConfig` CRD (overrides env var if non-empty) | `""` |
| `credentials.dataplane.username` | Dataplane API username | `admin` |
| `credentials.dataplane.password` | Dataplane API password. Empty falls back to a deterministic 32-char SHA256 hash of `<release>-<namespace>-haptic-dataplane-api`, preserved across upgrades from the existing Secret when `lookup` works. **Set explicitly in production** — see `reference.md`. | `""` |
| `networkPolicy.enabled` | Enable NetworkPolicy | `true` |

## Controller Configuration

The controller configuration is defined in `controller.config` and includes:

- **podSelector**: Labels to identify HAProxy pods to manage
- **watchedResources**: Kubernetes resources to watch — defaults derive from the enabled template libraries (e.g. Ingress, Service, EndpointSlice, Secret when the `ingress` + `ssl` libraries are on; plus HTTPRoute / GRPCRoute / Gateway when `gateway` is on). Override per-resource to extend or narrow the set
- **templateSnippets**: Reusable template fragments
- **maps**: HAProxy map file templates
- **files**: Auxiliary files (error pages, etc.)
- **haproxyConfig**: Main HAProxy configuration template

Example custom configuration:

```yaml
controller:
  config:
    podSelector:
      matchLabels:
        app: my-haproxy
        environment: production

    watchedResources:
      ingresses:
        apiVersion: networking.k8s.io/v1
        resources: ingresses
        indexBy: ["metadata.namespace", "metadata.name"]
```

## Ingress Class Filtering

By default, the controller only watches Ingress resources with `spec.ingressClassName: haptic`. The class name is deliberately `haptic` (not `haproxy`) so HAPTIC can run alongside other HAProxy-based ingress controllers during migration without fighting over the same IngressClass. Override `ingressClass.name` to `haproxy` (or any other value) if you are replacing an existing controller and want your existing Ingress manifests to match without edits.

**Default behavior:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=haptic"
```

**To change the ingress class name:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=my-custom-class"
```

**To watch all ingresses regardless of class:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: ""
```

`fieldSelector` here is client-side JSONPath filtering, not the Kubernetes
server-side `fieldSelector` (which only supports a handful of fields like
`metadata.name`). The controller fetches all Ingresses the API server returns
and drops the ones whose `spec.ingressClassName` doesn't match before adding
them to the store. To narrow the watch *server-side* — the cheaper option
when you can use it — set `labelSelector` (server-side label match) on the
same entry. See [Watching Resources →
Narrowing the Watch](watching-resources.md#narrowing-the-watch).

## Template Libraries

The controller uses a modular template library system where configuration files are merged at Helm render time. Each library provides specific HAProxy functionality and can be enabled or disabled independently.

| Library | Default | Values key | Description |
|---------|---------|------------|-------------|
| Base | Enabled | `base` | Core HAProxy configuration, extension points; disabling drops the `haproxyConfig` the other libraries plug into |
| SSL | Enabled | `ssl` | TLS certificates, HTTPS frontend |
| Ingress | Enabled | `ingress` | Kubernetes Ingress support |
| Gateway | Enabled | `gateway` | Gateway API (HTTPRoute, GRPCRoute, TLSRoute) |
| ingress-annotations-compat | Enabled | `ingressAnnotationsCompat` | Shared scaffold consumed by the Ingress vendor annotation libraries below (level 2.5) |
| haproxytech | Enabled | `haproxytech` | `haproxy.org/*` annotation support |
| haproxy-ingress | Enabled | `haproxyIngress` | `haproxy-ingress.github.io/*` annotation compatibility |
| nginx-ingress | Disabled | `nginxIngress` | `nginx.ingress.kubernetes.io/*` annotation compatibility |
| spoa-hub | Auto | `spoaHub` | HAProxy-side wiring for the SPOA hub sidecar (auto-loaded when `spoaHub.enabled: true` or any `spoaHub.plugins.<X>.enabled` is truthy) |

### Path Matching Order

Path-based routing inside the rendered `frontend-routing-logic` snippet evaluates four map types: exact, regex, prefix-exact, and prefix. The evaluation order is selected by `controller.config.routing.regexMatchOrder`:

| Value | Order | Use case |
|-------|-------|----------|
| `default` (default) | Exact > Regex > Prefix-exact > Prefix | De-facto standard; matches typical Ingress controller behaviour |
| `last` | Exact > Prefix-exact > Prefix > Regex | Performance-first; evaluates faster matchers before regex |

The chart swaps in the `frontend-routing-logic-regex-last` variant of the snippet at Helm load time when `last` is set. No runtime difference.

### Enabling/Disabling Libraries

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true   # Enable Gateway API support
    ingress:
      enabled: false  # Disable Ingress support
```

For comprehensive documentation including extension points and custom configuration injection, see [Template Libraries](./template-libraries.md).

For Gateway API features, see [Gateway API Library](./libraries/gateway.md).
