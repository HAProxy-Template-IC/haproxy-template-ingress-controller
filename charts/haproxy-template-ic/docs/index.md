# Helm Chart Installation

The Helm chart is the recommended way to deploy HAProxy Template IC. It includes:

- **Pre-built template libraries** for Kubernetes Ingress and Gateway API
- **HAProxy annotation support** compatible with haproxy.org annotations
- **Sensible defaults** for production deployments
- **High availability** with leader election enabled by default

For users who need full control over templates, see the [Templating Guide](/controller/latest/templating/).

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- HAProxy pods with Dataplane API sidecars (deployed separately)
- **HAProxy 3.0 or newer** (template libraries require HAProxy 3.0+ for SSL/TLS features)

## Quick Start

### Add the Helm Repository

```bash
helm repo add haproxy-template-ic https://haproxy-template-ic.github.io/charts
helm repo update
```

### Install the Chart

```bash
helm install my-controller haproxy-template-ic/haproxy-template-ic
```

### With Custom Values

```bash
helm install my-controller haproxy-template-ic/haproxy-template-ic \
  --set image.tag=v0.1.0 \
  --set replicaCount=2
```

### With Values File

```bash
helm install my-controller haproxy-template-ic/haproxy-template-ic \
  -f my-values.yaml
```

## Key Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas (2+ recommended for HA) | `2` |
| `image.repository` | Controller image repository | `ghcr.io/haproxy-template-ic/haproxy-template-ingress-controller` |
| `image.tag` | Controller image tag | Chart appVersion |
| `controller.templateLibraries.ingress.enabled` | Enable Ingress resource support | `true` |
| `controller.templateLibraries.gateway.enabled` | Enable Gateway API support | `false` |
| `ingressClass.enabled` | Create IngressClass resource | `true` |
| `ingressClass.name` | IngressClass name | `haproxy` |
| `gatewayClass.enabled` | Create GatewayClass resource | `true` |
| `gatewayClass.name` | GatewayClass name | `haproxy` |
| `credentials.dataplane.username` | Dataplane API username | `admin` |
| `credentials.dataplane.password` | Dataplane API password | `adminpass` |

## Template Libraries

The chart includes pre-built template libraries that can be enabled/disabled:

| Library | Description | Default |
|---------|-------------|---------|
| `ingress` | Kubernetes Ingress support | Enabled |
| `gateway` | Gateway API (HTTPRoute, GRPCRoute) | Disabled |
| `haproxytech` | HAProxy annotation compatibility | Enabled |

### Enable Gateway API

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true
```

See [Gateway API Support](gateway-api.md) for details on supported features.

## HAProxy Pod Selection

The controller watches HAProxy pods based on label selectors:

```yaml
controller:
  config:
    pod_selector:
      match_labels:
        app: haproxy
        component: loadbalancer
```

Customize this to match your HAProxy deployment's labels.

## Further Reading

- [Gateway API Support](gateway-api.md) - HTTPRoute and GRPCRoute configuration
- [HAProxy Annotations](annotations.md) - Compatible annotations reference
- [Controller Documentation](/controller/latest/) - Architecture and advanced configuration
