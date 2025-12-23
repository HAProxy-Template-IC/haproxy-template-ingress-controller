# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

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
