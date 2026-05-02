{{/*
Dataplane API credentials.
*/}}

{{/*
Dataplane API username
Uses provided value or defaults to "admin"
*/}}
{{- define "haptic.dataplane.username" -}}
{{- .Values.credentials.dataplane.username | default "admin" -}}
{{- end -}}

{{/*
Dataplane API password
Priority: 1) User-provided value, 2) Existing secret value, 3) Deterministic password from release identity

Uses lookup to preserve password across helm upgrades. When lookup is unavailable
(e.g., ArgoCD dry-run rendering), falls back to a deterministic hash based on
release name and namespace to prevent constant drift detection.
*/}}
{{- define "haptic.dataplane.password" -}}
{{- if .Values.credentials.dataplane.password -}}
{{- .Values.credentials.dataplane.password -}}
{{- else -}}
{{- $secretName := printf "%s-credentials" (include "haptic.fullname" .) -}}
{{- $existingSecret := lookup "v1" "Secret" .Release.Namespace $secretName -}}
{{- if and $existingSecret $existingSecret.data (index $existingSecret.data "dataplane_password") -}}
{{- index $existingSecret.data "dataplane_password" | b64dec -}}
{{- else -}}
{{- /* Deterministic password for GitOps tools where lookup returns empty */ -}}
{{- printf "%s-%s-haptic-dataplane-api" .Release.Name .Release.Namespace | sha256sum | trunc 32 -}}
{{- end -}}
{{- end -}}
{{- end -}}
