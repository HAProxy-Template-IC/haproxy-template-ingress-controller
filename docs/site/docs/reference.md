# Chart values reference

Every Helm value the chart accepts, with its type and default.

## Value ownership and upgrade migration

Each runtime setting has one authoritative value. The chart rejects obsolete
duplicates instead of silently choosing one and allowing the process, Service,
or generated HAProxy configuration to drift.

| Previous value | Authoritative value |
|----------------|---------------------|
| `controller.crdName` | `controller.configName` |
| `controller.debugPort` | `controller.ports.healthz` |
| `controller.config.controller.healthzPort` | `controller.ports.healthz` |
| `controller.config.controller.metricsPort` or `controller.extraEnv[].name=METRICS_PORT` | `controller.ports.metrics` (`0` disables metrics) |
| `controller.config.dataplane.port` | `haproxy.ports.dataplane` |
| `haproxy.dataplane.logLevel` | `haproxy.agent.logLevel` |
| `haproxy.dataplane.resources` | `haproxy.agent.resources` |
| `haproxy.dataplane.extraEnv` | `haproxy.agent.extraEnv` |
| `haproxy.dataplane.service` | `haproxy.agent.service` |
| `haproxy.dataplane.validateConfig` | Removed. The pod's own HAProxy binary judges the configuration at reload, and the webhook and the config-load gate still run the full `haproxy -c` |
| `haproxy.dataplane.debugSocketPath` | Removed. Profile the agent through its own metrics and `GET /v1/state` |
| `haproxy.dataplane.aclFormat` | Removed. It formatted the Data Plane API's own access log; the agent logs one structured line per apply instead |
| `haproxy.dataplaneBin` | Removed. The agent is the controller's binary, in the controller's image |
| `controller.config.routing.regexMatchOrder` | `controller.config.templatingSettings.extraContext.routing.regexMatchOrder` |
| `controller.defaultSSLCertificate` | `defaultSSLCertificate` |
| `haproxy.enterprise.version` | `haproxyVersion` |
| Root-level controller workload values (`replicaCount`, `image`, `deploymentAnnotations`, `webhook`, `monitoring`, `networkPolicy`, `autoscaling`, `podDisruptionBudget`, `service`, `serviceAccount`, `rbac`, `securityContext`, `resources`, probes, rollout, and extras) | The same key under `controller.*` (for example `controller.replicaCount`) |
| `controller.config.templatingSettings.extraContext.debug` | `controller.config.templatingSettings.extraContext.diagnostics.routingHeaders.enabled` (now defaults to `false`) |
| `controller.statusPatches.enabled` and `controller.config.templatingSettings.extraContext.statusPatchesDisabled` | `controller.config.templatingSettings.extraContext.statusPatches.enabled` (inverted: `statusPatchesDisabled: true` becomes `enabled: false`) |
| `controller.config.templatingSettings.extraContext.password_hash_validation_regex` and `…password_hash_validation_error_message` | `controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.regex` and `.errorMessage` |
| `controller.config.templatingSettings.extraContext.hstsEnabled`, `hstsMaxAge`, `hstsIncludeSubdomains`, `hstsPreload` | `controller.config.templatingSettings.extraContext.tls.hsts.enabled`, `.maxAge`, `.includeSubdomains`, `.preload` |
| `vector.excludeMaintServerMetrics` | `controller.config.templatingSettings.extraContext.prometheusExporter.excludeMaintServers` — HAProxy applies `?no-maint` itself, for every scraper |
| `vector.excludeMetrics` | `controller.config.templatingSettings.extraContext.prometheusExporter.excludeMetrics` — same entry names, `enabled`, `families` and `requires`; `pattern` is gone, HAProxy's exporter filters by exact family name |
| `vector.podMonitor` and `spoaHub.monitoring.podMonitor` | `haproxy.monitoring.podMonitor` — one PodMonitor for every metrics endpoint on the HAProxy pod |

Cache and rate-limit settings introduced after the previous release use their
final ownership from the start: `cache.varnish` owns the Varnish workload,
`cache.haproxy` owns HAProxy cache integration, `rateLimit.shared` owns the
feature, and `rateLimit.shared.managedStore` owns the optional bundled Valkey
topology. Plugin execution remains under `spoaHub.plugins.*`.

## CRD lifecycle

Helm installs the CRDs in `crds/` once and never upgrades them on a subsequent
`helm upgrade`. This hook Job runs `haptic apply-crds` (server-side
apply) so additive CRD schema changes reach the cluster on install and upgrade.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `crds.upgradeJob.enabled` | bool | `true` | Run the `pre-install`/`pre-upgrade` hook Job (and its scoped RBAC) that server-side applies the bundled CRDs. Disable if you manage CRDs out-of-band or lack cluster-scoped CRD write permission at upgrade time |
| `crds.upgradeJob.backoffLimit` | int | `2` | Job retry limit (the apply is idempotent, so retries are safe) |
| `crds.upgradeJob.activeDeadlineSeconds` | int | `300` | Job wall-clock deadline |
| `crds.upgradeJob.resources` | object | cpu `50m` / memory `64Mi`–`128Mi` | Resource requests and limits for the apply Job pod |
| `crds.upgradeJob.annotations` | map | `{}` | Extra annotations for the Job (merged with chart defaults) |
| `crds.upgradeJob.labels` | map | `{}` | Extra labels for the Job (merged with chart defaults) |

## Pre-rollout validation

A `pre-install`/`pre-upgrade` hook Job renders the chart embedded in the controller image with this release's values and runs the controller's own load gate over the result — structural validation and the full `validationTests` suite including `haproxy -c` — before any object is applied. A failing configuration fails the release; the previous release keeps serving. Argo CD runs it as a `PreSync` hook. The fail-closed load gate still guards every path that skips hooks (`--no-hooks`, `kubectl`, rollback).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `preRolloutValidation.enabled` | bool | `true` | Run the gate. The Job hard-fails when the controller image's embedded chart version differs from the chart being installed — validating the wrong chart would pass on the wrong input — so disable it when deliberately running a drifted image |
| `preRolloutValidation.backoffLimit` | int | `1` | Job retry limit |
| `preRolloutValidation.activeDeadlineSeconds` | int | `600` | Job wall-clock deadline. Generous: schema fetch, engine compile, and ~700 `haproxy -c` checks on a possibly cold node |
| `preRolloutValidation.resources` | object | cpu `200m` / memory `256Mi`–`512Mi` | Resource requests and limits for the validation Job pod |
| `preRolloutValidation.annotations` | map | `{}` | Extra annotations for the Job |
| `preRolloutValidation.labels` | map | `{}` | Extra labels for the Job |

## Deployment & Image

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.replicaCount` | int | `2` | Number of controller replicas (2+ recommended for HA with leader election) |
| `haproxyVersion` | string | `"3.4"` | HAProxy major.minor series. Drives both the controller image tag suffix (`:<version>-haproxy<haproxyVersion>`) and — combined with `haproxyPatchVersions` — the HAProxy pod image tag |
| `haproxyPatchVersions` | map | See values.yaml | Per-`haproxyVersion` community patch pins (for example `"3.2": "3.2.x"`). Maintained by the chart and auto-updated by Renovate |
| `haproxyEnterprisePatchVersions` | map | See values.yaml | Per-`haproxyVersion` enterprise revision pins (for example `"3.2": "3.2r1"`). Used when `haproxy.enterprise.enabled=true` |
| `controller.image.repository` | string | `registry.gitlab.com/haproxy-haptic/haptic` | Controller image repository |
| `controller.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `controller.image.tag` | string | `""` | Controller image tag *without* the HAProxy-series suffix; empty uses the chart `appVersion`. The rendered reference is always `<tag>-haproxy<haproxyVersion>`, so the suffix is appended to an explicit value too |
| `nameOverride` | string | `""` | Override chart name |
| `fullnameOverride` | string | `""` | Override full release name |
| `commonLabels` | map | `{}` | Labels added to every chart-rendered resource on top of the standard `app.kubernetes.io/*` set |
| `commonAnnotations` | map | `{}` | Annotations added to every chart-rendered resource that has an `annotations` block |
| `controller.deploymentAnnotations` | map | `{}` | Annotations added only to the controller `Deployment` (in addition to `commonAnnotations`); useful for hooks like reloader's `reloader.stakater.com/auto: "true"` |
| `extraDeploy` | list or map | `{}` | Free-form Kubernetes resources to render alongside the chart. Each entry is rendered through `tpl` so it can reference chart values. Map form (keys → manifests) is convenient for composing across multiple values files |

## Controller core

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.configName` | string | `haptic-config` | Name of the HAProxyTemplateConfig object the controller watches |
| `controller.logLevel` | string | `INFO` | Initial controller log level (`LOG_LEVEL` env var) — see [Logging and templating](#logging-and-templating) for the runtime override |
| `controller.kubeClient.qps` | float | `-1` | Client-side apiserver queries per second (QPS) for the controller's own requests (`KUBE_CLIENT_QPS` env var). `<= 0` disables client-side throttling and relies on apiserver Priority & Fairness; a positive value installs one shared client-side rate limiter across all the controller's clients |
| `controller.kubeClient.burst` | int | `0` | Client-side apiserver burst (`KUBE_CLIENT_BURST` env var); used only when `controller.kubeClient.qps > 0` (`0` means `2*qps`) |
| `controller.ports.healthz` | int | `8080` | Single source of truth for the controller's `/healthz` and `/debug/*` listener, container port, Service, probes, and NetworkPolicy |
| `controller.ports.metrics` | int | `9090` | Single source of truth for the `/metrics` listener, container port, Service, and monitors; `0` disables metrics and requires all monitor resources to be disabled |
| `controller.ports.webhook` | int | `9443` | Admission webhook HTTPS port |
| `controller.config.templatingSettings.extraContext.statusPatches.enabled` | bool | `true` | Whether the controller writes LoadBalancer addresses back to Ingress/Gateway `.status`. Disable during a controller migration so the incumbent keeps owning status — with `extraContext.statusPatches.enabled: false` the status-patch snippets become no-ops |

## Template libraries

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.templateLibraries.base.enabled` | bool | `true` | Core HAProxy configuration. Disabling drops the `haproxyConfig` template the other libraries plug into; leave on unless you supply a complete replacement |
| `controller.templateLibraries.kubernetesBackends.enabled` | bool | `true` | Shared Kubernetes Service port and EndpointSlice backend resolution used by the bundled routing libraries |
| `controller.templateLibraries.ssl.enabled` | bool | `true` | SSL/TLS and HTTPS frontend support |
| `controller.templateLibraries.ingress.enabled` | bool | `true` | Kubernetes Ingress resource support |
| `controller.templateLibraries.gateway.enabled` | bool | `true` | Gateway API support (HTTP, gRPC, TLS and TCP routes) |
| `controller.templateLibraries.gateway.experimentalChannel` | bool | `false` | Declare that the Gateway API *Experimental* channel (`experimental-install.yaml`) is installed. Enables the `validationTests` that assert experimental HTTPRoute fields (`retry` per Gateway Enhancement Proposal (GEP) 1731, `sessionPersistence` per GEP-1619) — Helm can't detect the channel because both installs ship identical CRDs and only HTTPRoute *fields* differ. The route snippets emit those directives whenever the fields are present, regardless of this flag |
| `controller.templateLibraries.ingressAnnotationsCompat.enabled` | bool | `true` | Shared ingress-annotations-compat scaffold (level 2.5). Provides parameterized macros consumed by the Ingress vendor annotation libraries below |
| `controller.templateLibraries.governance.enabled` | bool | `true` | Governance rule engine. Enforces declarative constraints over any watched resource; inert until you define `controller.config.templatingSettings.extraContext.governance.rules` |
| `controller.templateLibraries.hapticAnnotations.enabled` | bool | `true` | `haproxy-haptic.org/*` — HAPTIC's native annotation vocabulary; a best-of-breed superset of the three vendor libraries. The recommended vocabulary for new configs |
| `controller.templateLibraries.haproxytech.enabled` | bool | `false` | `haproxy.org/*` annotation compatibility (haproxytech/kubernetes-ingress migration) — opt-in |
| `controller.templateLibraries.haproxyIngress.enabled` | bool | `false` | `haproxy-ingress.github.io/*` annotation compatibility (jcmoraisjr/haproxy-ingress migration) — opt-in |
| `controller.templateLibraries.nginxIngress.enabled` | bool | `false` | `nginx.ingress.kubernetes.io/*` annotation compatibility (ingress-nginx migration) — opt-in |
| `controller.templateLibraries.customCrdExample.enabled` | bool | `false` | Worked example of a resource-agnostic library for a custom `Route` CRD, for learning or exercising the reload-free author contract. Not for production routing (it watches a demo kind) — opt-in |
| `controller.templateLibraries.spoaHub.enabled` | bool | `false` | HAProxy-side Stream Processing Offload Agent (SPOA) hub wiring. Auto-loaded when the SPOA hub sidecar is rendered (any `spoaHub.plugins.*` enabled, or `spoaHub.enabled: true`); set this to `true` to force-load the library standalone |

## Shared response cache

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `cache.haproxy.hashBalanceFactor` | int | `150` | HAProxy bounded-load consistent hashing: cap any one Varnish shard's share to this factor of the mean (`0` disables) |
| `cache.haproxy.responseTimeoutMs` | int | `2000` | Inactivity timeout on the Varnish hop. A miss spends it waiting for application response headers. Before headers, a timeout or transport failure retries GET/HEAD directly; after delivery starts, HAProxy terminates an idle partial response because it can't safely replay it. Set this above normal application time to first byte |
| `cache.varnish.enabled` | bool | `false` | Deploy the shared Varnish cache tier and emit the cache routing/backend. Cache-enabled GET/HEAD requests use healthy Varnish shards; other methods and requests observed while every shard is unhealthy go directly to the application backend. The bypass is recorded in `cache_degraded` and `haptic_degraded_cache_total` |
| `cache.varnish.loopbackPort` | int | `8090` | Dedicated internal HAProxy port Varnish fetches cache misses from (the "sandwich" backend leg), so the WAF/rate-limit/auth/routing chain runs once on the client request and never on the miss. Reached only by Varnish (via `originServiceName`, gated by the HAProxy NetworkPolicy); never published on the LoadBalancer |
| `cache.varnish.originServiceName` | string | `haptic-cache-origin` | Name of the internal ClusterIP Service (in the release namespace) that fronts the dedicated backend-fetch port on the HAProxy pods |
| `cache.varnish.workload` | string | `statefulset` | Varnish workload kind: `statefulset` (ordered rollout keeps `1/N` of the cache warm on restart) or `deployment` (ephemeral accelerator) |
| `cache.varnish.replicas` | int | `2` | Number of Varnish cache shards |
| `cache.varnish.image` | string | `varnish:9.0` | Varnish container image — stock upstream, since the loopback topology needs no custom build. Pin to a digest in production |
| `cache.varnish.imagePullPolicy` | string | `IfNotPresent` | Kubernetes pull policy for the Varnish image (`Always`, `IfNotPresent`, or `Never`) |
| `cache.varnish.malloc` | string | `256m` | Varnish `-s malloc,<size>` cache size per shard; set to roughly 75% of the pod memory limit |
| `cache.varnish.resources` | object | cpu `100m` / memory `384Mi` | Varnish pod resource requests and limits. A CPU request is required for autoscaling (the HPA's `Utilization` target is a percentage of the request); keep the memory limit above `malloc` plus overhead |
| `cache.varnish.podDisruptionBudget` | object | enabled `true`, `maxUnavailable` `1` | PodDisruptionBudget settings for the Varnish shards. Pods also prefer separate nodes and use a soft hostname topology spread, so the default still runs on single-node clusters |
| `cache.varnish.networkPolicy.enabled` | bool | `true` | Emit a NetworkPolicy that only allows this release's HAProxy pods to reach Varnish and only allows Varnish egress to DNS plus this release's HAProxy HTTP origin |
| `cache.varnish.autoscaling.enabled` | bool | `false` | Autoscale the Varnish tier with a HorizontalPodAutoscaler. When on, the HPA owns the replica count (the static `replicas` is ignored) |
| `cache.varnish.autoscaling.minReplicas` | int | `2` | Minimum Varnish shards the HPA keeps |
| `cache.varnish.autoscaling.maxReplicas` | int | `6` | Maximum Varnish shards the HPA scales to |
| `cache.varnish.autoscaling.targetCPUUtilizationPercentage` | int | `70` | Target average CPU utilization that drives scaling |
| `cache.varnish.autoscaling.scaleDownStabilizationSeconds` | int | `600` | Seconds the HPA waits before acting on a scale-down, to protect cache warmth |

## Shared request rate limiting

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `rateLimit.shared.enabled` | bool | `false` | Enable the native shared rate-limit annotations and auto-enable the bundled `rate-limit` SPOA plugin. When false, Ingresses carrying `haproxy-haptic.org/rate-limit-requests` fail the render loudly instead of being silently unprotected. When true, leave `rateLimit.shared.managedStore.enabled=true` for the chart-managed HA Valkey/Sentinel store or provide `rateLimit.shared.externalStore.urls`; HAPTIC fails the render instead of silently using a per-pod fallback |
| `rateLimit.shared.failClosed` | bool | `false` | Runtime dependency policy. By default, a Valkey failure degrades both algorithms to a bounded per-sidecar limiter; if the hub/plugin returns no verdict, HAProxy allows the request. These paths set `rate_limit_degraded`, and plugin metrics distinguish local fallback decisions. Set true to deny when the store or plugin can't answer. Lease mode still spends its existing local lease before applying the store-failure policy |
| `spoaHub.plugins.rate-limit.timeoutMs` | int | `50` | Per-plugin processing timeout for shared rate-limit checks. The chart derives the HAProxy engine's outer deadline from this value; keep it low on public edges so overload reaches the configured lease/exact failure policy quickly |
| `spoaHub.plugins.rate-limit.storeOperationTimeoutMs` | int | `10` | Per-operation Redis/Valkey timeout rendered as the rate-limit plugin's `store_timeout_ms`. Exact `gcra` mode waits on this path per request; tune according to measured store round-trip time and failover behavior |
| `rateLimit.shared.managedStore.enabled` | bool | `true` | Deploy chart-managed HA Valkey with Sentinel and inject its `store_url` into the rate-limit plugin, so budgets are shared across the HAProxy fleet. Leave true for the out-of-box HA store; set false only when you bring your own store via `rateLimit.shared.externalStore.urls`. Takes effect only when `rateLimit.shared.enabled` is also true — it's a sub-option of the shared limiter, so on its own it deploys nothing |
| `rateLimit.shared.externalStore.urls` | list | `[]` | One bring-your-own HA Redis/Valkey/Sentinel/Cluster endpoint, used with `managedStore.enabled=false` (setting both fails the render). Multiple URLs fail validation because the bundled plugin shares one circuit breaker across its shards. Configure the external store with a non-evicting memory policy. The chart owns the generated `store_url` and rejects a manual `store_url`/`store_urls` in `spoaHub.plugins.rate-limit.params` |
| `rateLimit.shared.managedStore.image` | string | `valkey/valkey:9.1.1-alpine` | Valkey image for the chart-managed shared rate-limit store |
| `rateLimit.shared.managedStore.imagePullPolicy` | string | `IfNotPresent` | Kubernetes pull policy for both the Valkey and Sentinel containers (`Always`, `IfNotPresent`, or `Never`) |
| `rateLimit.shared.managedStore.port` | int | `6379` | Valkey Service port for the chart-managed shared rate-limit store |
| `rateLimit.shared.managedStore.replicas` | int | `3` | Fixed Valkey pod count for the chart-managed Sentinel topology: one writable primary plus replicas for failover. Must be at least 3. This is HA, not automatic horizontal Valkey scaling |
| `rateLimit.shared.managedStore.maxMemory` | string | `96mb` | Valkey `--maxmemory` for the chart-managed shared rate-limit store |
| `rateLimit.shared.managedStore.maxMemoryPolicy` | string | `noeviction` | Valkey memory policy for the chart-managed shared rate-limit store. This must remain `noeviction`: evicting a limiter key silently recreates a full budget. At the memory limit, writes fail and follow the configured lease/exact dependency policy instead |
| `rateLimit.shared.managedStore.sentinel` | object | port `26379`, quorum `2` | Sentinel settings for managed-store failover: port, quorum, down-after, failover timeout, parallel syncs, and Sentinel container resources |
| `rateLimit.shared.managedStore.podDisruptionBudget` | object | enabled `true`, `maxUnavailable` `1` | PodDisruptionBudget settings for the managed Valkey pods |
| `rateLimit.shared.managedStore.networkPolicy.enabled` | bool | `true` | Emit a NetworkPolicy that only allows HAProxy/SPOA pods and store-internal Valkey/Sentinel traffic to the managed store |
| `rateLimit.shared.managedStore.resources` | object | cpu `50m` / memory `128Mi` | Valkey pod resource requests and limits. The chart-managed store is a fixed-size HA Sentinel topology; use bring-your-own Redis/Valkey infrastructure when you need horizontal store scaling |

## Request-body inspection and JSON schema validation

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.tune.bufsize` | int | `16384` | HAProxy's `tune.bufsize`, which is also the ceiling on one runtime CLI batch — the controller sizes its map and server batches from it. `requestBodyInspection.haproxyBuffer.sizeBytes` raises it when body inspection needs a larger buffer; the larger of the two is emitted |
| `controller.config.templatingSettings.extraContext.tune.cliMaxPayloadSize` | int | `131072` | HAProxy's `tune.cli.max-payload-size`, the ceiling on one runtime CLI payload. Emitted on HAProxy 3.4 and above only: the keyword doesn't exist below it, where a payload is capped by `tune.bufsize` instead |
| `controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes` | int | `16384` | Request-body inspectors need the whole body buffered, so this raises the shared HAProxy `tune.bufsize` whenever it names the larger of the two. API validation and Coraza policy body caps must fit within `sizeBytes - reservedBytes` |
| `controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.reservedBytes` | int | `8192` | Bytes reserved for the request line, headers, and rewrite space; increase for large cookies, JWTs, or tracing headers |
| `controller.config.templatingSettings.extraContext.requestBuffering.enabled` | bool | `true` | Wait for the request body before taking a backend connection, so a slow uploader holds an HAProxy buffer instead of a backend server slot. Only requests declaring a `Content-Length` are held, so gRPC and chunked streaming are never buffered |
| `controller.config.templatingSettings.extraContext.requestBuffering.waitTimeout` | string | `10s` | How long HAProxy waits for the declared body before returning `408`. HAProxy also releases the request once `tune.bufsize` is full |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled` | bool | `false` | Enable native JSON request-schema annotations and auto-enable the bundled `api-gateway` plugin. Matching annotations fail loudly while disabled |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.waitTimeout` | duration | `100ms` | HAProxy-side maximum wait for a matching POST/PUT/PATCH body. Unrelated routes don't wait |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.defaultMaxBytes` | int | `8192` | Default validation input cap when an Ingress omits `request-schema-max-body-size`. The effective cap must fit in the shared HAProxy body capacity |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.defaultFailOpen` | bool | `true` | Default when an Ingress omits `request-schema-fail-open`. A missing plugin verdict allows the request and sets `schema_degraded`, exported as `haptic_degraded_schema_total`; schema validation reduces risk, it doesn't establish identity. Missing referenced Kubernetes objects still fail at render time. Set `false` where a malformed body reaching the backend is worse than refusing the caller |
| `spoaHub.plugins.api-gateway.timeoutMs` | int | `25` | Hub-side processing timeout for JSON validation. The chart derives the message's outer HAProxy deadline from this value; increase only for measured CPU or scheduling pressure |
| `spoaHub.plugins.api-gateway.maxConcurrency` | int/tpl | derived from sidecar memory (16 at the default 256Mi) | Ceiling for concurrent JSON parse/schema evaluations. With `adaptiveConcurrency` on (default) this is the controller's upper bound, not a fixed limit; derived from `spoaHub.resources` memory so it self-scales. Set a literal to override |

## Coraza WAF

Template-side routing, policy catalogs, and Ingress-author permissions live in the structured `extraContext.waf` bag so raw `HAProxyTemplateConfig` users get the same behavior. Process and plugin execution settings live under `spoaHub.plugins.coraza`; neither tree aliases or overrides the other. A non-empty `policies.inline`, `policies.configMapRefs`, or `policies.defaultPolicy` activates policy governance—there is no second enable flag that can leave a configured catalog inert.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.waf.failClosed` | bool | `false` | What happens when the SPOA hub or Coraza plugin returns no verdict — a reload, a restart, a timeout. Default allows the request: a WAF reduces risk, it doesn't establish identity, so a blip in HAPTIC's own sidecar mustn't refuse traffic that did nothing wrong. The event is still counted (`haptic_degraded_waf_total`, and the `waf_degraded` access-log field), so a WAF silently not inspecting stays visible. Set true only where letting a request past uninspected is worse than refusing it. Requests the WAF did inspect and reject are denied with `403` either way |
| `controller.config.templatingSettings.extraContext.waf.dispatch.mode` | string | `opt-in` | Which requests the rendered config sends to Coraza: `opt-in` runs it only on routes carrying a native or compatibility WAF annotation; `default-on` runs it on every route unless an authorized `nginx.ingress.kubernetes.io/enable-modsecurity: "false"` opts out. `default-on` auto-enables the Coraza plugin |
| `controller.config.templatingSettings.extraContext.waf.dispatch.defaultEnforcement` | string | `deny` | WAF enforcement (`deny` or `detect`) for requests dispatched by `mode: default-on`; selected policies and authorized per-route overrides take precedence. Ignored when `mode: opt-in` |
| `controller.config.templatingSettings.extraContext.waf.crs.url` | string | `""` | URL of a gzip-compressed tar of an Open Worldwide Application Security Project (OWASP) Core Rule Set (CRS) release. Empty uses the ruleset compiled into the Coraza plugin. When set, HAPTIC fetches and expands the archive, writes the rule files to general storage, and substitutes them for the `@crs-setup.conf.example` and `@owasp_crs/*.conf` includes in `spoaHub.plugins.coraza.directives` — the rest of those directives, including their order, is left alone. Must be `https://`: the ruleset decides what the WAF blocks, so a plaintext fetch could be replaced in transit and the substituted WAF would still validate. If the ruleset can't be obtained HAPTIC keeps the one already deployed to the fleet, then falls back to the embedded ruleset, so the WAF is never left without rules |
| `controller.config.templatingSettings.extraContext.waf.crs.refreshInterval` | duration | `1h` | How often to re-fetch the ruleset. The request is conditional (`If-None-Match` / `If-Modified-Since`), so an unchanged ruleset costs one 304 and triggers no re-render, no push, and no WAF recompile |
| `controller.config.templatingSettings.extraContext.waf.crs.timeout` | duration | `30s` | Per-attempt HTTP timeout for the ruleset fetch |
| `controller.config.templatingSettings.extraContext.waf.crs.retries` | int | `3` | Retry attempts per fetch. A fetch that ultimately fails never fails the render — it falls back rather than blocking unrelated configuration changes |
| `controller.config.templatingSettings.extraContext.waf.customRules.limits.maxIngresses` | int | `32` | Maximum Ingress-specific Coraza applications from native or compatibility SecLang. This applies even when no reusable policy catalog is configured |
| `controller.config.templatingSettings.extraContext.waf.customRules.limits.maxBytesPerIngress` | int | `16384` | Maximum custom SecLang bytes accepted from one Ingress, independent of whether a reusable policy catalog is configured |
| `controller.config.templatingSettings.extraContext.waf.policies.defaultPolicy` | string | `""` | Optional policy applied to every managed Ingress without a selection. Required when `allowPolicySelection=false` |
| `controller.config.templatingSettings.extraContext.waf.policies.requestBody.waitTimeout` | duration | `100ms` | Maximum HAProxy wait only for policies whose `requestBody.mode` is `any` or `json` |
| `controller.config.templatingSettings.extraContext.waf.policies.requestBody.defaultMaxBytes` | int | `8192` | Body cap used when a policy omits `requestBody.maxBytes` |
| `controller.config.templatingSettings.extraContext.waf.policies.requestBody.maxBytes` | int | `8192` | Highest body cap any policy may request. This is deliberately separate from the default, so approving a larger policy doesn't enlarge every policy. The effective value is enforced by HAProxy and written into that Coraza application |
| `controller.config.templatingSettings.extraContext.waf.ingressPermissions.allowPolicySelection` | bool | `true` | Permit Ingress authors to select an approved policy. Disable with a `defaultPolicy` for an immutable baseline |
| `controller.config.templatingSettings.extraContext.waf.ingressPermissions.allowEnforcementOverride` | bool | `false` | Permit native or compatibility annotations to select `deny`/`detect` instead of administrator enforcement while Coraza governance is active |
| `controller.config.templatingSettings.extraContext.waf.ingressPermissions.allowWafDisable` | bool | `false` | Permit an Ingress to opt out through a compatibility annotation such as `enable-modsecurity: "false"`. This stronger capability is separate from enforcement-mode overrides |
| `controller.config.templatingSettings.extraContext.waf.ingressPermissions.allowCustomRules` | bool | `false` | Permit arbitrary per-Ingress WAF rules through the nginx-compatible `modsecurity-snippet` annotation while Coraza governance is active; this grants WAF-policy-author capability |
| `controller.config.templatingSettings.extraContext.waf.ingressPermissions.allowRawHAProxyConfig` | bool | `false` | Permit raw HAProxy annotations while Coraza governance is active. Enable only when every Ingress writer is trusted |
| `controller.config.templatingSettings.extraContext.waf.policies.inline` | map | `{}` | Administrator-owned policies. Fields: `description`, `enforcement`, nested `requestBody.mode`/`maxBytes`, `allowedMethods`, `paranoiaLevel`, `anomalyThreshold`, `ruleExclusions`, and advanced `secLang` |
| `controller.config.templatingSettings.extraContext.waf.policies.configMapRefs` | map | `{}` | Trusted ConfigMap catalogs keyed by catalog name, each an exact `namespace`/`name`/`key` triple using the same policy schema. A map, like its sibling `inline`, so adding one catalog keeps the rest |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.enabled` | bool | `false` | Namespaced self-service authoring: each namespace may define policies for its own Ingresses in its well-known catalog ConfigMap |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.configMapName` | string | `waf-policies` | Well-known ConfigMap name discovered per namespace for self-service catalogs |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.key` | string | `policies.yaml` | Data key inside each self-service catalog ConfigMap |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.allowSecLang` | bool | `false` | Permit `secLang` in self-service policies (arbitrary rule code in the shared Coraza process — grant deliberately) |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.limits.maxPoliciesPerNamespace` | int | `4` | Maximum self-service policies loaded per namespace (deterministic sorted cut; excess policies fail closed for their selectors) |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.limits.maxTotalPolicies` | int | `64` | Cluster-wide self-service policy budget, separate from the trusted catalog's `limits.maxCount` |
| `controller.config.templatingSettings.extraContext.waf.policies.limits.maxCount` | int | `16` | Maximum policies across inline definitions and all trusted ConfigMaps |
| `controller.config.templatingSettings.extraContext.waf.policies.limits.maxSecLangBytes` | int | `65536` | Maximum advanced SecLang bytes in one reusable policy |
| `controller.config.templatingSettings.extraContext.waf.policies.limits.maxRuleExclusions` | int | `256` | Maximum structured Core Rule Set (CRS) rule-exclusion entries in one reusable policy |
| `spoaHub.plugins.coraza.timeoutMs` | int | `15` | Hub-side processing timeout for WAF evaluation. The chart derives the message's outer HAProxy deadline from this value; this is a failure bound, not expected latency |
| `spoaHub.plugins.coraza.maxConcurrency` | int/tpl | derived from sidecar memory (16 at the default 256Mi) | Ceiling for concurrent Coraza evaluations. With `adaptiveConcurrency` on (default) this is the controller's upper bound, not a fixed limit; derived from `spoaHub.resources` memory so it self-scales (give the sidecar more memory → higher ceiling). Set a literal to override |
| `spoaHub.plugins.coraza.adaptiveConcurrency` | bool | `true` | The hub resizes the admission semaphore at runtime from Coraza's measured service time (ADR-0002), finding the right concurrency from live latency with no manual tuning; `maxConcurrency` is then the controller's ceiling. Set `false` for a fixed cap. Full adaptivity needs a hub image with adaptive support; an older hub ignores the flag and runs `maxConcurrency` as a fixed cap |

## Policy guardrails (governance)

Org-wide baselines that namespace teams can't omit. Configured entirely under
`extraContext` (it creates no Kubernetes resources). The engine is on by default because the annotation library ships one rule (`haptic-compress-enable`, which makes response compression on-by-default); with that rule disabled and no rules of your own, it does nothing. You
declare a map of generic, JSONPath-driven `rules`, keyed by a name you choose;
each rule targets a watched resource by name and, per matching resource, either
**injects** a default when a value is absent or **validates** the value when
present. Rules apply in sorted key order.

For a step-by-step rollout — audit, fix, then enforce — see the
[Governance guardrails how-to](operations/governance.md). This section is the
field reference.

With `enforcement: reject`, a new or edited violating resource is denied at the
admission webhook, scoped to that resource's own admission (so one violator never
blocks an unrelated apply), while an already-present violator records a
`GovernanceViolation` Warning Event and keeps serving. `enforcement: audit` only
ever warns (a roll-out mode).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `…extraContext.governance.enabled` | bool | `true` with the default library set | Master switch for the guardrails. The governance library itself declares `false`; the haptic-annotations library (on by default) merges after it and raises it to `true` so its shipped `haptic-compress-enable` rule applies. Set it to `false` explicitly to switch the engine off |
| `…extraContext.governance.exemptNamespaces` | list | `[]` | Namespaces skipped entirely (infra/system) |
| `…extraContext.governance.rules` | map | `{}` | Admin-declared rules keyed by rule name (see the fields below). A map, not a list, so your rules merge with — rather than replace — the ones the chart ships |

Each value of `rules` is an object:

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | bool | **Required.** `true` applies the rule, `false` switches it off. Required rather than defaulted, so a typo fails the render instead of leaving the rule silently inert |
| `resource` | string | **Required.** Watched-resource name the rule targets (`ingresses`, `httproutes`, a custom CRD, …) |
| `path` | string | Concrete JSONPath into the resource: dotted keys, `['bracket']` keys (for dots/slashes), `[n]` indices. For example, `metadata.annotations['haproxy-haptic.org/rate-limit-rps']` |
| `default` | string | Inject this value when `path` is absent. Only a **concrete** `path` may carry a default (filtered/wildcard paths are validate-only) |
| `required` | bool | The value at `path` must be present and non-empty |
| `min` / `max` | int | Numeric bounds for the value at `path` |
| `onViolation` | string | `reject` (default) or `clamp` — on a `min`/`max` violation, rewrite the value to the nearest bound instead of rejecting |
| `allowed` | list | Allowed values (enum) for `path` |
| `pattern` | string | Regex the value at `path` must match |
| `anyOf` | list | At least one of the listed JSONPath expressions must be present |
| `satisfiedBy` | string | `tls` — satisfied by `spec.tls` on the resource **or** the chart-wide default HTTPS |
| `enforcement` | string | `reject` (default) or `audit` |
| `message` | string | Custom violation message (optional) |

```yaml
governance:
  enabled: true
  exemptNamespaces: [kube-system]
  rules:
    # Inject a default per-source rate limit; clamp anything above the ceiling.
    rate-limit-floor:
      enabled: true
      resource: ingresses
      path: metadata.annotations['haproxy-haptic.org/rate-limit-rps']
      default: "100"
      max: 10000
      onViolation: clamp
    # Require a WAF policy annotation on every HTTPRoute (cross-resource).
    httproute-waf-policy:
      enabled: true
      resource: httproutes
      path: metadata.annotations['haproxy-haptic.org/waf-policy']
      required: true
      enforcement: audit
    # Require TLS (spec.tls or the chart-wide default HTTPS satisfies it).
    ingress-tls:
      enabled: true
      resource: ingresses
      satisfiedBy: tls
```

To switch off a single rule — including one a template library ships — set its
`enabled` to `false`. You don't restate the others:

```yaml
governance:
  rules:
    haptic-compress-enable:
      enabled: false
```

## Routing behavior

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.routing.regexMatchOrder` | string | `default` | Path matching order: `default` (Exact > Regex > Prefix-exact > Prefix) or `last` (Exact > Prefix-exact > Prefix > Regex, performance-first) |

## PROXY protocol

For HAProxy behind a layer-4 load balancer. See [PROXY protocol](haproxy-deployment.md#proxy-protocol).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.proxyProtocol.enabled` | bool | `false` | Add HTTP and HTTPS binds that require a PROXY protocol header, so `client_ip`, `src`-keyed rate limiting, the WAF, and IP ACLs see the real client instead of the balancer. Adds the ports to the HAProxy Service, the container ports, and the NetworkPolicy |
| `controller.config.templatingSettings.extraContext.proxyProtocol.httpPort` | int | `8081` | PROXY-protocol HTTP port. Additional to `haproxy.ports.http`, which stays open and header-free — a connection reaching this port without the header is dropped, so only the balancer may target it |
| `controller.config.templatingSettings.extraContext.proxyProtocol.httpsPort` | int | `8444` | PROXY-protocol HTTPS port, using the same certificates, ciphers, and protocol negotiation as `haproxy.ports.https`. With TLS-Passthrough configured it attaches to the SNI-routing frontend instead |

## Default SSL certificate

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `defaultSSLCertificate.enabled` | bool | `true` | Enable default SSL certificate requirement. When the cert-manager API is absent and no inline cert is set, the chart generates a self-signed Secret (never touching an existing one) so the install converges out of the box |
| `defaultSSLCertificate.secretName` | string | `default-ssl-cert` | TLS Secret name containing certificate |
| `defaultSSLCertificate.namespace` | string | `""` | Secret namespace (defaults to `Release.Namespace`) |
| `defaultSSLCertificate.ecdsaSecretName` | string | `""` | Optional ECDSA companion Secret for the default cert. When set, HAProxy serves ECDSA to modern clients and `secretName` (RSA) to the rest on the no-SNI / unmatched-SNI path. Must be in the same namespace. Empty = single default cert. See [Dual RSA and ECDSA certificates](ssl-certificates.md#default-certificate) |
| `defaultSSLCertificate.certManager.enabled` | bool | `true` | Use cert-manager for certificate provisioning |
| `defaultSSLCertificate.certManager.createIssuer` | bool | `true` | Create self-signed Issuer (dev/test only) |
| `defaultSSLCertificate.certManager.dnsNames` | list | `["localdev.me", "*.localdev.me"]` | DNS names for the certificate |
| `defaultSSLCertificate.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `defaultSSLCertificate.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `defaultSSLCertificate.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `defaultSSLCertificate.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `defaultSSLCertificate.create` | bool | `false` | Create Secret from inline cert/key (testing only). Requires `defaultSSLCertificate.certManager.enabled=false` so exactly one actor owns the Secret |
| `defaultSSLCertificate.cert` | string | `""` | PEM certificate (when create=true) |
| `defaultSSLCertificate.key` | string | `""` | PEM private key (when create=true) |
| `controller.config.templatingSettings.extraContext.tls.sessionTickets.enabled` | bool | `false` | Enable fleet-wide TLS session resumption. Every HAProxy pod shares one session-ticket encryption key (STEK) so a ticket issued by any pod resumes on any other (TLS 1.2 and 1.3); the key self-rotates daily through a 3-key sliding window with one hitless reload. See [TLS session resumption](ssl-certificates.md#tls-session-resumption) |

## Controller Config

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.credentialsSecretRef.name` | string | Auto-generated | Secret containing the agent credentials |
| `controller.config.credentialsSecretRef.namespace` | string | `""` | Credentials secret namespace |
| `controller.config.podSelector.matchLabels` | map | `{app.kubernetes.io/component: loadbalancer}` | Labels to match HAProxy pods |

## Leader election

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.controller.leaderElection.enabled` | bool | `true` | Enable leader election (recommended for HA) |
| `controller.config.controller.leaderElection.leaseName` | string | `""` | Lease resource name (defaults to release `fullname`) |
| `controller.config.controller.leaderElection.leaseDuration` | duration | `30s` | Failover timeout duration |
| `controller.config.controller.leaderElection.renewDeadline` | duration | `20s` | Leader renewal timeout |
| `controller.config.controller.leaderElection.retryPeriod` | duration | `5s` | Retry interval between attempts |
| `controller.config.controller.renderGateInterval` | duration | `1s` | Shortest start-to-start spacing of the render gate's `haproxy -c` runs. The gate validates each render off the reconcile path, on a semaphore slot of its own, so this only caps how much CPU a render storm can take from the admission webhook |

## Agent Configuration

The CRD block is still called `dataplane` — it configures the endpoint the
controller applies to, which is now the HAPTIC agent. Four of its fields changed
meaning with the agent; the paths didn't.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.dataplane.minDeploymentInterval` | duration | `5s` | Shortest interval between two reloads of one pod. The chart passes it to the agent as `--reload-interval-min`: a reload inside the window is scheduled, never dropped, and the controller polls the pod at the scheduled time. With `haproxy.enabled=true` the agent's 60-second ceiling applies, and the chart fails the render above it |
| `controller.config.dataplane.driftPreventionInterval` | duration | `60s` | How often the controller asks each pod to re-hash its tree (`GET /v1/state?verify=1`) and re-applies when a digest disagrees. The same call carries the newest validated plan, so a pod's rollback baseline never lags by more than one interval |
| `controller.config.dataplane.reloadVerificationTimeout` | duration | `60s` | How long the agent waits for HAProxy's master to report a reload finished before calling it failed and restoring the last known good file set. The chart passes it to the agent as `--reload-timeout`; unset, the agent uses its 60-second ceiling |
| `controller.config.dataplane.syncTimeout` | duration | `2m` | How long the controller waits for one pod to answer an apply |
| `controller.config.dataplane.mapsDir` | string | `/etc/haproxy/maps` | HAProxy maps directory. With the bundled fleet (`haproxy.enabled=true`) it must sit directly under `/etc/haproxy`, which is where the pod mounts its config volume and resolves every auxiliary path |
| `controller.config.dataplane.sslCertsDir` | string | `/etc/haproxy/ssl` | SSL certificates directory. Same `/etc/haproxy` constraint as `mapsDir` when the bundled fleet is enabled; the directory name itself is free |
| `controller.config.dataplane.generalStorageDir` | string | `/etc/haproxy/general` | General storage directory. With the bundled fleet this exact path is required: it's a separate volume the spoa-hub and vector sidecars mount to read rendered files without reaching SSL private keys. The chart fails the render rather than deploy a pod where those sidecars see an empty directory |
| `controller.config.dataplane.configFile` | string | `/etc/haproxy/haproxy.cfg` | HAProxy config file path. Same `/etc/haproxy` constraint as `mapsDir` when the bundled fleet is enabled |

## Watched Resources

`controller.config.watchedResources.<name>` is a map of resource entries. The chart's template libraries contribute most entries (Ingress, Service, EndpointSlice, Secret, plus the Gateway API route kinds when the gateway library is on); operators can add or override entries here. Each entry accepts:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiVersion` | string | one of `apiVersion`/`apiVersions` required | API group/version (for example `networking.k8s.io/v1`, `v1` for core). Mutually exclusive with `apiVersions` |
| `apiVersions` | list | `[]` | Ordered candidate API versions; the controller watches the first one the apiserver serves (for example `["gateway.networking.k8s.io/v1", "gateway.networking.k8s.io/v1alpha2"]`). Mutually exclusive with `apiVersion` |
| `optional` | bool | `false` | Marks the resource non-essential: when no candidate version is served, the watch is dropped and every `templateSnippet`/`validationTest` naming it in `requires` is stripped from the effective config, instead of failing startup |
| `resources` | string | required | Plural resource name (for example `ingresses`) |
| `indexBy` | list | `[]` | JSONPath expressions used to index resources for O(1) template lookup |
| `fieldSelector` | string | `""` | Client-side JSONPath filter (for example `spec.ingressClassName=haptic`); supports any JSONPath expression unlike Kubernetes' built-in `fieldSelector` |
| `labelSelector` | string | `""` | Server-side label selector for watch-time filtering (equality-only `key=value` pairs joined by commas) |
| `enableValidationWebhook` | bool | `false` | Include this resource in the chart-rendered `ValidatingWebhookConfiguration` |
| `statusPatch` | bool | `false` | Allow the controller to patch this resource's `/status` subresource |
| `store` | string | `full` | `full` keeps all resources in memory; `on-demand` fetches with caching (lower memory, slower lookups). Useful for very large Secret stores |
| `debounceInterval` | duration | `""` (`100ms`) | Per-resource debounce window; empty/unparseable falls back to the controller-wide default (`DefaultDebounceInterval`, `100ms`). Avoid raising the value for resources that drive backend membership — `EndpointSlices` and `pods` in particular — because the debounce delays Pod removal from the HAProxy server pool by that whole window, so live traffic continues hitting Terminating pods until the next render fires |

## Logging and templating

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.logLevel` | string | `INFO` | Initial log level (`LOG_LEVEL` env var): `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR` (case-insensitive) |
| `controller.config.logging.level` | string | `""` | Log level in the `HAProxyTemplateConfig` CRD (`spec.logging.level`); overrides `controller.logLevel` at runtime when non-empty |
| `controller.config.templatingSettings.engine` | string | `scriggo` | Template engine used for rendering; `scriggo` is the only supported value |
| `controller.config.templatingSettings.extraContext.diagnostics.routingHeaders.enabled` | bool | `false` | Opt-in `X-Backend-Name` and `X-Match-Type` response headers for routing diagnostics |
| `controller.config.templatingSettings.extraContext.tracing.enabled` | bool | `false` | Own W3C Trace Context in HAProxy itself, so it works with the spoa-hub disabled (the hub has no part in tracing). A valid inbound `traceparent` is adopted (same trace, its span becomes the parent, its sampling decision honoured); otherwise a trace id and span id are minted. `traceparent` is then set on the request **before** it reaches the backend, so your services join the same trace. The access log gains the fields the spans are built from: `span_id`, `parent_span_id`, `trace_flags`, `upstream_span_id`, the `route` that matched, the `handshake`/`idle`/`transfer` timings and the exact transaction end. They're removed from the rendered configuration entirely when tracing is off. Off by default because it changes what backends receive and there is no sensible default destination for traces. With it off, an inbound trace id is still adopted into `trace_id` for log correlation, but nothing is minted or propagated |
| `controller.config.templatingSettings.extraContext.tracing.sampleRate` | int | `100` | Percentage of requests sampled when HAPTIC is the trace **root** (0–100). An inbound `traceparent`'s decision is always honoured instead — re-deciding mid-trace produces half-sampled traces. A request that isn't sampled still propagates `traceparent` with flags `00`, so downstream honours the same decision rather than starting its own |
| `controller.config.templatingSettings.extraContext.tracing.otlp.endpoint` | string | `""` | OpenTelemetry Protocol (OTLP) HTTP traces endpoint, for example `http://tempo.observability.svc.cluster.local:4318/v1/traces`. Empty means propagation-only: HAProxy still sets `traceparent` so backends join the trace, HAPTIC just contributes no span. Spans are derived from **access-log records** in the Vector sidecar, so coverage is whatever HAProxy logs — which is everything: a 502/503/504, a WAF deny, a rate-limit 429, a redirect, an error page and a fixed response are all generated by HAProxy itself, and each produces a log record and therefore a span. Requires `vector.enabled=true`; the render fails otherwise rather than exporting nothing silently. Spans carry the same detail as the access log: HTTP and TLS metadata, the phase timings, and the HAPTIC decision fields (WAF, external auth, rate limit, schema validation, cache, mTLS), each named `haptic.` + its log-field name. Spans carry no client IP — neither the forwarded address nor the TCP peer — so correlate a trace with the access log through `haptic.req_id`. Client-side timings stay on the `SERVER` span; the chosen server, retries and queue wait are on the upstream `CLIENT` span. When no endpoint is set the span-building transform is dropped from the configuration entirely. Spans are named `{method} {host}{route}` after the matched route template — never the request URI, which would give every request a distinct span name. A prefix match is marked `*` (`GET example.com/api/*`); an exact match isn't (`GET example.com/api`). The host is included because most deployments serve every Ingress under a single `/` prefix, where the route alone names every span `GET /`; this deviates from the OpenTelemetry convention of `{method} {http.route}`, but the `http.route` attribute itself is unchanged and carries the path template without the host. With no route matched the name falls back to `{method} [namespace/name]`, and to `{method}` alone when there is no owning resource either |
| `controller.config.templatingSettings.extraContext.tracing.otlp.serviceName` | string | `haptic` | `service.name` resource attribute reported for HAPTIC's own spans |
| `controller.config.templatingSettings.extraContext.tracing.otlp.clusterName` | string | `""` | Cluster name reported as the `k8s.cluster.name` resource attribute on every exported span. No default: Kubernetes exposes no cluster name to a pod, so the attribute is omitted unless you set it. `service.namespace`, `service.version` and `k8s.deployment.name` need no setting — they come from the release, and identify which HAPTIC emitted a span when a cluster runs several |
| `controller.config.templatingSettings.extraContext.accessLog.fields` | map | `{}` | Extra JSON access-log fields: field name → one HAProxy sample expression, captured at request time and logged as a string. Use `str(<value>)` for a constant label. Names must match `^[A-Za-z_][A-Za-z0-9_]{0,39}$` and must not collide with a built-in field; expressions must not contain whitespace, `#`, `"` or a backslash. See [Access logging](haproxy-deployment.md#access-logging) |
| `controller.config.templatingSettings.extraContext.accessLog.targets` | map | `{vector: {address: /run/vector/haproxy.sock, format: raw}}` while `vector.enabled` is true (the chart default); `{stdout: {address: stdout}}` otherwise | Where access-log records go, keyed by a target name you choose; one HAProxy `log` line per entry, emitted in sorted key order, so several entries fan out. A map, not a list, so adding a target keeps the ones already configured. Each entry takes `address` (`stdout`, `stderr`, `fd@<n>`, `<host>:<port>` (UDP), an absolute socket path or `ring@<name>`), `format` (defaults to `raw` for stdout/stderr, `rfc5424` otherwise), `facility`, `level` (`info` or `debug` — anything stricter drops every record), or a `ring` block (`name`, `address`, `size`, `logProto`, `connectTimeout`, `serverTimeout`, `serverOptions`) for a buffered TCP client that survives a collector restart. HAProxy's own process messages keep their own stdout target. See [Where the logs go](haproxy-deployment.md#where-the-logs-go) |
| `controller.config.templatingSettings.extraContext.accessLog.maxLineBytes` | int | `16384` | `log ... len <bytes>`. HAProxy truncates a longer record mid-byte, which makes it invalid JSON; raise it if custom fields or captured request headers push records past the limit (1024–65535) |
| `controller.config.templatingSettings.extraContext.accessLog.suppress.successful` | bool | `false` | Opt-in: drop access-log records for 2xx/3xx requests that no gate denied. Denials, 4xx, and 5xx are always kept. Off by default — retaining a full log is lawful under legitimate interest (GDPR Art. 6(1)(f)), and the successful requests either side of a failure are what make a customer's report diagnosable. A record is ~740 bytes, so ~700 MB per million requests if volume forces your hand. See [Access logging](haproxy-deployment.md#access-logging) |
| `controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.regex` | string | `"^.*$"` | Regex every password hash in a basic-auth Secret must match (the `auth-secret` annotation handlers in the haproxytech and haproxy-ingress libraries). A non-matching hash fails the render with `passwordHashValidation.errorMessage`; the default accepts all hashes. Example restricting to MD5-crypt (apr1) hashes: `"^\$apr1\$"`. Go RE2 syntax — no lookaheads, so express the policy as the *allowed* format |
| `controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.errorMessage` | string | `Invalid password hash` | Error message emitted when a password hash fails validation; the rendered error appends the username, Secret name, and pattern |
| `controller.config.templatingSettings.extraContext.tls.hsts.enabled` | bool | `false` | Emit a global `Strict-Transport-Security` header on TLS responses. Opt-in; per-Ingress HSTS annotations still win |
| `controller.config.templatingSettings.extraContext.tls.hsts.maxAge` | string | `"31536000"` | HSTS `max-age` in seconds for the global header |
| `controller.config.templatingSettings.extraContext.tls.hsts.includeSubdomains` | bool | `false` | Add `includeSubDomains` to the global HSTS header |
| `controller.config.templatingSettings.extraContext.tls.hsts.preload` | bool | `false` | Add `preload` to the global HSTS header |
| `controller.config.watchedResourcesIgnoreFields` | list | `[metadata.managedFields, metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']]` | Fields to ignore in watched resources |

## Prometheus exporter

HAProxy's built-in Prometheus exporter answers on the `stats` port (`8404`, `/metrics`) on every HAProxy pod and is scraped directly. These values become the query HAProxy applies to a scrape that sends none, so every scraper — the bundled `haproxy.monitoring.podMonitor` and a hand-written job alike — gets the same exposition; a scraper's own query wins wholesale, so `/metrics?` returns the raw exposition. See [HAProxy data-plane metrics](operations/monitoring.md#haproxy-data-plane-metrics).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.prometheusExporter.excludeMaintServers` | bool | `true` | Apply HAProxy's `?no-maint`, omitting servers in `MAINT` state. A not-ready or terminating endpoint renders as a `disabled` server, which HAProxy reports in `MAINT`, and each still emits a full metric set of zeros; `?no-maint` drops those series. How much that saves depends on the deployment — most during a rolling update, when many pods are briefly not-ready or terminating, and little at steady state. No metric **name** disappears, so name-selecting dashboards and rules keep working, and `haproxy_backend_agg_server_status{state="MAINT"}` still reports the per-backend count of `MAINT` servers (which is why `haproxy_backend_agg_*` isn't in `excludeMetrics` by default). Set to `false` if you alert on an individual drained server. Honoured on HAProxy 3.0–3.4 |
| `controller.config.templatingSettings.extraContext.prometheusExporter.excludeMetrics` | map | eight exclusions, seven on | Named exclusions, each with `enabled` and `families` — **exact** metric names HAProxy leaves out of the exposition (`metrics=-<name>`; the exporter has no regex filter, and a name that isn't a bare metric name is refused at render time). A map rather than a list so you can disable one without restating the others: `prometheusExporter.excludeMetrics.backendAggCheckStatus.enabled=false`. Add your own with any new key; `enabled` is required so an entry can't sit inert. An entry may set `requires`, a dotted `extraContext` path that must be truthy for the exclusion to apply. The defaults drop HAProxy's host-computed maxima, the `agg_check_status` family (reproducible by summing `haproxy_server_check_status`), `agg_server_check_status` (identical to `agg_server_status` unless you use agent checks), and the cache and backup-topology families, which are constant unless you enable those features — together about a third of the exposition on a large fleet. The compression exclusion ships **off**, because response compression is on by default and those series carry real traffic. Two further groups carry `requires: vector.requestMetrics.enabled` and so drop out on their own if you turn that feature off: `haproxy_*_time_average_seconds` (1024-connection rolling averages, superseded by real duration histograms) and `haproxy_{backend,frontend}_http_{requests,responses}_total` (superseded by `<prefix>_requests`, which carries the exact status code plus route, method, host and service). `haproxy_backend_agg_server_status` and `haproxy_server_http_responses_total` are deliberately **kept** — the first is the only remaining per-backend census of `MAINT` (not-ready/terminating) servers once `excludeMaintServers` is on, and the second is per-server, a dimension the request metrics don't carry |

## Webhook Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.webhook.enabled` | bool | `true` | Enable admission webhook validation |
| `controller.webhook.timeoutSeconds` | int | `10` | API-server timeout for watched-resource admission. HAPTIC configures the controller deadline one second shorter. Allowed range: `2..30` |
| `controller.webhook.secretName` | string | Auto-generated | Webhook TLS certificate secret name |
| `controller.webhook.service.port` | int | `443` | Webhook service port |
| `controller.webhook.certManager.enabled` | bool | `false` | cert-manager integration for the webhook cert. Default `false`: the chart issues a self-signed cert itself. Set `true` for cert-manager-managed issuance and auto-rotation; for manual certs keep `false` and set `controller.webhook.caBundle` |
| `controller.webhook.certManager.createIssuer` | bool | `true` | Create a self-signed Issuer for webhook certs |
| `controller.webhook.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `controller.webhook.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `controller.webhook.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `controller.webhook.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `controller.webhook.selfSigned.certValidityDays` | int | `3650` | Validity in days of the chart-generated self-signed webhook cert (used when `certManager.enabled=false` and `caBundle` is empty). Long by default because the chart doesn't auto-rotate it: the cert is generated once and reused across upgrades via `lookup`. Rotate manually by deleting the Secret (`controller.webhook.secretName`) and running `helm upgrade`, or use cert-manager for automatic rotation |
| `controller.webhook.caBundle` | string | `""` | Base64-encoded CA bundle (manual certs) |

## Pluggable Validators

The validator sidecar runs a second `haproxy-spoa-hub` instance in `--validate-socket` mode next to the controller. The shared render pipeline consults it before publishing or deploying output, so broken plugin TOML (for example a bad `modsecurity-snippet`) is rejected regardless of whether a watched resource, config, HTTP refresh, or drift check triggered the render. See [Pluggable validators](./operations/pluggable-validators.md).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.validators.enabled` | bool/null | `null` | Master enable for the validator sidecar. `null` auto-derives from the SPOA hub sidecar's own enable; `true` renders it even when `spoaHub` is off; `false` forces it off |
| `controller.validators.socketDir` | string | `/var/run/haptic-validators` | Directory for the validator Unix socket — a shared emptyDir mounted into both the controller container and the validator sidecar |
| `controller.validators.socketName` | string | `spoa-hub.sock` | Socket filename. The controller dials `<socketDir>/<socketName>`, and the chart writes that path into the auto-wired `spec.validators` entry |
| `controller.validators.resources.requests.cpu` | string | `25m` | Validator sidecar CPU request; content caching avoids repeat validation for identical renders |
| `controller.validators.resources.requests.memory` | string | `64Mi` | Validator sidecar memory request |
| `controller.validators.resources.limits.memory` | string | `128Mi` | Validator sidecar memory limit |
| `controller.validators.securityContext` | map | See values.yaml | Container security context for the validator sidecar. Default runs user and group 65532 (matching the controller's nonroot user) so the Unix socket is readable and writable by the controller without extra `fsGroup` plumbing; read-only root filesystem, no privilege escalation, all capabilities dropped |
| `controller.validators.extraVolumeMounts` | list | `[]` | Extra volume mounts added to the validator sidecar only (rendered through `tpl`); for auxiliary data a plugin's `validate()` needs, such as MaxMind MMDB files or Open Worldwide Application Security Project (OWASP) Core Rule Set (CRS) files. Same shape as `spoaHub.extraVolumeMounts` |
| `controller.validators.entries` | list | `[]` | Entries appended to the CRD's `spec.validators` (each: `name`, `socketPath`, `files` glob list, optional `timeoutMs`/`maxConnections`). The chart auto-appends a `spoa-hub` entry validating `general/spoa-hub-config.toml` when both the SPOA hub sidecar and the validator sidecar are enabled; an operator entry named `spoa-hub` takes precedence |

## IngressClass

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `ingressClass.enabled` | bool | `true` | Create IngressClass resource |
| `ingressClass.name` | string | `haptic` | IngressClass name; default avoids conflict with other HAProxy-based ingress controllers (override to `haproxy` when replacing one) |
| `ingressClass.default` | bool | `false` | Mark as default IngressClass |
| `ingressClass.controllerName` | string | `haproxy-haptic.org/controller` | Controller identifier |

## GatewayClass

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `gatewayClass.enabled` | bool | `true` | Create GatewayClass resource |
| `gatewayClass.name` | string | `haptic` | GatewayClass name; default matches `ingressClass.name` |
| `gatewayClass.default` | bool | `false` | Mark as default GatewayClass |
| `gatewayClass.controllerName` | string | `haproxy-haptic.org/controller` | Controller identifier |
| `gatewayClass.parametersRef.group` | string | `haproxy-haptic.org` | HAProxyTemplateConfig API group |
| `gatewayClass.parametersRef.kind` | string | `HAProxyTemplateConfig` | HAProxyTemplateConfig kind |
| `gatewayClass.parametersRef.name` | string | `""` | Config name (defaults to `controller.configName`) |
| `gatewayClass.parametersRef.namespace` | string | `""` | Config namespace (defaults to `Release.Namespace`) |

## Credentials

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `credentials.dataplane.username` | string | `admin` | Agent username. The Secret keys keep their `dataplane_` names, so a rotation set up before the agent still works |
| `credentials.dataplane.password` | string | `""` | Agent password. Empty generates a random 32-char password. When `lookup` works (a normal `helm upgrade`, or an install against a reachable cluster) the chart reads the existing Secret and preserves the current password across renders. GitOps tools that render without cluster access (ArgoCD/Flux) can't `lookup`, so an empty value regenerates every sync — **set an explicit value** (SealedSecret / external secret) in those setups. |

## ServiceAccount & RBAC

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.serviceAccount.create` | bool | `true` | Create ServiceAccount |
| `controller.serviceAccount.automount` | bool | `true` | Automount API credentials |
| `controller.serviceAccount.annotations` | map | `{}` | ServiceAccount annotations |
| `controller.serviceAccount.name` | string | `""` | ServiceAccount name (auto-generated if empty) |
| `controller.rbac.create` | bool | `true` | Create RBAC resources |

## Pod configuration (controller)

Pod-spec scheduling, runtime, and metadata fields for the controller Deployment live under `controller.podSpec.*`. The chart's `_pod-spec.tpl` helper renders the universally shared subset; the remaining fields (`podAnnotations`, `podLabels`, `podSecurityContext`) are consumed directly by `templates/deployment.yaml`.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.podSpec.imagePullSecrets` | list | `[]` | Image pull secrets for private registries |
| `controller.podSpec.podAnnotations` | map | `{}` | Pod annotations |
| `controller.podSpec.podLabels` | map | `{}` | Additional pod labels |
| `controller.podSpec.priorityClassName` | string | `""` | Pod priority class name |
| `controller.podSpec.runtimeClassName` | string | `""` | Runtime class (for example gVisor, Kata) |
| `controller.podSpec.terminationGracePeriodSeconds` | int | `30` | Termination grace period |
| `controller.podSpec.dnsPolicy` | string | `ClusterFirst` | DNS policy |
| `controller.podSpec.dnsConfig` | map | `{}` | DNS config |
| `controller.podSpec.hostAliases` | list | `[]` | /etc/hosts entries |
| `controller.podSpec.topologySpreadConstraints` | list | `[]` | Pod topology spread constraints |
| `controller.podSpec.nodeSelector` | map | `{}` | Node selector |
| `controller.podSpec.tolerations` | list | `[]` | Pod tolerations |
| `controller.podSpec.affinity` | map | `{}` | Pod affinity rules |
| `controller.podSpec.podSecurityContext.runAsNonRoot` | bool | `true` | Run as non-root user |
| `controller.podSpec.podSecurityContext.runAsUser` | int | `65532` | User ID |
| `controller.podSpec.podSecurityContext.runAsGroup` | int | `65532` | Group ID |
| `controller.podSpec.podSecurityContext.fsGroup` | int | `65532` | Filesystem group ID |
| `controller.podSpec.podSecurityContext.seccompProfile.type` | string | `RuntimeDefault` | Seccomp profile type |

## Container security context

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.securityContext.allowPrivilegeEscalation` | bool | `false` | Allow privilege escalation |
| `controller.securityContext.capabilities.drop` | list | `[ALL]` | Dropped capabilities |
| `controller.securityContext.readOnlyRootFilesystem` | bool | `true` | Read-only root filesystem |
| `controller.securityContext.runAsNonRoot` | bool | `true` | Run as non-root |
| `controller.securityContext.runAsUser` | int | `65532` | Container user ID |

## Service & health probes

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.service.type` | string | `ClusterIP` | Controller service type |
| `controller.service.annotations` | map | `{}` | Service annotations (cloud-LB hints, etc.) |
| `controller.service.clusterIP` | string | `""` | Pin a specific ClusterIP; leave empty for auto-assignment |
| `controller.service.loadBalancerIP` | string | `""` | LoadBalancer IP (when `type: LoadBalancer`) |
| `controller.service.loadBalancerSourceRanges` | list | `[]` | CIDR allowlist for LoadBalancer traffic |
| `controller.service.loadBalancerClass` | string | `""` | LoadBalancer class (Kubernetes 1.24+ multi-LB) |
| `controller.service.externalTrafficPolicy` | string | `""` | `Cluster` or `Local`; `Local` preserves client source IP at the cost of uneven distribution |
| `controller.service.internalTrafficPolicy` | string | `""` | `Cluster` or `Local` for in-cluster traffic |
| `controller.service.sessionAffinity` | string | `""` | `None` or `ClientIP` |
| `controller.service.sessionAffinityConfig` | map | `{}` | Session-affinity tuning (when `sessionAffinity: ClientIP`) |
| `controller.livenessProbe.httpGet.path` | string | `/healthz` | Liveness probe path |
| `controller.livenessProbe.httpGet.port` | string | `healthz` | Named container port the probe targets (declared by `controller.ports.healthz`) |
| `controller.livenessProbe.initialDelaySeconds` | int | `10` | Initial delay |
| `controller.livenessProbe.periodSeconds` | int | `10` | Probe period |
| `controller.livenessProbe.failureThreshold` | int | `3` | Failure threshold |
| `controller.readinessProbe.httpGet.path` | string | `/healthz` | Readiness probe path |
| `controller.readinessProbe.httpGet.port` | string | `healthz` | Named container port the probe targets |
| `controller.readinessProbe.initialDelaySeconds` | int | `5` | Initial delay |
| `controller.readinessProbe.periodSeconds` | int | `5` | Probe period |
| `controller.readinessProbe.failureThreshold` | int | `3` | Failure threshold |
| `controller.startupProbe.enabled` | bool | `true` | Enable the startup probe; liveness/readiness probes are paused until it succeeds. On by default and load-bearing: controller startup runs the config's embedded `validationTests` (dozens of `haproxy -c` checks plus a full template compile), which can exceed the bare liveness budget on slow nodes and crash-loop the pod |
| `controller.startupProbe.httpGet.path` | string | `/healthz` | Startup probe path |
| `controller.startupProbe.httpGet.port` | string | `healthz` | Named container port the probe targets |
| `controller.startupProbe.initialDelaySeconds` | int | `0` | Initial delay |
| `controller.startupProbe.periodSeconds` | int | `10` | Probe period |
| `controller.startupProbe.timeoutSeconds` | int | `1` | Probe timeout |
| `controller.startupProbe.successThreshold` | int | `1` | Success threshold |
| `controller.startupProbe.failureThreshold` | int | `30` | Failure threshold (with `periodSeconds: 10` this gives 5 minutes for startup) |

## Resources & scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.resources.requests.cpu` | string | `100m` | CPU request |
| `controller.resources.requests.memory` | string | `1Gi` | Memory request (matches `limits.memory`) |
| `controller.resources.limits.memory` | string | `1Gi` | Memory limit. The floor is the load gate, which runs the bundled `validationTests` on every config load and peaks above 512Mi — see [Controller resource sizing](./operations/performance.md#controller-resource-sizing) |

Pod-level scheduling fields (`nodeSelector`, `tolerations`, `affinity`, etc.) live under `controller.podSpec.*` — see [Pod configuration (controller)](#pod-configuration-controller).

## Controller extras & rollout

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.extraEnv` | list | `[]` | Extra env vars for the controller container; `AUTOMEMLIMIT=…` here adjusts the GOMEMLIMIT ratio (default `0.9`) |
| `controller.extraVolumes` | list | `[]` | Extra volumes for the controller pod; rendered through `tpl` so values can reference chart values |
| `controller.extraVolumeMounts` | list | `[]` | Extra volume mounts for the controller container; rendered through `tpl` |
| `controller.initContainers` | list | `[]` | Init containers run before the controller starts |
| `controller.sidecars` | list | `[]` | Additional sidecar containers in the controller pod; rendered through `tpl` |
| `controller.lifecycle` | map | `{}` | Container lifecycle hooks (`preStop`, `postStart`) for the controller container |
| `controller.updateStrategy.type` | string | `RollingUpdate` | Controller Deployment update strategy |
| `controller.updateStrategy.rollingUpdate.maxSurge` | int/string | `25%` | Maximum surge during rolling updates |
| `controller.updateStrategy.rollingUpdate.maxUnavailable` | int/string | `25%` | Maximum unavailable during rolling updates |
| `controller.minReadySeconds` | int | `0` | Minimum seconds a new controller pod must be ready before counting as available |
| `controller.revisionHistoryLimit` | int | `10` | Number of old ReplicaSets to retain |

## Autoscaling & PDB

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.autoscaling.enabled` | bool | `false` | Enable HorizontalPodAutoscaler |
| `controller.autoscaling.minReplicas` | int | `1` | Minimum replicas |
| `controller.autoscaling.maxReplicas` | int | `10` | Maximum replicas |
| `controller.autoscaling.targetCPUUtilizationPercentage` | int | `80` | Target CPU utilization (omitted from the rendered HPA when empty) |
| `controller.autoscaling.targetMemoryUtilizationPercentage` | int | unset | Target memory utilization (omitted from the rendered HPA when empty) |
| `controller.podDisruptionBudget.enabled` | bool | `true` | Enable PodDisruptionBudget; only rendered when `controller.replicaCount > 1` |
| `controller.podDisruptionBudget.minAvailable` | int/string | `1` | Minimum available pods (mutually exclusive with `maxUnavailable`) |
| `controller.podDisruptionBudget.maxUnavailable` | int/string | unset | Maximum unavailable pods (mutually exclusive with `minAvailable`); leave unset to use `minAvailable` |

## Monitoring

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.monitoring.serviceMonitor.enabled` | bool | `false` | Create ServiceMonitor for Prometheus |
| `controller.monitoring.serviceMonitor.interval` | duration | `30s` | Scrape interval |
| `controller.monitoring.serviceMonitor.scrapeTimeout` | duration | `10s` | Scrape timeout |
| `controller.monitoring.serviceMonitor.labels` | map | `{}` | ServiceMonitor labels (used by Prometheus to select which ServiceMonitors to use) |
| `controller.monitoring.serviceMonitor.relabelings` | list | `[]` | Prometheus relabelings applied before scraping |
| `controller.monitoring.serviceMonitor.metricRelabelings` | list | `[]` | Metric relabelings applied to scraped metrics |
| `controller.monitoring.podMonitor.enabled` | bool | `false` | Create PodMonitor (alternative to ServiceMonitor when scraping pods directly) |
| `controller.monitoring.podMonitor.interval` | duration | `30s` | PodMonitor scrape interval |
| `controller.monitoring.podMonitor.scrapeTimeout` | duration | `10s` | PodMonitor scrape timeout |
| `controller.monitoring.podMonitor.labels` | map | `{}` | PodMonitor labels |
| `controller.monitoring.podMonitor.relabelings` | list | `[]` | PodMonitor relabelings |
| `controller.monitoring.podMonitor.metricRelabelings` | list | `[]` | PodMonitor metric relabelings |
| `controller.monitoring.prometheusRule.enabled` | bool | `false` | Create PrometheusRule with alerting rules |
| `controller.monitoring.prometheusRule.labels` | map | `{}` | PrometheusRule labels |
| `controller.monitoring.prometheusRule.rules` | list | `[]` | Custom alerting rules; overrides the default rule set when non-empty |
| `controller.monitoring.prometheusRule.defaultRules.enabled` | bool | `true` | Emit the chart's default rule set — fifteen alerts, each individually toggleable below; only consulted when `rules` is empty |
| `controller.monitoring.prometheusRule.defaultRules.reconciliationErrors` | bool | `true` | Include the `HAProxyControllerReconciliationErrors` warning rule |
| `controller.monitoring.prometheusRule.defaultRules.deploymentFailures` | bool | `true` | Include the `HAProxyControllerDeploymentFailures` critical rule |
| `controller.monitoring.prometheusRule.defaultRules.highQueueDepth` | bool | `true` | Include the `HAProxyControllerHighQueueDepth` warning rule |
| `controller.monitoring.prometheusRule.defaultRules.leaderElectionLost` | bool | `true` | Include the `HAProxyControllerNoLeader` critical rule |
| `controller.monitoring.prometheusRule.defaultRules.fleetDiverged` | bool | `true` | Include the `HAProxyFleetDiverged` warning rule: some HAProxy pods haven't converged to the desired config for 5 minutes. Transient deploy failures self-heal, so sustained divergence is a real fault — this is the noise-free replacement for alerting on raw deployment errors |
| `controller.monitoring.prometheusRule.defaultRules.configRejected` | bool | `true` | Include the `HAProxyControllerConfigRejected` warning rule: the validation gate rejected a config change — the controller keeps serving the last-good config and the latest change isn't live |
| `controller.monitoring.prometheusRule.defaultRules.configPinned` | bool | `true` | Include the `HAProxyControllerConfigPinned` critical rule: HAProxy refused two renders in a row, so the pods keep serving the last configuration it accepted and nothing new reaches them until the input is fixed |
| `controller.monitoring.prometheusRule.defaultRules.haproxyPodsRejected` | bool | `true` | Include the `HAProxyControllerHAProxyPodsRejected` warning rule: discovered HAProxy pods refused admission (often a HAProxy major.minor mismatch with `haproxyVersion`) |
| `controller.monitoring.prometheusRule.defaultRules.noHAProxyPods` | bool | `true` | Include the `HAProxyControllerNoHAProxyPods` critical rule: the controller finds no HAProxy pods to manage, so no config reaches the data plane |
| `controller.monitoring.prometheusRule.defaultRules.accessLogDropped` | bool | `true` | Include the `HAProxyAccessLogRecordsDropped` warning rule: HAProxy discarded access-log records because the Vector sidecar stopped draining the Unix datagram socket. Traffic is unaffected; the log is incomplete |
| `controller.monitoring.prometheusRule.defaultRules.criticalEventsDropped` | bool | `true` | Include the `HAProxyControllerCriticalEventsDropped` critical rule: a critical event-bus subscriber's buffer overflowed — reconciliation work was lost and the data plane may be stale |
| `controller.monitoring.prometheusRule.defaultRules.applyRejected` | bool | `true` | Include the `HAProxyAgentApplyRejected` warning rule: an HAProxy pod refused an apply and serves its last known good file set; HAProxy's message is in the pod's status condition |
| `controller.monitoring.prometheusRule.defaultRules.agentInvariantViolated` | bool | `true` | Include the `HAProxyAgentInvariantViolated` critical rule: an agent observed one of its own invariants failing — a defect, not an operator error |
| `controller.monitoring.prometheusRule.defaultRules.recoveryReloadFailed` | bool | `true` | Include the `HAProxyAgentRecoveryReloadFailed` critical rule: a pod rolled back to its last known good file set but the recovery reload failed, so its worker matches neither the rejected apply nor the restored files |
| `controller.monitoring.prometheusRule.defaultRules.agentVersionSkew` | bool | `true` | Include the `HAProxyAgentVersionSkew` warning rule: applies degrade to full state plus a reload because a pod's agent doesn't match the controller; expected during a rolling upgrade, a defect after one |
| `controller.monitoring.grafanaDashboard.enabled` | bool | `false` | Create a ConfigMap holding the Grafana dashboard JSON (picked up by the Grafana sidecar via the configured discovery label) |
| `controller.monitoring.grafanaDashboard.labels` | map | `{grafana_dashboard: "1"}` | Discovery labels for the Grafana sidecar |
| `controller.monitoring.grafanaDashboard.annotations` | map | `{grafana_folder: "HAProxy"}` | Annotations on the dashboard ConfigMap; the folder annotation must match `grafana.sidecar.dashboards.folderAnnotation` |
| `controller.monitoring.grafanaDashboard.namespace` | string | `""` | Namespace for the dashboard ConfigMap (defaults to release namespace) |
| `controller.monitoring.grafanaDashboard.useBuiltIn` | bool | `true` | Use the built-in dashboard (curated subset of controller metrics); set `false` to provide your own JSON via `customDashboard` |
| `controller.monitoring.grafanaDashboard.customDashboard` | map | `{}` | Custom dashboard JSON, only consulted when `useBuiltIn: false` |

## HAProxy Deployment

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.enabled` | bool | `true` | Deploy HAProxy pods with this chart |
| `haproxy.replicaCount` | int | `2` | Number of HAProxy replicas |
| `haproxy.podDisruptionBudget` | object | enabled `true`, `maxUnavailable` derived | Preserve at least one HAProxy pod during voluntary disruptions. The default is `0` for a one-pod minimum fleet and `1` otherwise. An explicit value must be smaller than the static replica count, or KEDA's minimum replica count when autoscaling is enabled |
| `haproxy.image.repository` | string | `""` (derived) | HAProxy image repository. Empty selects `haproxytech/haproxy-debian` or `hapee-registry.haproxy.com/haproxy-enterprise` from `haproxy.enterprise.enabled` |
| `haproxy.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `haproxy.image.tag` | string | `""` | HAProxy image tag; empty = derive from `haproxyVersion` plus the matching entry in `haproxyPatchVersions` (for example `3.2` → whichever 3.2.x patch the chart currently pins). Override to pin a specific patch yourself. |
| `haproxy.enterprise.enabled` | bool | `false` | Use HAProxy Enterprise. `haproxyVersion` selects the compatibility series, image revision map, and binary path together |
| `haproxy.haproxyBin` | string | Auto-detected | HAProxy binary path |
| `haproxy.initialConfig` | string | See values.yaml | HAProxy bootstrap config served until the controller pushes the first rendered config; processed via Helm `tpl`. Keep the `/ready` 503 gate or clients hit an empty backend set — see the [HAProxy deployment guide](./haproxy-deployment.md) |

## HAProxy Pod Configuration

Pod-spec scheduling, runtime, and metadata fields live under `haproxy.podSpec.*` (the chart's `_pod-spec.tpl` helper renders the universally shared subset). See also `controller.podSpec.*` for the controller Deployment.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.podSpec.imagePullSecrets` | list | `[]` | Image pull secrets for the HAProxy pod, which pulls both the HAProxy image and the HAPTIC image its agent container runs from. Empty follows `controller.podSpec.imagePullSecrets` |
| `haproxy.podSpec.podAnnotations` | map | `{}` | Extra pod annotations for HAProxy pods (supports template expressions) |
| `haproxy.podSpec.shareProcessNamespace` | bool | `false` | Share process namespace between containers (required for signal-based sidecar reload) |
| `haproxy.podSpec.priorityClassName` | string | `""` | Pod priority class |
| `haproxy.podSpec.terminationGracePeriodSeconds` | int | `30` | Termination grace period |
| `haproxy.podSpec.dnsPolicy` | string | `ClusterFirst` | DNS policy |
| `haproxy.podSpec.dnsConfig` | map | `{}` | DNS config |
| `haproxy.podSpec.hostAliases` | list | `[]` | /etc/hosts entries |
| `haproxy.podSpec.runtimeClassName` | string | `""` | Runtime class (for example gVisor, Kata) |
| `haproxy.podSpec.topologySpreadConstraints` | list | `[]` | Topology spread constraints |
| `haproxy.podSpec.nodeSelector` | map | `{}` | Node selector |
| `haproxy.podSpec.tolerations` | list | `[]` | Tolerations |
| `haproxy.podSpec.affinity` | map | `{}` | Affinity rules |
| `haproxy.podSpec.podSecurityContext` | map | See values.yaml | Pod-level security context (seccomp, sysctls). UIDs auto-derived from `haproxy.enterprise.enabled` |
| `haproxy.sidecars` | list | `[]` | Additional sidecar containers for HAProxy pod |
| `haproxy.initContainers` | list | `[]` | Init containers for HAProxy pod |
| `haproxy.extraVolumes` | list | `[]` | Extra volumes for HAProxy pod |
| `haproxy.extraVolumeMounts` | list | `[]` | Extra volume mounts for HAProxy container |
| `haproxy.extraEnv` | list | `[]` | Extra env vars for the HAProxy container |

## HAProxy ports

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.ports.http` | int | `80` | HTTP frontend container port |
| `haproxy.ports.https` | int | `443` | HTTPS frontend container port |
| `haproxy.ports.stats` | int | `8404` | Stats/health page port |
| `haproxy.ports.dataplane` | int | `5555` | Single source of truth for the agent's apply/state API: its listener, the Service, the NetworkPolicy, the container probes, and the controller's connection port |
| `haproxy.ports.agentMetrics` | int | `5557` | The agent's Prometheus endpoint. Scraped by `haproxy.monitoring.podMonitor` through the named container port `agent-metrics`, and allowed from the NetworkPolicy's metrics sources |

## HAProxy Service

The controller renders the user-facing HAProxy Service from these values (the base library's `k8sResources.haproxy-service` template) and owns it via Server-Side Apply — the chart itself only creates the internal agent Service. Changes therefore land when the controller reconciles, not at `helm upgrade` time.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.service.type` | string | `NodePort` | HAProxy service type |
| `haproxy.service.annotations` | map | `{}` | Service annotations |
| `haproxy.service.loadBalancerIP` | string | `""` | LoadBalancer IP (when `type: LoadBalancer`) |
| `haproxy.service.loadBalancerSourceRanges` | list | `[]` | CIDR allowlist for LoadBalancer traffic |
| `haproxy.service.loadBalancerClass` | string | `""` | LoadBalancer class (Kubernetes 1.24+ multi-LB) |
| `haproxy.service.externalTrafficPolicy` | string | `""` | `Cluster` or `Local`; `Local` preserves client source IP at the cost of uneven distribution |
| `haproxy.service.internalTrafficPolicy` | string | `""` | `Cluster` or `Local` for in-cluster traffic |
| `haproxy.service.healthCheckNodePort` | int | `""` | Fixed health-check NodePort for `type: LoadBalancer` with `externalTrafficPolicy: Local`; empty lets Kubernetes allocate one |
| `haproxy.service.publishNotReadyAddresses` | bool | `false` | Include not-ready HAProxy pods in the Service endpoints |
| `haproxy.service.http.port` | int | `80` | HTTP service port |
| `haproxy.service.http.nodePort` | int | `30080` | HTTP NodePort |
| `haproxy.service.https.port` | int | `443` | HTTPS service port |
| `haproxy.service.https.nodePort` | int | `30443` | HTTPS NodePort |
| `haproxy.service.stats.port` | int | `8404` | Stats service port |
| `haproxy.service.stats.nodePort` | int | `30404` | Stats NodePort |
| `haproxy.service.extraPorts` | list | `[]` | Additional Service ports (`corev1.ServicePort` shape) appended to the http/https/stats entries — for example a raw TCP frontend declared via a custom `haproxyConfig` snippet. Drop a default entry by setting `haproxy.service.{http,https,stats}.port: 0` |

## HAPTIC agent container

The agent owns the HAProxy pod's file tree and its runtime sockets. It runs the
controller's own image, so its version always matches the controller that talks
to it.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.agent.service.type` | string | `ClusterIP` | Type of the internal Service that fronts the agent port |
| `haproxy.agent.logLevel` | string | `info` | Log level for the agent, which logs JSON on stdout: `trace`, `debug`, `info`, `warning`, `error`. At `debug` the agent logs a line per apply with its verdict, the ops it ran and the reload it performed — raise it when diagnosing an apply the controller reports as failing but HAProxy accepts. The stream carries no end-user data; the only client is the controller |
| `haproxy.agent.resources.requests.cpu` | string | `50m` | Agent CPU request |
| `haproxy.agent.resources.requests.memory` | string | `256Mi` | Agent memory request (Guaranteed QoS — limits.memory matches) |
| `haproxy.agent.resources.limits.memory` | string | `256Mi` | Agent memory limit |
| `haproxy.agent.extraEnv` | list | `[]` | Extra env vars for the agent container; `GOMAXPROCS` here overrides the auto-calculation from CPU/memory limits |

The agent's reload pacing and reload deadline aren't separate values: the chart
templates them from [`controller.config.dataplane.minDeploymentInterval` and
`controller.config.dataplane.reloadVerificationTimeout`](#agent-configuration),
so the controller and the agent can't disagree.

Agent credentials are the top-level `credentials.dataplane.*` section — see [Credentials](#credentials) above.

The agent's probes are fixed: `startupProbe` on `/readyz` and `livenessProbe` on
`/healthz`. `/readyz` means "the agent can accept applies" and stays true after
a rejected apply, because a pod that can't be applied to is exactly the pod the
next apply has to reach.

## HAProxy tuning

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.nbthread` | int/string | absent | HAProxy `nbthread` directive value. Default (key absent): if `haproxy.resources.limits.cpu` is set, `ceil(cpu_limit)` clamped to ≥1; otherwise the directive is omitted and HAProxy auto-detects all node cores from its CPU affinity. Set `0` to force-omit the directive. Templatable — `"{{ mul 2 (...) }}"` is allowed |
| `haproxy.shmStats.enabled` | bool | `false` | Persist HAProxy stats counters across reloads via `shm-stats-file` (HAProxy 3.3+ only) |
| `haproxy.shmStats.path` | string | `/dev/shm/haproxy-stats` | Path to the shared-memory stats file |
| `haproxy.shmStats.maxObjects` | int | `50000` | Maximum object count in the shm-stats file. Each frontend, backend, listen, and server counts as one object — pick a value with headroom; HAProxy can't resize the file on reload |
| `haproxy.shmStats.shmSizeLimit` | string | `""` | `/dev/shm` emptyDir size limit. Empty auto-calculates from `maxObjects` (~4 KB/object + 10% overhead, rounded to MiB) |
| `haproxy.lifecycle` | map | `{}` | Container lifecycle hooks for the HAProxy container (`preStop`, `postStart`) |
| `haproxy.updateStrategy.type` | string | `RollingUpdate` | HAProxy Deployment update strategy |
| `haproxy.updateStrategy.rollingUpdate.maxSurge` | int/string | `1` | Maximum surge during rolling updates |
| `haproxy.updateStrategy.rollingUpdate.maxUnavailable` | int/string | `0` | Maximum unavailable during rolling updates |
| `haproxy.minReadySeconds` | int | `0` | Minimum seconds a new HAProxy pod must be ready before it counts as available |
| `haproxy.revisionHistoryLimit` | int | `10` | Number of old ReplicaSets to retain |

## HAProxy KEDA Autoscaling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.keda.enabled` | bool | `false` | Enable KEDA `ScaledObject` for HAProxy pods (event-driven autoscaling) |
| `haproxy.keda.minReplicaCount` | int | `2` | Minimum HAProxy replica count |
| `haproxy.keda.maxReplicaCount` | int | `10` | Maximum HAProxy replica count |
| `haproxy.keda.pollingInterval` | int | `30` | KEDA trigger polling interval in seconds |
| `haproxy.keda.cooldownPeriod` | int | `300` | Seconds to wait before scaling down |
| `haproxy.keda.fallback.failureThreshold` | int | `3` | Consecutive trigger failures before falling back |
| `haproxy.keda.fallback.replicas` | int | `2` | Replica count to fall back to on trigger failure |
| `haproxy.keda.advanced` | map | `{}` | Advanced HPA `behavior` overrides (scale-up/-down stabilisation windows, etc.) |
| `haproxy.keda.triggers` | list | required | Scaling triggers; see KEDA docs for the per-source schema (Prometheus, CPU, cron, …) |

## HAProxy monitoring

One `PodMonitor` for every metrics endpoint on the HAProxy pod. See [Where to scrape](operations/monitoring.md#where-to-scrape).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.monitoring.podMonitor.enabled` | bool | `false` | Create a `PodMonitor` selecting the HAProxy pods (needs the prometheus-operator CRDs). It declares one endpoint per metrics port the pod exposes: `stats` (HAProxy's exporter, `haproxy_*`), with the Vector sidecar on `vector-metrics` and — while a request-metrics size family is enabled — `vector-sizes`, and with the sidecar off and the SPOA hub on the hub's `metrics` port (a hub pinned to a loopback bind fails the render with guidance; `spoaHub.hub.metricsAddr: auto` resolves to a pod-routable bind in that case). No scrape parameters: HAProxy applies the exclusion policy itself |
| `haproxy.monitoring.podMonitor.interval` | duration | `30s` | Scrape interval, applied to every endpoint |
| `haproxy.monitoring.podMonitor.scrapeTimeout` | duration | `10s` | Scrape timeout, applied to every endpoint |
| `haproxy.monitoring.podMonitor.labels` | map | `{}` | Extra labels on the `PodMonitor` (for a Prometheus `podMonitorSelector`) |
| `haproxy.monitoring.podMonitor.relabelings` | list | `[]` | `relabelings` applied to every endpoint |
| `haproxy.monitoring.podMonitor.metricRelabelings` | list | `[]` | `metricRelabelings` applied to every endpoint |

## SPOA hub sidecar

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `spoaHub.enabled` | bool/null | `null` | Master enable. `null` auto-derives from `spoaHub.plugins.*.enabled`. Set `true` to force the sidecar on with no plugins enabled (test rendering); set `false` to force it off even when a plugin is enabled |
| `spoaHub.image.repository` | string | `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub` | SPOA hub image repository |
| `spoaHub.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `spoaHub.image.tag` | string | `""` | Override the tag; empty uses `.Chart.AppVersion` |
| `spoaHub.resources.requests.cpu` | string | `50m` | SPOA hub CPU request |
| `spoaHub.resources.requests.memory` | string | `128Mi` | SPOA hub memory request |
| `spoaHub.resources.limits.memory` | string | `256Mi` | SPOA hub memory limit |
| `spoaHub.hub.logLevel` | string | `info` | Hub log level (`info`/`debug`/`warn`/`error`) |
| `spoaHub.hub.workerThreads` | int/null | `null` | Tokio worker thread count; `null` defaults to CPU count |
| `spoaHub.hub.maxConnections` | int | `1000` | Maximum concurrent connections per listener |
| `spoaHub.hub.blockingThreadKeepAliveSecs` | int | `30` | Keep-alive seconds for blocking-thread workers |
| `spoaHub.hub.maxBlockingThreads` | int/null | `null` | Process-wide blocking-pool cap. Null derives the sum of resolved per-plugin concurrency; an explicit value must be at least that sum. Changing it rolls the HAProxy pods because Tokio fixes this pool at process start |
| `spoaHub.hub.reloadDrainTimeoutMs` | int/null | `null` | Hot-reload quiesce-and-drain budget. Null derives 1.5 times the largest plugin timeout, clamped to 1–30 seconds. `0` restores unsafe legacy immediate retirement and can lose in-flight/background work |
| `spoaHub.hub.metricsAddr` | string | `auto` | Hub Prometheus `/metrics` listen address. `auto` binds it where whatever scrapes it can reach: `127.0.0.1:9095` when `vector.enabled` is true (Vector scrapes over loopback from inside the pod and re-exports on its own port), `0.0.0.0:9095` when it's false (Prometheus scrapes the pod IP directly, so a loopback bind would be a dead target). Set an explicit `<ip>:<port>` to override, or `""` to disable the endpoint (loses per-plugin counters). The metrics carry per-Ingress/route cardinality, so prefer the derived value over exposing it unnecessarily |
| `spoaHub.hub.goGCPercent` | int | `300` | Go GC target percentage (`GOGC`) for the sidecar's embedded Go runtime (the coraza plugin). Higher than Go's default `100` collects less often under load — fewer stop-the-world pauses and less GC-assist CPU stealing on the request path — for a lower p99 tail. `GOMEMLIMIT` is derived automatically as a soft cap at 90% of the container memory limit. Set `100` to restore Go's default |
| `spoaHub.haproxy.socketPath` | string | `/run/spoa/hub.sock` | Unix socket path shared between HAProxy and the hub |
| `spoaHub.haproxy.modeSpop` | bool | `true` | Use HAProxy 3.1+ `mode spop` backend; auto-falls back to `mode tcp` on 3.0. Set `false` to force `mode tcp` on 3.1+ |
| `spoaHub.haproxy.timeoutHello` | duration | `2s` | Stream Processing Offload Engine (SPOE) hello timeout |
| `spoaHub.haproxy.timeoutIdle` | duration | `5m` | SPOE idle timeout |
| `spoaHub.haproxy.timeoutProcessing` | duration/null | `null` | HAProxy's outer per-message processing timeout. Null derives each enabled message's budget plus `timeoutProcessingMarginMs`; a message budget sums all plugin timeouts sharing that message, covering sequential dependency stages without adding unrelated plugins. An explicit value applies to every message and fails rendering when it's below any enabled message's minimum |
| `spoaHub.haproxy.timeoutProcessingMarginMs` | int | `100` | Scheduling and serialization margin added between each enabled message's plugin budget and its HAProxy deadline |
| `spoaHub.haproxy.poolMaxConn` | int | `100` | Connection pool maximum |
| `spoaHub.haproxy.poolPurgeDelay` | duration | `30s` | Idle-connection purge delay |
| `spoaHub.plugins.<name>.enabled` | bool/string | `'{{ false }}'` (templatable) | Per-plugin enable. Default value is a chart-evaluated `tpl` string so a plugin can auto-enable when the template libraries that rely on it are on; explicit `--set` bool always wins |
| `spoaHub.plugins.<name>.timeoutMs` | int | per-plugin | Plugin processing timeout in milliseconds |
| `spoaHub.plugins.<name>.maxConcurrency` | int/null | plugin default | Maximum plugin calls executing concurrently. Use this as the single owner of plugin CPU admission |
| `spoaHub.plugins.<name>.maxQueue` | int/null | plugin default | Maximum calls waiting for a concurrency slot. Coraza defaults to `0`, rejecting excess work instead of inflating latency under attack |
| `spoaHub.plugins.<name>.queueTimeoutMs` | int/null | plugin default | Maximum queue wait when `maxQueue` is non-zero |
| `spoaHub.plugins.<name>.adaptiveConcurrency` | bool | plugin default (on for coraza/api-gateway) | The latency-feedback concurrency controller (ADR-0002): the hub resizes the plugin's admission semaphore at runtime and `maxConcurrency` becomes the ceiling. Requires a hub image with adaptive support; an older hub ignores it |
| `spoaHub.plugins.<name>.messages` | list | per-plugin | SPOE messages this plugin handles |
| `spoaHub.plugins.<name>.dependsOn` | list | `[]` | Other plugin names this plugin must run after |
| `spoaHub.plugins.<name>.params` | string | per-plugin | Free-form TOML blob spliced verbatim under `[plugins.params]` — use dotted keys (`x.y = "..."`) or fully qualified headers (`[plugins.params.x]`) for nested values; bare `[x]` headers close the params scope and break the config |
| `spoaHub.plugins.coraza.directives` | string | OWASP CRS includes + `SecRuleEngine On` | Chart-wide Coraza WAF directives. Reusable policy applications and authorized per-Ingress rules layer on this base. Keep `SecRuleEngine On` *after* the includes — `@coraza.conf-recommended` sets `DetectionOnly`, so an earlier `On` is silently overridden and the WAF never blocks. Don't also set `directives` inside `params:`; the duplicate TOML field breaks the config |
| `spoaHub.plugins.mirror.targetTimeoutMs` | int | `2000` | Per-target timeout for asynchronous mirror requests. It's independent of application backend timeouts so a dead mirror releases shared hub capacity quickly |
| `spoaHub.plugins.mirror.targetRetries` | int | `0` | Retry count for asynchronous mirror requests. The default avoids multiplying work against an unavailable observability target |
| `spoaHub.securityContext` | map | See values.yaml | Container security context for the spoa-hub container. Default runs user and group 99, matching the pod's `fsGroup`, so the Unix socket the hub creates under `/run/spoa` is accessible to the HAProxy container; read-only root filesystem, no privilege escalation, all capabilities dropped |
| `spoaHub.extraVolumeMounts` | list | `[]` | Extra volume mounts added to the spoa-hub container only (rendered through `tpl`) — for MMDB files (`maxmind`), OpenID Connect (OIDC) client secrets (`sso-auth`), and similar plugin data |

Available plugin names (`<name>`): `api-gateway`, `coraza`, `external-auth`, `fingerprinting`, `maxmind`, `mirror`, `rate-limit`, `sso-auth`. See `values.yaml` for each plugin's defaults and the upstream plugin README for the `params:` schema.

## Vector sidecar

A [Vector](https://vector.dev) container on every HAProxy pod. It receives the access log over a Unix datagram socket, derives [per-request metrics](operations/monitoring.md#request-metrics) from it, and re-exports the SPOA hub's Prometheus metrics alongside its own. HAProxy's own exporter is scraped directly (see [Prometheus exporter](#prometheus-exporter)). See [Access logging](haproxy-deployment.md#access-logging).

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `vector.enabled` | bool | `true` | Run the Vector sidecar on each HAProxy pod. When enabled, the access log goes to `vector.socketPath` instead of stdout and surfaces under `kubectl logs <pod> -c vector`. Set to `false` to log straight to the HAProxy container's stdout and scrape the hub directly; HAProxy's exporter is scraped directly either way |
| `vector.image.repository` | string | `timberio/vector` | Vector image repository |
| `vector.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `vector.image.tag` | string | `0.58.0-debian` | Pinned Vector version. Renovate bumps it; keep the `# renovate:` comment above the value or tracking stops. An empty tag is rejected — a floating tag would change the log pipeline under a running fleet |
| `vector.metricsPort` | int | `9598` | Port serving Vector's `/metrics`: its own series, the log-derived and request metrics, and the hub's re-exported ones. Rejected at render time if it collides with an `haproxy.ports.*` entry or the hub's metrics port |
| `vector.sizeMetricsPort` | int | `9599` | Port serving the byte-size histograms from `requestMetrics`. A second exporter exists because Vector's `prometheus_exporter` takes one `buckets` list for every distribution it renders — there is no per-metric override — and bytes and seconds are different domains: a shared list would give each family boundaries it can never fall into. Rendered, along with its container port and `PodMonitor` endpoint, only while `request_size` or `response_size` is enabled. Validated for collisions the same way as `metricsPort`, including against `metricsPort` itself |
| `vector.socketPath` | string | `/run/vector/haproxy.sock` | Unix datagram socket HAProxy writes access-log records to. Must be an absolute path with no whitespace — HAProxy's `log <path>` form requires one |
| `vector.omitEmptyLogFields` | bool | `true` | Strip access-log fields whose value is the empty string, so a record carries only what actually happened. A feature that didn't fire still costs its field on every line: on a measured fleet `trace_id`, `denied_by`, `waf_matched_var`, `consumer` and `cache` were empty in 100% of records, and dropping every empty made records **27% smaller** (815 to 595 bytes average). Sending nothing rather than an empty value is what Elastic Common Schema and OpenTelemetry both recommend. HAProxy can't do it — its JSON encoder has no omit option and the closest one (`+M`) substitutes `-` instead — so Vector strips them in a `remap` transform that rewrites the line rather than re-encoding it, which preserves field order (re-encoding sorts keys alphabetically and would bury `ts`). Numbers are untouched: a genuine `queue_time_ms: 0` is kept. Set to `false` if you feed a strongly typed index, or have queries written as `field == ""`, where a stable field set matters more than the bytes |
| `vector.logMetrics` | map | eight entries, all on | Metrics derived from the access log, each entry naming a log **field** and how to project it. Several in-path components expose no scrape endpoint of their own — Varnish has none at all, its counters living in shared memory behind `varnishstat` — but the access log already carries their verdict per request, so the metric reads a field that's present anyway. Extraction is a regex over the raw record, never a JSON parse, because the pipeline deliberately doesn't parse access-log records. A map rather than a list so adding an entry doesn't replace the ones the chart ships. Each entry sets `enabled` (required; an entry omitting the flag is refused rather than sitting inert), `field`, `metric`, and `kind`: `enum` emits a counter tagged with the field's value and must list its accepted `values`, so an unexpected value can't invent an unbounded label, with `tag` naming the label (default `value`); `numeric` emits a counter incremented **by** the value. An entry may also set `requires`, a dotted values path that must be truthy — a metric whose emitter is switched off would otherwise cost a regex on every record. Field, metric, tag, and value names are validated at render time, since they're embedded in the rendered Vector config and an invalid one stops the sidecar child. Four shipped cache entries cover hit rate, object age, storage refusals, and cache bypass. The other four count limiter, WAF, and schema dependency degradation plus every denial reason |
| `vector.requestMetrics.enabled` | bool | `true` | Derive [per-request metrics](operations/monitoring.md#request-metrics) from the access log: one counter and six histograms, dimensioned by route rather than request URI, with the upstream call split into connect, headers and full response. `haproxy_*` has no equivalent — it offers per-backend counters and non-aggregatable rolling averages, so no per-route dimension, no quantiles and no phase breakdown. On whenever the sidecar is, since the log already carries every field |
| `vector.requestMetrics.prefix` | string | `haptic_ingress_controller` | Metric name prefix, becoming the Vector metric namespace. Set to `nginx_ingress_controller` to make the output byte-compatible with `ingress-nginx`, so its dashboards, recording rules and alerts keep working — see [Migrating](migrating.md#metrics). A trailing underscore is accepted and stripped, since the exporter joins namespace and name with one itself |
| `vector.requestMetrics.controllerClass` | string | `""` | Value of the `controller_class` label. Empty means `ingressClass.controllerName`. `ingress-nginx` puts its `--controller-class` here (`k8s.io/ingress-nginx`), so set that if a dashboard selects on it |
| `vector.requestMetrics.terminationStateLabel` | bool | `true` | Add `term`, HAProxy's 4-character termination state, to every family. It separates a client abort from a server abort, a connect failure, a queue timeout and a response HAProxy generated itself — the one label `ingress-nginx` has no equivalent of, and usually the fastest route from "requests are failing" to a cause. It's also the most expensive, because it multiplies the histograms too, so it's the first thing to turn off if the series count hurts. Setting `false` removes the label rather than blanking it, so the remaining series aggregate exactly as they would have without it |
| `vector.requestMetrics.pathLabel` | bool | `true` | Add `path`, the matched **route** — the path template you wrote, so it's bounded by the number of rules rather than by traffic. Turning it off also switches off the HAProxy-side route lookup, saving four map lookups per request as well as series |
| `vector.requestMetrics.hostLabel` | bool | `true` | Add `host`. The equivalent of `ingress-nginx`'s `--metrics-per-host` |
| `vector.requestMetrics.durationBuckets` | list | 15 boundaries, 1 ms to 60 s | `le` boundaries for the four duration histograms, on the `metricsPort` exporter. A strict **superset** of `ingress-nginx`'s `--time-buckets`, so a rule that hardcodes `le="0.5"` keeps resolving: 1 ms and 2.5 ms are added below, because an in-cluster backend commonly answers in 1–3 ms and upstream's 5 ms floor collapses the whole fast path into one bucket, and 30 s and 60 s above, because upstream stops at 10 s and every longer request lands in `+Inf`, saturating `histogram_quantile` well below the server timeouts this chart ships. Must be positive and strictly ascending — Prometheus reads them as cumulative, so an out-of-order list produces silently wrong quantiles |
| `vector.requestMetrics.sizeBuckets` | list | 12 boundaries, 100 B to 100 MB | `le` boundaries for the two size histograms, on the `sizeMetricsPort` exporter. A 1-3-10 ladder, deliberately not `ingress-nginx`'s: it measures `request_size` and `response_size` against 10, 20, … 100 **bytes**, so every real payload lands in `+Inf` and those `_bucket` series carry no information — there is no working size-quantile query to stay compatible with. `_sum` and `_count`, which is what its network-I/O panel reads, are unaffected either way |
| `vector.requestMetrics.cardinalityLimit.enabled` | bool | `true` | Cap how many distinct values any one label may take, per metric. A backstop for a label going unbounded despite the design — a route matched by regex, a Host header an attacker controls, a path template with an id in it. Applies to these metrics only; the `haproxy_*` re-export has bounded labels and keeps `honor_labels` intact. State is in memory and resets when the sidecar restarts, so treat a tripped limit as something to fix |
| `vector.requestMetrics.cardinalityLimit.valueLimit` | int | `500` | Distinct values allowed per label per metric before the limit trips |
| `vector.requestMetrics.cardinalityLimit.action` | string | `drop_tag` | What to do past the limit. `drop_tag` collapses the offending label onto one series and keeps request totals correct; `drop_event` discards the requests instead, so a cardinality problem would read as an outage |
| `vector.requestMetrics.metrics` | map | seven, all on | Which families to emit; the keys are the emitted name suffixes. An unknown key fails the render rather than sitting inert, and enabling the feature with every entry `false` is refused. `requests` counts one per logged request; `request_duration_seconds` is `%Ta`, total active time, the client's view; `response_duration_seconds` is the whole upstream call; `connect_duration_seconds` is `%Tc`; `header_duration_seconds` is `%Tr`; `request_size` is `%U`, request **body** bytes (so it reads below nginx's `$request_length`, which counts the request line and headers too) and adds a `bytes_in` access-log field; `response_size` is `%B`. The three upstream timers are only recorded when the phase actually happened — a request HAProxy answered itself contributes to `requests` and `request_duration_seconds` and to nothing else |
| `vector.scrapeIntervalSecs` | int | `15` | How often Vector scrapes the hub endpoint it re-exports. Keep at or below Prometheus's own interval, or Prometheus samples a value Vector hasn't refreshed |
| `vector.resources.requests.cpu` | string | `50m` | CPU request for the Vector container |
| `vector.resources.requests.memory` | string | `256Mi` | Memory request for the Vector container. Its memory tracks the request-metrics series (traffic shape, bounded by `cardinalityLimit`) plus the hub's re-export — measured at 146 MB idle and 364 MB steady with 5,000 distinct routes at 500 records/s. Raise it with the limit for a fleet with far more distinct routes and hosts, or lower `cardinalityLimit.valueLimit` and the label opt-outs instead |
| `vector.resources.limits.memory` | string | `1Gi` | Memory limit for the Vector container. Covers the measured 698 MB peak of the traffic run above with headroom, and sits above the request so that headroom is burstable rather than reserved. A supervisor running as process 1 restarts an exited or unresponsive Vector child without withdrawing healthy HAProxy traffic; a whole-container OOM can still briefly affect pod readiness. |
| `vector.securityContext.allowPrivilegeEscalation` | bool | `false` | Container security context for Vector |
| `vector.securityContext.readOnlyRootFilesystem` | bool | `true` | Read-only root filesystem; Vector's writable paths are the `data_dir` and `/tmp` emptyDir volumes |
| `vector.securityContext.runAsNonRoot` | bool | `true` | Refuse to run as root |
| `vector.securityContext.capabilities.drop` | list | `[ALL]` | Linux capabilities to drop |
| `vector.extraVolumeMounts` | list | `[]` | Extra volume mounts added to the Vector container only (rendered through `tpl`) — for credentials a downstream sink needs |

## HAProxy resources & scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.resources.requests.cpu` | string | `250m` | CPU request |
| `haproxy.resources.requests.memory` | string | `1Gi` | Memory request (Guaranteed QoS — limits.memory matches) |
| `haproxy.resources.limits.memory` | string | `1Gi` | Memory limit |

No CPU limit is set by default to avoid throttling. With no limit, HAProxy's `nbthread` auto-detects all node cores from its CPU affinity — so HAProxy uses every core on a static node without inflating CPU requests. Set `haproxy.resources.limits.cpu` to cap both the CPU quota and `nbthread` to `ceil(limit)`.

## HAProxy NetworkPolicy

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.networkPolicy.enabled` | bool | `true` | Enable HAProxy NetworkPolicy |
| `haproxy.networkPolicy.allowExternal` | bool | `true` | Allow external traffic |
| `haproxy.networkPolicy.allowedSources` | list | `[]` | Allowed traffic sources (when allowExternal=false) |
| `haproxy.networkPolicy.extraIngress` | list | `[]` | Additional ingress rules |
| `haproxy.networkPolicy.extraEgress` | list | `[]` | Additional egress rules |

## Controller NetworkPolicy

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.networkPolicy.enabled` | bool | `true` | Enable controller NetworkPolicy |
| `controller.networkPolicy.egress.allowDNS` | bool | `true` | Allow DNS resolution |
| `controller.networkPolicy.egress.kubernetesApi` | list | See values.yaml | Kubernetes API access rules |
| `controller.networkPolicy.egress.haproxyPods.enabled` | bool | `true` | Allow controller egress to the agent on each HAProxy pod (release namespace unless `namespaceSelector` is set) |
| `controller.networkPolicy.egress.haproxyPods.podSelector` | map | See values.yaml | Pod-label selector matching the HAProxy pods to reach |
| `controller.networkPolicy.egress.haproxyPods.namespaceSelector` | map | `{}` | Namespace selector. **`{}` emits no selector**, restricting the rule to the release namespace — set `matchLabels` to reach HAProxy pods in other namespaces |
| `controller.networkPolicy.egress.additionalRules` | list | See values.yaml | Additional egress rules; the chart default allows egress to every in-cluster pod (keeps `http.Fetch()` working) — set `[]` to lock down |
| `controller.networkPolicy.ingress.monitoring.enabled` | bool | `false` | Allow Prometheus scraping |
| `controller.networkPolicy.ingress.monitoring.podSelector` | map | `{}` | Prometheus pod selector. **`{}` means every pod** — set `matchLabels` to identify your Prometheus deployment |
| `controller.networkPolicy.ingress.monitoring.namespaceSelector` | map | `{}` | Prometheus namespace selector. **`{}` emits no selector**, so only same-namespace scrapers match — set `matchLabels` to admit your monitoring namespace |
| `controller.networkPolicy.ingress.healthChecks.enabled` | bool | `true` | Allow health check access |
| `controller.networkPolicy.ingress.healthChecks.from` | list | `[{podSelector: {}}]` | NetworkPolicy peers allowed to reach the health port (`controller.ports.healthz`). The default `podSelector: {}` admits every pod in the release namespace |
| `controller.networkPolicy.ingress.webhook.enabled` | bool | `true` | Allow webhook access |
| `controller.networkPolicy.ingress.webhook.from` | list | IPv4+IPv6 `ipBlock` catch-alls | NetworkPolicy peers allowed to reach the webhook port (`controller.ports.webhook`). Defaults to `ipBlock` catch-alls because the kube-apiserver runs host-network on most distributions — a pod/namespace selector would silently fail to match it and the webhook would return 502 errors. Both `0.0.0.0/0` and `::/0` appear because `ipBlock.cidr` is single-family. Tighten to your apiserver/node CIDRs for production |
| `controller.networkPolicy.ingress.additionalRules` | list | `[]` | Additional ingress rules |

## See also

- [Deploying with Helm](./deploying-with-helm.md) — install, upgrade, and a task-based tour of the chart
- [Template Libraries](./template-libraries.md) — what each `controller.templateLibraries.*` toggle loads
- [CRD Reference](./crd-reference.md) — every field of the `HAProxyTemplateConfig` the chart renders from `controller.config`
