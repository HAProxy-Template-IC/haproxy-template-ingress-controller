# Chart values reference

Every Helm value the chart accepts, with its type and default.

## Deployment & Image

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `replicaCount` | int | `2` | Number of controller replicas (2+ recommended for HA with leader election) |
| `haproxyVersion` | string | `"3.4"` | HAProxy major.minor series. Drives both the controller image tag suffix (`:<version>-haproxy<haproxyVersion>`) and — combined with `haproxyPatchVersions` — the HAProxy pod image tag |
| `haproxyPatchVersions` | map | See values.yaml | Per-`haproxyVersion` community patch pins (for example `"3.2": "3.2.x"`). Maintained by the chart and auto-updated by Renovate |
| `haproxyEnterprisePatchVersions` | map | See values.yaml | Per-`haproxyVersion` enterprise revision pins (for example `"3.2": "3.2r1"`). Used when `haproxy.enterprise.enabled=true` |
| `image.repository` | string | `registry.gitlab.com/haproxy-haptic/haptic` | Controller image repository |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `image.tag` | string | `""` | Controller image tag; empty = `<chart appVersion>-haproxy<haproxyVersion>` |
| `nameOverride` | string | `""` | Override chart name |
| `fullnameOverride` | string | `""` | Override full release name |
| `commonLabels` | map | `{}` | Labels added to every chart-rendered resource on top of the standard `app.kubernetes.io/*` set |
| `commonAnnotations` | map | `{}` | Annotations added to every chart-rendered resource that has an `annotations` block |
| `deploymentAnnotations` | map | `{}` | Annotations added only to the controller `Deployment` (in addition to `commonAnnotations`); useful for hooks like reloader's `reloader.stakater.com/auto: "true"` |
| `extraDeploy` | list or map | `{}` | Free-form Kubernetes resources to render alongside the chart. Each entry is rendered through `tpl` so it can reference chart values. Map form (keys → manifests) is convenient for composing across multiple values files |

## Controller core

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.crdName` | string | `haptic-config` | Name of HAProxyTemplateConfig CRD resource |
| `controller.debugPort` | int | `8080` | Introspection HTTP server port (/healthz, /debug/*) |
| `controller.logLevel` | string | `INFO` | Initial controller log level (`LOG_LEVEL` env var) — see [Logging and templating](#logging-and-templating) for the runtime override |
| `controller.ports.healthz` | int | `8080` | Health check endpoint port |
| `controller.ports.metrics` | int | `9090` | Prometheus metrics endpoint port |
| `controller.ports.webhook` | int | `9443` | Admission webhook HTTPS port |
| `controller.statusPatches.enabled` | bool | `true` | Whether the controller writes LoadBalancer addresses back to Ingress/Gateway `.status`. Disable during a controller migration so the incumbent keeps owning status — the chart sets `extraContext.statusPatchesDisabled: true` and the status-patch snippets become no-ops |

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
| `controller.config.routing.regexMatchOrder` | string | `default` | Path matching order: `default` (Exact > Regex > Prefix-exact > Prefix) or `last` (Exact > Prefix-exact > Prefix > Regex, performance-first) |

## Default SSL certificate

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.defaultSSLCertificate.enabled` | bool | `true` | Enable default SSL certificate requirement. When the cert-manager API is absent and no inline cert is set, the chart generates a self-signed Secret (never touching an existing one) so the install converges out of the box |
| `controller.defaultSSLCertificate.secretName` | string | `default-ssl-cert` | TLS Secret name containing certificate |
| `controller.defaultSSLCertificate.namespace` | string | `""` | Secret namespace (defaults to `Release.Namespace`) |
| `controller.defaultSSLCertificate.certManager.enabled` | bool | `true` | Use cert-manager for certificate provisioning |
| `controller.defaultSSLCertificate.certManager.createIssuer` | bool | `true` | Create self-signed Issuer (dev/test only) |
| `controller.defaultSSLCertificate.certManager.dnsNames` | list | `["localdev.me", "*.localdev.me"]` | DNS names for the certificate |
| `controller.defaultSSLCertificate.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `controller.defaultSSLCertificate.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `controller.defaultSSLCertificate.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `controller.defaultSSLCertificate.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `controller.defaultSSLCertificate.create` | bool | `false` | Create Secret from inline cert/key (testing only) |
| `controller.defaultSSLCertificate.cert` | string | `""` | PEM certificate (when create=true) |
| `controller.defaultSSLCertificate.key` | string | `""` | PEM private key (when create=true) |

## Controller Config

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.credentialsSecretRef.name` | string | Auto-generated | Secret containing Dataplane API credentials |
| `controller.config.credentialsSecretRef.namespace` | string | `""` | Credentials secret namespace |
| `controller.config.podSelector.matchLabels` | map | `{app.kubernetes.io/component: loadbalancer}` | Labels to match HAProxy pods |
| `controller.config.controller.healthzPort` | int | `8080` | **Display only** — the chart strips this from the serialized CRD. The actual health-check port is set by the container port `controller.ports.healthz` and the controller doesn't read this field |
| `controller.config.controller.metricsPort` | int | `9090` | **Display only** — the chart strips this from the serialized CRD. The actual metrics port is set by the `METRICS_PORT` env var (default `9090`); set `METRICS_PORT=0` via the top-level `extraEnv` to disable the metrics server. `controller.ports.metrics` declares the matching container port and Service port |

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
| `controller.config.dataplane.port` | int | `5555` | Dataplane API port |
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
| `controller.config.templatingSettings.extraContext.debug` | bool | `true` | Enable debug headers in HAProxy responses |
| `controller.config.templatingSettings.extraContext.password_hash_validation_regex` | string | `"^.*$"` | Regex every password hash in a basic-auth Secret must match (the `auth-secret` annotation handlers in the haproxytech and haproxy-ingress libraries). A non-matching hash fails the render with `password_hash_validation_error_message`; the default accepts all hashes. Example restricting to MD5-crypt (apr1) hashes: `"^\$apr1\$"`. Go RE2 syntax — no lookaheads, so express the policy as the *allowed* format |
| `controller.config.templatingSettings.extraContext.password_hash_validation_error_message` | string | `Invalid password hash` | Error message emitted when a password hash fails validation; the rendered error appends the username, Secret name, and pattern |
| `controller.config.templatingSettings.extraContext.coraza.dispatch.mode` | string | `opt-in` | Which requests the rendered config ships to the Coraza Web Application Firewall (WAF): `opt-in` runs it only on paths carrying a `modsecurity-snippet` or `waf: "modsecurity"` annotation; `default-on` runs it on every request unless the path opts out via `nginx.ingress.kubernetes.io/enable-modsecurity: "false"`. Changes the rendered haproxy.cfg only — the SPOA hub plugin itself is configured via `spoaHub.plugins.coraza.*` |
| `controller.config.templatingSettings.extraContext.coraza.dispatch.defaultEnforcement` | string | `deny` | WAF enforcement (`deny` or `detect`) for requests dispatched by `mode: default-on`; a per-path `haproxy-ingress.github.io/waf-mode: "detect"` annotation overrides it. Ignored when `mode: opt-in` |
| `controller.config.watchedResourcesIgnoreFields` | list | `[metadata.managedFields, metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']]` | Fields to ignore in watched resources |

## Webhook Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `webhook.enabled` | bool | `true` | Enable admission webhook validation |
| `webhook.haproxyTemplateConfig.enabled` | bool | `true` | Also validate `HAProxyTemplateConfig` CRD updates via the webhook (`failurePolicy: Ignore`, not configurable — controller downtime never blocks CRD edits). When active, the leader-side reconcile can skip `haproxy -c` on every render |
| `webhook.secretName` | string | Auto-generated | Webhook TLS certificate secret name |
| `webhook.service.port` | int | `443` | Webhook service port |
| `webhook.certManager.enabled` | bool | `false` | cert-manager integration for the webhook cert. Default `false`: the chart issues a self-signed cert itself. Set `true` for cert-manager-managed issuance and auto-rotation; for manual certs keep `false` and set `webhook.caBundle` |
| `webhook.certManager.createIssuer` | bool | `true` | Create a self-signed Issuer for webhook certs |
| `webhook.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `webhook.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `webhook.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `webhook.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `webhook.selfSigned.certValidityDays` | int | `3650` | Validity in days of the chart-generated self-signed webhook cert (used when `certManager.enabled=false` and `caBundle` is empty). Long by default because the chart doesn't auto-rotate it: the cert is generated once and reused across upgrades via `lookup`. Rotate manually by deleting the Secret (`webhook.secretName`) and running `helm upgrade`, or use cert-manager for automatic rotation |
| `webhook.caBundle` | string | `""` | Base64-encoded CA bundle (manual certs) |

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
| `gatewayClass.parametersRef.name` | string | `""` | Config name (defaults to controller.crdName) |
| `gatewayClass.parametersRef.namespace` | string | `""` | Config namespace (defaults to `Release.Namespace`) |

## Credentials

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `credentials.dataplane.username` | string | `admin` | Dataplane API username |
| `credentials.dataplane.password` | string | `""` | Dataplane API password. Empty generates a random 32-char password. When `lookup` works (a normal `helm upgrade`, or an install against a reachable cluster) the chart reads the existing Secret and preserves the current password across renders. GitOps tools that render without cluster access (ArgoCD/Flux) can't `lookup`, so an empty value regenerates every sync — **set an explicit value** (SealedSecret / external secret) in those setups. |

## ServiceAccount & RBAC

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `serviceAccount.create` | bool | `true` | Create ServiceAccount |
| `serviceAccount.automount` | bool | `true` | Automount API credentials |
| `serviceAccount.annotations` | map | `{}` | ServiceAccount annotations |
| `serviceAccount.name` | string | `""` | ServiceAccount name (auto-generated if empty) |
| `rbac.create` | bool | `true` | Create RBAC resources |

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
| `securityContext.allowPrivilegeEscalation` | bool | `false` | Allow privilege escalation |
| `securityContext.capabilities.drop` | list | `[ALL]` | Dropped capabilities |
| `securityContext.readOnlyRootFilesystem` | bool | `true` | Read-only root filesystem |
| `securityContext.runAsNonRoot` | bool | `true` | Run as non-root |
| `securityContext.runAsUser` | int | `65532` | Container user ID |

## Service & health probes

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service.type` | string | `ClusterIP` | Controller service type |
| `service.annotations` | map | `{}` | Service annotations (cloud-LB hints, etc.) |
| `service.clusterIP` | string | `""` | Pin a specific ClusterIP; leave empty for auto-assignment |
| `service.loadBalancerIP` | string | `""` | LoadBalancer IP (when `type: LoadBalancer`) |
| `service.loadBalancerSourceRanges` | list | `[]` | CIDR allowlist for LoadBalancer traffic |
| `service.loadBalancerClass` | string | `""` | LoadBalancer class (Kubernetes 1.24+ multi-LB) |
| `service.externalTrafficPolicy` | string | `""` | `Cluster` or `Local`; `Local` preserves client source IP at the cost of uneven distribution |
| `service.internalTrafficPolicy` | string | `""` | `Cluster` or `Local` for in-cluster traffic |
| `service.sessionAffinity` | string | `""` | `None` or `ClientIP` |
| `service.sessionAffinityConfig` | map | `{}` | Session-affinity tuning (when `sessionAffinity: ClientIP`) |
| `livenessProbe.httpGet.path` | string | `/healthz` | Liveness probe path |
| `livenessProbe.httpGet.port` | string | `healthz` | Named container port the probe targets (declared by `controller.ports.healthz`) |
| `livenessProbe.initialDelaySeconds` | int | `10` | Initial delay |
| `livenessProbe.periodSeconds` | int | `10` | Probe period |
| `livenessProbe.failureThreshold` | int | `3` | Failure threshold |
| `readinessProbe.httpGet.path` | string | `/healthz` | Readiness probe path |
| `readinessProbe.httpGet.port` | string | `healthz` | Named container port the probe targets |
| `readinessProbe.initialDelaySeconds` | int | `5` | Initial delay |
| `readinessProbe.periodSeconds` | int | `5` | Probe period |
| `readinessProbe.failureThreshold` | int | `3` | Failure threshold |
| `startupProbe.enabled` | bool | `true` | Enable the startup probe; liveness/readiness probes are paused until it succeeds. On by default and load-bearing: controller startup runs the config's embedded `validationTests` (dozens of `haproxy -c` checks plus a full template compile), which can exceed the bare liveness budget on slow nodes and crash-loop the pod |
| `startupProbe.httpGet.path` | string | `/healthz` | Startup probe path |
| `startupProbe.httpGet.port` | string | `healthz` | Named container port the probe targets |
| `startupProbe.initialDelaySeconds` | int | `0` | Initial delay |
| `startupProbe.periodSeconds` | int | `10` | Probe period |
| `startupProbe.timeoutSeconds` | int | `1` | Probe timeout |
| `startupProbe.successThreshold` | int | `1` | Success threshold |
| `startupProbe.failureThreshold` | int | `30` | Failure threshold (with `periodSeconds: 10` this gives 5 minutes for startup) |

## Resources & scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `resources.requests.cpu` | string | `100m` | CPU request |
| `resources.requests.memory` | string | `512Mi` | Memory request (Guaranteed QoS — matches `limits.memory`) |
| `resources.limits.memory` | string | `512Mi` | Memory limit |

Pod-level scheduling fields (`nodeSelector`, `tolerations`, `affinity`, etc.) live under `controller.podSpec.*` — see [Pod configuration (controller)](#pod-configuration-controller).

## Controller extras & rollout

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `extraEnv` | list | `[]` | Extra env vars for the controller container; `AUTOMEMLIMIT=…` here adjusts the GOMEMLIMIT ratio (default `0.9`) |
| `extraVolumes` | list | `[]` | Extra volumes for the controller pod; rendered through `tpl` so values can reference chart values |
| `extraVolumeMounts` | list | `[]` | Extra volume mounts for the controller container; rendered through `tpl` |
| `initContainers` | list | `[]` | Init containers run before the controller starts |
| `sidecars` | list | `[]` | Additional sidecar containers in the controller pod; rendered through `tpl` |
| `lifecycle` | map | `{}` | Container lifecycle hooks (`preStop`, `postStart`) for the controller container |
| `updateStrategy.type` | string | `RollingUpdate` | Controller Deployment update strategy |
| `updateStrategy.rollingUpdate.maxSurge` | int/string | `25%` | Maximum surge during rolling updates |
| `updateStrategy.rollingUpdate.maxUnavailable` | int/string | `25%` | Maximum unavailable during rolling updates |
| `minReadySeconds` | int | `0` | Minimum seconds a new controller pod must be ready before counting as available |
| `revisionHistoryLimit` | int | `10` | Number of old ReplicaSets to retain |

## Autoscaling & PDB

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `autoscaling.enabled` | bool | `false` | Enable HorizontalPodAutoscaler |
| `autoscaling.minReplicas` | int | `1` | Minimum replicas |
| `autoscaling.maxReplicas` | int | `10` | Maximum replicas |
| `autoscaling.targetCPUUtilizationPercentage` | int | `80` | Target CPU utilization (omitted from the rendered HPA when empty) |
| `autoscaling.targetMemoryUtilizationPercentage` | int | unset | Target memory utilization (omitted from the rendered HPA when empty) |
| `podDisruptionBudget.enabled` | bool | `true` | Enable PodDisruptionBudget; only rendered when `replicaCount > 1` |
| `podDisruptionBudget.minAvailable` | int/string | `1` | Minimum available pods (mutually exclusive with `maxUnavailable`) |
| `podDisruptionBudget.maxUnavailable` | int/string | unset | Maximum unavailable pods (mutually exclusive with `minAvailable`); leave unset to use `minAvailable` |

## Monitoring

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `monitoring.serviceMonitor.enabled` | bool | `false` | Create ServiceMonitor for Prometheus |
| `monitoring.serviceMonitor.interval` | duration | `30s` | Scrape interval |
| `monitoring.serviceMonitor.scrapeTimeout` | duration | `10s` | Scrape timeout |
| `monitoring.serviceMonitor.labels` | map | `{}` | ServiceMonitor labels (used by Prometheus to select which ServiceMonitors to use) |
| `monitoring.serviceMonitor.relabelings` | list | `[]` | Prometheus relabelings applied before scraping |
| `monitoring.serviceMonitor.metricRelabelings` | list | `[]` | Metric relabelings applied to scraped metrics |
| `monitoring.podMonitor.enabled` | bool | `false` | Create PodMonitor (alternative to ServiceMonitor when scraping pods directly) |
| `monitoring.podMonitor.interval` | duration | `30s` | PodMonitor scrape interval |
| `monitoring.podMonitor.scrapeTimeout` | duration | `10s` | PodMonitor scrape timeout |
| `monitoring.podMonitor.labels` | map | `{}` | PodMonitor labels |
| `monitoring.podMonitor.relabelings` | list | `[]` | PodMonitor relabelings |
| `monitoring.podMonitor.metricRelabelings` | list | `[]` | PodMonitor metric relabelings |
| `monitoring.prometheusRule.enabled` | bool | `false` | Create PrometheusRule with alerting rules |
| `monitoring.prometheusRule.labels` | map | `{}` | PrometheusRule labels |
| `monitoring.prometheusRule.rules` | list | `[]` | Custom alerting rules; overrides the default rule set when non-empty |
| `monitoring.prometheusRule.defaultRules.enabled` | bool | `true` | Emit the chart's default rule set — nine alerts, each individually toggleable below; only consulted when `rules` is empty |
| `monitoring.prometheusRule.defaultRules.reconciliationErrors` | bool | `true` | Include the `HAProxyControllerReconciliationErrors` warning rule |
| `monitoring.prometheusRule.defaultRules.deploymentFailures` | bool | `true` | Include the `HAProxyControllerDeploymentFailures` critical rule |
| `monitoring.prometheusRule.defaultRules.highQueueDepth` | bool | `true` | Include the `HAProxyControllerHighQueueDepth` warning rule |
| `monitoring.prometheusRule.defaultRules.leaderElectionLost` | bool | `true` | Include the `HAProxyControllerNoLeader` critical rule |
| `monitoring.prometheusRule.defaultRules.fleetDiverged` | bool | `true` | Include the `HAProxyFleetDiverged` warning rule: some HAProxy pods haven't converged to the desired config for 5 minutes. Transient deploy failures self-heal, so sustained divergence is a real fault — this is the noise-free replacement for alerting on raw deployment errors |
| `monitoring.prometheusRule.defaultRules.configRejected` | bool | `true` | Include the `HAProxyControllerConfigRejected` warning rule: the validation gate rejected a config change — the controller keeps serving the last-good config and the latest change isn't live |
| `monitoring.prometheusRule.defaultRules.haproxyPodsRejected` | bool | `true` | Include the `HAProxyControllerHAProxyPodsRejected` warning rule: discovered HAProxy pods refused admission (often a HAProxy major.minor mismatch with `haproxyVersion`) |
| `monitoring.prometheusRule.defaultRules.noHAProxyPods` | bool | `true` | Include the `HAProxyControllerNoHAProxyPods` critical rule: the controller finds no HAProxy pods to manage, so no config reaches the data plane |
| `monitoring.prometheusRule.defaultRules.criticalEventsDropped` | bool | `true` | Include the `HAProxyControllerCriticalEventsDropped` critical rule: a critical event-bus subscriber's buffer overflowed — reconciliation work was lost and the data plane may be stale |
| `monitoring.grafanaDashboard.enabled` | bool | `false` | Create a ConfigMap holding the Grafana dashboard JSON (picked up by the Grafana sidecar via the configured discovery label) |
| `monitoring.grafanaDashboard.labels` | map | `{grafana_dashboard: "1"}` | Discovery labels for the Grafana sidecar |
| `monitoring.grafanaDashboard.annotations` | map | `{grafana_folder: "HAProxy"}` | Annotations on the dashboard ConfigMap; the folder annotation must match `grafana.sidecar.dashboards.folderAnnotation` |
| `monitoring.grafanaDashboard.namespace` | string | `""` | Namespace for the dashboard ConfigMap (defaults to release namespace) |
| `monitoring.grafanaDashboard.useBuiltIn` | bool | `true` | Use the built-in dashboard (curated subset of controller metrics); set `false` to provide your own JSON via `customDashboard` |
| `monitoring.grafanaDashboard.customDashboard` | map | `{}` | Custom dashboard JSON, only consulted when `useBuiltIn: false` |

## HAProxy Deployment

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.enabled` | bool | `true` | Deploy HAProxy pods with this chart |
| `haproxy.replicaCount` | int | `2` | Number of HAProxy replicas |
| `haproxy.image.repository` | string | `haproxytech/haproxy-debian` | HAProxy image repository |
| `haproxy.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `haproxy.image.tag` | string | `""` | HAProxy image tag; empty = derive from `haproxyVersion` plus the matching entry in `haproxyPatchVersions` (for example `3.2` → whichever 3.2.x patch the chart currently pins). Override to pin a specific patch yourself. |
| `haproxy.enterprise.enabled` | bool | `false` | Use HAProxy Enterprise |
| `haproxy.enterprise.version` | string | `3.2` | Enterprise version |
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
| `haproxy.ports.dataplane` | int | `5555` | Dataplane API port |

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
| `haproxy.nbthread` | int/string | absent | HAProxy `nbthread` directive value. Default (key absent): auto-calculated as `ceil(cpu_request)` clamped to ≥1. Set `0` to skip the directive (HAProxy uses its built-in default). Templatable — `"{{ mul 2 (...) }}"` is allowed |
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
| `spoaHub.hub.logLevel` | string | `info` | Hub log level (`info`/`debug`/`warn`/`error`) |
| `spoaHub.hub.workerThreads` | int/null | `null` | Tokio worker thread count; `null` defaults to CPU count |
| `spoaHub.hub.maxConnections` | int | `1000` | Maximum concurrent connections per listener |
| `spoaHub.hub.blockingThreadKeepAliveSecs` | int | `30` | Keep-alive seconds for blocking-thread workers |
| `spoaHub.hub.metricsAddr` | string | `127.0.0.1:9095` | Hub Prometheus `/metrics` listen address. Loopback by default — the HAProxy container scrapes it via the shared pod network namespace. Set `""` to disable the endpoint (loses per-plugin counters) or `0.0.0.0:9095` to expose it cluster-wide (the metrics carry per-Ingress/route cardinality) |
| `spoaHub.haproxy.socketPath` | string | `/run/spoa/hub.sock` | Unix socket path shared between HAProxy and the hub |
| `spoaHub.haproxy.modeSpop` | bool | `true` | Use HAProxy 3.1+ `mode spop` backend; auto-falls back to `mode tcp` on 3.0. Set `false` to force `mode tcp` on 3.1+ |
| `spoaHub.haproxy.timeoutHello` | duration | `2s` | Stream Processing Offload Engine (SPOE) hello timeout |
| `spoaHub.haproxy.timeoutIdle` | duration | `5m` | SPOE idle timeout |
| `spoaHub.haproxy.timeoutProcessing` | duration | `500ms` | Per-message processing timeout |
| `spoaHub.haproxy.poolMaxConn` | int | `100` | Connection pool maximum |
| `spoaHub.haproxy.poolPurgeDelay` | duration | `30s` | Idle-connection purge delay |
| `spoaHub.mirrorStaticMinSlots` | int | `4` | Static floor for the mirror plugin's SPOE message slot count; the rendered count is the larger of this floor and the biggest per-rule mirror-filter fan-out in the cluster. The floor is also the slot capacity for nginx-ingress `mirror-target` Ingresses (one slot each), and it keeps hub handler state registered across reloads when a transient render shrinks the slot set. Raise it for more than 4 mirrors per HTTPRoute rule or more than 4 mirror-target Ingresses |
| `spoaHub.plugins.<name>.enabled` | bool/string | `'{{ false }}'` (templatable) | Per-plugin enable. Default value is a chart-evaluated `tpl` string so a plugin can auto-enable when the template libraries that rely on it are on; explicit `--set` bool always wins |
| `spoaHub.plugins.<name>.timeoutMs` | int | per-plugin | Plugin processing timeout in milliseconds |
| `spoaHub.plugins.<name>.messages` | list | per-plugin | SPOE messages this plugin handles |
| `spoaHub.plugins.<name>.dependsOn` | list | `[]` | Other plugin names this plugin must run after |
| `spoaHub.plugins.<name>.params` | string | per-plugin | Free-form TOML blob spliced verbatim under `[plugins.params]` — use dotted keys (`x.y = "..."`) or fully qualified headers (`[plugins.params.x]`) for nested values; bare `[x]` headers close the params scope and break the config |
| `spoaHub.plugins.coraza.directives` | string | OWASP CRS includes + `SecRuleEngine On` | Coraza WAF rule directives (coraza only). Rendered into the hub TOML's own `directives` field, with per-Ingress `modsecurity-snippet` values appended after it (sorted by `<ns>/<name>`). Keep `SecRuleEngine On` *after* the includes — `@coraza.conf-recommended` sets `DetectionOnly`, so an earlier `On` is silently overridden and the WAF never blocks. Don't also set `directives` inside `params:`; the duplicate TOML field breaks the config |
| `spoaHub.securityContext` | map | See values.yaml | Container security context for the spoa-hub container. Default runs user and group 99, matching the pod's `fsGroup`, so the Unix socket the hub creates under `/run/spoa` is accessible to the HAProxy container; read-only root filesystem, no privilege escalation, all capabilities dropped |
| `spoaHub.extraVolumeMounts` | list | `[]` | Extra volume mounts added to the spoa-hub container only (rendered through `tpl`) — for MMDB files (`maxmind`), OpenID Connect (OIDC) client secrets (`sso-auth`), and similar plugin data |

Available plugin names (`<name>`): `coraza`, `external-auth`, `fingerprinting`, `maxmind`, `mirror`, `otel`, `sso-auth`. See `values.yaml` for each plugin's default `messages` / `timeoutMs` and the upstream plugin README for the `params:` schema.

## HAProxy resources & scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.resources.requests.cpu` | string | `250m` | CPU request |
| `haproxy.resources.requests.memory` | string | `1Gi` | Memory request (Guaranteed QoS — limits.memory matches) |
| `haproxy.resources.limits.memory` | string | `1Gi` | Memory limit |

No CPU limit is set by default to avoid throttling; HAProxy's `nbthread` auto-calculates from the CPU request.

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
| `networkPolicy.enabled` | bool | `true` | Enable controller NetworkPolicy |
| `networkPolicy.egress.allowDNS` | bool | `true` | Allow DNS resolution |
| `networkPolicy.egress.kubernetesApi` | list | See values.yaml | Kubernetes API access rules |
| `networkPolicy.egress.haproxyPods.enabled` | bool | `true` | Allow controller egress to HAProxy Dataplane API pods (release namespace unless `namespaceSelector` is set) |
| `networkPolicy.egress.haproxyPods.podSelector` | map | See values.yaml | Pod-label selector matching the HAProxy pods to reach |
| `networkPolicy.egress.haproxyPods.namespaceSelector` | map | `{}` | Namespace selector. **`{}` emits no selector**, restricting the rule to the release namespace — set `matchLabels` to reach HAProxy pods in other namespaces |
| `networkPolicy.egress.additionalRules` | list | See values.yaml | Additional egress rules; the chart default allows egress to every in-cluster pod (keeps `http.Fetch()` working) — set `[]` to lock down |
| `networkPolicy.ingress.monitoring.enabled` | bool | `false` | Allow Prometheus scraping |
| `networkPolicy.ingress.monitoring.podSelector` | map | `{}` | Prometheus pod selector. **`{}` means every pod** — set `matchLabels` to identify your Prometheus deployment |
| `networkPolicy.ingress.monitoring.namespaceSelector` | map | `{}` | Prometheus namespace selector. **`{}` emits no selector**, so only same-namespace scrapers match — set `matchLabels` to admit your monitoring namespace |
| `networkPolicy.ingress.healthChecks.enabled` | bool | `true` | Allow health check access |
| `networkPolicy.ingress.healthChecks.from` | list | `[{podSelector: {}}]` | NetworkPolicy peers allowed to reach the health port (`controller.ports.healthz`). The default `podSelector: {}` admits every pod in the release namespace |
| `networkPolicy.ingress.dataplaneApi.enabled` | bool | `true` | Allow Dataplane API access (the policy selects all release pods, HAProxy included, so this rule is what lets the controller push configs) |
| `networkPolicy.ingress.dataplaneApi.from` | list | `[{podSelector: {}}]` | NetworkPolicy peers allowed to reach the Dataplane API port (`haproxy.ports.dataplane`). The default admits every pod in the release namespace, which covers the controller |
| `networkPolicy.ingress.webhook.enabled` | bool | `true` | Allow webhook access |
| `networkPolicy.ingress.webhook.from` | list | IPv4+IPv6 `ipBlock` catch-alls | NetworkPolicy peers allowed to reach the webhook port (`controller.ports.webhook`). Defaults to `ipBlock` catch-alls because the kube-apiserver runs host-network on most distributions — a pod/namespace selector would silently fail to match it and the webhook would return 502 errors. Both `0.0.0.0/0` and `::/0` appear because `ipBlock.cidr` is single-family. Tighten to your apiserver/node CIDRs for production |
| `networkPolicy.ingress.additionalRules` | list | `[]` | Additional ingress rules |

## See also

- [Deploying with Helm](./deploying-with-helm.md) — install, upgrade, and a task-based tour of the chart
- [Template Libraries](./template-libraries.md) — what each `controller.templateLibraries.*` toggle loads
- [CRD Reference](./crd-reference.md) — every field of the `HAProxyTemplateConfig` the chart renders from `controller.config`
