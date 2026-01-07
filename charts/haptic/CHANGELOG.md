# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added

- `haproxy.dataplane.validateConfig` value to control server-side config validation (default: `false` since controller validates locally)
- `controller.config.dataplane.maxParallel` value to limit concurrent Dataplane API operations (auto-calculated from dataplane GOMAXPROCS × 10 by default)
- Auth realm validation for `haproxy.org/auth-realm` annotation with configurable sanitization via `extraContext.sanitize_auth_realm`
- HAProxy `-m` memory limit flag automatically set from container memory requests to prevent OOMKill
- `extraEnv`, `haproxy.extraEnv`, and `haproxy.dataplane.extraEnv` for custom environment variables on all containers
- Auto-calculated `GOMAXPROCS` for dataplane container based on memory limits (prevents OOMKill on large nodes without CPU limits)

### Changed

- Basic auth now matches official HAProxy Ingress Controller behavior: userlist naming changed to `{namespace}-{ingressName}`, uses `http_auth_group()` with `authenticated-users` group
- Set production-ready default resource requests and limits for all containers:
  - Controller: 100m CPU / 512Mi memory (Guaranteed QoS)
  - HAProxy: 250m CPU / 1Gi memory (Guaranteed QoS)
  - Dataplane API sidecar: 50m CPU / 256Mi memory (Guaranteed QoS)
- No CPU limits to avoid throttling; memory requests equal limits for eviction protection

### Fixed

- CRD now includes `extraContext` field in validation tests for per-test context overrides

## [0.1.0-alpha.11] - 2026-01-02

### Added

- HAProxy Ingress template library (`haproxy-ingress.yaml`) with 47 annotation compatibility

### Changed

- HAProxy Ingress library now enabled by default (`controller.templateLibraries.haproxy-ingress.enabled=true`)
- Updated appVersion to 0.1.0-alpha.11

## [0.1.0-alpha.10] - 2026-01-01

### Changed

- Updated appVersion to 0.1.0-alpha.10 (server slot preservation, reconciliation debounce fix)

## [0.1.0-alpha.9] - 2025-12-30

### Changed

- Dataplane API credentials are now auto-generated if not provided (32-char random password)
- Consolidated credential configs into single `credentials.dataplane` section
- HAProxy deployment now reads credentials from Secret via environment variables
- Changed dataplane API probes from `httpGet` to `tcpSocket`
- Updated appVersion to 0.1.0-alpha.9 (SSL certificate chain fix, spurious update events fix)

### Removed

- Removed `haproxy.dataplane.credentials` section (use `credentials.dataplane` instead)

## [0.1.0-alpha.8] - 2025-12-30

### Changed

- Updated appVersion to 0.1.0-alpha.8 (deployment timeout, leader transition deadlock fix)

## [0.1.0-alpha.7] - 2025-12-30

### Changed

- Updated appVersion to 0.1.0-alpha.7 (reconciliation debouncing, SSL certificate fix)

## [0.1.0-alpha.6] - 2025-12-30

### Changed

- Updated appVersion to 0.1.0-alpha.6 (stall detection)

### Removed

- Removed redundant `controller.config.logging.level` default value from values.yaml

## [0.1.0-alpha.5] - 2025-12-30

### Changed

- Updated appVersion to 0.1.0-alpha.5 (template validation error improvements)

## [0.1.0-alpha.4] - 2025-12-29

### Changed

- Create `NetworkPolicy` for HAProxy instances by default

### Fixed

- Backend server generation now resolves Service targetPort to actual pod port

## [0.1.0-alpha.3] - 2025-12-28

### Fixed

- Service configuration values now rendered in templates (previously defined but unused):
  - Controller service: `clusterIP`, `loadBalancerIP`, `loadBalancerSourceRanges`, `loadBalancerClass`, `externalTrafficPolicy`, `internalTrafficPolicy`, `sessionAffinity`, `sessionAffinityConfig`, `annotations`
  - HAProxy service: `loadBalancerIP`, `loadBalancerSourceRanges`, `loadBalancerClass`, `externalTrafficPolicy`, `internalTrafficPolicy`, `healthCheckNodePort`, `publishNotReadyAddresses`

## [0.1.0-alpha.2] - 2025-12-28

### Changed

- Updated appVersion to 0.1.0-alpha.2 (SSL certificate filename fix)

## [0.1.0-alpha.1] - 2025-12-26

### Added

- Initial Helm chart for HAProxy Template Ingress Controller
- Leader election support with configurable replicas (default: 2 for HA)
- Default SSL certificate configuration via values
  - `controller.defaultSSLCertificate.secretName`: Reference existing Secret
  - `controller.defaultSSLCertificate.create`: Create Secret from inline cert/key
  - Certificate name/namespace passed to templates via `extraContext`
- Separate controller and HAProxy services
  - Controller Service: ClusterIP for operational endpoints (healthz, metrics)
  - HAProxy Service: Configurable LoadBalancer/ClusterIP for ingress traffic
- Modular template library system with composable libraries merged at Helm render time
  - `base.yaml`: Core HAProxy template structure
  - `ingress.yaml`: Kubernetes Ingress support
  - `gateway.yaml`: Gateway API support
  - `haproxytech.yaml`: HAProxy annotation compatibility
  - `ssl.yaml`: TLS/SSL features
  - Libraries can be enabled/disabled via `controller.templateLibraries.<name>.enabled`
- Kubernetes Ingress support via `ingress.yaml` library
  - Path types: Exact, Prefix, ImplementationSpecific (regex)
  - TLS termination with Secret references
  - Default backend configuration
  - IngressClass filtering (`haproxy-template`)
- Gateway API support via `gateway.yaml` library
  - HTTPRoute with path, header, query parameter, and method matching
  - GRPCRoute for gRPC traffic routing
  - TLSRoute for SNI-based routing
  - TCPRoute and UDPRoute for L4 traffic
  - Traffic splitting and weighted backends
  - Request/response header modification
  - URL rewrites and redirects
- HAProxy annotation compatibility via `haproxytech.yaml` library
  - `haproxy.org/*` annotations (HAProxyTech ingress controller)
  - Backend config snippets, server options, load balancing algorithms
  - SSL passthrough, SSL redirect, CORS, basic auth
