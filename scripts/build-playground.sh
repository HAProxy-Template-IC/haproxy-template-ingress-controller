#!/usr/bin/env bash
# Build the self-contained, per-version browser-playground bundle into <output-dir>.
#
# Produces everything the /playground/<version>/ page needs, all relative-linked:
#   index.html  playground.worker.js  starter.config.yaml  starter.resources.yaml
#   wasm_exec.js  playground.wasm(+.br)  schemas.json(+.br)  presets/*.yaml
#
# The <version> is stamped onto <html data-version="..."> so the version selector
# knows which build this is. Immutability comes from the per-version directory in
# the path (public/playground/<version>/), so no content-hashing is needed.
#
# Runnable locally (needs go + helm + yq + optionally brotli); the CI job
# build-playground-wasm calls it. Aggregating public/playground/versions.json
# across versions is the publisher's job (see docs/agents/playground-hosting.md).
#
# Usage: scripts/build-playground.sh <output-dir> [version]
set -euo pipefail

OUT="${1:?usage: build-playground.sh <output-dir> [version]}"
VERSION="${2:-local}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB="$REPO/cmd/playground/web"

command -v go >/dev/null || { echo "go not found" >&2; exit 1; }

mkdir -p "$OUT"

echo "==> shell -> $OUT"
cp "$WEB/index.html" "$WEB/editor.js" "$WEB/tryout.js" "$WEB/tryout-template.sh" \
   "$WEB/playground.worker.js" "$WEB/starter.config.yaml" "$WEB/starter.resources.yaml" \
   "$WEB/crd.config.yaml" "$WEB/crd.resources.yaml" "$OUT/"
cp -r "$WEB/vendor" "$OUT/vendor"   # committed CodeMirror bundle (no CDN at runtime)
# Stamp the version so the version selector marks this build as current.
sed -i "s#<html lang=\"en\">#<html lang=\"en\" data-version=\"${VERSION}\">#" "$OUT/index.html"

echo "==> wasm ($VERSION) + matching wasm_exec.js"
( cd "$REPO" && GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$OUT/playground.wasm" ./cmd/playground )
# wasm_exec.js MUST come from the exact toolchain that built the wasm.
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$OUT/wasm_exec.js"

echo "==> schema bundle + presets"
"$REPO/scripts/gen-playground-assets.sh" "$OUT"

# Precompress the large assets. A Pages host that negotiates Content-Encoding
# serves the .br sibling (best ratio) or the .gz sibling; otherwise the plain
# file is used. gzip is emitted too because Content-Encoding: br support on the
# Pages host is not guaranteed (see docs/agents/playground-hosting.md).
BIG_ASSETS=(playground.wasm schemas.json vendor/codemirror.js)
if command -v brotli >/dev/null; then
  echo "==> brotli precompress"
  for f in "${BIG_ASSETS[@]}"; do [ -f "$OUT/$f" ] && brotli -q 11 -k -f "$OUT/$f"; done
fi
if command -v gzip >/dev/null; then
  echo "==> gzip precompress"
  for f in "${BIG_ASSETS[@]}"; do [ -f "$OUT/$f" ] && gzip -9 -k -f "$OUT/$f"; done
fi

echo "==> done: $OUT (version ${VERSION}), wasm $(du -h "$OUT/playground.wasm" | cut -f1)$( [ -f "$OUT/playground.wasm.br" ] && echo " / $(du -h "$OUT/playground.wasm.br" | cut -f1) br")$( [ -f "$OUT/playground.wasm.gz" ] && echo " / $(du -h "$OUT/playground.wasm.gz" | cut -f1) gz")"
