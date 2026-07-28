{{/*
Library loading — the chart-time loader for charts/haptic/libraries/*.yaml.

Each library file declares its loading rules in a top-level `_helm_load:` block
(enable predicate, optional injects, optional unsets). The loader iterates a
fixed ordered list of library files (the order is a system property and lives
here, not in the library files). For each library it: parses YAML, evaluates
`enable`, applies injects (each gated by optional `when`), applies unsets,
strips `_helm_load`, applies `haptic.filterTests` universally (no-op for
libraries with no `_helm_skip_test`), and strips Scriggo comments.

The loader does NOT merge the libraries together. Each one is rendered as its
own HAProxyTemplateConfig and the controller merges the set at startup, in the
order it is handed via `CRD_NAME`, with the operator's own config last. That
keeps one merge implementation instead of two that can drift, and it keeps each
object clear of etcd's ~1.5 MiB per-object ceiling, which the single merged
config had reached. See ADR-0014.

Schema for `_helm_load:` is documented in charts/CLAUDE.md. See ADR-0002
(docs/adr/0002-decentralized-helm-library-loader.md) for the design rationale
of the decentralized load rules, which this preserves.
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
Ordered list of library sources. THE merge order — the controller replays it
verbatim from the `CRD_NAME` list, so this list is the single authority for
precedence and every consumer derives from it rather than restating it.

Each entry is a core path under the parent's libraries/ (base, ssl, ingress —
always present) or "subchart:<name>" — a conditional subchart whose library
YAML the parent reads via .Subcharts.<name>.Files. A disabled subchart is
pruned from the release Secret, so .Subcharts.<name> is absent and the entry
is skipped. A subchart with a single library.yaml is read directly; one with
an _index.yaml is a split-library (fragments merged in lexicographic order,
same as the old split-dir convention).

vector.yaml is last on purpose: it contributes only its own `files:` entry
(the vector config) and reads the settings the chart projects into
extraContext, so it depends on every earlier library's port/feature decisions
and nothing depends on it.
*/}}
{{- define "haptic.libraryFiles" -}}
- libraries/base.yaml
- libraries/ssl.yaml
- libraries/ingress.yaml
- subchart:gateway
- libraries/ingress-annotations-compat.yaml
- subchart:haptic-annotations
- subchart:haproxytech
- subchart:haproxy-ingress
- subchart:nginx-ingress
- libraries/spoa-hub/
- libraries/vector.yaml
{{- end }}

{{/*
Short name for a library source path, used as the suffix of its
HAProxyTemplateConfig object. Deliberately carries no ordering index: order
lives in the `CRD_NAME` list, so inserting a library never renames an existing
object.
Args: the source path.
*/}}
{{- define "haptic.librarySlug" -}}
{{- . | trimPrefix "subchart:" | trimPrefix "libraries/" | trimSuffix "/" | trimSuffix ".yaml" }}
{{- end }}

{{/*
The load rules of one library source, as YAML, or "" when the source is absent
(a pruned subchart). Reads only the load-rule authority — the flat file, or
_index.yaml / library.yaml for a directory or subchart — never the fragments,
so callers that just need the enable predicate don't pay for parsing a whole
split library.
Args (list): [source path, root context]
*/}}
{{- define "haptic.libraryLoadRules" -}}
{{- $file := index . 0 }}
{{- $context := index . 1 }}
{{- if hasPrefix "subchart:" $file }}
  {{- $sub := index $context.Subcharts (trimPrefix "subchart:" $file) }}
  {{- if $sub }}
    {{- (($sub.Files.Get "_index.yaml" | fromYaml)._helm_load | default (($sub.Files.Get "library.yaml" | fromYaml)._helm_load)) | toYaml }}
  {{- end }}
{{- else if hasSuffix "/" $file }}
  {{- ($context.Files.Get (printf "%s_index.yaml" $file) | fromYaml)._helm_load | toYaml }}
{{- else }}
  {{- ($context.Files.Get $file | fromYaml)._helm_load | toYaml }}
{{- end }}
{{- end }}

{{/*
Names of the enabled libraries' HAProxyTemplateConfig objects, in merge order,
as a YAML list. The controller is handed this list plus the operator's own
config name; a config it is not told about is not merged.

Evaluates the same `_helm_load.enable` predicates haptic.prepareLibraries does,
against the same source list, but without reading fragments. The two agreeing
is pinned by a chart unit test rather than by construction, because Helm has no
way to share one evaluation across template files.
Args: root context.
*/}}
{{- define "haptic.libraryConfigNames" -}}
{{- $context := . }}
{{- $configName := $context.Values.controller.configName }}
{{- range $file := (include "haptic.libraryFiles" $context | fromYamlArray) }}
  {{- $rules := include "haptic.libraryLoadRules" (list $file $context) | fromYaml }}
  {{- if $rules }}
    {{- if eq (tpl ($rules.enable | default "true") $context | trim) "true" }}
- {{ printf "%s-%s" $configName (include "haptic.librarySlug" $file) }}
    {{- end }}
  {{- end }}
{{- end }}
{{- end }}

{{/*
Load every enabled template library and return them prepared but NOT merged, as
`libraries: [{name, config}, ...]` in merge order. Each `config` is a complete
HAProxyTemplateConfig spec fragment ready to be rendered as its own object.

Each source is either:
  - A flat-file path like "libraries/base.yaml" (the original convention).
  - A directory path ending in "/" like "libraries/spoa-hub/" — a "split
    library" whose contents are spread across multiple fragment files.
    `_index.yaml` carries the load rules (`_helm_load`); fragments at the
    same level (and one level deep) merge into the library accumulator in
    lexicographic order before any inject / unset / strip happens. Fragments
    must NOT carry their own `_helm_load` — the convention is
    `_index.yaml`-owns-load-rules, fragments-own-content. See ADR-0008.
  - "subchart:<name>" — the same two shapes, read from a conditional subchart.

Fragments within one split library are still merged here with
`mustMergeOverwrite`; only merging ACROSS libraries moved to the controller.
Args: root context.
*/}}
{{- define "haptic.prepareLibraries" -}}
{{- $prepared := list }}
{{- $context := . }}
{{- $libraryFiles := include "haptic.libraryFiles" $context | fromYamlArray }}
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
  {{- /* $skip covers a pruned subchart, which yields no library at all. It has
         to gate the branch explicitly: an absent `_helm_load` defaults `enable`
         to "true", so without it a pruned subchart would render as an empty
         object — harmless when everything merged into one accumulator, a bogus
         config now that each library is its own object. */ -}}
  {{- if and (not $skip) (eq (tpl ($loadHints.enable | default "true") $context | trim) "true") }}
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
    {{- $library = include "haptic.filterTests" (list $library $context) | fromYaml }}
    {{- /* Every underscore-prefixed top-level key is chart-time-only and must
           not reach a rendered object: `_helm_load` (load rules) and ssl.yaml's
           `_test_tls_*` YAML-anchor scratch values. The CRD declares no such
           property, so an object carrying one would be rejected. Previously
           they were dropped implicitly, by the emitter forwarding an explicit
           key allow-list out of the merged accumulator. */ -}}
    {{- range $key, $_ := $library }}
      {{- if hasPrefix "_" $key }}
        {{- $_ := unset $library $key }}
      {{- end }}
    {{- end }}
    {{- include "haptic.stripSnippetComments" $library }}
    {{- $prepared = append $prepared (dict "name" (include "haptic.librarySlug" $file) "config" $library) }}
  {{- end }}
{{- end }}
{{- dict "libraries" $prepared | toYaml }}
{{- end }}

{{/*
Every watched resource the install ends up with, as YAML — the union across all
enabled libraries plus the operator's own `controller.config.watchedResources`,
with the operator winning. Retains the Helm-only `statusPatch` field, which the
CR emitter strips but RBAC needs.

Callers are the templates that must reason about the whole watch set rather
than one library's: the ClusterRole (one get/list/watch rule per resource, plus
a status/patch rule per `statusPatch: true`) and the ValidatingWebhookConfiguration
(one rule per `enableValidationWebhook: true`).
Args: root context.
*/}}
{{- define "haptic.watchedResourcesUnion" -}}
{{- $union := dict }}
{{- range $library := (include "haptic.prepareLibraries" . | fromYaml).libraries | default list }}
  {{- $union = mustMergeOverwrite $union (deepCopy ($library.config.watchedResources | default dict)) }}
{{- end }}
{{- $union = mustMergeOverwrite $union (deepCopy (.Values.controller.config.watchedResources | default dict)) }}
{{- $union | toYaml }}
{{- end }}

{{/*
Strip Scriggo-template comments from a library's templateSnippets, in place.

Comments document each snippet for chart authors but contribute nothing to the
rendered HAProxy config — Scriggo strips them at template-render time. Their
unstripped source would still ship in the deployed HAProxyTemplateConfig, where
it is pure overhead against two size ceilings (etcd's ~1.5 MiB per object and
the 1 MiB Helm release Secret). Library source files are unchanged — chart
authors still see verbose inline documentation.

Three patterns, in order:

  1. Leading {#- ... -#} block at the very start of the template
     (top-of-snippet doc header). Anchored on \A.

  2. Stand-alone {# ... #} block on its own line (mid-template
     documentation). Required to be on its own line so we don't remove
     inline `{#- something -#}` whitespace-control markers that share a
     line with rendered content.

  3. Stand-alone Go-style `// ...` line comments inside Scriggo template
     directives. These appear inside {%- ... -%} or {%% ... %%} blocks
     where Scriggo accepts Go syntax. Same stand-alone-line constraint to
     avoid touching // chars that might appear in rendered text (URLs,
     config values).

All three patterns require their match to occupy a whole line (preceded by \n +
whitespace, followed by \n) so removing the line collapses the source without
changing the surrounding formatting.
Args: the library dict (mutated in place).
*/}}
{{- define "haptic.stripSnippetComments" -}}
{{- $leadingDocComment := "(?s)\\A\\s*\\{#.*?#\\}\\s*\\n?" }}
{{- $standaloneBlockComment := "(?ms)^[ \\t]*\\{#.*?#\\}[ \\t]*\\n" }}
{{- $standaloneGoComment := "(?m)^[ \\t]*//[^\\n]*\\n" }}
{{- range $name, $snippet := (.templateSnippets | default dict) }}
  {{- $tpl := $snippet.template | default "" }}
  {{- $tpl = regexReplaceAll $leadingDocComment $tpl "" }}
  {{- $tpl = regexReplaceAll $standaloneBlockComment $tpl "" }}
  {{- $tpl = regexReplaceAll $standaloneGoComment $tpl "" }}
  {{- $_ := set $snippet "template" $tpl }}
{{- end }}
{{- end }}
