# Playground bundle

The browser playground (`cmd/playground/`) is built by `scripts/build-playground.sh
<output>/playground/<version> <version>` and served at
`https://haproxy-haptic.org/playground/<version>/`. Releases use the release version
and are immutable; `main` publishes the moving `dev` version. Publication, version
retention, and the Brotli transport are described in [GitHub Pages packaging](github-pages.md).

## Bundle contents

Everything is relative-linked, with no absolute paths:

  ```
  public/playground/<version>/
    index.html            # shell; <html data-version="<version>"> is stamped in
    editor.js             # CodeMirror setup (YAML palette, template overlay, autocomplete)
    migration-assets.mjs  # preset manifest and coverage asset loader
    migration-report.mjs  # migration-tab rendering helpers
    html.mjs              # shared HTML escaping
    playground.worker.js  # wasm worker
    wasm_exec.js          # MUST match the Go toolchain that built the wasm
    playground.wasm        (+ .br + .gz siblings)   # ~58 MB raw / ~7 MB br / ~12 MB gz
    schemas.json           (+ .br + .gz)            # typed-resource schema bundle
    vendor/codemirror.js   (+ .br + .gz)            # committed CM6 bundle (no CDN)
    highlight/config-highlight.bundle.js (+ .br + .gz)  # committed Lezer highlight bundle
    tryout.js  tryout-template.sh                   # "try it" copy-out helpers
    starter.config.yaml  starter.resources.yaml     # from-scratch starter preset
    crd.config.yaml  crd.resources.yaml             # custom-resource preset
    presets/*.config.yaml  presets/*.resources.yaml # 8 presets (ingress, extend, gateway,
                                                    #   haproxytech, haproxy-ingress,
                                                    #   nginx-ingress, haptic-annotations, all)
    migration/*.json       # per-source coverage + preset-to-source manifest
    release-identity.json                           # lineage marker, written by CI
  ```

The bundle is fully self-contained and has **no runtime third-party requests** — it renders
entirely client-side. Nothing but static files needs to be served.

## Version catalog

The shell fetches `../versions.json`, a sibling of every version directory:

```json
{
  "current": "0.2.0",
  "versions": [
    { "version": "0.2.0", "stable": true },
    { "version": "dev",   "stable": false }
  ]
}
```

Schema the shell consumes (`initVersions` in `index.html`):

- `versions[]` — array, newest first is fine; each `{ version, stable }`.
- `stable: false` renders the option as `<version> · pre`.
- `current` — pre-selected version; the shell prefers `data-version` (stamped per bundle),
  then `current`, then `versions[0]`.

A pre-release counts as non-stable (`-alpha`/`-beta`/`-rc`). `dev` is optional but
recommended so users can reach the latest main build; keep it non-stable and never `current`.

## Local preview of the exact bundle

```bash
scripts/build-playground.sh /tmp/pg/public/playground/dev dev
# Serve so /playground/dev/ resolves (versions.json is optional locally):
cd /tmp/pg && python3 -m http.server 8791 --bind 127.0.0.1
# open http://127.0.0.1:8791/public/playground/dev/index.html
```
