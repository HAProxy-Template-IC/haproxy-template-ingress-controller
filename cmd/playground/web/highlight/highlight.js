// Shared highlight module for HAPTIC's composite HAProxyTemplateConfig grammar.
//
// Three nested levels, stitched with @lezer/common's parseMixed:
//   1. @lezer/yaml parses the outer document. Each `key: |` block scalar yields a
//      BlockLiteralContent node; we dispatch it to a sub-parser by its spec-group
//      key path (the key directly under `spec`).
//   2. haproxy / map / text (scriggo) grammars parse the block body and expose the
//      four Scriggo tag forms as opaque `TemplateTag` nodes.
//   3. The scriggo grammar is overlaid onto every `TemplateTag`, and is also the
//      `text` parser used verbatim for files / sslCertificates blocks.
//
// Because @lezer/yaml treats block-scalar content as one opaque token, header
// lines inside a `files` block (e.g. `Cache-Control: no-cache`) are never seen as
// YAML keys — which is the whole point of replacing the old regex highlighter.

import { parser as yamlParser } from "@lezer/yaml";
import { parseMixed } from "@lezer/common";
import { styleTags, tags as t, tagHighlighter, highlightCode } from "@lezer/highlight";

import { parser as rawScriggo } from "./scriggo.js";
import { parser as rawHaproxy } from "./haproxy.js";
import { parser as rawMap } from "./map.js";

/* ---------- per-grammar highlight props ---------- */

const scriggoHighlight = styleTags({
  Keyword: t.controlKeyword,
  Bool: t.bool,
  FunctionName: t.function(t.variableName),
  VariableName: t.variableName,
  PropertyName: t.propertyName,
  String: t.string,
  Number: t.number,
  Operator: t.operator,
  "ExprOpen StmtOpen BlockOpen TagClose": t.processingInstruction,
  Comment: t.comment,
});

const haproxyHighlight = styleTags({
  Section: t.heading,
  Directive: t.keyword,
  ProxyName: t.function(t.variableName),
  Number: t.number,
  StringTok: t.string,
  Comment: t.comment,
});

const mapHighlight = styleTags({
  MapKey: t.definition(t.propertyName),
  Comment: t.comment,
});

/* ---------- assembled parsers ---------- */

// Inner: one scriggo tag (or a text+tags stream, reused as the `text` parser).
const scriggoParser = rawScriggo.configure({ props: [scriggoHighlight] });

// Overlay the scriggo parser onto every TemplateTag node.
const overlayScriggo = parseMixed((node) =>
  node.name === "TemplateTag" ? { parser: scriggoParser } : null);

const haproxyParser = rawHaproxy.configure({ props: [haproxyHighlight], wrap: overlayScriggo });
const mapParser = rawMap.configure({ props: [mapHighlight], wrap: overlayScriggo });
const textParser = scriggoParser;

// Reconstruct the spec-group key of a BlockLiteralContent node: the key directly
// under `spec` (or the outermost key for a bare spec document). Walks the Pair
// ancestors, reading each Key's text.
function specGroupOf(node, input) {
  const keys = [];
  for (let n = node.parent; n; n = n.parent) {
    if (n.name === "Pair") {
      const key = n.getChild("Key");
      if (key) keys.push(input.read(key.from, key.to).trim());
    }
  }
  keys.reverse(); // root → leaf, e.g. [spec, haproxyConfig, template]
  const si = keys.indexOf("spec");
  if (si >= 0 && si + 1 < keys.length) return keys[si + 1];
  return keys.length ? keys[0] : null;
}

// Bare-template parser (HAProxy body + scriggo overlay), exported so the editor
// can build a CM6 LanguageSupport for the scriggo scratchpad, where the buffer
// is a template body rather than a full HAProxyTemplateConfig document.
export const templateParser = haproxyParser;

// Outer: the fully-wrapped YAML parser (the composite grammar). Exported so the
// editor can build a CM6 LanguageSupport from it.
export const configParser = yamlParser.configure({
  wrap: parseMixed((node, input) => {
    if (node.name !== "BlockLiteralContent") return null;
    const group = specGroupOf(node.node, input);
    if (group === "haproxyConfig" || group === "templateSnippets") return { parser: haproxyParser };
    if (group === "maps") return { parser: mapParser };
    if (group === "files" || group === "sslCertificates") return { parser: textParser };
    return null;
  }),
});

/* ---------- highlighter ---------- */

// Maps grammar tags to HAPTIC's existing playground-config CSS classes. Modified
// tags (function(variableName), definition(propertyName)) are matched before their
// bases, so e.g. a scriggo PropertyName (pg-cf-tprop) and a YAML key
// (definition(propertyName) → pg-cf-key) stay distinct.
export const highlighter = tagHighlighter([
  { tag: t.controlKeyword, class: "pg-cf-tkw" },
  { tag: t.bool, class: "pg-cf-tbool" },
  { tag: t.function(t.variableName), class: "pg-cf-fn" },
  { tag: t.variableName, class: "pg-cf-tvar" },
  { tag: t.propertyName, class: "pg-cf-tprop" },
  { tag: t.definition(t.propertyName), class: "pg-cf-key" },
  { tag: t.string, class: "pg-cf-str" },
  { tag: t.number, class: "pg-cf-num" },
  { tag: t.operator, class: "pg-cf-top" },
  { tag: t.processingInstruction, class: "pg-cf-tdelim" },
  { tag: t.comment, class: "pg-cf-cmt" },
  { tag: t.heading, class: "pg-cf-sec" },
  { tag: t.keyword, class: "pg-cf-kw" },
]);

function esc(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function renderHTML(tree, code) {
  let html = "";
  highlightCode(
    code, tree, highlighter,
    (text, cls) => { html += cls ? `<span class="${cls}">${esc(text)}</span>` : esc(text); },
    () => { html += "\n"; },
  );
  return html;
}

// Headless highlighter: render a full HAProxyTemplateConfig to HTML spans using
// the composite parser. This is what the docs facade uses to highlight a config
// identically to the live editor.
export function highlightToHTML(code) {
  return renderHTML(configParser.parse(code), code);
}

// Headless highlighter for a bare template body (scriggo-only docs embeds and the
// editor's scriggo mode): the shown text is an HAProxy template with tags, not a
// YAML document, so parse it with the haproxy sub-grammar directly.
export function highlightTemplateToHTML(code) {
  return renderHTML(haproxyParser.parse(code), code);
}
