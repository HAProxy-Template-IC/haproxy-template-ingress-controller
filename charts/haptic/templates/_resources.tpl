{{/*
Resource math: CPU/memory quantity parsing, HAProxy nbthread, dataplane
GOMAXPROCS / maxParallel, HAProxy shm-stats sizing, and the
bootstrap-config checksum (which depends on several of these values, so
it lives next to them).
*/}}

{{/*
Convert a Kubernetes CPU quantity to whole cores using ceiling arithmetic.
Supports millicores (e.g. "250m" → 1, "2000m" → 2) and whole cores
(e.g. "2" → 2). Empty / zero inputs render as 0; callers that want a
floor of 1 should clamp with `max 1` themselves.
Input: CPU quantity string.
*/}}
{{- define "haptic.cpuToCores" -}}
{{- $cpu := . | toString -}}
{{- if hasSuffix "m" $cpu -}}
  {{- /* ceil(millis/1000): add 999 then divide */ -}}
  {{- div (add (trimSuffix "m" $cpu | int) 999) 1000 -}}
{{- else -}}
  {{- $cpu | int -}}
{{- end -}}
{{- end -}}

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
  {{- with dig "requests" "cpu" "" .Values.haproxy.resources -}}
    {{- max 1 (include "haptic.cpuToCores" . | int) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
Checksum of HAProxy bootstrap ConfigMap inputs.
Changes when any value feeding into the bootstrap config changes,
triggering a rolling update of HAProxy pods.
*/}}
{{- define "haptic.haproxy.bootstrapConfigChecksum" -}}
{{- printf "%v-%v" (tpl (.Values.haproxy.initialConfig | default "") .) (.Values.haproxy.shmStats | toJson) | sha256sum -}}
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
{{- $shmStats := .Values.haproxy.shmStats -}}
{{- with $shmStats.shmSizeLimit -}}
  {{- . -}}
{{- else -}}
  {{- $bytesNeeded := mul ($shmStats.maxObjects | int) 4096 -}}
  {{- $bytesWithMargin := add $bytesNeeded (div $bytesNeeded 10) -}}
  {{- printf "%dMi" (div (add $bytesWithMargin 1048575) 1048576) -}}
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
    {{- mul (trimSuffix "Gi" $memory | float64) 1024 | int -}}
  {{- else if hasSuffix "Mi" $memory -}}
    {{- trimSuffix "Mi" $memory | int -}}
  {{- else if hasSuffix "G" $memory -}}
    {{- mul (trimSuffix "G" $memory | float64) 1000 | int -}}
  {{- else if hasSuffix "M" $memory -}}
    {{- trimSuffix "M" $memory | int -}}
  {{- else if hasSuffix "Ki" $memory -}}
    {{- div (trimSuffix "Ki" $memory | float64) 1024 | int -}}
  {{- else if hasSuffix "K" $memory -}}
    {{- div (trimSuffix "K" $memory | float64) 1000 | int -}}
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
{{- $result := 0 -}}
{{- /* 1. Check if user explicitly set GOMAXPROCS */ -}}
{{- range .extraEnv | default list -}}
  {{- if eq .name "GOMAXPROCS" -}}
    {{- $result = .value | int -}}
  {{- end -}}
{{- end -}}
{{- if eq $result 0 -}}
  {{- $limits := .resources.limits | default dict -}}
  {{- if $limits.cpu -}}
    {{- /* 2. If CPU limit exists, estimate from it (automaxprocs behavior) */ -}}
    {{- $result = max 1 (include "haptic.cpuToCores" $limits.cpu | int) -}}
  {{- else if $limits.memory -}}
    {{- /* 3. Calculate from memory limit */ -}}
    {{- $result = div (include "haptic.memoryToMB" $limits.memory | int) 64 -}}
  {{- end -}}
{{- end -}}
{{- /* 4. Ensure minimum of 2 */ -}}
{{- max 2 $result -}}
{{- end -}}

{{/*
Calculate maxParallel for controller config dataplane section.
If user explicitly set maxParallel (including 0), use that value.
Otherwise, auto-calculate as dataplane GOMAXPROCS * 10.
Input: root context (.)
*/}}
{{- define "haptic.config.dataplane.maxParallel" -}}
{{- $dpConfig := .Values.controller.config.dataplane -}}
{{- /* Check if user explicitly set maxParallel to a number (including 0) */ -}}
{{- if hasKey $dpConfig "maxParallel" -}}
  {{- $dpConfig.maxParallel | int -}}
{{- else -}}
  {{- /* Auto-calculate: GOMAXPROCS * 10 */ -}}
  {{- mul (include "haptic.dataplane.gomaxprocsValue" .Values.haproxy.dataplane | int) 10 -}}
{{- end -}}
{{- end -}}

{{/*
Auto-calculate GOMAXPROCS for dataplane container.
Returns env var YAML if:
  - No CPU limit is set (automaxprocs won't work correctly)
  - User hasn't provided GOMAXPROCS in extraEnv
  - A memory limit is set (otherwise there's no signal to derive from)
Value comes from haptic.dataplane.gomaxprocsValue, which uses the same
mem_MB / 64 formula (min 2) when only a memory limit is present.
Input: .Values.haproxy.dataplane context
*/}}
{{- define "haptic.dataplane.autoGomaxprocs" -}}
{{- $limits := .resources.limits | default dict -}}
{{- $envNames := list -}}
{{- range .extraEnv | default list -}}{{- $envNames = append $envNames .name -}}{{- end -}}
{{- if and (not (has "GOMAXPROCS" $envNames)) (not $limits.cpu) $limits.memory -}}
- name: GOMAXPROCS
  value: {{ include "haptic.dataplane.gomaxprocsValue" . | quote }}
{{- end -}}
{{- end -}}
