# HAPTIC Helm Chart

## Overview

This Helm chart deploys HAPTIC (HAProxy Template Ingress Controller), which manages HAProxy configurations dynamically based on Kubernetes resources.

HAPTIC:

- Watches Kubernetes Ingress and/or Gateway API resources
- Renders Scriggo templates to generate [HAProxy](https://www.haproxy.org/) configurations
- Deploys configurations to HAProxy pods via [Dataplane API](https://github.com/haproxytech/dataplaneapi)
- Supports cross-namespace HAProxy pod management
- Template library system for modular feature support
- Conditional resource watching based on enabled features

For controller architecture and behavior documentation, see the [controller docs](https://haproxy-haptic.org/controller/latest/).

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- **HAProxy 3.0 or newer** (the chart deploys HAProxy by default; template libraries require 3.0+ for SSL/TLS features)

!!! note
    The `haproxyVersion` value controls both the controller image tag and the HAProxy image tag, ensuring version compatibility between the two. See the [configuration reference](./reference.md) for details.

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

## What's in This Chart

The chart deploys:

- **Controller Deployment** -- the operator that watches resources and generates configurations
- **HAProxy Deployment** (optional) -- the load balancers that serve your traffic, with Dataplane API sidecars
- **HAProxyTemplateConfig CRD** -- merged template library configuration that drives config rendering
- **IngressClass** and **GatewayClass** -- routing API integration for Ingress and Gateway API resources
- **RBAC**, **NetworkPolicy**, and **ServiceAccount** -- permissions and network security
- Optional **ServiceMonitor** -- Prometheus integration for metrics scraping
- Optional **admission webhook** -- configuration validation before deployment

If you are new to HAPTIC, start with the [Getting Started guide](https://haproxy-haptic.org/controller/latest/getting-started/) to deploy your first template-driven configuration.

## Documentation Sections

### Configuration

- [Configuration](./configuration.md) - Key values, controller config, ingress class filtering
- [IngressClass](./ingress-class.md) - IngressClass configuration and multi-controller environments
- [GatewayClass](./gateway-class.md) - GatewayClass, parametersRef, and advanced scenarios
- [SSL Certificates](./ssl-certificates.md) - SSL and webhook certificate management
- [Annotations](./annotations.md) - Supported Ingress annotations

### HAProxy Deployment

- [HAProxy Deployment](./haproxy-deployment.md) - Resource limits, service architecture, pod requirements

### Template Libraries

- [Template Libraries Overview](./template-libraries.md) - Library system and extension points
- Individual library documentation in [Libraries](./libraries/base.md)

### Operations

- [High Availability](./operations/high-availability.md) - Leader election and replica configuration
- [Monitoring](./operations/monitoring.md) - Prometheus ServiceMonitor setup
- [Networking](./operations/networking.md) - NetworkPolicy configuration
- [Debugging](./operations/debugging.md) - Introspection endpoints
- [Troubleshooting](./operations/troubleshooting.md) - Common issues

### Reference

- [Configuration Reference](./reference.md) - Complete list of all Helm values

## Upgrading

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0-alpha.11
```

## Uninstalling

```bash
helm uninstall my-controller
```

This removes all resources created by the chart.
