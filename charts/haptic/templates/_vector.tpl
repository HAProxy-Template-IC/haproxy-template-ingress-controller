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
  {{- if not (has $field (list "enabled" "image" "metricsPort" "socketPath" "scrapeIntervalSecs" "excludeMetrics" "excludeMaintServerMetrics" "resources" "securityContext" "podMonitor" "extraVolumeMounts")) -}}
    {{- fail (printf "vector contains unknown field %q. Valid fields: enabled, image, metricsPort, socketPath, scrapeIntervalSecs, excludeMetrics, excludeMaintServerMetrics, resources, securityContext, podMonitor, extraVolumeMounts." $field) -}}
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
  {{- /* The path is interpolated into the vector container's `/bin/sh -c` start
         script (it unlinks a stale socket before exec'ing vector), so restrict it
         to an explicit allowlist rather than trusting it. It is quoted there as
         well, but a value containing a single quote would break out of the
         quoting, so the charset check is the real boundary — belt and braces,
         because this is the one place a values string reaches a shell. */ -}}
  {{- if not (regexMatch "^[A-Za-z0-9._/-]+$" ($v.socketPath | toString)) -}}
    {{- fail (printf "vector.socketPath may only contain letters, digits, dot, underscore, hyphen and slash, got %q. It is interpolated into the sidecar's shell start script, so shell metacharacters are refused." $v.socketPath) -}}
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
  {{- /* Validate the exclusion patterns. An invalid or quote-bearing regex reaches
         the rendered vector config and fails its load — which crash-loops the
         sidecar, so it has to be caught here. VRL uses the Rust regex crate and
         Go uses RE2; both are RE2-family, so a pattern Go rejects would not have
         worked there either. */ -}}
  {{- /* Not a string: it becomes a scrape-URL suffix, and a truthy-looking string
         like "false" would silently enable the filter. */ -}}
  {{- if not (kindIs "bool" $v.excludeMaintServerMetrics) -}}
    {{- fail (printf "vector.excludeMaintServerMetrics must be a boolean, got %v." $v.excludeMaintServerMetrics) -}}
  {{- end -}}
  {{- if not (kindIs "slice" ($v.excludeMetrics | default list)) -}}
    {{- fail "vector.excludeMetrics must be a list of regex strings." -}}
  {{- end -}}
  {{- range $pat := ($v.excludeMetrics | default list) -}}
    {{- $ps := $pat | toString -}}
    {{- if eq (trim $ps) "" -}}
      {{- fail "vector.excludeMetrics contains an empty pattern. An empty regex matches every metric name and would drop the entire exposition." -}}
    {{- end -}}
    {{- /* Checked character by character rather than with one escaped regex: the
           escapes needed for a combined pattern do not survive Helm's parser. */ -}}
    {{- range $bad := (list "'" "\"" "\\" "\n" "\r") -}}
      {{- if contains $bad $ps -}}
        {{- fail (printf "vector.excludeMetrics pattern %q contains a quote, backslash or newline. Patterns are embedded in a VRL r'...' literal in the rendered config, so those characters would break vector's config load and crash-loop the sidecar." $ps) -}}
      {{- end -}}
    {{- end -}}
    {{- /* Compile check. MUST be mustRegexMatch: sprig's plain regexMatch does
           `match, _ := regexp.MatchString(...)` — it DISCARDS the compile error and
           returns false, so `regexMatch $ps ""` validated nothing at all and an
           uncompilable pattern rendered clean, then crash-looped the sidecar. */ -}}
    {{- $_ := mustRegexMatch $ps "" -}}
  {{- end -}}
  {{- if lt ($v.scrapeIntervalSecs | int) 1 -}}
    {{- fail (printf "vector.scrapeIntervalSecs must be a positive integer, got %v." $v.scrapeIntervalSecs) -}}
  {{- end -}}
  {{- /* Refuse the combination that silently stops all scraping. With the sidecar
         on, the chart skips the spoaHub PodMonitor (vector fronts both endpoints)
         — so if vector's own PodMonitor is off, an operator who HAD working hub +
         HAProxy scraping loses every haproxy_* and spoa_* series on upgrade, with
         nothing failing to tell them. Exactly the state a live cluster was in
         before this guard existed. Failing the render is loud and one line to
         resolve either way. */ -}}
  {{- /* Gated on the hub actually being DEPLOYED, not just on the flag. spoaHub.enabled
         auto-derives from plugins.*.enabled and defaults to null, so a values file can
         carry monitoring.podMonitor.enabled=true with no plugins on — the hub never
         renders, its PodMonitor never rendered either, and nothing is lost. Failing
         that configuration would be a false positive. Mirrors the same predicate
         spoa-hub-podmonitor.yaml itself uses. */ -}}
  {{- if and (include "haptic.spoaHub.enabled" .) .Values.spoaHub.monitoring.podMonitor.enabled (not $v.podMonitor.enabled) -}}
    {{- fail "spoaHub.monitoring.podMonitor.enabled=true has no effect while vector.enabled=true: the chart skips that PodMonitor because the vector sidecar re-exports both the hub's and HAProxy's metrics on one endpoint. Set vector.podMonitor.enabled=true to scrape the merged endpoint (recommended), or set vector.enabled=false to keep scraping the hub directly. Leaving it as-is would silently stop every haproxy_* and spoa_* scrape." -}}
  {{- end -}}
  {{- if eq (trim ($v.image.tag | toString)) "" -}}
    {{- fail "vector.image.tag must be pinned to an explicit tag so a silent upstream bump can't change the log pipeline under a running fleet." -}}
  {{- end -}}
{{- end -}}
{{- end -}}
