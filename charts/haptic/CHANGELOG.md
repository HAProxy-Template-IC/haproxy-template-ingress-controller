# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

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
