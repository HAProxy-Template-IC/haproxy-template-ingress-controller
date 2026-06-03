# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added

- `spoaHub` values block and a conditionally-rendered SPOA hub sidecar (hub plus seven plugin libraries: mirror, coraza, external-auth, fingerprinting, maxmind, otel, sso-auth), auto-rendered when any plugin is enabled. A `spoa-hub` template library wires the HAProxy-side SPOE config (`backend spoa-hub`, frontend filter, `spoe.conf`), and the controller delivers the hub's runtime `config.toml` via the dataplane API so the file-watching hub reloads without a restart.
- `controller.validators` block and a validator sidecar (same image, `--validate-socket` mode) consulted by the admission webhook, with readiness/liveness probes guarding the socket. Auto-enabled with the spoa-hub sidecar.
- Gateway API support for `RequestMirror` (via `spoaHub.plugins.mirror`, auto-enabled with the gateway library), `SupportTLSRoute`, `SupportGatewayStaticAddresses` (per-Gateway LoadBalancer Service, multi-IP via MetalLB annotation), `SupportGatewayInfrastructurePropagation` (per-Gateway marker Service carrying `spec.infrastructure` metadata), `SupportListenerSet` routing, and GEP-91 frontend client-cert validation (verify mode, per-reason status, `InsecureFrontendValidationMode` condition).
- nginx-ingress and haproxy-ingress libraries wire the external-auth annotation family (`auth-url`, `auth-signin`, `auth-method`, `auth-headers-request`, `auth-headers-succeed`/`-fail`, client-mTLS `auth-tls-*`) to the SPOA hub's external-auth plugin, plus the Coraza WAF annotations (`/waf`, `modsecurity-snippet`) with per-resource opt-in and a `default-on` dispatch mode.
- `nginx-ingress` template library for `nginx.ingress.kubernetes.io/*` annotation compatibility (disabled by default), and a new `ingress-annotations-compat` scaffold library (level 2.5) providing shared Ingress vendor-annotation macros (SSL passthrough, CIDR access control).
- `https-bind-extra-*` extension point for Gateway HTTPS listeners on non-default ports; SSL-passthrough SNI matcher now supports wildcard listener hostnames.
- HTTPRoute `RequestRedirect` with scheme and port both unset now preserves the inbound listener port in `Location`; per-listener-port routing isolation via a `:<port>` host-map fallback.
- The chart-static `haptic-haproxy` LoadBalancer Service is now rendered via the controller's `spec.k8sResources` (SSA with an `OwnerReference`), folding non-default Gateway listener ports into the same Service.
- `haproxy.initialConfig` values key for the HAProxy bootstrap ConfigMap; `controller.config.dataplane.{configPublishInterval,reloadVerificationTimeout,syncTimeout}` tunables (commented out by default).
- `controller.config.extraContext.hardStopAfter` (default `10s`) emits `hard-stop-after` so old workers don't accumulate across reloads.
- Always-on local `peers localinstance` section (`localpeer local`, listening on a unix socket so it can never collide with a frontend/Gateway listener bind) so stick-tables that opt in survive reloads; `haproxy.org/rate-limit-*` counters now persist across config reloads (replicated old-worker→new-worker, per-pod, no cross-replica sync) instead of resetting and granting every client a fresh budget.
- `haproxy.dataplane.aclFormat` to override the dataplane access-log format (adds per-request μs timing); test profiles opt in.
- ClusterRole now grants `customresourcedefinitions` get/list/watch (typebootstrap schema resolution) and, when the gateway library is enabled, cluster-wide `services` write verbs; a namespace-scoped Role grants `services` for same-namespace per-Gateway Services. New `extraContext` keys `controllerNamespace` and `haproxyPodSelector`.
- HAProxy NetworkPolicy opens to all TCP ports when `haproxy.networkPolicy.allowExternal: true` so dynamic Gateway listener ports work; restrictive mode unchanged.

### Changed

- **BREAKING**: path matching order is now selected by `controller.config.routing.regexMatchOrder` (`default`/`last`), replacing the removed `path-regex-last` template library. Operators with `templateLibraries.pathRegexLast.enabled: true` must switch to `routing.regexMatchOrder: last`. See [ADR-0005](../../docs/adr/0005-path-matching-order-as-values-flag.md).
- **BREAKING**: `ingressClass.name` and `gatewayClass.name` default from `haproxy` to `haptic`. Operators replacing an incumbent controller set them back to `haproxy` (or update their manifests to `ingressClassName: haptic` / `gatewayClassName: haptic`).
- **BREAKING**: pod-spec scheduling, runtime, and metadata fields moved under namespaced `podSpec:` blocks on both Deployments. Container-, Deployment-, and chart-wide fields are unchanged. Rename the following keys in custom values files:

  | Previous | New |
  |---|---|
  | `imagePullSecrets` | `controller.podSpec.imagePullSecrets` |
  | `podAnnotations` | `controller.podSpec.podAnnotations` |
  | `podLabels` | `controller.podSpec.podLabels` |
  | `priorityClassName` | `controller.podSpec.priorityClassName` |
  | `topologySpreadConstraints` | `controller.podSpec.topologySpreadConstraints` |
  | `podSecurityContext` | `controller.podSpec.podSecurityContext` |
  | `nodeSelector` | `controller.podSpec.nodeSelector` |
  | `tolerations` | `controller.podSpec.tolerations` |
  | `affinity` | `controller.podSpec.affinity` |
  | `terminationGracePeriodSeconds` | `controller.podSpec.terminationGracePeriodSeconds` |
  | `dnsPolicy` | `controller.podSpec.dnsPolicy` |
  | `dnsConfig` | `controller.podSpec.dnsConfig` |
  | `hostAliases` | `controller.podSpec.hostAliases` |
  | `runtimeClassName` | `controller.podSpec.runtimeClassName` |
  | `haproxy.priorityClassName` | `haproxy.podSpec.priorityClassName` |
  | `haproxy.topologySpreadConstraints` | `haproxy.podSpec.topologySpreadConstraints` |
  | `haproxy.nodeSelector` | `haproxy.podSpec.nodeSelector` |
  | `haproxy.tolerations` | `haproxy.podSpec.tolerations` |
  | `haproxy.affinity` | `haproxy.podSpec.affinity` |
  | `haproxy.dnsPolicy` | `haproxy.podSpec.dnsPolicy` |
  | `haproxy.dnsConfig` | `haproxy.podSpec.dnsConfig` |
  | `haproxy.hostAliases` | `haproxy.podSpec.hostAliases` |
  | `haproxy.runtimeClassName` | `haproxy.podSpec.runtimeClassName` |
  | `haproxy.podAnnotations` | `haproxy.podSpec.podAnnotations` |
  | `haproxy.shareProcessNamespace` | `haproxy.podSpec.shareProcessNamespace` |
  | `haproxy.terminationGracePeriodSeconds` | `haproxy.podSpec.terminationGracePeriodSeconds` |
  | `haproxy.podSecurityContext` | `haproxy.podSpec.podSecurityContext` |

- `haproxy.ports.http`/`https` defaults shift from `8080`/`8443` to `80`/`443` so `dst_port` equals the Gateway listener port; operators who set them explicitly keep their override.
- Dataplane `minDeploymentInterval` default `0s` → `5s` (matches haproxytech `--sync-period`), throttling reload-inducing structural deploys; endpoint changes still apply instantly via the runtime fast path.
- Default per-watcher `debounceInterval` raised from 100ms to 2s; EndpointSlice keeps `"0"` for instant rolling-restart reaction. Removed the `reconciliationDebounceInterval` knob.
- **HAProxy `defaults` `timeout connect` lowered from `5000` to `100` (100 ms).** Backends are pod IPs over the CNI; 100 ms fails fast on a SYN to a just-terminated pod so `option redispatch` retries. Operators on slow networks restore `5000` via `extraContext.timeout_connect`.
- Bundled SPOA image updated to hub `v0.7.2` with the v3-consumer plugin releases (mirror `v0.5.0`, coraza `v0.5.0`, external-auth `v0.5.0`, fingerprinting `v0.3.0`, maxmind `v0.4.0`, otel `v0.5.0`, sso-auth `v0.3.0`); reload now drains in-flight work before swapping.
- The mirror `messages` list is sized dynamically per render (floored at `spoaHub.mirrorStaticMinSlots`, default 4), with per-dispatch timeout/retries derived from `timeout server`/`retries`; HTTPS mirror targets dispatch over TLS, and `worker_threads` / `pool_max_idle_per_host` params are tunable.
- `spoaHub.plugins.coraza.enabled` default broadened to also follow `haproxyIngress.enabled` (coraza wired on chart defaults); `coraza.directives` is now its own values field — operators upgrading must move directives out of `params`.
- SPOA hub config now delivered via a shared `emptyDir` the controller overwrites via the dataplane API (was a direct ConfigMap mount); the validator sidecar gets a memory-backed `/tmp` for Coraza's pre-compile probe.
- `extraDeploy` now accepts both list and dict formats.
- `haproxy.org/pod-maxconn` quantizes the pod count to the next power of 2 to avoid reload cascades on scaling.
- Compound resource names (derived from `haptic.fullname`) now truncate to the 63-char label limit; releases with very long names will see affected resources renamed on upgrade (run `helm diff upgrade` first).
- Removed the "TLS Certificate Expiry" Grafana dashboard panel and its `haptic_webhook_cert_expiry_timestamp_seconds` query — the controller no longer emits that metric (the webhook cert is now hot-reloaded; see controller CHANGELOG).

### Fixed

- `defaults` block now sets `option redispatch` and `base.yaml` filters out not-ready / terminating endpoints, eliminating the single-replica rolling-restart 503 windows.
- Reserved-slot server addresses are now config-driven across reloads (removed the HAProxy server-state-file machinery); placeholders moved to the unroutable `192.0.2.1:1` sentinel. See [ADR-0011](../../docs/adr/0011-no-haproxy-server-state-file.md).
- `haproxy.dataplane.validateConfig: false` now actually skips the dataplane's `haproxy -c` (the flag was misplaced under `haproxy:` instead of `haproxy.reload.*`), cutting raw-config push time ~130ms → ~18ms.
- Named Service-port references (`port.name`) in Ingress / HTTPRoute / GRPCRoute and SSL-passthrough backends now resolve to the correct numeric port via a shared `ResolveServicePort` helper that fails loud on unresolvable input (was silent 503s / empty backends).
- `haproxy-ingress.github.io/path-type` annotation now actually routes (fixed missing `BACKEND:` qualifier, missing macro call on the regex renderer, and missing wildcard-host normalization).
- Gateway TLS-cert registration treats unspecified `tls.mode` as the spec default `Terminate` (was silently skipping the listener); BackendTLSPolicy with no resolvable CA now returns 503 instead of downgrading to plaintext; TLSRoute status patches target `gateway.networking.k8s.io/v1`.
- Per-resource feature maps (`auth-*`, `coraza-app`, `waf`) are keyed by resource identity, fixing silent auth/WAF skips on regex, prefix, and wildcard-host paths; `modsecurity-snippet` rules are path-scoped per Ingress.
- HAProxy-Ingress / nginx-ingress `auth-headers-*` directives now emit into both the HTTP/1.1 and HTTP/2 cleartext frontends.
- Basic-auth snippets no longer panic when a referenced auth Secret is briefly absent from the render snapshot.
- PrometheusRule default alerts `HAProxyControllerHighQueueDepth` / `HAProxyControllerNoLeader` repointed to metrics the controller actually emits (the old names never fired).
- `networkPolicy.ingress.webhook.from` and `networkPolicy.egress.kubernetesApi` defaults switched to `ipBlock` `0.0.0.0/0` + `::/0`, so the host-network apiserver can reach the webhook on clusters enforcing NetworkPolicy (was silent 502 admission blocks).
- The validating-webhook configuration now sources `watchedResources` from the merged libraries, so library-declared resources are actually validated.
- `features-160-ssl-redirect-map` uses a per-snippet dedup so the map registration and the rule referencing it stay in lockstep under churn.
- `base.yaml` no longer references gateway-library state, restoring the Level-0 resource-agnostic architecture on clusters without Gateway API.
- SPOA mirror plugin no longer flakes the `HTTPRouteRequestMirror` / `MultipleMirrors` / `PercentageMirror` conformance tests (mirror message slots floored at a static minimum; reload drains in-flight dispatches).
- NOTES.txt no longer claims the Gateway API library is enabled when its CRDs are absent (severity downgraded to ℹ️).
- `haproxy.org/pod-maxconn` counts only Running + Ready HAProxy pods.

### Removed

- Dead `spoaHub.podSecurityContext` values key (never consumed — use `haproxy.podSpec.podSecurityContext`).

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
- `haproxy.dataplane.debugSocketPath` to enable Unix socket for runtime profiling of the Dataplane API sidecar
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
