{{/*
Vector sidecar helpers.

The sidecar is HAPTIC's own operational plumbing on the pods it deploys to — the
log/metrics path for the fleet it manages — so kind-specific Helm and Go for it is
correct and NOT a RULE #1 violation (see the "operational-identity exception" in
the root CLAUDE.md). It is not a resource an operator swaps out when describing
their routing.
*/}}

{{/* True when the vector sidecar should be rendered. */}}
{{- define "haptic.vector.enabled" -}}
{{- if .Values.vector.enabled -}}
true
{{- end -}}
{{- end -}}

{{- define "haptic.vector.configMapName" -}}
{{- printf "%s-vector" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "haptic.vector.image" -}}
{{- $v := .Values.vector.image -}}
{{- printf "%s:%s" $v.repository $v.tag -}}
{{- end -}}

{{/*
Directory holding the log socket. Derived from vector.socketPath so the two can
never drift: the haproxy container mounts this dir to reach the socket and vector
mounts it to create the socket.
*/}}
{{- define "haptic.vector.socketDir" -}}
{{- .Values.vector.socketPath | dir -}}
{{- end -}}

{{/*
The metrics port Prometheus should scrape for an HAProxy pod, and the endpoint
list that goes with it. When vector is enabled it fronts everything on one port;
otherwise callers fall back to the pre-existing direct endpoints.
*/}}
{{- define "haptic.vector.metricsPort" -}}
{{- .Values.vector.metricsPort | int -}}
{{- end -}}

{{/*
Validate the vector values. Duplicated in the Scriggo library on purpose: the
HAProxyTemplateConfig CR is a first-class API that bypasses Helm entirely, so a
guard that lives only here would not protect a hand-written CR.
*/}}
{{- define "haptic.vector.validateValues" -}}
{{- $v := .Values.vector -}}
{{- if not (kindIs "map" $v) -}}
  {{- fail "vector must be a map." -}}
{{- end -}}
{{- range $field := keys $v -}}
  {{- if not (has $field (list "enabled" "image" "metricsPort" "socketPath" "scrapeIntervalSecs" "resources" "securityContext" "podMonitor" "extraVolumeMounts")) -}}
    {{- fail (printf "vector contains unknown field %q. Valid fields: enabled, image, metricsPort, socketPath, scrapeIntervalSecs, resources, securityContext, podMonitor, extraVolumeMounts." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $v.enabled) -}}
  {{- fail "vector.enabled must be a boolean." -}}
{{- end -}}
{{- if $v.enabled -}}
  {{- $port := $v.metricsPort | int -}}
  {{- if or (lt $port 1) (gt $port 65535) -}}
    {{- fail (printf "vector.metricsPort must be a TCP port between 1 and 65535, got %v." $v.metricsPort) -}}
  {{- end -}}
  {{- /* A collision here renders a pod that crash-loops on bind, so catch it at
         render time rather than in CrashLoopBackOff. */ -}}
  {{- range $name, $p := .Values.haproxy.ports -}}
    {{- if and (ne ($p | int) 0) (eq ($p | int) ($v.metricsPort | int)) -}}
      {{- fail (printf "vector.metricsPort (%v) collides with haproxy.ports.%s. Both bind in the same pod network namespace; pick a different port." $v.metricsPort $name) -}}
    {{- end -}}
  {{- end -}}
  {{- $hubPort := include "haptic.spoaHub.metricsPort" . -}}
  {{- if and (ne $hubPort "") (eq ($hubPort | int) ($v.metricsPort | int)) -}}
    {{- fail (printf "vector.metricsPort (%v) collides with the spoa-hub metrics port. Pick a different port." $v.metricsPort) -}}
  {{- end -}}
  {{- if not (hasPrefix "/" ($v.socketPath | toString)) -}}
    {{- fail (printf "vector.socketPath must be an absolute path, got %q. HAProxy's `log <path>` form requires one." $v.socketPath) -}}
  {{- end -}}
  {{- /* HAProxy resolves a relative log path against default-path origin, and a
         path with whitespace breaks the generated `log` line's tokenisation. */ -}}
  {{- if regexMatch "[[:space:]]" ($v.socketPath | toString) -}}
    {{- fail (printf "vector.socketPath must not contain whitespace, got %q." $v.socketPath) -}}
  {{- end -}}
  {{- /* The socket's PARENT directory is what gets mounted as the shared emptyDir
         (haptic.vector.socketDir), in both the haproxy and vector containers. A
         path directly at the root, e.g. /haproxy.sock, makes that parent "/" and
         the mount would shadow each container's entire root filesystem — the pod
         breaks in a way that looks nothing like a bad log path. Rejected here
         rather than in the Scriggo library because the mount is Helm-only; the
         library validates what it can see (the address itself). */ -}}
  {{- if eq (dir ($v.socketPath | toString)) "/" -}}
    {{- fail (printf "vector.socketPath must be inside a subdirectory, not directly at the filesystem root, got %q. The chart mounts the socket's parent directory as a shared emptyDir in both the haproxy and vector containers, so a parent of \"/\" would shadow their root filesystems. Use something like /run/vector/haproxy.sock." $v.socketPath) -}}
  {{- end -}}
  {{- if lt ($v.scrapeIntervalSecs | int) 1 -}}
    {{- fail (printf "vector.scrapeIntervalSecs must be a positive integer, got %v." $v.scrapeIntervalSecs) -}}
  {{- end -}}
  {{- if eq (trim ($v.image.tag | toString)) "" -}}
    {{- fail "vector.image.tag must be pinned to an explicit tag so a silent upstream bump can't change the log pipeline under a running fleet." -}}
  {{- end -}}
{{- end -}}
{{- end -}}
