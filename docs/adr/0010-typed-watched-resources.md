# ADR-0010: Typed top-level globals for watched resources

## Status

Accepted. Implementation: Tier-2 typed-watched-resources work merged in
`feat: typed watched resources — Tier 2 (full stack)`
(commit `17777895`); embedded-builtins offline source removed by
`chore: drop builtin schemas, require --schema-dir for offline typed access`
(this ADR).

## Context

Templates have always reached into watched Kubernetes resources via an
untyped `dig()` chain:

```scriggo
{%- for _, gw := range resources.gateways.List() %}
  {{ dig(gw, "metadata", "namespace") }}/{{ dig(gw, "metadata", "name") }}
  {%- for _, l := range dig(gw, "spec", "listeners") | toSlice() %}
    ...
  {%- end %}
{%- end %}
```

Three failure modes that this shape exhibits cost real time:

1. **Typos at the field-name level only surface at render time.** A
   misspelled `dig(gw, "metaadata", "namespace")` is valid Scriggo
   syntax; the chart compiles, the controller runs, and the typo
   silently returns `nil`. Symptoms downstream (a backend named
   `__svc_foo` instead of `default_svc_foo`, an empty host.map row,
   …) are removed enough from the source that operators have lost
   hours triangulating which field was wrong.
2. **Cross-field-type mistakes are invisible until rendered.** `dig`
   returns `any`; `gw | dig("spec", "listeners") | toint()` is a
   chart-syntactic no-op that returns 0 at render time. The chart
   carries this kind of bug across many rendered configs before
   someone notices.
3. **No discoverability inside chart-side LSP / editor tooling.**
   `dig()` is a string-keyed lookup; nothing tells an author what
   keys are valid for which kind. Authors copy from existing
   snippets and hope.

A typed top-level global per watched resource fixes all three at chart
*engine compile time*. The same Scriggo engine that already validates
the chart's helper macros can validate `gw.Metadata.Namespace`,
because the typed declaration carries the field set.

## Decision

The renderer declares one typed top-level global per `watchedResources`
entry, named the same as the key. The typed shape is a
`*[]*<GeneratedStruct>` produced by `pkg/k8s/typegen` from the
resource's OpenAPI v3 schema.

```scriggo
{#- gateways is *[]*<generated Gateway>; iterating yields *Gateway -#}
{%- for _, gw := range gateways %}
  # {{ gw.Metadata.Namespace }}/{{ gw.Metadata.Name }}
{%- end %}
```

The existing untyped `resources["<name>"]` path keeps working
unchanged. `dig()` is enhanced to navigate typed structs by JSON tag
so chart code that mixes both shapes (typed iteration, untyped helper
macros) operates on the same field-name conventions.

### Where schemas come from

- **Production:** the controller fetches schemas live from the
  kube-apiserver. CRDs via their embedded `openAPIV3Schema`, K8s core
  resources via the apiserver's OpenAPI v3 endpoint. There is no
  embedded fallback in the binary — if the apiserver call fails for a
  watched resource, that resource declares as the universal envelope
  shape (`Metadata.{Name, Namespace, Labels, Annotations}` typed; no
  Spec/Status), and templates reaching deeper fail at engine compile
  time with a clear "no schema for X" pointer.

- **Offline (`controller validate`, chart `validationTests`,
  `scripts/test-templates.sh`):** schemas come from `--schema-dir` or
  `HAPTIC_SCHEMA_DIR`. The directory accepts full CRD YAMLs (the wire
  form `kubectl get crd X -o yaml` produces) and bare OpenAPI v3
  spec.Schema files with an `x-kubernetes-group-version-kind`
  extension. Without `--schema-dir`, no resources receive typed
  support; chart code that uses only the untyped `resources` path
  validates fine.

- **Repo's `tests/schemas/` bundle:** the canonical schema-dir for the
  chart's bundled libraries (Gateway API CRDs + haptic CRDs). The
  chart-test script auto-wires it; projects with their own typed
  watched resources copy this bundle and extend it.

### Field-name convention

`pkg/k8s/typegen/converter.go::goFieldName` is the canonical rule:
Go-PascalCase of the JSON tag, with NO acronym preservation, and
non-letter/digit characters mapped to `_`.

| JSON tag (source YAML) | Typed field        |
|------------------------|--------------------|
| `metadata`             | `Metadata`         |
| `apiVersion`           | `ApiVersion`       |
| `tls`                  | `Tls`              |
| `ingressClassName`     | `IngressClassName` |
| `kubernetes.io/foo`    | `Kubernetes_io_foo` |

No acronym dictionary is deliberate. K8s API conventions name things
inconsistently (`apiVersion` vs `httpHeader` vs `IPBlock`), and
maintaining a dictionary that mirrors the upstream conventions for
every release is a cost we don't want to pay. The cost on the chart
side is one extra rule for template authors to internalise; the win is
zero ongoing maintenance.

## Alternatives considered

### Alternative 1: untyped only (status quo before Tier 2)

Keep `dig()`. Accept the three failure modes from the Context section.

Rejected because the failure modes are real and have cost real
operator time. The cost of adding the typed path is bounded (one
package, one engine declaration point, no impact on chart code that
doesn't opt in); the cost of the existing untyped-only world is
unbounded over time as the chart grows.

### Alternative 2: typed-only

Force all chart code to use the typed shape. Drop the untyped path.

Rejected because:

- Helper macros that span multiple resource types (e.g. a macro that
  takes `any` and dispatches on `kind`) lose their natural shape.
- `Fetch(...)` / `GetSingle(...)` index-keyed lookups don't map
  naturally to the typed slice — the typed global exposes only
  iteration.
- Mixed-shape chart code is the realistic adoption pattern (the
  chart's helper-macro signatures take `any` for a reason). Hard
  cutover would break every macro and force a tree-wide rewrite that
  doesn't carry its weight.

### Alternative 3: embedded schemas + schema-dir overlay

The Tier-2 implementation initially shipped two offline schema
sources: an embedded `pkg/k8s/schemafetcher/builtin/` directory in the
controller binary, overlaid by user-supplied `--schema-dir`. Builtins
filled the gaps when no `--schema-dir` was provided so the validate
CLI worked out of the box for the chart's Gateway-API path.

Rejected (and removed) because:

- Duplication: the schemas in `builtin/` were trimmed copies of files
  already sitting in `tests/schemas/`. Every CRD bump required
  updating both places.
- Two offline code paths to test. The Overlay logic was correct, but
  it existed to paper over the single missing `--schema-dir` argument
  in the `controller validate` invocation.
- Adoption cliff was illusory: chart authors running `controller
  validate` against a non-trivial chart config already need
  `--schema-dir` for any resource beyond Gateway, so making it
  required for any typed access doesn't change the operator
  experience for serious users — it only removes the "looks like it
  works for one specific resource" misleading default.

The replacement is the simpler two-path model in this ADR's Decision
section: live in production, `--schema-dir` offline, no embedded
defaults.

### Alternative 4: reflect from k8s.io/api Go types

Build typed shapes by reflecting on the bundled Go types from
`k8s.io/api` and `sigs.k8s.io/gateway-api` rather than from OpenAPI
schemas.

Rejected because:

- Pulls every API package into the controller binary, adding tens of
  megabytes of compiled code for resources the chart may or may not
  touch.
- Shape changes with every Go-side rename / restructure, even when
  the wire schema is stable. Two binaries of the same controller
  version on different Go SDK versions could disagree on field
  names.
- Doesn't extend naturally to operator-defined CRDs (the whole point
  of dynamic typing is that operators add their own CRDs and chart
  code follows). The OpenAPI v3 path handles operator CRDs and
  upstream resources identically.

## Consequences

### Positive

- Field-name typos in chart templates fail at the right place
  (engine boot / validate-CLI) instead of at the wrong place (next
  reconcile, no error, silently wrong output).
- Chart authors get LSP-style discoverability via Scriggo's
  type-checker against the schema-derived struct.
- The same chart code can be developed with `scripts/test-templates.sh`
  offline and runs unchanged in production — both paths produce
  identical typed declarations because both fetch from the same
  schema shape.
- The `dig()` path stays a first-class supported approach for the
  cases it's better suited for (helper macros over `any`,
  cross-kind generic traversal). Adoption is incremental: convert
  snippets one by one as it makes sense.

### Negative

- One more configuration knob for the offline path: chart authors
  running validate against a config that uses typed access need to
  supply `--schema-dir` (or rely on `tests/schemas/` via the
  auto-wired chart-test script). The "no schemas, fall back to
  envelope" graceful path keeps the no-typed-access case working
  without the flag.
- `pkg/k8s/typegen` is a new abstraction surface the codebase must
  maintain. The OpenAPI-v3 → reflect.Type conversion has subtleties
  (allOf merging, $ref dereferencing, x-kubernetes-int-or-string,
  …) that are easy to get wrong on edge-case schemas; the package's
  tests pin the cases we care about.
- The field-name convention (PascalCase, no acronym preservation)
  diverges from how Go-side K8s authors typically write field names
  in code. Chart authors writing `gw.ApiVersion` once and reading
  `obj.APIVersion` in upstream Go source is a one-time adjustment;
  documented in `docs/controller/docs/templating.md` and the chart
  CLAUDE.md.

## Do not re-suggest

If you find yourself proposing to "re-add embedded schemas for the
validate CLI's convenience": don't. The previous mention of
`pkg/k8s/schemafetcher/builtin/` was removed because the
`--schema-dir` flag plus `tests/schemas/` as a documented bundle
covers the same use case without two parallel code paths to
maintain. If the convenience argument resurfaces, ship a one-line
shell wrapper that defaults `--schema-dir=tests/schemas/` (the
chart-test script already does this) rather than embedding bytes
into the controller binary.

If you find yourself proposing a global acronym dictionary so
`gw.APIVersion` works instead of `gw.ApiVersion`: don't. The
no-dictionary rule is intentional. If the chart-side ergonomics
become a real friction point, prefer renaming the JSON tag upstream
(`apiVersion` is unfortunate but stable) over carrying a translation
table in this repo.

If you find yourself proposing to drop the untyped `dig()` path
entirely: don't. The mixed-shape adoption pattern is the realistic
one for a chart that already has hundreds of dig() call sites in
helper macros over `any`. Forced cutover doesn't carry its weight
(see Alternative 2).

## Related

- `docs/controller/docs/templating.md#typed-top-level-globals` —
  chart-author-facing reference for the field-name convention,
  schema sources, and worked example.
- `docs/controller/docs/watching-resources.md#typed-access-in-templates`
  — brief mention with a pointer to the templating reference.
- `charts/CLAUDE.md` — chart-side dev context with the same guidance
  duplicated for chart contributors.
- `charts/haptic/libraries/gateway/05-typed-access-smoke.yaml` — the
  canonical worked example, with its companion test
  `test-gateway-typed-access-smoke` as the wiring regression canary.
- ADR-0004 (no typed registry over `globalFeatures`) is the parallel
  decision in the opposite direction for cross-library shared state:
  there the failure modes are bounded (~4 keys, no observed bug) and
  the registry adds machinery for risks that haven't materialised.
  For watched resources the failure modes ARE bounded by the count
  of dig() call sites (798 today) and HAVE materialised (typo
  incidents lost to debugging time); the trade is worth taking here.
