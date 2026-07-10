# Vendored CodeMirror 6 bundle

`codemirror.js` is a single self-contained ESM bundle of CodeMirror 6 + the
extensions the playground editor needs (YAML language, decorations, autocomplete,
highlight styling). It's committed (not fetched from a CDN) so the playground has
no third-party runtime dependency and no multi-instance `@codemirror/state` bug.

## Rebuild (only when bumping CodeMirror)

```bash
cd /tmp && rm -rf cmbuild && mkdir cmbuild && cd cmbuild
cat > entry.mjs <<'JS'
export { EditorView, basicSetup } from "codemirror";
export { EditorState, Prec, RangeSetBuilder } from "@codemirror/state";
export { Decoration, ViewPlugin, keymap } from "@codemirror/view";
export { yaml } from "@codemirror/lang-yaml";
export { HighlightStyle, syntaxHighlighting, LRLanguage, LanguageSupport } from "@codemirror/language";
export { autocompletion } from "@codemirror/autocomplete";
export { tags } from "@lezer/highlight";
JS
npm install codemirror@6.0.1 @codemirror/lang-yaml@6.1.2 @codemirror/view@6 \
  @codemirror/state@6 @codemirror/language@6 @codemirror/autocomplete@6 @lezer/highlight@1
npx esbuild@0.24 entry.mjs --bundle --format=esm --minify --legal-comments=none \
  --target=es2020 --outfile=codemirror.bundle.js
cp codemirror.bundle.js <repo>/cmd/playground/web/vendor/codemirror.js
```
