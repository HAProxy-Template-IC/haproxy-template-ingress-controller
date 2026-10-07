# Changelog

All notable changes to HAPTIC — the controller and its Helm chart — are
documented in this file. Controller changes are listed first; chart changes
(values, templates, chart defaults) follow under each release's "Helm chart"
subsection.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.1] - 2026-10-07

### Fixed

- Admission no longer mistakes unchanged inputs for conflicting updates, which could reject valid changes under load.
- Repaired fetched HTTP lists can pass validation and replace the previously accepted content.
- Rejected watched-resource changes emit a Kubernetes Warning event on the affected resource.
- Fetched HTTP content rejected before acceptance emits an `HTTPContentRejected` Warning event on the HAProxyTemplateConfig and increments `haptic_http_content_rejected_total`.

## [0.4.0] - 2026-10-06

HAPTIC 0.4 adds Gateway external authorization, header-based session persistence,
and weighted TLS routing. It rejects invalid Helm values, improves controller
startup at large route counts, and keeps independent routing updates moving when
a watched resource is invalid or external HTTP content awaits validation.

**Before upgrading:** check your Helm values, rename underscore-containing
`auth-headers-request` entries, and review Gateway route timeouts. The
[upgrade notes](./docs/site/docs/upgrade-notes.md#upgrading-to-04) cover each change.

### Added

- `haptic_http_content_revoked_total` metric and `HTTPContentRevoked` Warning Event: `http.Fetch` content is taken back when HAProxy refuses a configuration containing it.

### Changed

- Controllers use less CPU to validate a configuration before loading it, reducing startup time.
- Publishing a route change reuses unchanged auxiliary-file objects and pod-status entries; changed files are published in parallel, and cleanup avoids repeatedly reading the full configuration.
- Deploying renders leave new `http.Fetch` content out until its background fetch and validation succeed; a `critical: true` source without accepted content fails the render.

### Fixed

- Isolate invalid watched resource changes so independent endpoint updates and admission requests can continue through complete configuration validation.
- Prevent overlapping renders from repeatedly rewriting unchanged status conditions and blocking resource updates.
- Preserve event correlation for updates triggered by external HTTP content.
- Lower controller memory at large route counts: a controller restarting with 3,000 Ingresses completes within a 3 GiB limit instead of being `OOMKilled` on every attempt.
- A restarted controller whose first full render takes longer than the render timeout now finishes it instead of retrying it forever, so it deploys and validates admission requests again.
- A replica still running its first full render leaves the admission webhook Service while another replica can validate; alone, it denies requests with a message that says why.
- External auth forwards request headers whose names contain a dash, such as `X-Api-Key` from `auth-headers-request` and the default `X-Forwarded-For`/`-Proto`/`-Host`/`-Uri`; only `Authorization` and `Cookie` reached the auth service before (external-auth plugin v0.6.0).
- New `http.Fetch` content is accepted while watched resources change or concurrent renders read the same source; valid responses are no longer discarded or kept pending indefinitely.
- A failed `critical: true` `http.Fetch` fails the render as documented; before, `{{ http.Fetch(...) }}` rendered the source as empty.

### Helm chart

#### Added

- Governance rules take `namespaces` and `exemptNamespaces` to apply a single rule only in, or everywhere except, the listed namespaces.
- Gateway API: header-based session persistence (`sessionPersistence.type: Header`) on HTTPRoute and GRPCRoute.
- Gateway API: TLSRoute rules split connections across several `backendRefs` by `weight`; an invalid ref's share is rejected.
- Gateway API: `sessionPersistence.cookie.lifetimeType: Permanent` sets the cookie's `Max-Age`.
- Gateway API: `RetryBackoffUnsupported` Warning event on a route that sets `retry.backoff`, which HAProxy can't enforce.
- Gateway API: HTTPRoute `ExternalAuth` filter (`protocol: HTTP`) through the SPOA hub external-auth plugin; a rule whose filter can't be enforced answers `500` and its route status reports why.

#### Fixed

- Invalid Gateway TLS options no longer reject admission for unrelated resources.
- `extraContext.tls.ciphers`, `ciphersuites`, and `minVersion` install; a chart guard rejected the documented TLS policy keys.
- A WAF policy catalog set only through `waf.policies.configMapRefs` fails the install when the SPOA hub or the `hapticAnnotations` library is disabled, instead of rendering without the WAF.
- An HTTPRoute or GRPCRoute no longer inherits Ingress annotation features (external auth, access control, CORS, method and header requirements, request validation and others) from an Ingress with the same namespace and name.
- Gateway API: `retry` and `sessionPersistence` apply to their own route rule; rules of one route sharing a Service no longer all take the first rule's policy. Such a rule's backend is named with a `_r<rule index>` suffix.
- Gateway API: an HTTPRoute `retry.codes` entry HAProxy can't retry on, such as `507`, no longer breaks the configuration; HAPTIC drops it and records a `RetryCodeUnsupported` Warning event.
- Gateway API: a session name with spaces or other characters HAProxy can't take is rejected instead of being written into the configuration.
- Gateway API: `sessionPersistence` timeouts with several units, such as `1h30m`, apply in full instead of only their first unit.

#### Changed

- **BREAKING:** Gateway route timeouts follow HAProxy Unified Gateway: `request` is ignored and only `backendRequest` sets server inactivity; zero uses HAProxy's maximum timeout. Overall request deadlines remain unsupported.
- **BREAKING:** The chart ships a strict `values.schema.json`: Helm rejects unknown keys and wrongly typed values on install, upgrade, and template instead of ignoring them. [Check your values](./docs/site/docs/upgrade-notes.md#upgrading-to-04) before upgrading.
- **BREAKING:** `haproxy-haptic.org/auth-headers-request` and `haproxy-ingress.github.io/auth-headers-request` no longer forward header names containing `_`: admission rejects them and an existing Ingress gets an `InvalidAuthHeader` Warning Event. [Rename them](./docs/site/docs/upgrade-notes.md#3-check-auth-headers-request-header-names) before upgrading.

#### Security

- The pre-rollout validation hook's values Secret no longer contains the generated agent password and webhook private key.

## [0.3.0] - 2026-10-05

HAPTIC 0.3 serves HTTP/3 by default, keeps admission validation available while
a new configuration fails to load, and runs validationTests independently of
your deployment values.

**Before upgrading:** rename the changed `extraContext` keys, replace basic-auth
hashes HAPTIC now refuses, check host alias annotations, allow UDP on the
HTTPS port, and update custom templates and monitoring. The
[upgrade notes](./docs/site/docs/upgrade-notes.md#upgrading-to-03) give one
step for every **BREAKING** change below.

### Added

- `templatingSettings.testExtraContext`: the extraContext validationTests render with in place of `extraContext`.
- `http.Fetch` option `acceptStatus`: statuses besides 200 whose response body is the content.
- `http.Pending(url)`: whether an empty `http.Fetch` result is new content waiting to be accepted rather than a failed fetch; validationTest `httpResources` entries take `pending: true` to test it.
- `/readyz` and `/livez` health endpoints that judge the configuration a replica serves; `/healthz` keeps judging the newest one.
- The playground migration report classifies each key of a pasted ingress-nginx controller ConfigMap against the migration guide's ConfigMap table.

### Changed

- **BREAKING:** validationTest assertions no longer see `templatingSettings.extraContext`; they render with library defaults, `testExtraContext`, `_global` and their own `extraContext`. A test asserting output of your own values must set them in its `extraContext`, `_global` or `testExtraContext`.
- Each validationTest also renders its fixtures with your extraContext, which must render and, where the test asserts `haproxy_valid`, pass `haproxy -c`; the live validationTests budget grows from 100 ms to 175 ms per test to cover it.
- **BREAKING:** `trim` is the `trim(s, cutset)` builtin in every render path; use `strip(s)` to trim whitespace.
- **BREAKING:** The agent rejects an apply manifest without `identity_version: 1` with `400` instead of downgrading it to a reload. Clients built against `pkg/dataplane/agent/api` must set it.

### Removed

- **BREAKING:** The `http.Fetch` option `delay`; a call that sets it fails. Rename it to `interval`.
- **BREAKING:** The `strings_replace` template function; use `replace`.
- **BREAKING:** The `haptic_events_dropped_total` metric; use `haptic_events_dropped_critical_total`.

### Fixed

- A configuration reload that keeps failing no longer denies every admission request cluster-wide: the leading replica keeps serving and validating the previous configuration until a replica starts the new one.
- Overriding a value a bundled validationTest asserts the default of (`haproxy.ports.http`/`https`, `hardStopAfter`, `tune.bufsize`, `sslRedirectDefault`, basic-auth hash validation) no longer fails the configuration at load.
- Watched resources finish their initial sync only after every listed resource reached the store, so the first render after startup can't miss resources.
- Release the leader lease when a controller shuts down during a configuration hand-over, so a standby replica takes over within seconds instead of after the lease expires.
- Stop reporting normal controller shutdown cancellations and leadership handover as failures.
- Endpoint and other resource changes reach HAProxy within its retry window during rolling restarts and while resources keep changing, also while new `http.Fetch` content waits to be accepted, so requests no longer fail with 503 on a stopped pod.
- Renders scale linearly with the number of watched resources: a one-Ingress change among 3,000 Ingresses renders in 0.24 s instead of 1.5 s.

### Helm chart

#### Added

- HTTP/3 (QUIC), on by default: every TLS-terminating HTTPS listener, Gateway HTTPS listeners included, also listens on UDP and is advertised with `alt-svc`. The HAProxy and Gateway Services gain a UDP port with the HTTPS port's number; firewalls and load balancers must allow UDP 443. Disable with `extraContext.http3.enabled: false`.
- nginx-ingress library: `proxy-request-buffering`, `limit-burst-multiplier`, `affinity-mode` and `auth-tls-match-cn` annotations.
- nginx-ingress library: `custom-http-errors` replaces listed upstream error responses with pages fetched from the default backend, or from `extraContext.nginxDefaultBackendService`.
- nginx-ingress library: `proxy-buffer-size` records a `ProxyBufferSizeExceeded` Warning Event when the response headers can't fit HAProxy's buffer.
- An Ingress annotation under an enabled vendor library's prefix that the library doesn't know records an `UnknownAnnotation` Warning Event; nginx-ingress migration coverage classifies every annotation in the ingress-nginx reference.

#### Changed

- **BREAKING:** Rename extraContext `timeout_connect`, `timeout_client`, `timeout_server`, `timeout_http_request` and `timeout_http_keep_alive` to `timeoutConnect`, `timeoutClient`, `timeoutServer`, `timeoutHttpRequest` and `timeoutHttpKeepAlive`; the old keys fail the install.
- **BREAKING:** Rename extraContext `ssl_redirect_default` to `sslRedirectDefault`, now a boolean; the old key and a quoted value fail the install.
- **BREAKING:** Remove extraContext `hapticHstsMaxAge`; `haproxy-haptic.org/hsts` without `hsts-max-age` uses `tls.hsts.maxAge`, so its default `max-age` drops from two years to one.
- **BREAKING:** Basic-auth Secrets accept only bcrypt, SHA-256/SHA-512 crypt and yescrypt hashes by default, for every `auth-secret` annotation including nginx-ingress; `$apr1$`, `$1$`, DES crypt and plaintext are refused with an error naming the Ingress. Before upgrading, regenerate such hashes with `htpasswd -nB <user>` or `openssl passwd -6`.
- **BREAKING for Varnish users:** Varnish runs as a bundled non-root image that locks shared memory; cluster policy must allow `IPC_LOCK`.
- Response headers the chart adds (`Server`, HSTS, Ingress custom response headers, routing diagnostics) are set with `http-after-response`, so error pages HAProxy generates or replaces carry them too.
- Probe controller readiness on `/readyz` and liveness on `/livez`; the controller NetworkPolicy lets controller replicas reach each other's health port.
- Set `testExtraContext` to the extraContext computed from the chart defaults, keeping the deployment's library set, HAProxy version and feature switches.
- The controller dashboard's events-dropped panel splits drops by subscriber.
- Update the default HAProxy images to 3.0.29, 3.2.25, 3.3.16 and 3.4.6.

#### Fixed

- nginx-ingress: `use-regex` and `rewrite-target` route regex paths as case-insensitive regexes, and `$N` in `rewrite-target` now refers to the path's capture groups; before, the path matched as a literal prefix and `$2` was always empty.
- Increase the bundled validator memory limit to 256 MiB to accommodate large configuration inputs.
- Apply idle-connection draining only to HTTP frontends, avoiding warnings from TCP listeners.
- Derive the cache dispatcher timeout from application timeouts and retries without forcing reloads for route timeout changes.
- Use direct Valkey health probes to prevent orphaned probe processes and spurious child-process warnings.

#### Security

- **BREAKING:** `haproxy-haptic.org/host-alias-regex` and `haproxy-ingress.github.io/server-alias-regex` match the whole hostname and never apply to Gateway listeners; before, a regex matched any part of a hostname and could capture a Gateway listener's requests. A regex relying on a partial match stops matching; widen it, for example `example\.com` to `.*example\.com`. A regex whose groups don't close within it is rejected.
- **BREAKING:** `haproxy-haptic.org/host-alias`, `haproxy-ingress.github.io/server-alias` and `nginx.ingress.kubernetes.io/server-alias` reject a value that isn't a hostname, such as one with a port, path or underscore; aliases are matched case-insensitively.
- A host alias for a hostname an older Ingress already claims, as a rule host or an alias, is no longer routed and records a `RouteConflict` Warning Event; before, both entries reached the host map and either could win.

## [0.2.2] - 2026-10-03

### Fixed

- After a rolling upgrade, the config's `Validated` status could stay `False` (`LoadGateFailed`) when an old controller pod wrote it after the new leader; the leader now restores its own verdict.

### Helm chart

#### Security

- Regex paths (`haproxy-haptic.org/path-type: regex`, `haproxy-ingress.github.io/path-type: regex`, Gateway `RegularExpression`) match only on their own host; a top-level `|` or an early `)` in the path could capture other hosts' requests. A regex path whose groups don't close within it is now rejected, and a leading `^` now matches.

## [0.2.1] - 2026-10-01

### Changed

- Refine the HAPTIC logo with centered, scalable artwork that preserves the template-tag symbol.

### Fixed

- Avoid excessive controller memory use on Linux nodes with transparent huge pages enabled.
- Prevent standby controllers from restarting when HAProxy discovery events accumulate.
- Complete pod-status cleanup across auxiliary files after HAProxy replacement, including recovery from interrupted updates.
- Keep admission validation available while a terminating controller drains requests.
- Documentation version menus retain older stable releases and remove prereleases after their final release is published.
- The playground's default starter renders correctly on first load and when restoring an older saved session.

### Helm chart

#### Changed

- Allow HAProxy connections up to 60 seconds to drain after reloads and give controller and HAProxy pods 90 seconds to terminate.
- Update the default cache image to Varnish 9.1 and shared rate-limit storage to Valkey 9.2.

#### Fixed

- Close idle HTTP connections after their next response during reloads, avoiding abrupt disconnects for clients reusing a connection.
- Prefer the more specific HTTPRoute or GRPCRoute hostname when multiple routes intersect the same listener hostname.
- Keep HTTP and gRPC routes isolated between Gateways with overlapping hostnames.

## [0.2.0] - 2026-09-24

Changes since 0.1.0, including the 0.2.0 alpha series.

HAPTIC 0.2 expands Gateway API routing and application policies, adds checks
before deployment and fleet diagnostics, and applies more routing changes
without reloading HAProxy. You can try custom templates in the browser before
installing the controller.

**Before upgrading:** Kubernetes 1.33 or newer is required. Migrate Helm values,
custom templates, and monitoring integrations using the
[upgrade notes](./docs/site/docs/upgrade-notes.md#upgrading-to-02). Preserve your
existing routing class names and explicitly enable any vendor annotation
libraries you use. Read the
[rollback limits](./docs/site/docs/deploying-with-helm.md#recover-a-failed-upgrade)
before changing the chart's resource schemas.

### Added

- Custom status templates can declare ownership of list entries, preserving entries written by other controllers.
- Templates can inspect PEM public keys with `public_key_info` and parse one YAML document with `parse_yaml`.
- `haptic doctor` checks live fleet state and creates diagnostic bundles without Secret values or rendered configuration.
- HAProxy 3.4 support, including runtime addition and removal of eligible backends without a reload.
- Typed watched-resource access and collection pipelines for custom templates, reusable `HAProxyTemplateLibrary` resources, and template-defined Kubernetes resources.
- Runtime discovery of watched API versions and schemas, including automatic adaptation when watched CRDs are installed, upgraded, or removed.
- Pluggable output validators and enforcement of embedded validation tests whenever configuration loads or changes.
- `haptic preflight` validates chart values before deployment, `haptic diff` predicts reloads, and `haptic agent state` inspects a pod's deployed configuration.
- A browser playground and editable documentation examples with a full-window editor and first-edit guidance for trying templates without a cluster.
- A portable agent skill for template customization, resource watches, and validation, with installation instructions and versioned downloads.

### Changed

- **BREAKING:** The `haptic` binary and per-pod HAPTIC agent replace `haptic-controller` and the HAProxy Data Plane API. Eligible map, certificate, and server updates apply at runtime; rejected reloads restore the last working files.
- **BREAKING:** Custom templates use `currentConfig.ServerIndex`, validation fixtures use `currentServers`, and `statusPatch` takes the resource object. Use `toJSON` for composite values instead of implicit string conversion.
- Incremental rendering, smaller resource caches, and warm follower replicas reduce repeated work, memory use, and leadership-transition delays.
- Controller metrics now cover agent operations and fleet convergence; update dashboards using the [metric migration table](./docs/site/docs/operations/metrics-reference.md#where-the-old-metrics-went).

### Fixed

- Playground downloads include all rendered maps, certificates, and error files; local try-out supports configurations without hostname maps and publishes ports on localhost.
- Avoid rejecting valid resource updates when the admission cache has not yet observed a newly created dependency.
- Preserve the full connection-drain quiet period when HAProxy counter reads or agent scheduling are delayed.
- Playground previews include status, events, applied resources, and reload impact.
- Leadership handover preserves the validated configuration result when startup events arrive out of order, allowing stale upgrade validation failures to clear.
- Pending reload follow-ups observe already accepted configurations without resending updates that can collide with the reload.
- Large runtime updates complete all operation batches before map read-back and plan publication, avoiding false divergence and fallback reloads during bulk route removal.
- Validation worker concurrency accounts for the memory limit, preventing parallel HAProxy checks from exhausting preflight containers on large nodes.
- Incremental templates read optional scalar fields consistently, including explicit false and zero values.
- Optional HTTP fetch failures can publish validated output without poisoning the incremental cache.
- Templates that take variable addresses inside nested functions now keep references to the correct values.

### Security

- Map replacements remain atomic, and pending reloads retain denial entries until their replacement enforcement can run.
- Missing or invalid WAF catalogs deny affected routes instead of retaining their previous enforcement.
- Controller-to-agent mutual TLS supports live certificate replacement and bounded CA trust overlap; agent diagnostics use a local read-only socket.
- Admission requests have payload limits; HTTP redirects and diagnostics no longer expose credentials or auxiliary-file contents.

### Known limitations

- Backend TLS certificate verification uses the configured SNI hostname; independent certificate SAN matching is unsupported. See [Gateway API coverage](./docs/site/docs/operations/gateway-conformance.md#coverage).

### Helm chart

#### Added

- `HAProxyRoutePolicy` attaches JWT/API-key authentication, shared rate limits, WAF catalogs, and private HTTP caching to Gateway route rules; credential and catalog rotation use validated references to immutable objects.
- `credentials.existingSecret` uses externally managed credentials without generating random values during GitOps rendering.
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

- Gateway route and backend TLS policy status updates preserve entries owned by other controllers, including concurrent updates.
- Gateway route status retains explicitly selected parent ports and each parent's condition timestamps.
- Gateway routes without filters avoid policy-template execution; compact route and backend names retain route-kind and cross-namespace identity.
- Ingress and Gateway JWT authentication accept the same normalized PEM public keys when sharing a Secret.
- Admission webhook names support watch keys containing underscores or uppercase letters.
- Gateway rule filters and backends keep route kinds and target namespaces distinct when resource names match.
- Named Service ports resolve correctly, and exact Ingress paths preserve trailing slashes.

#### Security

- TCPRoute backends enforce BackendTLSPolicy CA and hostname verification.
- BackendTLSPolicy rejects unsupported certificate identities and blocks affected backends instead of verifying a different hostname.
- Agent mutual TLS is enabled by default, with automatic CA and identity renewal, optional cert-manager provisioning, or externally supplied Secrets.
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
