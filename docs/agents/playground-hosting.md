# Playground hosting — cross-repo publish contract

The browser playground (`cmd/playground/`) is built **here** (in `haproxy-haptic/haptic`)
and published **there** (in `haproxy-haptic/haproxy-haptic.gitlab.io`, the docs/landing
Pages site). This doc is the contract between the two: what this repo produces, and what
the Pages repo must do to serve it at `https://haproxy-haptic.org/playground/`.

The split mirrors the existing docs flow: this repo triggers the Pages project
(`trigger-pages` on the default branch, `trigger-docs-release` on `v*` tags,
`strategy: depend`), and the Pages project's `scripts/deploy-docs.sh` assembles the site.

## What this repo produces

The `build-playground-wasm` job (`.gitlab-ci.yml`, `build` stage) runs
`scripts/build-playground.sh public/playground/<version> <version>` and uploads
`public/playground/` as a job artifact (`expire_in: 1 month`).

- **Version identity**
  - Release pipeline (`v*` tag): `<version>` = `${CI_COMMIT_TAG#v}` (e.g. `0.2.0-alpha.1`) — an **immutable** directory.
  - Default branch (`main`): `<version>` = `dev` — a **moving** directory, overwritten each push.
- **Bundle layout** (everything relative-linked, no absolute paths):

  ```
  public/playground/<version>/
    index.html            # shell; <html data-version="<version>"> is stamped in
    editor.js             # CodeMirror setup (YAML palette, template overlay, autocomplete)
    playground.worker.js  # wasm worker
    wasm_exec.js          # MUST match the Go toolchain that built the wasm
    playground.wasm        (+ .br + .gz siblings)   # ~58 MB raw / ~7 MB br / ~12 MB gz
    schemas.json           (+ .br + .gz)            # typed-resource schema bundle
    vendor/codemirror.js   (+ .br + .gz)            # committed CM6 bundle (no CDN)
    starter.config.yaml  starter.resources.yaml     # from-scratch starter preset
    presets/*.config.yaml  presets/*.resources.yaml # 7 presets (ingress, gateway, vendors, all)
  ```

The bundle is fully self-contained and has **no runtime third-party requests** — it renders
entirely client-side. Nothing but static files needs to be served.

## What the Pages repo must do (`deploy-docs.sh`)

On each triggered pipeline, add a playground publish step:

1. **Determine the version.** Use `DOC_VERSION` when the trigger sets it (release: it's
   `${CI_COMMIT_TAG}`, so strip the leading `v`); otherwise default to `dev` (default-branch
   trigger sets no version).

2. **Fetch the bundle artifact** from this repo by ref + job name:

   ```bash
   # ref = the tag for releases, "main" for dev
   PROJECT="haproxy-haptic%2Fhaptic"
   curl --location --fail --output pg.zip \
     --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
     "https://gitlab.com/api/v4/projects/${PROJECT}/jobs/artifacts/${REF}/download?job=build-playground-wasm"
   unzip -q pg.zip           # yields public/playground/<version>/
   ```

   (The `pages-preview` job already fetches the Pages artifact this same way — reuse the pattern.)

3. **Place it in the site** so it serves at `/playground/<version>/`. Copy
   `public/playground/<version>/` into the site's `public/playground/<version>/`.
   Immutable release dirs must never be re-touched once published; only `dev/` is overwritten.

4. **Update the shared `public/playground/versions.json`** (a sibling of every version
   directory — the shell fetches it as `../versions.json`, so it is written **once**, not
   into each version dir). Add/refresh this version's entry and set `current` to the newest
   **stable** release:

   ```json
   {
     "current": "0.2.0-alpha.1",
     "versions": [
       { "version": "dev",          "stable": false },
       { "version": "0.2.0-alpha.1", "stable": false }
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

5. **Keep the compressed siblings.** GitLab Pages negotiates `Content-Encoding` and serves
   `foo.wasm.br` / `foo.wasm.gz` automatically when the sibling exists next to `foo.wasm`, so
   just publish all three (raw + `.br` + `.gz`). Do not strip them.

## Verify (Milestone 5, task 6.6)

Brotli transport on GitLab Pages is unconfirmed — confirm it once after the first publish:

```bash
curl -sI -H 'Accept-Encoding: br'   https://haproxy-haptic.org/playground/<version>/playground.wasm | grep -i content-encoding   # expect: br
curl -sI -H 'Accept-Encoding: gzip' https://haproxy-haptic.org/playground/<version>/playground.wasm | grep -i content-encoding   # expect: gzip
```

If `br` is not honoured, `gzip` (also shipped) is the fallback — no action needed beyond
noting it. If neither is served, the raw 58 MB wasm downloads once and is then cached by the
per-version path; the shell shows a download-progress bar meanwhile.

## Local preview of the exact bundle

```bash
scripts/build-playground.sh /tmp/pg/public/playground/dev dev
# Serve so /playground/dev/ resolves (versions.json is optional locally):
cd /tmp/pg && python3 -m http.server 8791 --bind 127.0.0.1
# open http://127.0.0.1:8791/public/playground/dev/index.html
```
