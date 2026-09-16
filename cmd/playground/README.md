# cmd/playground — browser (WASM) template playground

WebAssembly entrypoint that renders a `HAProxyTemplateConfig` against example
resources **entirely in the browser**, by driving the controller's *production*
render path (`renderer.RenderService.Render`). Playground output is therefore
identical to what the controller deploys at the same release.

Both render paths work: the **untyped** from-scratch starter and the **typed** bundled HAPTIC
chart (schema bundle → in-memory `MapFetcher` → production `typebootstrap`; the
real ingress `HAProxyTemplateConfig` renders correctly in Chromium). Example
resources accept raw `kubectl get … -o yaml` output (a `List`, a single object,
or a multi-document stream), bucketed by `apiVersion`+`kind` against the config's
`watchedResources` and filtered by each watched resource's label/field selector
exactly as the controller's watchers do (`bucket.go`, unit-tested in
`bucket_test.go`). The UI is styled to match the docs landing page (a dark
"terminal" aesthetic): resizable three-panel bench, mobile-responsive, a
line-numbered + syntax-highlighted output pane, CodeMirror editors with a YAML
palette, a Scriggo-template overlay (template tokens highlighted inside the YAML
block scalars), and template-aware autocomplete.

## Layout

- `main.go` (`//go:build js && wasm`) — the wasm entrypoint. Exposes a
  warm-engine API on the JS global: `hapticLoadConfig(configYAML, schemasJSON,
  haproxyVersion, migrationCoverageJSON?)` compiles the engine + render service
  once and holds them warm; `hapticRender(resourcesYAML)` renders resources
  against that warm engine, so a resource-only edit skips template recompilation.
  Call `hapticLoadConfig` again when the
  config, schema bundle, HAProxy version, or migration coverage changes.
- `stub.go` — no-op `main` for non-wasm builds so `go build ./...` stays green.
- `web/` — the static shell: `index.html`, `editor.js` (CodeMirror setup:
  YAML palette, Scriggo-template overlay, autocomplete), `migration-assets.mjs`,
  `playground.worker.js`,
  the from-scratch starter (`starter.config.yaml`, `starter.resources.yaml`),
  the committed `vendor/codemirror.js` bundle (no CDN — see `web/vendor/README.md`
  for how to rebuild it), `presets/` (the bundled-chart example resources), and
  the **Try it locally** feature: `tryout.js` + `tryout-template.sh` generate a
  self-contained bash script (downloaded by the "Try out" button) that writes the
  rendered files and runs the config via `haproxy -c`, a local Docker/Podman
  container, or a `kubectl` Pod — with the static-config caveat spelled out (no
  controller → frozen backend IPs → 503 on target-pod restart). The k8s mode also
  explains NetworkPolicy effects (a default-deny egress policy blocks the Pod's
  backends → *policy* 503s on an enforcing CNI; `port-forward` is unaffected —
  it reaches the Pod's loopback, a Kubernetes guarantee), with a
  `HAPTIC_TRYOUT_LABELS` escape hatch and an `emit-netpol` command that prints a
  ready-to-apply allow-egress policy.

## Design boundaries

The browser uses the production render service directly; test-runner orchestration
is reserved for running validation tests. The standard Go WebAssembly toolchain
preserves the runtime reflection used to build typed resources from schemas.

Bundled presets come from real `helm template` runs at build time. The browser
doesn't reproduce Helm's merge rules in JavaScript. For custom chart values, paste
the rendered config and its referenced libraries into the config editor. The
controller release selects the WebAssembly and preset assets; the HAProxy version
selects rendering capabilities. Changing either requires the corresponding engine
configuration to be loaded again. See [playground hosting](../../docs/agents/playground-hosting.md)
for asset publication and version selection.

Rendering runs in a worker so compilation doesn't block the editors. Responses
carry sequence numbers so an older result can't overwrite a newer displayed
result. Errors leave the last successful output visible alongside the error.

Browser schema checks validate individual HAProxy fields; they cannot replace
`haproxy -c`, which also checks references and whole-config semantics. The
**Try out** script provides that real-binary check outside the browser.

## Build & run locally

```bash
# 1. Assemble a complete serve directory (wasm + wasm_exec.js + shell + vendor +
#    schema bundle + presets + migration assets). Requires go, helm, yq
#    (brotli optional).
scripts/build-playground.sh /tmp/pg

# 2. Serve on loopback (any static server works)
cd /tmp/pg && python3 -m http.server 8791 --bind 127.0.0.1

# 3. Open http://127.0.0.1:8791/index.html
```

`build-playground.sh` copies `wasm_exec.js` from the same Go toolchain that built
the `.wasm` (they MUST match — re-run the script on every toolchain bump) and runs
`scripts/gen-playground-assets.sh` for the schema bundle, bundled-chart presets,
and per-source migration coverage assets.

The render path performs no filesystem, `os/exec`, or network access at runtime
(HAProxy version detection is bypassed with a supplied version;
`schemafetcher.DirFetcher` is replaced by an in-memory fetcher). The `os/exec`
package is present in the dependency graph but is never reached at render time.
