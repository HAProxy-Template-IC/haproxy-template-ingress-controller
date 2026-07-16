{{/*
SPOA hub plugin enablement, image reference, ConfigMap name, and
plugin shared-library filename helpers.
*/}}

{{/*
Resolve a single plugin's `enabled` value to true/false. The field is
allowed to be either a literal bool (operator override via values or
--set) OR a templated string (default in values.yaml; the chart evaluates
it with `tpl` so library-driven auto-enable conditions can live in the
default value itself).
Args: dict "plugin" <plugin map> "root" $
*/}}
{{- define "haptic.spoaHub.pluginEnabled" -}}
{{- $val := (default dict .plugin).enabled -}}
{{- if eq (kindOf $val) "bool" -}}
  {{- if $val -}}true{{- end -}}
{{- else if eq (kindOf $val) "string" -}}
  {{- if eq (trim (tpl $val .root)) "true" -}}true{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Resolve a plugin timeout. Like `enabled`, `timeoutMs` may be a literal number
or a templated string in values.yaml. The latter lets feature-level chart values
(for example controller.rateLimit.shared.pluginTimeoutMs) drive the plugin
config while still allowing direct spoaHub plugin overrides.
Args: dict "plugin" <plugin map> "root" $ "name" <plugin name>
*/}}
{{- define "haptic.spoaHub.pluginTimeoutMs" -}}
{{- $plugin := default dict .plugin -}}
{{- $raw := 500 -}}
{{- if hasKey $plugin "timeoutMs" -}}
  {{- $raw = $plugin.timeoutMs -}}
{{- end -}}
{{- $resolved := $raw -}}
{{- if eq (kindOf $raw) "string" -}}
  {{- $resolved = (trim (tpl $raw .root)) -}}
{{- end -}}
{{- $ms := int $resolved -}}
{{- if le $ms 0 -}}
  {{- fail (printf "spoaHub.plugins.%s.timeoutMs must resolve to a positive integer milliseconds value." .name) -}}
{{- end -}}
{{- $ms -}}
{{- end }}

{{/*
Whether the SPOA hub sidecar should be rendered.
True when:
  - spoaHub.enabled is explicitly true, OR
  - spoaHub.enabled is null/empty AND any plugin resolves to enabled.
False when spoaHub.enabled is explicitly false (operator override).
Returns "true" or "" (Helm-truthy convention).
*/}}
{{- define "haptic.spoaHub.enabled" -}}
{{- $root := . -}}
{{- $hub := $root.Values.spoaHub | default dict -}}
{{- if eq (kindOf $hub.enabled) "bool" -}}
  {{- if $hub.enabled -}}true{{- end -}}
{{- else -}}
  {{- /* Track "any plugin enabled" via a mutable wrapper so the answer is
         emitted at most once. Emitting "true" inside the range concatenates
         a separate "true" per enabled plugin (`"truetrue"`, …) which
         compares not-equal to the literal `"true"` that callers like
         libraries/spoa-hub/_index.yaml's `_helm_load.enable` predicate test
         against — silently dropping the spoa-hub library when ≥2 plugins
         are enabled. */}}
  {{- $any := dict "v" false -}}
  {{- range $name, $plugin := $hub.plugins -}}
    {{- if include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $root) -}}
      {{- $_ := set $any "v" true -}}
    {{- end -}}
  {{- end -}}
  {{- if $any.v -}}true{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Inverse of `haptic.spoaHub.enabled` — returns "true" when SPOA hub will NOT
be rendered, empty string otherwise. The library `_helm_skip_test`
predicates use this to skip validation tests that depend on snippets the
spoa-hub library contributes.
*/}}
{{- define "haptic.spoaHub.disabled" -}}
{{- if not (include "haptic.spoaHub.enabled" .) -}}true{{- end -}}
{{- end -}}

{{/*
ConfigMap name for the SPOA hub's config.toml. Used by
templates/spoa-hub-configmap.yaml (metadata.name) and by
templates/haproxy-deployment.yaml (volumes.configMap.name).
*/}}
{{- define "haptic.spoaHub.configMapName" -}}
{{- printf "%s-spoa-hub" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
SPOA hub container image reference.
Uses spoaHub.image.tag if set, otherwise falls back to .Chart.AppVersion.
Example: registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:0.1.0
*/}}
{{- define "haptic.spoaHub.image" -}}
{{- printf "%s:%s" .Values.spoaHub.image.repository (.Values.spoaHub.image.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{/*
Whether the validator sidecar should be rendered.
True when:
  - controller.validators.enabled is explicitly true, OR
  - controller.validators.enabled is null AND `haptic.spoaHub.enabled`
    is truthy (i.e. at least one plugin is on).
False when controller.validators.enabled is explicitly false.
Returns "true" or "" (Helm-truthy convention).
*/}}
{{- define "haptic.validators.enabled" -}}
{{- $val := (default dict (default dict .Values.controller).validators).enabled -}}
{{- if eq (kindOf $val) "bool" -}}
  {{- if $val -}}true{{- end -}}
{{- else -}}
  {{- include "haptic.spoaHub.enabled" . -}}
{{- end -}}
{{- end -}}

{{/*
Validator sidecar Unix-socket path. Concat of
controller.validators.socketDir and controller.validators.socketName.
Used as the shared mountpoint between controller and validator sidecar
and as the dial address the controller writes into spec.validators[].
*/}}
{{- define "haptic.validators.socketPath" -}}
{{- $v := .Values.controller.validators -}}
{{- printf "%s/%s" (trimSuffix "/" $v.socketDir) $v.socketName -}}
{{- end -}}

{{/*
Bootstrap TOML for the SPOA hub sidecar. The sidecar starts with this content
from a ConfigMap, then reloads the controller-rendered runtime TOML from
general storage after the first successful reconciliation.
*/}}
{{- define "haptic.spoaHub.bootstrapConfigContent" -}}
{{- $spoaHub := .Values.spoaHub -}}
{{- $hub := $spoaHub.hub -}}
plugin_dir = "/etc/haproxy-spoa-hub/plugins"
default_timeout_ms = 500
log_level = {{ $hub.logLevel | quote }}
max_connections = {{ $hub.maxConnections }}
blocking_thread_keep_alive_secs = {{ $hub.blockingThreadKeepAliveSecs }}
{{- with $hub.metricsAddr }}
{{- /* Bootstrap-side metrics_addr — mirrors the runtime-rendered
       libraries/spoa-hub/ `metrics_addr` line so the /metrics
       endpoint is bound from process start, not just after the
       controller pushes the first runtime config. The two have to
       stay in sync: the bootstrap is what the sidecar loads on pod
       startup (read-only, mounted from this ConfigMap), and the
       hub's file-watch swaps in the runtime config later. Without
       this, scraping /metrics during early-cluster boot returns
       empty — what bit issue #45's first artifact run. */}}
metrics_addr = {{ . | quote }}
{{- end }}
{{- with $hub.workerThreads }}
worker_threads = {{ . }}
{{- end }}

[[listeners]]
type = "unix"
address = {{ $spoaHub.haproxy.socketPath | quote }}

{{- range $name, $plugin := $spoaHub.plugins }}
{{- if include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $) }}

{{- /* The hub builds SPOP response variable names as `<plugin>.<var>`,
       so the plugin name shows up directly in HAProxy's variable namespace
       (e.g. `txn.<var-prefix>.<plugin>.allowed`). HAProxy variable names
       are restricted identifiers — dashes aren't allowed mid-identifier
       the same way they are in YAML keys, so a TOML name of `external-auth`
       produces a var like `external-auth.allowed` which the deny rule
       can't match. The upstream plugin's example hub.toml uses the
       snake_case form for this reason. Convert dashes to underscores
       here to keep the YAML key kebab-case (idiomatic) while emitting
       a HAProxy-friendly identifier. */}}
[[plugins]]
name = {{ regexReplaceAll "-" $name "_" | quote }}
library = {{ include "haptic.spoaHub.libName" (dict "name" $name) | quote }}
messages = {{ $plugin.messages | toJson }}
timeout_ms = {{ include "haptic.spoaHub.pluginTimeoutMs" (dict "plugin" $plugin "root" $ "name" $name) }}
{{- with $plugin.dependsOn }}
depends_on = {{ . | toJson }}
{{- end }}

[plugins.params]
{{- /* Coraza's directives lives in its own values field so the
       controller-rendered TOML (rendered by libraries/spoa-hub/)
       can append per-Ingress modsecurity-snippet values into it.
       The bootstrap placeholder rendered here is just enough to let
       the hub start; the controller pushes the real config via the
       dataplane API and the hub reloads on file-watch. */}}
{{- if and (eq $name "coraza") $plugin.directives }}
directives = """
{{- $plugin.directives | nindent 0 }}
"""
{{- end }}
{{- with (include "haptic.spoaHub.effectivePluginParams" (dict "root" $ "name" $name "plugin" $plugin)) }}
{{ . | trim }}
{{- end }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
Names and URLs for the chart-managed Valkey store used by the shared
rate-limit plugin. The workload itself is emitted by the haptic-annotations
library through k8sResources so it is owned by the HAProxyTemplateConfig CR.
*/}}
{{- define "haptic.rateLimit.storeServiceName" -}}
{{- printf "%s-rl-store" (include "haptic.fullname" .) | trunc 43 | trimSuffix "-" -}}
{{- end -}}

{{- define "haptic.rateLimit.storeSentinelServiceName" -}}
{{- printf "%s-sentinel" (include "haptic.rateLimit.storeServiceName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "haptic.rateLimit.storeSentinelMasterName" -}}
{{- "haptic-rate-limit" -}}
{{- end -}}

{{- define "haptic.rateLimit.storeSentinelURL" -}}
{{- $root := . -}}
{{- $store := $root.Values.controller.rateLimit.store -}}
{{- $sentinel := $store.sentinel | default dict -}}
{{- $sentinelPort := int ($sentinel.port | default 26379) -}}
{{- $svc := include "haptic.rateLimit.storeSentinelServiceName" $root -}}
{{- $storeSvc := include "haptic.rateLimit.storeServiceName" $root -}}
{{- $master := include "haptic.rateLimit.storeSentinelMasterName" $root -}}
{{- $url := printf "redis-sentinel://%s.%s.svc:%d/0?sentinelServiceName=%s" $svc $root.Release.Namespace $sentinelPort $master -}}
{{- range $i := until (int $store.replicas) -}}
  {{- $node := printf "%s-%d.%s.%s.svc.cluster.local:%d" $storeSvc $i $storeSvc $root.Release.Namespace $sentinelPort -}}
  {{- $url = printf "%s&node=%s" $url $node -}}
{{- end -}}
{{- $url -}}
{{- end -}}

{{- define "haptic.rateLimit.storeURLTOML" -}}
store_url = {{ include "haptic.rateLimit.storeSentinelURL" . | quote }}
{{- end -}}

{{/*
Return the effective params TOML for one spoaHub plugin. Most plugins pass
their values.yaml params through unchanged. rate-limit gets chart-managed
store_url appended when controller.rateLimit.shared.enabled=true and
controller.rateLimit.store.enabled=true, so the bootstrap ConfigMap and the
runtime-rendered hub config stay byte-for-byte consistent about the store
endpoints.
*/}}
{{- define "haptic.spoaHub.effectivePluginParams" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $plugin := default dict .plugin -}}
{{- $rawParams := ($plugin.params | default "") -}}
{{- $params := $rawParams -}}
{{- if and (or (eq $name "rate-limit") (eq $name "api-gateway")) (eq (kindOf $rawParams) "string") -}}
  {{- $params = tpl $rawParams $root -}}
{{- end -}}
{{- $hasStoreURL := regexMatch "(?m)^\\s*store_urls?\\s*=" $params -}}
{{- $managedStore := and $root.Values.controller.rateLimit.shared.enabled $root.Values.controller.rateLimit.store.enabled -}}
{{- if and (eq $name "rate-limit") $root.Values.controller.rateLimit.shared.enabled -}}
  {{- $storeTimeoutMs := int $root.Values.controller.rateLimit.shared.storeTimeoutMs -}}
  {{- if le $storeTimeoutMs 0 -}}
    {{- fail "controller.rateLimit.shared.storeTimeoutMs must be a positive integer milliseconds value." -}}
  {{- end -}}
{{- end -}}
{{- if and (eq $name "rate-limit") $managedStore -}}
  {{- if $hasStoreURL -}}
    {{- fail "controller.rateLimit.store.enabled=true auto-injects store_url for spoaHub.plugins.rate-limit; remove the manual store_url/store_urls from spoaHub.plugins.rate-limit.params or disable controller.rateLimit.store.enabled to bring your own store" -}}
  {{- end -}}
  {{- if $params -}}
{{ trimSuffix "\n" $params }}
{{ include "haptic.rateLimit.storeURLTOML" $root }}
  {{- else -}}
{{ include "haptic.rateLimit.storeURLTOML" $root }}
  {{- end -}}
{{- else if and (eq $name "rate-limit") $root.Values.controller.rateLimit.shared.enabled (not $hasStoreURL) -}}
  {{- fail "controller.rateLimit.shared.enabled=true requires a shared store. Leave controller.rateLimit.store.enabled=true for chart-managed HA Valkey, or set spoaHub.plugins.rate-limit.params.store_url/store_urls to bring your own Redis/Valkey." -}}
{{- else -}}
{{ $params }}
{{- end -}}
{{- end -}}

{{/*
SPOA hub plugin shared-library filename.
Maps the plugin shortname (as it appears under spoaHub.plugins.<X>) to the
.so filename produced by the upstream build. Most plugins use
`lib<name>_plugin.so` (with dashes mapped to underscores), except sso-auth
whose Cargo crate name produces `libhaproxy_spoa_hub_plugin_sso_auth.so`.
Argument: dict with `name` key.
*/}}
{{- define "haptic.spoaHub.libName" -}}
{{- if eq .name "sso-auth" -}}
libhaproxy_spoa_hub_plugin_sso_auth.so
{{- else -}}
lib{{ regexReplaceAll "-" .name "_" }}_plugin.so
{{- end -}}
{{- end -}}
