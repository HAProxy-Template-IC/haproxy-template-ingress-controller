# GitHub Pages packaging

The migration keeps the landing page, documentation versions, and playground
versions on one GitHub Pages site. Each playground engine is stored once, as
Brotli, instead of retaining raw, gzip, and Brotli transport copies. The decoded
engine and its matching Go runtime remain unchanged.

Run these commands against a complete site with all archived engines restored:

```bash
make prepare-github-pages PAGES_SOURCE=/path/to/complete-site PAGES_OUTPUT=/path/to/new-output
npm ci --prefix scripts/visual-qa
node scripts/visual-qa/node_modules/playwright/cli.js install chromium firefox
PAGES_BROWSERS=chromium,firefox make test-github-pages-browser PAGES_OUTPUT=/path/to/new-output
```

The output directory must not exist. Packaging verifies every Brotli engine
against its original SHA-256 digest before removing redundant transport copies
from the output. It materializes local symlinks, rejects escaping links, and
enforces GitHub Pages' [1 GB published-site limit](https://docs.github.com/en/pages/getting-started-with-github-pages/github-pages-limits).
An oversized build fails without replacing the previous site or deleting versions.

The browser fetches `playground.wasm.br` explicitly. It uses native Brotli
decompression where available, with a pinned, locally served decoder otherwise.
The fallback decodes in bounded chunks in the existing playground worker, so its
WebAssembly heap does not retain a second engine-sized allocation. Compressed and
decoded checksums are verified before the engine starts. No runtime CDN is needed.

`shared/pages-transport.json` records the source inventory, removed transport
copies, and exact worker/index changes. The index selects the new worker by digest
to avoid reusing its previous download implementation from the browser cache.
Release identities, schemas, templates, and engine bytes retain their original
digests. Unknown worker implementations fail packaging for review.

The complete migration baseline is archived in a GitHub release. Its URL, size,
and SHA-256 are pinned in `.github/pages-baseline.json`; the archive contains its
source provenance and every file's checksum. Restore and verify it with:

```bash
make restore-github-pages PAGES_OUTPUT=/path/to/new-output
```

Restoration verifies the archive and complete inventory before making the output
available. Links are stored as ordinary files, and
restoration rejects path traversal, links, duplicates, and incomplete inventories.
`make restore-current-github-website` restores this baseline plus every retained
publication. `make build-docs-preview` uses that complete site and runs each strict
MkDocs build once, retaining those same files for preview and publication.

## Subsequent publications

`.github/workflows/pages.yml` remains disabled until `MIGRATION_ACTIVE` is `true`.
After cutover, successful main CI publishes development content. The release
workflow publishes versioned content after image, chart, and conformance checks.
Maintenance releases cannot advance `latest` past a newer stable version.

The publisher rechecks the canonical CI receipt and downloads its documentation
and playground artifacts. Their producing job IDs, run attempts, source commits,
creation times, and GitHub SHA-256 digests must match. Merge-queue reuse follows
the original verified run; it does not rebuild the engine. A documentation-only
run keeps the previously published playground and records that provenance.

Every new version is retained as a content-addressed archive in the
`website-content` GitHub prerelease, separate from product releases. Archives
include the original transport copies, a complete file inventory, and source
evidence. The `website-state` branch contains only the small version catalog.
New publications restore every catalog entry, so expiry of CI artifacts cannot
remove a released version. Released content cannot be overwritten or silently
removed; `dev` may only move forward through main's history.

All engines and legacy redirects pass Chromium and Firefox checks before any
publication is retained. The archive is downloaded anonymously and verified
before the catalog advances. Catalog updates reject concurrent changes and never
force-push. GitHub Pages deploys only after that retention succeeds; browser
checks then compare the live inventory, landing page, documentation entry points,
version catalogs, and source identities with the exact uploaded artifact.

The catalog records the site prepared for deployment. If deployment fails after
retention, the previous live site remains; rerun the failed deployment jobs. A
new build also restores the retained version, so that failure cannot discard a
release. The workflow uses the shared `pages-publication` concurrency group.

Publishing happens after CI and adds no jobs to the PR verification gate. The
end-to-end canonical publisher still requires a live rehearsal before cutover;
the migration proof and production domain remain separate from this workflow.

Existing documentation and playground page URLs remain available. The redundant
raw `.wasm` and `.wasm.gz` URLs are replaced by `.wasm.br` for engine loading;
the complete original site export must remain archived. GitLab's old documentation
redirect rules become static HTML redirects, with matching assets for deep links.
GitHub Pages does not consume GitLab's `_redirects` file.

The first complete export has eight engines. Packaging reduces its 1.32 GB site
to about 478 MB, including the legacy documentation routes and decoder. Browser
verification loads and renders every version and checks missing, truncated,
corrupted, and mismatched engines. This proves the package locally; live Pages
deployment and the production domain switch remain separate migration steps.
