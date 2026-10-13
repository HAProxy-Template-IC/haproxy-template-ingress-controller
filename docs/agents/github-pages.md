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
`make build-docs-preview` uses this baseline and the repository's own build script.

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
