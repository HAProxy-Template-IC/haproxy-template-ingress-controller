{{/*
Naming, labels, and serviceAccount helpers.

All Helm template names are global; this file is a purely organizational split
of the original _helpers.tpl. Consumers `include` these helpers by name as
before — file boundaries are invisible to callers.
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
Component labels
Generates app.kubernetes.io/component label for a given component name
Usage: {{ include "haptic.componentLabels" "loadbalancer" }}
*/}}
{{- define "haptic.componentLabels" -}}
app.kubernetes.io/component: {{ . }}
{{- end -}}
