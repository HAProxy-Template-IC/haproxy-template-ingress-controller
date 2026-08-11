# cmd/playground — browser (WASM) template playground

WebAssembly entrypoint that renders a `HAProxyTemplateConfig` against example
resources **entirely in the browser**, by driving the controller's *production*
render path (`renderer.RenderService.Render`). Playground output is therefore
identical to what the controller deploys at the same release — see the OpenSpec
change `browser-wasm-template-playground` for the full plan.

Status: **early** — a working vertical slice, browser-verified. Both render
paths work: the **untyped** from-scratch starter and the **typed** bundled HAPTIC
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
block scalars), and template-aware autocomplete. Still to come: the full preset
matrix and per-version publishing.

## Layout

- `main.go` (`//go:build js && wasm`) — the wasm entrypoint. Exposes a
  warm-engine API on the JS global: `hapticLoadConfig(configYAML, schemasJSON,
  haproxyVersion, migrationCoverageJSON?)` compiles the engine + render service
  once and holds them warm; `hapticRender(resourcesYAML)` renders resources
  against that warm engine (so a resource-only edit skips template recompilation
  — ~13× faster on the bundled chart). Call `hapticLoadConfig` again when the
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
(`dataplane.DetectLocalVersion` is bypassed with a supplied version;
`schemafetcher.DirFetcher` is replaced by an in-memory fetcher). The `os/exec`
package is present in the dependency graph but is never reached at render time.
