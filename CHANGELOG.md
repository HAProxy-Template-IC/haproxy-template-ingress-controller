# Changelog

All notable changes to the HAProxy Template Ingress Controller will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For Helm chart changes, see [Chart CHANGELOG](./charts/haptic/CHANGELOG.md).

## [Unreleased]

### Added

- New multi-arch `spoa-hub` container image at `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<version>` bundling [`haproxy-spoa-hub`](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) plus the six plugin shared libraries (coraza, external-auth, fingerprinting, maxmind, otel, sso-auth). Cosign-signed by digest with a CycloneDX SBOM attestation. See `docs/controller/docs/operations/spoa-hub.md` for the bundled component list.
- New `versions-spoa.env` pinning the upstream hub + plugin versions, with renovate-managed bumps (grouped into one MR per run).
- New `make spoa-hub-image` for local single-arch builds; `make spoa-prep` runs the upstream sha256 + cosign verification of plugin `.so` files into `plugins/`.
- New `spoa-hub` template library wires HAProxy to the bundled SPOA hub sidecar — adds the `backend spoa-hub` block, the `filter spoe engine spoa-hub` directive in frontends, and registers `spoe.conf` as a general file. Auto-enabled when any plugin under `spoaHub.plugins.*` is on, or `spoaHub.enabled: true` is set explicitly. Plugin libraries plug into the rendered SPOE config via the `spoe-agents-*`, `spoe-messages-*`, and `frontend-spoe-filters-*` snippet globs. Configuration flows through the existing `templatingSettings.extraContext` mechanism — the controller stays generic.

### Removed

- Drop `namespaceSelector` from `watchedResources` entries. The field was never wired up; setting it had no effect. Configs that included it must remove it (the API server may now reject unknown fields). For namespace scoping, filter at the template level against a watched `namespaces` resource, use `labelSelector:` on the resource, or run separate controller instances per scope.

## [0.1.0] - 2026-03-09

### Added

- **Template-driven HAProxy configuration**: Generate HAProxy configs using Scriggo templates (Go-based, Jinja2-like syntax) with full access to Kubernetes resources, built-in utility functions, and modular template snippets
- **Embedded validation tests**: Declarative test fixtures and assertions for testing HAProxy configurations within template libraries; run via `haptic-controller validate --test <name>`
- **Dry-run validation webhook**: Admission webhook for opted-in watched resources (Ingress, HTTPRoute, GRPCRoute by default) that re-renders the template set with the proposed change and rejects requests that would produce an invalid HAProxy configuration
- **Multi-architecture container images**: `linux/amd64`, `linux/arm64`, `linux/arm/v7`
- **HAProxy version support**: 3.0, 3.1, 3.2, 3.3 — version-specific images tagged accordingly
- **Supply chain security**: Container images, binaries, and Helm charts signed with Cosign (keyless OIDC); SBOM attestations in SPDX format
- **Prometheus metrics**: Reconciliation timing, template rendering duration, validation results, and Kubernetes API latencies
- **Leader election for high availability**: Multiple controller replicas with automatic leader election; hot-standby replicas continue watching and validating; configurable failover timing
- **Stall detection**: Components detect when blocked and report unhealthy via `/healthz`, enabling automatic pod restart via Kubernetes liveness probes
- **Configurable deployment timeout**: `deploymentTimeout` in dataplane config (default: 30s) to recover from stuck deployments
- **Server slot preservation**: Preserve HAProxy server slots during rolling deployments to enable zero-reload runtime API updates via `currentConfig` template context
- **HAProxy Ingress annotation compatibility**: 56 `haproxy-ingress.github.io/*` annotations via the haproxy-ingress template library
- **Dataplane API concurrency limiting**: `maxParallel` config option to limit concurrent API operations, preventing timeouts for large configurations
- **CRD content compression**: HAProxyCfg content compressed with zstd when exceeding `configPublishing.compressionThreshold` (default 1 MiB), reducing etcd storage
- **HAProxyGeneralFile CRD**: Publish general files (error pages, etc.) as Kubernetes custom resources with compression support
- **HAProxyCRTListFile CRD**: Publish crt-list files as Kubernetes custom resources with compression support
- **`semver_gte` template filter**: Version comparison for gating features on HAProxy version (e.g., `semver_gte(haproxyVersion, "3.3")`)
- **Template-driven status patches**: Templates can register status patches for any Kubernetes resource via `statusPatch()` function, with outcome-keyed variants (`rendered`, `deployed`, `renderFailed`, `deployFailed`) applied automatically based on pipeline phase
- **Backend diff field diagnostics**: Reconciliation log now includes which BackendBase fields caused backend updates, aiding diagnosis of false diffs from parser round-trip asymmetries
- **Status patch helper functions**: `condition()`, `transitionTime()`, and `toJSON()` template functions for building Kubernetes status conditions with stable transition timestamps

### Changed

- **Reconciliation triggering**: Leading-edge triggering with a 5s refractory period; no latency for isolated changes, bursts during that window are batched into a single reconciliation
- **Parallel Dataplane API operations**: Operations execute in parallel within each priority group, reducing sync time for large configurations
- **Balance directive**: `balance roundrobin` moved to `defaults` section to prevent silent behavior change when upgrading to HAProxy 3.3 (which changed the default balance algorithm from `roundrobin` to `random`)
- **Go runtime 1.26.1**: Green Tea GC replaces manual GOGC tuning
