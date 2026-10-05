{{- define "haptic.haproxyPodDisruptionBudget.validateValues" -}}
{{- $haproxy := .Values.haproxy -}}
{{- $pdb := $haproxy.podDisruptionBudget -}}
{{- $maxUnavailable := $pdb.maxUnavailable -}}
{{- if and (ne $maxUnavailable nil) (not (regexMatch "^[0-9]+$" (toString $maxUnavailable))) -}}
  {{- fail "haproxy.podDisruptionBudget.maxUnavailable must be a non-negative integer." -}}
{{- end -}}
{{- $replicaCount := $haproxy.replicaCount -}}
{{- if not (regexMatch "^[0-9]+$" (toString $replicaCount)) -}}
  {{- fail "haproxy.replicaCount must be a non-negative integer." -}}
{{- end -}}

{{- $keda := $haproxy.keda -}}
{{- $kedaMinReplicaCount := $keda.minReplicaCount -}}
{{- if not (regexMatch "^[0-9]+$" (toString $kedaMinReplicaCount)) -}}
  {{- fail "haproxy.keda.minReplicaCount must be a non-negative integer." -}}
{{- end -}}

{{- $minimumFleet := int $replicaCount -}}
{{- if $keda.enabled -}}{{- $minimumFleet = int $kedaMinReplicaCount -}}{{- end -}}
{{- $effectiveMaxUnavailable := 0 -}}
{{- if ne $maxUnavailable nil -}}
  {{- $effectiveMaxUnavailable = int $maxUnavailable -}}
{{- else if gt $minimumFleet 1 -}}
  {{- $effectiveMaxUnavailable = 1 -}}
{{- end -}}
{{- if and $haproxy.enabled $pdb.enabled (ge $effectiveMaxUnavailable $minimumFleet) -}}
  {{- fail "haproxy.podDisruptionBudget.maxUnavailable must be smaller than the minimum HAProxy replica count, so voluntary disruptions preserve at least one load balancer." -}}
{{- end -}}
{{- end -}}

{{- define "haptic.haproxyPodDisruptionBudget.maxUnavailable" -}}
{{- $configured := .Values.haproxy.podDisruptionBudget.maxUnavailable -}}
{{- if ne $configured nil -}}
{{- int $configured -}}
{{- else -}}
  {{- $minimumFleet := int .Values.haproxy.replicaCount -}}
  {{- if .Values.haproxy.keda.enabled -}}{{- $minimumFleet = int .Values.haproxy.keda.minReplicaCount -}}{{- end -}}
  {{- if gt $minimumFleet 1 -}}1{{- else -}}0{{- end -}}
{{- end -}}
{{- end -}}
