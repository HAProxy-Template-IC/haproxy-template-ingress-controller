{{- define "haptic.varnish.image" -}}
{{- .Values.cache.varnish.image | default (printf "registry.gitlab.com/haproxy-haptic/haptic/varnish:%s" .Chart.AppVersion) -}}
{{- end -}}

{{/*
Validate the complete chart-managed cache value surface, including while the
feature is disabled. This prevents staged configuration from hiding typos or
invalid availability/autoscaling combinations until a later enable.
*/}}
{{- define "haptic.cache.validateValues" -}}
{{- $cache := .Values.cache -}}

{{- $haproxy := $cache.haproxy | default dict -}}
{{- if not (regexMatch "^[0-9]+$" (toString $haproxy.hashBalanceFactor)) -}}{{- fail "cache.haproxy.hashBalanceFactor must be 0 (disabled) or an integer greater than 100." -}}{{- end -}}
{{- $hashBalanceFactor := int $haproxy.hashBalanceFactor -}}
{{- if and (ne $hashBalanceFactor 0) (le $hashBalanceFactor 100) -}}{{- fail "cache.haproxy.hashBalanceFactor must be 0 (disabled) or an integer greater than 100." -}}{{- end -}}
{{- if or (not (regexMatch "^[0-9]+$" (toString $haproxy.responseTimeoutMs))) (lt (int $haproxy.responseTimeoutMs) 1) -}}{{- fail "cache.haproxy.responseTimeoutMs must be a positive integer in milliseconds." -}}{{- end -}}

{{- $varnish := $cache.varnish | default dict -}}
{{- if hasKey $varnish "hashBalanceFactor" -}}{{- fail "cache.varnish.hashBalanceFactor has moved to cache.haproxy.hashBalanceFactor because HAProxy, not Varnish, consumes it." -}}{{- end -}}
{{- if hasKey $varnish "loopbackPort" -}}
  {{- if or (not (regexMatch "^[0-9]+$" (toString $varnish.loopbackPort))) (lt (int $varnish.loopbackPort) 1) (gt (int $varnish.loopbackPort) 65535) -}}
    {{- fail "cache.varnish.loopbackPort must be a port between 1 and 65535." -}}
  {{- end -}}
{{- end -}}
{{- if and (hasKey $varnish "originServiceName") (or (not (kindIs "string" $varnish.originServiceName)) (not (regexMatch "^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$" $varnish.originServiceName))) -}}
  {{- fail "cache.varnish.originServiceName must be a valid Kubernetes Service name." -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+$" (toString $varnish.replicas)) -}}{{- fail "cache.varnish.replicas must be a positive integer." -}}{{- end -}}
{{- $replicas := int $varnish.replicas -}}
{{- if lt $replicas 1 -}}{{- fail "cache.varnish.replicas must be a positive integer." -}}{{- end -}}
{{- if or (not (kindIs "string" $varnish.image)) (and (ne $varnish.image "") (eq (trim $varnish.image) "")) -}}{{- fail "cache.varnish.image must be an image reference string or empty for the bundled image." -}}{{- end -}}
{{- if or (not (kindIs "string" $varnish.malloc)) (not (regexMatch "^[1-9][0-9]*[kKmMgGtT]?$" $varnish.malloc)) -}}
  {{- fail "cache.varnish.malloc must be a positive Varnish malloc size in bytes or with a K, M, G, or T suffix, such as 256m." -}}
{{- end -}}
{{- $autoscaling := $varnish.autoscaling | default dict -}}
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
{{- end -}}
