{{/*
Image references, binary paths, and the principal UID/GID for HAProxy and
the dataplane API container.

`haptic.haproxy.uid` returns the single UID/GID used for runAsUser, runAsGroup,
fsGroup on the HAProxy pod and runAsUser on the dataplane container, which
must share the volume group with HAProxy.
*/}}

{{/*
Controller image
Combines base tag (defaults to Chart.AppVersion) with HAProxy version suffix
Example: registry.gitlab.com/haproxy-haptic/haptic:0.1.0-alpha.12-haproxy3.2
*/}}
{{- define "haptic.controller.image" -}}
{{- printf "%s:%s-haproxy%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) .Values.haproxyVersion -}}
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
{{- $patchVersions := ternary .Values.haproxyEnterprisePatchVersions .Values.haproxyPatchVersions .Values.haproxy.enterprise.enabled -}}
{{- printf "%s:%s" .Values.haproxy.image.repository (.Values.haproxy.image.tag | default (index $patchVersions .Values.haproxyVersion) | default .Values.haproxyVersion) -}}
{{- end -}}

{{/*
HAProxy binary path
Enterprise: /opt/hapee-{version}/sbin/hapee-lb
Community: /usr/local/sbin/haproxy
*/}}
{{- define "haptic.haproxy.bin" -}}
{{- .Values.haproxy.haproxyBin | default (ternary (printf "/opt/hapee-%s/sbin/hapee-lb" .Values.haproxy.enterprise.version) "/usr/local/sbin/haproxy" .Values.haproxy.enterprise.enabled) -}}
{{- end -}}

{{/*
Dataplane API binary path
Enterprise: /opt/hapee-extras/sbin/hapee-dataplaneapi
Community: /usr/local/bin/dataplaneapi
*/}}
{{- define "haptic.haproxy.dataplanebin" -}}
{{- .Values.haproxy.dataplaneBin | default (ternary "/opt/hapee-extras/sbin/hapee-dataplaneapi" "/usr/local/bin/dataplaneapi" .Values.haproxy.enterprise.enabled) -}}
{{- end -}}

{{/*
HAProxy / dataplane principal UID & GID.
Enterprise: 1000 (hapee-lb user / hapee group)
Community:  99   (haproxy user / haproxy group)

Used identically as runAsUser, runAsGroup, fsGroup on the HAProxy pod and as
runAsUser on the dataplane container (which shares the volume group).
*/}}
{{- define "haptic.haproxy.uid" -}}
{{- ternary 1000 99 .Values.haproxy.enterprise.enabled -}}
{{- end -}}
