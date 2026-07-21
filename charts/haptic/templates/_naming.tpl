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
Cluster-scoped fullname: haptic.fullname suffixed with the release namespace.

Cluster-scoped objects (ClusterRole, ClusterRoleBinding, the
ValidatingWebhookConfiguration, the CRD-upgrade hook's RBAC) share one
cluster-wide namespace, so naming them by release fullname alone collides when
the same release name is installed into two namespaces. Suffixing the namespace
disambiguates them. Namespaced objects (ServiceAccount, Role, Services) and
cluster singletons that are a user-facing API (IngressClass, the CRDs) must NOT
use this — their names are either non-colliding or externally referenced.
*/}}
{{- define "haptic.clusterScopedFullname" -}}
{{- printf "%s-%s" (include "haptic.fullname" .) .Release.Namespace | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create the controller Deployment name with a "-controller" suffix.
Used as metadata.name for the controller Deployment, as the
HPA's scaleTargetRef, and in NOTES.txt's kubectl port-forward example.
*/}}
{{- define "haptic.controllerFullname" -}}
{{- printf "%s-controller" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create the base name for HAProxy pod resources (Deployment, Service,
ConfigMap, ScaledObject, NetworkPolicy). Templates that need a longer
name like "<haproxy>-config" or "<haproxy>-dataplane" append their own
suffix to the result.
*/}}
{{- define "haptic.haproxyFullname" -}}
{{- printf "%s-haproxy" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
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
Common labels combined with operator-supplied .Values.commonLabels. Use this
helper wherever a resource's metadata.labels block emits the chart's labels
and then appends commonLabels — collapses the two-step pattern (an
`include "haptic.labels"` line plus a separate `with .Values.commonLabels`
block) into a single call.
*/}}
{{- define "haptic.labels.withCommon" -}}
{{ include "haptic.labels" . }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "haptic.selectorLabels" -}}
app.kubernetes.io/name: {{ include "haptic.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
HAProxy pod selector labels — match the labelset the HAProxy Deployment
applies to its Pods. Templates that emit Services targeting the shared
HAProxy front-door use this so Service.spec.selector resolves to the
right pods. The labelset is the chart-wide selectorLabels plus the
component=loadbalancer discriminator that distinguishes HAProxy pods
from controller / spoa-hub / dataplane-api pods.

This helper exists so templates which emit per-Gateway LoadBalancer
Services via renderResource() (SupportGatewayStaticAddresses) can
construct the same selector the chart's static haproxy-service.yaml
uses, keeping the two in lockstep automatically.
*/}}
{{- define "haptic.haproxy.selectorLabels" -}}
{{ include "haptic.selectorLabels" . }}
app.kubernetes.io/component: loadbalancer
{{- end }}

{{/*
Release-scoped name for the managed Varnish cache tier. Keep the suffix in one
helper so the Helm-owned NetworkPolicies and controller-emitted resources cannot
drift, and truncate after suffixing to stay within DNS label limits.
*/}}
{{- define "haptic.varnish.cacheName" -}}
{{- printf "%s-varnish-cache" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "haptic.serviceAccountName" -}}
{{- .Values.controller.serviceAccount.name | default (ternary (include "haptic.fullname" .) "default" .Values.controller.serviceAccount.create) -}}
{{- end }}

{{/*
Webhook Service name (the ClusterIP Service that fronts the controller's
ValidatingWebhook endpoint). Used by templates/webhook-service.yaml
(metadata.name), templates/validatingwebhookconfiguration.yaml
(clientConfig.service.name and the per-rule webhook name), templates/
webhook-certificate.yaml (cert-manager dnsNames), and templates/
deployment.yaml (WEBHOOK_SERVICE_NAME env var).
*/}}
{{- define "haptic.webhook.serviceName" -}}
{{- printf "%s-webhook" (include "haptic.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
Webhook TLS Secret / Certificate name. Resolves the operator override
.Values.controller.webhook.secretName, or falls back to "<fullname>-webhook-cert".
Used by every place that points at the webhook TLS material —
templates/webhook-certificate.yaml (Certificate metadata.name and
spec.secretName), templates/validatingwebhookconfiguration.yaml
(cert-manager.io/inject-ca-from), and templates/deployment.yaml
(the webhook-certs volume mounted at /etc/webhook/certs, which the
controller reads via WEBHOOK_CERT_DIR).
Centralising this here keeps those references aligned when an
operator sets controller.webhook.secretName.
*/}}
{{- define "haptic.webhook.secretName" -}}
{{- .Values.controller.webhook.secretName | default (printf "%s-webhook-cert" (include "haptic.fullname" .)) -}}
{{- end -}}

{{/*
Kubernetes admissionregistration.k8s.io/v1 restricts timeoutSeconds to 1..30.
HAPTIC requires at least two seconds so the controller can keep its internal
deadline one second shorter and return a structured response before the API
server's outer deadline. Keep these helpers as the single validation point used
by both the ValidatingWebhookConfiguration and controller Deployment.
*/}}
{{- define "haptic.webhook.resourceTimeoutSeconds" -}}
{{- $raw := toString .Values.controller.webhook.timeoutSeconds -}}
{{- $timeout := int $raw -}}
{{- if or (not (regexMatch "^[0-9]+$" $raw)) (lt $timeout 2) (gt $timeout 30) -}}
{{- fail "controller.webhook.timeoutSeconds must be an integer between 2 and 30." -}}
{{- end -}}
{{- $timeout -}}
{{- end -}}

{{- define "haptic.webhook.configTimeoutSeconds" -}}
{{- $raw := toString .Values.controller.webhook.haproxyTemplateConfig.timeoutSeconds -}}
{{- $timeout := int $raw -}}
{{- if or (not (regexMatch "^[0-9]+$" $raw)) (lt $timeout 2) (gt $timeout 30) -}}
{{- fail "controller.webhook.haproxyTemplateConfig.timeoutSeconds must be an integer between 2 and 30." -}}
{{- end -}}
{{- $timeout -}}
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
Return the candidate apiVersion list of a watchedResources entry as a JSON
array string (parse with fromJsonArray). Entries declare either a singular
`apiVersion` or an ordered `apiVersions` candidate list (the controller
resolves the served one at runtime); helm-side consumers (RBAC rules,
webhook rules) must cover every candidate.
*/}}
{{- define "haptic.candidateVersionsOf" -}}
{{- if .apiVersions -}}{{ .apiVersions | toJson }}{{- else -}}{{ list .apiVersion | toJson }}{{- end -}}
{{- end -}}

{{/*
Extract the API version from a Kubernetes apiVersion string.
Returns the part after the slash, or the whole input when there is no
group (e.g. "v1" -> "v1", "networking.k8s.io/v1" -> "v1").
*/}}
{{- define "haptic.apiVersionOf" -}}
{{- regexFind "[^/]+$" . -}}
{{- end -}}
