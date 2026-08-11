## ADDED Requirements

### Requirement: Render via the production render path

The playground SHALL produce rendered output exclusively through the controller's production `renderer.RenderService.Render` entrypoint and its dependencies (`rendercontext.Builder`, `helpers.NewEngineFromConfigWithOptions`, `helpers.BuildAdditionalDeclarations`). The playground MUST NOT contain any render, template-context-assembly, or Helm-library-merge orchestration of its own.

#### Scenario: Output matches the controller at the selected release

- **WHEN** a user renders a `HAProxyTemplateConfig` and a set of example resources in the playground built from release tag `vX.Y.Z`
- **THEN** the produced `haproxy.cfg`, map files, general files, certificates, and status patches are byte-identical to what the `vX.Y.Z` controller produces from the same config, resources, and HAProxy version

#### Scenario: No parallel render implementation

- **WHEN** the playground WASM module is built
- **THEN** the rendering call graph reaches `renderer.RenderService.Render` and does not include `testrunner.RenderFixtures` or any playground-local re-implementation of auxiliary-file, k8s-resource, or status-patch rendering

### Requirement: Build render inputs from in-memory stores

The playground SHALL construct render inputs by building in-memory stores from the user's example resources via the production `testrunner.CreateStoresFromFixtures` and wrapping them with `stores.NewRealStoreProvider`, indexed using each watched resource's configured `IndexBy` via the production indexer. It SHALL accept example resources as raw `kubectl get ... -o yaml` output — a `List`, a single object, or a multi-document stream — as well as the name-keyed `validationTests.fixtures` shape.

#### Scenario: Resource added to the correct store

- **WHEN** a user adds an example resource under a watched-resource name declared in the config
- **THEN** the resource is indexed by that resource's `IndexBy` and appears in the render context under `resources.<name>`

#### Scenario: Paste raw kubectl output

- **WHEN** a user pastes the output of `kubectl get <resource> -A -o yaml` (a `List`, a single object, or a multi-document stream)
- **THEN** the playground buckets each object into the watched resources whose GVK matches its `apiVersion`+`kind`, filtered by each watched resource's label and field selector exactly as the controller's watchers would, and renders from the resulting stores

#### Scenario: Bucketing names no kinds

- **WHEN** a user watches an arbitrary custom resource and pastes its `kubectl` output
- **THEN** the object is bucketed by the same generic `apiVersion`+`kind`-against-`watchedResources` matching, with no Kubernetes-kind-specific code in the playground (RULE #1)

#### Scenario: Empty stores are safe

- **WHEN** a config declares a watched resource for which the user supplies no example objects
- **THEN** the render context still exposes that resource as an empty, listable store and the render does not error on its absence

### Requirement: Fully client-side execution

The playground SHALL run entirely in the user's browser and SHALL perform no filesystem, process-execution, or network access during rendering. HAProxy version and capabilities SHALL be supplied as runtime parameters (`dataplane.CapabilitiesFromVersion(dataplane.ParseVersionString(v))`) rather than detected, and schemas SHALL be supplied in-memory via `schemafetcher.MapFetcher`.

#### Scenario: Render performs no host I/O

- **WHEN** the playground renders any config
- **THEN** no filesystem, `os/exec`, or network syscall is invoked by the render path

### Requirement: Prepopulation modes

The playground SHALL offer four ways to populate its editors: (a) all-libraries presets with per-library enable/disable, (b) a minimal from-scratch starter derived from `examples/byo-crd`, (c) pasting an existing `HAProxyTemplateConfig` (accepting both the full-resource and bare-spec YAML shapes), and (d) a toggle that loads a config's `validationTests` fixtures as example resources.

#### Scenario: Paste a full or bare config

- **WHEN** a user pastes a `HAProxyTemplateConfig` as either a full Kubernetes resource or a bare spec
- **THEN** the playground parses it and makes all of its templates immediately available for rendering

#### Scenario: Load validation-test fixtures as resources

- **WHEN** a user enables the fixtures toggle for a config containing `validationTests`
- **THEN** each test's fixtures (with the `_global` entry merged in) populate the resource pane, and the `_global` synthetic entry is never presented as an executable test

### Requirement: Library toggling without client-side merge reimplementation

Per-library enable/disable SHALL be served as CI-pre-rendered Helm presets (v1). The client SHALL NOT reimplement `haptic.mergeLibraries`. An optional, lazily-loaded in-WASM Helm-merge module MAY provide arbitrary library combinations (v2) by running the genuine chart loader.

Migration coverage SHALL be published as separate per-source tooling assets. The build SHALL derive each preset's source list from the annotation libraries its Helm render enabled, and the browser SHALL pass the selected coverage to the WASM module separately from the HAProxyTemplateConfig.

#### Scenario: Selecting a library preset

- **WHEN** a user selects a library configuration in the playground
- **THEN** the resulting `HAProxyTemplateConfig` is one produced by the real `helm template` at build time, not assembled by client-side merge logic

#### Scenario: Selecting a migration preset

- **WHEN** a user selects a preset with an annotation compatibility library
- **THEN** the browser loads that library's coverage asset without adding tooling metadata to the config editor

### Requirement: HAProxy version selector

The playground SHALL expose a HAProxy-version selector covering the chart's supported versions (3.0–3.4), defaulting to the build default. Changing it SHALL apply as a runtime parameter (capabilities map and `extraContext.haproxyVersion`) and re-render without reloading the WASM module.

#### Scenario: Switching HAProxy version re-renders instantly

- **WHEN** a user changes the HAProxy version
- **THEN** the output re-renders using the new capabilities without fetching or re-instantiating a WASM module

#### Scenario: Version-gated fixtures are hidden

- **WHEN** the fixtures mode is active and a fixture is gated above the selected HAProxy version by `MinHAProxyVersion`
- **THEN** that fixture is excluded, matching controller behavior at the selected version

### Requirement: Controller version selector

The playground SHALL expose a controller/chart-version selector that lazy-loads and caches a per-release WASM module and its asset bundle, defaulting to the newest stable release. Pre-releases SHALL be selectable but SHALL NOT be the default.

#### Scenario: Switching controller version loads and caches its module

- **WHEN** a user selects a controller version not yet loaded
- **THEN** the playground fetches and instantiates that version's module, and a subsequent switch back to it uses the cached module without re-fetching

### Requirement: Per-version asset publishing

Each release's CI SHALL publish an immutable, content-hashed asset bundle for the playground — the WASM module, its matching `wasm_exec.js` from the same Go toolchain, the rendered presets, migration assets, and the schema bundles — and SHALL append the release to a `versions.json` index. The `wasm_exec.js` SHALL be the exact toolchain version that built the WASM and SHALL NOT be shared across versions.

#### Scenario: Tag pipeline publishes a version bundle

- **WHEN** a `v*` release tag pipeline runs
- **THEN** an immutable `/playground/<version>/` asset bundle is published and `versions.json` lists that version

### Requirement: Compressed transport

The playground WASM SHALL be served brotli-precompressed with `Content-Encoding: br`. The build SHALL NOT apply `wasm-opt -Oz` on the download path, since it does not reduce brotli-compressed transfer size.

#### Scenario: WASM delivered compressed

- **WHEN** the browser requests the WASM module
- **THEN** it is delivered as a brotli-precompressed asset

### Requirement: Live re-render with a warm engine

The playground SHALL re-render on input change using a Web Worker that holds the compiled engine warm. A config/template change SHALL recompile the engine; a resource-only or version-only change SHALL reuse the warm engine and render without recompiling. Renders SHALL be tagged with a monotonically increasing sequence number and stale results (older than the newest request) SHALL be discarded.

#### Scenario: Resource edit skips recompilation

- **WHEN** a user edits only the example resources
- **THEN** the playground renders using the already-compiled engine without recompiling templates

#### Scenario: Stale renders are dropped

- **WHEN** a user types quickly, superseding an in-flight render
- **THEN** the superseded render's result is discarded and only the newest result is displayed

### Requirement: Output-change attribution

The playground SHALL visually attribute rendered-output changes to the triggering edit by diffing the current output against a pinned baseline, for any watched resource kind, using no per-kind logic.

#### Scenario: Changed lines are highlighted

- **WHEN** an edit changes the rendered `haproxy.cfg`
- **THEN** the lines that were added, removed, or modified relative to the baseline are visually marked

### Requirement: Error surfacing without losing output

The playground SHALL preserve the last successful render when a new input fails, and SHALL surface config-parse, template-compile, and render/runtime errors as distinct, pane-scoped messages.

#### Scenario: Compile error keeps last good output

- **WHEN** a template edit fails to compile
- **THEN** the output pane keeps the last successful render, and a template-compile error is shown scoped to the offending template with an indication that a stale render is displayed

#### Scenario: Missing schema for typed access

- **WHEN** a pasted config uses typed resource access but no schema is loaded for that kind
- **THEN** the playground surfaces a clear error indicating a schema is required, mirroring the offline `--schema-dir` behavior

### Requirement: Shareable and persistent state

The playground SHALL encode its state (versions, config, resources, migration source selection, library selection, active preset) into the URL for sharing and SHALL autosave to local storage, both without a server round-trip.

#### Scenario: Share via URL

- **WHEN** a user copies the playground URL after editing
- **THEN** opening that URL reconstructs the same config, resources, and version selections
