{{/*
Dataplane API credentials helpers: Secret name, username, password
(with upgrade-stable lookup fallback), and the rolling-update checksum.
*/}}

{{/*
Name of the Secret that holds the Dataplane API credentials. Referenced
by templates/secret.yaml (the resource itself), templates/deployment.yaml
and templates/haproxy-deployment.yaml (env vars on the controller and
HAProxy pods), templates/haproxytemplateconfig.yaml (the
credentialsSecretRef on the rendered HAProxyTemplateConfig), and
haptic.dataplane.password below (lookup for upgrade-stable passwords).
*/}}
{{- define "haptic.dataplane.credentialsSecretName" -}}
{{- printf "%s-credentials" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end -}}

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
{{- with .Values.credentials.dataplane.password -}}
{{- . -}}
{{- else -}}
{{- with dig "data" "dataplane_password" "" (lookup "v1" "Secret" .Release.Namespace (include "haptic.dataplane.credentialsSecretName" .)) -}}
{{- . | b64dec -}}
{{- else -}}
{{- /* Deterministic password for GitOps tools where lookup returns empty */ -}}
{{- printf "%s-%s-haptic-dataplane-api" .Release.Name .Release.Namespace | sha256sum | trunc 32 -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SHA256 checksum of the dataplane credentials, suitable for the
`checksum/secret` pod-template annotation. Both the controller and
HAProxy Deployments need to roll when the credentials change, so they
share this computation.
*/}}
{{- define "haptic.dataplane.credentialsChecksum" -}}
{{- printf "%s-%s" (include "haptic.dataplane.username" .) (include "haptic.dataplane.password" .) | sha256sum -}}
{{- end -}}
