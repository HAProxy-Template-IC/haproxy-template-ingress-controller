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
      {{- /* Also strip `description`: it's documentation-only metadata the
             testrunner echoes on failure, not part of the test logic. The
             test name already identifies the case. Dropping it from the
             rendered HAProxyTemplateConfig keeps the Helm release Secret
             clear of the K8s 1 MiB limit (these descriptions sum to ~47 KB
             across the bundled libraries). */ -}}
      {{- $_ := set $filteredTests $testName (omit $testDef "_helm_skip_test" "description") }}
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

Each entry in `$libraryFiles` is either:
  - A flat-file path like "libraries/base.yaml" (the original convention).
  - A directory path ending in "/" like "libraries/gateway/" — a "split
    library" whose contents are spread across multiple fragment files.
    `_index.yaml` carries the load rules (`_helm_load`); fragments at the
    same level (and one level deep) merge into the library accumulator
    in lexicographic order before any inject / unset / strip / merge
    happens. Fragments must NOT carry their own `_helm_load` — the
    convention is `_index.yaml`-owns-load-rules, fragments-own-content.
    See ADR-0008.
*/}}
{{- define "haptic.mergeLibraries" -}}
{{- $merged := dict }}
{{- $context := . }}
{{- $libraryFiles := list
    "libraries/base.yaml"
    "libraries/ssl.yaml"
    "libraries/ingress.yaml"
    "libraries/gateway/"
    "libraries/ingress-annotations-compat.yaml"
    "libraries/haproxytech.yaml"
    "libraries/haproxy-ingress/"
    "libraries/nginx-ingress/"
    "libraries/spoa-hub/"
}}
{{- range $file := $libraryFiles }}
  {{- $library := dict }}
  {{- if hasSuffix "/" $file }}
    {{- /* Split library: read _index.yaml as the load-rule authority, */ -}}
    {{- /* then merge fragments in lexicographic order: top-level YAML */ -}}
    {{- /* files plus any one-level-deep YAML files under subdirs.     */ -}}
    {{- /* _index.yaml is excluded from the fragment set. We collect-  */ -}}
    {{- /* then-sort because Helm's Files.Glob returns a map whose     */ -}}
    {{- /* iteration order is unspecified.                             */ -}}
    {{- $indexPath := printf "%s_index.yaml" $file }}
    {{- $library = $context.Files.Get $indexPath | fromYaml }}
    {{- if not $library }}
      {{- fail (printf "split library %q is missing %s" $file $indexPath) }}
    {{- end }}
    {{- $fragmentPaths := list }}
    {{- range $path, $_ := $context.Files.Glob (printf "%s*.yaml" $file) }}
      {{- if ne $path $indexPath }}
        {{- $fragmentPaths = append $fragmentPaths $path }}
      {{- end }}
    {{- end }}
    {{- range $path, $_ := $context.Files.Glob (printf "%s*/*.yaml" $file) }}
      {{- $fragmentPaths = append $fragmentPaths $path }}
    {{- end }}
    {{- range $fragmentPath := sortAlpha $fragmentPaths }}
      {{- $fragment := $context.Files.Get $fragmentPath | fromYaml }}
      {{- if $fragment._helm_load }}
        {{- fail (printf "split-library fragment %q must not declare _helm_load (only _index.yaml does)" $fragmentPath) }}
      {{- end }}
      {{- $library = mustMergeOverwrite $library $fragment }}
    {{- end }}
  {{- else }}
    {{- $library = $context.Files.Get $file | fromYaml }}
  {{- end }}
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

{{- /* Strip Scriggo-template comments from templateSnippets in the merged
       output. Comments document each snippet for chart authors but
       contribute nothing to the rendered HAProxy config — Scriggo strips
       them at template-render time. Their unstripped source still ships
       in the deployed HAProxyTemplateConfig CR, where it's pure overhead.
       The chart's growth has pushed the rendered CR past the 1 MiB
       Kubernetes Secret hard-cap that Helm's release storage hits, so
       the rendered CR has to shrink. Library source files are
       unchanged — chart authors still see verbose inline documentation.

       Three patterns, in order:

         1. Leading {#- ... -#} block at the very start of the template
            (top-of-snippet doc header). Anchored on \A.

         2. Stand-alone {# ... #} block on its own line (mid-template
            documentation). Required to be on its own line so we don't
            remove inline `{#- something -#}` whitespace-control markers
            that share a line with rendered content.

         3. Stand-alone Go-style `// ...` line comments inside Scriggo
            template directives. These appear inside {%- ... -%} or
            {%% ... %%} blocks where Scriggo accepts Go syntax. Same
            stand-alone-line constraint to avoid touching // chars that
            might appear in rendered text (URLs, config values).

       All three patterns require their match to occupy a whole line
       (preceded by \n + whitespace, followed by \n) so removing the
       line collapses the source without changing the surrounding
       formatting. */ -}}
{{- $leadingDocComment := "(?s)\\A\\s*\\{#.*?#\\}\\s*\\n?" }}
{{- $standaloneBlockComment := "(?ms)^[ \\t]*\\{#.*?#\\}[ \\t]*\\n" }}
{{- $standaloneGoComment := "(?m)^[ \\t]*//[^\\n]*\\n" }}
{{- range $name, $snippet := ($merged.templateSnippets | default dict) }}
  {{- $tpl := $snippet.template | default "" }}
  {{- $tpl = regexReplaceAll $leadingDocComment $tpl "" }}
  {{- $tpl = regexReplaceAll $standaloneBlockComment $tpl "" }}
  {{- $tpl = regexReplaceAll $standaloneGoComment $tpl "" }}
  {{- $_ := set $snippet "template" $tpl }}
{{- end }}

{{- /* Return merged config as YAML */ -}}
{{- $merged | toYaml }}
{{- end }}
