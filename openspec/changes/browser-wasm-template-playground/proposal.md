# Proposal: Browser WASM Template Playground

## Why

HAPTIC's core value — template-driven, resource-agnostic HAProxy config generation — is only observable by running a full controller against a live cluster, a steep barrier for evaluation, template authoring, and debugging. A browser-hosted playground on the docs site lets anyone edit a `HAProxyTemplateConfig` and example Kubernetes resources and immediately see the exact `haproxy.cfg` the controller would deploy — no server, no cluster, no install. Because it compiles and calls the controller's **production** render path to WebAssembly, what users see is guaranteed identical to what the controller produces at that release; the docs site becomes a live, zero-drift demonstration of the engine.

## What Changes

- Add a `cmd/playground` WebAssembly entrypoint (`GOOS=js GOARCH=wasm`, standard Go — TinyGo is precluded by `reflect.StructOf` in `pkg/k8s/typegen`) that renders **exclusively** via the production `renderer.RenderService.Render`, reusing `conversion.ConvertSpec`, `helpers.{BuildAdditionalDeclarations,NewEngineFromConfigWithOptions}`, `typebootstrap.Bootstrap`, and `testrunner.CreateStoresFromFixtures` → `stores.NewRealStoreProvider`. It contains **no** playground-specific render or Helm-merge orchestration.
- Replace the two offline-hostile calls: `dataplane.DetectLocalVersion()` (execs the `haproxy` binary) with `dataplane.CapabilitiesFromVersion(dataplane.ParseVersionString(v))`, and `schemafetcher.DirFetcher` (reads disk) with the in-memory `schemafetcher.MapFetcher`. No other Go change is required for the render to run client-side.
- (Investigated, then deferred) Severing the offline render path from `client-native` to shrink the WASM — the bundled chart's typed `currentConfig.ServerIndex` access forces keeping `client-native/models`, so the win is sub-1 MB and requires a risky deploy-path refactor. Size is managed via transport + caching instead (see design D5).
- Add a `/playground/` page to the docs site: a vanilla-JS + CodeMirror 6 shell that runs the WASM in a Web Worker with a warm compiled engine, a config editor (multi-document), a per-type resource pane, and a live output view (`haproxy.cfg` + maps/files/certs/status-patches).
- Support four prepopulation modes: (a) all-libraries presets with per-library enable/disable, (b) a minimal from-scratch starter based on `examples/byo-crd`, (c) paste an existing `HAProxyTemplateConfig`, (d) a toggle that loads a config's `validationTests` fixtures as example resources.
- Add a controller/chart-version selector (lazy-loads a per-release WASM module) and a HAProxy-version selector (3.0–3.4, a runtime parameter — no reload).
- Add a `build-playground-wasm` CI job and `scripts/gen-playground-presets.sh` that publish, per release, an immutable content-hashed asset bundle (WASM, the matching `wasm_exec.js`, the Helm-rendered presets, and schema bundles) into the versioned docs/pages output, plus a `versions.json` index.
- Fix the stale `--api-versions` gateway guidance in `charts/CLAUDE.md` surfaced while designing the preset generator.

No controller changes at all: the playground is purely additive (a new `cmd/playground` wasm target + docs assets + CI). The client-native severance that would have touched `pkg/dataplane` is deferred.

## Capabilities

### New Capabilities

- `template-playground`: a client-side (WebAssembly) environment that renders `HAProxyTemplateConfig` templates and example resources through the controller's production render path, with prepopulation modes, two-axis version selection, live re-rendering, output-change attribution, and error surfacing — all guaranteed byte-identical to the controller at the selected release.

### Modified Capabilities

<!-- None. The parser severance is an internal dependency refactor with no spec-level behavior change; the playground reuses existing render/template-engine requirements unchanged rather than altering them. -->

## Impact

- **New Go**: `cmd/playground` (WASM main + `syscall/js` API). Generic and resource-agnostic — no per-resource code (RULE #1 clean).
- **Go refactor**: none. The client-native severance (extracting render-only leaf types out of `pkg/dataplane`) was investigated and deferred as not worth the deploy-path risk for a sub-1 MB win (see design D5); no production Go is modified by this change.
- **Docs site**: new `/playground/` page + Web Worker shell (CodeMirror 6, vanilla JS, no framework/bundler) under `docs/landing/`; per-version immutable asset bundles in the versioned pages output.
- **CI**: new `build-playground-wasm` job (reuses the Go image; runs on `v*` tags and the default branch), `scripts/gen-playground-presets.sh` (10-preset `helm template` matrix + schema bundles); hook into `trigger-docs-release` / `trigger-pages`; `wasm_exec.js` toolchain-version lock.
- **Deps**: no new Go dependencies for the render module; CodeMirror 6 + `wasm_exec.js` shipped as static assets (CDN/vendored, no build pipeline).
- **Docs**: one-line `--api-versions` gateway-preset fix in `charts/CLAUDE.md`.
- **Explicitly not changed**: controller runtime, the render/template-engine behavior, or the CRD schema.
