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
{{- range $field := keys $accessLog -}}{{- if not (has $field (list "fields" "maxLineBytes")) -}}{{- fail (printf "controller.config.templatingSettings.extraContext.accessLog contains unknown field %q. Valid fields: fields, maxLineBytes." $field) -}}{{- end -}}{{- end -}}
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
