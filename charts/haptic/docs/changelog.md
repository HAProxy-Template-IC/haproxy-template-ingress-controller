# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](/controller/latest/changelog/).

## [Unreleased]

### Added

- Expanded HAProxy Ingress library with 56 annotations for haproxy-ingress.github.io compatibility:
  - Path matching: `path-type` with regex, exact, prefix, and begin modes
  - Backend configuration: timeouts, load balancing, connection limits, health checks
  - Session affinity: cookie-based persistence with full configuration options
  - Access control: allowlist and denylist source ranges
  - SSL features: ssl-passthrough, backend SSL/mTLS, PROXY protocol
  - Redirects: ssl-redirect, app-root, redirect-to
  - Security headers: HSTS with full options, CORS
  - Authentication: basic auth with secret reference

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
  - Path types: Exact, Prefix, ImplementationSpecific (regex, via the haproxy-ingress library's `path-type: regex` annotation)
  - TLS termination with Secret references
  - Default backend configuration
  - IngressClass filtering (default class name: `haptic`; override `ingressClass.name` when replacing an incumbent controller)
- Gateway API support via `gateway.yaml` library
  - HTTPRoute and GRPCRoute are watched and routed (path, header, query-parameter, and method matchers)
  - Traffic splitting and weighted backends
  - RequestHeaderModifier / ResponseHeaderModifier filters, URL rewrites, and redirects
  - Gateway, HTTPRoute, and GRPCRoute status patches (Accepted, Programmed, ResolvedRefs, attachedRoutes, addresses)
  - TLS/TCP/UDP listeners are surfaced in each Gateway's `supportedKinds` status; TLSRoute/TCPRoute/UDPRoute resources are not watched or routed
- HAProxy annotation compatibility via `haproxytech.yaml` library
  - `haproxy.org/*` annotations (HAProxyTech ingress controller)
  - Backend config snippets, server options, load balancing algorithms
  - SSL passthrough, SSL redirect, CORS, basic auth
