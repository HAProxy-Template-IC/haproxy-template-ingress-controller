{{/*
Agent credentials helpers: Secret name, username, password
(with upgrade-stable lookup fallback), and the rolling-update checksum.
*/}}

{{/*
Name of the Secret that holds the agent credentials. Referenced
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
Agent username
Uses provided value or defaults to "admin"
*/}}
{{- define "haptic.dataplane.username" -}}
{{- .Values.credentials.dataplane.username | default "admin" -}}
{{- end -}}

{{/*
Agent password
Priority: 1) User-provided value, 2) Existing Secret value (preserved across
upgrades via lookup), 3) A freshly generated random password.

The agent applies whatever it is sent to HAProxy and is served over plain HTTP
on the cluster network, so the password must not be guessable. The
previous deterministic fallback (sha256 of release name + namespace) derived the
credential entirely from public, guessable inputs — anyone who knew the release
and namespace could reconstruct it. We now generate a random password instead.

The result is memoised on .Values (like the webhook self-signed cert) so the
Secret data and the credentials checksum annotations on both Deployments all see
the SAME value within one render (randAlphaNum is non-deterministic — without
this they would disagree and the pods would roll on every render).

GitOps note: when lookup is unavailable (e.g. ArgoCD/Flux rendering without
cluster access) AND no explicit password is set, a fresh random password is
generated on every render — the value cannot be preserved, so it rotates on
every sync. GitOps users should set credentials.dataplane.password explicitly
(e.g. via a SealedSecret / external secret) — the recommended pattern for any
generated credential under GitOps.
*/}}
{{- define "haptic.dataplane.password" -}}
{{- if not (hasKey .Values "_dataplanePassword") -}}
  {{- $pw := .Values.credentials.dataplane.password | default "" -}}
  {{- if not $pw -}}
    {{- $existing := dig "data" "dataplane_password" "" (lookup "v1" "Secret" .Release.Namespace (include "haptic.dataplane.credentialsSecretName" .)) -}}
    {{- if $existing -}}
      {{- $pw = $existing | b64dec -}}
    {{- else -}}
      {{- $pw = randAlphaNum 32 -}}
    {{- end -}}
  {{- end -}}
  {{- $_ := set .Values "_dataplanePassword" $pw -}}
{{- end -}}
{{- get .Values "_dataplanePassword" -}}
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
