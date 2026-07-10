#!/usr/bin/env node
// Guard the shared config highlighter (cmd/playground/web/highlight/).
//
// One Lezer grammar drives both playground surfaces — the CM6 editor and the
// docs facade — via the built ESM bundle `config-highlight.bundle.js`. That
// bundle is committed in TWO deploy roots because they publish separately:
//   * cmd/playground/web/highlight/  → /playground/<v>/  (the live editor)
//   * docs/shared/                   → /shared/          (the docs facade)
// If they drift, the editor and the shown config stop matching — the exact
// problem the single grammar was meant to end. This check fails `make lint`
// when the two copies differ, or when either is stale versus a fresh build of
// the .grammar sources (when the @lezer build toolchain is available).
import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';

const SRC = 'cmd/playground/web/highlight/config-highlight.bundle.js';
const FACADE = 'docs/shared/config-highlight.bundle.js';

const src = readFileSync(SRC);
const facade = readFileSync(FACADE);
if (!src.equals(facade)) {
  console.error(`check-highlight-bundle: FAIL — the two bundle copies differ.\n` +
    `  ${SRC}\n  ${FACADE}\n` +
    `  Rebuild with 'node cmd/playground/web/highlight/build.mjs' (it writes both).`);
  process.exit(1);
}

// If the build toolchain is present, verify the committed bundle is not stale
// versus the .grammar sources. Skipped (not failed) when @lezer isn't installed,
// so the check still runs in a bare checkout — CI installs the toolchain.
try {
  execFileSync('node', ['cmd/playground/web/highlight/build.mjs', '--check'],
    { stdio: 'pipe' });
} catch (e) {
  const msg = String(e.stderr || e.stdout || e.message);
  if (/Cannot find package|ERR_MODULE_NOT_FOUND/.test(msg)) {
    console.log('check-highlight-bundle: OK (copies match; build toolchain absent, freshness skipped).');
    process.exit(0);
  }
  console.error('check-highlight-bundle: FAIL — committed bundle is stale vs .grammar sources.\n' + msg);
  process.exit(1);
}
console.log('check-highlight-bundle: OK — bundle copies match and are fresh.');
