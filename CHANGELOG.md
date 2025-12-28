# Changelog

All notable changes to the HAProxy Template Ingress Controller will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For Helm chart changes, see [Chart CHANGELOG](./charts/haptic/CHANGELOG.md).

## [Unreleased]

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
