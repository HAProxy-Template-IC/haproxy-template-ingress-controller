## Why

This is a brownfield baseline: the controller is fully implemented but has no formal specifications. Reverse-engineering specs from the existing codebase establishes a behavioral contract that future changes can diff against, ensures refactoring preserves existing behavior, and creates living documentation of what the system actually does.

## What Changes

- No code changes. This change only produces specification artifacts.
- Creates baseline specs for all major capability areas by reverse-engineering behavior from the existing implementation.

## Capabilities

### New Capabilities

- `kubernetes-resource-watching`: Watches Kubernetes resources (Ingress, HTTPRoute, GRPCRoute, Gateway, Services, Endpoints, EndpointSlices, Secrets, ConfigMaps, custom CRDs) and maintains indexed in-memory stores.
- `template-engine`: Scriggo-based template engine with pre-compilation, custom filters (sort_by, glob_match, b64decode, extract, group_by, transform, debug, eval), built-in functions (dig, fallback, merge, keys, append, toSlice), caching (has_cached/get_cached/set_cached), macros, dynamic includes, and glob renders.
- `haproxy-config-generation`: Generates complete HAProxy configuration (haproxy.cfg), map files, general files, SSL certificates, and CRT lists from templates and Kubernetes resource data.
- `dataplane-sync`: Deploys generated configuration to HAProxy pods via the Dataplane API (v3.0/3.1/3.2), with intelligent diffing, fine-grained operations, runtime API optimization for non-reload changes, and multi-pod support.
- `validation-and-testing`: HAProxy configuration syntax validation, embedded validation tests in HAProxyTemplateConfig CRDs with multiple assertion types (haproxy_valid, contains, not_contains, equals, jsonpath), test fixtures, and CLI validation command.
- `leader-election`: Kubernetes Lease-based leader election for HA deployments with automatic failover (~15-20s), hot-standby replicas, and PodDisruptionBudget support.
- `metrics-and-observability`: Prometheus metrics (reconciliation cycles, deployment latency, error counts, config size), health/readiness endpoints, structured JSON logging, template tracing, and filter debug logging.
- `introspection`: Debug HTTP server with `/debug/vars` for inspecting internal state via JSONPath queries and `/debug/pprof` for Go profiling.
- `validating-webhook`: Admission webhook for HAProxyTemplateConfig CRDs with dry-run validation and TLS certificate management.
- `cli-commands`: Controller run mode (main daemon), validate command (local template/config validation), and benchmark command (rendering performance analysis).
- `storage-strategies`: Memory store (full in-memory), cached store (on-demand with TTL), and configurable index definitions for O(1) lookups.
- `event-driven-architecture`: Generic EventBus infrastructure with pub/sub, scatter-gather requests, typed events, and debounced reconciliation triggering.
- `haproxy-version-management`: Multi-version HAProxy support (3.0, 3.1, 3.2) with build-time version selection and matching controller/HAProxy image tags.
- `template-libraries`: Predefined template libraries (base, SSL, Ingress, Gateway API, HAProxy annotations, HAProxy Ingress annotations) that provide reusable macros for common patterns.
- `configuration-management`: HAProxyTemplateConfig CRD schema, credentials via Secrets, environment variables, CLI flags, and Helm chart values for deployment configuration.

### Modified Capabilities

_(none — this is the initial baseline)_

## Impact

- No code changes. Spec files created under `openspec/specs/`.
- Future changes will reference these baseline specs via delta specs (ADDED/MODIFIED/REMOVED).
