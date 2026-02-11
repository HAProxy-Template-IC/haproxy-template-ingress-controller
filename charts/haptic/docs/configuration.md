# Configuration

## Overview

This page covers the key configuration options for the HAPTIC Helm chart, including controller settings, ingress class filtering, and template library management.

For the complete list of all Helm values, see the [Configuration Reference](./reference.md).

## Key Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas (2+ recommended for HA) | `2` |
| `image.repository` | Controller image repository | `registry.gitlab.com/haproxy-haptic/haptic` |
| `image.tag` | Controller image tag | Chart appVersion |
| `controller.templateLibraries.ingress.enabled` | Enable Ingress resource support | `true` |
| `controller.templateLibraries.gateway.enabled` | Enable Gateway API support (HTTPRoute, GRPCRoute) | `true` |
| `ingressClass.enabled` | Create IngressClass resource | `true` |
| `ingressClass.name` | IngressClass name | `haproxy` |
| `gatewayClass.enabled` | Create GatewayClass resource | `true` |
| `gatewayClass.name` | GatewayClass name | `haproxy` |
| `controller.debugPort` | Introspection HTTP server port (provides /healthz and /debug/*) | `8080` |
| `controller.config.podSelector` | Labels to match HAProxy pods | `{app.kubernetes.io/component: loadbalancer}` |
| `controller.logLevel` | Initial log level (LOG_LEVEL env var) | `INFO` |
| `controller.config.logging.level` | Log level from ConfigMap (overrides env var if set) | `""` |
| `credentials.dataplane.username` | Dataplane API username | `admin` |
| `credentials.dataplane.password` | Dataplane API password | `adminpass` |
| `networkPolicy.enabled` | Enable NetworkPolicy | `true` |

## Controller Configuration

The controller configuration is defined in `controller.config` and includes:

- **podSelector**: Labels to identify HAProxy pods to manage
- **watchedResources**: Kubernetes resources to watch (Ingress, Service, EndpointSlice, Secret)
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

By default, the controller only watches Ingress resources with `spec.ingressClassName: haproxy`. This ensures the controller only processes ingresses intended for it.

**Default behavior:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=haproxy"
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

| Library | Default | Description |
|---------|---------|-------------|
| Base | Always enabled | Core HAProxy configuration, extension points |
| SSL | Enabled | TLS certificates, HTTPS frontend |
| Ingress | Enabled | Kubernetes Ingress support |
| Gateway | Enabled | Gateway API (HTTPRoute, GRPCRoute) |
| HAProxy Annotations | Enabled | `haproxy.org/*` annotation support |
| HAProxy Ingress | Enabled | HAProxy Ingress Controller compatibility |
| Path Regex Last | Disabled | Performance-first path matching |

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
