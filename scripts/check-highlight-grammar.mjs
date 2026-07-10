#!/usr/bin/env node
// Guard against drift between the two copies of the Scriggo/HAProxy highlight
// grammar. The live editor (`cmd/playground/web/editor.js`) is the SOURCE OF
// TRUTH — it exports `haproxyTokens` and defines the grammar the CodeMirror
// overlay uses. The docs facade (`docs/shared/playground-embed.js`) mirrors the
// same grammar to highlight the shown config before the iframe loads; the two
// live in different deploy roots (versioned /playground/ vs unversioned /shared/)
// so they can't share a module at runtime without more machinery than ~35 lines
// of stable grammar warrant. This check keeps the mirror honest instead: if the
// shared constants diverge, `make lint` fails and tells you to re-sync.
import { readFileSync } from 'node:fs';

const EDITOR = 'cmd/playground/web/editor.js';
const FACADE = 'docs/shared/playground-embed.js';

// `new Set([...])` → the elements, sorted (so wrapping/order don't matter).
function grabSet(src, name) {
  const m = src.match(new RegExp('(?:const|var)\\s+' + name + '\\s*=\\s*new Set\\(\\[([\\s\\S]*?)\\]\\)'));
  if (!m) return null;
  return m[1].split(',').map((s) => s.trim().replace(/^['"]|['"]$/g, '')).filter(Boolean).sort().join(',');
}
// A regex literal → the source, whitespace-stripped.
function grabRe(src, name) {
  const m = src.match(new RegExp('(?:const|var)\\s+' + name + '\\s*=\\s*(/[\\s\\S]*?/[a-z]*)\\s*;'));
  return m ? m[1].replace(/\s+/g, '') : null;
}

const editor = readFileSync(EDITOR, 'utf8');
const facade = readFileSync(FACADE, 'utf8');

const keys = { HP_SECTIONS: grabSet, HP_GROUPS: grabSet, CTRL: grabSet, INNER_RE: grabRe };
let failed = false;
for (const [k, grab] of Object.entries(keys)) {
  const a = grab(editor, k);
  const b = grab(facade, k);
  if (a === null) { console.error(`  MISSING ${k} in ${EDITOR}`); failed = true; continue; }
  if (b === null) { console.error(`  MISSING ${k} in ${FACADE}`); failed = true; continue; }
  if (a !== b) {
    console.error(`  DRIFT in ${k}:`);
    console.error(`    ${EDITOR}: ${a}`);
    console.error(`    ${FACADE}: ${b}`);
    failed = true;
  }
}

if (failed) {
  console.error(`\nhighlight grammar drift: re-sync ${FACADE} with the source of truth ${EDITOR}.`);
  process.exit(1);
}
console.log('check-highlight-grammar: OK — facade highlight grammar matches editor.js.');
