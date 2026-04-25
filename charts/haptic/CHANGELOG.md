# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added

- nginx-ingress template library for `nginx.ingress.kubernetes.io/*` annotation compatibility (disabled by default)

### Fixed

- `haproxy.org/pod-maxconn` now only counts Running and Ready HAProxy pods (previously counted all pods including Pending, SysctlForbidden, CrashLoopBackOff)

### Changed

- **BREAKING**: `ingressClass.name` and `gatewayClass.name` default from `haproxy` to `haptic`. Avoids conflicts with other HAProxy-based ingress controllers during side-by-side migration. Existing users replacing an incumbent controller should set `ingressClass.name: haproxy` (and/or `gatewayClass.name: haproxy`) explicitly in their values, or update their Ingress / Gateway manifests to `ingressClassName: haptic` / `gatewayClassName: haptic`.
- `extraDeploy` now accepts both list and dict formats (dict enables composing across multiple values files)
- `haproxy.org/pod-maxconn` quantizes the pod count to the next power of 2 to avoid HAProxy reload cascades on scaling

## [0.1.0] - 2026-03-09

### Added

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
- `haproxy.dataplane.debugSocket` to enable Unix socket for runtime profiling of the Dataplane API sidecar
- `controller.config.dataplane.maxParallel` to limit concurrent Dataplane API operations
- `controller.statusPatches.enabled` to disable status patch writes during migration from another ingress controller
- `extraDeploy` for deploying arbitrary Kubernetes resources alongside the chart (supports Helm templating)
- `extraEnv`, `haproxy.extraEnv`, `haproxy.dataplane.extraEnv` for custom environment variables on all containers
- `global-settings-*`, `defaults-settings-*`, and `frontend-extra-*` extension points for customizing HAProxy global/defaults sections and early frontend directives via template snippets
- `status-patches-*` and `status-extra-*` extension points for custom status and Prometheus endpoint configuration
- `template` post-processor type for declarative output transformations in `postProcessing`
- `guid` directives on all frontends, backends, and servers for stable object identification

### Changed

- Dataplane API credentials consolidated into `credentials.dataplane` section; auto-generated if not provided
- Basic auth userlists are named `auth_<secretNs>_<secretName>` and deduplicated per Secret; each Ingress references its userlist via `http_auth()`. Differs from the official HAProxy Ingress Controller's per-Ingress `{namespace}-{ingressName}` naming so multiple Ingresses sharing the same Secret produce a single userlist (significant speedup for bcrypt hashes)
- Production-ready default resource requests and limits: controller (100m CPU / 512Mi memory), HAProxy (250m CPU / 1Gi memory), dataplane sidecar (50m CPU / 256Mi memory)
- `sidecars`, `extraVolumes`, `extraVolumeMounts` and their `haproxy.*` counterparts support Helm template expressions

### Removed

- `image.appendHaproxyVersion` value (HAProxy version suffix is now always included in controller image tag)
- `haproxy.dataplane.credentials` section (use `credentials.dataplane` instead)
