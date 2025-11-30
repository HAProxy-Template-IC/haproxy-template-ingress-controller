# HAProxy Template I(ngress)C(ontroller)

<p align="center">
  <img src="docs/assets/logo.svg" alt="HAProxy Template IC Logo" width="400">
</p>

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Build Status](https://github.com/HAProxy-Template-IC/haproxy-template-ingress-controller/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/HAProxy-Template-IC/haproxy-template-ingress-controller/actions/workflows/ci.yml)

## Overview

The HAProxy Template IC is a Kubernetes operator that manages HAProxy load balancer configurations through Jinja2-like templates. Unlike annotation-based ingress controllers, you define the entire HAProxy configuration using templates, giving you complete control over all HAProxy features without being constrained by a predefined set of annotations.

The controller watches Kubernetes resources you specify (Ingress, Service, Secrets, custom CRDs, or any combination), renders your templates with the current resource state, validates the generated configuration, and deploys it to HAProxy instances using the Dataplane API. Configuration changes are applied through HAProxy's runtime API when possible to avoid process reloads and maintain existing connections.

**When to use this controller:**
- You need access to HAProxy features not exposed by annotation-based controllers (custom ACLs, stick tables, rate limiting, multi-tier routing)
- You want to define your own data model using Kubernetes resources rather than adapting to the Ingress resource structure
- You need fine-grained control over how Kubernetes resources map to HAProxy configuration
- You're comfortable writing and maintaining Jinja2 templates

## Features

- **Template-driven configuration**: Use Jinja2-like syntax to generate HAProxy configurations from any Kubernetes resources
- **Flexible resource watching**: Monitor any Kubernetes resource type (Ingress, Service, Secrets, CRDs) as input to your templates
- **Multi-phase validation**: Configurations are validated by the client-native parser and HAProxy binary before deployment
- **Embedded validation tests**: Write tests directly in your HAProxyTemplateConfig with fixtures and assertions to catch template errors in CI/CD. Includes debugging flags (`--verbose`, `--dump-rendered`, `--trace-templates`) for troubleshooting failures
- **Zero-reload optimization**: Uses HAProxy runtime API for server weight, address, and maintenance state changes
- **High availability**: Leader election with multiple controller replicas for automatic failover and zero-downtime upgrades
- **Smart deployment scheduling**: Rate limiting prevents concurrent deployments, periodic drift detection corrects external modifications
- **Event-driven architecture**: Components communicate through an event bus for clean separation and observability
- **Prometheus metrics**: Comprehensive metrics for controller operations, reconciliation cycles, deployment success rates, and leader election status

## Quick Start

This guide shows you how to deploy the controller and create your first template-driven configuration.

> [!NOTE]
> The examples below use simplified HAProxy deployments for demonstration. For production deployments with proper volume sharing, health checks, and HA setup, see the [Helm chart documentation](charts/haproxy-template-ic/README.md#haproxy-pod-requirements).

### Deploy HAProxy with Dataplane API

The controller manages HAProxy instances through the Dataplane API. You need to deploy HAProxy pods with Dataplane API sidecars:

```bash
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: haproxy
      component: loadbalancer
  template:
    metadata:
      labels:
        app: haproxy
        component: loadbalancer
    spec:
      containers:
      # Main HAProxy container
      - name: haproxy
        image: haproxytech/haproxy-debian:3.2
        ports:
        - containerPort: 80
          name: http
        - containerPort: 443
          name: https
      # Dataplane API sidecar for configuration management
      - name: dataplane
        image: haproxytech/dataplane-api:3.0
        args:
          - --host=0.0.0.0
          - --port=5555
          - --haproxy-bin=/usr/local/sbin/haproxy
          - --config-file=/etc/haproxy/haproxy.cfg
        env:
        - name: DATAPLANE_USER
          value: admin
        - name: DATAPLANE_PASS
          value: adminpass
        ports:
        - containerPort: 5555
          name: dataplane-api
EOF
```

### Install the Controller

Install the controller using Helm:

```bash
helm install haproxy-ic ./charts/haproxy-template-ic \
  --set credentials.dataplane.username=admin \
  --set credentials.dataplane.password=adminpass
```

The default configuration watches Ingress resources and generates corresponding HAProxy frontends and backends. You can customize the configuration by creating a HAProxyTemplateConfig CRD resource.

### Create an Ingress Resource

Create an Ingress to test the controller:

```bash
kubectl apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: example
  namespace: default
spec:
  rules:
  - host: example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: my-service
            port:
              number: 80
EOF
```

### Verify the Deployment

Check that the controller rendered and deployed the configuration:

```bash
# View controller logs to see the reconciliation process
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic -n haproxy-template-ic

# Verify the generated HAProxy configuration in one of the HAProxy pods
kubectl exec -it deployment/haproxy -c haproxy -- cat /etc/haproxy/haproxy.cfg
```

You should see your Ingress resource translated into HAProxy frontend rules and backend server definitions.

## Configuration Example

Here's a minimal configuration that watches Kubernetes Ingress resources and generates HAProxy backends:

```yaml
# HAProxyTemplateConfig CRD: haproxy-template-ic-config
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haproxy-template-ic-config
  namespace: default
spec:
  watchedResources:
  # Watch Ingress resources across all namespaces
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    # Index by namespace and name for O(1) lookups in templates
    index_by:
      - metadata.namespace
      - metadata.name

haproxy_config:
  template: |
    global
        daemon
        maxconn 256

    defaults
        mode http
        timeout client 30s
        timeout server 30s
        timeout connect 5s

    # Frontend that routes requests based on Host header
    frontend http
        bind :80
        {% for ingress in resources.ingresses %}
        {% for rule in ingress.spec.rules %}
        # Route for {{ rule.host }}
        acl host_{{ ingress.metadata.name }} hdr(host) -i {{ rule.host }}
        use_backend {{ ingress.metadata.name }} if host_{{ ingress.metadata.name }}
        {% endfor %}
        {% endfor %}

    # Backend for each Ingress resource
    {% for ingress in resources.ingresses %}
    backend {{ ingress.metadata.name }}
        balance roundrobin
        {% for rule in ingress.spec.rules %}
        {% for path in rule.http.paths %}
        # Server pointing to Kubernetes Service
        server {{ path.backend.service.name }} {{ path.backend.service.name }}.{{ ingress.metadata.namespace }}.svc.cluster.local:{{ path.backend.service.port.number }} check
        {% endfor %}
        {% endfor %}
    {% endfor %}
```

Any Ingress resource created in your cluster automatically appears in the `resources.ingresses` collection, and the template generates the corresponding HAProxy configuration. The controller renders the template, validates it, and deploys it to all HAProxy instances.

## Architecture

The controller watches Kubernetes resources, renders HAProxy configurations from templates, and deploys them to HAProxy instances via the Dataplane API:

```mermaid
graph LR
    K8S[(Kubernetes API)]

    subgraph ctrl[Controller Deployment]
        CTL[Template Controller]
    end

    subgraph pod[HAProxy Pod]
        DPA[Dataplane API<br/>Sidecar]
        HAP[HAProxy]
    end

    K8S -->|1. Watch/Fetch<br/>Resources| CTL
    CTL -->|2. Render<br/>Templates| CTL
    CTL -->|3. Deploy<br/>Config| DPA
    DPA -.->|4. Configure| HAP
```

Internally, the controller uses an event-driven architecture where components communicate exclusively through an EventBus:

```
Kubernetes API → Resource Watchers → EventBus → Reconciler
                                         ↓
                   Template Renderer → Configuration Validator
                                         ↓
                   Deployment Scheduler → HAProxy Deployer
                                         ↓
                              HAProxy Dataplane API
```

**Key components:**

- **Resource Watchers**: Monitor Kubernetes resources and index them for fast template lookups
- **Reconciler**: Debounces resource changes and triggers reconciliation cycles
- **Template Renderer**: Renders Jinja2 templates using indexed Kubernetes resources
- **Configuration Validator**: Validates generated configurations using client-native parser and HAProxy binary
- **Deployment Scheduler**: Rate-limits deployments and prevents version conflicts
- **HAProxy Deployer**: Deploys configurations to HAProxy instances via Dataplane API

For detailed architecture documentation, see [docs/development/design.md](docs/development/design.md).

## Validation Tests

The controller includes a built-in test runner for validating HAProxy templates before deployment. You can embed validation tests directly in your HAProxyTemplateConfig CRD to catch template errors early in your development workflow.

**Add tests to your configuration:**

```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: my-config
spec:
  # ... template configuration ...

  validationTests:
    - name: test-basic-frontend
      description: Frontend should be created with correct settings
      fixtures:
        services:
          - apiVersion: v1
            kind: Service
            metadata:
              name: my-service
              namespace: default
            spec:
              ports:
                - port: 80
                  targetPort: 8080
      assertions:
        - type: haproxy_valid
          description: Configuration must be syntactically valid

        - type: contains
          target: haproxy.cfg
          pattern: "frontend.*default"
          description: Must have default frontend
```

**Run tests locally:**

```bash
# Run all validation tests
controller validate -f config.yaml

# Run specific test by name
controller validate -f config.yaml --test test-basic-frontend

# Output results as JSON (for CI/CD integration)
controller validate -f config.yaml --output json
```

**Supported assertion types:**
- `haproxy_valid` - Validates HAProxy syntax using the HAProxy binary
- `contains` - Verifies configuration contains a regex pattern
- `not_contains` - Verifies configuration doesn't contain a pattern
- `equals` - Checks exact value matches
- `jsonpath` - Queries template rendering context

For complete documentation on writing and running validation tests, see [docs/validation-tests.md](docs/validation-tests.md).

## Documentation

### User Guides

- [Getting Started](docs/getting-started.md) - Step-by-step guide to deploying and configuring the controller
- [CRD Reference](docs/crd-reference.md) - HAProxyTemplateConfig custom resource documentation (recommended)
- [Configuration Reference](docs/configuration.md) - Legacy ConfigMap configuration (deprecated)
- [Templating Guide](docs/templating.md) - How to write templates for HAProxy configuration, maps, and certificates
- [Validation Tests](docs/validation-tests.md) - Write and run embedded validation tests for template verification
- [Watching Resources](docs/watching-resources.md) - Resource watching and storage strategies
- [Supported HAProxy Configuration](docs/supported-configuration.md) - HAProxy features available through templates
- [Troubleshooting](docs/troubleshooting.md) - Common issues and solutions

### Operations

- [High Availability](docs/operations/high-availability.md) - Leader election and multi-replica deployments
- [Monitoring](docs/operations/monitoring.md) - Prometheus metrics and alerting
- [Debugging](docs/operations/debugging.md) - Runtime introspection and troubleshooting
- [Security](docs/operations/security.md) - RBAC, secrets, and security best practices
- [Performance](docs/operations/performance.md) - Resource sizing and optimization
- [Helm Chart](charts/haproxy-template-ic/README.md) - Installation and configuration guide

### Package Documentation

- [Controller](pkg/controller/README.md) - Event-driven controller orchestration
- [Core](pkg/core/README.md) - Configuration API reference
- [Dataplane](pkg/dataplane/README.md) - HAProxy integration and synchronization
- [Templating](pkg/templating/README.md) - Template engine usage and Jinja2 syntax
- [Kubernetes](pkg/k8s/README.md) - Resource watching and indexing
- [Events](pkg/events/README.md) - Event bus infrastructure

### Development Documentation

- [Design Documentation](docs/development/design.md) - Architecture overview and design decisions
- [CRD Validation Design](docs/development/crd-validation-design.md) - CRD implementation and validation framework
- [Linting Guidelines](docs/development/linting.md) - Code quality and linting setup

## Development

### Build and Test

```bash
# Build the controller binary
make build

# Run unit tests
make test

# Run integration tests (requires kind cluster)
make test-integration

# Run linting checks
make lint

# Run all checks (tests + linting)
make check-all

# Build Docker image
make docker-build

# Generate coverage report
make test-coverage
```

### Local Development Environment

The project includes scripts for local development with kind:

```bash
# Start development cluster with controller
./scripts/start-dev-env.sh

# Rebuild and restart controller after code changes
./scripts/start-dev-env.sh --restart

# View controller logs
./scripts/start-dev-env.sh logs

# Check deployment status
./scripts/start-dev-env.sh status

# Test ingress functionality
./scripts/start-dev-env.sh test

# Clean up development environment
./scripts/start-dev-env.sh down
```

> [!WARNING]
> Always use the `kind-haproxy-template-ic-dev` cluster context for development work. The `kind-haproxy-test` context is reserved for integration tests and will be automatically created and destroyed by test runs.

### Pre-commit Hooks

Set up automatic code quality checks using pre-commit:

```bash
# Install pre-commit (one-time setup)
pip install pre-commit
# or: brew install pre-commit

# Install git hooks (one-time per repository clone)
pre-commit install

# Hooks now run automatically on git commit
git commit -m "my changes"  # Runs make lint && make audit

# Skip hooks if needed (for WIP commits)
git commit --no-verify -m "WIP"

# Run hooks manually on all files
pre-commit run --all-files
```

The hooks run `make lint` and `make audit` before each commit to catch issues early.

## Contributing

Contributions are welcome. Before submitting pull requests:

1. Run `make check-all` to verify code quality
2. Add tests for new functionality
3. Update documentation as needed
4. Follow existing code style and patterns

See [CLAUDE.md](CLAUDE.md) for detailed development context and patterns.

## License

Licensed under the Apache License 2.0 - see [LICENSE](LICENSE) file for details.

Copyright 2025 Philipp Hossner

## Acknowledgments

This project builds on open source software:

- [Kubernetes client-go](https://github.com/kubernetes/client-go) - Kubernetes API client library
- [HAProxy client-native](https://github.com/haproxytech/client-native) - HAProxy Dataplane API client
- [Gonja](https://github.com/nikolalohinski/gonja) - Jinja2-like templating engine for Go
