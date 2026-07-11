// Acceptance test for the composite Lezer highlighter. Parses the fixture config
// through configParser + highlightToHTML and asserts the resulting spans. Prints
// PASS/FAIL per assertion; exits non-zero on any failure.

import { highlightToHTML } from "./highlight.js";

const cfg = `apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: files-demo
spec:
  files:
    503.http:
      template: |
        HTTP/1.0 503 Service Unavailable
        Cache-Control: no-cache
        Connection: close
  haproxyConfig:
    template: |
      frontend http
        bind :80
      {%- for _, ing := range resources.ingresses.List() %}
      backend {{ ing.metadata.name }}
        server s1 {{ ing.metadata.name | tostring }}:80 check
      {%- end %}
`;

// A second fixture: a lone Scriggo comment inside a haproxyConfig block.
const cfgComment = `spec:
  haproxyConfig:
    template: |
      {# this is a comment #}
`;

const html = highlightToHTML(cfg);
const htmlComment = highlightToHTML(cfgComment);

let failures = 0;
function check(desc, ok) {
  console.log((ok ? "PASS" : "FAIL") + "  " + desc);
  if (!ok) failures++;
}

// `<span class="CLS">TEXT</span>` present?
function hasSpan(h, cls, text) {
  return h.includes(`<span class="${cls}">${text}</span>`);
}
// TEXT appears somewhere inside a span of class CLS (span may contain more)?
function wrappedIn(h, cls, text) {
  const re = new RegExp(`<span class="${cls}">[^<]*${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`);
  return re.test(h);
}

console.log("\n--- Assertion 1: files-block header names are NOT YAML keys/props ---");
for (const word of ["Cache-Control", "Connection"]) {
  const keyed = wrappedIn(html, "pg-cf-key", word) || wrappedIn(html, "pg-cf-tprop", word);
  check(`${word} is plain text (no pg-cf-key / pg-cf-tprop span)`, !keyed);
}
check("Cache-Control line rendered verbatim as plain text", html.includes("Cache-Control: no-cache"));
check("Connection line rendered verbatim as plain text", html.includes("Connection: close"));

console.log("\n--- Assertion 2: YAML keys outside block scalars ARE pg-cf-key ---");
for (const key of ["apiVersion", "kind", "metadata", "name", "spec", "files", "haproxyConfig", "template"]) {
  check(`${key} → pg-cf-key`, hasSpan(html, "pg-cf-key", key));
}

console.log("\n--- Assertion 3: HAProxy config tokens ---");
check("frontend → pg-cf-sec", hasSpan(html, "pg-cf-sec", "frontend"));
check("backend → pg-cf-sec", hasSpan(html, "pg-cf-sec", "backend"));
check("bind → pg-cf-kw", hasSpan(html, "pg-cf-kw", "bind"));
check("server → pg-cf-kw", hasSpan(html, "pg-cf-kw", "server"));
check(":80 → pg-cf-num", hasSpan(html, "pg-cf-num", ":80"));

console.log("\n--- Assertion 4: Scriggo tag tokens ---");
check("for → pg-cf-tkw", hasSpan(html, "pg-cf-tkw", "for"));
check("range → pg-cf-tkw", hasSpan(html, "pg-cf-tkw", "range"));
check("end → pg-cf-tkw", hasSpan(html, "pg-cf-tkw", "end"));
check("resources → pg-cf-tvar", hasSpan(html, "pg-cf-tvar", "resources"));
check("ing → pg-cf-tvar", hasSpan(html, "pg-cf-tvar", "ing"));
check("List → pg-cf-fn (call)", hasSpan(html, "pg-cf-fn", "List"));
check("tostring → pg-cf-fn (after-pipe filter)", hasSpan(html, "pg-cf-fn", "tostring"));
check("metadata → pg-cf-tprop (after .)", hasSpan(html, "pg-cf-tprop", "metadata"));
check("name → pg-cf-tprop (after .)", hasSpan(html, "pg-cf-tprop", "name"));
check("{%- → pg-cf-tdelim", hasSpan(html, "pg-cf-tdelim", "{%-"));
check("%} → pg-cf-tdelim", hasSpan(html, "pg-cf-tdelim", "%}"));
check("{{ → pg-cf-tdelim", hasSpan(html, "pg-cf-tdelim", "{{"));
check("}} → pg-cf-tdelim", hasSpan(html, "pg-cf-tdelim", "}}"));
check("| → pg-cf-top", hasSpan(html, "pg-cf-top", "|"));

console.log("\n--- Assertion 5: Scriggo comment is entirely pg-cf-cmt ---");
check("{# this is a comment #} → pg-cf-cmt (whole span)",
  hasSpan(htmlComment, "pg-cf-cmt", "{# this is a comment #}"));

// --- Regression: template scalars highlight regardless of document shape ---
// (a) Helm-values wrapping (controller.config.templateSnippets...), (b) a
// dedented facade excerpt with no group key at all. Both must give the
// template body real HAProxy/Scriggo spans, not plain YAML-scalar text.
const shapes = {
  "values-wrapped": `controller:
  config:
    templateSnippets:
      my-snippet:
        template: |
          {%- if x %}
          http-request set-header X-Y z
          {%- end %}
`,
  "dedented excerpt": `my-snippet:
  template: |
    {%- if x %}
    http-request set-header X-Y z
    {%- end %}
`,
  "spec-shaped": `spec:
  templateSnippets:
    my-snippet:
      template: |
        {%- if x %}
        http-request set-header X-Y z
        {%- end %}
`,
};
console.log("\n--- Assertion 5: template scalars highlight in every document shape ---");
for (const [name, doc] of Object.entries(shapes)) {
  const h = highlightToHTML(doc);
  check(`${name}: scriggo keyword 'if' highlighted`, wrappedIn(h, "pg-cf-tkw", "if"));
  check(`${name}: haproxy keyword 'http-request' highlighted`, wrappedIn(h, "pg-cf-kw", "http-request") || hasSpan(h, "pg-cf-kw", "http-request"));
}

console.log("\n" + (failures === 0 ? "ALL ASSERTIONS PASSED" : `${failures} ASSERTION(S) FAILED`));
process.exit(failures === 0 ? 0 : 1);
