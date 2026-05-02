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
Filter validationTests based on _helm_skip_test condition
Evaluates _helm_skip_test Go template and excludes tests where it evaluates to "true"
*/}}
{{- define "haptic.filterTests" -}}
{{- $library := index . 0 }}
{{- $context := index . 1 }}
{{- if $library.validationTests }}
  {{- $filteredTests := dict }}
  {{- range $testName, $testDef := $library.validationTests }}
    {{- $skipTest := false }}
    {{- if $testDef._helm_skip_test }}
      {{- /* Evaluate _helm_skip_test template expression */ -}}
      {{- $skipCondition := tpl $testDef._helm_skip_test $context }}
      {{- if eq $skipCondition "true" }}
        {{- $skipTest = true }}
      {{- end }}
    {{- end }}
    {{- if not $skipTest }}
      {{- /* Include test, removing _helm_skip_test metadata */ -}}
      {{- $cleanTest := omit $testDef "_helm_skip_test" }}
      {{- $_ := set $filteredTests $testName $cleanTest }}
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
{{- $obj := index . 0 -}}
{{- $path := index . 1 -}}
{{- $value := index . 2 -}}
{{- $parts := splitList "." $path -}}
{{- $lastIdx := sub (len $parts) 1 -}}
{{- $cursor := $obj -}}
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
{{- $obj := index . 0 -}}
{{- $path := index . 1 -}}
{{- $parts := splitList "." $path -}}
{{- $cursor := $obj -}}
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
*/}}
{{- define "haptic.unsetNested" -}}
{{- $obj := index . 0 -}}
{{- $path := index . 1 -}}
{{- $parts := splitList "." $path -}}
{{- $lastIdx := sub (len $parts) 1 -}}
{{- $cursor := $obj -}}
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
  {{- $enableExpr := $loadHints.enable | default "true" }}
  {{- $enabled := tpl $enableExpr $context | trim }}
  {{- if eq $enabled "true" }}
    {{- range $inject := $loadHints.inject | default list }}
      {{- $whenExpr := $inject.when | default "true" }}
      {{- if eq (tpl $whenExpr $context | trim) "true" }}
        {{- if hasKey $inject "from" }}
          {{- $copied := include "haptic.getNested" (list $library $inject.from) | fromJson }}
          {{- include "haptic.setNested" (list $library $inject.path $copied) }}
        {{- else }}
          {{- $value := tpl ($inject.value | toString) $context }}
          {{- include "haptic.setNested" (list $library $inject.path $value) }}
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
{{- $userConfigKeys := list "templateSnippets" "maps" "files" "sslCertificates" "haproxyConfig" "validationTests" }}
{{- range $key := $userConfigKeys }}
  {{- $value := index $context.Values.controller.config $key }}
  {{- if $value }}
    {{- $_ := set $userConfig $key $value }}
  {{- end }}
{{- end }}
{{- $merged = mustMergeOverwrite $merged $userConfig }}

{{- /* Return merged config as YAML */ -}}
{{- $merged | toYaml }}
{{- end }}
