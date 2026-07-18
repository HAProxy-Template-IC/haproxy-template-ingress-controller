{{/*
Webhook TLS certificate helpers.

The chart supports three mutually exclusive ways to provision the webhook
serving certificate (see values.yaml `controller.webhook`):

  1. cert-manager        — controller.webhook.certManager.enabled=true (opt-in; recommended
                           for production: real CA chains + automatic rotation).
  2. chart self-signed   — controller.webhook.certManager.enabled=false AND no
                           controller.webhook.caBundle (the DEFAULT): the chart generates a
                           long-lived self-signed serving cert so the webhook
                           works out of the box with no external dependency.
  3. manual              — controller.webhook.certManager.enabled=false AND
                           controller.webhook.caBundle
                           set: the operator manages the cert Secret + caBundle.
*/}}

{{/*
haptic.webhook.selfSigned returns "true" when the chart should generate the
self-signed webhook certificate itself (mode 2 above), empty otherwise.
*/}}
{{- define "haptic.webhook.selfSigned" -}}
{{- if and .Values.controller.webhook.enabled (not .Values.controller.webhook.certManager.enabled) (not .Values.controller.webhook.caBundle) -}}
true
{{- end -}}
{{- end -}}

{{/*
haptic.webhook.selfSignedCert returns the self-signed webhook certificate as a
YAML dict { crt, key, ca } of PEM strings (caBundle == the self-signed cert,
since it is its own issuer).

On upgrade it REUSES the existing Secret via `lookup`, so the cert is stable
across upgrades — no churn, no caBundle flap. It generates a fresh long-lived
cert (10y) only on first install or when the Secret is missing/incomplete (e.g.
`helm template`/CI with no cluster).

The result is memoised on `.Values` so the Secret template and the
ValidatingWebhookConfiguration both see the SAME certificate within one render
(genSelfSignedCert is non-deterministic — without this, the served cert and the
injected caBundle would not match).
*/}}
{{- define "haptic.webhook.selfSignedCert" -}}
{{- if not (hasKey .Values "_webhookSelfSignedCert") -}}
  {{- $svc := include "haptic.webhook.serviceName" . -}}
  {{- $ns := .Release.Namespace -}}
  {{- $altNames := list $svc (printf "%s.%s" $svc $ns) (printf "%s.%s.svc" $svc $ns) (printf "%s.%s.svc.cluster.local" $svc $ns) -}}
  {{- $secretName := include "haptic.webhook.secretName" . -}}
  {{- $existing := lookup "v1" "Secret" $ns $secretName -}}
  {{- $result := dict -}}
  {{- if and $existing $existing.data (index $existing.data "tls.crt") (index $existing.data "tls.key") -}}
    {{- $ca := index $existing.data "ca.crt" | default (index $existing.data "tls.crt") -}}
    {{- $result = dict
        "crt" (index $existing.data "tls.crt" | b64dec)
        "key" (index $existing.data "tls.key" | b64dec)
        "ca"  ($ca | b64dec) -}}
  {{- else -}}
    {{- $days := (.Values.controller.webhook.selfSigned | default dict).certValidityDays | default 3650 | int -}}
    {{- $cert := genSelfSignedCert (printf "%s.%s.svc" $svc $ns) nil $altNames $days -}}
    {{- $result = dict "crt" $cert.Cert "key" $cert.Key "ca" $cert.Cert -}}
  {{- end -}}
  {{- $_ := set .Values "_webhookSelfSignedCert" $result -}}
{{- end -}}
{{- get .Values "_webhookSelfSignedCert" | toYaml -}}
{{- end -}}

{{/*
haptic.webhook.caBundle returns the base64-encoded CA bundle to inject into the
ValidatingWebhookConfiguration's clientConfig, for the non-cert-manager modes:
the generated self-signed cert (mode 2) or the operator-provided
controller.webhook.caBundle
(mode 3, required). cert-manager (mode 1) injects via annotation, not this value.
*/}}
{{- define "haptic.webhook.caBundle" -}}
{{- if include "haptic.webhook.selfSigned" . -}}
{{- (include "haptic.webhook.selfSignedCert" . | fromYaml).ca | b64enc -}}
{{- else -}}
{{- required "controller.webhook.caBundle is required when controller.webhook.certManager.enabled is false and no chart self-signed cert is generated" .Values.controller.webhook.caBundle -}}
{{- end -}}
{{- end -}}
