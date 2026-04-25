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
| `controller.templateLibraries.gateway.enabled` | Enable Gateway API support (HTTPRoute, GRPCRoute) | `true` |
| `ingressClass.enabled` | Create IngressClass resource | `true` |
| `ingressClass.name` | IngressClass name | `haptic` |
| `gatewayClass.enabled` | Create GatewayClass resource | `true` |
| `gatewayClass.name` | GatewayClass name | `haptic` |
| `controller.debugPort` | Introspection HTTP server port (provides /healthz and /debug/*) | `8080` |
| `controller.config.podSelector` | Labels to match HAProxy pods | `{app.kubernetes.io/component: loadbalancer}` |
| `controller.logLevel` | Initial log level (`LOG_LEVEL` env var: TRACE, DEBUG, INFO, WARNING, ERROR) | `INFO` |
| `controller.config.logging.level` | Log level from the `HAProxyTemplateConfig` CRD (overrides env var if non-empty) | `""` |
| `credentials.dataplane.username` | Dataplane API username | `admin` |
| `credentials.dataplane.password` | Dataplane API password (empty = auto-generated 32-char random) | `""` |
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

The field selector uses Kubernetes server-side filtering for efficient resource watching. Only ingresses matching the specified `spec.ingressClassName` will be processed by the controller.

## Template Libraries

The controller uses a modular template library system where configuration files are merged at Helm render time. Each library provides specific HAProxy functionality and can be enabled or disabled independently.

| Library | Default | Values key | Description |
|---------|---------|------------|-------------|
| Base | Always enabled | `base` | Core HAProxy configuration, extension points |
| SSL | Enabled | `ssl` | TLS certificates, HTTPS frontend |
| Ingress | Enabled | `ingress` | Kubernetes Ingress support |
| Gateway | Enabled | `gateway` | Gateway API (HTTPRoute, GRPCRoute) |
| haproxytech | Enabled | `haproxytech` | `haproxy.org/*` annotation support |
| haproxy-ingress | Enabled | `haproxyIngress` | `haproxy-ingress.github.io/*` annotation compatibility |
| nginx-ingress | Disabled | `nginxIngress` | `nginx.ingress.kubernetes.io/*` annotation compatibility |
| Path Regex Last | Disabled | `pathRegexLast` | Performance-first path matching |

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
