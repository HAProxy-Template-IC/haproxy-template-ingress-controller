{{- define "haptic.agentTLS.serverSecretName" -}}
{{- .Values.haproxy.agent.tls.serverSecretName | default (include "haptic.suffixedName" (dict "name" (include "haptic.fullname" .) "suffix" "-agent-tls" "maxLength" 63)) -}}
{{- end -}}

{{- define "haptic.agentTLS.clientSecretName" -}}
{{- .Values.haproxy.agent.tls.clientSecretName | default (include "haptic.suffixedName" (dict "name" (include "haptic.fullname" .) "suffix" "-controller-tls" "maxLength" 63)) -}}
{{- end -}}

{{- define "haptic.agentTLS.serverName" -}}
{{- .Values.haproxy.agent.tls.serverName | default (printf "%s.%s.svc" (include "haptic.agentTLS.serverSecretName" .) .Release.Namespace) -}}
{{- end -}}

{{- define "haptic.agentTLS.clientName" -}}
{{- .Values.haproxy.agent.tls.clientName | default (printf "%s.%s.svc" (include "haptic.agentTLS.clientSecretName" .) .Release.Namespace) -}}
{{- end -}}

{{- define "haptic.agentTLS.issuerSecretName" -}}
{{- .Values.haproxy.agent.tls.issuerSecretName | default (include "haptic.suffixedName" (dict "name" (include "haptic.fullname" .) "suffix" "-agent-issuer" "maxLength" 63)) -}}
{{- end -}}

{{- define "haptic.agentTLS.renewalName" -}}
{{- include "haptic.suffixedName" (dict "name" (include "haptic.fullname" .) "suffix" "-agent-renewal" "maxLength" 52) -}}
{{- end -}}

{{- define "haptic.agentTLS.validate" -}}
{{- $tls := .Values.haproxy.agent.tls -}}
{{- if not (kindIs "map" $tls) -}}{{- fail "haproxy.agent.tls must be an object" -}}{{- end -}}
{{- $fields := list "enabled" "managed" "issuerSecretName" "serverSecretName" "clientSecretName" "serverName" "clientName" "certValidityDays" "renewal" "certManager" -}}
{{- range $field := keys $tls -}}
  {{- if not (has $field $fields) -}}{{- fail (printf "haproxy.agent.tls contains unknown field %q" $field) -}}{{- end -}}
{{- end -}}
{{- range $field := list "enabled" "managed" -}}
  {{- if not (kindIs "bool" (get $tls $field)) -}}{{- fail (printf "haproxy.agent.tls.%s must be a boolean" $field) -}}{{- end -}}
{{- end -}}
{{- range $field := list "issuerSecretName" "serverSecretName" "clientSecretName" "serverName" "clientName" -}}
  {{- if not (kindIs "string" (get $tls $field)) -}}{{- fail (printf "haproxy.agent.tls.%s must be a string" $field) -}}{{- end -}}
{{- end -}}
{{- include "haptic.agentTLS.validateManager" . -}}
{{- if $tls.enabled -}}
  {{- range $field := list "issuerSecretName" "serverSecretName" "clientSecretName" "serverName" "clientName" -}}
    {{- $name := get $tls $field -}}
    {{- if and $name (or (gt (len $name) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $name))) -}}
      {{- fail (printf "haproxy.agent.tls.%s must be a lowercase DNS name" $field) -}}
    {{- end -}}
  {{- end -}}

  {{- range $role := list "server" "client" -}}
    {{- $name := include (printf "haptic.agentTLS.%sName" $role) $ -}}
    {{- if gt (len $name) 253 -}}{{- fail (printf "Agent TLS %s identity exceeds 253 characters; set a shorter %sName" $role $role) -}}{{- end -}}
    {{- range $label := splitList "." $name -}}
      {{- if gt (len $label) 63 -}}{{- fail (printf "Agent TLS %s identity has a DNS label longer than 63 characters; set a shorter %sName" $role $role) -}}{{- end -}}
    {{- end -}}
  {{- end -}}

  {{- if and (not $tls.managed) (or (not $tls.serverSecretName) (not $tls.clientSecretName)) -}}
    {{- fail "External agent TLS needs serverSecretName and clientSecretName. Create both identity Secrets before installing." -}}
  {{- end -}}
  {{- if eq (include "haptic.agentTLS.serverSecretName" .) (include "haptic.agentTLS.clientSecretName" .) -}}
    {{- fail "Agent TLS server and client Secret names must differ" -}}
  {{- end -}}
  {{- if eq (include "haptic.agentTLS.serverName" .) (include "haptic.agentTLS.clientName" .) -}}
    {{- fail "Agent TLS server and client certificate names must differ" -}}
  {{- end -}}
  {{- if and $tls.managed (has (include "haptic.agentTLS.issuerSecretName" .) (list (include "haptic.agentTLS.serverSecretName" .) (include "haptic.agentTLS.clientSecretName" .))) -}}
    {{- fail "Agent TLS issuer and identity Secret names must differ" -}}
  {{- end -}}
  {{- if or (not (regexMatch "^[1-9][0-9]*$" (toString $tls.certValidityDays))) (gt (int $tls.certValidityDays) 3650) -}}
    {{- fail "haproxy.agent.tls.certValidityDays must be an integer from 1 to 3650" -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{- define "haptic.agentTLS.validateManager" -}}
{{- $tls := .Values.haproxy.agent.tls -}}
{{- if not (kindIs "map" $tls.certManager) -}}{{- fail "haproxy.agent.tls.certManager must be an object" -}}{{- end -}}
{{- range $field := keys $tls.certManager -}}
  {{- if not (has $field (list "enabled" "createIssuer" "issuerRef")) -}}{{- fail (printf "haproxy.agent.tls.certManager contains unknown field %q" $field) -}}{{- end -}}
{{- end -}}
{{- range $field := list "enabled" "createIssuer" -}}
  {{- if not (kindIs "bool" (get $tls.certManager $field)) -}}{{- fail (printf "haproxy.agent.tls.certManager.%s must be a boolean" $field) -}}{{- end -}}
{{- end -}}
{{- if not (kindIs "map" $tls.certManager.issuerRef) -}}{{- fail "haproxy.agent.tls.certManager.issuerRef must be an object" -}}{{- end -}}
{{- range $field := keys $tls.certManager.issuerRef -}}
  {{- if not (has $field (list "name" "kind" "group")) -}}{{- fail (printf "haproxy.agent.tls.certManager.issuerRef contains unknown field %q" $field) -}}{{- end -}}
{{- end -}}
{{- range $field := list "name" "kind" "group" -}}
  {{- if not (kindIs "string" (get $tls.certManager.issuerRef $field)) -}}{{- fail (printf "haproxy.agent.tls.certManager.issuerRef.%s must be a string" $field) -}}{{- end -}}
{{- end -}}
{{- if and $tls.enabled $tls.certManager.enabled -}}
  {{- if not $tls.managed -}}{{- fail "Agent cert-manager provisioning requires haproxy.agent.tls.managed=true" -}}{{- end -}}
  {{- if and $tls.certManager.createIssuer $tls.certManager.issuerRef.name -}}{{- fail "Set agent TLS certManager.createIssuer=false when selecting an existing issuer" -}}{{- end -}}
  {{- if and (not $tls.certManager.createIssuer) (or (not $tls.certManager.issuerRef.name) (not $tls.certManager.issuerRef.kind) (not $tls.certManager.issuerRef.group)) -}}
    {{- fail "External agent TLS issuerRef needs name, kind, and group" -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "map" $tls.renewal) -}}{{- fail "haproxy.agent.tls.renewal must be an object" -}}{{- end -}}
{{- range $field := keys $tls.renewal -}}
  {{- if ne $field "resources" -}}{{- fail (printf "haproxy.agent.tls.renewal contains unknown field %q" $field) -}}{{- end -}}
{{- end -}}
{{- if not (kindIs "map" $tls.renewal.resources) -}}{{- fail "haproxy.agent.tls.renewal.resources must be an object" -}}{{- end -}}
{{- end -}}
