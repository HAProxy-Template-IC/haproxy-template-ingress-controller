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
  {{- $resolved := trim (tpl $val .root) -}}
  {{- if not (has $resolved (list "true" "false")) -}}
    {{- fail (printf "spoaHub.plugins.%s.enabled must be a boolean or a template resolving exactly to true or false; got %q." (.name | default "<unknown>") $resolved) -}}
  {{- end -}}
  {{- if eq $resolved "true" -}}true{{- end -}}
{{- else if ne $val nil -}}
  {{- fail (printf "spoaHub.plugins.%s.enabled must be a boolean or a template string resolving to true or false." (.name | default "<unknown>")) -}}
{{- end -}}
{{- end -}}

{{/*
Resolve a plugin timeout. Like `enabled`, `timeoutMs` may be a literal number
or a templated string in values.yaml. The authoritative operator-facing value
is always spoaHub.plugins.<name>.timeoutMs; feature settings do not provide
aliases with surprising precedence.
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
{{- if not (regexMatch "^[0-9]+$" (toString $resolved)) -}}
  {{- fail (printf "spoaHub.plugins.%s.timeoutMs must resolve to a positive integer milliseconds value." .name) -}}
{{- end -}}
{{- $ms := int $resolved -}}
{{- if le $ms 0 -}}
  {{- fail (printf "spoaHub.plugins.%s.timeoutMs must resolve to a positive integer milliseconds value." .name) -}}
{{- end -}}
{{- $ms -}}
{{- end }}

{{/*
Resolve and validate one integer from spoaHub.hub. Callers decide whether an
optional null field should be emitted before invoking this helper. Keeping the
validation here ensures bootstrap and controller-rendered runtime TOML cannot
silently diverge on zero/negative values.
Args: dict "root" $ "field" <name> "min" <integer>
*/}}
{{- define "haptic.spoaHub.hubInteger" -}}
{{- $raw := index .root.Values.spoaHub.hub .field -}}
{{- if not (regexMatch "^[0-9]+$" (toString $raw)) -}}
  {{- fail (printf "spoaHub.hub.%s must be an integer greater than or equal to %d." .field (int .min)) -}}
{{- end -}}
{{- $value := int $raw -}}
{{- if lt $value (int .min) -}}
  {{- fail (printf "spoaHub.hub.%s must be an integer greater than or equal to %d." .field (int .min)) -}}
{{- end -}}
{{- if and (hasKey . "max") (gt $value (int .max)) -}}
  {{- fail (printf "spoaHub.hub.%s must be an integer between %d and %d." .field (int .min) (int .max)) -}}
{{- end -}}
{{- $value -}}
{{- end }}

{{/*
Resolve a required integer value from one optional plugin field. Like enabled
and timeoutMs, admission fields may be literal numbers or templated strings.
Callers first check hasKey, then pass the allowed minimum.
Args: dict "plugin" <map> "root" $ "name" <name> "field" <field> "min" <n>
*/}}
{{- define "haptic.spoaHub.pluginInteger" -}}
{{- $raw := index .plugin .field -}}
{{- $resolved := $raw -}}
{{- if eq (kindOf $raw) "string" -}}
  {{- $resolved = (trim (tpl $raw .root)) -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $resolved)) -}}
  {{- fail (printf "spoaHub.plugins.%s.%s must resolve to an integer greater than or equal to %d." .name .field (int .min)) -}}
{{- end -}}
{{- $value := int $resolved -}}
{{- if lt $value (int .min) -}}
  {{- fail (printf "spoaHub.plugins.%s.%s must resolve to an integer greater than or equal to %d." .name .field (int .min)) -}}
{{- end -}}
{{- if and (hasKey . "max") (gt $value (int .max)) -}}
  {{- fail (printf "spoaHub.plugins.%s.%s must resolve to an integer between %d and %d." .name .field (int .min) (int .max)) -}}
{{- end -}}
{{- $value -}}
{{- end }}

{{/*
Validate a positive HAProxy duration. Bare integers retain HAProxy's native
millisecond interpretation; explicit us/ms/s/m/h/d suffixes are accepted.
Args: dict "value" <value> "field" <full values path>
*/}}
{{- define "haptic.spoaHub.duration" -}}
{{- $value := toString .value -}}
{{- if or (not (regexMatch "^[0-9]+(us|ms|s|m|h|d)?$" $value)) (le (int64 (regexFind "^[0-9]+" $value)) 0) -}}
  {{- fail (printf "%s must be a positive HAProxy duration using an optional us, ms, s, m, h, or d suffix." .field) -}}
{{- end -}}
{{- $value -}}
{{- end }}

{{/*
Validate the one process-wide HAProxy request buffer shared by every body
inspector and return its usable body capacity (sizeBytes - reservedBytes).
The returned capacity is also the chart-managed top-level Coraza body limit,
so HAProxy and the plugin cannot disagree about bytes that are available.
*/}}
{{- define "haptic.requestBodyInspection.capacity" -}}
{{- $configuredExtraContext := dig "templatingSettings" "extraContext" dict (.Values.controller.config | default dict) -}}
{{- $inspection := dig "requestBodyInspection" dict $configuredExtraContext -}}
{{- if not (kindIs "map" $inspection) -}}{{- fail "controller.config.templatingSettings.extraContext.requestBodyInspection must be a map." -}}{{- end -}}
{{- range $field := keys $inspection -}}{{- if ne $field "haproxyBuffer" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.requestBodyInspection contains unknown field %q. Valid field: haproxyBuffer." $field) -}}{{- end -}}{{- end -}}
{{- $buffer := dig "haproxyBuffer" dict $inspection -}}
{{- if not (kindIs "map" $buffer) -}}{{- fail "controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer must be a map." -}}{{- end -}}
{{- range $field := keys $buffer -}}{{- if not (has $field (list "sizeBytes" "reservedBytes")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer contains unknown field %q. Valid fields: sizeBytes, reservedBytes." $field) -}}{{- end -}}{{- end -}}
{{- $sizeRaw := dig "sizeBytes" 16384 $buffer | toString -}}
{{- $reservedRaw := dig "reservedBytes" 8192 $buffer | toString -}}
{{- if not (regexMatch "^[0-9]+$" $sizeRaw) -}}{{- fail "controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes must be an integer between 16384 and 2097152 bytes." -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" $reservedRaw) -}}{{- fail "controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.reservedBytes must be a positive integer smaller than sizeBytes." -}}{{- end -}}
{{- $size := int $sizeRaw -}}
{{- $reserved := int $reservedRaw -}}
{{- if or (lt $size 16384) (gt $size 2097152) -}}{{- fail "controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes must be between 16384 and 2097152 bytes." -}}{{- end -}}
{{- if or (le $reserved 0) (ge $reserved $size) -}}{{- fail "controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.reservedBytes must be positive and smaller than sizeBytes." -}}{{- end -}}
{{- sub $size $reserved -}}
{{- end }}

{{/*
Validate the complete chart-owned SPOA Hub value surface, including disabled
plugins. Disabled/staged configuration must not hide typos that only explode
on a later enable. This helper intentionally does not validate Kubernetes API
objects such as resources/securityContext; the apiserver owns those schemas.
*/}}
{{- define "haptic.spoaHub.validateValues" -}}
{{- $root := . -}}
{{- $spoa := $root.Values.spoaHub -}}
{{- if not (kindIs "map" $spoa) -}}
  {{- fail "spoaHub must be a map." -}}
{{- end -}}
{{- range $field := keys $spoa -}}
  {{- if not (has $field (list "enabled" "image" "resources" "hub" "haproxy" "plugins" "securityContext" "extraVolumeMounts")) -}}
    {{- fail (printf "spoaHub contains unknown field %q." $field) -}}
  {{- end -}}
{{- end -}}
{{- if and (ne $spoa.enabled nil) (not (kindIs "bool" $spoa.enabled)) -}}
  {{- fail "spoaHub.enabled must be a boolean or null." -}}
{{- end -}}

{{- $hub := $spoa.hub | default dict -}}
{{- if not (kindIs "map" $hub) -}}{{- fail "spoaHub.hub must be a map." -}}{{- end -}}
{{- range $field := keys $hub -}}
  {{- if not (has $field (list "logLevel" "workerThreads" "maxConnections" "blockingThreadKeepAliveSecs" "maxBlockingThreads" "reloadDrainTimeoutMs" "metricsAddr" "goGCPercent")) -}}
    {{- fail (printf "spoaHub.hub contains unknown field %q. Valid fields: logLevel, workerThreads, maxConnections, blockingThreadKeepAliveSecs, maxBlockingThreads, reloadDrainTimeoutMs, metricsAddr, goGCPercent." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "string" $hub.logLevel) -}}{{- fail "spoaHub.hub.logLevel must be a string." -}}{{- end -}}
{{- if not (has $hub.logLevel (list "trace" "debug" "info" "warn" "error")) -}}
  {{- fail "spoaHub.hub.logLevel must be one of: trace, debug, info, warn, error." -}}
{{- end -}}
{{- $_ := include "haptic.spoaHub.hubInteger" (dict "root" $root "field" "maxConnections" "min" 1) -}}
{{- $_ := include "haptic.spoaHub.hubInteger" (dict "root" $root "field" "blockingThreadKeepAliveSecs" "min" 1) -}}
{{- $_ := include "haptic.spoaHub.hubInteger" (dict "root" $root "field" "goGCPercent" "min" 1) -}}
{{- if ne $hub.workerThreads nil -}}{{- $_ := include "haptic.spoaHub.hubInteger" (dict "root" $root "field" "workerThreads" "min" 1) -}}{{- end -}}
{{- if ne $hub.maxBlockingThreads nil -}}{{- $_ := include "haptic.spoaHub.hubInteger" (dict "root" $root "field" "maxBlockingThreads" "min" 1) -}}{{- end -}}
{{- if ne $hub.reloadDrainTimeoutMs nil -}}{{- $_ := include "haptic.spoaHub.hubInteger" (dict "root" $root "field" "reloadDrainTimeoutMs" "min" 0) -}}{{- end -}}
{{- if not (kindIs "string" $hub.metricsAddr) -}}{{- fail "spoaHub.hub.metricsAddr must be a string." -}}{{- end -}}
{{- if and (ne $hub.metricsAddr "") (not (regexMatch "^(([0-9]{1,3}\\.){3}[0-9]{1,3}|\\[[0-9A-Fa-f:]+\\]):[0-9]{1,5}$" $hub.metricsAddr)) -}}
  {{- fail "spoaHub.hub.metricsAddr must be empty or a numeric IP address and port such as 127.0.0.1:9095 or [::1]:9095; hostnames are not supported by the hub." -}}
{{- end -}}
{{- if ne $hub.metricsAddr "" -}}
  {{- $metricsPort := int (regexFind "[0-9]+$" $hub.metricsAddr) -}}
  {{- if or (le $metricsPort 0) (gt $metricsPort 65535) -}}{{- fail "spoaHub.hub.metricsAddr port must be between 1 and 65535." -}}{{- end -}}
{{- end -}}

{{- $haproxy := $spoa.haproxy | default dict -}}
{{- if not (kindIs "map" $haproxy) -}}{{- fail "spoaHub.haproxy must be a map." -}}{{- end -}}
{{- range $field := keys $haproxy -}}
  {{- if not (has $field (list "socketPath" "modeSpop" "timeoutHello" "timeoutIdle" "timeoutProcessing" "timeoutProcessingMarginMs" "poolMaxConn" "poolPurgeDelay" "mirror")) -}}
    {{- fail (printf "spoaHub.haproxy contains unknown field %q. Valid fields: socketPath, modeSpop, timeoutHello, timeoutIdle, timeoutProcessing, timeoutProcessingMarginMs, poolMaxConn, poolPurgeDelay, mirror." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "string" $haproxy.socketPath) -}}{{- fail "spoaHub.haproxy.socketPath must be a string." -}}{{- end -}}
{{- if not (regexMatch "^/run/spoa/[A-Za-z0-9._-]+\\.sock$" $haproxy.socketPath) -}}
  {{- fail "spoaHub.haproxy.socketPath must name a .sock file directly under the chart's shared /run/spoa mount." -}}
{{- end -}}
{{- if not (kindIs "bool" $haproxy.modeSpop) -}}{{- fail "spoaHub.haproxy.modeSpop must be a boolean." -}}{{- end -}}
{{- $_ := include "haptic.spoaHub.duration" (dict "value" $haproxy.timeoutHello "field" "spoaHub.haproxy.timeoutHello") -}}
{{- $_ := include "haptic.spoaHub.duration" (dict "value" $haproxy.timeoutIdle "field" "spoaHub.haproxy.timeoutIdle") -}}
{{- if ne $haproxy.timeoutProcessing nil -}}{{- $_ := include "haptic.spoaHub.duration" (dict "value" $haproxy.timeoutProcessing "field" "spoaHub.haproxy.timeoutProcessing") -}}{{- end -}}
{{- $_ := include "haptic.spoaHub.duration" (dict "value" $haproxy.poolPurgeDelay "field" "spoaHub.haproxy.poolPurgeDelay") -}}
{{- if not (regexMatch "^[0-9]+$" (toString $haproxy.timeoutProcessingMarginMs)) -}}{{- fail "spoaHub.haproxy.timeoutProcessingMarginMs must be an integer between 1 and 60000 milliseconds." -}}{{- end -}}
{{- $processingMargin := int $haproxy.timeoutProcessingMarginMs -}}
{{- if or (lt $processingMargin 1) (gt $processingMargin 60000) -}}{{- fail "spoaHub.haproxy.timeoutProcessingMarginMs must be between 1 and 60000 milliseconds." -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $haproxy.poolMaxConn)) -}}{{- fail "spoaHub.haproxy.poolMaxConn must be a positive integer." -}}{{- end -}}
{{- $poolMaxConn := int $haproxy.poolMaxConn -}}
{{- if lt $poolMaxConn 1 -}}{{- fail "spoaHub.haproxy.poolMaxConn must be a positive integer." -}}{{- end -}}
{{- if gt $poolMaxConn (int $hub.maxConnections) -}}
  {{- fail "spoaHub.haproxy.poolMaxConn must not exceed spoaHub.hub.maxConnections; the hub cannot accept the advertised HAProxy pool capacity." -}}
{{- end -}}
{{- $mirror := $haproxy.mirror | default dict -}}
{{- if not (kindIs "map" $mirror) -}}{{- fail "spoaHub.haproxy.mirror must be a map." -}}{{- end -}}
{{- range $field := keys $mirror -}}{{- if ne $field "minMessageSlots" -}}{{- fail (printf "spoaHub.haproxy.mirror contains unknown field %q. Valid field: minMessageSlots." $field) -}}{{- end -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $mirror.minMessageSlots)) -}}{{- fail "spoaHub.haproxy.mirror.minMessageSlots must be an integer between 0 and 1024." -}}{{- end -}}
{{- $mirrorMinSlots := int $mirror.minMessageSlots -}}
{{- if or (lt $mirrorMinSlots 0) (gt $mirrorMinSlots 1024) -}}{{- fail "spoaHub.haproxy.mirror.minMessageSlots must be between 0 and 1024." -}}{{- end -}}

{{- $plugins := $spoa.plugins | default dict -}}
{{- if not (kindIs "map" $plugins) -}}{{- fail "spoaHub.plugins must be a map." -}}{{- end -}}
{{- $normalizedNames := dict -}}
{{- $enabledNames := dict -}}
{{- $enabledConcurrency := 0 -}}
{{- range $name, $plugin := $plugins -}}
  {{- if not (regexMatch "^[a-z][a-z0-9-]*$" $name) -}}{{- fail (printf "spoaHub.plugins key %q must contain lowercase letters, digits, and hyphens and start with a letter." $name) -}}{{- end -}}
  {{- if not (kindIs "map" $plugin) -}}{{- fail (printf "spoaHub.plugins.%s must be a map." $name) -}}{{- end -}}
  {{- $normalizedName := regexReplaceAll "-" $name "_" -}}
  {{- if hasKey $normalizedNames $normalizedName -}}{{- fail (printf "spoaHub plugin names %q and %q both normalize to %q; choose distinct names." (index $normalizedNames $normalizedName) $name $normalizedName) -}}{{- end -}}
  {{- $_ := set $normalizedNames $normalizedName $name -}}
  {{- $allowedFields := list "enabled" "timeoutMs" "messages" "dependsOn" "maxConcurrency" "maxQueue" "queueTimeoutMs" "adaptiveConcurrency" "params" -}}
  {{- if eq $name "coraza" -}}{{- $allowedFields = append $allowedFields "directives" -}}{{- end -}}
  {{- if eq $name "rate-limit" -}}{{- $allowedFields = append $allowedFields "storeOperationTimeoutMs" -}}{{- end -}}
  {{- range $field := keys $plugin -}}
    {{- if not (has $field $allowedFields) -}}{{- fail (printf "spoaHub.plugins.%s contains unknown field %q. Plugin runtime settings belong inside params." $name $field) -}}{{- end -}}
  {{- end -}}
  {{- $enabled := include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $root "name" $name) -}}
  {{- if $enabled -}}{{- $_ := set $enabledNames $normalizedName true -}}{{- end -}}
  {{- if not (hasKey $plugin "timeoutMs") -}}{{- fail (printf "spoaHub.plugins.%s.timeoutMs is required so every plugin has an explicit request-path deadline." $name) -}}{{- end -}}
  {{- $_ := include "haptic.spoaHub.pluginTimeoutMs" (dict "plugin" $plugin "root" $root "name" $name) -}}
  {{- if not (kindIs "slice" $plugin.messages) -}}{{- fail (printf "spoaHub.plugins.%s.messages must be a list." $name) -}}{{- end -}}
  {{- if and $enabled (eq (len $plugin.messages) 0) -}}{{- fail (printf "spoaHub.plugins.%s.messages must contain at least one message while the plugin is enabled." $name) -}}{{- end -}}
  {{- $seenMessages := dict -}}
  {{- range $message := $plugin.messages -}}
    {{- if not (kindIs "string" $message) -}}{{- fail (printf "spoaHub.plugins.%s.messages entries must be strings." $name) -}}{{- end -}}
    {{- if not (regexMatch "^[A-Za-z][A-Za-z0-9_-]*$" $message) -}}{{- fail (printf "spoaHub.plugins.%s message %q contains unsupported characters." $name $message) -}}{{- end -}}
    {{- if hasKey $seenMessages $message -}}{{- fail (printf "spoaHub.plugins.%s.messages contains duplicate %q." $name $message) -}}{{- end -}}
    {{- $_ := set $seenMessages $message true -}}
  {{- end -}}
  {{- $dependencies := $plugin.dependsOn | default list -}}
  {{- if not (kindIs "slice" $dependencies) -}}{{- fail (printf "spoaHub.plugins.%s.dependsOn must be a list." $name) -}}{{- end -}}
  {{- range $dependency := $dependencies -}}
    {{- if not (kindIs "string" $dependency) -}}{{- fail (printf "spoaHub.plugins.%s.dependsOn entries must be strings." $name) -}}{{- end -}}
    {{- if not (regexMatch "^[a-z][a-z0-9-]*$" $dependency) -}}{{- fail (printf "spoaHub.plugins.%s dependency %q is not a valid plugin name." $name $dependency) -}}{{- end -}}
  {{- end -}}
  {{- $maxConcurrency := 0 -}}
  {{- if hasKey $plugin "maxConcurrency" -}}
    {{- $maxConcurrency = int (include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $root "name" $name "field" "maxConcurrency" "min" 1)) -}}
    {{- if $enabled -}}{{- $enabledConcurrency = add $enabledConcurrency $maxConcurrency -}}{{- end -}}
  {{- end -}}
  {{- $maxQueue := 0 -}}
  {{- if hasKey $plugin "maxQueue" -}}{{- $maxQueue = int (include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $root "name" $name "field" "maxQueue" "min" 0)) -}}{{- end -}}
  {{- if hasKey $plugin "queueTimeoutMs" -}}
    {{- $_ := include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $root "name" $name "field" "queueTimeoutMs" "min" 1) -}}
    {{- if eq $maxQueue 0 -}}{{- fail (printf "spoaHub.plugins.%s.queueTimeoutMs has no effect when maxQueue is zero; remove it or configure a bounded queue." $name) -}}{{- end -}}
  {{- end -}}
  {{- if and (hasKey $plugin "params") (not (kindIs "string" $plugin.params)) -}}{{- fail (printf "spoaHub.plugins.%s.params must be a TOML string." $name) -}}{{- end -}}
  {{- if and (eq $name "coraza") (hasKey $plugin "directives") (not (kindIs "string" $plugin.directives)) -}}{{- fail "spoaHub.plugins.coraza.directives must be a string." -}}{{- end -}}
  {{- if eq $name "rate-limit" -}}
    {{- if not (hasKey $plugin "storeOperationTimeoutMs") -}}{{- fail "spoaHub.plugins.rate-limit.storeOperationTimeoutMs is required." -}}{{- end -}}
    {{- $_ := include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $root "name" $name "field" "storeOperationTimeoutMs" "min" 1) -}}
  {{- end -}}
{{- end -}}
{{- range $name, $plugin := $plugins -}}
  {{- $enabled := include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $root "name" $name) -}}
  {{- if $enabled -}}
    {{- range $dependency := ($plugin.dependsOn | default list) -}}
      {{- $normalizedDependency := regexReplaceAll "-" $dependency "_" -}}
      {{- if not (hasKey $enabledNames $normalizedDependency) -}}{{- fail (printf "spoaHub.plugins.%s.dependsOn references %q, which is not an enabled plugin." $name $dependency) -}}{{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if and (ne $hub.maxBlockingThreads nil) (gt $enabledConcurrency 0) (lt (int $hub.maxBlockingThreads) $enabledConcurrency) -}}
  {{- fail (printf "spoaHub.hub.maxBlockingThreads (%d) must be at least the sum of explicit maxConcurrency values for enabled plugins (%d); the hub performs the final check including CPU-derived plugin defaults." (int $hub.maxBlockingThreads) $enabledConcurrency) -}}
{{- end -}}
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
    {{- if include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $root "name" $name) -}}
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
{{- $_ := include "haptic.spoaHub.validateValues" . -}}
{{- $_ := include "haptic.rateLimit.validateValues" . -}}
{{- $spoaHub := .Values.spoaHub -}}
{{- $hub := $spoaHub.hub -}}
plugin_dir = "/etc/haproxy-spoa-hub/plugins"
default_timeout_ms = 500
log_level = {{ $hub.logLevel | quote }}
max_connections = {{ include "haptic.spoaHub.hubInteger" (dict "root" . "field" "maxConnections" "min" 1) }}
blocking_thread_keep_alive_secs = {{ include "haptic.spoaHub.hubInteger" (dict "root" . "field" "blockingThreadKeepAliveSecs" "min" 1) }}
{{- if ne $hub.maxBlockingThreads nil }}
max_blocking_threads = {{ include "haptic.spoaHub.hubInteger" (dict "root" . "field" "maxBlockingThreads" "min" 1) }}
{{- end }}
{{- if ne $hub.reloadDrainTimeoutMs nil }}
reload_drain_timeout_ms = {{ include "haptic.spoaHub.hubInteger" (dict "root" . "field" "reloadDrainTimeoutMs" "min" 0) }}
{{- end }}
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
{{- if ne $hub.workerThreads nil }}
worker_threads = {{ include "haptic.spoaHub.hubInteger" (dict "root" . "field" "workerThreads" "min" 1) }}
{{- end }}

[[listeners]]
type = "unix"
address = {{ $spoaHub.haproxy.socketPath | quote }}

{{- range $name, $plugin := $spoaHub.plugins }}
{{- if include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $ "name" $name) }}

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
{{- if hasKey $plugin "maxConcurrency" }}
max_concurrency = {{ include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $ "name" $name "field" "maxConcurrency" "min" 1) }}
{{- end }}
{{- if hasKey $plugin "maxQueue" }}
max_queue = {{ include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $ "name" $name "field" "maxQueue" "min" 0) }}
{{- end }}
{{- if hasKey $plugin "queueTimeoutMs" }}
queue_timeout_ms = {{ include "haptic.spoaHub.pluginInteger" (dict "plugin" $plugin "root" $ "name" $name "field" "queueTimeoutMs" "min" 1) }}
{{- end }}
{{- if hasKey $plugin "adaptiveConcurrency" }}
{{- if not (kindIs "bool" $plugin.adaptiveConcurrency) -}}{{- fail (printf "spoaHub.plugins.%s.adaptiveConcurrency must be a boolean." $name) -}}{{- end -}}
{{- if $plugin.adaptiveConcurrency }}
adaptive_concurrency = true
{{- end }}
{{- end }}
{{- $normalizedDependencies := list }}
{{- range ($plugin.dependsOn | default list) }}
  {{- $normalizedDependencies = append $normalizedDependencies (regexReplaceAll "-" . "_") }}
{{- end }}
{{- with $normalizedDependencies }}
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

{{/*
Validate the chart-managed shared rate-limit configuration even while the
feature is disabled. These values are later interpolated into shell scripts and
Valkey/Sentinel configuration, so silently coercing zero values or accepting
unknown keys is unsafe and especially surprising when a staged feature is
enabled months later.
*/}}
{{- define "haptic.rateLimit.validateValues" -}}
{{- $rateLimit := .Values.rateLimit -}}
{{- if not (kindIs "map" $rateLimit) -}}{{- fail "rateLimit must be a map." -}}{{- end -}}
{{- range $field := keys $rateLimit -}}
  {{- if ne $field "shared" -}}{{- fail (printf "rateLimit contains unknown field %q. Valid field: shared." $field) -}}{{- end -}}
{{- end -}}
{{- $shared := $rateLimit.shared | default dict -}}
{{- if not (kindIs "map" $shared) -}}{{- fail "rateLimit.shared must be a map." -}}{{- end -}}
{{- range $field := keys $shared -}}{{- if not (has $field (list "enabled" "managedStore" "externalStore")) -}}{{- fail (printf "rateLimit.shared contains unknown field %q. Valid fields: enabled, managedStore, externalStore." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $shared.enabled) -}}{{- fail "rateLimit.shared.enabled must be a boolean." -}}{{- end -}}

{{- $external := $shared.externalStore | default dict -}}
{{- if not (kindIs "map" $external) -}}{{- fail "rateLimit.shared.externalStore must be a map." -}}{{- end -}}
{{- range $field := keys $external -}}
  {{- if ne $field "urls" -}}{{- fail (printf "rateLimit.shared.externalStore contains unknown field %q. Valid field: urls." $field) -}}{{- end -}}
{{- end -}}
{{- $externalURLs := $external.urls | default list -}}
{{- if not (kindIs "slice" $externalURLs) -}}{{- fail "rateLimit.shared.externalStore.urls must be a list of Redis/Valkey URLs." -}}{{- end -}}
{{- range $url := $externalURLs -}}
  {{- if or (not (kindIs "string" $url)) (eq (trim $url) "") -}}{{- fail "rateLimit.shared.externalStore.urls entries must be non-empty URL strings." -}}{{- end -}}
{{- end -}}

{{- $store := $shared.managedStore | default dict -}}
{{- if not (kindIs "map" $store) -}}{{- fail "rateLimit.shared.managedStore must be a map." -}}{{- end -}}
{{- range $field := keys $store -}}
  {{- if has $field (list "maxmemory" "maxmemoryPolicy") -}}{{- fail "rateLimit.shared.managedStore.maxmemory and maxmemoryPolicy were renamed to maxMemory and maxMemoryPolicy to follow the chart's camelCase value convention." -}}{{- end -}}
  {{- if not (has $field (list "enabled" "image" "imagePullPolicy" "port" "replicas" "maxMemory" "maxMemoryPolicy" "sentinel" "podDisruptionBudget" "networkPolicy" "resources")) -}}
    {{- fail (printf "rateLimit.shared.managedStore contains unknown field %q." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $store.enabled) -}}{{- fail "rateLimit.shared.managedStore.enabled must be a boolean." -}}{{- end -}}
{{- if and $store.enabled (gt (len $externalURLs) 0) -}}
  {{- fail "rateLimit.shared.managedStore.enabled=true and rateLimit.shared.externalStore.urls are mutually exclusive; disable the managed store to bring your own Redis/Valkey." -}}
{{- end -}}
{{- if or (not (kindIs "string" $store.image)) (eq (trim $store.image) "") -}}{{- fail "rateLimit.shared.managedStore.image must be a non-empty image reference string." -}}{{- end -}}
{{- if or (not (kindIs "string" $store.imagePullPolicy)) (not (has $store.imagePullPolicy (list "Always" "IfNotPresent" "Never"))) -}}{{- fail "rateLimit.shared.managedStore.imagePullPolicy must be one of: Always, IfNotPresent, Never." -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $store.port)) -}}{{- fail "rateLimit.shared.managedStore.port must be an integer between 1 and 65535." -}}{{- end -}}
{{- $port := int $store.port -}}
{{- if or (lt $port 1) (gt $port 65535) -}}{{- fail "rateLimit.shared.managedStore.port must be between 1 and 65535." -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $store.replicas)) -}}{{- fail "rateLimit.shared.managedStore.replicas must be an integer greater than or equal to 3 for chart-managed Sentinel HA." -}}{{- end -}}
{{- $replicas := int $store.replicas -}}
{{- if lt $replicas 3 -}}{{- fail "rateLimit.shared.managedStore.replicas must be at least 3 for chart-managed Sentinel HA." -}}{{- end -}}
{{- if or (not (kindIs "string" $store.maxMemory)) (not (regexMatch "(?i)^[1-9][0-9]*(b|k|kb|m|mb|g|gb|t|tb)$" $store.maxMemory)) -}}
  {{- fail "rateLimit.shared.managedStore.maxMemory must be a positive Valkey byte size such as 96mb or 1gb." -}}
{{- end -}}
{{- if or (not (kindIs "string" $store.maxMemoryPolicy)) (not (has $store.maxMemoryPolicy (list "noeviction" "allkeys-lru" "allkeys-lfu" "allkeys-random" "volatile-lru" "volatile-lfu" "volatile-random" "volatile-ttl"))) -}}
  {{- fail "rateLimit.shared.managedStore.maxMemoryPolicy must be a supported Valkey eviction policy." -}}
{{- end -}}

{{- $sentinel := $store.sentinel | default dict -}}
{{- if not (kindIs "map" $sentinel) -}}{{- fail "rateLimit.shared.managedStore.sentinel must be a map." -}}{{- end -}}
{{- range $field := keys $sentinel -}}
  {{- if not (has $field (list "port" "quorum" "downAfterMilliseconds" "failoverTimeoutMilliseconds" "parallelSyncs" "resources")) -}}{{- fail (printf "rateLimit.shared.managedStore.sentinel contains unknown field %q." $field) -}}{{- end -}}
{{- end -}}
{{- range $field := list "port" "quorum" "downAfterMilliseconds" "failoverTimeoutMilliseconds" "parallelSyncs" -}}
  {{- if not (regexMatch "^[0-9]+$" (toString (index $sentinel $field))) -}}{{- fail (printf "rateLimit.shared.managedStore.sentinel.%s must be a positive integer." $field) -}}{{- end -}}
{{- end -}}
{{- $sentinelPort := int $sentinel.port -}}
{{- if or (lt $sentinelPort 1) (gt $sentinelPort 65535) -}}{{- fail "rateLimit.shared.managedStore.sentinel.port must be between 1 and 65535." -}}{{- end -}}
{{- if eq $sentinelPort $port -}}{{- fail "rateLimit.shared.managedStore.sentinel.port must differ from rateLimit.shared.managedStore.port." -}}{{- end -}}
{{- $quorum := int $sentinel.quorum -}}
{{- if or (lt $quorum 1) (gt $quorum $replicas) -}}{{- fail "rateLimit.shared.managedStore.sentinel.quorum must be between 1 and rateLimit.shared.managedStore.replicas." -}}{{- end -}}
{{- $downAfter := int $sentinel.downAfterMilliseconds -}}
{{- if lt $downAfter 1 -}}{{- fail "rateLimit.shared.managedStore.sentinel.downAfterMilliseconds must be positive." -}}{{- end -}}
{{- $failoverTimeout := int $sentinel.failoverTimeoutMilliseconds -}}
{{- if lt $failoverTimeout $downAfter -}}{{- fail "rateLimit.shared.managedStore.sentinel.failoverTimeoutMilliseconds must be greater than or equal to downAfterMilliseconds." -}}{{- end -}}
{{- $parallelSyncs := int $sentinel.parallelSyncs -}}
{{- if or (lt $parallelSyncs 1) (ge $parallelSyncs $replicas) -}}{{- fail "rateLimit.shared.managedStore.sentinel.parallelSyncs must be between 1 and replicas minus 1." -}}{{- end -}}

{{- $pdb := $store.podDisruptionBudget | default dict -}}
{{- if not (kindIs "map" $pdb) -}}{{- fail "rateLimit.shared.managedStore.podDisruptionBudget must be a map." -}}{{- end -}}
{{- range $field := keys $pdb -}}{{- if not (has $field (list "enabled" "maxUnavailable")) -}}{{- fail (printf "rateLimit.shared.managedStore.podDisruptionBudget contains unknown field %q. Valid fields: enabled, maxUnavailable." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $pdb.enabled) -}}{{- fail "rateLimit.shared.managedStore.podDisruptionBudget.enabled must be a boolean." -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $pdb.maxUnavailable)) -}}{{- fail "rateLimit.shared.managedStore.podDisruptionBudget.maxUnavailable must be a non-negative integer." -}}{{- end -}}
{{- $maxUnavailable := int $pdb.maxUnavailable -}}
{{- if gt $maxUnavailable (sub $replicas $quorum) -}}
  {{- fail "rateLimit.shared.managedStore.podDisruptionBudget.maxUnavailable must not exceed replicas minus Sentinel quorum, so voluntary disruptions preserve failover quorum." -}}
{{- end -}}
{{- $networkPolicy := $store.networkPolicy | default dict -}}
{{- if not (kindIs "map" $networkPolicy) -}}{{- fail "rateLimit.shared.managedStore.networkPolicy must be a map." -}}{{- end -}}
{{- range $field := keys $networkPolicy -}}{{- if ne $field "enabled" -}}{{- fail (printf "rateLimit.shared.managedStore.networkPolicy contains unknown field %q. Valid field: enabled." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $networkPolicy.enabled) -}}{{- fail "rateLimit.shared.managedStore.networkPolicy.enabled must be a boolean." -}}{{- end -}}
{{- end }}

{{- define "haptic.rateLimit.storeSentinelServiceName" -}}
{{- printf "%s-sentinel" (include "haptic.rateLimit.storeServiceName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "haptic.rateLimit.storeSentinelMasterName" -}}
{{- "haptic-rate-limit" -}}
{{- end -}}

{{- define "haptic.rateLimit.storeSentinelURL" -}}
{{- $root := . -}}
{{- $store := $root.Values.rateLimit.shared.managedStore -}}
{{- $sentinel := $store.sentinel | default dict -}}
{{- $sentinelPort := int $sentinel.port -}}
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
store_url appended when rateLimit.shared.enabled=true and
rateLimit.shared.managedStore.enabled=true, so the bootstrap ConfigMap and the
runtime-rendered hub config stay byte-for-byte consistent about the store
endpoints.
*/}}
{{/*
Adaptive-concurrency ceiling for a spoa-hub plugin, derived from the sidecar's
MEMORY rather than CPU or a hand-set number — so it self-scales across small and
large deployments with no manual tuning. Concurrency in the hub is realised as
blocking OS threads (one per in-flight plugin call), so the binding resource is
memory, not cores; deriving from cores would be both the wrong dimension and
unreliable in a container (see haproxy-spoa-hub ADR-0002). The controller then
finds the live operating point under this ceiling from request latency, so the
ceiling is a backstop, not the operating point.

Formula: reserve ~128MB for the coraza ruleset + Go runtime, budget ~8MB per
concurrent call (thread stack + per-transaction buffers), clamp to [8, 256].
Reads the sidecar memory limit (the hard OOM boundary), falling back to the
request. Argument: the root context (`.`).
*/}}
{{- define "haptic.spoaHub.adaptiveCeiling" -}}
{{- $res := .Values.spoaHub.resources | default dict -}}
{{- $mem := dig "limits" "memory" (dig "requests" "memory" "" $res) $res -}}
{{- $memMB := include "haptic.memoryToMB" $mem | int -}}
{{- $ceiling := div (sub $memMB 128) 8 -}}
{{- max 8 (min 256 $ceiling) -}}
{{- end -}}

{{/*
Go-runtime environment for a hub sidecar. The coraza plugin embeds a Go runtime,
and under sustained request load Go's default GOGC=100 triggers GC often; each
cycle steals CPU from the request path (GC assist) and drains the plugin's
transaction sync.Pool, so raising GOGC trims the p99 tail (measured ~15-20% at
moderate load on a memory-headroom-rich sidecar). GOMEMLIMIT is a soft cap at
90% of the container memory limit so the higher GOGC can never breach it. Set
spoaHub.hub.goGCPercent to tune (100 restores Go's default); GOMEMLIMIT is
derived automatically and omitted when no memory limit is set.
Args: dict "root" $ "resources" <container resources map>
*/}}
{{- define "haptic.spoaHub.goRuntimeEnv" -}}
- name: GOGC
  value: {{ .root.Values.spoaHub.hub.goGCPercent | toString | quote }}
{{- $memLimit := dig "limits" "memory" "" (.resources | default dict) -}}
{{- if $memLimit -}}
{{- $memMB := int (include "haptic.memoryToMB" $memLimit) -}}
{{- if gt $memMB 0 }}
- name: GOMEMLIMIT
  value: {{ printf "%dMiB" (div (mul $memMB 90) 100) | quote }}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "haptic.spoaHub.effectivePluginParams" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $plugin := default dict .plugin -}}
{{- $rawParams := "" -}}
{{- if hasKey $plugin "params" -}}
  {{- if not (kindIs "string" $plugin.params) -}}
    {{- fail (printf "spoaHub.plugins.%s.params must be a TOML string." $name) -}}
  {{- end -}}
  {{- $rawParams = $plugin.params -}}
{{- end -}}
{{- $params := $rawParams -}}
{{- if eq (kindOf $rawParams) "string" -}}
  {{- $params = tpl $rawParams $root -}}
{{- end -}}
{{- $managedLines := list -}}
{{- if eq $name "api-gateway" -}}
  {{- if regexMatch "(?m)^\\s*(default_fail_open|max_body_bytes)\\s*=" $params -}}
    {{- fail "spoaHub.plugins.api-gateway.params must not define default_fail_open or max_body_bytes; controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation is their single owner." -}}
  {{- end -}}
  {{- $validation := $root.Values.controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation -}}
  {{- $managedLines = append $managedLines (printf "default_fail_open = %s" ($validation.defaultFailOpen | toString)) -}}
  {{- $managedLines = append $managedLines (printf "max_body_bytes = %d" (int $validation.requestBody.defaultMaxBytes)) -}}
{{- end -}}
{{- if eq $name "coraza" -}}
  {{- if regexMatch "(?m)^\\s*directives\\s*=" $params -}}
    {{- fail "spoaHub.plugins.coraza.params must not define directives; spoaHub.plugins.coraza.directives is its single owner." -}}
  {{- end -}}
  {{- if regexMatch "(?m)^\\s*(request_body_limit|request_body_in_memory_limit|response_body_limit|response_check)\\s*=" $params -}}
    {{- fail "spoaHub.plugins.coraza.params must not define request_body_limit, request_body_in_memory_limit, response_body_limit, or response_check. The chart derives request limits from controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer and disables unsupported response-body inspection." -}}
  {{- end -}}
  {{- $bodyCapacity := int (include "haptic.requestBodyInspection.capacity" $root) -}}
  {{- $managedLines = append $managedLines (printf "request_body_limit = %d" $bodyCapacity) -}}
  {{- $managedLines = append $managedLines (printf "request_body_in_memory_limit = %d" $bodyCapacity) -}}
  {{- $managedLines = append $managedLines "response_check = false" -}}
{{- end -}}
{{- if eq $name "rate-limit" -}}
  {{- if regexMatch "(?m)^\\s*store_timeout_ms\\s*=" $params -}}
    {{- fail "spoaHub.plugins.rate-limit.params must not define store_timeout_ms; spoaHub.plugins.rate-limit.storeOperationTimeoutMs is its single owner." -}}
  {{- end -}}
  {{- if not (regexMatch "^[0-9]+$" (toString $plugin.storeOperationTimeoutMs)) -}}
    {{- fail "spoaHub.plugins.rate-limit.storeOperationTimeoutMs must be a positive integer milliseconds value." -}}
  {{- end -}}
  {{- $storeOperationTimeoutMs := int $plugin.storeOperationTimeoutMs -}}
  {{- if le $storeOperationTimeoutMs 0 -}}
    {{- fail "spoaHub.plugins.rate-limit.storeOperationTimeoutMs must be a positive integer milliseconds value." -}}
  {{- end -}}
  {{- $managedLines = append $managedLines (printf "store_timeout_ms = %d" $storeOperationTimeoutMs) -}}
  {{- if regexMatch "(?m)^\\s*store_urls?\\s*=" $params -}}
    {{- fail "spoaHub.plugins.rate-limit.params must not define store_url or store_urls; rateLimit.shared.managedStore (chart-managed HA Valkey) or rateLimit.shared.externalStore.urls (bring your own store) is their single owner." -}}
  {{- end -}}
  {{- $externalURLs := $root.Values.rateLimit.shared.externalStore.urls | default list -}}
  {{- if and $root.Values.rateLimit.shared.enabled $root.Values.rateLimit.shared.managedStore.enabled -}}
    {{- $managedLines = append $managedLines (include "haptic.rateLimit.storeURLTOML" $root | trim) -}}
  {{- else if gt (len $externalURLs) 0 -}}
    {{- if eq (len $externalURLs) 1 -}}
      {{- $managedLines = append $managedLines (printf "store_url = %s" (first $externalURLs | quote)) -}}
    {{- else -}}
      {{- $quoted := list -}}
      {{- range $url := $externalURLs -}}{{- $quoted = append $quoted ($url | quote) -}}{{- end -}}
      {{- $managedLines = append $managedLines (printf "store_urls = [%s]" (join ", " $quoted)) -}}
    {{- end -}}
  {{- else if $root.Values.rateLimit.shared.enabled -}}
    {{- fail "rateLimit.shared.enabled=true requires a shared store. Leave rateLimit.shared.managedStore.enabled=true for chart-managed HA Valkey, or set rateLimit.shared.externalStore.urls to bring your own Redis/Valkey." -}}
  {{- end -}}
{{- end -}}
{{- $sections := list -}}
{{- if gt (len $managedLines) 0 -}}{{- $sections = append $sections (join "\n" $managedLines) -}}{{- end -}}
{{- if ne (trim $params) "" -}}{{- $sections = append $sections (trimSuffix "\n" $params) -}}{{- end -}}
{{- join "\n" $sections -}}
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
