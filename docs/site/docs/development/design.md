# Design Documentation

## Overview

HAPTIC is a Kubernetes operator that watches Kubernetes resources and renders them through templates into validated HAProxy configurations.

Components communicate through a central EventBus using pub/sub and request-response patterns, enabling observability, testability, and loose coupling.

**Why this architecture:**

- Event-driven design allows components to evolve independently
- Template-based approach provides maximum flexibility without annotation constraints
- Multi-phase validation prevents invalid configurations from reaching production
- Runtime API optimization minimizes service disruption during updates

## Navigation

This section is for people changing HAPTIC's Go code and charts; if you run HAPTIC rather than develop it, the Operations guides (starting with [High Availability](../operations/high-availability.md)) are for you.

The design documentation is organized the way the sidebar groups it:

**Architecture**

- **[Architecture Overview](design/architecture-overview.md)** - High-level system architecture with component diagrams showing the controller's internal event-driven structure, validation flow, and the operating assumptions and constraints the design rests on

- **[Package Structure](design/package-structure.md)** - Go package organization including directory structure, package dependencies, and key interfaces

- **[Sequence Diagrams](design/sequence-diagrams.md)** - Dynamic behavior including startup initialization, resource change handling, configuration validation, and zero-reload deployment

**Components**

- **[Configuration Model](design/configuration.md)** - User interface design showing how you configure the controller through HAProxyTemplateConfig CRD

- **[Deployment](design/deployment.md)** - Kubernetes deployment architecture showing controller pods, HAProxy pods, container configuration, and network topology

- **[Leader Election](design/leader-election.md)** - Lease-based leader election, the leader-only vs all-replica component split, and the bootstrap pattern that prevents missed events on leadership transitions

- **[Runtime Introspection](design/introspection.md)** - Debug HTTP endpoints for runtime state inspection, event history tracking, and integration with acceptance testing

**Validation and decisions**

- **[CRD Validation](crd-validation-design.md)** - Durable design decisions behind the `HAProxyTemplateConfig` CRD and the three-layer validation stack (OpenAPI schema, admission webhooks, runtime validation)

- **[Design Decisions](design/design-decisions.md)** - Key architectural choices with rationale covering validation strategy, template engine selection, concurrency model, and observability integration

Process pages — [Linting](linting.md), [Releasing](releasing.md), and the [Gateway API Upgrade Playbook](gateway-api-upgrade-playbook.md) — cover the contributor workflow rather than the design.

## Core Capabilities

The controller provides these capabilities:

**Template-Driven Configuration**
Generate HAProxy configurations using Scriggo templates with access to any Kubernetes resources you choose to watch. Templates give you complete control over the HAProxy configuration without annotation limitations.

**Dynamic Resource Watching**
Monitor any Kubernetes resource types (Ingress, Service, ConfigMap, custom CRDs) you specify. Resources are indexed using JSONPath expressions for fast template lookups.

**Validation-First Deployment**
All generated configurations pass three validation phases before they can reach production, split across two gates: the admission webhook runs all three (client-native syntax parse, version-specific OpenAPI schema check, and the `haproxy -c` semantic check), so invalid input never lands in etcd; the leader's reconcile pipeline then re-runs syntax + schema on every render, and the Dataplane API re-validates server-side on push.

**Zero-Reload Optimization**
Configuration changes that only modify server weight, address, port, or maintenance state are applied through HAProxy's runtime API without process reloads. This maintains existing connections and minimizes service disruption. Changes requiring structural modifications trigger a reload.

**Structured Configuration Comparison**
The controller parses both current and desired configurations into structured representations and performs fine-grained comparison at the attribute level. This minimizes unnecessary deployments and maximizes use of runtime API operations.

**Declarative Kubernetes Resource Emission**
Templates declared under `spec.k8sResources` produce one or more Kubernetes resources per render (multi-doc YAML, `---`-separated). The renderer parses the rendered YAML, validates required fields, and feeds the result into the controller's `resourceapplier`, which reconciles the set to the cluster via Server-Side Apply with field manager `haptic`, prunes orphans across renders, and injects an `OwnerReference` to the `HAProxyTemplateConfig` CR so cascade-delete (e.g. `helm uninstall`) GCs the rendered resources. Same engine context as the main `haproxyConfig` template (resources, filters, snippets, file registry, shared cache) — chart libraries can compose extension points and shared state across the two passes.

## Design Principles

**Resource-Agnostic Engine**
The Go code must be agnostic to every Kubernetes resource you choose to watch. If you decide to use some CRD instead of Gateway or Ingress resources, you should only need to touch HAPTIC templates and config — **no Go code**. Writing templates for an arbitrary CRD must be just as comfortable as for Ingress or Gateway API resources, with **no preferential treatment for well-known resources**. Resource shape comes from the kube-apiserver (live) or `--schema-dir` (offline) at runtime; the controller never bakes in a fixed list of supported kinds. This applies to all Go-side machinery — engine filters, runtime-context types, generated wrappers, helpers — and to chart-side scaffolding that crosses the Go/template boundary. The corollary on the chart side: resource-specific behavior lives in resource-specific template libraries (`ingress.yaml`, `gateway/*.yaml`, vendor annotation libs), never in `base.yaml`.

**Fail-Safe Operation**
Invalid configurations are rejected before reaching production. The validation phase catches syntax errors, semantic issues, and configuration conflicts. If validation fails, the current production configuration remains unchanged.

**Performance Through Indexing**
Resource indexing using JSONPath expressions enables O(1) lookups in templates. Debouncing at the per-watcher level coalesces rapid resource changes into a single `ResourceIndexUpdatedEvent` before the reconciler fires; the reconciler itself triggers immediately with no added latency. Rate limiting (the deployer's `minDeploymentInterval`) prevents deployment conflicts.

**Observable Event Flow**
All component interactions flow through the EventBus. The Event Commentator subscribes to all events and produces structured logs with contextual insights. Metrics track reconciliation cycles, validation results, and deployment success rates.

**Clean Component Separation**
Pure business logic components (templating, k8s, dataplane) have no event dependencies and can be tested in isolation. Event adapters in the controller package coordinate these pure components through EventBus messages.

## See Also

### User Guides

- [Templating Guide](../templating.md) - User guide for writing templates
- [CRD Reference](../crd-reference.md) - HAProxyTemplateConfig CRD documentation
- [Supported Configuration Reference](../supported-configuration.md) - What HAProxy features you can configure

### Operations

- [High Availability](../operations/high-availability.md) - Leader election and HA deployments
- [Monitoring](../operations/monitoring.md) - Prometheus metrics and alerting
- [Debugging](../operations/debugging.md) - Runtime introspection and troubleshooting
- [Security](../operations/security.md) - RBAC and security best practices
- [Performance](../operations/performance.md) - Resource sizing and optimization

### Package Documentation

- [Controller Package](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/controller/README.md) - Event-driven controller implementation
- [Template Engine](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/templating/README.md) - Template engine API reference
- [Kubernetes Integration](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/k8s/README.md) - Resource watching and indexing API
- [Dataplane Integration](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/dataplane/README.md) - HAProxy configuration synchronization
