// Build script for the composite config highlighter.
//   1. Compiles each .grammar to <name>.js + <name>.terms.js (lezer-generator).
//   2. esbuild-bundles the headless highlighter (highlight.js) + its @lezer deps
//      into one self-contained ESM file, written to BOTH deploy roots (the
//      playground editor at cmd/playground/web/highlight/ and the docs facade at
//      docs/shared/) so the two surfaces share one grammar byte-for-byte.
//
// `--check` builds in memory and fails if either committed copy is stale,
// without writing (used by scripts/check-highlight-bundle.mjs in `make lint`).
//
// Run with the @lezer/* + esbuild toolchain on the module path (the repo's build
// image, or /tmp/lezerbuild during development).

import { buildParserFile } from "@lezer/generator";
import * as esbuild from "esbuild";
import { readFileSync, writeFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, "..", "..", "..", ".."); // web/highlight -> repo root
const check = process.argv.includes("--check");
const grammars = ["scriggo", "haproxy", "map"];
// The bundle is committed in both deploy roots; keep them identical.
const outputs = [
  join(here, "config-highlight.bundle.js"),
  join(repo, "docs", "shared", "config-highlight.bundle.js"),
];

for (const name of grammars) {
  const src = readFileSync(join(here, `${name}.grammar`), "utf8");
  const { parser, terms } = buildParserFile(src, {
    fileName: `${name}.grammar`,
    moduleStyle: "es",
  });
  if (check) {
    // Verify the committed .js/.terms.js match a fresh compile of the .grammar.
    // The bundle check below only proves committed-.js -> committed-bundle; this
    // proves .grammar -> committed-.js, so together they cover the full chain.
    if (readFileSync(join(here, `${name}.js`), "utf8") !== parser ||
        readFileSync(join(here, `${name}.terms.js`), "utf8") !== terms) {
      console.error(`stale: ${name}.js / ${name}.terms.js differs from a fresh compile of ${name}.grammar`);
      process.exit(1);
    }
  } else {
    writeFileSync(join(here, `${name}.js`), parser);
    writeFileSync(join(here, `${name}.terms.js`), terms);
    console.log(`compiled ${name}.grammar -> ${name}.js + ${name}.terms.js`);
  }
}

const result = await esbuild.build({
  entryPoints: [join(here, "highlight.js")],
  bundle: true,
  format: "esm",
  minify: true,
  write: false,
});
const code = Buffer.from(result.outputFiles[0].contents);

if (check) {
  for (const out of outputs) {
    if (!readFileSync(out).equals(code)) {
      console.error(`stale: ${out} differs from a fresh build of the .grammar sources`);
      process.exit(1);
    }
  }
  console.log("config-highlight.bundle.js is fresh in both deploy roots.");
} else {
  for (const out of outputs) writeFileSync(out, code);
  const gzip = gzipSync(code);
  console.log(
    `bundled highlight.js -> config-highlight.bundle.js (x${outputs.length}): ` +
    `${(code.length / 1024).toFixed(1)} KiB raw, ${(gzip.length / 1024).toFixed(1)} KiB gzip`,
  );
}
