{{/*
Render the universally-shared pod-spec scheduling/runtime fields from a
podSpec subtree (e.g. .Values.controller.podSpec or .Values.haproxy.podSpec).

Args (list): [podSpec dict, root context]

Renders only fields whose semantics are identical across chart workloads:
imagePullSecrets, priorityClassName, runtimeClassName, terminationGracePeriodSeconds,
dnsPolicy, dnsConfig, hostAliases, topologySpreadConstraints, nodeSelector,
affinity, tolerations.

Workload-specific concerns are NOT rendered here; the caller still emits:
- securityContext (each workload computes runAsUser/runAsGroup/fsGroup
  differently — controller uses values verbatim, HAProxy injects haptic.haproxy.uid)
- containers / initContainers / volumes / sidecars (workload-specific)
- shareProcessNamespace (haproxy only)
- pod template metadata (annotations / labels — workload-specific merging
  with checksums and component labels)
- serviceAccountName (workload-specific helper call)

Output is at zero base indent; callers `nindent` it to fit their YAML position
(typically `nindent 6` because pod.spec sits at 6-space indent).
*/}}
{{- define "haptic.podSpec" -}}
{{- $ps := index . 0 -}}
{{- /* root context is provided as index . 1 for future use (templated values, lookups) */ -}}
{{- with $ps.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $ps.priorityClassName }}
priorityClassName: {{ . }}
{{- end }}
{{- with $ps.runtimeClassName }}
runtimeClassName: {{ . }}
{{- end }}
{{- if hasKey $ps "terminationGracePeriodSeconds" }}
terminationGracePeriodSeconds: {{ $ps.terminationGracePeriodSeconds }}
{{- end }}
{{- with $ps.dnsPolicy }}
dnsPolicy: {{ . }}
{{- end }}
{{- with $ps.dnsConfig }}
dnsConfig:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $ps.hostAliases }}
hostAliases:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $ps.topologySpreadConstraints }}
topologySpreadConstraints:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $ps.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $ps.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $ps.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}
