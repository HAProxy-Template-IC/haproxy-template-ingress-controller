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
{{- range $field := keys $v -}}
  {{- if eq $field "global" -}}{{- continue -}}{{- end -}}
  {{- if not (has $field (list "enabled" "image" "metricsPort" "sizeMetricsPort" "socketPath" "scrapeIntervalSecs" "excludeMetrics" "excludeMaintServerMetrics" "omitEmptyLogFields" "logMetrics" "requestMetrics" "resources" "securityContext" "podMonitor" "extraVolumeMounts")) -}}
    {{- fail (printf "vector contains unknown field %q. Valid fields: enabled, image, metricsPort, sizeMetricsPort, socketPath, scrapeIntervalSecs, excludeMetrics, excludeMaintServerMetrics, omitEmptyLogFields, logMetrics, requestMetrics, resources, securityContext, podMonitor, extraVolumeMounts." $field) -}}
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
  {{- /* Same reason as excludeMaintServerMetrics: a truthy-looking string like
         "false" would silently enable the transform. */ -}}
  {{- if not (kindIs "bool" $v.omitEmptyLogFields) -}}
    {{- fail (printf "vector.omitEmptyLogFields must be a boolean, got %v." $v.omitEmptyLogFields) -}}
  {{- end -}}
  {{- /* Not a string: it becomes a scrape-URL suffix, and a truthy-looking string
         like "false" would silently enable the filter. */ -}}
  {{- if not (kindIs "bool" $v.excludeMaintServerMetrics) -}}
    {{- fail (printf "vector.excludeMaintServerMetrics must be a boolean, got %v." $v.excludeMaintServerMetrics) -}}
  {{- end -}}
  {{- /* Reject exclusion patterns that Vector cannot load; Go and VRL accept the
         same regular-expression family. */ -}}
  {{- if not (kindIs "map" ($v.excludeMetrics | default dict)) -}}
    {{- fail "vector.excludeMetrics must be a map of named exclusions, each with `pattern` and `enabled`. It was a list until 0.2.0; a list is replaced wholesale by Helm, so disabling one exclusion meant restating every other and silently losing any added later." -}}
  {{- end -}}
  {{- range $name, $ex := ($v.excludeMetrics | default dict) -}}
    {{- if not (kindIs "map" $ex) -}}
      {{- fail (printf "vector.excludeMetrics.%s must be a map with `pattern` and `enabled`, got %T." $name $ex) -}}
    {{- end -}}
    {{- range $field, $_ := $ex -}}
      {{- if not (has $field (list "enabled" "pattern" "families" "requires")) -}}
        {{- fail (printf "vector.excludeMetrics.%s contains unknown field %q. Valid fields: enabled, pattern, families, requires." $name $field) -}}
      {{- end -}}
    {{- end -}}
    {{- /* An exclusion whose replacement is off would drop a family with nothing
           standing in for it. Resolved via haptic.vector.valuePath. */ -}}
    {{- if hasKey $ex "requires" -}}
      {{- if not (regexMatch "^[A-Za-z_][A-Za-z0-9_]*(\\.[A-Za-z_][A-Za-z0-9_]*)*$" ($ex.requires | toString)) -}}
        {{- fail (printf "vector.excludeMetrics.%s.requires must be a dotted values path such as `vector.requestMetrics.enabled`, got %q." $name $ex.requires) -}}
      {{- end -}}
    {{- end -}}
    {{- /* `enabled` is required, not defaulted. Defaulting it to false means an
           operator who adds `mine: {pattern: ...}` and forgets the flag gets a
           silently inert entry: it passes validation, is skipped by the
           resolution loop, and nothing is ever excluded. The doc says an entry
           needs `pattern` and `enabled`, so say so at render time. */ -}}
    {{- if not (hasKey $ex "enabled") -}}
      {{- fail (printf "vector.excludeMetrics.%s has no `enabled`. It is required, so that an entry cannot sit inert and silently exclude nothing — set `enabled: false` to keep it and turn it off." $name) -}}
    {{- end -}}
    {{- if not (kindIs "bool" $ex.enabled) -}}
      {{- fail (printf "vector.excludeMetrics.%s.enabled must be a boolean." $name) -}}
    {{- end -}}
    {{- /* An entry disabled by an operator override keeps its (unused) pattern,
           so only validate what will actually be rendered. */ -}}
    {{- if not $ex.enabled -}}
      {{- continue -}}
    {{- end -}}
    {{- if not $ex.pattern -}}
      {{- fail (printf "vector.excludeMetrics.%s is enabled but has no `pattern`." $name) -}}
    {{- end -}}
    {{- $ps := $ex.pattern | toString -}}
    {{- if eq (trim $ps) "" -}}
      {{- fail "vector.excludeMetrics contains an empty pattern. An empty regex matches every metric name and would drop the entire exposition." -}}
    {{- end -}}
    {{- /* Checked character by character rather than with one escaped regex: the
           escapes needed for a combined pattern do not survive Helm's parser. */ -}}
    {{- range $bad := (list "'" "\"" "\\" "\n" "\r") -}}
      {{- if contains $bad $ps -}}
        {{- fail (printf "vector.excludeMetrics pattern %q contains a quote, backslash or newline. Patterns are embedded in a VRL r'...' literal in the rendered config, so those characters would break vector's config load and keep log and metric export unavailable." $ps) -}}
      {{- end -}}
    {{- end -}}
    {{- /* mustRegexMatch surfaces compile errors; regexMatch discards them. */ -}}
    {{- $_ := mustRegexMatch $ps "" -}}
    {{- /* families are exact metric names appended to HAProxy's scrape URL as
           `metrics=-<name>`, so they never reach vector's parser — that is what
           makes them cut the parse burst rather than only retention.
           They are an OPTIMISATION: `pattern` is authoritative and still drops
           anything missing here, one stage later. Two guards:
             1. every name must match its own entry's pattern, so the two cannot
                drift and a stale list cannot quietly exclude something else;
             2. charset, because HAProxy IGNORES an unknown name silently, and a
                '%' starts a percent-escape that corrupts the whole parameter —
                measured: one stray '%' took a 204,914-series scrape down to 3. */ -}}
    {{- if not (kindIs "slice" ($ex.families | default list)) -}}
      {{- fail (printf "vector.excludeMetrics.%s.families must be a list of exact metric names." $name) -}}
    {{- end -}}
    {{- range $fam := ($ex.families | default list) -}}
      {{- $fs := $fam | toString -}}
      {{- if not (mustRegexMatch "^[a-zA-Z_][a-zA-Z0-9_]*$" $fs) -}}
        {{- fail (printf "vector.excludeMetrics.%s.families entry %q is not a bare metric name. It is sent to HAProxy as a scrape-URL parameter, where anything else is either ignored silently or corrupts the parameter." $name $fs) -}}
      {{- end -}}
      {{- if not (mustRegexMatch $ps $fs) -}}
        {{- fail (printf "vector.excludeMetrics.%s.families entry %q does not match that entry's own pattern %q. families is an optimisation for pattern, so a name outside it would be excluded at the source without pattern ever agreeing." $name $fs $ps) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- if lt ($v.scrapeIntervalSecs | int) 1 -}}
    {{- fail (printf "vector.scrapeIntervalSecs must be a positive integer, got %v." $v.scrapeIntervalSecs) -}}
  {{- end -}}
  {{- include "haptic.vector.validateRequestMetrics" . -}}
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
Walk a dotted .Values path; emit "true" when truthy. Backs `requires` on both
logMetrics and excludeMetrics. Here, not in the library, because the flags live
in values rather than extraContext.

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
