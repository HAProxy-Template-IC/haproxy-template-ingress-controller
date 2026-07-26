{{/*
Validate chart values whose authoritative owner is the controller workload or
the managed HAProxy fleet. In particular, reject historical duplicate paths:
keeping two writable values for one runtime setting lets manifests render while
the process listens somewhere else.
*/}}
{{- define "haptic.controller.validateValues" -}}
{{- $controller := .Values.controller -}}
{{- if not (kindIs "map" $controller) -}}{{- fail "controller must be a map." -}}{{- end -}}
{{- $movedRootValues := dict
      "replicaCount" "controller.replicaCount"
      "image" "controller.image"
      "deploymentAnnotations" "controller.deploymentAnnotations"
      "updateStrategy" "controller.updateStrategy"
      "minReadySeconds" "controller.minReadySeconds"
      "revisionHistoryLimit" "controller.revisionHistoryLimit"
      "serviceAccount" "controller.serviceAccount"
      "rbac" "controller.rbac"
      "service" "controller.service"
      "securityContext" "controller.securityContext"
      "resources" "controller.resources"
      "extraEnv" "controller.extraEnv"
      "extraVolumes" "controller.extraVolumes"
      "extraVolumeMounts" "controller.extraVolumeMounts"
      "initContainers" "controller.initContainers"
      "sidecars" "controller.sidecars"
      "lifecycle" "controller.lifecycle"
      "livenessProbe" "controller.livenessProbe"
      "readinessProbe" "controller.readinessProbe"
      "startupProbe" "controller.startupProbe"
      "autoscaling" "controller.autoscaling"
      "podDisruptionBudget" "controller.podDisruptionBudget"
      "monitoring" "controller.monitoring"
      "networkPolicy" "controller.networkPolicy"
      "webhook" "controller.webhook" -}}
{{- range $old, $new := $movedRootValues -}}
  {{- if hasKey $.Values $old -}}{{- fail (printf "%s moved to %s so every workload setting has an explicit component owner." $old $new) -}}{{- end -}}
{{- end -}}
{{- if hasKey $controller "crdName" -}}{{- fail "controller.crdName was renamed to controller.configName because it names a HAProxyTemplateConfig object, not a CRD." -}}{{- end -}}
{{- if hasKey $controller "debugPort" -}}{{- fail "controller.debugPort was removed; controller.ports.healthz is now the single source of truth for the /healthz and /debug listener." -}}{{- end -}}
{{- if hasKey $controller "statusPatches" -}}{{- fail "controller.statusPatches moved to controller.config.templatingSettings.extraContext.statusPatches because status writes are template-library behavior." -}}{{- end -}}
{{- if hasKey $controller "apiGateway" -}}{{- fail "controller.apiGateway moved to controller.config.templatingSettings.extraContext.apiGateway because request-schema validation is template-library behavior; body limits live under extraContext.requestBodyInspection.haproxyBuffer." -}}{{- end -}}

{{- if not (kindIs "string" $controller.configName) -}}
  {{- fail "controller.configName must be a valid non-empty Kubernetes DNS subdomain no longer than 253 characters." -}}
{{- end -}}
{{- if or (gt (len $controller.configName) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $controller.configName)) -}}
  {{- fail "controller.configName must be a valid non-empty Kubernetes DNS subdomain no longer than 253 characters." -}}
{{- end -}}

{{- $ports := $controller.ports | default dict -}}
{{- if not (kindIs "map" $ports) -}}{{- fail "controller.ports must be a map." -}}{{- end -}}
{{- range $field := keys $ports -}}{{- if not (has $field (list "healthz" "metrics" "webhook")) -}}{{- fail (printf "controller.ports contains unknown field %q. Valid fields: healthz, metrics, webhook." $field) -}}{{- end -}}{{- end -}}
{{- $seenPorts := dict -}}
{{- range $field := list "healthz" "metrics" "webhook" -}}
  {{- $raw := index $ports $field | toString -}}
  {{- if not (regexMatch "^[0-9]+$" $raw) -}}{{- fail (printf "controller.ports.%s must be an integer between %d and 65535." $field (ternary 0 1 (eq $field "metrics"))) -}}{{- end -}}
  {{- $port := int $raw -}}
  {{- $minimum := ternary 0 1 (eq $field "metrics") -}}
  {{- if or (lt $port $minimum) (gt $port 65535) -}}{{- fail (printf "controller.ports.%s must be between %d and 65535." $field $minimum) -}}{{- end -}}
  {{- if gt $port 0 -}}
    {{- $portKey := toString $port -}}
    {{- if hasKey $seenPorts $portKey -}}{{- fail (printf "controller.ports.%s duplicates controller.ports.%s (%d); controller listeners must use distinct ports." $field (index $seenPorts $portKey) $port) -}}{{- end -}}
    {{- $_ := set $seenPorts $portKey $field -}}
  {{- end -}}
{{- end -}}
{{- if and (eq (int $ports.metrics) 0) (or $controller.monitoring.serviceMonitor.enabled $controller.monitoring.podMonitor.enabled $controller.monitoring.prometheusRule.enabled) -}}
  {{- fail "controller.ports.metrics=0 cannot be combined with ServiceMonitor, PodMonitor, or PrometheusRule monitoring; enable the metrics listener or disable those resources." -}}
{{- end -}}
{{- if and (eq (int $ports.metrics) 0) $controller.networkPolicy.enabled $controller.networkPolicy.ingress.monitoring.enabled -}}
  {{- fail "controller.ports.metrics=0 cannot be combined with controller.networkPolicy.ingress.monitoring.enabled; the monitoring ingress rule would reference port 0, which Kubernetes rejects." -}}
{{- end -}}

{{- $config := $controller.config | default dict -}}
{{- if not (kindIs "map" $config) -}}{{- fail "controller.config must be a map." -}}{{- end -}}
{{- $runtimeController := $config.controller | default dict -}}
{{- if not (kindIs "map" $runtimeController) -}}{{- fail "controller.config.controller must be a map." -}}{{- end -}}
{{- if hasKey $runtimeController "healthzPort" -}}{{- fail "controller.config.controller.healthzPort was a no-op and has been removed; use controller.ports.healthz." -}}{{- end -}}
{{- if hasKey $runtimeController "metricsPort" -}}{{- fail "controller.config.controller.metricsPort was a no-op and has been removed; use controller.ports.metrics." -}}{{- end -}}

{{- $dataplane := $config.dataplane | default dict -}}
{{- if not (kindIs "map" $dataplane) -}}{{- fail "controller.config.dataplane must be a map." -}}{{- end -}}
{{- if hasKey $dataplane "port" -}}{{- fail "controller.config.dataplane.port was removed; haproxy.ports.dataplane is now the single source of truth for both the Dataplane API listener and controller connection." -}}{{- end -}}
{{- if hasKey $config "routing" -}}{{- fail "controller.config.routing moved to controller.config.templatingSettings.extraContext.routing because it controls template-library behavior; use extraContext.routing.regexMatchOrder." -}}{{- end -}}

{{- $extraContext := dig "templatingSettings" "extraContext" dict $config -}}
{{- if not (kindIs "map" $extraContext) -}}{{- fail "controller.config.templatingSettings.extraContext must be a map." -}}{{- end -}}
{{- range $legacy := list "debug" "statusPatchesDisabled" "password_hash_validation_regex" "password_hash_validation_error_message" "hstsEnabled" "hstsMaxAge" "hstsIncludeSubdomains" "hstsPreload" -}}
  {{- if hasKey $extraContext $legacy -}}{{- fail (printf "controller.config.templatingSettings.extraContext.%s uses a removed flat value; use the structured diagnostics, statusPatches, annotationCompatibility, or tls tree documented in the chart values reference." $legacy) -}}{{- end -}}
{{- end -}}
{{- range $reserved := list "cache" "rateLimit" "spoaHub" "controllerName" "gatewayClassResource" "haproxyVersion" "controllerNamespace" "annotationLibraries" -}}
  {{- if hasKey $extraContext $reserved -}}{{- fail (printf "controller.config.templatingSettings.extraContext.%s is chart-managed; configure its component-owned Helm value instead of overriding the generated runtime context." $reserved) -}}{{- end -}}
{{- end -}}
{{- $routing := dict -}}
{{- if hasKey $extraContext "routing" -}}{{- $routing = $extraContext.routing -}}{{- end -}}
{{- if not (kindIs "map" $routing) -}}{{- fail "controller.config.templatingSettings.extraContext.routing must be a map." -}}{{- end -}}
{{- range $field := keys $routing -}}{{- if ne $field "regexMatchOrder" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.routing contains unknown field %q. Valid field: regexMatchOrder." $field) -}}{{- end -}}{{- end -}}
{{- $regexMatchOrder := $routing.regexMatchOrder | default "default" -}}
{{- if or (not (kindIs "string" $regexMatchOrder)) (not (has $regexMatchOrder (list "default" "last"))) -}}{{- fail "controller.config.templatingSettings.extraContext.routing.regexMatchOrder must be one of: default, last." -}}{{- end -}}

{{- $diagnostics := $extraContext.diagnostics | default dict -}}
{{- if not (kindIs "map" $diagnostics) -}}{{- fail "controller.config.templatingSettings.extraContext.diagnostics must be a map." -}}{{- end -}}
{{- range $field := keys $diagnostics -}}{{- if ne $field "routingHeaders" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.diagnostics contains unknown field %q. Valid field: routingHeaders." $field) -}}{{- end -}}{{- end -}}
{{- $routingHeaders := $diagnostics.routingHeaders | default dict -}}
{{- if not (kindIs "map" $routingHeaders) -}}{{- fail "controller.config.templatingSettings.extraContext.diagnostics.routingHeaders must be a map." -}}{{- end -}}
{{- range $field := keys $routingHeaders -}}{{- if ne $field "enabled" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.diagnostics.routingHeaders contains unknown field %q. Valid field: enabled." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $routingHeaders.enabled) -}}{{- fail "controller.config.templatingSettings.extraContext.diagnostics.routingHeaders.enabled must be a boolean." -}}{{- end -}}

{{- /* Access log. The same checks exist Scriggo-side in base.yaml's
       util-log-format-http, because the HAProxyTemplateConfig CR is a
       first-class API that bypasses Helm entirely. */ -}}
{{- $accessLog := $extraContext.accessLog | default dict -}}
{{- if not (kindIs "map" $accessLog) -}}{{- fail "controller.config.templatingSettings.extraContext.accessLog must be a map." -}}{{- end -}}
{{- range $field := keys $accessLog -}}{{- if not (has $field (list "fields" "maxLineBytes" "targets")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog contains unknown field %q. Valid fields: fields, maxLineBytes, targets." $field) -}}{{- end -}}{{- end -}}
{{- /* Log targets. Structure and enums are checked here so `helm install` fails
       fast; base.yaml's util-access-log-targets repeats them for the CR path. */ -}}
{{- $logTargets := $accessLog.targets | default list -}}
{{- if not (kindIs "slice" $logTargets) -}}{{- fail "controller.config.templatingSettings.extraContext.accessLog.targets must be a list of log targets." -}}{{- end -}}
{{- /* An ABSENT key takes the chart default; an explicitly EMPTY list would fall
       back to stdout, the one destination this knob exists to move records off. */ -}}
{{- if and (hasKey $accessLog "targets") (eq (len $logTargets) 0) -}}{{- fail "controller.config.templatingSettings.extraContext.accessLog.targets is an empty list. Name at least one target — use [{address: stdout}] for the chart default; an empty list would silently fall back to stdout, which is what this setting exists to move the access log away from." -}}{{- end -}}
{{- $logFormats := list "raw" "rfc3164" "rfc5424" "local" "priority" "short" "timed" "iso" -}}
{{- $logFacilities := list "kern" "user" "mail" "daemon" "auth" "syslog" "lpr" "news" "uucp" "cron" "auth2" "ftp" "ntp" "audit" "alert" "cron2" "local0" "local1" "local2" "local3" "local4" "local5" "local6" "local7" -}}
{{- /* The level is a MAX severity filter and access records are emitted at info,
       so notice and above silently drop every record while haproxy -c returns 0. */ -}}
{{- $logLevels := list "info" "debug" -}}
{{- /* Ring names this chart will emit. A `log ring@<name>` with no such ring
       passes haproxy -c and then makes HAProxy refuse to start, so an address may
       only reference a ring declared here. */ -}}
{{- $declaredRings := dict -}}
{{- $seenLogLines := dict -}}
{{- range $i, $target := $logTargets -}}
  {{- if kindIs "map" $target -}}
    {{- $r := $target.ring | default dict -}}
    {{- if and (kindIs "map" $r) (gt (len $r) 0) -}}
      {{- $ringName := $r.name | default "accesslog" | toString -}}
      {{- if hasKey $declaredRings $ringName -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.name %q is declared by an earlier target. Two ring sections cannot share a name." $i $ringName) -}}{{- end -}}
      {{- $_ := set $declaredRings $ringName true -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- range $i, $target := $logTargets -}}
  {{- if not (kindIs "map" $target) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d] must be a map with an address (or a ring)." $i) -}}{{- end -}}
  {{- range $field := keys $target -}}{{- if not (has $field (list "address" "format" "facility" "level" "ring")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d] contains unknown field %q. Valid fields: address, format, facility, level, ring." $i $field) -}}{{- end -}}{{- end -}}
  {{- $addr := $target.address | default "" -}}
  {{- $ring := $target.ring | default dict -}}
  {{- if not (kindIs "map" $ring) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring must be a map." $i) -}}{{- end -}}
  {{- if and (ne $addr "") (gt (len $ring) 0) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d] sets both address and ring. A ring target logs to ring@<name>, so set one or the other." $i) -}}{{- end -}}
  {{- if and (eq $addr "") (eq (len $ring) 0) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d] needs an address (stdout, stderr, fd@<n>, <host>:<port>, /path/to.sock or ring@<name>) or a ring." $i) -}}{{- end -}}
  {{- if and (ne $addr "") (not (regexMatch "^(stdout|stderr|fd@[0-9]+|ring@[A-Za-z_][A-Za-z0-9_-]{0,63}|/[^[:space:]]+|\\[[0-9A-Fa-f:]+\\]:[0-9]{1,5}|[0-9A-Za-z._-]+:[0-9]{1,5})$" $addr)) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].address %q is not a valid HAProxy log target." $i $addr) -}}{{- end -}}
  {{- /* A 5-digit shape match is not enough: HAProxy hard-ALERTs above 65535, so
         the value would pass here and take down the controller's config load. */ -}}
  {{- /* Skip an absolute path: it is a UNIX socket, and a colon is a legal
         filename character, so trailing digits there are part of the name. */ -}}
  {{- $addrPort := "" -}}
  {{- if not (hasPrefix "/" $addr) -}}{{- $addrPort = regexFind ":[0-9]{1,5}$" $addr -}}{{- end -}}
  {{- if $addrPort -}}
    {{- $p := trimPrefix ":" $addrPort | int -}}
    {{- if or (lt $p 1) (gt $p 65535) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].address %q has port %d, outside 1-65535. A port above 65535 is rejected by HAProxy at config parse; port 0 is accepted there but no record can ever be delivered to it." $i $addr $p) -}}{{- end -}}
  {{- end -}}
  {{- if hasPrefix "ring@" $addr -}}
    {{- if not (hasKey $declaredRings (trimPrefix "ring@" $addr)) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].address %q points at a ring no target declares. HAProxy accepts this at config check and then refuses to start, so it is rejected here. Declare the ring on the target with a ring: block." $i $addr) -}}{{- end -}}
  {{- end -}}
  {{- if and (hasKey $target "format") (not (has $target.format $logFormats)) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].format %q is invalid. Valid: %s." $i (toString $target.format) (join ", " $logFormats)) -}}{{- end -}}
  {{- if and (hasKey $target "facility") (not (has $target.facility $logFacilities)) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].facility %q must be one of the 24 standard syslog facilities." $i (toString $target.facility)) -}}{{- end -}}
  {{- if and (hasKey $target "level") (not (has $target.level $logLevels)) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].level %q is invalid. Valid: %s. The level is a maximum-severity filter and access records are emitted at info, so a stricter level silently drops every one of them." $i (toString $target.level) (join ", " $logLevels)) -}}{{- end -}}
  {{- if gt (len $ring) 0 -}}
    {{- range $field := keys $ring -}}{{- if not (has $field (list "name" "address" "size" "logProto" "connectTimeout" "serverTimeout" "serverOptions")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring contains unknown field %q. Valid fields: name, address, size, logProto, connectTimeout, serverTimeout, serverOptions." $i $field) -}}{{- end -}}{{- end -}}
    {{- $ringName := $ring.name | default "accesslog" -}}
    {{- if not (regexMatch "^[A-Za-z_][A-Za-z0-9_-]{0,63}$" $ringName) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.name %q names an HAProxy section and must match ^[A-Za-z_][A-Za-z0-9_-]{0,63}$." $i $ringName) -}}{{- end -}}
    {{- $ringAddr := $ring.address | default "" -}}
    {{- if not (regexMatch "^(\\[[0-9A-Fa-f:]+\\]:[0-9]{1,5}|[0-9A-Za-z._-]+:[0-9]{1,5})$" $ringAddr) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.address %q must be <host>:<port> or [<ipv6>]:<port>. For a UNIX socket collector use a plain-path address target; HAProxy 3.4 rejects a UNIX ring server while 3.0 accepts it." $i $ringAddr) -}}{{- end -}}
    {{- $ringPort := regexFind ":[0-9]{1,5}$" $ringAddr -}}
    {{- if $ringPort -}}
      {{- $rp := trimPrefix ":" $ringPort | int -}}
      {{- if or (lt $rp 1) (gt $rp 65535) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.address %q has port %d, outside 1-65535. A port above 65535 is rejected by HAProxy at config parse; port 0 is accepted there but no record can ever be delivered to it." $i $ringAddr $rp) -}}{{- end -}}
    {{- end -}}
    {{- $ringSize := dig "size" 65536 $ring | toString -}}
    {{- if not (regexMatch "^[0-9]{4,9}$" $ringSize) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.size must be an integer between 4096 and 134217728." $i) -}}{{- end -}}
    {{- if or (lt (int $ringSize) 4096) (gt (int $ringSize) 134217728) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.size must be an integer between 4096 and 134217728." $i) -}}{{- end -}}
    {{- /* The ring carries `maxlen <maxLineBytes>` and HAProxy caps that at the
           buffer minus a ~197-byte header, emitting only a [WARNING] and then
           truncating every longer record into invalid JSON. Same on 3.0 and 3.4. */ -}}
    {{- $rawMaxLine := dig "maxLineBytes" 16384 $accessLog | toString -}}
    {{- if regexMatch "^[0-9]{1,5}$" $rawMaxLine -}}
      {{- if lt (int $ringSize) (add (int $rawMaxLine) 256) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.size is %s, too small for accessLog.maxLineBytes %s. HAProxy would cap the ring's maxlen to the buffer minus its header and truncate every longer record into invalid JSON, warning but not failing. Give the ring at least %d bytes." $i $ringSize $rawMaxLine (add (int $rawMaxLine) 256)) -}}{{- end -}}
    {{- end -}}
    {{- if and (hasKey $ring "logProto") (not (has $ring.logProto (list "legacy" "octet-count"))) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.logProto must be legacy or octet-count." $i) -}}{{- end -}}
    {{- range $tk := list "connectTimeout" "serverTimeout" -}}
      {{- if and (hasKey $ring $tk) (not (regexMatch "^[0-9]+(us|ms|s|m|h|d)?$" (toString (get $ring $tk)))) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.%s must be an HAProxy duration such as 5s or 500ms." $i $tk) -}}{{- end -}}
    {{- end -}}
    {{- $ringOpts := $ring.serverOptions | default "" -}}
    {{- if regexMatch "[[:cntrl:]#]" (toString $ringOpts) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d].ring.serverOptions is appended verbatim to the ring's server line and must not contain control characters or '#' (config-injection guard)." $i) -}}{{- end -}}
  {{- end -}}
  {{- /* Each target renders one `log` line and HAProxy sends every record down all
         of them, so two identical lines log each request twice — accepted by
         haproxy -c, silent at runtime. Targets differing in any field are real
         fan-out and stay allowed. */ -}}
  {{- $effAddr := $addr -}}
  {{- if gt (len $ring) 0 -}}{{- $effAddr = printf "ring@%s" ($ring.name | default "accesslog" | toString) -}}{{- end -}}
  {{- $defaultFormat := ternary "raw" "rfc5424" (or (eq $effAddr "stdout") (eq $effAddr "stderr")) -}}
  {{- $lineKey := printf "%s %s %s %s" $effAddr ($target.format | default $defaultFormat | toString) ($target.facility | default "local0" | toString) ($target.level | default "info" | toString) -}}
  {{- if hasKey $seenLogLines $lineKey -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.targets[%d] renders the same log line as an earlier target (%q), so every request would be logged twice. Drop the duplicate, or make the targets differ (a different format, facility, or level)." $i $lineKey) -}}{{- end -}}
  {{- $_ := set $seenLogLines $lineKey true -}}
{{- end -}}
{{- $maxLineBytes := dig "maxLineBytes" 16384 $accessLog | toString -}}
{{- /* Cap the digit count before `int`: Sprig's cast silently returns 0 on an
       int64 overflow, so a 30-digit value would reach the range check as 0 and be
       rejected only by accident. Five digits covers the 65535 upper bound. */ -}}
{{- if not (regexMatch "^[0-9]{1,5}$" $maxLineBytes) -}}{{- fail "controller.config.templatingSettings.extraContext.accessLog.maxLineBytes must be an integer between 1024 and 65535." -}}{{- end -}}
{{- if or (lt (int $maxLineBytes) 1024) (gt (int $maxLineBytes) 65535) -}}{{- fail "controller.config.templatingSettings.extraContext.accessLog.maxLineBytes must be an integer between 1024 and 65535." -}}{{- end -}}
{{- $logFields := $accessLog.fields | default dict -}}
{{- if not (kindIs "map" $logFields) -}}{{- fail "controller.config.templatingSettings.extraContext.accessLog.fields must be a map of <JSON field name> to <HAProxy sample expression>." -}}{{- end -}}
{{- range $fieldName, $fieldExpr := $logFields -}}
  {{- if not (regexMatch "^[A-Za-z_][A-Za-z0-9_]{0,39}$" $fieldName) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.fields contains invalid field name %q. A JSON field name must match ^[A-Za-z_][A-Za-z0-9_]{0,39}$." $fieldName) -}}{{- end -}}
  {{- if or (not (kindIs "string" $fieldExpr)) (eq $fieldExpr "") -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.fields[%q] must be a non-empty string holding one HAProxy sample expression, e.g. req.hdr(X-Tenant) or str(prod-eu)." $fieldName) -}}{{- end -}}
  {{- if regexMatch "[[:space:][:cntrl:]\"\\\\#]" $fieldExpr -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog.fields[%q] value %q must not contain whitespace, '#', '\"' or a backslash (config-injection guard). For a constant label use str(<value>) with no spaces." $fieldName $fieldExpr) -}}{{- end -}}
{{- end -}}

{{- $statusPatches := $extraContext.statusPatches | default dict -}}
{{- if not (kindIs "map" $statusPatches) -}}{{- fail "controller.config.templatingSettings.extraContext.statusPatches must be a map." -}}{{- end -}}
{{- range $field := keys $statusPatches -}}{{- if ne $field "enabled" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.statusPatches contains unknown field %q. Valid field: enabled." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $statusPatches.enabled) -}}{{- fail "controller.config.templatingSettings.extraContext.statusPatches.enabled must be a boolean." -}}{{- end -}}

{{- $compat := $extraContext.annotationCompatibility | default dict -}}
{{- if not (kindIs "map" $compat) -}}{{- fail "controller.config.templatingSettings.extraContext.annotationCompatibility must be a map." -}}{{- end -}}
{{- range $field := keys $compat -}}{{- if ne $field "basicAuth" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.annotationCompatibility contains unknown field %q. Valid field: basicAuth." $field) -}}{{- end -}}{{- end -}}
{{- $basicAuth := $compat.basicAuth | default dict -}}
{{- if not (kindIs "map" $basicAuth) -}}{{- fail "controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth must be a map." -}}{{- end -}}
{{- range $field := keys $basicAuth -}}{{- if ne $field "passwordHashValidation" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth contains unknown field %q. Valid field: passwordHashValidation." $field) -}}{{- end -}}{{- end -}}
{{- $hashValidation := $basicAuth.passwordHashValidation | default dict -}}
{{- if not (kindIs "map" $hashValidation) -}}{{- fail "controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation must be a map." -}}{{- end -}}
{{- range $field := keys $hashValidation -}}{{- if not (has $field (list "regex" "errorMessage")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation contains unknown field %q. Valid fields: regex, errorMessage." $field) -}}{{- end -}}{{- end -}}
{{- if or (not (kindIs "string" $hashValidation.regex)) (eq $hashValidation.regex "") -}}{{- fail "controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.regex must be a non-empty string." -}}{{- end -}}
{{- if or (not (kindIs "string" $hashValidation.errorMessage)) (eq $hashValidation.errorMessage "") -}}{{- fail "controller.config.templatingSettings.extraContext.annotationCompatibility.basicAuth.passwordHashValidation.errorMessage must be a non-empty string." -}}{{- end -}}

{{- $tls := $extraContext.tls | default dict -}}
{{- if not (kindIs "map" $tls) -}}{{- fail "controller.config.templatingSettings.extraContext.tls must be a map." -}}{{- end -}}
{{- range $field := keys $tls -}}
  {{- if eq $field "defaultCertificate" -}}{{- fail "controller.config.templatingSettings.extraContext.tls.defaultCertificate is chart-managed; use the top-level defaultSSLCertificate values." -}}{{- end -}}
  {{- if not (has $field (list "hsts" "sessionTickets")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.tls contains unknown field %q. Valid fields: hsts, sessionTickets." $field) -}}{{- end -}}
{{- end -}}
{{- $hsts := $tls.hsts | default dict -}}
{{- if not (kindIs "map" $hsts) -}}{{- fail "controller.config.templatingSettings.extraContext.tls.hsts must be a map." -}}{{- end -}}
{{- range $field := keys $hsts -}}{{- if not (has $field (list "enabled" "maxAge" "includeSubdomains" "preload")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.tls.hsts contains unknown field %q." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $hsts.enabled) -}}{{- fail "controller.config.templatingSettings.extraContext.tls.hsts.enabled must be a boolean." -}}{{- end -}}
{{- if or (not (kindIs "string" $hsts.maxAge)) (not (regexMatch "^[0-9]+$" $hsts.maxAge)) -}}{{- fail "controller.config.templatingSettings.extraContext.tls.hsts.maxAge must be a non-negative integer encoded as a string." -}}{{- end -}}
{{- range $field := list "includeSubdomains" "preload" -}}{{- if not (kindIs "bool" (index $hsts $field)) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.tls.hsts.%s must be a boolean." $field) -}}{{- end -}}{{- end -}}
{{- $sessionTickets := $tls.sessionTickets | default dict -}}
{{- if not (kindIs "map" $sessionTickets) -}}{{- fail "controller.config.templatingSettings.extraContext.tls.sessionTickets must be a map." -}}{{- end -}}
{{- range $field := keys $sessionTickets -}}{{- if ne $field "enabled" -}}{{- fail (printf "controller.config.templatingSettings.extraContext.tls.sessionTickets contains unknown field %q. Valid field: enabled." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $sessionTickets.enabled) -}}{{- fail "controller.config.templatingSettings.extraContext.tls.sessionTickets.enabled must be a boolean." -}}{{- end -}}

{{- $portEnvOwner := dict "DEBUG_PORT" "healthz" "METRICS_PORT" "metrics" "WEBHOOK_PORT" "webhook" -}}
{{- range $entry := $controller.extraEnv | default list -}}
  {{- if and (kindIs "map" $entry) (hasKey $portEnvOwner (toString $entry.name)) -}}
    {{- fail (printf "controller.extraEnv must not override %s; use controller.ports.%s so the process listener, container port, Service, probes, and NetworkPolicy stay aligned." $entry.name (index $portEnvOwner (toString $entry.name))) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{- define "haptic.haproxy.validateValues" -}}
{{- $haproxy := .Values.haproxy -}}
{{- if not (kindIs "map" $haproxy) -}}{{- fail "haproxy must be a map." -}}{{- end -}}
{{- $enterprise := $haproxy.enterprise | default dict -}}
{{- if not (kindIs "map" $enterprise) -}}{{- fail "haproxy.enterprise must be a map." -}}{{- end -}}
{{- if hasKey $enterprise "version" -}}{{- fail "haproxy.enterprise.version was removed; haproxyVersion now selects the image series and derived Enterprise binary path together." -}}{{- end -}}
{{- range $field := keys $enterprise -}}{{- if ne $field "enabled" -}}{{- fail (printf "haproxy.enterprise contains unknown field %q. Valid field: enabled." $field) -}}{{- end -}}{{- end -}}
{{- if not (kindIs "bool" $enterprise.enabled) -}}{{- fail "haproxy.enterprise.enabled must be a boolean." -}}{{- end -}}

{{- $ports := $haproxy.ports | default dict -}}
{{- if not (kindIs "map" $ports) -}}{{- fail "haproxy.ports must be a map." -}}{{- end -}}
{{- range $field := keys $ports -}}{{- if not (has $field (list "http" "https" "stats" "dataplane")) -}}{{- fail (printf "haproxy.ports contains unknown field %q. Valid fields: http, https, stats, dataplane." $field) -}}{{- end -}}{{- end -}}
{{- $seenPorts := dict -}}
{{- range $field := list "http" "https" "stats" "dataplane" -}}
  {{- $raw := index $ports $field | toString -}}
  {{- if not (regexMatch "^[0-9]+$" $raw) -}}{{- fail (printf "haproxy.ports.%s must be an integer between 1 and 65535." $field) -}}{{- end -}}
  {{- $port := int $raw -}}
  {{- if or (lt $port 1) (gt $port 65535) -}}{{- fail (printf "haproxy.ports.%s must be between 1 and 65535." $field) -}}{{- end -}}
  {{- $portKey := toString $port -}}
  {{- if hasKey $seenPorts $portKey -}}{{- fail (printf "haproxy.ports.%s duplicates haproxy.ports.%s (%d); HAProxy pod listeners must use distinct ports." $field (index $seenPorts $portKey) $port) -}}{{- end -}}
  {{- $_ := set $seenPorts $portKey $field -}}
{{- end -}}

{{- if and $enterprise.enabled (eq (trim (toString $haproxy.image.tag)) "") -}}
  {{- $pin := index .Values.haproxyEnterprisePatchVersions .Values.haproxyVersion | default "" -}}
  {{- if eq (trim (toString $pin)) "" -}}{{- fail (printf "haproxy.enterprise.enabled=true has no tested image pin for haproxyVersion %q; select a supported series or set haproxy.image.tag explicitly." .Values.haproxyVersion) -}}{{- end -}}
{{- end -}}
{{- end -}}
