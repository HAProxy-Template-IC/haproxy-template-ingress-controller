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
  {{- range $name, $plugin := $hub.plugins -}}
    {{- if include "haptic.spoaHub.pluginEnabled" (dict "plugin" $plugin "root" $root) -}}true{{- end -}}
  {{- end -}}
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
