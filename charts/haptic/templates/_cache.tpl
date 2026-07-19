{{/*
Validate the complete chart-managed cache value surface, including while the
feature is disabled. This prevents staged configuration from hiding typos or
invalid availability/autoscaling combinations until a later enable.
*/}}
{{- define "haptic.cache.validateValues" -}}
{{- $cache := .Values.cache -}}
{{- if not (kindIs "map" $cache) -}}{{- fail "cache must be a map." -}}{{- end -}}
{{- range $field := keys $cache -}}
  {{- if not (has $field (list "haproxy" "varnish")) -}}{{- fail (printf "cache contains unknown field %q. Valid fields: haproxy, varnish." $field) -}}{{- end -}}
{{- end -}}

{{- $haproxy := $cache.haproxy | default dict -}}
{{- if not (kindIs "map" $haproxy) -}}{{- fail "cache.haproxy must be a map." -}}{{- end -}}
{{- range $field := keys $haproxy -}}{{- if ne $field "hashBalanceFactor" -}}{{- fail (printf "cache.haproxy contains unknown field %q. Valid field: hashBalanceFactor." $field) -}}{{- end -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $haproxy.hashBalanceFactor)) -}}{{- fail "cache.haproxy.hashBalanceFactor must be 0 (disabled) or an integer greater than 100." -}}{{- end -}}
{{- $hashBalanceFactor := int $haproxy.hashBalanceFactor -}}
{{- if and (ne $hashBalanceFactor 0) (le $hashBalanceFactor 100) -}}{{- fail "cache.haproxy.hashBalanceFactor must be 0 (disabled) or an integer greater than 100." -}}{{- end -}}

{{- $varnish := $cache.varnish | default dict -}}
{{- if not (kindIs "map" $varnish) -}}{{- fail "cache.varnish must be a map." -}}{{- end -}}
{{- range $field := keys $varnish -}}
  {{- if eq $field "hashBalanceFactor" -}}{{- fail "cache.varnish.hashBalanceFactor has moved to cache.haproxy.hashBalanceFactor because HAProxy, not Varnish, consumes it." -}}{{- end -}}
  {{- if not (has $field (list "enabled" "loopbackPort" "originServiceName" "workload" "replicas" "image" "imagePullPolicy" "malloc" "resources" "podDisruptionBudget" "networkPolicy" "autoscaling")) -}}
    {{- fail (printf "cache.varnish contains unknown field %q." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $varnish.enabled) -}}{{- fail "cache.varnish.enabled must be a boolean." -}}{{- end -}}
{{- if hasKey $varnish "loopbackPort" -}}
  {{- if or (not (regexMatch "^[0-9]+$" (toString $varnish.loopbackPort))) (lt (int $varnish.loopbackPort) 1) (gt (int $varnish.loopbackPort) 65535) -}}
    {{- fail "cache.varnish.loopbackPort must be a port between 1 and 65535." -}}
  {{- end -}}
{{- end -}}
{{- if and (hasKey $varnish "originServiceName") (or (not (kindIs "string" $varnish.originServiceName)) (not (regexMatch "^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$" $varnish.originServiceName))) -}}
  {{- fail "cache.varnish.originServiceName must be a valid Kubernetes Service name." -}}
{{- end -}}
{{- if or (not (kindIs "string" $varnish.workload)) (not (has $varnish.workload (list "statefulset" "deployment"))) -}}
  {{- fail "cache.varnish.workload must be one of: statefulset, deployment." -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $varnish.replicas)) -}}{{- fail "cache.varnish.replicas must be a positive integer." -}}{{- end -}}
{{- $replicas := int $varnish.replicas -}}
{{- if lt $replicas 1 -}}{{- fail "cache.varnish.replicas must be a positive integer." -}}{{- end -}}
{{- if or (not (kindIs "string" $varnish.image)) (eq (trim $varnish.image) "") -}}{{- fail "cache.varnish.image must be a non-empty image reference string." -}}{{- end -}}
{{- if or (not (kindIs "string" $varnish.imagePullPolicy)) (not (has $varnish.imagePullPolicy (list "Always" "IfNotPresent" "Never"))) -}}{{- fail "cache.varnish.imagePullPolicy must be one of: Always, IfNotPresent, Never." -}}{{- end -}}
{{- if or (not (kindIs "string" $varnish.malloc)) (not (regexMatch "^[1-9][0-9]*[kKmMgGtT]?$" $varnish.malloc)) -}}
  {{- fail "cache.varnish.malloc must be a positive Varnish malloc size in bytes or with a K, M, G, or T suffix, such as 256m." -}}
{{- end -}}
{{- $autoscaling := $varnish.autoscaling | default dict -}}
{{- if not (kindIs "map" $autoscaling) -}}{{- fail "cache.varnish.autoscaling must be a map." -}}{{- end -}}
{{- range $field := keys $autoscaling -}}
  {{- if not (has $field (list "enabled" "minReplicas" "maxReplicas" "targetCPUUtilizationPercentage" "scaleDownStabilizationSeconds")) -}}
    {{- fail (printf "cache.varnish.autoscaling contains unknown field %q." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $autoscaling.enabled) -}}{{- fail "cache.varnish.autoscaling.enabled must be a boolean." -}}{{- end -}}
{{- range $field := list "minReplicas" "maxReplicas" "targetCPUUtilizationPercentage" "scaleDownStabilizationSeconds" -}}
  {{- if not (regexMatch "^[0-9]+$" (toString (index $autoscaling $field))) -}}{{- fail (printf "cache.varnish.autoscaling.%s must be a non-negative integer." $field) -}}{{- end -}}
{{- end -}}
{{- $minReplicas := int $autoscaling.minReplicas -}}
{{- $maxReplicas := int $autoscaling.maxReplicas -}}
{{- $targetCPU := int $autoscaling.targetCPUUtilizationPercentage -}}
{{- $scaleDownWindow := int $autoscaling.scaleDownStabilizationSeconds -}}
{{- if lt $minReplicas 1 -}}{{- fail "cache.varnish.autoscaling.minReplicas must be at least 1." -}}{{- end -}}
{{- if lt $maxReplicas $minReplicas -}}{{- fail "cache.varnish.autoscaling.maxReplicas must be greater than or equal to minReplicas." -}}{{- end -}}
{{- if lt $targetCPU 1 -}}{{- fail "cache.varnish.autoscaling.targetCPUUtilizationPercentage must be positive." -}}{{- end -}}
{{- if gt $scaleDownWindow 3600 -}}{{- fail "cache.varnish.autoscaling.scaleDownStabilizationSeconds must be between 0 and 3600." -}}{{- end -}}
{{- if and $autoscaling.enabled (eq (dig "requests" "cpu" "" ($varnish.resources | default dict) | toString | trim) "") -}}
  {{- fail "cache.varnish.autoscaling.enabled=true requires cache.varnish.resources.requests.cpu because CPU utilization is calculated relative to that request." -}}
{{- end -}}

{{- $pdb := $varnish.podDisruptionBudget | default dict -}}
{{- if not (kindIs "map" $pdb) -}}{{- fail "cache.varnish.podDisruptionBudget must be a map." -}}{{- end -}}
{{- range $field := keys $pdb -}}{{- if not (has $field (list "enabled" "maxUnavailable")) -}}{{- fail (printf "cache.varnish.podDisruptionBudget contains unknown field %q. Valid fields: enabled, maxUnavailable." $field) -}}{{- end -}}{{- end -}}
{{- $pdbEnabled := true -}}
{{- if hasKey $pdb "enabled" -}}{{- if not (kindIs "bool" $pdb.enabled) -}}{{- fail "cache.varnish.podDisruptionBudget.enabled must be a boolean." -}}{{- end -}}{{- $pdbEnabled = $pdb.enabled -}}{{- end -}}
{{- $maxUnavailableRaw := dig "maxUnavailable" 1 $pdb | toString -}}
{{- if not (regexMatch "^[0-9]+$" $maxUnavailableRaw) -}}{{- fail "cache.varnish.podDisruptionBudget.maxUnavailable must be a non-negative integer." -}}{{- end -}}
{{- $maxUnavailable := int $maxUnavailableRaw -}}
{{- $minimumFleet := $replicas -}}
{{- if $autoscaling.enabled -}}{{- $minimumFleet = $minReplicas -}}{{- end -}}
{{- if and $pdbEnabled (ge $maxUnavailable $minimumFleet) -}}
  {{- fail "cache.varnish.podDisruptionBudget.maxUnavailable must be smaller than the minimum Varnish replica count, so voluntary disruptions preserve at least one cache shard." -}}
{{- end -}}

{{- $networkPolicy := $varnish.networkPolicy | default dict -}}
{{- if not (kindIs "map" $networkPolicy) -}}{{- fail "cache.varnish.networkPolicy must be a map." -}}{{- end -}}
{{- range $field := keys $networkPolicy -}}{{- if ne $field "enabled" -}}{{- fail (printf "cache.varnish.networkPolicy contains unknown field %q. Valid field: enabled." $field) -}}{{- end -}}{{- end -}}
{{- if and (hasKey $networkPolicy "enabled") (not (kindIs "bool" $networkPolicy.enabled)) -}}{{- fail "cache.varnish.networkPolicy.enabled must be a boolean." -}}{{- end -}}
{{- end -}}
