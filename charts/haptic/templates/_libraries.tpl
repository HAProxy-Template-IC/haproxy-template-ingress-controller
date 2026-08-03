{{/*
haptic.companionTestsEnabled — whether validationTests go to the companion
HAProxyValidationTests object or stay inline on the config.

Both templates MUST agree: if only one side flips, the tests are either dropped
from both objects (a suite that silently runs nothing) or emitted twice.

Inline is the fallback, never "no tests". The gate exists because `helm diff
--dry-run=server` runs BEFORE helm's pre-upgrade hooks, so on the upgrade that
first introduces the kind the apply-crds hook has not run yet and the diff
cannot resolve it — "no matches for kind HAProxyValidationTests". Rendering
inline that one time keeps the upgrade working; the hook installs the CRD during
it, and the next apply moves the tests out.

IsInstall is part of the condition because on a FRESH install .Capabilities does
NOT see a CRD the chart itself ships in crds/ (measured: false on install, true
on the next apply) — but Helm applies crds/ before the manifests, so emitting
the companion is safe there. Without this an all-vendors fresh install would
render every test inline and exceed etcd's per-object limit.
*/}}
{{- define "haptic.companionTestsEnabled" -}}
{{- if or .Release.IsInstall (.Capabilities.APIVersions.Has "haproxy-haptic.org/v1alpha1/HAProxyValidationTests") -}}
true
{{- end -}}
{{- end }}

{{/*
haptic.libraryShapedKeys — the config keys a template library and an operator may
BOTH contribute.

One list, two consumers: the operator's values are forwarded through it, and the
merged libraries are set onto the config through it. They were separate lists and
drifted — `k8sResources` was missing from the operator side, so
`controller.config.k8sResources.*` was silently discarded whenever any enabled
library supplied that key. base.yaml could create Services and Events through
k8sResources and an operator's own library could not.

migrationCoverage is deliberately absent: it concatenates across sources rather
than being replaced, and is library-declared only.
*/}}
{{- define "haptic.libraryShapedKeys" -}}
- templateSnippets
- maps
- files
- sslCertificates
- k8sResources
- haproxyConfig
- validationTests
{{- end }}

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
             clear of the K8s 1 MiB limit. This applies at BOTH levels: the
             test-level description and each assertion's `description` (the
             assertion's type/target/pattern fully define it). Together these
             sum to ~120 KB across the bundled libraries. */ -}}
      {{- $stripped := omit $testDef "_helm_skip_test" "description" }}
      {{- if $stripped.assertions }}
        {{- $cleanAssertions := list }}
        {{- range $assertion := $stripped.assertions }}
          {{- $cleanAssertions = append $cleanAssertions (omit $assertion "description") }}
        {{- end }}
        {{- $_ := set $stripped "assertions" $cleanAssertions }}
      {{- end }}
      {{- $_ := set $filteredTests $testName $stripped }}
    {{- end }}
  {{- end }}
  {{- $_ := set $library "validationTests" $filteredTests }}
{{- end }}
{{- $library | toYaml }}
{{- end }}

{{/*
Returns "true" when the nginx-ingress template library is disabled (empty
string otherwise), mirroring `haptic.spoaHub.disabled`. validationTests whose
fixtures rely on nginx.ingress.kubernetes.io/* annotations — scanned only by
the nginx-ingress library's util-nginx-ingress-coraza-scan — use this in their
`_helm_skip_test` predicate so they don't run (and fail) when the library is
absent from the bundle.
*/}}
{{- define "haptic.nginxIngress.disabled" -}}
{{- if not .Values.controller.templateLibraries.nginxIngress.enabled -}}true{{- end -}}
{{- end -}}

{{/*
Returns "true" when the Gateway API *experimental* channel is NOT in use (empty
string otherwise), driven by the explicit `templateLibraries.gateway.experimentalChannel`
value. Experimental-only HTTPRoute fields (sessionPersistence / GEP-1619, retry /
GEP-1731) only exist when experimental-install.yaml is applied, so validationTests
asserting the directives those fields drive use this in their `_helm_skip_test`
predicate — otherwise they fail (and, with the fatal load gate, crash-loop the
controller) on a standard-channel cluster where the snippets correctly emit nothing.

Why a value and not Helm .Capabilities: as of Gateway API v1.6 the Standard and
Experimental installs ship an IDENTICAL CRD set (TCPRoute, ListenerSet, UDPRoute
et al. all graduated to Standard). The only difference is field-level additions
to HTTPRoute, and .Capabilities exposes GVKs, not fields — so no APIVersions.Has
check can tell the channels apart anymore. The operator declares which channel
they installed; offline renders (CI, scripts/test-templates.sh) set the flag true
to exercise these tests against the experimental schemas in tests/schemas.
*/}}
{{- define "haptic.gatewayExperimental.disabled" -}}
{{- if not .Values.controller.templateLibraries.gateway.experimentalChannel -}}true{{- end -}}
{{- end -}}

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
{{- $migrationCoverage := list }}
{{- $context := . }}
{{- /* Each entry is a core path under the parent's libraries/ (base, ssl,
       ingress — always present) or "subchart:<name>" — a conditional subchart
       whose library YAML the parent reads via .Subcharts.<name>.Files. A
       disabled subchart is pruned from the release Secret, so .Subcharts.<name>
       is absent and the entry is skipped. A subchart with a single library.yaml
       is read directly; one with an _index.yaml is a split-library (fragments
       merged in lexicographic order, same as the old split-dir convention). */ -}}
{{- $libraryFiles := list
    "libraries/base.yaml"
    "libraries/ssl.yaml"
    "libraries/ingress.yaml"
    "subchart:gateway"
    "libraries/ingress-annotations-compat.yaml"
    "subchart:haptic-annotations"
    "subchart:haproxytech"
    "subchart:haproxy-ingress"
    "subchart:nginx-ingress"
    "libraries/spoa-hub/"
    "subchart:vector"
}}
{{- /* vector is last on purpose: it contributes only its own `files:` entry (the
       vector config) and reads what the chart projects into extraContext, so it
       depends on every earlier library's port/feature decisions and nothing
       depends on it. A subchart because its source is ~97 KB the release Secret
       would otherwise store on top of the rendered copy. */}}
{{- range $file := $libraryFiles }}
  {{- $library := dict }}
  {{- $skip := false }}
  {{- if hasPrefix "subchart:" $file }}
    {{- $name := trimPrefix "subchart:" $file }}
    {{- $sub := index $context.Subcharts $name }}
    {{- if not $sub }}
      {{- /* subchart disabled (its condition is false) → pruned from the
             release; nothing to read or merge. */ -}}
      {{- $skip = true }}
    {{- else if $sub.Files.Get "_index.yaml" }}
      {{- /* split-library subchart: _index.yaml + fragments */ -}}
      {{- $library = $sub.Files.Get "_index.yaml" | fromYaml }}
      {{- $fragmentPaths := list }}
      {{- range $path, $_ := $sub.Files.Glob "*.yaml" }}
        {{- /* exclude _index.yaml (the load authority, already read) and the
               subchart's own Chart.yaml/values.yaml metadata */ -}}
        {{- if not (or (eq $path "_index.yaml") (eq $path "Chart.yaml") (eq $path "values.yaml")) }}
          {{- $fragmentPaths = append $fragmentPaths $path }}
        {{- end }}
      {{- end }}
      {{- range $path, $_ := $sub.Files.Glob "*/*.yaml" }}
        {{- $fragmentPaths = append $fragmentPaths $path }}
      {{- end }}
      {{- range $fragmentPath := sortAlpha $fragmentPaths }}
        {{- $fragment := $sub.Files.Get $fragmentPath | fromYaml }}
        {{- if $fragment._helm_load }}
          {{- fail (printf "split-library fragment %q must not declare _helm_load (only _index.yaml does)" $fragmentPath) }}
        {{- end }}
        {{- $library = mustMergeOverwrite $library $fragment }}
      {{- end }}
    {{- else }}
      {{- /* single-file subchart */ -}}
      {{- $library = $sub.Files.Get "library.yaml" | fromYaml }}
      {{- if not $library }}
        {{- fail (printf "subchart %q has neither _index.yaml nor library.yaml" $name) }}
      {{- end }}
    {{- end }}
  {{- else if hasSuffix "/" $file }}
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
    {{- range $unsetItem := $loadHints.unset | default list }}
      {{- /* An unset item is either a bare dotted-path string (always removed)
             or a {path, when} map (removed only when `when` tpls to "true").
             The conditional form lets a library strip resource-specific
             watches/snippets/tests when their optional CRD is absent. */ -}}
      {{- if kindIs "string" $unsetItem }}
        {{- include "haptic.unsetNested" (list $library $unsetItem) }}
      {{- else if eq (tpl ($unsetItem.when | default "true") $context | trim) "true" }}
        {{- include "haptic.unsetNested" (list $library $unsetItem.path) }}
      {{- end }}
    {{- end }}
    {{- $_ := unset $library "_helm_load" }}
    {{- $library = include "haptic.filterTests" (list $library $context) | fromYaml }}
    {{- /* migrationCoverage is a LIST of per-source declarations, one entry
           per contributing library. mustMergeOverwrite would REPLACE the
           accumulated list with the current library's, so pull it out and
           concat instead — every enabled library's declaration survives;
           a disabled library (enable=false or pruned subchart) contributes
           nothing. */ -}}
    {{- with $library.migrationCoverage }}
      {{- $migrationCoverage = concat $migrationCoverage . }}
      {{- $_ := unset $library "migrationCoverage" }}
    {{- end }}
    {{- /* mustMergeOverwrite is silently last-wins, so two libraries declaring
           the same snippet or test name would resolve to whichever loads later
           and the losing definition would simply never run. Across separate
           objects the controller reports that as an error; the chart-side merge
           has to raise it itself. */ -}}
    {{- range $key := list "templateSnippets" "validationTests" "maps" "files" "sslCertificates" "k8sResources" }}
      {{- range $name, $_ := (index $library $key | default dict) }}
        {{- /* _global is the documented exception: it is a shared baseline
               several libraries each contribute part of, so "already declared"
               is its normal state. pkg/controller/conversion/union.go makes the
               same exemption. */ -}}
        {{- if and (ne $name "_global") (hasKey (index $merged $key | default dict) $name) }}
          {{- fail (printf "template library %s redefines %s.%s, which another library already declared: one definition would silently replace the other and never run" $file $key $name) }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- $merged = mustMergeOverwrite $merged $library }}
  {{- end }}
{{- end }}

{{- /* Merge user-provided config from values.yaml (highest priority).
       Only the keys with library-overlapping shape are forwarded; other
       controller.config.* fields (routing, dataplane, …) are consumed
       directly by other templates. */ -}}
{{- $userConfig := dict }}
{{- range $key := include "haptic.libraryShapedKeys" . | fromYamlArray }}
  {{- with index $context.Values.controller.config $key }}
    {{- $_ := set $userConfig $key . }}
  {{- end }}
{{- end }}
{{- $merged = mustMergeOverwrite $merged $userConfig }}

{{- /* Operator-declared migrationCoverage (controller.config.migrationCoverage)
       is APPENDED after the library entries — an operator shipping a custom
       annotation library can declare its coverage without erasing the bundled
       libraries' declarations. */ -}}
{{- with $context.Values.controller.config.migrationCoverage }}
  {{- $migrationCoverage = concat $migrationCoverage . }}
{{- end }}
{{- if $migrationCoverage }}
  {{- $_ := set $merged "migrationCoverage" $migrationCoverage }}
{{- end }}

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

{{- /* The same strip for the OTHER template-bearing sections. They were left
       out originally and their comments ship in the CR verbatim — measured at
       2,273 B in `files` (13.3% of it) and 1,417 B in `k8sResources`.

       Two patterns only, and NOT $standaloneGoComment:

       - A whitespace-STRIPPING comment (`{#- … -#}`) removes the newline on
         each side, so deleting its line leaves the preceding newline behind and
         un-fuses output Scriggo had fused. Harmless in an HAProxy config, and
         structural in a YAML `files` template such as the vector config. The
         pattern below therefore matches only the NON-stripping `{# … #}` form,
         where deleting the whole line is output-neutral.
       - `//` is a Go comment only inside a `{%- … -%}` block; in a rendered
         file it can be content. A line-based regex cannot tell the difference,
         and these sections render arbitrary file formats. */ -}}
{{- $safeBlockComment := "(?ms)^[ \\t]*\\{#[^-].*?[^-]#\\}[ \\t]*\\n" }}
{{- range $section := list "files" "maps" "k8sResources" "sslCertificates" }}
  {{- range $name, $entry := ($merged | dig $section dict) }}
    {{- if kindIs "map" $entry }}
      {{- $tpl := $entry.template | default "" }}
      {{- if $tpl }}
        {{- $tpl = regexReplaceAll $leadingDocComment $tpl "" }}
        {{- $tpl = regexReplaceAll $safeBlockComment $tpl "" }}
        {{- $_ := set $entry "template" $tpl }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}

{{- /* Return merged config as YAML */ -}}
{{- $merged | toYaml }}
{{- end }}

{{/*
True when the frontend should compute txn.route, the matched route key. A
predicate rather than `if tracing` because two features want it: spans for
http.route, request metrics for their `path` label. It costs a four-step lookup
cascade per request. Mirrored Scriggo-side by util-want-route, for a CR that
bypasses Helm.
*/}}
{{- define "haptic.routeKeyEnabled" -}}
{{- $ec := .Values.controller.config.templatingSettings.extraContext | default dict -}}
{{- if dig "tracing" "enabled" false $ec -}}
true
{{- else -}}
  {{- $rm := .Values.vector.requestMetrics | default dict -}}
  {{- if and .Values.vector.enabled $rm.enabled $rm.pathLabel -}}
true
  {{- end -}}
{{- end -}}
{{- end -}}
