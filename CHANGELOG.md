# Changelog

All notable changes to HAPTIC — the controller and its Helm chart — are
documented in this file. Controller changes are listed first; chart changes
(values, templates, chart defaults) follow under each release's "Helm chart"
subsection.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Changes since 0.1.0, including the 0.2.0 alpha series. Follow the
[upgrade guide](./docs/site/docs/upgrading-to-0.2.md) to migrate existing
installations and custom templates.

### Added

- HAProxy 3.4 support, including runtime addition and removal of eligible backends without a reload.
- Typed watched-resource access and collection pipelines for custom templates, reusable `HAProxyTemplateLibrary` resources, and template-defined Kubernetes resources.
- Runtime discovery of watched API versions and schemas, including automatic adaptation when watched CRDs are installed, upgraded, or removed.
- Pluggable output validators and enforcement of embedded validation tests whenever configuration loads or changes.
- `haptic preflight` validates chart values before deployment, `haptic diff` predicts reloads, and `haptic agent state` inspects a pod's deployed configuration.
- A browser playground and editable documentation examples for trying templates without a cluster.
- A portable agent skill for Scriggo customization, resource watches, and validation, with installation instructions and versioned downloads.

### Changed

- **BREAKING:** The `haptic` binary and per-pod HAPTIC agent replace `haptic-controller` and the HAProxy Data Plane API. Eligible map, certificate, and server updates apply at runtime; rejected reloads restore the last working files.
- **BREAKING:** Custom templates use `currentConfig.ServerIndex`, validation fixtures use `currentServers`, and `statusPatch` takes the resource object. Use `toJSON` for composite values instead of implicit string conversion.
- Incremental rendering, smaller resource caches, and warm follower replicas reduce repeated work, memory use, and leadership-transition delays.
- Controller metrics now cover agent operations and fleet convergence; update dashboards using the [metric migration table](./docs/site/docs/operations/monitoring.md#where-the-old-metrics-went).

### Security

- Admission requests have payload limits; HTTP redirects and diagnostics no longer expose credentials or auxiliary-file contents.

### Known issues

The intermittent output mismatch in [#213](https://gitlab.com/haproxy-haptic/haptic/-/issues/213)
has no confirmed root cause. It was closed as not reproducible; the controller
still rejects mismatched output before publication.

### Helm chart

#### Added

- Gateway API TLSRoute, TCPRoute, ListenerSet, backend TLS, frontend client-certificate authentication, and request mirroring.
- Native `haproxy-haptic.org/*` annotations for API-key, JWT, and HMAC authentication, shared rate limiting, Varnish caching, opt-in response compression, request-schema validation, and reusable WAF policies.
- NGINX Ingress annotation compatibility and expanded HAProxy Ingress annotation support, including external authentication and rate limiting.
- Governance rules for enforcing administrator-defined policies on watched resources.
- Vector request metrics and optional distributed tracing, with route and backend identity in access logs and spans.
- Pre-rollout configuration validation and automatic CRD upgrades through Helm hooks; webhook certificates no longer require cert-manager.

#### Changed

- **BREAKING:** Kubernetes 1.33 or newer is required. Native sidecars and connection draining keep HAProxy's dependencies available during shutdown.
- **BREAKING:** Controller workload and pod settings move under `controller.*` and `*.podSpec.*`; agent settings replace `haproxy.dataplane.*`, and certificates move to `defaultSSLCertificate`. See the upgrade guide for removed values and replacements.
- **BREAKING:** IngressClass and GatewayClass names default to `haptic` instead of `haproxy`; vendor annotation libraries require explicit enablement.
- **BREAKING:** Access logs use JSON, and rendered manifests contain separate template-library resources.
- HAProxy defaults to 3.4, HTTP/HTTPS pod ports to 80/443, and backend connection timeout to 100 ms. Ingress HTTPS is enabled by default; request replay defaults to idempotent methods.
- Route policies use shared rules and maps, and servers use pod names instead of reserved slots, allowing eligible changes without reloads. Hash-based balancing defaults to consistent hashing.
- Controller memory requests and limits increase to 1 GiB; pre-rollout validation also has a 1 GiB limit.

#### Fixed

- Named Service ports resolve correctly, and exact Ingress paths preserve trailing slashes.

#### Security

- Route and annotation values are validated to prevent HAProxy configuration injection; the default NetworkPolicy restricts access to management ports.

## [0.1.0] - 2026-03-09

### Added

- **Template-driven HAProxy configuration**: Generate HAProxy configs using Scriggo templates (Go-based, Jinja2-like syntax) with full access to Kubernetes resources, built-in utility functions, and modular template snippets
- **Embedded validation tests**: Declarative test fixtures and assertions for testing HAProxy configurations within template libraries; run via `haptic-controller validate --test <name>`
- **Dry-run validation webhook**: Admission webhook that intercepts CREATE/UPDATE on opted-in watched resources (Ingress, HTTPRoute, GRPCRoute by default), renders templates with the proposed object overlaid on the live store, and rejects the write if rendering or HAProxy validation fails
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

### Helm chart

#### Added

- Initial Helm chart deploying the controller and HAProxy pods (2 replicas by default)
- Separate controller Service (ClusterIP for operational endpoints) and HAProxy Service (configurable LoadBalancer/ClusterIP)
- Default NetworkPolicy for HAProxy instances
- Leader election support with configurable replica count
- Default SSL certificate configuration via `controller.defaultSSLCertificate`
- Modular template library system with composable libraries merged at Helm render time (enable/disable via `controller.templateLibraries.<name>.enabled`):
    - `base.yaml`: Core HAProxy template structure with extension points
    - `ingress.yaml`: Kubernetes Ingress support (path types: Exact, Prefix, ImplementationSpecific; TLS termination; default backend)
    - `gateway.yaml`: Gateway API support — HTTPRoute and GRPCRoute are watched and routed; traffic splitting, request/response header modification, URL rewrites, and Gateway/Route status patches are emitted. TLS/TCP/UDP listeners are reflected in each Gateway's `supportedKinds` status but TLSRoute/TCPRoute/UDPRoute resources are not watched or routed
    - `haproxytech.yaml`: `haproxy.org/*` annotation compatibility (backend config snippets, SSL passthrough, CORS, basic auth)
    - `ssl.yaml`: TLS/SSL features
    - `haproxy-ingress.yaml`: 56 `haproxy-ingress.github.io/*` annotation compatibility (enabled by default)
- Gateway API status reporting: Gateway conditions (Accepted, Programmed), listener status, HTTPRoute/GRPCRoute parent status with Accepted and ResolvedRefs conditions
- Ingress status reporting: LoadBalancer addresses propagated to Ingress `.status.loadBalancer`
- HAProxy built-in Prometheus exporter enabled by default on the status frontend (`/metrics` on port 8404)
- Grafana dashboard annotations for leader transitions and controller pod starts
- Auto-generated Dataplane API credentials stored in a Secret (deterministic 32-char SHA256 of release-name + namespace; preserved across upgrades from the existing Secret)
- `haproxy.sysctls` for setting kernel parameters on HAProxy pods via pod-level securityContext
- `haproxy.podAnnotations` for custom pod annotations on HAProxy pods (supports Helm template expressions)
- `haproxy.shareProcessNamespace` to enable process namespace sharing between containers (required for signal-based sidecar reload, e.g., SPIFFE/SPIRE mTLS agents)
- `haproxy.shmStats.enabled` to persist stats counters across HAProxy reloads via shared memory (requires HAProxy 3.3+); automatically provisions `/dev/shm` emptyDir volume with auto-calculated size
- `haproxy.nbthread` to control HAProxy thread count (auto-calculated from CPU requests by default)
- `haproxy.dataplane.validateConfig` to control server-side config validation
- `haproxy.dataplane.debugSocketPath` to enable Unix socket for runtime profiling of the Dataplane API sidecar
- `controller.config.dataplane.maxParallel` to limit concurrent Dataplane API operations
- `controller.statusPatches.enabled` to disable status patch writes during migration from another ingress controller
- `extraDeploy` for deploying arbitrary Kubernetes resources alongside the chart (supports Helm templating)
- `extraEnv`, `haproxy.extraEnv`, `haproxy.dataplane.extraEnv` for custom environment variables on all containers
- `global-settings-*`, `defaults-settings-*`, and `frontend-extra-*` extension points for customizing HAProxy global/defaults sections and early frontend directives via template snippets
- `status-patches-*` and `status-extra-*` extension points for custom status and Prometheus endpoint configuration
- `template` post-processor type for declarative output transformations in `postProcessing`
- `guid` directives on all frontends, backends, and servers for stable object identification

#### Changed

- Dataplane API credentials consolidated into `credentials.dataplane` section; auto-generated if not provided
- Basic auth userlists are named `auth_<secretNs>_<secretName>` and deduplicated per Secret; each Ingress references its userlist via `http_auth()`. Differs from the official HAProxy Ingress Controller's per-Ingress `{namespace}-{ingressName}` naming so multiple Ingresses sharing the same Secret produce a single userlist (significant speedup for bcrypt hashes)
- Production-ready default resource requests and limits: controller (100m CPU / 512Mi memory), HAProxy (250m CPU / 1Gi memory), dataplane sidecar (50m CPU / 256Mi memory)
- `sidecars`, `extraVolumes`, `extraVolumeMounts` and their `haproxy.*` counterparts support Helm template expressions

#### Removed

- `image.appendHaproxyVersion` value (HAProxy version suffix is now always included in controller image tag)
- `haproxy.dataplane.credentials` section (use `credentials.dataplane` instead)
