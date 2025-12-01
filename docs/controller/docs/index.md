# HAProxy Template IC

A template-driven HAProxy Ingress Controller for Kubernetes that generates HAProxy configurations using Jinja2 templates and deploys them via the HAProxy Dataplane API.

## What is HAProxy Template IC?

HAProxy Template IC is an event-driven Kubernetes controller that:

- **Watches Kubernetes resources** (Ingresses, Services, EndpointSlices, Secrets, and Gateway API resources)
- **Renders Jinja2 templates** to generate HAProxy configurations
- **Deploys configurations** to HAProxy pods via the Dataplane API
- **Validates configurations** before deployment to prevent outages

Unlike traditional ingress controllers with hardcoded configuration logic, HAProxy Template IC uses a template-driven approach that gives you full control over the generated HAProxy configuration.

## Key Features

- **Template Libraries** - Pre-built templates for Ingress and Gateway API with HAProxy annotation support
- **Resource-Agnostic Architecture** - Watch any Kubernetes resource type via configuration
- **High Availability** - Leader election with automatic failover
- **Validation Pipeline** - Configurations are validated before deployment
- **Observability** - Prometheus metrics, structured logging, and debug endpoints

## Quick Start

### Install with Helm

```bash
helm repo add haproxy-template-ic https://haproxy-template-ic.github.io/charts
helm repo update
helm install my-controller haproxy-template-ic/haproxy-template-ic
```

See the [Helm Chart Installation Guide](helm-chart/index.md) for detailed configuration options.

### Create an Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: example
spec:
  ingressClassName: haproxy
  rules:
    - host: example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: example-service
                port:
                  number: 80
```

## Documentation

| Section | Description |
|---------|-------------|
| [Getting Started](getting-started.md) | Initial setup and basic concepts |
| [Helm Chart](helm-chart/index.md) | Installation via Helm with pre-built templates |
| [Gateway API](helm-chart/gateway-api.md) | HTTPRoute and GRPCRoute support |
| [HAProxy Annotations](helm-chart/annotations.md) | Compatible with haproxy.org annotations |
| [Templating](templating.md) | Custom template development |
| [Operations](operations/high-availability.md) | Production deployment guidance |

## Architecture

```
                    Kubernetes Cluster
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
    ┌────▼────┐      ┌─────▼─────┐     ┌─────▼─────┐
    │ Ingress │      │  Service  │     │  Secret   │
    └────┬────┘      └─────┬─────┘     └─────┬─────┘
         │                 │                 │
         └────────────┬────┴─────────────────┘
                      │
              ┌───────▼───────┐
              │  Controller   │
              │   (Watcher)   │
              └───────┬───────┘
                      │
              ┌───────▼───────┐
              │   Template    │
              │    Engine     │
              └───────┬───────┘
                      │
              ┌───────▼───────┐
              │   Dataplane   │
              │     API       │
              └───────┬───────┘
                      │
              ┌───────▼───────┐
              │    HAProxy    │
              │     Pods      │
              └───────────────┘
```

## Why Template-Driven?

Traditional ingress controllers embed configuration logic in code. When you need custom HAProxy features, you're limited to what the controller supports or waiting for upstream changes.

HAProxy Template IC inverts this model:

1. **You control the templates** - Full access to HAProxy's configuration language
2. **Add features without code changes** - New HAProxy directives are just template updates
3. **Test configurations independently** - Validation tests run against your templates
4. **Version your configuration** - Templates are declarative and reviewable

## Project Status

HAProxy Template IC is in active development. The Helm chart provides production-ready defaults for:

- Kubernetes Ingress resources
- Gateway API HTTPRoute and GRPCRoute
- HAProxy Tech annotation compatibility

See the [Supported Configuration](supported-configuration.md) reference for the complete feature matrix.

## License

Apache License 2.0
