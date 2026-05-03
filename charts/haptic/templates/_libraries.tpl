{{/*
Library merging — the chart-time loader for charts/haptic/libraries/*.yaml.

Each library file declares its loading rules in a top-level `_helm_load:` block
(enable predicate, optional injects, optional unsets). The loader iterates a
fixed ordered list of library files (the merge order is a system property and
lives here, not in the library files). For each library it: parses YAML,
evaluates `enable`, applies injects (each gated by optional `when`), applies
unsets, strips `_helm_load`, applies `haptic.filterTests` universally (no-op for
libraries with no `_helm_skip_test`), and `mustMergeOverwrite`s into the
accumulator. User-provided `controller.config.*` overrides are merged last.

Schema for `_helm_load:` is documented in charts/CLAUDE.md. See ADR-0002
(docs/adr/0002-decentralized-helm-library-loader.md) for the design rationale.
*/}}

{{/*
Strip skip-marked entries from the library's validationTests block, then
return the (possibly modified) library as YAML. For each test, evaluates
the optional `_helm_skip_test` template expression: if it trims to
"true", the test is dropped; otherwise the test is kept with its
`_helm_skip_test` metadata stripped. Libraries with no validationTests
or no `_helm_skip_test` markers pass through untouched.
Args (list): [library dict, root context]
*/}}
{{- define "haptic.filterTests" -}}
{{- $library := index . 0 }}
{{- $context := index . 1 }}
{{- if $library.validationTests }}
  {{- $filteredTests := dict }}
  {{- range $testName, $testDef := $library.validationTests }}
    {{- /* Drop tests whose _helm_skip_test expression renders to "true"; keep the rest with the marker stripped. */ -}}
    {{- if not (and $testDef._helm_skip_test (eq (tpl ($testDef._helm_skip_test | toString) $context | trim) "true")) }}
      {{- $_ := set $filteredTests $testName (omit $testDef "_helm_skip_test") }}
    {{- end }}
  {{- end }}
  {{- $_ := set $library "validationTests" $filteredTests }}
{{- end }}
{{- $library | toYaml }}
{{- end }}

{{/*
Set a value at a dotted path within a dict, creating intermediate dicts as needed.
Args (list): [obj dict, path string, value any]
Returns: empty (mutates obj by side effect)
*/}}
{{- define "haptic.setNested" -}}
{{- $cursor := index . 0 -}}
{{- $value := index . 2 -}}
{{- $parts := splitList "." (index . 1) -}}
{{- $lastIdx := sub (len $parts) 1 -}}
{{- range $idx, $part := $parts -}}
  {{- if lt $idx $lastIdx -}}
    {{- if not (hasKey $cursor $part) -}}
      {{- $_ := set $cursor $part dict -}}
    {{- end -}}
    {{- $cursor = index $cursor $part -}}
  {{- else -}}
    {{- $_ := set $cursor $part $value -}}
  {{- end -}}
{{- end -}}
{{- end }}

{{/*
Get a value at a dotted path within a dict, returning JSON-encoded.
Callers do `(include "haptic.getNested" ... | fromJson)` to round-trip dicts/lists.
Args (list): [obj dict, path string]
Returns: JSON-encoded value, or "null" if path missing.
*/}}
{{- define "haptic.getNested" -}}
{{- $cursor := index . 0 -}}
{{- $parts := splitList "." (index . 1) -}}
{{- $reachable := true -}}
{{- range $part := $parts -}}
  {{- if and $reachable (kindIs "map" $cursor) (hasKey $cursor $part) -}}
    {{- $cursor = index $cursor $part -}}
  {{- else -}}
    {{- $reachable = false -}}
  {{- end -}}
{{- end -}}
{{- if $reachable -}}{{ $cursor | toJson }}{{- else -}}null{{- end -}}
{{- end }}

{{/*
Unset a key at a dotted path within a dict. No-op if path missing.
Args (list): [obj dict, path string]
Returns: empty (mutates obj by side effect)
*/}}
{{- define "haptic.unsetNested" -}}
{{- $cursor := index . 0 -}}
{{- $parts := splitList "." (index . 1) -}}
{{- $lastIdx := sub (len $parts) 1 -}}
{{- $reachable := true -}}
{{- range $idx, $part := $parts -}}
  {{- if lt $idx $lastIdx -}}
    {{- if and $reachable (kindIs "map" $cursor) (hasKey $cursor $part) -}}
      {{- $cursor = index $cursor $part -}}
    {{- else -}}
      {{- $reachable = false -}}
    {{- end -}}
  {{- else if $reachable -}}
    {{- $_ := unset $cursor $part -}}
  {{- end -}}
{{- end -}}
{{- end }}

{{/*
Deep merge template libraries.
*/}}
{{- define "haptic.mergeLibraries" -}}
{{- $merged := dict }}
{{- $context := . }}
{{- $libraryFiles := list
    "libraries/base.yaml"
    "libraries/ssl.yaml"
    "libraries/ingress.yaml"
    "libraries/gateway.yaml"
    "libraries/annotation-compat.yaml"
    "libraries/haproxytech.yaml"
    "libraries/haproxy-ingress.yaml"
    "libraries/nginx-ingress.yaml"
    "libraries/spoa-hub.yaml"
}}
{{- range $file := $libraryFiles }}
  {{- $library := $context.Files.Get $file | fromYaml }}
  {{- $loadHints := $library._helm_load | default dict }}
  {{- if eq (tpl ($loadHints.enable | default "true") $context | trim) "true" }}
    {{- range $inject := $loadHints.inject | default list }}
      {{- if eq (tpl ($inject.when | default "true") $context | trim) "true" }}
        {{- if hasKey $inject "from" }}
          {{- include "haptic.setNested" (list $library $inject.path (include "haptic.getNested" (list $library $inject.from) | fromJson)) }}
        {{- else }}
          {{- include "haptic.setNested" (list $library $inject.path (tpl ($inject.value | toString) $context)) }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- range $unsetPath := $loadHints.unset | default list }}
      {{- include "haptic.unsetNested" (list $library $unsetPath) }}
    {{- end }}
    {{- $_ := unset $library "_helm_load" }}
    {{- $library = include "haptic.filterTests" (list $library $context) | fromYaml }}
    {{- $merged = mustMergeOverwrite $merged $library }}
  {{- end }}
{{- end }}

{{- /* Merge user-provided config from values.yaml (highest priority).
       Only the keys with library-overlapping shape are forwarded; other
       controller.config.* fields (routing, dataplane, …) are consumed
       directly by other templates. */ -}}
{{- $userConfig := dict }}
{{- range $key := list "templateSnippets" "maps" "files" "sslCertificates" "haproxyConfig" "validationTests" }}
  {{- with index $context.Values.controller.config $key }}
    {{- $_ := set $userConfig $key . }}
  {{- end }}
{{- end }}
{{- $merged = mustMergeOverwrite $merged $userConfig }}

{{- /* Return merged config as YAML */ -}}
{{- $merged | toYaml }}
{{- end }}
