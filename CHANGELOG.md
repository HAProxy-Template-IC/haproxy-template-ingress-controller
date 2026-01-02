# Changelog

All notable changes to the HAProxy Template Ingress Controller will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For Helm chart changes, see [Chart CHANGELOG](./charts/haptic/CHANGELOG.md).

## [Unreleased]

### Added

- Test-specific `extraContext` overrides for validation tests

## [0.1.0-alpha.11] - 2026-01-02

### Added

- **HAProxy Ingress annotation compatibility**: Support for 47 haproxy-ingress annotations via the haproxy-ingress template library

### Fixed

- **Template validator currentConfig**: Add missing type declaration for `currentConfig` in TemplateValidator context

## [0.1.0-alpha.10] - 2026-01-01

### Added

- **Server slot preservation**: Preserve HAProxy server slots during rolling deployments to enable zero-reload runtime API updates via `currentConfig` template context

### Fixed

- **Reconciliation debounce during rolling deployments**: Fix timer reset on each resource change that violated maximum latency guarantee, causing 3-4+ second delays during rapid endpoint updates

## [0.1.0-alpha.9] - 2025-12-30

### Fixed

- **SSL certificate chain comparison**: Parse all certificates in PEM chain to match HAProxy API issuer format, fixing spurious updates for CA-signed certificates
- **Spurious update events in bulk watcher**: Skip update events where old and new objects are identical, reducing unnecessary reconciliations

## [0.1.0-alpha.8] - 2025-12-30

### Added

- **Configurable deployment timeout**: New `deploymentTimeout` field in dataplane config (default: 30s) to recover from stuck deployments

### Fixed

- **Deployment deadlock after leader transition**: Fix race condition where deployments become permanently stuck after leadership changes due to lost events between leader-only components

## [0.1.0-alpha.7] - 2025-12-30

### Changed

- **Reconciliation debouncing**: Switch from trailing-edge to leading-edge triggering with 100ms refractory period (down from 500ms), reducing latency for isolated changes from 500ms to 0ms

### Fixed

- **SSL certificate comparison**: Fix identifier format mismatch causing unnecessary re-uploads every reconciliation cycle

## [0.1.0-alpha.6] - 2025-12-30

### Added

- **Stall detection**: Components now detect when they're blocked and report unhealthy via `/healthz`, enabling automatic pod restart via Kubernetes liveness probes

## [0.1.0-alpha.5] - 2025-12-30

### Fixed

- **Template validation errors**: Improved error messages with line/column location and template context; errors now logged at ERROR level and visible in CRD status

## [0.1.0-alpha.4] - 2025-12-29

### Fixed

- **Scriggo template engine**: Update fork with fix for `break` statement in nested loops causing premature loop termination

## [0.1.0-alpha.3] - 2025-12-28

### Fixed

- **Resource store ordering**: Ensure deterministic ordering when querying resources for consistent HAProxy server slot assignment

## [0.1.0-alpha.2] - 2025-12-28

### Fixed

- **SSL certificate filenames**: Sanitize certificate filenames with dots to match HAProxy storage format

## [0.1.0-alpha.1] - 2025-12-27

### Added

- **Template-driven HAProxy configuration**: Generate HAProxy configs using Scriggo templates (Go-based, Jinja2-like syntax)
  - Full access to Kubernetes resources in templates via `resources.<type>.List()` and `resources.<type>.Get()`
  - Built-in functions for common operations: sorting, filtering, deduplication, base64 encoding
  - Template snippets for modular, reusable configuration blocks

- **Embedded validation tests**: Test HAProxy configurations within template libraries
  - Declarative test fixtures (Services, Endpoints, Ingresses, HTTPRoutes)
  - Assertions: HAProxy config validity, pattern matching, exact content
  - Run tests via `controller validate --test <name>`

- **Dry-run validation webhook**: Validate configuration changes before applying
  - Admission webhook for HAProxyTemplateConfig CRD
  - Renders templates with proposed changes
  - Validates resulting HAProxy config syntax
  - Rejects invalid configurations with detailed errors

- **Multi-architecture container images**: Support for common Kubernetes platforms
  - linux/amd64, linux/arm64, linux/arm/v7
  - Based on official HAProxy Docker images

- **Multiple HAProxy version support**: Choose your HAProxy version
  - HAProxy 3.0, 3.1, 3.2 variants available
  - Version-specific images tagged accordingly

- **Supply chain security**: Signed artifacts and transparency
  - All container images signed with Cosign (keyless OIDC)
  - SBOM (Software Bill of Materials) attestations in SPDX format
  - Signed release binaries with SHA256 checksums
  - Helm chart OCI artifacts signed

- **Prometheus metrics**: Comprehensive observability
  - Reconciliation timing and success/failure counts
  - Template rendering duration
  - HAProxy config validation results
  - Kubernetes API request latencies

- **Leader election for high availability**: Multiple controller replicas now supported with automatic leader election
  - Only the leader replica deploys configurations to HAProxy instances
  - All replicas continue watching resources, rendering templates, and validating configs (hot standby)
  - Automatic failover when leader fails (~15-20 seconds downtime)
  - Configurable timing parameters for failover speed and clock skew tolerance
