# Changelog

All notable changes to HAPTIC — the controller and its Helm chart — are
documented in this file. Controller changes are listed first; chart changes
(values, templates, chart defaults) follow under each release's "Helm chart"
subsection.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A browser playground for `HAProxyTemplateConfig` runs the controller's production render path entirely client-side in WebAssembly — nothing is uploaded, so real secrets and cluster resources are safe to paste. It shows the exact `haproxy.cfg`, map files, decoded certificates, status patches, and rendered Kubernetes resources a config produces, with per-line provenance back to the template that emitted each line, a reload-vs-runtime impact verdict against a pinned baseline, and an annotation-migration report. It is published per version on the docs site at `/playground/<version>/` (and a moving `/playground/dev/` from `main`).
- The playground runs a config's `spec.validationTests` in the browser via a **tests** tab, reporting each test and assertion as pass or fail with the failure message. Because a browser has no `haproxy` binary, `haproxy_valid` assertions fall back to the pure-Go syntax + schema check and are labelled `syntax + schema`; every other assertion type behaves identically to `haptic-controller validate`.
- The user docs and landing page embed the playground inline: how-to pages carry runnable, editable examples — including challenges with revealable solutions — that render live in the browser via the same engine, each with an "open in the full playground" link. An embed can spotlight any output tab, auxiliary file, or line range. Links can deep-link a specific example with `/playground/?preset=<id>`.
- `haptic-controller migrate-check` audits another ingress controller's Ingresses before you switch to HAPTIC: it classifies every source-controller annotation as supported, different, dropped, failing, or unknown, and renders each Ingress through the real template pipeline to catch rejections. With no arguments it uses the image-embedded chart, live-cluster schemas, and live Ingresses; `-f`/`--resources`/`--schema-dir`/`-n`/`--output text|json|markdown` switch inputs offline. Exit codes: `0` clean, `1` differences or unknowns, `2` blockers. Coverage is declared per source by the template libraries (`spec.migrationCoverage`), so no controller or annotation name is hardcoded.
- Fleet-convergence and config-staleness metrics — `haptic_haproxy_fleet_size`, `haptic_haproxy_fleet_converged`, `haptic_last_full_sync_timestamp_seconds`, and `haptic_deployment_consecutive_failures` — give a noise-free "is the fleet converged, and for how long has it not been" signal to alert on instead of the now-self-healing deploy error counter.

### Fixed

- The admission webhook now validates the EFFECTIVE HAProxyTemplateConfig — resolving watched-resource versions against live discovery and stripping `requires`/`requiresFields`-unsatisfied snippets and tests, exactly like the controller's load gate. Previously it compiled the raw proposed spec, so on clusters without an optional watched resource's CRDs (e.g. no Gateway API) every config update was denied with a template compile error while the running controller accepted the same config (#79). If resolution itself fails transiently, the webhook admits with a warning and defers to the load gate.
- A failed or timed-out deployment now invalidates the deploy scheduler's lane baseline, so a queued runtime-only render re-dispatches as a full structural sync instead of silently restamping the config version header over structural content the HAProxy workers never loaded — previously this could defeat the fast retry with a 0-op "success" and leave new listeners parked unreloaded until the next unrelated change (#76).
- A runtime server update against a backend the loaded config doesn't yet have (`No such backend`/`No such server`) is no longer misclassified as a transient reload and retried futilely for up to 2 seconds per apply; it now fails fast to the scheduled structural deploy that actually creates the backend, so convergence isn't delayed by the retry storm.
- A retryable deploy failure now self-reschedules with bounded exponential backoff (capped, then handed off to the 60s drift backstop) instead of waiting up to a full minute for the periodic drift check, and a fully-failed deploy now reports `Programmed=False` with a reason instead of freezing the last-known status (#72).

### Helm chart

#### Added

- Each vendor annotation library (nginx-ingress, haproxy-ingress, haproxytech) now declares machine-readable migration coverage for its source controller's annotations — surfaced on `spec.migrationCoverage` and used to generate the per-source annotation-support tables in the [migration guide](./docs/site/docs/migrating.md). Only enabled libraries contribute.
- `HAProxyFleetDiverged` PrometheusRule alert (toggleable) and a Grafana fleet-convergence panel, built on the new fleet-convergence metrics.
- New `haproxy.org/cors-respond-to-options` annotation: when `"true"`, HAProxy answers the CORS preflight (OPTIONS) with a 204 instead of forwarding it to the backend, matching the upstream HAProxy Kubernetes Ingress Controller.
- New `extraContext.hstsEnabled` opt-in (default off, declared by the SSL library) sends `Strict-Transport-Security` on every TLS response, matching ingress-nginx's and haproxy-ingress's global HSTS default; `hstsMaxAge`/`hstsIncludeSubdomains`/`hstsPreload` tune the value, and a per-Ingress `hsts` annotation still overrides it per host. Because HSTS is only effective over HTTPS, the rendered config emits a warning when it is enabled without an HTTP→HTTPS redirect. The nginx-ingress and haproxy-ingress migration coverage now points to this knob.

#### Fixed

- Installing without cert-manager now works out of the box: when cert-manager provisioning is enabled (the default) but its API is absent, the chart generates a self-signed `default-ssl-cert` Secret (10-year validity) instead of silently skipping the `Certificate` — previously the controller failed every render (`TLS Secret not found`) and the HAProxy pods never became ready. A Secret that already exists (external-secrets, manual) is left untouched, the generated one survives upgrades, and manual mode (`certManager.enabled=false`) keeps its provide-your-own contract (#78).
- The `nginx.ingress.kubernetes.io/enable-cors` and `haproxy-ingress.github.io/cors-enable` annotations now answer the CORS preflight (OPTIONS) request in HAProxy with a synthetic 204 carrying the `Access-Control-*` headers, matching ingress-nginx — previously the preflight was forwarded to the backend, which then had to handle OPTIONS itself. Both annotations are now marked `supported` in the migration coverage.
- The `haproxy.org/cors-*` annotations now mirror the upstream HAProxy Kubernetes Ingress Controller: a non-wildcard `cors-allow-origin` is treated as a regex matched against the request Origin and the `Access-Control-Allow-Origin` header echoes the matched origin (previously the value was emitted verbatim, so a regex or multi-origin allow-list produced an invalid header). CORS headers are now added via `http-after-response` (so they also apply to the preflight response), and the `cors-allow-methods` / `cors-allow-headers` / `cors-max-age` defaults now track upstream (`*` / `*` / `5s`).
- The `nginx.ingress.kubernetes.io/cors-allow-origin` and `haproxy-ingress.github.io/cors-allow-origin` annotations now accept a comma-separated allow-list with single-level `*.` subdomain wildcards, match it against the request `Origin`, and echo the matched origin back (adding `Vary: Origin` for non-wildcard lists) — matching ingress-nginx. Previously the annotation value was emitted verbatim as the `Access-Control-Allow-Origin` header, so a multi-origin list or a wildcard produced an invalid header. A malformed origin now fails the render.
- `haproxy-ingress.github.io/auth-secret` now also parses an `auth`-key htpasswd Secret (haproxy-ingress's and ingress-nginx's native format — one `user:hash` line each, `user::plain` for insecure), so a Secret migrated from those controllers authenticates. The previous base64-hash-per-username Secret shape still works.
- `haproxy.org/auth-realm` now normalizes spaces to dashes (matching the upstream HAProxy Kubernetes Ingress Controller) instead of failing the render, and its default realm is now `Protected-Content` (was `RestrictedArea`). The `sanitize_auth_realm` toggle is removed (normalization is unconditional).
- The Gateway API template library no longer requires the Ingress library. Its shared hostname-to-map-key helper (`MapKeyForHost`) moved into the always-loaded base library (renamed `util-ingress-host-key` → `util-host-key`), so `controller.templateLibraries.gateway` can be enabled with `ingress` disabled without a template-compilation error.

## [0.2.0-alpha.1] - 2026-07-05

### Added

- Support for every Gateway API release, adapted at runtime: `watchedResources` entries accept an ordered `apiVersions` candidate list and an `optional` flag, `templateSnippets`/`validationTests` accept `requires`/`requiresFields` (features whose resources or schema fields aren't served are stripped at config load), and a CRD watch reinitializes the controller when a watched CRD is installed, upgraded, or removed — no Helm operation or pod restart needed. Resolved versions are exposed via `resources.<name>.APIVersion()` and `/debug/vars/effectiveConfigResolution`; `controller validate` resolves the same way against `--schema-dir`. A required resource with no served version fails startup with a clear error instead of hanging.
- The config's embedded `validationTests` are now enforced at every gate: the admission webhook denies a failing `HAProxyTemplateConfig` at `kubectl apply`, and the controller rejects a failing config at startup and on every change — the last-good config keeps serving, and failing test names surface on `status.validationErrors`. Previously only the `controller validate` CLI ran them.
- Kubernetes Events on the `HAProxyTemplateConfig`: a `Warning`/`ValidationFailed` Event when a config change fails validation and a `Normal`/`Validated` Event on recovery, so failures surface in `kubectl describe` and `kubectl get events`.
- HAProxy 3.4 support: the controller is built, validated, and released for HAProxy 3.4.
- Typed watched resources: chart templates access watched resources through compile-time typed top-level globals (e.g. `gateways`, `httproutes`), type-checked at controller startup against schemas from the live apiserver or `--schema-dir`. See [ADR-0010](./docs/adr/0010-typed-watched-resources.md).
- `spec.k8sResources` declares full Kubernetes resources reconciled via Server-Side Apply, owned by the `HAProxyTemplateConfig` CR (garbage-collected on uninstall); a partial-ownership mode (`haproxy-haptic.org/ownership: partial`) lets chart-owned and haptic-owned entries coexist on one resource.
- `spec.validators` declares pluggable validator sidecars consulted by the admission webhook (per-entry socket, file-glob routing, timeout); `/healthz` reports each socket's reachability.
- The admission webhook hot-reloads its TLS certificate from the mounted Secret, so a cert-manager renewal is served within about a minute without a controller restart. The `--webhook-cert-secret-name` flag is replaced by `--webhook-cert-dir` (env `WEBHOOK_CERT_DIR`).
- Gateway API `RequestMirror` filters, including multiple and percentage/fraction mirrors, backed by the bundled mirror SPOA plugin.
- Ingress `spec.defaultBackend` support, both rules-less (catch-all) and combined with `spec.rules`.
- HAProxy responses now carry a `Server: haptic` header.
- New metrics: `haptic_haproxy_reloads_total` (the canonical reload/SLO signal), `haptic_dataplane_api_operations_total`, runtime fast-path counters (`haptic_runtime_fast_path_*`), and per-plugin SPOA metrics via the hub's `/metrics` endpoint.
- `spec.dataplane.configPublishInterval`, `reloadVerificationTimeout`, and `syncTimeout` are now tunable; `spec.watchedResources.<name>.debounceInterval` adds a per-resource batching override.
- New `spoa-hub` container image bundling the SPOE hub plus seven plugin libraries (mirror, coraza, external-auth, fingerprinting, maxmind, otel, sso-auth), Cosign-signed with a CycloneDX SBOM.
- External authentication (`auth-url`-style annotations) wired end to end via the spoa-hub external-auth plugin.

### Changed

- Leader-election timing defaults raised from 15s/10s/2s to 30s/20s/5s (`leaseDuration`/`renewDeadline`/`retryPeriod`): more headroom against apiserver or CPU-starvation stalls, at the cost of slower crash-failover (up to ~35s; voluntary handoffs still release the lease immediately). Tune via `spec.controller.leaderElection` (#57).
- Config validation now runs asynchronously with latest-wins coalescing: rapid successive `HAProxyTemplateConfig` edits validate only the newest change, and an in-flight validation no longer blocks config processing or leaves a new leader without a config (#55).
- `store: on-demand` watched resources keep far less memory resident: the informer cache strips object bodies down to index and identity fields; templates still read the full object, fetched live on access. See [ADR-0012](./docs/adr/0012-on-demand-projection-and-access-gated-reconcile.md).
- All log output now shares one structured logfmt format (client-go and other third-party lines included), and routine per-request/per-reconcile success lines moved from INFO to DEBUG — failures, denials, config changes, and leadership transitions stay at INFO. Set `LOG_LEVEL=DEBUG` for the previous verbosity.
- Runtime-eligible server changes (pod-IP rotation, port, admin state) now reach the live HAProxy workers in milliseconds via a runtime fast path instead of waiting for the next scheduled deploy; structural changes still take the rate-limited reload path. See [ADR-0011](./docs/adr/0011-no-haproxy-server-state-file.md). The reconciler-level debounce was removed — batching is per-watcher via `spec.watchedResources.<name>.debounceInterval` (default raised from 100ms to 2s; `"0"` fires on every change).
- Content-only changes now apply to the live HAProxy workers via the runtime API without a reload: map files, TLS certificates (HAProxy 3.2+), SSL CA files (HAProxy 3.2+, e.g. frequent trust-bundle rotation), and frontend `maxconn`. Adding or removing files and all other structural changes still reload, and a failed runtime apply falls back to a reload, so the result always converges.
- `controller validate` now requires `--schema-dir` (or `HAPTIC_SCHEMA_DIR`) for typed-access templates; the embedded Gateway schema fallback was removed. Production (live apiserver) is unaffected.

### Fixed

- Fixed controller crash-loop and Gateway status-apply failures on clusters running older Gateway API releases (v1.1–v1.5) whose schemas lack fields the bundled chart exercises (#59).
- A replica that loses the leader-election lease now reinitializes and re-enters the election loop instead of running as a permanent follower — on single-replica deployments, a missed lease renewal could previously stall deployments until a pod restart (#57).
- Runtime map updates are now verified by read-back and fall back to a reload when the live map did not converge — a map write acknowledged but lost by HAProxy could previously latch stale routing until an unrelated reload (#48).
- Eliminated intermittent 503s during rolling restarts of single-replica backends: a newly-Ready pod's endpoint change reaches the live workers within `option redispatch`'s rescue window even when it coincides with an in-flight structural deploy, runtime applies retry across a concurrent reload, and deleting an unreferenced certificate no longer schedules a stray second reload that briefly blacked out the runtime socket (#67).
- Reloads are bounded to one per `minDeploymentInterval` under concurrent churn, and auxiliary-file updates batch into the main config sync's single reload instead of triggering separate DataPlane API auto-reloads.
- Resource status no longer goes stale: statuses are written even when a change produces no config diff (a Gateway with no attached routes no longer sticks at `Programmed=Unknown`), and sustained event bursts no longer drop status or deployment events.
- `HAProxyCfg.status.deployedToPods` now reflects reality: a failed deploy keeps the pod's last successfully-deployed checksum plus a `lastError` instead of reading as converged, failed deployments are retried on the next reconcile instead of waiting for the drift timer, and a startup race no longer leaves a deployed pod unreported.
- Cross-pod config drift is now detected and repaired: the deployer tracks each pod's actual post-sync state, and a render race no longer leaves a newer render undeployed.
- Credentials-Secret rotation now takes effect immediately, and content updates to watched Secrets/ConfigMaps are no longer dropped.
- gRPC: requests hitting the default backend get a proper trailers-only `grpc-status: 12` response instead of a connection-breaking 404, GRPCRoute method matching works on TLS + ALPN h2 and plaintext h2c, and gRPC over h2c multiplexes with HTTP/1.1 on the same bind.
- Gateway TLS listeners on non-default ports now bind correctly.
- The renderer fails fast when the config references a map file it didn't register (previously the file was deleted as unreferenced, breaking every subsequent reload), and each render sees one consistent snapshot of the watched-resource stores.
- `store: on-demand` resources no longer trigger a full list or one API fetch per resource on per-key reads, drift cycles, and the `/debug/vars` endpoints.
- Ingress `pathType: Exact` now preserves trailing slashes (`/foo/` no longer matches `/foo`).
- `controller validate` renders with the same context as production (`capabilities`, `extraContext` promotion), so a template can no longer pass validation yet render differently in production.
- HAProxy 3.3 configs are now validated against the v3.3 DataPlane API schema (was v3.2), and DataPlane API versions newer than the newest bundled client clamp down to it instead of falling back to the oldest v3.0 client.
- Watched-resource admission (e.g. Ingress) now gets the same ~9s internal validation deadline as `HAProxyTemplateConfig` admission (was 5s), so large-config dry-runs aren't prematurely admitted without validation.
- Status patches, admission overlays, and owned-resource applies resolve a resource's plural via the cluster RESTMapper, so a CRD with an irregular plural no longer targets a nonexistent GroupVersionResource.
- Hardened component lifecycle: re-acquired leadership no longer stacks orphaned event subscriptions (previously endless "critical drops" log spam), event-loop panics recover instead of silently killing the loop, and the controller no longer schedules a spurious iteration restart right after startup.
- HTTP content stores no longer misclassify an empty `200 OK` as `304 Not Modified` (stale content), SSL-certificate Secret publishing retries on write conflicts, and backends whose only change is on the `default-server` line now update instead of staying stale.
- HAProxy file paths in rendered configs use slash-only semantics regardless of host OS, fixing the controller binary on Windows (no-op on Linux).
- Bundled SPOA hub updated to v0.7.3: config reloads quiesce in-flight SPOE dispatches before retiring the old plugin set, fixing lost mirror/OTLP work around reloads (#47); unhandled SPOE messages surface via a WARN log and `spoa_messages_unhandled_total`.

### Removed

- The dormant webhook-cert-rotation pipeline and its `haptic_webhook_cert_expiry_timestamp_seconds` / `haptic_webhook_cert_rotations_total` metrics (it never rotated certs) — replaced by the webhook-cert hot-reload above.
- `namespaceSelector` on `watchedResources` entries (was never wired up). Scope via `labelSelector` or separate controller instances.
- Management of HAProxy `program` sections — HAProxy removed the section in 3.3 and `client-native` dropped the model. Rendered configs may still contain `program` sections on older HAProxy versions, but the controller no longer parses, diffs, or normalizes them.

### Helm chart

#### Added

- New `nginx-ingress` template library for `nginx.ingress.kubernetes.io/*` annotation compatibility (disabled by default): routing and redirects (`rewrite-target`, `app-root`, `server-alias`, `default-backend`, `from-to-www-redirect`, `permanent-redirect`/`temporal-redirect` with code overrides, `ssl-redirect`/`force-ssl-redirect` — 308 by default, tunable via the `nginxHttpRedirectCode` extraContext var), session affinity (`session-cookie-*`), rate limiting and throttling (`limit-rps`/`limit-rpm`/`limit-whitelist`, `limit-rate`/`limit-rate-after`), backend TLS (`proxy-ssl-*`), request/response header rewrites (`upstream-vhost`, `x-forwarded-prefix`, `connection-proxy-header`, `proxy-cookie-domain`/`-path`, `proxy-redirect-from`/`-to`), retries (`proxy-next-upstream`/`-tries`), body-size limits (`proxy-body-size`), auth (`auth-*`, `auth-secret-type`, `satisfy`), and request mirroring (`mirror-target`, via the spoa-hub mirror plugin).
- haproxy-ingress library additions: `oauth: oauth2_proxy` (desugars onto the external-auth machinery), `rewrite-target`, rate limiting (`limit-rps`/`limit-rpm`/`limit-whitelist` — previously advertised but emitting no config), `proxy-body-size`, `ssl-ciphers-backend`/`ssl-cipher-suites-backend`, agent health checks (`agent-check-*`), `default-backend-redirect`/`-code`, `server-alias`/`server-alias-regex`, and raw directive injection (`config-frontend`/`config-global`/`config-defaults`).
- haproxytech library additions: `rate-limit-whitelist` and `ssl-redirect-port`.
- Gateway API support extended: TLSRoute (Terminate and Passthrough), TCPRoute (L4 forwarding), `RequestMirror` filters (via the spoa-hub mirror plugin), static Gateway addresses (per-Gateway LoadBalancer Service, multi-IP), `spec.infrastructure` propagation, ListenerSet routing, GEP-91 frontend client-certificate validation, HTTPRoute/GRPCRoute cookie session persistence (GEP-1619), HTTPRoute `retry`, per-listener TLS options (GEP-2907), and BackendTLSPolicy `validation.subjectAltNames`.
- `spoaHub` values block and SPOA hub sidecar (hub v0.7.3 plus seven plugin libraries: mirror, coraza, external-auth, fingerprinting, maxmind, otel, sso-auth), auto-rendered when any plugin is enabled; a `spoa-hub` template library wires the HAProxy-side SPOE config, and the hub's runtime config reloads without a pod restart.
- nginx-ingress and haproxy-ingress libraries wire the external-auth annotation family (`auth-url`, `auth-signin`, `auth-method`, `auth-headers-*`, client-mTLS `auth-tls-*`) to the external-auth plugin, plus the Coraza WAF annotations (`/waf`, `modsecurity-snippet`) with per-resource opt-in and a `default-on` dispatch mode.
- `controller.validators` block and a validator sidecar consulted by the admission webhook, auto-enabled and auto-wired with the spoa-hub sidecar, so admission-time WAF/TOML validation works out of the box.
- The `HAProxyTemplateConfig` admission webhook now also runs the config's embedded `validationTests`, denying a failing config at `kubectl apply`; its `timeoutSeconds` is 10, and `failurePolicy: Ignore` still admits-with-warning when the webhook is slow or unreachable.
- The ingress library now emits a `Warning` Event (reason `BackendUnresolved`) on each Ingress whose backend Service or named port can't be resolved — visible in `kubectl describe ingress` — so a permanent Service-name typo is distinguishable from a propagation race; the Event is removed automatically once the Service appears (#66).
- Four more default PrometheusRule alerts (individually toggleable): `configRejected`, `haproxyPodsRejected`, `noHAProxyPods`, and `criticalEventsDropped`.
- `controller.templateLibraries.gateway.experimentalChannel` gates the validationTests that assert Experimental-channel HTTPRoute fields (`sessionPersistence`, `retry`) — since Gateway API v1.6 the Standard and Experimental channels ship an identical CRD set, so the channel can't be auto-detected.
- Template libraries can declare default `templatingSettings.extraContext` values, merged at the lowest precedence so operator overrides still win.
- Always-on local `peers localinstance` section so opted-in stick-tables survive reloads: rate-limit counters (`haproxy.org/rate-limit-*`, nginx-ingress `limit-rps`/`limit-connections`, haproxy-ingress `limit-rps`/`limit-rpm`) now persist across config reloads instead of resetting.
- New values: `haproxy.initialConfig` (HAProxy bootstrap ConfigMap), `controller.config.dataplane.{configPublishInterval,reloadVerificationTimeout,syncTimeout}`, `controller.config.extraContext.hardStopAfter` (default `10s`, emits `hard-stop-after` so old workers don't accumulate across reloads), and `haproxy.dataplane.aclFormat` (dataplane access-log format override).
- The chart-static `haptic-haproxy` LoadBalancer Service is now rendered via the controller's `spec.k8sResources` (Server-Side Apply with an `OwnerReference`), folding non-default Gateway listener ports into the same Service.
- HAProxy NetworkPolicy opens to all TCP ports when `haproxy.networkPolicy.allowExternal: true`, so dynamic Gateway listener ports work; restrictive mode is unchanged.
- RBAC: the ClusterRole gains `customresourcedefinitions` read access (schema resolution), cluster-wide Event write verbs (ingress library), and cluster-wide plus namespace-scoped `services` write verbs (gateway library, per-Gateway Services); ClusterRole and webhook rules now cover every declared API-version candidate of each watched resource.

#### Changed

- **BREAKING**: path matching order is now selected by `controller.config.routing.regexMatchOrder` (`default`/`last`), replacing the removed `path-regex-last` template library. Operators with `templateLibraries.pathRegexLast.enabled: true` must switch to `routing.regexMatchOrder: last`. See [ADR-0005](./docs/adr/0005-path-matching-order-as-values-flag.md).
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

- The gateway library now supports every Gateway API release, resolved at runtime: each Gateway kind is an optional watched resource with an ordered `apiVersions` candidate list (core kinds v1/v1beta1, GRPCRoute v1/v1alpha2, TLSRoute v1/v1alpha3/v1alpha2, TCPRoute v1/v1alpha2, ReferenceGrant v1/v1beta1, BackendTLSPolicy v1/v1alpha3). Installing or upgrading Gateway API CRDs activates support at runtime — no `helm upgrade` needed; features whose fields don't exist in an older release's schemas stay inactive there. The Helm-render-time `.Capabilities` gate is removed.
- The GatewayClass object is now created at runtime by the controller (Server-Side Apply, owned by the `HAProxyTemplateConfig`) instead of by a Helm template, so it exists exactly when the `gatewayclasses` CRD is served. **Upgrade note:** `helm upgrade` removes the previously Helm-owned GatewayClass and the controller recreates it within one reconcile; existing Gateways are unaffected.
- Per-route annotation policies are now applied by shared frontend rules reading per-backend/per-host map files instead of inline per-backend/per-ingress rules: `proxy-body-size`, upstream header overrides (`set-host`, `upstream-vhost`, `x-forwarded-prefix`, `connection-proxy-header`), literal `rewrite-target`, `ssl-redirect`/`force-ssl-redirect`, host redirects (`redirect-to`, `request-redirect`, `permanent-redirect`/`temporal-redirect`), HSTS, `app-root`, `auth-tls-error-page`, `from-to-www-redirect`, `default-backend-redirect`, and `ssl-redirect-port`. Adding or changing these is now a map-only, reload-free update; behavior, status codes, and config-injection guards are unchanged (`app-root`, `auth-tls-error-page`, and `hsts-max-age` gain previously missing injection guards).
- The deployed DataPlane API now addresses storage (maps/certs/general files) by relative paths resolved against the HAProxy base dir — the same identifiers HAProxy uses — so map content changes apply via the runtime API without a reload. The `dataplane.mapsDir`/`sslCertsDir`/`generalStorageDir` values are unchanged.
- The auto-generated DataPlane API password (`credentials.dataplane.password` left empty) is now a random 32-char value instead of a deterministic hash of release name and namespace. Existing installs are unaffected (the password is preserved from the existing Secret on `helm upgrade`). **GitOps note:** under ArgoCD/Flux (no cluster lookup at render time), an empty password regenerates on every sync — set `credentials.dataplane.password` explicitly for those setups.
- The validating admission webhook now provisions its own self-signed TLS certificate by default (`webhook.certManager.enabled` defaults to `false`; validity via `webhook.selfSigned.certValidityDays`, default 10y, reused across upgrades), so admission validation works out of the box without cert-manager. cert-manager stays opt-in and is recommended for production (automatic rotation); the self-signed cert is not auto-rotated. **Upgrade note:** a release that used the previous cert-manager default holds a webhook Secret without Helm ownership metadata; before upgrading, either keep cert-manager (`--set webhook.certManager.enabled=true`) or delete the old Secret (`kubectl delete secret <release>-webhook-cert -n <namespace>`) so the chart can recreate it.
- Default `haproxyVersion` is now `3.4` (was `3.2`); the controller and HAProxy pod images default to the HAProxy 3.4 series. Override `haproxyVersion` to stay on an older series.
- Template libraries (`gateway`, `nginx-ingress`, `haproxy-ingress`, `haproxytech`) are now packaged as conditional subcharts: a release stores only the source of the libraries it enables, so an install with all libraries enabled no longer exceeds the Kubernetes 1 MiB release-Secret limit. No values changes (the same `controller.templateLibraries.<x>.enabled` flags), and the rendered `HAProxyTemplateConfig` is byte-identical.
- The controller's startup probe is now enabled by default (30 × 10s budget): startup runs the config's embedded `validationTests`, so time-to-first-healthy can exceed the liveness budget on slow or contended nodes — previously the liveness probe could kill the controller mid-initialization.
- `haproxy.ports.http`/`https` defaults shift from `8080`/`8443` to `80`/`443` so `dst_port` equals the Gateway listener port; explicit overrides are kept.
- Dataplane `minDeploymentInterval` default raised to `5s` (was `2s`), throttling reload-inducing structural deploys; endpoint changes still apply instantly via the controller's runtime fast path. EndpointSlice watches keep `debounceInterval: "0"` for instant rolling-restart reaction.
- **HAProxy `defaults` `timeout connect` lowered from `5000` to `100` (100 ms).** Backends are pod IPs over the CNI; 100 ms fails fast on a SYN to a just-terminated pod so `option redispatch` retries. Operators on slow networks restore `5000` via `extraContext.timeout_connect`.
- gateway library: the cluster-wide `configmaps` watch now defaults to `store: on-demand` (it is only read by name for BackendTLSPolicy CA bundles), keeping references instead of every ConfigMap body resident.
- `extraDeploy` now accepts both list and dict formats.
- `haproxy.org/pod-maxconn` quantizes the pod count to the next power of 2 to avoid reload cascades on scaling.
- Compound resource names (derived from `haptic.fullname`) now truncate to the 63-char label limit; releases with very long names will see affected resources renamed on upgrade (run `helm diff upgrade` first).
- Removed the "TLS Certificate Expiry" Grafana dashboard panel — the controller no longer emits `haptic_webhook_cert_expiry_timestamp_seconds` (the webhook cert is hot-reloaded; see the controller section above).

#### Security

- Annotation values interpolated into the HAProxy config — CIDR lists (allow/deny lists, rate-limit and `satisfy` whitelists) and single-value annotations (upstream header overrides, `proxy-ssl-*`, `proxy-cookie-domain`/`-path`, the auth realm) — are now validated against strict charsets, closing a config-injection vector where a crafted annotation containing a newline could inject arbitrary HAProxy directives into the rendered config.

#### Fixed

- Fixed controller crash-loop and Gateway status-patch failures on clusters running older Gateway API releases (v1.1–v1.5) or Standard-channel installs without the TCPRoute CRD (#59).
- Named Service-port references (`port.name`) in Ingress, HTTPRoute, GRPCRoute, and SSL-passthrough backends now resolve to the correct numeric port (previously silent 503s or empty backends). When the referenced Service isn't in the controller's store yet, the backend renders degraded (placeholder slots, 503 for that route) and converges once the Service appears, instead of failing the whole render and denying the Ingress at admission (#50); a Service that exists but lacks the named port still fails loudly.
- `defaults` now sets `option redispatch` and `base.yaml` filters out not-ready/terminating endpoints, eliminating the single-replica rolling-restart 503 windows.
- Reserved-slot server addresses are now config-driven across reloads (removed the HAProxy server-state-file machinery); placeholders use the unroutable `192.0.2.1:1` sentinel. See [ADR-0011](./docs/adr/0011-no-haproxy-server-state-file.md).
- `haproxy.dataplane.validateConfig: false` now actually skips the dataplane's `haproxy -c` (the flag was misplaced), cutting raw-config push time ~130ms → ~18ms.
- haproxytech: `haproxy.org/check`, `check-interval`, and `scale-server-slots` are now honored instead of silently ignored, and IP access control reads the canonical `haproxy.org/allow-list`/`deny-list` annotations (deprecated `whitelist`/`blacklist` honored as fallback) — the real annotations previously emitted no ACL.
- haproxy-ingress: `maxconn-server`, `maxqueue-server`, `initial-weight`, `backend-check-interval`, and `health-check-port`/`-fall-count`/`-rise-count` now render their `default-server` keywords (previously validated but never emitted), the deprecated `whitelist-source-range` alias is honored, and the `path-type` annotation now actually routes.
- Gateway TLS: an unspecified `tls.mode` defaults to `Terminate` per spec (the listener was previously skipped silently), and a BackendTLSPolicy with no resolvable CA returns 503 instead of downgrading to plaintext.
- The bundled `validationTests` now pass in any release namespace (the shared SSL fixture no longer hardcodes `haproxy-haptic`/`default`).
- The chart fails fast at install with actionable guidance when `webhook.certManager.enabled=true` but the cert-manager CRDs are absent, instead of leaving the controller pod stuck in `ContainerCreating`.
- Basic-auth snippets no longer fail when the referenced auth Secret is briefly absent from the render snapshot.
- PrometheusRule default alerts `HAProxyControllerHighQueueDepth`/`HAProxyControllerNoLeader` now reference metrics the controller actually emits (the old expressions never fired).
- `networkPolicy.ingress.webhook.from` and `networkPolicy.egress.kubernetesApi` defaults switched to `ipBlock` `0.0.0.0/0` + `::/0`, so a host-network apiserver can reach the webhook on clusters enforcing NetworkPolicy (previously silent admission failures).
- The validating-webhook configuration now sources `watchedResources` from the merged libraries, so library-declared resources are actually validated.

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
