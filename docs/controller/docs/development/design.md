# Design Documentation

## Overview

This document provides the architectural design for the HAProxy Template Ingress Controller. The controller is a Kubernetes operator that manages HAProxy load balancer configurations through template-driven configuration generation, continuously monitoring Kubernetes resources and translating them into validated HAProxy configurations.

The design follows event-driven architecture principles with clean component separation. Components communicate through a central EventBus using pub/sub and request-response patterns, enabling observability, testability, and loose coupling.

**Why this architecture:**
- Event-driven design allows components to evolve independently
- Template-based approach provides maximum flexibility without annotation constraints
- Multi-phase validation prevents invalid configurations from reaching production
- Runtime API optimization minimizes service disruption during updates

## Navigation

The design documentation is organized into focused documents:

- **[Considerations](design/considerations.md)** - Assumptions about the operating environment, constraints imposed by HAProxy Dataplane API, and Kubernetes cluster requirements

- **[Architecture Overview](design/architecture-overview.md)** - High-level system architecture with component diagrams showing the controller's internal event-driven structure and validation flow

- **[Package Structure](design/package-structure.md)** - Go package organization including directory structure, package dependencies, and key interfaces

- **[Sequence Diagrams](design/sequence-diagrams.md)** - Dynamic behavior including startup initialization, resource change handling, configuration validation, and zero-reload deployment

- **[Deployment](design/deployment.md)** - Kubernetes deployment architecture showing controller pods, HAProxy pods, container configuration, and network topology

- **[Design Decisions](design/design-decisions.md)** - Key architectural choices with rationale covering validation strategy, template engine selection, concurrency model, and observability integration

- **[Runtime Introspection](design/introspection.md)** - Debug HTTP endpoints for runtime state inspection, event history tracking, and integration with acceptance testing

- **[Configuration](design/configuration.md)** - User interface design showing how you configure the controller through HAProxyTemplateConfig CRD

- **[Appendices](design/appendices.md)** - Definitions, abbreviations, and external references

## Core Capabilities

The controller provides these capabilities:

**Template-Driven Configuration**
Generate HAProxy configurations using Jinja2-like templates with access to any Kubernetes resources you choose to watch. Templates give you complete control over the HAProxy configuration without annotation limitations.

**Dynamic Resource Watching**
Monitor any Kubernetes resource types (Ingress, Service, ConfigMap, custom CRDs) you specify. Resources are indexed using JSONPath expressions for fast template lookups. You define which resources to watch and how to index them.

**Validation-First Deployment**
All generated configurations pass through two-phase validation before deployment. The client-native parser validates syntax and structure, then the HAProxy binary performs semantic validation. This prevents invalid configurations from reaching production instances.

**Zero-Reload Optimization**
Configuration changes that only modify server weight, address, port, or maintenance state are applied through HAProxy's runtime API without process reloads. This maintains existing connections and minimizes service disruption. Changes requiring structural modifications trigger a reload.

**Structured Configuration Comparison**
The controller parses both current and desired configurations into structured representations and performs fine-grained comparison at the attribute level. This minimizes unnecessary deployments and maximizes use of runtime API operations.

## Design Principles

**Fail-Safe Operation**
Invalid configurations are rejected before reaching production. The validation phase catches syntax errors, semantic issues, and configuration conflicts. If validation fails, the current production configuration remains unchanged.

**Performance Through Indexing**
Resource indexing using JSONPath expressions enables O(1) lookups in templates. Debouncing prevents rapid successive template renders during bulk resource changes. Rate limiting prevents deployment conflicts.

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
- [Controller Package](../../pkg/controller/README.md) - Event-driven controller implementation
- [Template Engine](../../pkg/templating/README.md) - Template engine API reference
- [Kubernetes Integration](../../pkg/k8s/README.md) - Resource watching and indexing API
- [Dataplane Integration](../../pkg/dataplane/README.md) - HAProxy configuration synchronization
