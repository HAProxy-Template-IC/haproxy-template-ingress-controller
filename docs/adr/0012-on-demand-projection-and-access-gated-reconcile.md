# ADR-0012: `store: on-demand` — informer-body projection (accept) + access-gated reconcile (defer)

## Status

Partially implemented. **Mechanism 1 (informer-body projection) has shipped** — `pkg/k8s/watcher/projection.go` installs the transform via `SharedIndexInformer.SetTransform` for CachedStore-backed kinds. **Mechanism 2 (access-gated reconcile) remains deferred and unimplemented.**

Originally proposed, after three adversarial reviews + source verification (workflow
`wf_ef74052e-50c`, 2026-06-04, branch `feat/feature-gap-implementation`).
Improves the **existing** on-demand watcher (`store: on-demand` → `CachedStore`);
introduces **no** new store type.

- **Mechanism 1 — informer-body projection: ACCEPT** (on-demand kinds only).
  *Maintainer decision 2026-06-04: implement this now.*
- **Mechanism 2 — access-gated reconcile: DEFER / do not ship as first cut.**
  Achievable, but heavier and lower-value than it looked, and all three reviews
  recommend against it for the near-term workload. *Maintainer decision
  2026-06-04: deferred until profiling shows irrelevant-on-demand-change render
  CPU is an actual bottleneck.* Recorded here in full so the decision is
  documented, not re-litigated.
- **Chart default `configmaps: store: on-demand` (the watcher audit's
  conclusion): DEFER to a follow-up.** The Go projection ships independently and
  immediately benefits `secrets` (on-demand by default). Flipping `configmaps`
  to on-demand is correct (cluster-wide watch read only by `GetSingle` for
  BackendTLSPolicy CA refs, never `List()`), but the rendered chart already sits
  at the ~1 MiB Helm-release-Secret ceiling and even this one-line addition tips
  `helm install` over the limit in e2e/conformance. It needs a chart-size
  headroom fix first (the 519 KB validationTests are double-counted in the
  release Secret — chart source + rendered manifest). Tracked separately.

## Context

`store: on-demand` exists for one workload: a watched kind with **many large
items where the controller only reads a few by name** — the canonical case is
TLS Secrets (`charts/haptic/libraries/ssl.yaml:15`, opt-in via
`controller.config.watchedResources.secrets.store: on-demand`,
`charts/haptic/values.yaml:488`). All 29 secret accesses in the bundled chart go
through `resources.secrets.GetSingle(namespace, name)` — full-key lookups,
never `.List()` (verified).

Two maintainer goals:

1. **(memory)** Do not hold all watched objects' full bodies in memory.
2. **(render-suppression)** Do not re-render on a change to an index key no
   recent render accessed — "the actual expensive operation."

What on-demand does **today** (code-grounded):

- `createInformer()` runs unconditionally for every store type
  (`watcher.go:182`), so on-demand runs a full list+watch `SharedIndexInformer`
  over the whole kind. **The client-go informer keeps a full copy of every
  object body in its own internal cache regardless of store type** — there is no
  `SetTransform` anywhere in `pkg/k8s`. *This is the dominant memory cost.*
- `CachedStore` itself already keeps only `refs` (ns+name+indexKeys per object —
  tiny) plus a bounded LRU of full bodies (`DefaultMaxCacheSize = 256`,
  `cached.go:58`; `MaxCacheSize` never overridden by the watcher). On a render
  miss it does a live API GET (`fetchResourceByRef`, `cached.go:328`). So **the
  body memory the projection should target is the informer cache, not
  `CachedStore`** — `CachedStore` already discards bodies.
- The informer fires `OnChange` for every change → a full reconcile, regardless
  of whether any render referenced that object.

### Hard constraint — arbitrary `indexBy` genericity (non-negotiable)

`indexBy` is a list of **arbitrary JSONPath expressions** (`types.go:338`). The
maintainer plans to extend it (namespace+label indexing for Pods). No design may
restrict keys to name/label/"apiserver-selectable". The apiserver can filter
LIST/WATCH only by name, labels, and a per-resource field-selector whitelist —
never arbitrary spec/status JSONPath — so "scope the watch server-side to
recently-accessed keys" is impossible in general and is **not** attempted. We
keep watching the whole kind; we reduce what we *store* (Mechanism 1) and,
optionally, what we *render on* (Mechanism 2).

## Mechanism 1 — informer-body projection (ACCEPT)

When `StoreType == Cached`, install a `cache.TransformFunc` on the informer
(`SharedIndexInformer.SetTransform`, in `createInformer` before `informer.Run`).
The transform **projects** each object to the fields HAPTIC needs before
client-go stores it in the informer cache:

- `apiVersion`, `kind`, the entire `metadata` block (identity + labels/
  annotations that `indexBy`/field-selectors commonly read), and
- the **top-level field named by the first segment** of every `indexBy` and
  every `fieldSelector` expression for this watcher.

Everything else is dropped — for Secrets, `data`/`stringData` (the certificate
bytes, the whole weight). The rule is **generic and config-driven** (reads only
`cfg.IndexBy`/`cfg.FieldSelector`, never a resource kind) and **conservative**
(retains whole top-level blocks rather than subtree-trimming arbitrary
JSONPath), so it can never drop a field that indexing, field-selection, or
identity needs. RULE #1 holds.

**Why this is safe on the on-demand path — and only there.** The render never
reads an on-demand body from the informer or the store cache: `GetSingle`/`Fetch`
resolve through `CachedStore.Get` → `fetchResourceByRef` → **live API GET of the
full, un-projected object** (`cached.go:347-365`). So the informer's projected
copy is never template-visible. **This reasoning fails for MemoryStore** (the
stored body *is* what templates read), so projection is applied **only to
CachedStore-backed kinds**. This directly answers the cost review's blocker B1
("you can't discard the body and still emit a body-bearing `ConvertedResource`"):
on the cached path the `ConvertedResource` body is never template-read, so a
slim one is fine.

**Write-path cache change (resolves B3 — serving husks).** Today
`Add`/`Update` opportunistically cache the informer-delivered body in the LRU
(`cached.go:251,283`); the LazySnapshot warm-prime then serves it to templates
(`store_wrapper.go:189`). Under projection that body is incomplete, so a
projected `CachedStore`:

- does **not** populate the LRU from `Add`/`Update`, and
- on `Update`/`Delete` **invalidates** any stale LRU entry for that ns/name,

making the LRU purely read-through, populated only by `fetchResourceByRef` (full
live body). `ListCached()` then only ever returns full bodies (or nothing, →
on-demand fetch), so the warm-prime never serves a husk. Cost: the first render
read of a key after a change pays one live GET instead of reusing a free
informer body — negligible for an infrequently-accessed kind (the definition of
on-demand).

**Accepted caveats** (documented, not blocking):

- `logUpdateContent` (`watcher_handlers.go:305`) DEBUG forensic dump shows
  projected husks for on-demand kinds. Secrets are not the rolling-restart hot
  path (that's EndpointSlices, `store: full`, unprojected), so the
  rolling-restart forensic value is unaffected.
- `statecache_providers.GetResourcesByType` introspection already returns only
  the warm subset for on-demand (documented); under invalidate-on-write those
  are full bodies (live-GET-cached) or absent — no husks.

**Honest scope of the win.** The win is eliminating the informer's full-body
retention for on-demand kinds — real and exactly goal (1) for the "many large
Secrets" use case, but it does **not** reduce per-event network/decode/convert
cost (the transform runs *after* decode). Projection is a steady-state-heap
lever, not a throughput lever; do not extend on-demand to iteration-heavy kinds
(a 10k-object kind against a 256-entry LRU would cause render-time live-GET
storms — keep those on `store: full` + `IgnoreFields`).

### Implementation surface (Mechanism 1)

| File | Change |
|------|--------|
| `pkg/k8s/types/types.go` | `WatcherConfig`: nothing new required — projection roots derive from existing `IndexBy` + `FieldSelector`. |
| `pkg/k8s/watcher/watcher.go` `createInformer` | Build a projection `TransformFunc` from `IndexBy`+`FieldSelector` roots; `informer.SetTransform(...)` before `Run`. Only when `StoreType == Cached`. |
| `pkg/k8s/store/cached.go` | A `projected` flag; under it, `Add`/`Update` skip `cacheResource` and `Update`/`Delete` invalidate the LRU. |
| `pkg/controller/resourcewatcher/watcher.go` | No change (StoreType already drives the cached branch). |

Verification (TDD): projection retains indexBy + field-selector + identity
fields; drops a heavy non-indexed field; key extraction + field-selector eval
still pass on the projected object; the render read returns the **full** body
(live GET); `ListCached`/warm-prime never serves a husk.

## Mechanism 2 — access-gated reconcile (DEFER)

The idea: `CachedStore` change handlers consult a recently-accessed-key set and
suppress the reconcile-trigger when a change touches no recently-accessed key;
the index is always updated, only the *render* is gated.

The scrutiny established this is **achievable without a new store type but
materially heavier and lower-value than the sketch**, and recommends not
shipping it first. Recorded requirements so it isn't re-discovered:

**Where it must live.** Not in `CachedStore.Get` — warm reads served from the
LazySnapshot `ListCached()` prime bypass `Get` entirely
(`store_wrapper.go:183-196`), so keys read *every* render would silently age out
and get wrongly suppressed (the decisive flaw). The accessed-set must live
**controller-side**, recorded by `StoreWrapper` on **all** read paths (snapshot
hit, lazy `Store.Get` including misses, and the prime fold), and consulted by the
watcher via a new generic `WatcherConfig.ShouldReconcile(...)` hook **per-event,
upstream of the debouncer** (the debouncer coalesces changes into a keyless
`ChangeStats`, so the reconcile-trigger layer has no key to test).

**Correctness preconditions (S1–S6), all confirmed against code:**

- **S1 — whole-kind relevance for `List()`-consumed kinds.** `List()` carries no
  key; restricting the gate to keyed-lookup kinds (CachedStore only) is the only
  survivable scope. (Routing resources are `store: full` → ungated → unaffected.)
- **S2 — record on miss.** A `GetSingle` miss must arm the key, so a
  referenced-before-created Secret (default GitOps apply ordering) re-fires on
  creation. The miss path does reach `Store.Get` (`store_wrapper.go:336`).
- **S3 — per-key version epoching, not boolean membership.** The accessed-set is
  populated *during* a render; a change racing an in-flight first-reference
  render is otherwise dropped with no follow-up. Closing it needs comparing the
  change's `rv` against the `rv` the last render *read* — heavier than a TTL set.
  Without S3, this case is bounded by drift (≤ interval).
- **S4 — record warm-prime reads.** (The placement point above.)
- **S5 — full-key normalization** (expand partial-key/prefix accesses) so
  partial-key-indexed kinds don't miss adds. Handled by prefix expansion at gate
  time; moot for the current config (the partial-key hot kind, EndpointSlices,
  is `store: full`).
- **S6 — OLD-key reliability on tombstoned delete.** Safe for immutable indexBy
  (ns/name); arbitrary *mutable*-field indexBy can carry a stale tombstone key.
  Documented caveat.

**The invariant that makes it sound for the maintainer's stated model.** The
maintainer pre-accepted the semantics: *"if we referenced a secret during the
last render, changes are immediate; if not, the secret was not relevant anyway;
delayed convergence is no problem; it's important that the index of existence
stays up to date."* This holds **iff**: every render arms every referenced key
(true — a render reads all referenced secrets); renders happen ≤ every drift
interval D (drift mandatory); access-TTL ≥ 2D (reuse the existing
`cacheTTL = 2.2D`, `resourcewatcher/watcher.go:147`); and accesses are recorded
on the wrapper (S4) including misses (S2). Under those, a change to any
currently-referenced key is never suppressed, the index of existence is always
fresh (refs updated on every change), and only genuinely-unreferenced-key
changes are suppressed — exactly the maintainer's model. Branch transitions that
*change* which key is relevant are driven by `store: full` resources
(Ingress/Gateway/HTTPRoute/BackendTLSPolicy/annotations), whose changes are
ungated and re-arm the newly-relevant key immediately. The only residual gap is
S3's in-flight race (a secret mutated in the ms-window of the very render that
first references it), bounded by drift — consistent with the drift backstop the
system already relies on.

**Why DEFER despite being achievable.** The value is **modest and already
bounded**: an irrelevant on-demand change cannot cause a deploy (the render diff
is empty → no reload, no etcd write), and the debounce (2s leading-edge) +
`minDeploymentInterval` (2s) already cap render rate at ~1/2s regardless. The
gate only removes wasted render+diff **CPU** for irrelevant on-demand changes —
real for high-churn-many-Secrets clusters, but not a correctness or convergence
win. Against that modest, bounded benefit stands a cross-layer change
(`store_wrapper` recording on three paths + a new `WatcherConfig` hook +
per-event gate + OLD-key extraction in `processUpdate`) with correctness
subtleties (S3 epoching for full soundness). All three adversarial reviews
recommend against shipping it as the first cut.

**Recommendation:** ship Mechanism 1 now (delivers goal 1 cleanly). Reconsider
Mechanism 2 only if profiling shows irrelevant-on-demand-change render CPU is an
actual bottleneck on a real deployment — at which point implement it per S1–S6
above (controller-side accessed-set, per-event gate, drift mandatory for
on-demand).

## Alternatives considered (and rejected)

- **Scope the watch to recently-accessed keys (server-side):** impossible for
  arbitrary `indexBy`; unsound (missed-change race). Sacrifices the one hard
  constraint.
- **Name/label-keyed only:** same genericity sacrifice; rejected by the
  maintainer.
- **Key-projection that discards the body for MemoryStore kinds:** breaks
  templates (body is the store) — blocker B1. Mechanism 1 is cached-path-only
  precisely to avoid this.

## Open items to verify while implementing Mechanism 1

1. `client-go@v0.36.1`: `SetTransform` must be installed before `Run` (it is, in
   `createInformer`); confirm the transform is per-kind (one informer per GVR via
   `ForResource(GVR).Informer()` — it is, `watcher.go:220`).
2. Confirm the projection field-set is sufficient for every shipped on-demand
   watcher's `indexBy`/`fieldSelector` (today: Secrets, ns/name, no field
   selector).
3. Render-equivalence: a `validationTest` (or store unit test) asserting the
   rendered output is byte-identical with/without the transform for the
   Secrets-on-demand path.
