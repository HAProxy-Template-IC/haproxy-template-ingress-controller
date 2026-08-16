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
{{- /* `global` is Helm's, not the operator's: naming the subchart `vector` makes
       Helm inject .Values.vector.global into its values namespace. */ -}}
{{- /* Moved keys fail with the destination, not as "unknown field": HAProxy's
       exporter is scraped directly since the re-export was removed, and one
       PodMonitor now covers every endpoint on the HAProxy pod. */ -}}
{{- if hasKey $v "excludeMetrics" -}}
  {{- fail "vector.excludeMetrics moved to controller.config.templatingSettings.extraContext.prometheusExporter.excludeMetrics: HAProxy applies the exclusions itself, and Prometheus scrapes its exporter directly instead of through vector. Entries keep their names, `enabled`, `families` and `requires`; `pattern` was dropped — the exporter filters by exact family name." -}}
{{- end -}}
{{- if hasKey $v "excludeMaintServerMetrics" -}}
  {{- fail "vector.excludeMaintServerMetrics moved to controller.config.templatingSettings.extraContext.prometheusExporter.excludeMaintServers: HAProxy applies ?no-maint itself, for every scraper." -}}
{{- end -}}
{{- if hasKey $v "podMonitor" -}}
  {{- fail "vector.podMonitor moved to haproxy.monitoring.podMonitor: one PodMonitor now scrapes every metrics endpoint on the HAProxy pod — HAProxy's exporter (stats port), vector's endpoints and, without vector, the hub's. Same fields (enabled, interval, scrapeTimeout, labels, relabelings, metricRelabelings)." -}}
{{- end -}}
{{- range $field := keys $v -}}
  {{- if eq $field "global" -}}{{- continue -}}{{- end -}}
  {{- if not (has $field (list "enabled" "image" "metricsPort" "sizeMetricsPort" "socketPath" "scrapeIntervalSecs" "omitEmptyLogFields" "logMetrics" "requestMetrics" "resources" "securityContext" "extraVolumeMounts")) -}}
    {{- fail (printf "vector contains unknown field %q. Valid fields: enabled, image, metricsPort, sizeMetricsPort, socketPath, scrapeIntervalSecs, omitEmptyLogFields, logMetrics, requestMetrics, resources, securityContext, extraVolumeMounts." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $v.enabled) -}}
  {{- fail "vector.enabled must be a boolean." -}}
{{- end -}}
{{- if $v.enabled -}}
  {{- /* Validate the dormant port too, before request_size activates its child listener. */ -}}
  {{- $ports := dict "metricsPort" ($v.metricsPort | int) -}}
  {{- $_ := set $ports "sizeMetricsPort" ($v.sizeMetricsPort | int) -}}
  {{- $hubPort := include "haptic.spoaHub.metricsPort" . -}}
  {{- range $key, $port := $ports -}}
    {{- if or (lt $port 1) (gt $port 65535) -}}
      {{- fail (printf "vector.%s must be a TCP port between 1 and 65535, got %v." $key $port) -}}
    {{- end -}}
    {{- /* A collision here renders a pod that crash-loops on bind, so catch it at
           render time rather than in CrashLoopBackOff. */ -}}
    {{- range $name, $p := $.Values.haproxy.ports -}}
      {{- if and (ne ($p | int) 0) (eq ($p | int) $port) -}}
        {{- fail (printf "vector.%s (%v) collides with haproxy.ports.%s. Both bind in the same pod network namespace; pick a different port." $key $port $name) -}}
      {{- end -}}
    {{- end -}}
    {{- if and (ne $hubPort "") (eq ($hubPort | int) $port) -}}
      {{- fail (printf "vector.%s (%v) collides with the spoa-hub metrics port. Pick a different port." $key $port) -}}
    {{- end -}}
  {{- end -}}
  {{- if eq ($v.metricsPort | int) ($v.sizeMetricsPort | int) -}}
    {{- fail (printf "vector.metricsPort and vector.sizeMetricsPort are both %v. They are two prometheus_exporter sinks with different histogram buckets, so they cannot share a port." ($v.metricsPort | int)) -}}
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
  {{- /* A truthy-looking string like "false" would silently enable the transform. */ -}}
  {{- if not (kindIs "bool" $v.omitEmptyLogFields) -}}
    {{- fail (printf "vector.omitEmptyLogFields must be a boolean, got %v." $v.omitEmptyLogFields) -}}
  {{- end -}}
  {{- if lt ($v.scrapeIntervalSecs | int) 1 -}}
    {{- fail (printf "vector.scrapeIntervalSecs must be a positive integer, got %v." $v.scrapeIntervalSecs) -}}
  {{- end -}}
  {{- include "haptic.vector.validateRequestMetrics" . -}}
  {{- if eq (trim ($v.image.tag | toString)) "" -}}
    {{- fail "vector.image.tag must be pinned to an explicit tag so a silent upstream bump can't change the log pipeline under a running fleet." -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/* The emitted name suffixes. Keep in step with values.yaml and vector.yaml. */}}
{{- define "haptic.vector.requestMetricNames" -}}
requests request_duration_seconds response_duration_seconds connect_duration_seconds header_duration_seconds request_size response_size
{{- end -}}

{{/* Shared by the container port, the PodMonitor endpoint and the projection, so
the three cannot disagree about whether sizeMetricsPort is listening. */}}
{{- define "haptic.vector.sizeExporterEnabled" -}}
{{- $rm := .Values.vector.requestMetrics | default dict -}}
{{- if and .Values.vector.enabled $rm.enabled -}}
  {{- $m := $rm.metrics | default dict -}}
  {{- if or (index $m "request_size") (index $m "response_size") -}}
true
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
Walk a dotted .Values path; emit "true" when truthy. Backs `requires` on
logMetrics. Here, not in the library, because the flags live in values rather
than extraContext.

Usage: include "haptic.vector.valuePath" (dict "root" $.Values "path" "a.b.c")
*/}}
{{- define "haptic.vector.valuePath" -}}
{{- $cur := .root -}}
{{- range $seg := splitList "." .path -}}
  {{- if kindIs "map" $cur -}}
    {{- $cur = index $cur $seg -}}
  {{- else -}}
    {{- $cur = nil -}}
  {{- end -}}
{{- end -}}
{{- if $cur -}}
true
{{- end -}}
{{- end -}}

{{/* Mirrored in the Scriggo library because a hand-written CR bypasses Helm. */}}
{{- define "haptic.vector.validateRequestMetrics" -}}
{{- $rm := .Values.vector.requestMetrics | default dict -}}
{{- if not (kindIs "map" $rm) -}}
  {{- fail "vector.requestMetrics must be a map." -}}
{{- end -}}
{{- range $field, $_ := $rm -}}
  {{- if not (has $field (list "enabled" "prefix" "controllerClass" "terminationStateLabel" "pathLabel" "hostLabel" "durationBuckets" "sizeBuckets" "cardinalityLimit" "metrics")) -}}
    {{- fail (printf "vector.requestMetrics contains unknown field %q. Valid fields: enabled, prefix, controllerClass, terminationStateLabel, pathLabel, hostLabel, durationBuckets, sizeBuckets, cardinalityLimit, metrics." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $rm.enabled) -}}
  {{- fail (printf "vector.requestMetrics.enabled must be a boolean, got %v." $rm.enabled) -}}
{{- end -}}
{{- if $rm.enabled -}}
  {{- /* A trailing underscore is accepted (operators think of this as
         `nginx_ingress_controller_`) and stripped, so the name never doubles up. */ -}}
  {{- $prefix := $rm.prefix | toString | trimSuffix "_" -}}
  {{- if not (regexMatch "^[A-Za-z_][A-Za-z0-9_]*$" $prefix) -}}
    {{- fail (printf "vector.requestMetrics.prefix must be a Prometheus metric-name prefix (letters, digits, underscore; not starting with a digit), got %q." $rm.prefix) -}}
  {{- end -}}
  {{- /* Same reason as omitEmptyLogFields: a quoted "false" is truthy in
         Scriggo, so a string here would silently keep the label on. */ -}}
  {{- range $flag := list "terminationStateLabel" "pathLabel" "hostLabel" -}}
    {{- if not (kindIs "bool" (index $rm $flag)) -}}
      {{- fail (printf "vector.requestMetrics.%s must be a boolean, got %v. A quoted \"false\" is truthy in the render and would leave the label on." $flag (index $rm $flag)) -}}
    {{- end -}}
  {{- end -}}
  {{- /* `le` boundaries are cumulative: out of order the counts are nonsense and
         nothing reports it. */ -}}
  {{- range $which := list "durationBuckets" "sizeBuckets" -}}
    {{- $bs := index $rm $which -}}
    {{- if not (kindIs "slice" $bs) -}}
      {{- fail (printf "vector.requestMetrics.%s must be a list of numbers." $which) -}}
    {{- end -}}
    {{- if eq (len $bs) 0 -}}
      {{- fail (printf "vector.requestMetrics.%s is empty. A histogram with no boundaries reports only +Inf, which carries no information beyond the count." $which) -}}
    {{- end -}}
    {{- $prev := 0.0 -}}
    {{- range $i, $b := $bs -}}
      {{- if not (or (kindIs "float64" $b) (kindIs "int" $b) (kindIs "int64" $b)) -}}
        {{- fail (printf "vector.requestMetrics.%s[%d] must be a number, got %v (%T). Quote-wrapped numbers reach vector as strings and fail its config load." $which $i $b $b) -}}
      {{- end -}}
      {{- $f := $b | float64 -}}
      {{- if le $f 0.0 -}}
        {{- fail (printf "vector.requestMetrics.%s[%d] must be greater than zero, got %v." $which $i $b) -}}
      {{- end -}}
      {{- /* The render formats with 9 decimals, so anything smaller emits a
             le="0" boundary — and two such values collapse onto one. */ -}}
      {{- if lt $f 0.000000001 -}}
        {{- fail (printf "vector.requestMetrics.%s[%d] is %v, smaller than the exporter renders (9 decimals); it would emit a le=\"0\" boundary." $which $i $b) -}}
      {{- end -}}
      {{- if and (gt $i 0) (le $f $prev) -}}
        {{- fail (printf "vector.requestMetrics.%s must be strictly ascending; %v follows %v. Prometheus reads these as cumulative `le` boundaries, so out-of-order values produce silently wrong quantiles." $which $b $prev) -}}
      {{- end -}}
      {{- $prev = $f -}}
    {{- end -}}
  {{- end -}}
  {{- $cl := $rm.cardinalityLimit | default dict -}}
  {{- if not (kindIs "map" $cl) -}}
    {{- fail "vector.requestMetrics.cardinalityLimit must be a map with `enabled`, `valueLimit` and `action`." -}}
  {{- end -}}
  {{- range $field, $_ := $cl -}}
    {{- if not (has $field (list "enabled" "valueLimit" "action")) -}}
      {{- fail (printf "vector.requestMetrics.cardinalityLimit contains unknown field %q. Valid fields: enabled, valueLimit, action." $field) -}}
    {{- end -}}
  {{- end -}}
  {{- if not (kindIs "bool" $cl.enabled) -}}
    {{- fail (printf "vector.requestMetrics.cardinalityLimit.enabled must be a boolean, got %v." $cl.enabled) -}}
  {{- end -}}
  {{- if $cl.enabled -}}
    {{- if lt ($cl.valueLimit | int) 1 -}}
      {{- fail (printf "vector.requestMetrics.cardinalityLimit.valueLimit must be a positive integer, got %v." $cl.valueLimit) -}}
    {{- end -}}
    {{- if not (has ($cl.action | toString) (list "drop_tag" "drop_event")) -}}
      {{- fail (printf "vector.requestMetrics.cardinalityLimit.action must be \"drop_tag\" or \"drop_event\", got %q. drop_tag collapses the runaway label and keeps the totals; drop_event discards the requests, so a cardinality problem would read as an outage." $cl.action) -}}
    {{- end -}}
  {{- end -}}
  {{- $known := splitList " " (include "haptic.vector.requestMetricNames" .) -}}
  {{- $m := $rm.metrics | default dict -}}
  {{- if not (kindIs "map" $m) -}}
    {{- fail "vector.requestMetrics.metrics must be a map of metric name to boolean." -}}
  {{- end -}}
  {{- $on := 0 -}}
  {{- range $name, $val := $m -}}
    {{- if not (has $name $known) -}}
      {{- fail (printf "vector.requestMetrics.metrics contains unknown metric %q. Valid names: %s." $name (join ", " $known)) -}}
    {{- end -}}
    {{- if not (kindIs "bool" $val) -}}
      {{- fail (printf "vector.requestMetrics.metrics.%s must be a boolean, got %v." $name $val) -}}
    {{- end -}}
    {{- if $val -}}
      {{- $on = add1 $on -}}
    {{- end -}}
  {{- end -}}
  {{- if eq $on 0 -}}
    {{- fail "vector.requestMetrics.enabled is true but every entry in `metrics` is false, so the pipeline would parse each access-log record and emit nothing. Set vector.requestMetrics.enabled: false instead." -}}
  {{- end -}}
{{- end -}}
{{- end -}}
