{{/* Credential source shared by the controller and agent. */}}
{{- define "haptic.dataplane.credentialsSecretName" -}}
{{- $external := get .Values.credentials "existingSecret" -}}
{{- if not (kindIs "string" $external) -}}{{- fail "credentials.existingSecret must be a Secret name." -}}{{- end -}}
{{- if $external -}}
  {{- if or (gt (len $external) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $external)) -}}
    {{- fail "credentials.existingSecret must be a valid Kubernetes Secret name." -}}
  {{- end -}}
  {{- if .Values.credentials.dataplane.password -}}
    {{- fail "credentials.existingSecret and credentials.dataplane.password cannot both be set." -}}
  {{- end -}}
  {{- $external -}}
{{- else -}}
  {{- printf "%s-credentials" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "haptic.dataplane.username" -}}
{{- .Values.credentials.dataplane.username | default "admin" -}}
{{- end -}}

{{/* Memoize random generation so the Secret and both deployment checksums agree. */}}
{{- define "haptic.dataplane.password" -}}
{{- if not (hasKey .Values "_dataplanePassword") -}}
  {{- $pw := .Values.credentials.dataplane.password | default "" -}}
  {{- if not $pw -}}
    {{- $existing := dig "data" "dataplane_password" "" (lookup "v1" "Secret" .Release.Namespace (include "haptic.dataplane.credentialsSecretName" .)) -}}
    {{- if $existing -}}
      {{- $pw = $existing | b64dec -}}
    {{- else -}}
      {{- $pw = randAlphaNum 32 -}}
    {{- end -}}
  {{- end -}}
  {{- $_ := set .Values "_dataplanePassword" $pw -}}
{{- end -}}
{{- get .Values "_dataplanePassword" -}}
{{- end -}}

{{- define "haptic.dataplane.credentialsChecksum" -}}
{{- if .Values.credentials.existingSecret -}}
  {{- include "haptic.dataplane.credentialsSecretName" . | sha256sum -}}
{{- else -}}
  {{- printf "%s-%s" (include "haptic.dataplane.username" .) (include "haptic.dataplane.password" .) | sha256sum -}}
{{- end -}}
{{- end -}}
