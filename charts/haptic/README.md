# HAPTIC Helm Chart

This Helm chart deploys HAPTIC (HAProxy Template Ingress Controller), which manages HAProxy configurations dynamically based on Kubernetes resources.

## Overview

HAPTIC:

- Watches Kubernetes Ingress and/or Gateway API resources
- Renders Scriggo templates to generate [HAProxy](https://www.haproxy.org/) configurations
- Deploys configurations to HAProxy pods via [Dataplane API](https://github.com/haproxytech/dataplaneapi)
- Supports cross-namespace HAProxy pod management
- Template library system for modular feature support
- Conditional resource watching based on enabled features

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- **HAProxy 3.0 or newer** (the chart deploys HAProxy by default; template libraries require 3.0+ for SSL/TLS features)

## Installation

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.1.0-alpha.11
```

With custom values:

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0-alpha.11 \
  -f my-values.yaml
```

## Key Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas (2+ recommended for HA) | `2` |
| `image.tag` | Controller image tag | Chart appVersion |
| `controller.templateLibraries.ingress.enabled` | Enable Ingress resource support | `true` |
| `controller.templateLibraries.gateway.enabled` | Enable Gateway API support | `true` |
| `ingressClass.name` | IngressClass name | `haproxy` |
| `gatewayClass.name` | GatewayClass name | `haproxy` |
| `controller.debugPort` | Introspection HTTP server port | `8080` |
| `controller.logLevel` | Log level (TRACE, DEBUG, INFO, WARN, ERROR) | `INFO` |
| `credentials.dataplane.username` | Dataplane API username | `admin` |
| `credentials.dataplane.password` | Dataplane API password | `adminpass` |
| `networkPolicy.enabled` | Enable NetworkPolicy | `true` |
| `haproxy.enabled` | Deploy HAProxy pods with this chart | `true` |
| `monitoring.serviceMonitor.enabled` | Create Prometheus ServiceMonitor | `false` |

For the complete values reference, see the [Configuration Reference](./docs/reference.md).

## Template Libraries

The controller uses a modular template library system where configuration files are merged at Helm render time.

| Library | Default | Description |
|---------|---------|-------------|
| Base | Always enabled | Core HAProxy configuration, extension points |
| SSL | Enabled | TLS certificates, HTTPS frontend |
| Ingress | Enabled | Kubernetes Ingress support |
| Gateway | Enabled | Gateway API (HTTPRoute, GRPCRoute) |
| HAProxy Annotations | Enabled | `haproxy.org/*` annotation support |
| HAProxy Ingress | Enabled | HAProxy Ingress Controller compatibility |
| Path Regex Last | Disabled | Performance-first path matching |

For comprehensive documentation, see [Template Libraries](./docs/template-libraries.md).

## Documentation

Full documentation is available in the [chart docs site](./docs/):

- **Getting Started**: [Configuration](./docs/configuration.md), [IngressClass](./docs/ingress-class.md), [GatewayClass](./docs/gateway-class.md), [SSL Certificates](./docs/ssl-certificates.md), [Annotations](./docs/annotations.md)
- **HAProxy**: [HAProxy Deployment](./docs/haproxy-deployment.md) (resource limits, service architecture, pod requirements)
- **Template Libraries**: [Overview](./docs/template-libraries.md) and individual [library documentation](./docs/libraries/)
- **Operations**: [High Availability](./docs/operations/high-availability.md), [Monitoring](./docs/operations/monitoring.md), [Networking](./docs/operations/networking.md), [Debugging](./docs/operations/debugging.md), [Troubleshooting](./docs/operations/troubleshooting.md)
- **Reference**: [Complete values reference](./docs/reference.md)

## Upgrading

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0-alpha.11
```

With new values:

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0-alpha.11 \
  -f my-values.yaml
```

## Uninstalling

```bash
helm uninstall my-controller
```

This removes all resources created by the chart.

## Examples

See the `examples/` directory for:

- Basic Ingress setup
- Multi-namespace configuration
- Production-ready values
- NetworkPolicy configurations

## Contributing

Contributions are welcome! Please see the main repository for guidelines.

## License

See the main repository for license information.
