{{/*
Naming, labels, and serviceAccount helpers. Split across _*.tpl files for
readability — Helm template names are global, so file boundaries are
invisible to callers.
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "haptic.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "haptic.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create controller deployment name with -controller suffix.
Only used for the controller Deployment resource.
*/}}
{{- define "haptic.controllerFullname" -}}
{{- printf "%s-controller" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "haptic.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "haptic.labels" -}}
helm.sh/chart: {{ include "haptic.chart" . }}
{{ include "haptic.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "haptic.selectorLabels" -}}
app.kubernetes.io/name: {{ include "haptic.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "haptic.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "haptic.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Webhook TLS Secret / Certificate name. Resolves the operator override
.Values.webhook.secretName, or falls back to "<fullname>-webhook-cert".
Used by every place that points at the webhook TLS material —
templates/webhook-certificate.yaml (Certificate metadata.name and
spec.secretName), templates/validatingwebhookconfiguration.yaml
(cert-manager.io/inject-ca-from), and templates/deployment.yaml
(WEBHOOK_CERT_SECRET_NAME env var and the webhook-certs volume).
Centralising this here keeps those references aligned when an
operator sets webhook.secretName.
*/}}
{{- define "haptic.webhook.secretName" -}}
{{- .Values.webhook.secretName | default (printf "%s-webhook-cert" (include "haptic.fullname" .)) -}}
{{- end -}}

{{/*
Extract the API group from a Kubernetes apiVersion string and render it as
a YAML scalar suitable for an `apiGroups:` list item. Core resources
("v1") render as the literal "" (empty quoted string); grouped resources
render as the bare group name (e.g. networking.k8s.io for
"networking.k8s.io/v1"). The output is the YAML token, not a raw Go
string, so callers do `- {{ include "haptic.apiGroupOf" ... }}` without
piping through `quote`.
*/}}
{{- define "haptic.apiGroupOf" -}}
{{- if contains "/" . -}}{{ regexFind "^[^/]+" . }}{{- else -}}""{{- end -}}
{{- end -}}

{{/*
Extract the API version from a Kubernetes apiVersion string.
Returns the part after the slash, or the whole input when there is no
group (e.g. "v1" -> "v1", "networking.k8s.io/v1" -> "v1").
*/}}
{{- define "haptic.apiVersionOf" -}}
{{- regexFind "[^/]+$" . -}}
{{- end -}}

