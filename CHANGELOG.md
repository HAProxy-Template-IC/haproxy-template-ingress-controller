# Changelog

All notable changes to the HAProxy Template Ingress Controller will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For Helm chart changes, see [Chart CHANGELOG](./charts/haptic/CHANGELOG.md).

## [Unreleased]

### Added

- **Template-driven HAProxy configuration**: Generate HAProxy configs using Scriggo templates (Go-based, Jinja2-like syntax)
  - Full access to Kubernetes resources in templates via `resources.<type>.List()` and `resources.<type>.Get()`
  - Built-in functions for common operations: sorting, filtering, deduplication, base64 encoding
  - Template snippets for modular, reusable configuration blocks

- **Kubernetes Ingress support**: Full support for `networking.k8s.io/v1` Ingress resources
  - Path types: Exact, Prefix, ImplementationSpecific (regex)
  - TLS termination with Secret references
  - Default backend configuration
  - IngressClass filtering (`haproxy-template`)

- **Gateway API support**: Native support for Gateway API resources
  - HTTPRoute with path, header, query parameter, and method matching
  - GRPCRoute for gRPC traffic routing
  - TLSRoute for SNI-based routing
  - TCPRoute and UDPRoute for L4 traffic
  - Traffic splitting and weighted backends
  - Request/response header modification
  - URL rewrites and redirects

- **HAProxy annotation compatibility**: Drop-in compatibility with existing HAProxy ingress controllers
  - `haproxy.org/*` annotations (HAProxyTech ingress controller)
  - Backend config snippets, server options, load balancing algorithms
  - SSL passthrough, SSL redirect, CORS, basic auth

- **Modular template library system**: Composable libraries merged at Helm render time
  - `base.yaml`: Core HAProxy template structure
  - `ingress.yaml`: Kubernetes Ingress support
  - `gateway.yaml`: Gateway API support
  - `haproxytech.yaml`: HAProxy annotation compatibility
  - `ssl.yaml`: TLS/SSL features
  - Libraries can be enabled/disabled independently

- **Embedded validation tests**: Test HAProxy configurations within template libraries
  - Declarative test fixtures (Services, Endpoints, Ingresses, HTTPRoutes)
  - Assertions: HAProxy config validity, pattern matching, exact content
  - Run tests via `controller validate --test <name>`
  - CI integration for template regression testing

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

- **Default SSL certificate support**: Quick-start TLS configuration for development and testing
  - Optional reference to existing Kubernetes TLS Secret via `controller.defaultSSLCertificate.secretName`
  - Optional inline certificate creation via `controller.defaultSSLCertificate.create` with cert/key in values
  - Development helper script `scripts/generate-dev-ssl-cert.sh` for self-signed certificate generation
  - Certificate name/namespace passed to templates via `extraContext` (variables: `default_ssl_cert_name`, `default_ssl_cert_namespace`)
  - Comprehensive SSL configuration documentation in Helm chart README

- **Leader election for high availability**: Multiple controller replicas now supported with automatic leader election
  - Only the leader replica deploys configurations to HAProxy instances
  - All replicas continue watching resources, rendering templates, and validating configs (hot standby)
  - Automatic failover when leader fails (~15-20 seconds downtime)
  - Configurable timing parameters for failover speed and clock skew tolerance
  - Default deployment now uses 2 replicas for HA

- **Leader election metrics**: Three new Prometheus metrics for monitoring leadership
  - `haptic_leader_election_is_leader`: Current leadership status (gauge)
  - `haptic_leader_election_transitions_total`: Total leadership transitions (counter)
  - `haptic_leader_election_time_as_leader_seconds_total`: Cumulative time as leader (counter)

- **High availability operations guide**: Comprehensive documentation in `docs/operations/high-availability.md`
  - Configuration and deployment instructions
  - Leadership monitoring and troubleshooting
  - Best practices for production deployments
  - Migration guide from single-replica

- **Separate controller and HAProxy services**: Clean separation of concerns
  - Controller Service (`haproxy-template-ic`): ClusterIP for operational endpoints (healthz, metrics)
  - HAProxy Service (`haproxy-template-ic-haproxy`): Configurable LoadBalancer/ClusterIP for ingress traffic
  - Independent configuration for ports, annotations, and labels
