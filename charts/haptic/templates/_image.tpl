{{/*
Image references, binary paths, and the principal UID/GID for HAProxy and
the dataplane API container.

`haptic.haproxy.uid` returns the single UID/GID used for runAsUser, runAsGroup,
fsGroup, and the dataplane container (which must share the volume group with
HAProxy). The four previously-separate helpers (haptic.haproxy.runAsUser,
runAsGroup, fsGroup, dataplaneRunAsUser) all returned the same value and were
collapsed in #4 — see ADR/CHANGELOG entries.
*/}}

{{/*
Controller image
Combines base tag (defaults to Chart.AppVersion) with HAProxy version suffix
Example: registry.gitlab.com/haproxy-haptic/haptic:0.1.0-alpha.12-haproxy3.2
*/}}
{{- define "haptic.controller.image" -}}
{{- $baseTag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s-haproxy%s" .Values.image.repository $baseTag .Values.haproxyVersion -}}
{{- end -}}

{{/*
HAProxy image
Uses haproxy.image.tag if set, otherwise looks up the patch/revision version from
haproxyEnterprisePatchVersions (when enterprise.enabled) or haproxyPatchVersions,
falling back to haproxyVersion itself.
Community example:  haproxytech/haproxy-debian:3.2.13
Enterprise example: hapee-registry.haproxy.com/haproxy-enterprise:3.2r1
*/}}
{{- define "haptic.haproxy.image" -}}
{{- $defaultTag := "" -}}
{{- if .Values.haproxy.enterprise.enabled -}}
{{- $defaultTag = index .Values.haproxyEnterprisePatchVersions .Values.haproxyVersion -}}
{{- else -}}
{{- $defaultTag = index .Values.haproxyPatchVersions .Values.haproxyVersion -}}
{{- end -}}
{{- $patchTag := .Values.haproxy.image.tag | default $defaultTag | default .Values.haproxyVersion -}}
{{- printf "%s:%s" .Values.haproxy.image.repository $patchTag -}}
{{- end -}}

{{/*
HAProxy binary path
Enterprise: /opt/hapee-{version}/sbin/hapee-lb
Community: /usr/local/sbin/haproxy
*/}}
{{- define "haptic.haproxy.bin" -}}
{{- if .Values.haproxy.haproxyBin -}}
{{- .Values.haproxy.haproxyBin -}}
{{- else if .Values.haproxy.enterprise.enabled -}}
{{- printf "/opt/hapee-%s/sbin/hapee-lb" .Values.haproxy.enterprise.version -}}
{{- else -}}
/usr/local/sbin/haproxy
{{- end -}}
{{- end -}}

{{/*
Dataplane API binary path
Enterprise: /opt/hapee-extras/sbin/hapee-dataplaneapi
Community: /usr/local/bin/dataplaneapi
*/}}
{{- define "haptic.haproxy.dataplanebin" -}}
{{- if .Values.haproxy.dataplaneBin -}}
{{- .Values.haproxy.dataplaneBin -}}
{{- else if .Values.haproxy.enterprise.enabled -}}
/opt/hapee-extras/sbin/hapee-dataplaneapi
{{- else -}}
/usr/local/bin/dataplaneapi
{{- end -}}
{{- end -}}

{{/*
HAProxy / dataplane principal UID & GID.
Enterprise: 1000 (hapee-lb user / hapee group)
Community:  99   (haproxy user / haproxy group)

Used identically as runAsUser, runAsGroup, fsGroup on the HAProxy pod and as
runAsUser on the dataplane container (which shares the volume group).
*/}}
{{- define "haptic.haproxy.uid" -}}
{{- if .Values.haproxy.enterprise.enabled -}}
1000
{{- else -}}
99
{{- end -}}
{{- end -}}
