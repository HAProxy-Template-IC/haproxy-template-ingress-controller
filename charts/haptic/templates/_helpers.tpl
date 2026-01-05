{{/*
Expand the name of the chart.
*/}}
{{- define "haptic.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "haptic.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create controller deployment name with -controller suffix.
Only used for the controller Deployment resource.
*/}}
{{- define "haptic.controllerFullname" -}}
{{- printf "%s-controller" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "haptic.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "haptic.labels" -}}
helm.sh/chart: {{ include "haptic.chart" . }}
{{ include "haptic.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "haptic.selectorLabels" -}}
app.kubernetes.io/name: {{ include "haptic.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "haptic.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "haptic.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Filter validationTests based on _helm_skip_test condition
Evaluates _helm_skip_test Go template and excludes tests where it evaluates to "true"
*/}}
{{- define "haptic.filterTests" -}}
{{- $library := index . 0 }}
{{- $context := index . 1 }}
{{- if $library.validationTests }}
  {{- $filteredTests := dict }}
  {{- range $testName, $testDef := $library.validationTests }}
    {{- $skipTest := false }}
    {{- if $testDef._helm_skip_test }}
      {{- /* Evaluate _helm_skip_test template expression */ -}}
      {{- $skipCondition := tpl $testDef._helm_skip_test $context }}
      {{- if eq $skipCondition "true" }}
        {{- $skipTest = true }}
      {{- end }}
    {{- end }}
    {{- if not $skipTest }}
      {{- /* Include test, removing _helm_skip_test metadata */ -}}
      {{- $cleanTest := omit $testDef "_helm_skip_test" }}
      {{- $_ := set $filteredTests $testName $cleanTest }}
    {{- end }}
  {{- end }}
  {{- $_ := set $library "validationTests" $filteredTests }}
{{- end }}
{{- $library | toYaml }}
{{- end }}

{{/*
Deep merge template libraries based on enabled flags
Returns merged config with libraries applied in order: base -> ssl -> ingress -> gateway -> haproxytech -> haproxyIngress -> pathRegexLast -> values.yaml
Uses mustMergeOverwrite for deep merging of all nested structures
*/}}
{{- define "haptic.mergeLibraries" -}}
{{- $merged := dict }}
{{- $context := . }}

{{- /* Load base library if enabled */ -}}
{{- if $context.Values.controller.templateLibraries.base.enabled }}
  {{- $baseLibrary := $context.Files.Get "libraries/base.yaml" | fromYaml }}
  {{- $merged = mustMergeOverwrite $merged $baseLibrary }}
{{- end }}

{{- /* Load ssl library if enabled */ -}}
{{- if $context.Values.controller.templateLibraries.ssl.enabled }}
  {{- $sslLibrary := $context.Files.Get "libraries/ssl.yaml" | fromYaml }}
  {{- $merged = mustMergeOverwrite $merged $sslLibrary }}
{{- end }}

{{- /* Load ingress library if enabled */ -}}
{{- if $context.Values.controller.templateLibraries.ingress.enabled }}
  {{- $ingressLibrary := $context.Files.Get "libraries/ingress.yaml" | fromYaml }}
  {{- /* Inject ingressClassName from values into fieldSelector */ -}}
  {{- if and $ingressLibrary.watchedResources $ingressLibrary.watchedResources.ingresses }}
    {{- $fieldSelector := printf "spec.ingressClassName=%s" $context.Values.ingressClass.name }}
    {{- $_ := set $ingressLibrary.watchedResources.ingresses "fieldSelector" $fieldSelector }}
  {{- end }}
  {{- $merged = mustMergeOverwrite $merged $ingressLibrary }}
{{- end }}

{{- /* Load gateway library if enabled AND Gateway API CRDs are available */ -}}
{{- if and $context.Values.controller.templateLibraries.gateway.enabled ($context.Capabilities.APIVersions.Has "gateway.networking.k8s.io/v1/GatewayClass") }}
  {{- $gatewayLibrary := $context.Files.Get "libraries/gateway.yaml" | fromYaml }}
  {{- /* Inject gatewayClassName from values into fieldSelector */ -}}
  {{- if and $gatewayLibrary.watchedResources $gatewayLibrary.watchedResources.gateways }}
    {{- $fieldSelector := printf "spec.gatewayClassName=%s" $context.Values.gatewayClass.name }}
    {{- $_ := set $gatewayLibrary.watchedResources.gateways "fieldSelector" $fieldSelector }}
  {{- end }}
  {{- $merged = mustMergeOverwrite $merged $gatewayLibrary }}
{{- end }}

{{- /* Load haproxytech library if enabled */ -}}
{{- if $context.Values.controller.templateLibraries.haproxytech.enabled }}
  {{- $haproxytechLibrary := $context.Files.Get "libraries/haproxytech.yaml" | fromYaml }}
  {{- /* Filter tests based on _helm_skip_test conditions */ -}}
  {{- $filteredLibrary := include "haptic.filterTests" (list $haproxytechLibrary $context) | fromYaml }}
  {{- $merged = mustMergeOverwrite $merged $filteredLibrary }}
{{- end }}

{{- /* Load haproxy-ingress library if enabled */ -}}
{{- if $context.Values.controller.templateLibraries.haproxyIngress.enabled }}
  {{- $haproxyIngressLibrary := $context.Files.Get "libraries/haproxy-ingress.yaml" | fromYaml }}
  {{- $merged = mustMergeOverwrite $merged $haproxyIngressLibrary }}
{{- end }}

{{- /* Load path-regex-last library if enabled (overrides routing order) */ -}}
{{- if $context.Values.controller.templateLibraries.pathRegexLast.enabled }}
  {{- $pathRegexLastLibrary := $context.Files.Get "libraries/path-regex-last.yaml" | fromYaml }}
  {{- $merged = mustMergeOverwrite $merged $pathRegexLastLibrary }}
{{- end }}

{{- /* Merge user-provided config from values.yaml (highest priority) */ -}}
{{- $userConfig := dict }}
{{- if $context.Values.controller.config.templateSnippets }}
  {{- $_ := set $userConfig "templateSnippets" $context.Values.controller.config.templateSnippets }}
{{- end }}
{{- if $context.Values.controller.config.maps }}
  {{- $_ := set $userConfig "maps" $context.Values.controller.config.maps }}
{{- end }}
{{- if $context.Values.controller.config.files }}
  {{- $_ := set $userConfig "files" $context.Values.controller.config.files }}
{{- end }}
{{- if $context.Values.controller.config.sslCertificates }}
  {{- $_ := set $userConfig "sslCertificates" $context.Values.controller.config.sslCertificates }}
{{- end }}
{{- if $context.Values.controller.config.haproxyConfig }}
  {{- $_ := set $userConfig "haproxyConfig" $context.Values.controller.config.haproxyConfig }}
{{- end }}
{{- if $context.Values.controller.config.validationTests }}
  {{- $_ := set $userConfig "validationTests" $context.Values.controller.config.validationTests }}
{{- end }}

{{- /* Merge user config last so it overrides libraries */ -}}
{{- $merged = mustMergeOverwrite $merged $userConfig }}

{{- /* Return merged config as YAML */ -}}
{{- $merged | toYaml }}
{{- end }}

{{/*
Controller image
Combines base tag (defaults to Chart.AppVersion) with HAProxy version suffix (unless disabled)
Example: registry.gitlab.com/haproxy-haptic/haptic:0.1.0-alpha.11-haproxy3.2
For dev environments, set image.appendHaproxyVersion=false to use plain tag
*/}}
{{- define "haptic.controller.image" -}}
{{- $baseTag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if ne .Values.image.appendHaproxyVersion false -}}
{{- printf "%s:%s-haproxy%s" .Values.image.repository $baseTag .Values.haproxyVersion -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository $baseTag -}}
{{- end -}}
{{- end -}}

{{/*
HAProxy image
Uses haproxy.image.tag if set, otherwise falls back to haproxyVersion
Example: haproxytech/haproxy-debian:3.2
*/}}
{{- define "haptic.haproxy.image" -}}
{{- $tag := .Values.haproxy.image.tag | default .Values.haproxyVersion -}}
{{- printf "%s:%s" .Values.haproxy.image.repository $tag -}}
{{- end -}}

{{/*
HAProxy binary path
Enterprise: /opt/hapee-{version}/sbin/hapee-lb
Community: /usr/local/sbin/haproxy
*/}}
{{- define "haptic.haproxy.bin" -}}
{{- if .Values.haproxy.haproxyBin -}}
{{- .Values.haproxy.haproxyBin -}}
{{- else if .Values.haproxy.enterprise.enabled -}}
{{- printf "/opt/hapee-%s/sbin/hapee-lb" .Values.haproxy.enterprise.version -}}
{{- else -}}
/usr/local/sbin/haproxy
{{- end -}}
{{- end -}}

{{/*
Dataplane API binary path
Enterprise: /opt/hapee-extras/sbin/hapee-dataplaneapi
Community: /usr/local/bin/dataplaneapi
*/}}
{{- define "haptic.haproxy.dataplanebin" -}}
{{- if .Values.haproxy.dataplaneBin -}}
{{- .Values.haproxy.dataplaneBin -}}
{{- else if .Values.haproxy.enterprise.enabled -}}
/opt/hapee-extras/sbin/hapee-dataplaneapi
{{- else -}}
/usr/local/bin/dataplaneapi
{{- end -}}
{{- end -}}

{{/*
HAProxy user
Enterprise: hapee-lb
Community: haproxy
*/}}
{{- define "haptic.haproxy.user" -}}
{{- if .Values.haproxy.user -}}
{{- .Values.haproxy.user -}}
{{- else if .Values.haproxy.enterprise.enabled -}}
hapee-lb
{{- else -}}
haproxy
{{- end -}}
{{- end -}}

{{/*
Component labels
Generates app.kubernetes.io/component label for a given component name
Usage: {{ include "haptic.componentLabels" "loadbalancer" }}
*/}}
{{- define "haptic.componentLabels" -}}
app.kubernetes.io/component: {{ . }}
{{- end -}}

{{/*
HAProxy runAsUser
Enterprise: 1000 (hapee-lb user)
Community: 99 (haproxy user)
*/}}
{{- define "haptic.haproxy.runAsUser" -}}
{{- if .Values.haproxy.enterprise.enabled -}}
1000
{{- else -}}
99
{{- end -}}
{{- end -}}

{{/*
HAProxy runAsGroup
Enterprise: 1000 (hapee group)
Community: 99 (haproxy group)
*/}}
{{- define "haptic.haproxy.runAsGroup" -}}
{{- if .Values.haproxy.enterprise.enabled -}}
1000
{{- else -}}
99
{{- end -}}
{{- end -}}

{{/*
HAProxy fsGroup
Enterprise: 1000 (hapee group)
Community: 99 (haproxy group)
*/}}
{{- define "haptic.haproxy.fsGroup" -}}
{{- if .Values.haproxy.enterprise.enabled -}}
1000
{{- else -}}
99
{{- end -}}
{{- end -}}

{{/*
Dataplane API runAsUser
Uses same UID as HAProxy to share volumes
Enterprise: 1000 (hapee-lb user, same group as hapee-dataplaneapi)
Community: 99 (haproxy user)
*/}}
{{- define "haptic.haproxy.dataplaneRunAsUser" -}}
{{- if .Values.haproxy.enterprise.enabled -}}
1000
{{- else -}}
99
{{- end -}}
{{- end -}}

{{/*
Dataplane API username
Uses provided value or defaults to "admin"
*/}}
{{- define "haptic.dataplane.username" -}}
{{- .Values.credentials.dataplane.username | default "admin" -}}
{{- end -}}

{{/*
Dataplane API password
Priority: 1) User-provided value, 2) Existing secret value, 3) Deterministic password from release identity

Uses lookup to preserve password across helm upgrades. When lookup is unavailable
(e.g., ArgoCD dry-run rendering), falls back to a deterministic hash based on
release name and namespace to prevent constant drift detection.
*/}}
{{- define "haptic.dataplane.password" -}}
{{- if .Values.credentials.dataplane.password -}}
{{- .Values.credentials.dataplane.password -}}
{{- else -}}
{{- $secretName := printf "%s-credentials" (include "haptic.fullname" .) -}}
{{- $existingSecret := lookup "v1" "Secret" .Release.Namespace $secretName -}}
{{- if and $existingSecret $existingSecret.data (index $existingSecret.data "dataplane_password") -}}
{{- index $existingSecret.data "dataplane_password" | b64dec -}}
{{- else -}}
{{- /* Deterministic password for GitOps tools where lookup returns empty */ -}}
{{- printf "%s-%s-haptic-dataplane-api" .Release.Name .Release.Namespace | sha256sum | trunc 32 -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Convert Kubernetes memory value to megabytes for HAProxy -m flag.
Supports: Gi, Mi, G, M, Ki, K formats
Returns empty string if no memory requests configured.
*/}}
{{- define "haptic.haproxy.memoryLimitMB" -}}
{{- $memory := "" -}}
{{- if .Values.haproxy.resources -}}
{{- if .Values.haproxy.resources.requests -}}
{{- $memory = .Values.haproxy.resources.requests.memory | default "" -}}
{{- end -}}
{{- end -}}
{{- if $memory -}}
  {{- if hasSuffix "Gi" $memory -}}
    {{- $val := trimSuffix "Gi" $memory | float64 -}}
    {{- mul $val 1024 | int -}}
  {{- else if hasSuffix "Mi" $memory -}}
    {{- trimSuffix "Mi" $memory | int -}}
  {{- else if hasSuffix "G" $memory -}}
    {{- $val := trimSuffix "G" $memory | float64 -}}
    {{- mul $val 1000 | int -}}
  {{- else if hasSuffix "M" $memory -}}
    {{- trimSuffix "M" $memory | int -}}
  {{- else if hasSuffix "Ki" $memory -}}
    {{- $val := trimSuffix "Ki" $memory | float64 -}}
    {{- div $val 1024 | int -}}
  {{- else if hasSuffix "K" $memory -}}
    {{- $val := trimSuffix "K" $memory | float64 -}}
    {{- div $val 1000 | int -}}
  {{- else -}}
    {{- /* Assume bytes, convert to MB */ -}}
    {{- div ($memory | float64) 1048576 | int -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
Convert a Kubernetes memory string to megabytes.
Supports: Gi, Mi, G, M, Ki, K formats.
Input: memory string (e.g., "256Mi", "1Gi")
Returns: integer megabytes, or 0 if parsing fails
*/}}
{{- define "haptic.memoryToMB" -}}
{{- $memory := . -}}
{{- if $memory -}}
  {{- if hasSuffix "Gi" $memory -}}
    {{- $val := trimSuffix "Gi" $memory | float64 -}}
    {{- mul $val 1024 | int -}}
  {{- else if hasSuffix "Mi" $memory -}}
    {{- trimSuffix "Mi" $memory | int -}}
  {{- else if hasSuffix "G" $memory -}}
    {{- $val := trimSuffix "G" $memory | float64 -}}
    {{- mul $val 1000 | int -}}
  {{- else if hasSuffix "M" $memory -}}
    {{- trimSuffix "M" $memory | int -}}
  {{- else if hasSuffix "Ki" $memory -}}
    {{- $val := trimSuffix "Ki" $memory | float64 -}}
    {{- div $val 1024 | int -}}
  {{- else if hasSuffix "K" $memory -}}
    {{- $val := trimSuffix "K" $memory | float64 -}}
    {{- div $val 1000 | int -}}
  {{- else -}}
    {{- /* Assume bytes, convert to MB */ -}}
    {{- div ($memory | float64) 1048576 | int -}}
  {{- end -}}
{{- else -}}
  {{- 0 -}}
{{- end -}}
{{- end -}}

{{/*
Calculate the effective GOMAXPROCS value for the dataplane container.
Returns the numeric value in ALL cases (for use in calculations like maxParallel).
Priority:
  1. If user set GOMAXPROCS in extraEnv → use that
  2. If CPU limit exists → estimate from CPU (ceil of limit)
  3. If memory limit exists → calculate from memory (mem_MB / 64)
  4. Fallback → 2
Input: .Values.haproxy.dataplane context
*/}}
{{- define "haptic.dataplane.gomaxprocsValue" -}}
{{- $resources := .resources -}}
{{- $extraEnv := .extraEnv | default list -}}
{{- $result := 0 -}}
{{- /* 1. Check if user explicitly set GOMAXPROCS */ -}}
{{- range $extraEnv -}}
  {{- if eq .name "GOMAXPROCS" -}}
    {{- $result = .value | int -}}
  {{- end -}}
{{- end -}}
{{- if eq $result 0 -}}
  {{- /* 2. If CPU limit exists, estimate from it (automaxprocs behavior) */ -}}
  {{- if and $resources $resources.limits $resources.limits.cpu -}}
    {{- $cpuLimit := $resources.limits.cpu | toString -}}
    {{- /* Parse CPU: "2" -> 2, "2000m" -> 2, "500m" -> 1 */ -}}
    {{- if hasSuffix "m" $cpuLimit -}}
      {{- $millis := trimSuffix "m" $cpuLimit | int -}}
      {{- /* ceil(millis/1000): add 999 then divide */ -}}
      {{- $result = max 1 (div (add $millis 999) 1000) -}}
    {{- else -}}
      {{- $result = $cpuLimit | int -}}
    {{- end -}}
  {{- else if and $resources $resources.limits $resources.limits.memory -}}
    {{- /* 3. Calculate from memory limit */ -}}
    {{- $memLimit := $resources.limits.memory -}}
    {{- $memMB := include "haptic.memoryToMB" $memLimit | int -}}
    {{- $result = div $memMB 64 | int -}}
  {{- end -}}
{{- end -}}
{{- /* 4. Ensure minimum of 2 */ -}}
{{- if lt $result 2 -}}
  {{- $result = 2 -}}
{{- end -}}
{{- $result -}}
{{- end -}}

{{/*
Calculate maxParallel for controller config dataplane section.
If user explicitly set maxParallel (including 0), use that value.
Otherwise, auto-calculate as dataplane GOMAXPROCS * 10.
Input: root context (.)
*/}}
{{- define "haptic.config.dataplane.maxParallel" -}}
{{- /* Check if user explicitly set maxParallel to a number (including 0) */ -}}
{{- if hasKey .Values.controller.config.dataplane "maxParallel" -}}
  {{- .Values.controller.config.dataplane.maxParallel | int -}}
{{- else -}}
  {{- /* Auto-calculate: GOMAXPROCS * 10 */ -}}
  {{- $gomaxprocs := include "haptic.dataplane.gomaxprocsValue" .Values.haproxy.dataplane | int -}}
  {{- mul $gomaxprocs 10 -}}
{{- end -}}
{{- end -}}

{{/*
Auto-calculate GOMAXPROCS for dataplane container.
Returns env var YAML if:
  - No CPU limit is set (automaxprocs won't work correctly)
  - User hasn't provided GOMAXPROCS in extraEnv
Formula: max(2, floor(memory_limit_MB / 128))
Input: .Values.haproxy.dataplane context
*/}}
{{- define "haptic.dataplane.autoGomaxprocs" -}}
{{- $resources := .resources -}}
{{- $extraEnv := .extraEnv | default list -}}
{{- /* Check if user already set GOMAXPROCS */ -}}
{{- $userSetGomaxprocs := false -}}
{{- range $extraEnv -}}
  {{- if eq .name "GOMAXPROCS" -}}
    {{- $userSetGomaxprocs = true -}}
  {{- end -}}
{{- end -}}
{{- /* Check if CPU limit exists (automaxprocs will handle it) */ -}}
{{- $hasCpuLimit := false -}}
{{- if and $resources $resources.limits $resources.limits.cpu -}}
  {{- $hasCpuLimit = true -}}
{{- end -}}
{{- /* Auto-calculate only if needed */ -}}
{{- if and (not $userSetGomaxprocs) (not $hasCpuLimit) -}}
  {{- $memLimit := "" -}}
  {{- if and $resources $resources.limits -}}
    {{- $memLimit = $resources.limits.memory | default "" -}}
  {{- end -}}
  {{- if $memLimit -}}
    {{- $memMB := include "haptic.memoryToMB" $memLimit | int -}}
    {{- $gomaxprocs := div $memMB 64 | int -}}
    {{- if lt $gomaxprocs 2 -}}
      {{- $gomaxprocs = 2 -}}
    {{- end -}}
- name: GOMAXPROCS
  value: {{ $gomaxprocs | quote }}
  {{- end -}}
{{- end -}}
{{- end -}}
