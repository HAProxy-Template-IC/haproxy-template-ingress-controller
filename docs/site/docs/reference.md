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
| `controller.config.routing.regexMatchOrder` | `controller.config.templatingSettings.extraContext.routing.regexMatchOrder` |
| `controller.defaultSSLCertificate` | `defaultSSLCertificate` |
| `haproxy.enterprise.version` | `haproxyVersion` |
| Root-level controller workload values (`replicaCount`, `image`, `deploymentAnnotations`, `webhook`, `monitoring`, `networkPolicy`, `autoscaling`, `podDisruptionBudget`, `service`, `serviceAccount`, `rbac`, `securityContext`, `resources`, probes, rollout, and extras) | The same key under `controller.*` (for example `controller.replicaCount`) |
| `controller.config.templatingSettings.extraContext.debug` | `controller.config.templatingSettings.extraContext.diagnostics.routingHeaders.enabled` (now defaults to `false`) |
| `controller.statusPatches.enabled` and `controller.config.templatingSettings.extraContext.statusPatchesDisabled` | `controller.config.templatingSettings.extraContext.statusPatches.enabled` (inverted: `statusPatchesDisabled: true` becomes `enabled: false`) |
| `controller.config.templatingSettings.extraContext.password_hash_validation_regex` and `…password_hash_validation_error_message` | `controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.regex` and `.errorMessage` |
| `controller.config.templatingSettings.extraContext.hstsEnabled`, `hstsMaxAge`, `hstsIncludeSubdomains`, `hstsPreload` | `controller.config.templatingSettings.extraContext.tls.hsts.enabled`, `.maxAge`, `.includeSubdomains`, `.preload` |

Cache and rate-limit settings introduced after the previous release use their
final ownership from the start: `cache.varnish` owns the Varnish workload,
`cache.haproxy` owns HAProxy cache integration, `rateLimit.shared` owns the
feature, and `rateLimit.shared.managedStore` owns the optional bundled Valkey
topology. Plugin execution remains under `spoaHub.plugins.*`.

## CRD lifecycle

Helm installs the CRDs in `crds/` once and never upgrades them on a subsequent
`helm upgrade`. This hook Job runs `haptic-controller apply-crds` (server-side
apply) so additive CRD schema changes reach the cluster on install and upgrade.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `crds.upgradeJob.enabled` | bool | `true` | Run the `pre-install`/`pre-upgrade` hook Job (and its scoped RBAC) that server-side applies the bundled CRDs. Disable if you manage CRDs out-of-band or lack cluster-scoped CRD write permission at upgrade time |
| `crds.upgradeJob.backoffLimit` | int | `2` | Job retry limit (the apply is idempotent, so retries are safe) |
| `crds.upgradeJob.activeDeadlineSeconds` | int | `300` | Job wall-clock deadline |
| `crds.upgradeJob.resources` | object | cpu `50m` / memory `64Mi`–`128Mi` | Resource requests and limits for the apply Job pod |
| `crds.upgradeJob.annotations` | map | `{}` | Extra annotations for the Job (merged with chart defaults) |
| `crds.upgradeJob.labels` | map | `{}` | Extra labels for the Job (merged with chart defaults) |

## Deployment & Image

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.replicaCount` | int | `2` | Number of controller replicas (2+ recommended for HA with leader election) |
| `haproxyVersion` | string | `"3.4"` | HAProxy major.minor series. Drives both the controller image tag suffix (`:<version>-haproxy<haproxyVersion>`) and — combined with `haproxyPatchVersions` — the HAProxy pod image tag |
| `haproxyPatchVersions` | map | See values.yaml | Per-`haproxyVersion` community patch pins (for example `"3.2": "3.2.x"`). Maintained by the chart and auto-updated by Renovate |
| `haproxyEnterprisePatchVersions` | map | See values.yaml | Per-`haproxyVersion` enterprise revision pins (for example `"3.2": "3.2r1"`). Used when `haproxy.enterprise.enabled=true` |
| `controller.image.repository` | string | `registry.gitlab.com/haproxy-haptic/haptic` | Controller image repository |
| `controller.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `controller.image.tag` | string | `""` | Controller image tag; empty = `<chart appVersion>-haproxy<haproxyVersion>` |
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
| `controller.ports.healthz` | int | `8080` | Single source of truth for the controller's `/healthz` and `/debug/*` listener, container port, Service, probes, and NetworkPolicy |
| `controller.ports.metrics` | int | `9090` | Single source of truth for the `/metrics` listener, container port, Service, and monitors; `0` disables metrics and requires all monitor resources to be disabled |
| `controller.ports.webhook` | int | `9443` | Admission webhook HTTPS port |
| `controller.config.templatingSettings.extraContext.statusPatches.enabled` | bool | `true` | Whether the controller writes LoadBalancer addresses back to Ingress/Gateway `.status`. Disable during a controller migration so the incumbent keeps owning status — with `extraContext.statusPatches.enabled: false` the status-patch snippets become no-ops |

## Template libraries

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.templateLibraries.base.enabled` | bool | `true` | Core HAProxy configuration. Disabling drops the `haproxyConfig` template the other libraries plug into; leave on unless you supply a complete replacement |
| `controller.templateLibraries.ssl.enabled` | bool | `true` | SSL/TLS and HTTPS frontend support |
| `controller.templateLibraries.ingress.enabled` | bool | `true` | Kubernetes Ingress resource support |
| `controller.templateLibraries.gateway.enabled` | bool | `true` | Gateway API support (HTTP, gRPC, TLS and TCP routes) |
| `controller.templateLibraries.gateway.experimentalChannel` | bool | `false` | Declare that the Gateway API *Experimental* channel (`experimental-install.yaml`) is installed. Enables the `validationTests` that assert experimental HTTPRoute fields (`retry` per Gateway Enhancement Proposal (GEP) 1731, `sessionPersistence` per GEP-1619) — Helm can't detect the channel because both installs ship identical CRDs and only HTTPRoute *fields* differ. The route snippets emit those directives whenever the fields are present, regardless of this flag |
| `controller.templateLibraries.ingressAnnotationsCompat.enabled` | bool | `true` | Shared ingress-annotations-compat scaffold (level 2.5). Provides parameterized macros consumed by the Ingress vendor annotation libraries below |
| `controller.templateLibraries.hapticAnnotations.enabled` | bool | `true` | `haproxy-haptic.org/*` — HAPTIC's native annotation vocabulary; a best-of-breed superset of the three vendor libraries. The recommended vocabulary for new configs |
| `controller.templateLibraries.haproxytech.enabled` | bool | `false` | `haproxy.org/*` annotation compatibility (haproxytech/kubernetes-ingress migration) — opt-in |
| `controller.templateLibraries.haproxyIngress.enabled` | bool | `false` | `haproxy-ingress.github.io/*` annotation compatibility (jcmoraisjr/haproxy-ingress migration) — opt-in |
| `controller.templateLibraries.nginxIngress.enabled` | bool | `false` | `nginx.ingress.kubernetes.io/*` annotation compatibility (ingress-nginx migration) — opt-in |
| `controller.templateLibraries.spoaHub.enabled` | bool | `false` | HAProxy-side Stream Processing Offload Agent (SPOA) hub wiring. Auto-loaded when the SPOA hub sidecar is rendered (any `spoaHub.plugins.*` enabled, or `spoaHub.enabled: true`); set this to `true` to force-load the library standalone |

## Shared response cache

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `cache.haproxy.hashBalanceFactor` | int | `150` | HAProxy bounded-load consistent hashing: cap any one Varnish shard's share to this factor of the mean (`0` disables) |
| `cache.varnish.enabled` | bool | `false` | Deploy the shared Varnish cache tier and emit the cache routing/backend. When on, Ingresses carrying `haproxy-haptic.org/cache-*` annotations are routed through a consistent-hash-sharded Varnish workload so the cache is shared across the HAProxy fleet |
| `cache.varnish.loopbackPort` | int | `8090` | Dedicated internal HAProxy port Varnish fetches cache misses from (the "sandwich" backend leg), so the WAF/rate-limit/auth/routing chain runs once on the client request and never on the miss. Reached only by Varnish (via `originServiceName`, gated by the HAProxy NetworkPolicy); never published on the LoadBalancer |
| `cache.varnish.originServiceName` | string | `haptic-cache-origin` | Name of the internal ClusterIP Service (in the release namespace) that fronts the dedicated backend-fetch port on the HAProxy pods |
| `cache.varnish.workload` | string | `statefulset` | Varnish workload kind: `statefulset` (ordered rollout keeps `1/N` of the cache warm on restart) or `deployment` (ephemeral accelerator) |
| `cache.varnish.replicas` | int | `2` | Number of Varnish cache shards |
| `cache.varnish.image` | string | `varnish:7.7` | Varnish container image — stock upstream, since the loopback topology needs no custom build. Pin to a digest in production |
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
| `spoaHub.plugins.rate-limit.timeoutMs` | int | `50` | Outer SPOE/plugin processing timeout for shared rate-limit checks. This is the only owner of the plugin timeout; keep it low on public edges so overload fails closed quickly |
| `spoaHub.plugins.rate-limit.storeOperationTimeoutMs` | int | `10` | Per-operation Redis/Valkey timeout rendered as the rate-limit plugin's `store_timeout_ms`. Exact `gcra` mode waits on this path per request; tune according to measured store round-trip time and failover behavior |
| `rateLimit.shared.managedStore.enabled` | bool | `true` | Deploy chart-managed HA Valkey with Sentinel and inject its `store_url` into the rate-limit plugin, so budgets are shared across the HAProxy fleet. Leave true for the out-of-box HA store; set false only when you bring your own store via `rateLimit.shared.externalStore.urls`. Takes effect only when `rateLimit.shared.enabled` is also true — it's a sub-option of the shared limiter, so on its own it deploys nothing |
| `rateLimit.shared.externalStore.urls` | list | `[]` | Bring-your-own Redis/Valkey/Sentinel/Cluster URLs, used with `managedStore.enabled=false` (setting both fails the render). One URL renders the plugin's `store_url`; several render `store_urls` for deployments that intentionally shard keys across independent stores. The chart owns the generated store lines and rejects a manual `store_url`/`store_urls` in `spoaHub.plugins.rate-limit.params` |
| `rateLimit.shared.managedStore.image` | string | `valkey/valkey:8-alpine` | Valkey image for the chart-managed shared rate-limit store |
| `rateLimit.shared.managedStore.imagePullPolicy` | string | `IfNotPresent` | Kubernetes pull policy for both the Valkey and Sentinel containers (`Always`, `IfNotPresent`, or `Never`) |
| `rateLimit.shared.managedStore.port` | int | `6379` | Valkey Service port for the chart-managed shared rate-limit store |
| `rateLimit.shared.managedStore.replicas` | int | `3` | Fixed Valkey pod count for the chart-managed Sentinel topology: one writable primary plus replicas for failover. Must be at least 3. This is HA, not automatic horizontal Valkey scaling |
| `rateLimit.shared.managedStore.maxMemory` | string | `96mb` | Valkey `--maxmemory` for the chart-managed shared rate-limit store |
| `rateLimit.shared.managedStore.maxMemoryPolicy` | string | `volatile-ttl` | Valkey eviction policy for the chart-managed shared rate-limit store. All limiter keys expire, so this bounds memory under high key cardinality instead of OOM-killing the pod |
| `rateLimit.shared.managedStore.sentinel` | object | port `26379`, quorum `2` | Sentinel settings for managed-store failover: port, quorum, down-after, failover timeout, parallel syncs, and Sentinel container resources |
| `rateLimit.shared.managedStore.podDisruptionBudget` | object | enabled `true`, `maxUnavailable` `1` | PodDisruptionBudget settings for the managed Valkey pods |
| `rateLimit.shared.managedStore.networkPolicy.enabled` | bool | `true` | Emit a NetworkPolicy that only allows HAProxy/SPOA pods and store-internal Valkey/Sentinel traffic to the managed store |
| `rateLimit.shared.managedStore.resources` | object | cpu `50m` / memory `128Mi` | Valkey pod resource requests and limits. The chart-managed store is a fixed-size HA Sentinel topology; use bring-your-own Redis/Valkey infrastructure when you need horizontal store scaling |

## Request-body inspection and JSON schema validation

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes` | int | `16384` | Shared HAProxy `tune.bufsize` for request-body inspectors. API validation and Coraza policy body caps must fit within `sizeBytes - reservedBytes` |
| `controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.reservedBytes` | int | `8192` | Bytes reserved for the request line, headers, and rewrite space; increase for large cookies, JWTs, or tracing headers |
| `controller.config.templatingSettings.extraContext.requestBuffering.enabled` | bool | `true` | Wait for the request body before taking a backend connection, so a slow uploader holds an HAProxy buffer instead of a backend server slot. Only requests declaring a `Content-Length` are held, so gRPC and chunked streaming are never buffered |
| `controller.config.templatingSettings.extraContext.requestBuffering.waitTimeout` | string | `10s` | How long HAProxy waits for the declared body before returning `408`. HAProxy also releases the request once `tune.bufsize` is full |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled` | bool | `false` | Enable native JSON request-schema annotations and auto-enable the bundled `api-gateway` plugin. Matching annotations fail loudly while disabled |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.waitTimeout` | duration | `100ms` | HAProxy-side maximum wait for a matching POST/PUT/PATCH body. Unrelated routes don't wait |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.defaultMaxBytes` | int | `8192` | Default validation input cap when an Ingress omits `request-schema-max-body-size`. The effective cap must fit in the shared HAProxy body capacity |
| `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.defaultFailOpen` | bool | `false` | Default when an Ingress omits `request-schema-fail-open`; unknown schema ids or missing plugin verdicts fail closed by default |
| `spoaHub.plugins.api-gateway.timeoutMs` | int | `25` | Outer SPOE processing timeout for JSON validation; increase only for measured CPU or scheduling pressure |
| `spoaHub.plugins.api-gateway.maxConcurrency` | int/tpl | derived from sidecar memory (16 at the default 256Mi) | Ceiling for concurrent JSON parse/schema evaluations. With `adaptiveConcurrency` on (default) this is the controller's upper bound, not a fixed limit; derived from `spoaHub.resources` memory so it self-scales. Set a literal to override |

## Coraza WAF

Template-side routing, policy catalogs, and Ingress-author permissions live in the structured `extraContext.waf` bag so raw `HAProxyTemplateConfig` users get the same behavior. Process and plugin execution settings live under `spoaHub.plugins.coraza`; neither tree aliases or overrides the other. A non-empty `policies.inline`, `policies.configMapRefs`, or `policies.defaultPolicy` activates policy governance—there is no second enable flag that can leave a configured catalog inert.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.templatingSettings.extraContext.waf.dispatch.mode` | string | `opt-in` | Which requests the rendered config sends to Coraza: `opt-in` runs it only on routes carrying a native or compatibility WAF annotation; `default-on` runs it on every route unless an authorized `nginx.ingress.kubernetes.io/enable-modsecurity: "false"` opts out. `default-on` auto-enables the Coraza plugin |
| `controller.config.templatingSettings.extraContext.waf.dispatch.defaultEnforcement` | string | `deny` | WAF enforcement (`deny` or `detect`) for requests dispatched by `mode: default-on`; selected policies and authorized per-route overrides take precedence. Ignored when `mode: opt-in` |
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
| `controller.config.templatingSettings.extraContext.waf.policies.configMapRefs` | list | `[]` | Exact trusted `namespace`/`name`/`key` ConfigMap catalogs using the same policy schema |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.enabled` | bool | `false` | Namespaced self-service authoring: each namespace may define policies for its own Ingresses in its well-known catalog ConfigMap |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.configMapName` | string | `waf-policies` | Well-known ConfigMap name discovered per namespace for self-service catalogs |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.key` | string | `policies.yaml` | Data key inside each self-service catalog ConfigMap |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.allowSecLang` | bool | `false` | Permit `secLang` in self-service policies (arbitrary rule code in the shared Coraza process — grant deliberately) |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.limits.maxPoliciesPerNamespace` | int | `4` | Maximum self-service policies loaded per namespace (deterministic sorted cut; excess policies fail closed for their selectors) |
| `controller.config.templatingSettings.extraContext.waf.policies.selfService.limits.maxTotalPolicies` | int | `64` | Cluster-wide self-service policy budget, separate from the trusted catalog's `limits.maxCount` |
| `controller.config.templatingSettings.extraContext.waf.policies.limits.maxCount` | int | `16` | Maximum policies across inline definitions and all trusted ConfigMaps |
| `controller.config.templatingSettings.extraContext.waf.policies.limits.maxSecLangBytes` | int | `65536` | Maximum advanced SecLang bytes in one reusable policy |
| `controller.config.templatingSettings.extraContext.waf.policies.limits.maxRuleExclusions` | int | `256` | Maximum structured Core Rule Set (CRS) rule-exclusion entries in one reusable policy |
| `spoaHub.plugins.coraza.timeoutMs` | int | `15` | Outer SPOE timeout for WAF evaluation. This is a failure bound, not expected latency |
| `spoaHub.plugins.coraza.maxConcurrency` | int/tpl | derived from sidecar memory (16 at the default 256Mi) | Ceiling for concurrent Coraza evaluations. With `adaptiveConcurrency` on (default) this is the controller's upper bound, not a fixed limit; derived from `spoaHub.resources` memory so it self-scales (give the sidecar more memory → higher ceiling). Set a literal to override |
| `spoaHub.plugins.coraza.adaptiveConcurrency` | bool | `true` | The hub resizes the admission semaphore at runtime from Coraza's measured service time (ADR-0002), finding the right concurrency from live latency with no manual tuning; `maxConcurrency` is then the controller's ceiling. Set `false` for a fixed cap. Full adaptivity needs a hub image with adaptive support; an older hub ignores the flag and runs `maxConcurrency` as a fixed cap |

## Policy guardrails (governance)

Org-wide baselines that namespace teams can't omit. Configured entirely under
`extraContext` (it creates no Kubernetes resources) and disabled by default. You
declare a list of generic, JSONPath-driven `rules`; each rule targets a watched
resource by name and, per matching resource, either **injects** a default when a
value is absent or **validates** the value when present.

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
| `…extraContext.governance.enabled` | bool | `false` | Master switch for the guardrails |
| `…extraContext.governance.exemptNamespaces` | list | `[]` | Namespaces skipped entirely (infra/system) |
| `…extraContext.governance.rules` | list | `[]` | Admin-declared rules (see the fields below) |

Each entry of `rules` is an object:

| Field | Type | Description |
|-------|------|-------------|
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
    - resource: ingresses
      path: metadata.annotations['haproxy-haptic.org/rate-limit-rps']
      default: "100"
      max: 10000
      onViolation: clamp
    # Require a WAF policy annotation on every HTTPRoute (cross-resource).
    - resource: httproutes
      path: metadata.annotations['haproxy-haptic.org/waf-policy']
      required: true
      enforcement: audit
    # Require TLS (spec.tls or the chart-wide default HTTPS satisfies it).
    - resource: ingresses
      satisfiedBy: tls
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
| `controller.config.credentialsSecretRef.name` | string | Auto-generated | Secret containing Dataplane API credentials |
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

## Dataplane Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.dataplane.minDeploymentInterval` | duration | `5s` | Minimum time between deployments |
| `controller.config.dataplane.driftPreventionInterval` | duration | `60s` | Periodic drift prevention interval |
| `controller.config.dataplane.mapsDir` | string | `/etc/haproxy/maps` | HAProxy maps directory |
| `controller.config.dataplane.sslCertsDir` | string | `/etc/haproxy/ssl` | SSL certificates directory |
| `controller.config.dataplane.generalStorageDir` | string | `/etc/haproxy/general` | General storage directory |
| `controller.config.dataplane.configFile` | string | `/etc/haproxy/haproxy.cfg` | HAProxy config file path |

## Watched Resources

`controller.config.watchedResources.<name>` is a map of resource entries. The chart's template libraries contribute most entries (Ingress, Service, EndpointSlice, Secret, plus the Gateway API route kinds when the gateway library is on); operators can add or override entries here. Each entry accepts:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `apiVersion` | string | required | API group/version (for example `networking.k8s.io/v1`, `v1` for core) |
| `resources` | string | required | Plural resource name (for example `ingresses`) |
| `indexBy` | list | `[]` | JSONPath expressions used to index resources for O(1) template lookup |
| `fieldSelector` | string | `""` | Client-side JSONPath filter (for example `spec.ingressClassName=haptic`); supports any JSONPath expression unlike Kubernetes' built-in `fieldSelector` |
| `labelSelector` | string | `""` | Server-side label selector for watch-time filtering (equality-only `key=value` pairs joined by commas) |
| `enableValidationWebhook` | bool | `false` | Include this resource in the chart-rendered `ValidatingWebhookConfiguration` |
| `statusPatch` | bool | `false` | Allow the controller to patch this resource's `/status` subresource |
| `store` | string | `full` | `full` keeps all resources in memory; `on-demand` fetches with caching (lower memory, slower lookups). Useful for very large Secret stores |
| `debounceInterval` | duration | `""` (`2s`) | Per-resource debounce window; empty/unparseable falls back to the controller-wide default (`DefaultDebounceInterval`, `2s`). Lower for fast-reacting resources (for example `500ms` on httproutes during canaries). Avoid raising the value for resources that drive backend membership — `EndpointSlices` and `pods` in particular — because the debounce delays Pod removal from the HAProxy server pool by that whole window, so live traffic continues hitting Terminating pods until the next render fires |

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
| `controller.config.templatingSettings.extraContext.accessLog.fields` | map | `{}` | Extra JSON access-log fields: field name → one HAProxy sample expression, captured at request time and logged as a string. Use `str(<value>)` for a constant label. Names must match `^[A-Za-z_][A-Za-z0-9_]{0,39}$` and must not collide with a built-in field; expressions must not contain whitespace, `#`, `"` or a backslash. See [Access logging](haproxy-deployment.md#access-logging) |
| `controller.config.templatingSettings.extraContext.accessLog.targets` | list | `[{address: stdout}]` | Where access-log records go; one HAProxy `log` line per entry, so several entries fan out. Each entry takes `address` (`stdout`, `stderr`, `fd@<n>`, `<host>:<port>` (UDP), an absolute socket path or `ring@<name>`), `format` (defaults to `raw` for stdout/stderr, `rfc5424` otherwise), `facility`, `level` (`info` or `debug` — anything stricter drops every record), or a `ring` block (`name`, `address`, `size`, `logProto`, `connectTimeout`, `serverTimeout`, `serverOptions`) for a buffered TCP client that survives a collector restart. HAProxy's own process messages keep their own stdout target. See [Where the logs go](haproxy-deployment.md#where-the-logs-go) |
| `controller.config.templatingSettings.extraContext.accessLog.maxLineBytes` | int | `16384` | `log ... len <bytes>`. HAProxy truncates a longer record mid-byte, which makes it invalid JSON; raise it if custom fields or captured request headers push records past the limit (1024–65535) |
| `controller.config.templatingSettings.extraContext.accessLog.suppress.successful` | bool | `false` | Opt-in: drop access-log records for 2xx/3xx requests that no gate denied. Denials, 4xx and 5xx are always kept. Off by default — retaining a full log is lawful under legitimate interest (GDPR Art. 6(1)(f)), and the successful requests either side of a failure are what make a customer's report diagnosable. A record is ~740 bytes, so ~700 MB per million requests if volume forces your hand. See [Access logging](haproxy-deployment.md#access-logging) |
| `controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.regex` | string | `"^.*$"` | Regex every password hash in a basic-auth Secret must match (the `auth-secret` annotation handlers in the haproxytech and haproxy-ingress libraries). A non-matching hash fails the render with `passwordHashValidation.errorMessage`; the default accepts all hashes. Example restricting to MD5-crypt (apr1) hashes: `"^\$apr1\$"`. Go RE2 syntax — no lookaheads, so express the policy as the *allowed* format |
| `controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.errorMessage` | string | `Invalid password hash` | Error message emitted when a password hash fails validation; the rendered error appends the username, Secret name, and pattern |
| `controller.config.templatingSettings.extraContext.tls.hsts.enabled` | bool | `false` | Emit a global `Strict-Transport-Security` header on TLS responses. Opt-in; per-Ingress HSTS annotations still win |
| `controller.config.templatingSettings.extraContext.tls.hsts.maxAge` | string | `"31536000"` | HSTS `max-age` in seconds for the global header |
| `controller.config.templatingSettings.extraContext.tls.hsts.includeSubdomains` | bool | `false` | Add `includeSubDomains` to the global HSTS header |
| `controller.config.templatingSettings.extraContext.tls.hsts.preload` | bool | `false` | Add `preload` to the global HSTS header |
| `controller.config.watchedResourcesIgnoreFields` | list | `[metadata.managedFields, metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']]` | Fields to ignore in watched resources |

## Webhook Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.webhook.enabled` | bool | `true` | Enable admission webhook validation |
| `controller.webhook.timeoutSeconds` | int | `10` | API-server timeout for watched-resource admission. HAPTIC configures the controller deadline one second shorter. Allowed range: `2..30` |
| `controller.webhook.haproxyTemplateConfig.enabled` | bool | `true` | Also validate `HAProxyTemplateConfig` CRD updates via the webhook (`failurePolicy: Ignore`, not configurable — controller downtime never blocks CRD edits). When active, the leader-side reconcile can skip `haproxy -c` on every render |
| `controller.webhook.haproxyTemplateConfig.timeoutSeconds` | int | `30` | API-server timeout for the more expensive prospective-config admission path, including its size-scaled `validationTests` run. HAPTIC configures the controller deadline one second shorter and admits with a warning on timeout so recovery remains possible. Allowed range: `2..30` |
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

The validator sidecar runs a second `haproxy-spoa-hub` instance in `--validate-socket` mode next to the controller; the admission webhook consults it on every change to a webhook-validated resource so manifests with broken plugin TOML (for example a bad `modsecurity-snippet`) are rejected before they reach the data plane. See [Pluggable validators](./operations/pluggable-validators.md).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.validators.enabled` | bool/null | `null` | Master enable for the validator sidecar. `null` auto-derives from the SPOA hub sidecar's own enable (plugins needing admission-time validation are the ones running on the data plane); `true` renders it even when `spoaHub` is off; `false` forces it off |
| `controller.validators.socketDir` | string | `/var/run/haptic-validators` | Directory for the validator Unix socket — a shared emptyDir mounted into both the controller container and the validator sidecar |
| `controller.validators.socketName` | string | `spoa-hub.sock` | Socket filename. The controller dials `<socketDir>/<socketName>`, and the chart writes that path into the auto-wired `spec.validators` entry |
| `controller.validators.resources.requests.cpu` | string | `25m` | Validator sidecar CPU request (validation is bursty — admission calls are infrequent — so it's sized small) |
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
| `credentials.dataplane.username` | string | `admin` | Dataplane API username |
| `credentials.dataplane.password` | string | `""` | Dataplane API password. Empty generates a random 32-char password. When `lookup` works (a normal `helm upgrade`, or an install against a reachable cluster) the chart reads the existing Secret and preserves the current password across renders. GitOps tools that render without cluster access (ArgoCD/Flux) can't `lookup`, so an empty value regenerates every sync — **set an explicit value** (SealedSecret / external secret) in those setups. |

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
| `controller.resources.requests.memory` | string | `512Mi` | Memory request (Guaranteed QoS — matches `limits.memory`) |
| `controller.resources.limits.memory` | string | `512Mi` | Memory limit |

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
| `controller.monitoring.prometheusRule.defaultRules.enabled` | bool | `true` | Emit the chart's default rule set — nine alerts, each individually toggleable below; only consulted when `rules` is empty |
| `controller.monitoring.prometheusRule.defaultRules.reconciliationErrors` | bool | `true` | Include the `HAProxyControllerReconciliationErrors` warning rule |
| `controller.monitoring.prometheusRule.defaultRules.deploymentFailures` | bool | `true` | Include the `HAProxyControllerDeploymentFailures` critical rule |
| `controller.monitoring.prometheusRule.defaultRules.highQueueDepth` | bool | `true` | Include the `HAProxyControllerHighQueueDepth` warning rule |
| `controller.monitoring.prometheusRule.defaultRules.leaderElectionLost` | bool | `true` | Include the `HAProxyControllerNoLeader` critical rule |
| `controller.monitoring.prometheusRule.defaultRules.fleetDiverged` | bool | `true` | Include the `HAProxyFleetDiverged` warning rule: some HAProxy pods haven't converged to the desired config for 5 minutes. Transient deploy failures self-heal, so sustained divergence is a real fault — this is the noise-free replacement for alerting on raw deployment errors |
| `controller.monitoring.prometheusRule.defaultRules.configRejected` | bool | `true` | Include the `HAProxyControllerConfigRejected` warning rule: the validation gate rejected a config change — the controller keeps serving the last-good config and the latest change isn't live |
| `controller.monitoring.prometheusRule.defaultRules.haproxyPodsRejected` | bool | `true` | Include the `HAProxyControllerHAProxyPodsRejected` warning rule: discovered HAProxy pods refused admission (often a HAProxy major.minor mismatch with `haproxyVersion`) |
| `controller.monitoring.prometheusRule.defaultRules.noHAProxyPods` | bool | `true` | Include the `HAProxyControllerNoHAProxyPods` critical rule: the controller finds no HAProxy pods to manage, so no config reaches the data plane |
| `controller.monitoring.prometheusRule.defaultRules.accessLogDropped` | bool | `true` | Include the `HAProxyAccessLogRecordsDropped` warning rule: HAProxy discarded access-log records because the Vector sidecar stopped draining the Unix datagram socket. Traffic is unaffected; the log is incomplete |
| `controller.monitoring.prometheusRule.defaultRules.criticalEventsDropped` | bool | `true` | Include the `HAProxyControllerCriticalEventsDropped` critical rule: a critical event-bus subscriber's buffer overflowed — reconciliation work was lost and the data plane may be stale |
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
| `haproxy.image.repository` | string | `""` (derived) | HAProxy image repository. Empty selects `haproxytech/haproxy-debian` or `hapee-registry.haproxy.com/haproxy-enterprise` from `haproxy.enterprise.enabled` |
| `haproxy.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `haproxy.image.tag` | string | `""` | HAProxy image tag; empty = derive from `haproxyVersion` plus the matching entry in `haproxyPatchVersions` (for example `3.2` → whichever 3.2.x patch the chart currently pins). Override to pin a specific patch yourself. |
| `haproxy.enterprise.enabled` | bool | `false` | Use HAProxy Enterprise. `haproxyVersion` selects the compatibility series, image revision map, and binary path together |
| `haproxy.haproxyBin` | string | Auto-detected | HAProxy binary path |
| `haproxy.dataplaneBin` | string | Auto-detected | Dataplane API binary path |
| `haproxy.initialConfig` | string | See values.yaml | HAProxy bootstrap config served until the controller pushes the first rendered config; processed via Helm `tpl`. Keep the `/ready` 503 gate or clients hit an empty backend set — see the [HAProxy deployment guide](./haproxy-deployment.md) |

## HAProxy Pod Configuration

Pod-spec scheduling, runtime, and metadata fields live under `haproxy.podSpec.*` (the chart's `_pod-spec.tpl` helper renders the universally shared subset). See also `controller.podSpec.*` for the controller Deployment.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
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
| `haproxy.ports.dataplane` | int | `5555` | Single source of truth for the Dataplane API listener, Service, NetworkPolicy, probes, and the controller's connection port |

## HAProxy Service

The controller renders the user-facing HAProxy Service from these values (the base library's `k8sResources.haproxy-service` template) and owns it via Server-Side Apply — the chart itself only creates the internal dataplane Service. Changes therefore land when the controller reconciles, not at `helm upgrade` time.

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

## HAProxy Dataplane sidecar

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.dataplane.service.type` | string | `ClusterIP` | Dataplane service type |
| `haproxy.dataplane.logLevel` | string | `info` | Log level for the Dataplane API sidecar: `trace`, `debug`, `info`, `warning`, `error`. It logs one line per file operation, so `trace` produced ~671 lines for a single startup plus config cycle. Raise it when diagnosing a config push the controller reports as failing but HAProxy accepts. The stream carries no end-user data — the only client is the controller |
| `haproxy.dataplane.resources.requests.cpu` | string | `50m` | Dataplane sidecar CPU request |
| `haproxy.dataplane.resources.requests.memory` | string | `256Mi` | Dataplane sidecar memory request (Guaranteed QoS — limits.memory matches) |
| `haproxy.dataplane.resources.limits.memory` | string | `256Mi` | Dataplane sidecar memory limit |
| `haproxy.dataplane.extraEnv` | list | `[]` | Extra env vars for the dataplane sidecar; `GOMAXPROCS` here overrides the auto-calculation from CPU/memory limits |
| `haproxy.dataplane.validateConfig` | bool | `false` | Run a server-side `haproxy -c` against each transaction. The controller already validates locally, so server-side validation is redundant; enable for double-validation when extra safety is required |
| `haproxy.dataplane.debugSocketPath` | string | `""` | Unix socket path for runtime profiling of the dataplane sidecar (sets `debug_socket_path` in `dataplaneapi.yaml`) |
| `haproxy.dataplane.aclFormat` | string | `""` | Apache Common Log Format override for the dataplane API access log. Empty leaves the dataplane API's built-in default in place; set this to a format with timing fields (for example `%{us}T` microseconds, `%D` milliseconds) to surface per-request publish-step latency in the access log |

Dataplane API credentials moved to the top-level `credentials.dataplane.*` section — see [Credentials](#credentials) above.

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
| `spoaHub.monitoring.podMonitor.enabled` | bool | `false` | Create a PodMonitor for the HAProxy pod that scrapes the hub data-path metrics (`spoa_*`, `plugin_coraza_*`) and HAProxy's exporter (`:8404`). Only rendered when `vector.enabled` is false — with Vector on, `vector.podMonitor` scrapes the single merged endpoint instead. The `auto` default for `spoaHub.hub.metricsAddr` already resolves to a pod-routable bind in that case; rendering fails only if you pin a loopback address by hand |
| `spoaHub.monitoring.podMonitor.interval` | duration | `30s` | PodMonitor scrape interval |
| `spoaHub.monitoring.podMonitor.scrapeTimeout` | duration | `10s` | PodMonitor scrape timeout |
| `spoaHub.monitoring.podMonitor.labels` | map | `{}` | PodMonitor labels (used by Prometheus to select which PodMonitors to use) |
| `spoaHub.monitoring.podMonitor.relabelings` | list | `[]` | PodMonitor relabelings applied before scraping |
| `spoaHub.monitoring.podMonitor.metricRelabelings` | list | `[]` | PodMonitor metric relabelings applied to scraped metrics |
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
| `spoaHub.haproxy.timeoutProcessing` | duration/null | `null` | HAProxy's outer per-message processing timeout. Null derives the largest enabled message budget plus `timeoutProcessingMarginMs`; a message budget sums all plugin timeouts sharing that message, covering sequential dependency stages without adding unrelated plugins. An explicit value below that minimum fails rendering |
| `spoaHub.haproxy.timeoutProcessingMarginMs` | int | `100` | Scheduling and serialization margin added between the largest enabled message budget and HAProxy's derived outer deadline |
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
| `spoaHub.securityContext` | map | See values.yaml | Container security context for the spoa-hub container. Default runs user and group 99, matching the pod's `fsGroup`, so the Unix socket the hub creates under `/run/spoa` is accessible to the HAProxy container; read-only root filesystem, no privilege escalation, all capabilities dropped |
| `spoaHub.extraVolumeMounts` | list | `[]` | Extra volume mounts added to the spoa-hub container only (rendered through `tpl`) — for MMDB files (`maxmind`), OpenID Connect (OIDC) client secrets (`sso-auth`), and similar plugin data |

Available plugin names (`<name>`): `api-gateway`, `coraza`, `external-auth`, `fingerprinting`, `maxmind`, `mirror`, `rate-limit`, `sso-auth`. See `values.yaml` for each plugin's defaults and the upstream plugin README for the `params:` schema.

## Vector sidecar

A [Vector](https://vector.dev) container on every HAProxy pod. It receives the access log over a Unix datagram socket and re-exports HAProxy's and the SPOA hub's Prometheus metrics alongside its own on a single port. See [Access logging](haproxy-deployment.md#access-logging).

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `vector.enabled` | bool | `true` | Run the Vector sidecar on each HAProxy pod. When enabled, the access log goes to `vector.socketPath` instead of stdout and surfaces under `kubectl logs <pod> -c vector`. Set to `false` to log straight to the HAProxy container's stdout and scrape HAProxy (and the hub) directly |
| `vector.image.repository` | string | `timberio/vector` | Vector image repository |
| `vector.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `vector.image.tag` | string | `0.57.0-debian` | Pinned Vector version. Renovate bumps it; keep the `# renovate:` comment above the value or tracking stops. An empty tag is rejected — a floating tag would change the log pipeline under a running fleet |
| `vector.metricsPort` | int | `9598` | Port serving the merged `/metrics` (HAProxy's exporter, the hub's metrics, and Vector's own). Rejected at render time if it collides with an `haproxy.ports.*` entry or the hub's metrics port |
| `vector.socketPath` | string | `/run/vector/haproxy.sock` | Unix datagram socket HAProxy writes access-log records to. Must be an absolute path with no whitespace — HAProxy's `log <path>` form requires one |
| `vector.omitEmptyLogFields` | bool | `true` | Strip access-log fields whose value is the empty string, so a record carries only what actually happened. A feature that didn't fire still costs its field on every line: on a measured fleet `trace_id`, `denied_by`, `waf_matched_var`, `consumer` and `cache` were empty in 100% of records, and dropping every empty made records **27% smaller** (815 to 595 bytes average). Sending nothing rather than an empty value is what Elastic Common Schema and OpenTelemetry both recommend. HAProxy can't do it — its JSON encoder has no omit option and the closest one (`+M`) substitutes `-` instead — so Vector strips them in a `remap` transform that rewrites the line rather than re-encoding it, which preserves field order (re-encoding sorts keys alphabetically and would bury `ts`). Numbers are untouched: a genuine `queue_time_ms: 0` is kept. Set to `false` if you feed a strongly typed index, or have queries written as `field == ""`, where a stable field set matters more than the bytes |
| `vector.excludeMaintServerMetrics` | bool | `true` | Add HAProxy's `?no-maint` scrape parameter, omitting servers in `MAINT` state. This is the largest single reduction in scrape size, because in HAPTIC's model `MAINT` means a reserved slot with no backing pod: backends render a fixed number of slots so scaling needs no reload, and each spare slot still emits a full metric set of zeros. On a measured 2-pod fleet, 159 of 193 slots were empty — 10,176 of 15,116 series (67%) and 1.15 MB of every 1.67 MB scrape. No metric **name** disappears, so name-selecting dashboards and rules keep working, and `haproxy_backend_agg_server_status{state="MAINT"}` still reports the free-slot count per backend (which is why `haproxy_backend_agg_*` isn't in `excludeMetrics` by default). Set to `false` if you alert on an individual drained server. Honoured on HAProxy 3.0–3.4 |
| `vector.excludeMetrics` | list | drops `haproxy_*_max_*` | Regular expressions matched against the **metric name**; matching families are dropped before re-export. The default removes HAProxy's host-computed maxima, a Prometheus anti-pattern — they can't be aggregated across pods, and a "since the process started" maximum never resets, so it reports the all-time worst value forever. Use `max_over_time(haproxy_*_current_sessions[1h])` instead: a windowed max, from series that are already exported. Two aggregates are deliberately **kept**: `haproxy_backend_agg_*`, because with `excludeMaintServerMetrics` on it's the only remaining free-slot census, and `haproxy_*_average_seconds`, because HAProxy's exporter publishes no histogram, summary, `_sum` or `_count`, so those averages are its only latency signal. Set to `[]` to re-export everything. Patterns may not contain quotes, backslashes or newlines — they're embedded in the rendered Vector config, and an invalid one fails Vector's config load and crash-loops the sidecar |
| `vector.scrapeIntervalSecs` | int | `15` | How often Vector scrapes the HAProxy and hub endpoints it re-exports. Keep at or below Prometheus's own interval, or Prometheus samples a value Vector hasn't refreshed |
| `vector.resources.requests.cpu` | string | `50m` | CPU request for the Vector container |
| `vector.resources.requests.memory` | string | `64Mi` | Memory request for the Vector container |
| `vector.resources.limits.memory` | string | `256Mi` | Memory limit for the Vector container |
| `vector.securityContext.allowPrivilegeEscalation` | bool | `false` | Container security context for Vector |
| `vector.securityContext.readOnlyRootFilesystem` | bool | `true` | Read-only root filesystem; Vector's writable paths are the `data_dir` and `/tmp` emptyDir volumes |
| `vector.securityContext.runAsNonRoot` | bool | `true` | Refuse to run as root |
| `vector.securityContext.capabilities.drop` | list | `[ALL]` | Linux capabilities to drop |
| `vector.podMonitor.enabled` | bool | `false` | Create a `PodMonitor` for the merged endpoint. Requires the prometheus-operator CRDs. Enabling it replaces the two endpoints the spoa-hub `PodMonitor` declares with one target per pod, and lets `spoaHub.hub.metricsAddr` stay loopback-only |
| `vector.podMonitor.interval` | string | `30s` | Scrape interval |
| `vector.podMonitor.scrapeTimeout` | string | `10s` | Scrape timeout |
| `vector.podMonitor.labels` | map | `{}` | Extra labels on the `PodMonitor` (for a Prometheus `podMonitorSelector`) |
| `vector.podMonitor.relabelings` | list | `[]` | `relabelings` applied to the endpoint |
| `vector.podMonitor.metricRelabelings` | list | `[]` | `metricRelabelings` applied to the endpoint |
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
| `controller.networkPolicy.egress.haproxyPods.enabled` | bool | `true` | Allow controller egress to HAProxy Dataplane API pods (release namespace unless `namespaceSelector` is set) |
| `controller.networkPolicy.egress.haproxyPods.podSelector` | map | See values.yaml | Pod-label selector matching the HAProxy pods to reach |
| `controller.networkPolicy.egress.haproxyPods.namespaceSelector` | map | `{}` | Namespace selector. **`{}` emits no selector**, restricting the rule to the release namespace — set `matchLabels` to reach HAProxy pods in other namespaces |
| `controller.networkPolicy.egress.additionalRules` | list | See values.yaml | Additional egress rules; the chart default allows egress to every in-cluster pod (keeps `http.Fetch()` working) — set `[]` to lock down |
| `controller.networkPolicy.ingress.monitoring.enabled` | bool | `false` | Allow Prometheus scraping |
| `controller.networkPolicy.ingress.monitoring.podSelector` | map | `{}` | Prometheus pod selector. **`{}` means every pod** — set `matchLabels` to identify your Prometheus deployment |
| `controller.networkPolicy.ingress.monitoring.namespaceSelector` | map | `{}` | Prometheus namespace selector. **`{}` emits no selector**, so only same-namespace scrapers match — set `matchLabels` to admit your monitoring namespace |
| `controller.networkPolicy.ingress.healthChecks.enabled` | bool | `true` | Allow health check access |
| `controller.networkPolicy.ingress.healthChecks.from` | list | `[{podSelector: {}}]` | NetworkPolicy peers allowed to reach the health port (`controller.ports.healthz`). The default `podSelector: {}` admits every pod in the release namespace |
| `controller.networkPolicy.ingress.dataplaneApi.enabled` | bool | `true` | Allow Dataplane API access (the policy selects all release pods, HAProxy included, so this rule is what lets the controller push configs) |
| `controller.networkPolicy.ingress.dataplaneApi.from` | list | `[{podSelector: {}}]` | NetworkPolicy peers allowed to reach the Dataplane API port (`haproxy.ports.dataplane`). The default admits every pod in the release namespace, which covers the controller |
| `controller.networkPolicy.ingress.webhook.enabled` | bool | `true` | Allow webhook access |
| `controller.networkPolicy.ingress.webhook.from` | list | IPv4+IPv6 `ipBlock` catch-alls | NetworkPolicy peers allowed to reach the webhook port (`controller.ports.webhook`). Defaults to `ipBlock` catch-alls because the kube-apiserver runs host-network on most distributions — a pod/namespace selector would silently fail to match it and the webhook would return 502 errors. Both `0.0.0.0/0` and `::/0` appear because `ipBlock.cidr` is single-family. Tighten to your apiserver/node CIDRs for production |
| `controller.networkPolicy.ingress.additionalRules` | list | `[]` | Additional ingress rules |

## See also

- [Deploying with Helm](./deploying-with-helm.md) — install, upgrade, and a task-based tour of the chart
- [Template Libraries](./template-libraries.md) — what each `controller.templateLibraries.*` toggle loads
- [CRD Reference](./crd-reference.md) — every field of the `HAProxyTemplateConfig` the chart renders from `controller.config`
