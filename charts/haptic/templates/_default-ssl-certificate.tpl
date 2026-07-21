{{/*
Validate the chart-owned default TLS certificate surface. Inline Secret
ownership and cert-manager ownership are deliberately mutually exclusive:
both paths targeting the same Secret is a reconciliation race, not a fallback.
Disabled/staged settings are validated so enabling them later is predictable.
*/}}
{{- define "haptic.defaultSSLCertificate.validateValues" -}}
{{- $default := .Values.defaultSSLCertificate -}}
{{- if not (kindIs "map" $default) -}}{{- fail "defaultSSLCertificate must be a map." -}}{{- end -}}
{{- range $field := keys $default -}}
  {{- if not (has $field (list "enabled" "secretName" "namespace" "certManager" "create" "cert" "key" "ecdsaSecretName")) -}}{{- fail (printf "defaultSSLCertificate contains unknown field %q." $field) -}}{{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $default.enabled) -}}{{- fail "defaultSSLCertificate.enabled must be a boolean." -}}{{- end -}}
{{- if and (hasKey $default "ecdsaSecretName") (not (kindIs "string" $default.ecdsaSecretName)) -}}{{- fail "defaultSSLCertificate.ecdsaSecretName must be a string." -}}{{- end -}}
{{- if and (kindIs "string" $default.ecdsaSecretName) (ne $default.ecdsaSecretName "") (or (gt (len $default.ecdsaSecretName) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $default.ecdsaSecretName))) -}}
  {{- fail "defaultSSLCertificate.ecdsaSecretName must be empty or a valid Kubernetes DNS subdomain no longer than 253 characters." -}}
{{- end -}}
{{- if and (kindIs "string" $default.ecdsaSecretName) (ne $default.ecdsaSecretName "") (eq $default.ecdsaSecretName $default.secretName) -}}
  {{- fail "defaultSSLCertificate.ecdsaSecretName must differ from defaultSSLCertificate.secretName; equal names share a namespace and resolve to the same Secret (a redundant no-op dual)." -}}
{{- end -}}
{{- if not (kindIs "bool" $default.create) -}}{{- fail "defaultSSLCertificate.create must be a boolean." -}}{{- end -}}
{{- if or (not (kindIs "string" $default.secretName)) (gt (len $default.secretName) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $default.secretName)) -}}
  {{- fail "defaultSSLCertificate.secretName must be a valid non-empty Kubernetes DNS subdomain no longer than 253 characters." -}}
{{- end -}}
{{- if not (kindIs "string" $default.namespace) -}}{{- fail "defaultSSLCertificate.namespace must be a string." -}}{{- end -}}
{{- if and (ne $default.namespace "") (or (gt (len $default.namespace) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $default.namespace))) -}}
  {{- fail "defaultSSLCertificate.namespace must be empty or a valid Kubernetes namespace no longer than 63 characters." -}}
{{- end -}}

{{- $cm := $default.certManager | default dict -}}
{{- if not (kindIs "map" $cm) -}}{{- fail "defaultSSLCertificate.certManager must be a map." -}}{{- end -}}
{{- range $field := keys $cm -}}
  {{- if not (has $field (list "enabled" "createIssuer" "dnsNames" "issuerRef" "duration" "renewBefore")) -}}{{- fail (printf "defaultSSLCertificate.certManager contains unknown field %q." $field) -}}{{- end -}}
{{- end -}}
{{- if not (kindIs "bool" $cm.enabled) -}}{{- fail "defaultSSLCertificate.certManager.enabled must be a boolean." -}}{{- end -}}
{{- if not (kindIs "bool" $cm.createIssuer) -}}{{- fail "defaultSSLCertificate.certManager.createIssuer must be a boolean." -}}{{- end -}}
{{- if not (kindIs "slice" $cm.dnsNames) -}}{{- fail "defaultSSLCertificate.certManager.dnsNames must be a list." -}}{{- end -}}
{{- if and $cm.enabled (eq (len $cm.dnsNames) 0) -}}{{- fail "defaultSSLCertificate.certManager.dnsNames must be non-empty while cert-manager integration is enabled." -}}{{- end -}}
{{- range $dnsName := $cm.dnsNames -}}
  {{- if or (not (kindIs "string" $dnsName)) (eq (trim $dnsName) "") -}}{{- fail "defaultSSLCertificate.certManager.dnsNames entries must be non-empty strings." -}}{{- end -}}
{{- end -}}
{{- range $field := list "duration" "renewBefore" -}}
  {{- $value := index $cm $field -}}
  {{- if or (not (kindIs "string" $value)) (eq (trim $value) "") -}}{{- fail (printf "defaultSSLCertificate.certManager.%s must be a non-empty duration string." $field) -}}{{- end -}}
{{- end -}}
{{- $issuerRef := $cm.issuerRef | default dict -}}
{{- if not (kindIs "map" $issuerRef) -}}{{- fail "defaultSSLCertificate.certManager.issuerRef must be a map." -}}{{- end -}}
{{- range $field := keys $issuerRef -}}
  {{- if not (has $field (list "name" "kind" "group")) -}}{{- fail (printf "defaultSSLCertificate.certManager.issuerRef contains unknown field %q. Valid fields: name, kind, group." $field) -}}{{- end -}}
{{- end -}}
{{- range $field := list "name" "kind" -}}
  {{- if not (kindIs "string" (index $issuerRef $field)) -}}{{- fail (printf "defaultSSLCertificate.certManager.issuerRef.%s must be a string." $field) -}}{{- end -}}
{{- end -}}
{{- if and (hasKey $issuerRef "group") (not (kindIs "string" $issuerRef.group)) -}}{{- fail "defaultSSLCertificate.certManager.issuerRef.group must be a string." -}}{{- end -}}
{{- if and $cm.enabled (not $cm.createIssuer) (eq (trim $issuerRef.name) "") -}}{{- fail "defaultSSLCertificate.certManager.issuerRef.name is required when certManager.enabled=true and createIssuer=false." -}}{{- end -}}

{{- $cert := "" -}}{{- if hasKey $default "cert" -}}{{- if not (kindIs "string" $default.cert) -}}{{- fail "defaultSSLCertificate.cert must be a string." -}}{{- end -}}{{- $cert = $default.cert -}}{{- end -}}
{{- $key := "" -}}{{- if hasKey $default "key" -}}{{- if not (kindIs "string" $default.key) -}}{{- fail "defaultSSLCertificate.key must be a string." -}}{{- end -}}{{- $key = $default.key -}}{{- end -}}
{{- if and $default.create $cm.enabled -}}{{- fail "defaultSSLCertificate.create=true requires defaultSSLCertificate.certManager.enabled=false so only one actor owns the TLS Secret." -}}{{- end -}}
{{- if and $default.create (or (eq (trim $cert) "") (eq (trim $key) "")) -}}{{- fail "defaultSSLCertificate.create=true requires non-empty defaultSSLCertificate.cert and defaultSSLCertificate.key PEM strings." -}}{{- end -}}
{{- if and (not $default.create) (or (ne (trim $cert) "") (ne (trim $key) "")) -}}{{- fail "defaultSSLCertificate.cert and key are ignored unless defaultSSLCertificate.create=true; remove them or enable inline Secret creation." -}}{{- end -}}
{{- end -}}
