# Design: Browser WASM Template Playground

## Context

HAPTIC renders `HAProxyTemplateConfig` templates against watched Kubernetes resources to produce HAProxy configuration. The render is already factored around a shared core: production (`pkg/controller/renderer/service.go` — `RenderService.Render`), the `controller validate` CLI (`pkg/controller/testrunner`), and any other caller all go through `rendercontext.NewBuilder(...).Build()` + `engine.Render`. The only thing that varies between production and the offline paths is the **store source** (live informers vs in-memory fixtures) and the HAProxy **capabilities** (detected vs supplied).

The render graph compiles to `GOOS=js GOARCH=wasm` with standard Go 1.26 and has been verified to render a real config in WASM-under-Node. TinyGo is precluded because `pkg/k8s/typegen` builds resource types at runtime via `reflect.StructOf`. The only runtime-hostile calls on the path are `dataplane.DetectLocalVersion()` (execs `haproxy`) and `schemafetcher.DirFetcher` (reads disk); both have drop-in offline replacements. The docs site is Material for MkDocs (`docs/landing/`) with per-version publishing.

Measured size through the real production entrypoint is ~57 MB raw / **~7.09 MB brotli** (an earlier 4.2 MB figure came from a probe that bypassed `rendercontext.Builder` and is not representative).

## Goals / Non-Goals

**Goals:**

- Guarantee, by construction, that playground output equals controller output at the selected release (zero drift).
- Maximize reuse of production code; add no parallel render logic.
- Make "what changes if I edit this template?" and "what happens if I create this resource?" instantly answerable with live, attributed feedback.
- Ship a clean, engaging UI on the docs site with no server-side rendering.
- Keep transfer size manageable via a production dependency trim + brotli + per-version caching.

**Non-Goals:**

- Deploying the rendered config to a real HAProxy or validating it with the `haproxy` binary (rendering only).
- **In-browser HAProxy config validation.** Investigated (the wasm already ships client-native): `dataplane.ValidateSyntaxAndSchema` was wired up and tested, but client-native's in-memory validation is far too lenient to be useful — its parser round-trips arbitrary text and reports "valid" even for junk input (`"this is not haproxy config"` → valid), and its schema pass only covers backend/frontend models (misses `weight 999999`, bogus balance algorithms, `global`/`defaults` errors). Meaningful validation needs `haproxy -c` (Phase 2), which requires the binary and can't run in wasm. A "valid ✓" badge on broken configs would give false confidence, so it was reverted. Template-authoring mistakes surface instead as template-compile and render errors (which the pipeline already returns).
- Reimplementing `haptic.mergeLibraries` client-side.
- Arbitrary 2^N library toggling in v1 (deferred to an optional Helm-in-WASM v2 module).
- A general Kubernetes YAML playground; scope is HAPTIC template rendering.

## Decisions

### D1. Call the production `RenderService.Render`, not `testrunner.RenderFixtures`

The WASM builds an in-memory `stores.StoreProvider` from the user's resources and calls `renderer.RenderService.Render(ctx, provider)` — the exact function the leader pipeline calls (`pkg/controller/pipeline/pipeline.go`). This is the spine of the zero-drift guarantee: the only inputs differing from production are the store source and capabilities, both of which production also parameterizes.

*Alternatives considered:* `testrunner.RenderFixtures` — rejected: it is the `controller validate` path with a **parallel** aux/k8s/status orchestration (`testrunner/rendering.go`), which is both a drift surface and ~2× the WASM size. A new `RenderOnce(...)` wrapper — rejected: pure indirection with no drift-safety gain; the WASM makes the ~10 production calls directly.

### D2. Reuse `CreateStoresFromFixtures` for the store bridge

Resources arrive keyed by watched-resource user-name (the `validationTests.fixtures` shape). `testrunner.CreateStoresFromFixtures` creates an empty `store.NewMemoryStore` for every `WatchedResources` entry (so `.List()` is always safe) and indexes each object with the production indexer; `stores.NewRealStoreProvider` wraps the map; `SeparateHAProxyPodStore` peels the `haproxy-pods` self-watch. The bridge is not drift-sensitive (same indexer as production).

*Alternative:* a ~30-line inline store builder duplicating `fixtures.go` — kept as a fallback only if dead-code elimination fails to prune the assertion/`RunTests` weight that `CreateStoresFromFixtures`'s owning type drags in (measure with `-dumpdep` in Milestone 0).

### D3. Two version axes

- **HAProxy version (3.0–3.4)** reaches the engine only as config data — the capabilities map (`CapabilitiesFromVersion`) and `extraContext.haproxyVersion` (consumed by the generic `semver_gte` filter). So it is a **runtime dropdown** parameter to one module.
- **Controller/chart version** is different Go code + different bundled templates → a **different WASM per release**, lazy-loaded and cached, defaulting to newest stable (via the existing `stable-version.js` predicate).

This satisfies "version-specific playgrounds OR a selector" with the superset of both. Byte-parity per release is guaranteed because each module is built from that release's tag.

### D4. Prepopulation via CI-pre-rendered Helm presets (v1)

`haptic.mergeLibraries` does value-dependent injects/unsets, `tpl`-evaluated enable predicates, union-merged shared `watchedResources`, and order-sensitive passes — not safely decomposable client-side. CI runs real `helm template` per preset and ships the merged `HAProxyTemplateConfig` as a static asset; the UI offers a preset dropdown plus paste-your-own and the `byo-crd` minimal starter. An optional **Helm-in-WASM v2** module (the `chartrender.go` import set compiles to `js/wasm` at ~5.14 MB brotli) can later run the genuine loader for arbitrary toggles, lazily loaded only when requested.

### D5. Size: client-native severance — investigated and DEFERRED

The original plan assumed the offline render path could drop `haproxytech/client-native/v6` entirely (~7.09 → ~5.8 MB brotli). Implementation-time investigation disproved that premise:

- **`client-native/models` cannot be removed.** The bundled chart's slot-preservation macro does *typed* access — `charts/haptic/libraries/base.yaml`: `var bkServers = currentConfig.ServerIndex[bkName]` — which Scriggo type-checks against `parserconfig.StructuredConfig` at compile time (the `isNil(currentConfig)` guard is runtime-only). So the wasm must link `StructuredConfig` → `client-native/models` to *compile* the bundled chart. `parserconfig` alone pulls only 3 of the 30 client-native packages (`models`, `models/funcs`, `misc`); those are unavoidable.
- **The achievable cut is 27 of 30 packages** (the parser/comparator/validators/orchestrator machinery, keeping `models`), for a corrected floor of **~6 MB brotli, not 5.8**.
- **It is a multi-package refactor of correctness-critical deploy code, not a clean win.** The render-facing leaf value types are welded into heavy files: `dataplane.Version` is a type alias of `client.Version`; `validator.go` mixes `ValidationPaths` with parser-based validation; `version.go` mixes `ParseVersionString` with client-based `DetectLocalVersion`. Extracting them cleanly means splitting `pkg/dataplane` *and* `pkg/dataplane/client`, plus an interface seam for `currentconfigstore` and an inline store builder — perturbing the HAProxy sync path for a sub-1 MB win that still leaves the module in the "multiple MB" range.

**Decision: defer.** The effective size levers live in the packaging path (transport + caching, §Version Model / Milestone 5), not a deploy-code refactor. `wasm-opt -Oz` is also not applied (measured brotli +1%).

### D6. Warm-engine Web Worker for live feedback

Rendering runs in a Web Worker holding the compiled `RenderService` as worker-global state. `config` messages recompile the engine; `render`/`version` messages reuse the warm engine (compile is separable from render — `scriggo.BuildTemplate` vs `engine.Render`). Every reply carries the request `seq`; stale replies are dropped; single-flight keeps only the newest queued job. This maps 1:1 onto the two hero questions and keeps the UI thread free.

### D7. UI shell: vanilla JS + CodeMirror 6, no framework/bundler

CodeMirror 6 (YAML mode) is chosen for line decorations (the change-glow) and cheap multi-document switching, at ~300 KB over CDN (<10% of the WASM), no local build step. Output is a read-only styled `<pre>` with per-line spans and a worker-computed line-level diff against a pinned baseline. State is shared via `#s=` (`CompressionStream` + base64url, zero-dep) with a localStorage fallback and autosave.

## Risks / Trade-offs

- **~7 MB brotli is the module's realistic floor** (standard Go + Scriggo + apimachinery + the unavoidable `client-native/models`; measured 7.3 MB via the M0 entrypoint). Severing the rest of `client-native` was investigated and **deferred** (D5) — marginal, and it risks the deploy path. *Mitigation:* the cost is one-time — serve `.wasm.br` with `Content-Encoding: br` and cache the immutable, content-hashed per-version module (Milestone 5), so repeat visits and version switches pay nothing.
- **`CreateStoresFromFixtures` lives on `*testrunner.Runner`, potentially dragging assert/test weight into the WASM** → rely on Go function-level DCE; measure with `-dumpdep`; fall back to the inline builder (D2).
- **Pasted configs using typed access need a registered schema** → bundle core+haptic schema inline (~25 KB), lazy-load gateway (~40 KB) when a gateway kind is watched, and surface a clear "schema required" error otherwise.
- **`Content-Encoding: br` support on the pages host is unconfirmed** → prototype a `HEAD` check early; fall back to gzip or a tiny in-page brotli decompressor.
- **Zero-pod backends render empty server lines (unengaging demo)** → seed a fake `haproxy-pods` fixture in every preset (cosmetic, not drift).
- **`renderWithStores` remains a latent drift surface between `controller validate` and production** (independent of this change) → out of scope; flag for a future cleanup where `renderWithStores` delegates to `RenderService.Render`.

## Migration Plan

Purely additive to the controller and docs site; no runtime migration. The client-native severance (D5) is deferred, so no production Go changes ship with this playground work. Rollback is removing the `/playground/` page and the `build-playground-wasm` CI job; no persisted state or API surface is affected.

## Open Questions

- Confirm `Content-Encoding: br` behavior on the GitLab pages host before relying on it (fallback ready).
- Confirm DCE prunes the assertion path from `CreateStoresFromFixtures` (`-dumpdep`), else adopt the inline store builder.
- D5 severance is deferred. If module size ever becomes a hard blocker, revisit via full leaf-package extraction (no build-tag drift) — but only after confirming transport + caching (Milestone 5) are insufficient.
- Whether to ship the Helm-in-WASM v2 module at all, or leave arbitrary toggles to paste-your-own.
