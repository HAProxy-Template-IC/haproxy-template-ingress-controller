# Changelog

All notable changes to the HAProxy Template Ingress Controller will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For Helm chart changes, see [Chart CHANGELOG](./charts/haptic/CHANGELOG.md).

## [Unreleased]

### Fixed

- ConfigChangeHandler now triggers iteration restart on credentials-Secret and webhook-cert Secret rotation, not only on CRD changes. Previously the certloader / credentialsloader would publish `CertParsedEvent` / `CredentialsUpdatedEvent` into the void — components held references to whichever PEM bytes they parsed at startup, so rotating the underlying Secret silently left the running pod serving the stale cert (or stale credentials) until it was manually restarted. The handler now records each Secret's resourceVersion at iteration startup and signals reinitialization through the existing `configChangeCh` path the moment a watcher event reports a different version. The next iteration re-fetches the rotated Secret as part of `fetchAndValidateInitialConfig` and constructs a fresh webhook server / dataplane client with the new bytes — no per-component hot-rotation, just one reload path that already covers CRD changes too.
- Renderer now fails fast when the rendered HAProxy config references map files the renderer did not register. Previously, a chart-side inconsistency could ship a config that referenced a missing map; the orchestrator's post-config-delete phase would then delete the file as "unreferenced" and every subsequent reload would fail until the offending Ingress was removed.
- Render-time view of the watched-resource stores is now pinned per-render across `List()`, `Fetch()`, and `GetSingle()`. The template-facing `StoreWrapper` snapshots the underlying store on first access and serves all subsequent reads — including keyed lookups — from an in-memory composite-key index built from the configured `IndexBy`. Previously, a live informer Add landing between two snippet executions could let one snippet (e.g. the auth `global-top-*` userlist emitter) iterate a different ingress set than another (the `backend-directives-*` `http_auth(...)` emitter) within a single render, producing an HAProxy config that referenced a userlist no snippet emitted — admission then denied the offending ingress with `unable to find userlist '...'`. Templates didn't need any change; the cross-snippet coherence guarantee is now structural.

### Added

- `/healthz` now reflects the configured pluggable-validator sockets. When `spec.validators` is non-empty, the probe stat()'s every socket and reports a single `pluggable-validators` component entry — `Healthy: true` when every socket is reachable, otherwise `Healthy: false` with a semicolon-joined `<name>: <reason>` failure list. Empty `validators` keeps `/healthz` output unchanged so operators not using the feature see no behaviour change. The check is sub-millisecond on the happy path so it stays cheap on every Kubernetes liveness/readiness probe interval.
- New `spec.validators` field on `HAProxyTemplateConfig` declares pluggable validator sidecars consulted by the admission webhook. Each entry names a validator (RFC 1123 label), points at a Unix domain socket inside the controller pod, and lists file-glob patterns the controller matches against rendered file paths to decide which files to send to that validator (controller-side routing — the validator program itself is opaque to the controller). Optional `timeoutMs` (per-call deadline) and `maxConnections` (adaptive connection-pool ceiling) round out the per-entry shape. When configured, the controller forwards each glob-matched file over a length-prefixed JSON wire protocol with persistent keep-alive connections; `(validator, file)` round-trips run in parallel. The validator's three-result response (`valid` / `warning` / `error`) maps directly to admission outcomes — warnings populate `AdmissionResponse.Warnings` and let admission proceed; errors deny with line-numbered diagnostics. New `pkg/controller/pluggablevalidator` package implements the wire-protocol primitives, a per-(validator, file-path, content-hash) LRU cache, an adaptive connection pool per validator (starts small, grows on contention, shrinks on idleness), and a `Manager` orchestrating parallel dispatch. The chart-side sidecar wiring lands in a follow-up MR. See `docs/controller/docs/operations/pluggable-validators.md` for end-user documentation and `docs/development/validator-protocol.md` for the wire-protocol spec.
- Make the reconciliation refractory window (`spec.controller.reconciliationDebounceInterval`), the HAProxyCfg republish throttle (`spec.dataplane.configPublishInterval`), the per-sync reload-verification timeout (`spec.dataplane.reloadVerificationTimeout`), the overall sync timeout (`spec.dataplane.syncTimeout`), and the HTTP 409 retry budget (`spec.dataplane.syncMaxRetries`) tunable via `HAProxyTemplateConfig`. Together with the existing `minDeploymentInterval`, `driftPreventionInterval`, `deploymentTimeout`, leader-election durations, and per-watcher `debounceInterval`, every throttle on the path between a Kubernetes event and a HAProxy push can now be tuned without rebuilding the controller.
- Per-resource `debounceInterval` override on `spec.watchedResources.*`. Defaults to the global 5s window when empty or unparseable; useful to react faster on resources where 5s is too slow (e.g. HTTPRoute changes during canary rollouts) or to throttle further for very high-churn resources on large clusters.
- New multi-arch `spoa-hub` container image at `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<version>` bundling [`haproxy-spoa-hub`](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) plus the six plugin shared libraries (coraza, external-auth, fingerprinting, maxmind, otel, sso-auth). Cosign-signed by digest with a CycloneDX SBOM attestation. See `docs/controller/docs/operations/spoa-hub.md` for the bundled component list.
- New `versions-spoa.env` pinning the upstream hub + plugin versions, with renovate-managed bumps (grouped into one MR per run).
- New `make spoa-hub-image` for local single-arch builds; `make spoa-prep` runs the upstream sha256 + cosign verification of plugin `.so` files into `plugins/`.
- New `spoa-hub` template library wires HAProxy to the bundled SPOA hub sidecar — adds the `backend spoa-hub` block, the `filter spoe engine spoa-hub` directive in frontends, and registers `spoe.conf` as a general file. Auto-enabled when any plugin under `spoaHub.plugins.*` is on, or `spoaHub.enabled: true` is set explicitly. Plugin libraries plug into the rendered SPOE config via the `spoe-agents-*`, `spoe-messages-*`, and `frontend-spoe-filters-*` snippet globs. Configuration flows through the existing `templatingSettings.extraContext` mechanism — the controller stays generic.
- External-auth flow wired up end to end: nginx-ingress library reads `nginx.ingress.kubernetes.io/auth-url` annotations into a new `auth-url.map`, the spoa-hub library emits a per-frontend lookup + `send-spoe-group` trigger + `deny_status 401` rule, and the spoe.conf agent uses `groups` with explicit triggering (canonical pattern from the haproxy-spoa-hub external-auth plugin). The plugin sets `txn.hub.external_auth.allowed` (bool), which the deny rule checks. Per-message dispatch in `spoe-conf-content` lets one MR add a real body for one message without breaking other plugins still in stub form.

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
