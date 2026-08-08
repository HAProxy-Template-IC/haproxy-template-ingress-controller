# ADR-0017: Library content moves to a HAProxyTemplateLibrary kind

## Status

Accepted 2026-08-08. Supersedes [ADR-0016](0016-one-config-kind-many-instances-pre-rollout-validation.md)'s
`spec.partial` + ordered `CRD_NAME` mechanism. ADR-0016 never shipped in a
release: `spec.partial` landed in `30ce6540`, after `v0.1.0`, and is absent from
`v0.1.0`'s CRD. Only snapshot consumers of `main` are affected.

## Context

ADR-0016 split the configuration across N `HAProxyTemplateConfig` objects, each
marked `spec.partial`, merged in `CRD_NAME` order. It solved the size problem
and created three others.

**The completeness gate latched.** `configloader.record()` asked whether every
configured name had been seen *at least once*. Once that flipped, a later
single-object event re-merged against the stale copies it still held, so a torn
apply rendered a mixed set with no signal. Libraries deliberately override one
another, so a missing member changes behaviour rather than removing it — a
config missing its WAF library renders successfully and serves traffic unarmed.

**Merge order was stated three times.** `CRD_NAME` on the Deployment, the set of
objects actually applied, and the chart's `prepareLibraries` evaluation all had
to agree. Nothing enforced that they did.

**`spec.partial` waived the completeness rule for every object.** Because eight
of nine chart objects are fragments, the CRD's CEL rule had to exempt them all,
so the apiserver could judge completeness for nothing the chart installs.

## Measurement

Rendered chart defaults, 9 objects, 1.96 MB of spec:

| spec field | share |
|---|---:|
| `templateSnippets` | 62.1% |
| `validationTests` | 31.8% |
| everything else | 6.1% |

94% of the bulk is two fields. Moving content out leaves ~120 KB — one object,
with 92% of the etcd budget spare.

## Decision

**A new `HAProxyTemplateLibrary` kind carries template library content.** One
`HAProxyTemplateConfig` references them through an ordered `spec.libraryRefs`.

It is named for the capability, not for one of its fields: a library defines
`templateSnippets`, `validationTests`, `maps`, `files`, `sslCertificates`,
`k8sResources`, `templatingSettings` and `haproxyConfig` — measured on the
bundled chart, seven of the eight are in use by at least one library and four
by four or more. "Template library" is also the term the chart already uses
(`controller.templateLibraries.*`, `haptic.prepareLibraries`, the
`template-library` component label), so the CRD and the values now read as one
idea.

```yaml
spec:
  libraryRefs:
    - {name: haptic-config-base,    revision: "base-43dc4467f7e88090"}
    - {name: haptic-config-gateway, revision: "gateway-5da793f017afc1c5"}
```

**Libraries carry content only** — no `podSelector`, `watchedResources`,
`dataplane`, `validators`, `controller` or `logging`. A library cannot redefine
the controller's operational identity. `templatingSettings` *is* carried, since
libraries ship template-context defaults and the config merges last, so an
operator always wins.

The `watchedResources` union lands on the config, via the pre-existing
`haptic.watchedResourcesUnion` that the ClusterRole and the webhook already
consume.

**The revision is compared, never recomputed.** The writer stamps the same
string on both sides in one apply; the controller only ever compares them.

| case | content | stamp | outcome |
|---|---|---|---|
| `kubectl edit` a snippet | changed | unchanged | matches → renders the edit |
| torn apply | mixed | mismatched | holds last-good |
| writer rewrites both | changed | changed | matches → renders |

Verifying a content hash would break row 1, which is the experimentation case
this design exists to protect. It would also reintroduce a failure mode that
cost a previous attempt dearly: the two sides hash different bytes once the
apiserver prunes, defaults, or reorders fields.

A Helm release counter is not usable — `.Release.Revision` is always `1` under
`helm template | kubectl apply` and under Argo CD, so it would silently never
change for a large share of users. A content digest changes exactly when
content does, under every delivery method, which is why the chart uses one as
the *source* of an otherwise opaque string.

**Merge order is declared once**, in `spec.libraryRefs`. `CRD_NAME` returns to a
single name.

**The CEL rule gets stronger, not weaker.** `podSelector` and
`watchedResources` are now required unconditionally, because nothing else can
supply them. `haproxyConfig` is required unless `libraryRefs` names something
that can.

**The controller stamps an `ownerReference`** from the config onto each library
it references, so Argo CD and `kubectl tree` show the relationship and
`helm uninstall` cannot strand the content objects. The chart cannot do this —
an ownerReference needs the owner's UID, which does not exist until the config
is applied. It is best-effort: a failure logs and never blocks a valid
configuration from loading.

## Consequences

**Per-object size stops being a ceiling.** Nothing binds one library to one
object: a library approaching the limit is partitioned by key across
`…-gateway-1`, `…-gateway-2`, and the parts merge identically because their keys
are disjoint. This is a mechanism any config author has, not a vendor
privilege — the same test ADR-0014 applied.

**Document order is no longer merge order.** Helm sorts rendered manifests by
kind, so a `helm template` stream lists every `HAProxyTemplateConfig` before any
`HAProxyTemplateLibrary` — the reverse. `conversion.AssembleSources` orders by
`libraryRefs` instead, and `validate -f` and the pre-rollout hook use it.

**The `_global` accumulation and duplicate-name rules are unchanged.** The
"only the last source may override" exemption now lands exactly on the object an
operator edits, because assembly appends the config after its snippets — the
override point stopped being a positional accident.

**The Helm release Secret is untouched by this.** It holds the rendered
manifest, so object count is irrelevant to it. It sits at 74.1% (default) /
85.1% (all libraries) of the 1 MiB limit and is gated by
`make chart-size-check` at 950,000 bytes. Splitting into separate Helm releases
per library was considered and rejected on 2026-08-08: it costs a lot of
convenience and is not needed yet.

## Verification

- `make test` 6,563 tests; five loader tests cover missing ref, revision
  mismatch, in-place edit still renders, delete holds, config overrides snippets
- `make lint` 0 findings, arch-go compliant
- `make lint-chart` 428 chart tests
- rendered defaults: config 20,143 B (1.3% of the etcd limit), 8 snippets
  objects, largest `gateway` at 53%, zero operational fields on any of them
- `validate -f` assembles the real `helm template` stream despite Helm's
  kind-sorted document order
