# Changelog

All notable changes to the Haptic Helm Chart will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For controller changes, see [Controller CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added

- `controller.validators` values block plus a conditionally-rendered validator sidecar in the controller pod. The sidecar runs the same `haproxy-spoa-hub` image as the HAProxy-side spoa-hub but in `--validate-socket` mode, listening on a Unix socket on a shared `emptyDir` (default `/var/run/haptic-validators/spoa-hub.sock`) so the admission webhook can call into plugin `validate()` overrides at admission time. Auto-enabled when the spoa-hub sidecar is on (i.e. any plugin enabled); set `controller.validators.enabled` to `true` / `false` to force on/off. Loads the same `config.toml` ConfigMap as the HAProxy-side spoa-hub so validator and runtime see identical plugin behavior. `controller.validators.entries` is appended to `spec.validators` of the rendered HAProxyTemplateConfig (de-duplicated by `name` with operator-supplied entries winning); empty by default — operators wire globs based on which template snippets produce validate-able file content.
- `haproxy.initialConfig` values key. Renders the HAProxy bootstrap ConfigMap (`<release>-haptic-haproxy-config`) that runs before the controller's first Dataplane API push. The default in `values.yaml` matches the previously hardcoded bootstrap byte-for-byte; operators can copy and edit it. Helm's `tpl` is applied so chart helpers (`haptic.haproxy.nbthread`) and `.Values` references continue to work. Edits roll HAProxy pods via the existing `bootstrapConfigChecksum`. Replaces the same key dropped in the previous cleanup, which had not been wired to the rendered ConfigMap.
- Five new tunables surfaced under `controller.config.controller.*` and `controller.config.dataplane.*` so the chart exposes every event-to-deployment throttle knob without requiring chart code changes: `reconciliationDebounceInterval`, `configPublishInterval`, `reloadVerificationTimeout`, `syncTimeout`, and `syncMaxRetries`. All commented out by default; existing deployments are unaffected.
- New `annotation-compat` template library at hierarchy level 2.5 (between resource libraries and the vendor annotation libraries). Provides parameterized macros that the protocol libraries call into instead of duplicating the same patterns. Currently houses two macros: `BuildAnnotationSSLPassthrough` (replaces three near-identical SSL passthrough scanners — `haproxytech`, `haproxy-ingress`, `nginx-ingress` — that all looped over Ingresses, checked a vendor-specific annotation, and appended host entries to `gf["sslPassthroughBackends"]`) and `EmitAnnotationAccessControl` (replaces three near-identical CIDR allow/deny ACL emitters that read source-range annotations and emitted `acl ... src` + `http-request deny` rules). Backend names, ACL names, and rendered HAProxy output are byte-identical to the previous per-library implementations. Cookie-affinity, backend-timeout, and request/response-header patterns were considered but not extracted: their shapes diverge enough across the three libraries that a shared abstraction would obscure rather than concentrate the behaviour. See `docs/adr/0003-annotation-compat-scaffold-level-2-5.md`.
- `spoaHub` values block plus a conditionally-rendered SPOA hub sidecar in the HAProxy pod. The sidecar runs `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub` and bundles six plugin shared libraries (coraza, external-auth, fingerprinting, maxmind, otel, sso-auth). It is absent by default, auto-rendered when any plugin is enabled, and exposes a Unix-domain socket at `/run/spoa/hub.sock` shared with the HAProxy container. Per-plugin `params:` values are a free-form TOML string blob so chart upgrades don't require values-schema churn when upstream plugins evolve.
- New `spoa-hub` template library generates the HAProxy-side SPOE wiring — the `backend spoa-hub` block, the `filter spoe engine` directive in frontends, and the `spoe.conf` general file. Auto-enabled together with the sidecar; opt-in via `controller.templateLibraries.spoaHub.enabled: true` to load it standalone. Configuration is delivered via `templatingSettings.extraContext.spoaHub` (chart-populated from `spoaHub.haproxy.*` values), keeping the controller generic.
- nginx-ingress library wires the `nginx.ingress.kubernetes.io/auth-url` annotation to the SPOA hub's `external-auth` plugin: each ingress with the annotation gets a host+path entry in the new `auth-url.map`, and the spoa-hub library emits a per-frontend `set-var(txn.auth_url)` lookup, an `http-request send-spoe-group spoa-hub check-auth-group` trigger, and a `deny_status 401` rule fired when the plugin returns `allowed=false`. The `spoaHub.plugins.external-auth.enabled` default now auto-enables when `controller.templateLibraries.nginxIngress.enabled` is on, so operators don't need to flip both switches.
- haproxy-ingress library wires the `haproxy-ingress.github.io/auth-url` annotation through the same `auth-url.map` and SPOE wiring as nginx-ingress. The auto-enable expression on `spoaHub.plugins.external-auth.enabled` deliberately does NOT OR in `controller.templateLibraries.haproxyIngress.enabled`: haproxy-ingress is on by default for many operators who don't use external-auth, and auto-enabling would deploy the spoa-hub sidecar (~50 MB) chart-wide. Operators using `haproxy-ingress.github.io/auth-url` should set `spoaHub.plugins.external-auth.enabled=true` explicitly. The haproxytech library (`haproxy.org/*`) has no external-auth annotation in its public schema and is therefore not wired.
- Both ingress libraries also wire the `auth-signin` annotation (`nginx.ingress.kubernetes.io/auth-signin` and `haproxy-ingress.github.io/auth-signin`). When set, an auth failure produces a 302 redirect to the configured sign-in URL instead of a 401 — the standard pattern for OIDC / SAML browser flows. Per-ingress entries land in the new `auth-signin.map`; the spoa-hub library's frontend wiring picks the URL up and emits an `http-request redirect location` rule that fires before the deny rule.
- Both ingress libraries wire the `auth-method` annotation (`nginx.ingress.kubernetes.io/auth-method` and `haproxy-ingress.github.io/auth-method`). When set, the auth subrequest goes out with the configured HTTP verb (allowed: GET / HEAD / POST / PUT / PATCH / DELETE / OPTIONS); without the annotation the plugin falls back to its plugin-level method config. Implementation mirrors the auth-url / auth-signin map pattern; the SPOE check-auth message body now threads `method=var(txn.auth_method)` so the per-route value reaches the plugin. Body-having methods (POST/PUT/PATCH) go out with an empty body — the plugin does not forward the original request payload. Requires external-auth plugin v0.3.0+.
- haproxy-ingress library wires the `haproxy-ingress.github.io/auth-headers-request` annotation: the comma-separated header allowlist lands in the new `auth-forward-headers.map` and surfaces in the SPOE check-auth message's `forward_headers` arg. Plugin v0.3.0+ replaces its plugin-level forward_headers list with the per-route value when the arg is non-empty. nginx-ingress's `auth-snippet` is freeform HAProxy config injection and intentionally not wired. Note: the SPOE message body's fixed `hdr_<name>` arg list still constrains which headers can be forwarded — operators wanting custom headers must extend `spoe-message-check-auth-body` chart-side; this annotation only narrows that captured set per-route.
- Both ingress libraries wire the `auth-headers-succeed` annotations (`nginx.ingress.kubernetes.io/auth-response-headers` and `haproxy-ingress.github.io/auth-headers-succeed`); haproxy-ingress additionally wires `auth-headers-fail`. The per-ingress comma-list of header names lands in the new `auth-extract-headers.map` (as the union of pass + fail headers per ingress) and surfaces in the SPOE check-auth message's `extract_headers` arg. The plugin v0.3.0+ extracts those headers from every reply path (2xx, 3xx, 4xx, 5xx) and publishes each as a transaction variable `txn.hub.external_auth.<name>` (lowercased, dashes → underscores). Two new `frontend-spoe-set-pass-headers-*` and `frontend-spoe-set-fail-headers-*` extension points emit one HAProxy directive per unique header: `http-request set-header` for pass (gated on `allowed -m bool`, fires on backend forwarding) and `http-response set-header` for fail (gated on `var(txn.auth_url) -m found` and `!allowed`, fires on the deny response so e.g. `WWW-Authenticate` reaches the client to drive a Bearer challenge). Per-route gating happens implicitly via the plugin's per-route `extract_headers` SPOE arg — routes that didn't ask for a header have its txn var unset, so the `var ... -m found` gate skips them.
- haproxy-ingress library auto-extends the SPOE check-auth message body's `hdr_<name>=req.hdr(<Name>)` capture list from the union of `auth-headers-request` annotations across all ingresses. The chart was previously hardcoded to six standard headers (Authorization, Cookie, X-Forwarded-{For,Proto,Host,Uri}); operators wanting to forward custom headers like `X-Tenant-ID` had to override `spoe-message-check-auth-body` themselves. Now the annotation alone is the source of truth: list the header on the ingress, the chart captures it via SPOE, and the plugin's `forward_headers` arg narrows per-route as before. New `spoe-message-check-auth-extra-args-*` extension point in the spoa-hub library; haproxy-ingress contributes the implementation. Headers in the hardcoded six are skipped (no duplicate SPOE arg); cross-ingress duplicates collapse to one capture; RFC 7230 token validation via the existing `util-auth-validate-header-name` macro applies.
- Both ingress libraries wire the client-mTLS annotation family for incoming client-cert verification: `nginx.ingress.kubernetes.io/auth-tls-secret` + `auth-tls-verify-client` + `auth-tls-error-page` + `auth-tls-pass-certificate-to-upstream`, and the haproxy-ingress equivalents `haproxy-ingress.github.io/auth-tls-secret` + `auth-tls-verify-client` + `auth-tls-error-page` + `auth-tls-cert-header`. The chart looks up the referenced `kubernetes.io/tls` Secret, writes its `ca.crt` field to `ssl/<ns>-<secret>-client-ca.pem` via the file registry, and registers per-host config in a new shared-state map `globalFeatures.clientCertVerifyHosts`. The ssl.yaml `features-150-ssl-crtlist` snippet now groups each TLS cert's SNIs by their (ca-file, verify-mode) tuple and emits one crt-list line per group with `[ca-file <path> verify <mode>]` — the same cert can appear multiple times in the crt-list with different per-line options, which HAProxy supports. The error-page and cert-passthrough annotations attach `http-request redirect` and `http-request set-header` directives gated on `ssl_c_verify` / `ssl_fc_has_crt`. Verify modes `on`/`optional`/`optional_no_ca`/`off` map to HAProxy's `verify required`/`optional`/`optional` (no distinct mode for "verify but accept invalid")/no-op respectively. `nginx.ingress.kubernetes.io/auth-tls-verify-depth` and `haproxy-ingress.github.io/auth-tls-strict` are intentionally not wired separately — HAProxy's crt-list has no per-line verify-depth, and `auth-tls-strict` overlaps with `auth-tls-verify-client: optional`.
- nginx-ingress template library for `nginx.ingress.kubernetes.io/*` annotation compatibility (disabled by default)

### Changed

- **BREAKING**: Path matching order is now selected by `controller.config.routing.regexMatchOrder` (`default` or `last`), not by enabling the `path-regex-last` template library. The library has been removed; its single override snippet now lives in `base.yaml` as a swappable variant of `frontend-routing-logic`. Operators with `controller.templateLibraries.pathRegexLast.enabled: true` must replace it with `controller.config.routing.regexMatchOrder: last`. Rationale: the library scaffold (top-level merge entry, dedicated docs page, hierarchy slot) exceeded the behaviour it carried — one snippet override differing in four lines. See `docs/adr/0005-path-matching-order-as-values-flag.md`.
- **BREAKING**: `ingressClass.name` and `gatewayClass.name` default from `haproxy` to `haptic`. Avoids conflicts with other HAProxy-based ingress controllers during side-by-side migration. Existing users replacing an incumbent controller should set `ingressClass.name: haproxy` (and/or `gatewayClass.name: haproxy`) explicitly in their values, or update their Ingress / Gateway manifests to `ingressClassName: haptic` / `gatewayClassName: haptic`.
- `extraDeploy` now accepts both list and dict formats (dict enables composing across multiple values files)
- `haproxy.org/pod-maxconn` quantizes the pod count to the next power of 2 to avoid HAProxy reload cascades on scaling
- Compound resource names derived from `haptic.fullname` (the HAProxy Deployment / Service / ConfigMap, the dataplane credentials Secret, the webhook Service, the SPOA-hub ConfigMap) are now produced by named helpers that apply `| trunc 63 | trimSuffix "-"`. Previously these names were inlined as `{{ include "haptic.fullname" . }}-<suffix>` without truncation, which exceeded Kubernetes' 63-char DNS-1035 label limit for Services on releases with long fullnames (and silently violated the same convention for the other resources). For typical release names the rendered names are byte-identical; releases with `haptic.fullname` longer than ~51 characters will see the affected resources renamed on upgrade — run `helm diff upgrade` first and clean up the orphaned objects manually.
- **BREAKING**: Pod-spec scheduling, runtime, and metadata fields have moved under namespaced `podSpec:` blocks for consistency between the controller and HAProxy Deployments. The chart now renders the universally-shared fields via a single `_pod-spec.tpl` helper. Operators must rename the following keys in their custom values files:

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

  Container-level fields (`securityContext`, `resources`, probes, `lifecycle`), Deployment-level fields (`updateStrategy`, `replicaCount`, `autoscaling`, `podDisruptionBudget`, `revisionHistoryLimit`, `minReadySeconds`, `deploymentAnnotations`), and chart-wide fields (`commonLabels`, `commonAnnotations`) are unchanged. The four UID/GID helpers (`haproxy.runAsUser` / `runAsGroup` / `fsGroup` / `dataplaneRunAsUser`) collapsed into a single `haproxy.uid` helper since all four returned the same value; this is internal to the chart and operator-invisible. `_helpers.tpl` was split into purpose-grouped files (`_naming.tpl`, `_libraries.tpl`, `_image.tpl`, `_credentials.tpl`, `_resources.tpl`, `_spoa-hub.tpl`, `_pod-spec.tpl`) for navigability.

### Fixed

- `templates/validatingwebhookconfiguration.yaml` now sources `watchedResources` from the merged template libraries (`haptic.mergeLibraries`) instead of iterating raw `.Values.controller.config.watched_resources`. The old form silently produced no `ValidatingWebhookConfiguration` for chart users whose watched resources were declared via libraries (e.g. `libraries/ingress.yaml: enableValidationWebhook: true`) rather than via raw helm-values overrides — meaning the admission webhook was effectively disabled, and malformed Ingresses (e.g. conflicting `cookie-persistence` + `cookie-persistence-no-dynamic` annotations) reached the controller where they failed render and stalled the reconcile pipeline for *all* other Ingresses. This is the same merging path `haproxytemplateconfig.yaml` uses, ensuring the webhook scope matches what the controller actually validates.
- `features-160-ssl-redirect-map` no longer uses the cross-render `first_seen` cache to dedupe hosts; it now uses a per-snippet `seen` map keyed by `(code, host)`. The previous form produced an inconsistent render under high resource churn — the snippet would skip registering `ssl-redirect-<code>.map` while the paired `frontend-filters-050-ssl-redirect` snippet still emitted the rule referencing it. Result: HAProxy reload failed with `failed to open pattern file <maps/ssl-redirect-<code>.map>` once the orchestrator post-config phase deleted the (correctly-) unreferenced map file. The fix keeps the two snippets in lockstep regardless of how often `redirectHosts` is observed.
- `haproxy.org/pod-maxconn` now only counts Running and Ready HAProxy pods (previously counted all pods including Pending, SysctlForbidden, CrashLoopBackOff)

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
