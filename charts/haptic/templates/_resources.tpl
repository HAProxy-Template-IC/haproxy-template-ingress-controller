{{/*
Resource math: memory parsing, CPU → nbthread, dataplane GOMAXPROCS / maxParallel,
HAProxy shm-stats sizing, and the bootstrap-config checksum (which depends on
several of these values, so it lives next to them).

The previous helper `haptic.haproxy.memoryLimitMB` was a thin wrapper around
`haptic.memoryToMB` with a hardcoded input source; it was deleted in #4 and
its single caller now invokes `haptic.memoryToMB` directly with the memory
string from values. See ADR/CHANGELOG entries.
*/}}

{{/*
Calculate nbthread for HAProxy global section.
If haproxy.nbthread is explicitly set, use that value (rendered via tpl for templatability).
Otherwise, auto-calculate from haproxy.resources.requests.cpu using ceiling arithmetic.
Supports: millicores (e.g., "250m") and whole cores (e.g., "2").
Returns empty string if no CPU requests configured and no override, or if override is 0.
*/}}
{{- define "haptic.haproxy.nbthread" -}}
{{- if and .Values.haproxy (hasKey .Values.haproxy "nbthread") -}}
  {{- $override := tpl (toString .Values.haproxy.nbthread) . | int -}}
  {{- if gt $override 0 -}}
    {{- $override -}}
  {{- end -}}
{{- else -}}
  {{- $cpu := "" -}}
  {{- if .Values.haproxy.resources -}}
  {{- if .Values.haproxy.resources.requests -}}
  {{- $cpu = .Values.haproxy.resources.requests.cpu | default "" | toString -}}
  {{- end -}}
  {{- end -}}
  {{- if $cpu -}}
    {{- if hasSuffix "m" $cpu -}}
      {{- $millis := trimSuffix "m" $cpu | int -}}
      {{- /* ceil(millis/1000): add 999 then divide */ -}}
      {{- max 1 (div (add $millis 999) 1000) -}}
    {{- else -}}
      {{- max 1 ($cpu | int) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
Checksum of HAProxy bootstrap ConfigMap inputs.
Changes when any value feeding into the bootstrap config changes,
triggering a rolling update of HAProxy pods.
*/}}
{{- define "haptic.haproxy.bootstrapConfigChecksum" -}}
{{- printf "%v-%v-%v-%v" (.Values.haproxy.ports | toJson) (include "haptic.haproxy.nbthread" . | default "0") (.Values.haproxy.initialConfig | default "") (.Values.haproxy.shmStats | toJson) | sha256sum -}}
{{- end -}}

{{/*
Calculate /dev/shm emptyDir sizeLimit for HAProxy shm-stats-file.
Auto-calculates from maxObjects if shmSizeLimit is not explicitly set.
Formula: ceil(maxObjects * 4096 * 1.1 / 1048576) MiB
  - 4096 bytes per object (empirically ~3.2KB, 4KB provides safety margin)
  - 1.1 multiplier for filesystem overhead (10%)
  - Converted to MiB, rounded up
*/}}
{{- define "haptic.haproxy.shmSizeLimit" -}}
{{- if .Values.haproxy.shmStats.shmSizeLimit -}}
  {{- .Values.haproxy.shmStats.shmSizeLimit -}}
{{- else -}}
  {{- $maxObjects := .Values.haproxy.shmStats.maxObjects | int -}}
  {{- $bytesNeeded := mul $maxObjects 4096 -}}
  {{- $bytesWithMargin := add $bytesNeeded (div $bytesNeeded 10) -}}
  {{- $mib := div (add $bytesWithMargin 1048575) 1048576 -}}
  {{- printf "%dMi" $mib -}}
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
Formula: max(2, floor(memory_limit_MB / 64))
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
