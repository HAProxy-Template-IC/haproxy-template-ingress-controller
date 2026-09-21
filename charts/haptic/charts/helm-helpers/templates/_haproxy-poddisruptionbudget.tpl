{{- define "haptic.haproxyPodDisruptionBudget.validateValues" -}}
{{- $haproxy := .Values.haproxy -}}
{{- $pdb := $haproxy.podDisruptionBudget -}}
{{- if not (kindIs "map" $pdb) -}}{{- fail "haproxy.podDisruptionBudget must be a map." -}}{{- end -}}
{{- range $field := keys $pdb -}}
  {{- if not (has $field (list "enabled" "maxUnavailable")) -}}
    {{- fail (printf "haproxy.podDisruptionBudget contains unknown field %q. Valid fields: enabled, maxUnavailable." $field) -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $pdb.enabled) -}}{{- fail "haproxy.podDisruptionBudget.enabled must be a boolean." -}}{{- end -}}

{{- $maxUnavailable := $pdb.maxUnavailable -}}
{{- if ne $maxUnavailable nil -}}
{{- $maxUnavailableIsNumber := or
      (kindIs "int" $maxUnavailable) (kindIs "int8" $maxUnavailable)
      (kindIs "int16" $maxUnavailable) (kindIs "int32" $maxUnavailable)
      (kindIs "int64" $maxUnavailable) (kindIs "uint" $maxUnavailable)
      (kindIs "uint8" $maxUnavailable) (kindIs "uint16" $maxUnavailable)
      (kindIs "uint32" $maxUnavailable) (kindIs "uint64" $maxUnavailable)
      (kindIs "float32" $maxUnavailable) (kindIs "float64" $maxUnavailable) -}}
{{- if or (not $maxUnavailableIsNumber) (not (regexMatch "^[0-9]+$" (toString $maxUnavailable))) -}}
  {{- fail "haproxy.podDisruptionBudget.maxUnavailable must be a non-negative integer." -}}
{{- end -}}
{{- end -}}

{{- $replicaCount := $haproxy.replicaCount -}}
{{- $replicaCountIsNumber := or
      (kindIs "int" $replicaCount) (kindIs "int8" $replicaCount)
      (kindIs "int16" $replicaCount) (kindIs "int32" $replicaCount)
      (kindIs "int64" $replicaCount) (kindIs "uint" $replicaCount)
      (kindIs "uint8" $replicaCount) (kindIs "uint16" $replicaCount)
      (kindIs "uint32" $replicaCount) (kindIs "uint64" $replicaCount)
      (kindIs "float32" $replicaCount) (kindIs "float64" $replicaCount) -}}
{{- if or (not $replicaCountIsNumber) (not (regexMatch "^[0-9]+$" (toString $replicaCount))) -}}
  {{- fail "haproxy.replicaCount must be a non-negative integer." -}}
{{- end -}}

{{- $keda := $haproxy.keda -}}
{{- if not (kindIs "map" $keda) -}}{{- fail "haproxy.keda must be a map." -}}{{- end -}}
{{- if not (kindIs "bool" $keda.enabled) -}}{{- fail "haproxy.keda.enabled must be a boolean." -}}{{- end -}}
{{- $kedaMinReplicaCount := $keda.minReplicaCount -}}
{{- $kedaMinReplicaCountIsNumber := or
      (kindIs "int" $kedaMinReplicaCount) (kindIs "int8" $kedaMinReplicaCount)
      (kindIs "int16" $kedaMinReplicaCount) (kindIs "int32" $kedaMinReplicaCount)
      (kindIs "int64" $kedaMinReplicaCount) (kindIs "uint" $kedaMinReplicaCount)
      (kindIs "uint8" $kedaMinReplicaCount) (kindIs "uint16" $kedaMinReplicaCount)
      (kindIs "uint32" $kedaMinReplicaCount) (kindIs "uint64" $kedaMinReplicaCount)
      (kindIs "float32" $kedaMinReplicaCount) (kindIs "float64" $kedaMinReplicaCount) -}}
{{- if or (not $kedaMinReplicaCountIsNumber) (not (regexMatch "^[0-9]+$" (toString $kedaMinReplicaCount))) -}}
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
