{{- define "haptic.agentTLS.renewalJobSpec" -}}
{{- $root := .root -}}
{{- $controller := $root.Values.controller -}}
backoffLimit: 2
activeDeadlineSeconds: 300
ttlSecondsAfterFinished: 86400
template:
  metadata:
    labels:
      {{- include "haptic.selectorLabels" $root | nindent 6 }}
      app.kubernetes.io/component: agent-certificate-renewal
  spec:
    serviceAccountName: {{ .serviceAccount }}
    restartPolicy: Never
    {{- include "haptic.podSpec" $controller.podSpec | nindent 4 }}
    {{- with $controller.podSpec.podSecurityContext }}
    securityContext:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    containers:
      - name: renew
        {{- with $controller.securityContext }}
        securityContext:
          {{- toYaml . | nindent 10 }}
        {{- end }}
        image: {{ include "haptic.controller.image" $root | quote }}
        imagePullPolicy: {{ $controller.image.pullPolicy }}
        command: ["/usr/local/bin/haptic"]
        args:
          - certificates
          - renew
          - --namespace={{ $root.Release.Namespace }}
          - --issuer-secret={{ include "haptic.agentTLS.issuerSecretName" $root }}
          - --server-secret={{ include "haptic.agentTLS.serverSecretName" $root }}
          - --client-secret={{ include "haptic.agentTLS.clientSecretName" $root }}
          - --server-name={{ include "haptic.agentTLS.serverName" $root }}
          - --client-name={{ include "haptic.agentTLS.clientName" $root }}
          - --validity-days={{ $root.Values.haproxy.agent.tls.certValidityDays }}
        resources:
          {{- toYaml $root.Values.haproxy.agent.tls.renewal.resources | nindent 10 }}
{{- end -}}

{{- define "haptic.agentTLS.renewalRBAC" -}}
{{- $root := .root -}}
{{- range $kind := list "ServiceAccount" "Role" "RoleBinding" }}
---
apiVersion: {{ if eq $kind "ServiceAccount" }}v1{{ else }}rbac.authorization.k8s.io/v1{{ end }}
kind: {{ $kind }}
metadata:
  name: {{ $.name }}
  namespace: {{ $root.Release.Namespace }}
  labels:
    {{- include "haptic.labels.withCommon" $root | nindent 4 }}
  {{- if $.hook }}
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "6"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
  {{- end }}
{{- if eq $kind "ServiceAccount" }}
automountServiceAccountToken: true
{{- else if eq $kind "Role" }}
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["create"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames:
      - {{ include "haptic.agentTLS.issuerSecretName" $root }}
      - {{ include "haptic.agentTLS.serverSecretName" $root }}
      - {{ include "haptic.agentTLS.clientSecretName" $root }}
    verbs: ["get", "update"]
{{- else }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ $.name }}
subjects:
  - kind: ServiceAccount
    name: {{ $.name }}
    namespace: {{ $root.Release.Namespace }}
{{- end }}
{{- end }}
{{- end -}}
