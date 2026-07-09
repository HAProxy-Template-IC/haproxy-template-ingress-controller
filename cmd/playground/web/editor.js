// Playground editors: CodeMirror 6 (vendored, no CDN) with YAML highlighting, a
// Scriggo-template overlay for `haproxyConfig`/`maps`/`files`/snippet block
// scalars, and template-aware autocomplete. The vendored bundle is imported
// dynamically inside makeEditors so that if it ever fails to load, we fall back
// to a plain <textarea> and the playground still works.

/* ---------- Scriggo template grammar (CM-independent data) ---------- */
// Delimiter forms: {{ … }}, {% … %} (with {%- / -%} trim), {%% … %%} (blocks),
// and {# … #} comments. Match the block form first so its %%} isn't mis-closed
// by the {% … %} rule.
const TPL_RE = /\{%%[\s\S]*?%%\}|\{%[\s\S]*?%\}|\{\{[\s\S]*?\}\}|\{#[\s\S]*?#\}/g;
const OPEN_RE = /^\{%%-?|^\{%-?|^\{\{-?/;
const CLOSE_RE = /-?%%\}$|-?%\}$|-?\}\}$/;
// Scriggo statement keywords + literals (control flow), not the callable builtins.
const CTRL = new Set(['for', 'in', 'if', 'else', 'end', 'range', 'break', 'continue',
  'return', 'var', 'const', 'show', 'switch', 'case', 'default', 'fallthrough',
  'macro', 'extends', 'import', 'using', 'defer', 'and', 'or', 'not', 'contains',
  'true', 'false', 'nil']);
// Group 1: `//` line comment (matched first so quotes/backticks inside a comment
// don't start a string). Group 2: strings — backtick spans lines (Go raw string)
// but "…"/'…' stop at a newline so a stray quote can't bleed. Group 3: number.
// Group 4: identifier.
const INNER_RE = /(\/\/[^\n]*)|(`[^`]*`|"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')|(\b\d+(?:\.\d+)?\b)|([A-Za-z_]\w*)/g;

/* ---------- HAProxy config grammar (for injection into template blocks) ---------- */
// Section keywords that open an HAProxy section (highlighted as headings). Every
// other leading word of a config line is treated as a directive keyword.
const HP_SECTIONS = new Set(['global', 'defaults', 'frontend', 'backend', 'listen',
  'peers', 'resolvers', 'cache', 'ring', 'userlist', 'mailers', 'program',
  'http-errors', 'fcgi-app', 'log-forward', 'crt-store', 'traces', 'acme', 'ruleset']);
// spec groups whose `template:` blocks render HAProxy config (not maps/files/certs).
const HP_GROUPS = new Set(['haproxyConfig', 'templateSnippets']);

// haproxyTokens classifies one HAProxy config line into spans, each
// {start, end, cls} with cls one of sec|kw|fn|num|str|cmt. This is the SINGLE
// source of truth shared by the output pane (index.html hlHaproxy) and the
// template-block overlay, so the left editor matches the right output exactly.
export function haproxyTokens(line) {
  const toks = [];
  // haproxy comments run '#' to end of line.
  let code = line, commentStart = -1;
  const hash = line.indexOf('#');
  if (hash >= 0) { code = line.slice(0, hash); commentStart = hash; }
  const lead = code.match(/^\s*/)[0];
  const rest = code.slice(lead.length);
  if (rest) {
    const parts = rest.split(/(\s+)/);   // keep the whitespace runs to track offsets
    let pos = lead.length, firstWord = true, afterSection = false;
    for (const tok of parts) {
      const start = pos; pos += tok.length;
      if (tok === '' || /^\s+$/.test(tok)) continue;
      if (firstWord) {
        firstWord = false;
        if (lead === '' && HP_SECTIONS.has(tok.toLowerCase())) { toks.push({ start, end: pos, cls: 'sec' }); afterSection = true; }
        else toks.push({ start, end: pos, cls: 'kw' });
      } else if (afterSection) {
        afterSection = false;
        toks.push({ start, end: pos, cls: 'fn' });   // proxy name after a section keyword
      } else if (/^-?\d[\w.:/]*$/.test(tok)) {
        toks.push({ start, end: pos, cls: 'num' });   // numbers, status codes, ports, IPs
      } else if (/^["']/.test(tok)) {
        toks.push({ start, end: pos, cls: 'str' });
      }
    }
  }
  if (commentStart >= 0) toks.push({ start: commentStart, end: line.length, cls: 'cmt' });
  return toks;
}

/* ---------- autocomplete data ---------- */
// Builtins + runtime context vars, mirrored from pkg/templating (filters_scriggo.go
// constants + registerScriggoRuntimeVars). Convenience data, not a correctness path.
const FUNCTIONS = ['b64decode', 'debug', 'glob_match', 'indent', 'sort_by', 'strip', 'toJSON',
  'trim', 'append', 'basename', 'ceil', 'coalesce', 'condition', 'dig', 'dig_string', 'fail',
  'fallback', 'first_seen', 'isdigit', 'isNil', 'join', 'join_key', 'keys', 'make_guid', 'merge',
  'namespace', 'regex_search', 'replace', 'sanitize_regex', 'selectattr', 'semver_gte', 'seq',
  'shard_slice', 'sort_ints', 'sort_strings', 'statusPatch', 'strings_contains', 'strings_lower',
  'strings_replace', 'strings_splitn', 'strings_split', 'strings_trim', 'title', 'tofloat', 'toint',
  'toSlice', 'toStringSlice', 'tostring', 'to_str_map', 'transitionTime'];
const VARS = ['resources', 'pathResolver', 'controller', 'templateSnippets', 'fileRegistry',
  'dataplane', 'capabilities', 'shared', 'extraContext', 'http', 'runtimeEnvironment', 'currentConfig'];
const KEYWORDS = ['for', 'in', 'if', 'else', 'end', 'range', 'var', 'show', 'break', 'continue',
  'return', 'switch', 'case', 'default', 'macro', 'extends', 'using', 'defer', 'and', 'or', 'not', 'contains'];

const IDENT_OPTS = [
  ...KEYWORDS.map((k) => ({ label: k, type: 'keyword' })),
  ...VARS.map((v) => ({ label: v, type: 'variable', detail: 'context' })),
  ...FUNCTIONS.map((f) => ({ label: f, type: 'function', detail: 'builtin', apply: f + '(' })),
];

// Typed-field completion: property names from the loaded OpenAPI schema bundle,
// offered inside dig(subject, "a", "b", …) string arguments. fieldsAtPath(segs)
// returns the union of child field names reachable after the already-typed path
// segments, across ALL watched-resource schemas — path-aware but resource-agnostic
// (the dig subject's concrete type isn't tracked). null until a schema is loaded.
let fieldsAtPath = null;

export function setSchemaBundle(json) {
  fieldsAtPath = null;
  if (!json) return;
  let bundle;
  try { bundle = JSON.parse(json); } catch { return; }
  const schemas = Object.values(bundle).map((e) => e && e.schema).filter(Boolean);
  if (!schemas.length) return;
  // The child-property map of a schema node: object.properties, or array items.properties.
  const childrenOf = (node) => {
    if (!node || typeof node !== 'object') return null;
    if (node.properties) return node.properties;
    if (node.items && node.items.properties) return node.items.properties;
    return null;
  };
  const walk = (node, segs) => {
    let p = childrenOf(node);
    for (const s of segs) { if (!p || !p[s]) return null; p = childrenOf(p[s]); }
    return p;
  };
  fieldsAtPath = (segs) => {
    const out = new Set();
    if (!segs.length) {                        // no path yet → resource roots (apiVersion/spec/…)
      for (const sc of schemas) { const p = childrenOf(sc); if (p) Object.keys(p).forEach((k) => out.add(k)); }
      return [...out];
    }
    // Match the segment sequence starting at ANY node (handles digging from a
    // nested subject, e.g. dig(rule, "backend", …)), unioning the children.
    const stack = schemas.slice(), seen = new Set();
    while (stack.length) {
      const n = stack.pop();
      if (!n || typeof n !== 'object' || seen.has(n)) continue;
      seen.add(n);
      const f = walk(n, segs); if (f) Object.keys(f).forEach((k) => out.add(k));
      const p = childrenOf(n); if (p) for (const k in p) stack.push(p[k]);
    }
    return [...out];
  };
}

// digContext returns the argument text (from the call's "(" to the cursor) when
// the cursor sits inside a dig()/dig_string() call, else null. Scans backwards
// tracking paren depth so nested balanced calls (…List()) don't fool it.
function digContext(before) {
  let depth = 0;
  for (let i = before.length - 1; i >= 0; i--) {
    const c = before[i];
    if (c === ')') depth++;
    else if (c === '(') {
      if (depth === 0) return /(?:\bdig_string|\bdig)\s*$/.test(before.slice(0, i)) ? before.slice(i + 1) : null;
      depth--;
    }
  }
  return null;
}

// digFieldOptions returns { partial, fields } for the dig() string argument the
// cursor is in — `partial` is the text already typed in the current segment,
// `fields` the sorted schema field names to offer — or null when the cursor
// isn't inside an open dig-string argument. Exported for unit testing without
// CodeMirror; templateCompletions wraps it into a CM completion result.
export function digFieldOptions(before) {
  if (!fieldsAtPath) return null;
  const args = digContext(before);
  if (args == null) return null;
  // Odd number of quotes → the cursor sits inside an unterminated string (the
  // current path segment). Even → every string is closed, so we're between args.
  if ((args.match(/['"]/g) || []).length % 2 === 0) return null;
  const qi = Math.max(args.lastIndexOf('"'), args.lastIndexOf("'")); // the open quote
  const prior = [...args.slice(0, qi).matchAll(/(['"])(.*?)\1/g)].map((m) => m[2]);
  const fields = fieldsAtPath(prior);
  return fields.length ? { partial: args.slice(qi + 1), fields: fields.slice().sort() } : null;
}

// Watched-resource keys declared in the current config's `watchedResources:` block.
function watchedKeys(doc) {
  const text = doc.toString();
  const start = text.search(/^watchedResources:/m);
  if (start < 0) return [];
  const keys = [];
  for (const line of text.slice(start).split('\n').slice(1)) {
    if (/^\S/.test(line)) break;                  // dedented to the next top-level key
    const mm = line.match(/^  (\w[\w-]*):\s*$/);  // two-space-indented map key
    if (mm) keys.push(mm[1]);
  }
  return keys;
}

function insideTemplate(before) {
  const open = Math.max(before.lastIndexOf('{{'), before.lastIndexOf('{%'));
  const close = Math.max(before.lastIndexOf('}}'), before.lastIndexOf('%}'));
  return open > close;
}

function templateCompletions(context) {
  const before = context.state.doc.sliceString(Math.max(0, context.pos - 4000), context.pos);
  if (!insideTemplate(before)) return null;

  // Typed-field completion inside dig(subject, "seg", "seg", "<here>").
  const dig = digFieldOptions(before);
  if (dig) {
    return {
      from: context.pos - dig.partial.length,
      options: dig.fields.map((f) => ({ label: f, type: 'property', detail: 'field' })),
      validFor: /^[\w.-]*$/,
    };
  }

  const dotted = context.matchBefore(/[\w.]+/);
  if (dotted && dotted.text.includes('.')) {
    const seg = context.matchBefore(/\w*/);        // segment after the last dot
    const head = dotted.text.slice(0, dotted.text.length - seg.text.length).replace(/\.$/, '');
    const options = head === 'resources'
      ? watchedKeys(context.state.doc).map((k) => ({ label: k, type: 'class', detail: 'watched' }))
      : [{ label: 'List', type: 'method', detail: '() []any', apply: 'List()' }];  // resources.<kind>.List()
    return { from: seg.from, options, validFor: /^\w*$/ };
  }
  const word = context.matchBefore(/\w*/);
  if (!word || (word.from === word.to && !context.explicit)) return null;
  return { from: word.from, options: IDENT_OPTS, validFor: /^\w*$/ };
}

/* ---------- CM-dependent setup (built after the bundle loads) ---------- */
function initCodeMirror(CM) {
  const { EditorView, basicSetup, Prec, RangeSetBuilder, Decoration, ViewPlugin,
    yaml, HighlightStyle, syntaxHighlighting, tags } = CM;

  // YAML palette (docs-landing token colors).
  const yamlPalette = HighlightStyle.define([
    { tag: [tags.definition(tags.propertyName), tags.propertyName], color: 'var(--t-var)' },
    { tag: [tags.string, tags.special(tags.string)], color: 'var(--t-str)' },
    { tag: tags.number, color: 'var(--t-num)' },
    { tag: [tags.bool, tags.null, tags.keyword, tags.atom], color: 'var(--t-kw)' },
    { tag: tags.comment, color: 'var(--t-cmt)', fontStyle: 'italic' },
    { tag: [tags.meta, tags.punctuation, tags.separator], color: 'var(--t-d)' },
  ]);

  // Editor theme — a terminal, always dark regardless of page theme.
  const theme = EditorView.theme({
    '&': { height: '100%', backgroundColor: 'transparent', color: 'var(--term-fg)' },
    '.cm-scroller': { fontFamily: 'var(--mono)', fontSize: '12.5px', lineHeight: '1.6' },
    '.cm-content': { padding: '10px 0' },
    '.cm-gutters': { backgroundColor: 'transparent', color: 'var(--term-dim)', border: 'none' },
    '.cm-activeLine': { backgroundColor: 'rgba(107,163,214,.06)' },
    '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--term-fg)' },
    '.cm-cursor': { borderLeftColor: 'var(--accent-2)' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: 'color-mix(in srgb, var(--accent) 24%, transparent)' },
    '&.cm-focused': { outline: 'none' },
    '.cm-tooltip': { backgroundColor: 'var(--term-chrome)', border: '1px solid var(--term-border)', borderRadius: '8px', color: 'var(--term-fg)' },
    '.cm-tooltip.cm-tooltip-autocomplete > ul': { fontFamily: 'var(--mono)', fontSize: '12px', maxHeight: '16em' },
    '.cm-tooltip-autocomplete ul li[aria-selected]': { backgroundColor: 'var(--accent-soft)', color: 'var(--term-fg)' },
    '.cm-completionLabel': { color: 'var(--term-fg)' },
    '.cm-completionDetail': { color: 'var(--term-dim)', fontStyle: 'normal', marginLeft: '1em' },
    '.cm-completionIcon': { color: 'var(--accent)', opacity: '.8' },
    // Ctrl/Cmd-hover go-to-definition affordance.
    '.cm-godef': { textDecoration: 'underline', textDecorationColor: 'var(--accent)', textUnderlineOffset: '2px', cursor: 'pointer' },
  }, { dark: true });

  const MARK = {
    delim: Decoration.mark({ class: 'tpl-delim' }),
    ctrl: Decoration.mark({ class: 'tpl-ctrl' }),
    fn: Decoration.mark({ class: 'tpl-fn' }),
    vari: Decoration.mark({ class: 'tpl-var' }),
    str: Decoration.mark({ class: 'tpl-str' }),
    num: Decoration.mark({ class: 'tpl-num' }),
    comment: Decoration.mark({ class: 'tpl-comment' }),
  };

  const tokenizeInner = (add, base, inner) => {
    let t;
    INNER_RE.lastIndex = 0;
    while ((t = INNER_RE.exec(inner))) {
      const from = base + t.index, to = from + t[0].length;
      if (t[1]) add(from, to, MARK.comment);   // // line comment
      else if (t[2]) add(from, to, MARK.str);
      else if (t[3]) add(from, to, MARK.num);
      else {
        const name = t[4];
        if (CTRL.has(name)) add(from, to, MARK.ctrl);
        else if (/^\s*\(/.test(inner.slice(t.index + name.length))) add(from, to, MARK.fn);
        else add(from, to, MARK.vari);
      }
    }
  };

  // Scan only the visible ranges (+ a margin to catch a tag opened just above the
  // viewport) so the overlay stays fast on large bundled configs regardless of
  // total size. A tag straddling the far edge re-resolves on the next scroll.
  const templateDecorations = (view) => {
    const b = new RangeSetBuilder();
    const doc = view.state.doc, docLen = doc.length;
    const marks = [];
    const seen = new Set();   // region-start offsets already emitted (windows can overlap)
    for (const { from, to } of view.visibleRanges) {
      const winStart = Math.max(0, from - 3000);
      const text = doc.sliceString(winStart, Math.min(docLen, to + 3000));
      let m;
      TPL_RE.lastIndex = 0;
      while ((m = TPL_RE.exec(text))) {
        const s = m[0], base = winStart + m.index;
        if (seen.has(base)) continue;
        seen.add(base);
        if (s.startsWith('{#')) { marks.push({ from: base, to: base + s.length, mark: MARK.comment }); continue; }   // {# comment #}
        const open = s.match(OPEN_RE)[0], close = s.match(CLOSE_RE)[0];
        marks.push({ from: base, to: base + open.length, mark: MARK.delim });
        tokenizeInner((f, t, mk) => marks.push({ from: f, to: t, mark: mk }), base + open.length, s.slice(open.length, s.length - close.length));
        marks.push({ from: base + s.length - close.length, to: base + s.length, mark: MARK.delim });
      }
    }
    marks.sort((a, z) => a.from - z.from || a.to - z.to);   // RangeSetBuilder needs sorted, non-overlapping
    let lastTo = -1;
    for (const { from, to, mark } of marks) {
      if (from < lastTo) continue;
      b.add(from, to, mark);
      lastTo = to;
    }
    return b.finish();
  };

  const templateOverlay = ViewPlugin.fromClass(class {
    constructor(view) { this.decorations = templateDecorations(view); }
    update(u) { if (u.docChanged || u.viewportChanged) this.decorations = templateDecorations(u.view); }
  }, { decorations: (v) => v.decorations });

  // --- HAProxy config injection ---
  // Highlight HAProxy directives / section keywords / comments inside the
  // `template:` block scalars under haproxyConfig & templateSnippets (which render
  // HAProxy config). maps/files/certs template blocks are left as plain strings.
  // Same classes the output pane uses (hl-*), so the highlighting matches exactly.
  const HL_MARK = {
    sec: Decoration.mark({ class: 'hl-sec' }),
    kw: Decoration.mark({ class: 'hl-kw' }),
    fn: Decoration.mark({ class: 'hl-fn' }),
    num: Decoration.mark({ class: 'hl-num' }),
    str: Decoration.mark({ class: 'hl-str' }),
    cmt: Decoration.mark({ class: 'hl-cmt' }),
  };

  // haproxyRegions returns the {from,to} char ranges of HAProxy-config block
  // content in one O(n) pass: track the current spec group (2-space-indented
  // keys) and, inside a `template:` block under an HP group, its content until
  // the block dedents.
  const haproxyRegions = (doc) => {
    const regions = [];
    let group = '', inBlock = false, keyIndent = 0, from = -1, to = -1;
    for (let i = 1; i <= doc.lines; i++) {
      const line = doc.line(i), text = line.text;
      const ind = /^\s*/.exec(text)[0].length;
      const blank = text.trim() === '';
      if (inBlock) {
        if (!blank && ind <= keyIndent) {          // dedent → block ends; re-process this line below
          if (from !== -1) regions.push({ from, to });
          inBlock = false;
        } else {
          if (!blank) { if (from === -1) from = line.from; to = line.to; }
          continue;
        }
      }
      // A spec group key. The config may be a bare spec (haproxyConfig: at col 0)
      // or a full CR (spec: -> haproxyConfig: at 2 spaces), so match either indent.
      const g = /^(?: {2})?([A-Za-z][\w-]*):\s*$/.exec(text);
      if (g) { group = g[1]; continue; }
      const tm = /^(\s*)template:\s*\|/.exec(text);
      if (tm && HP_GROUPS.has(group)) { inBlock = true; keyIndent = tm[1].length; from = -1; to = -1; }
    }
    if (inBlock && from !== -1) regions.push({ from, to });
    return regions;
  };

  // haproxyDecorations runs the shared HAProxy tokenizer on each visible region
  // line, with scriggo tags masked to spaces so the tokenizer ignores them (the
  // scriggo overlay highlights the tags themselves). A '#' comment is clipped at
  // the first scriggo tag after it, so a live `{{ … }}` inside a comment line
  // stays a template tag rather than being swallowed by the comment.
  const haproxyDecorations = (view, regions) => {
    const b = new RangeSetBuilder();
    const doc = view.state.doc;
    const marks = [];
    for (const { from: vf, to: vt } of view.visibleRanges) {
      for (const reg of regions) {
        if (reg.to < vf || reg.from > vt) continue;
        let pos = Math.max(vf, reg.from);
        const end = Math.min(vt, reg.to);
        while (pos <= end) {
          const line = doc.lineAt(pos), text = line.text;
          const tagStarts = [];
          const masked = text.replace(TPL_RE, (m, off) => { tagStarts.push(off); return ' '.repeat(m.length); });
          for (const t of haproxyTokens(masked)) {
            let to = t.end;
            if (t.cls === 'cmt') { const clip = tagStarts.find((s) => s >= t.start); if (clip !== undefined) to = clip; }
            if (to > t.start) marks.push({ from: line.from + t.start, to: line.from + to, mark: HL_MARK[t.cls] });
          }
          if (line.to + 1 <= pos) break;   // last line
          pos = line.to + 1;
        }
      }
    }
    marks.sort((a, z) => a.from - z.from || a.to - z.to);
    let lastTo = -1;
    for (const { from, to, mark } of marks) { if (from < lastTo) continue; b.add(from, to, mark); lastTo = to; }
    return b.finish();
  };

  const haproxyOverlay = ViewPlugin.fromClass(class {
    constructor(view) { this.regions = haproxyRegions(view.state.doc); this.decorations = haproxyDecorations(view, this.regions); }
    update(u) {
      if (u.docChanged) this.regions = haproxyRegions(u.state.doc);
      if (u.docChanged || u.viewportChanged) this.decorations = haproxyDecorations(u.view, this.regions);
    }
  }, { decorations: (v) => v.decorations });

  const yamlSupport = yaml();
  const base = [
    basicSetup,
    yamlSupport,
    Prec.highest(syntaxHighlighting(yamlPalette)),   // our palette wins over basicSetup's default
    theme,
    EditorView.lineWrapping,
  ];
  // The config editor gets the template overlay + template-aware completions;
  // the resources editor is plain YAML.
  // haproxyOverlay first so templateOverlay's scriggo marks layer over it.
  const configExtra = [haproxyOverlay, templateOverlay, yamlSupport.language.data.of({ autocomplete: templateCompletions })];
  return { EditorView, base, configExtra };
}

/* ---------- public API ---------- */
export async function makeEditors(onConfigChange, onResChange, onNav, onGotoDef) {
  const $ = (id) => document.getElementById(id);
  const notify = (name) => { if (onNav) onNav(name); };
  try {
    const CM = await import('./vendor/codemirror.js');
    const { EditorView, base, configExtra } = initCodeMirror(CM);
    const { Decoration, ViewPlugin } = CM;
    // The token under a document position: word run (including '-' for HAProxy
    // keywords like http-request, '.' for Scriggo member chains like
    // pathResolver.GetPath), trimmed of leading/trailing punctuation. Returns the
    // trimmed token plus its document offsets so both the click and the hover
    // affordance resolve the exact same span.
    function tokenAt(view, pos) {
      const doc = view.state.doc, line = doc.lineAt(pos), col = pos - line.from, lt = line.text;
      let inTpl = false, m; TPL_RE.lastIndex = 0;
      const text = doc.toString();
      while ((m = TPL_RE.exec(text))) { if (pos >= m.index && pos < m.index + m[0].length) { inTpl = true; break; } }
      const ch = inTpl ? /[\w.]/ : /[\w-]/;
      let a = col, b = col;
      while (a > 0 && ch.test(lt[a - 1])) a--;
      while (b < lt.length && ch.test(lt[b])) b++;
      while (a < b && /[.-]/.test(lt[a])) a++;
      while (b > a && /[.-]/.test(lt[b - 1])) b--;
      const token = lt.slice(a, b);
      return token ? { token, inTpl, from: line.from + a, to: line.from + b } : null;
    }

    // Ctrl/Cmd-hover feedback: underline the token a Ctrl/Cmd-click would open.
    // A ViewPlugin renders the mark from `hoverTarget`; the mouse handlers update
    // `hoverTarget` and force a redraw with an empty dispatch (only when it changes,
    // so mousemove doesn't spam transactions).
    let hoverTarget = null, godefView = null;
    const godefMark = Decoration.mark({ class: 'cm-godef' });
    const godefPlugin = ViewPlugin.fromClass(class {
      constructor() { this.decorations = this.build(); }
      update() { this.decorations = this.build(); }
      build() { return hoverTarget ? Decoration.set([godefMark.range(hoverTarget.from, hoverTarget.to)]) : Decoration.none; }
    }, { decorations: (v) => v.decorations });
    const setHover = (view, next) => {
      const cur = hoverTarget;
      if ((!cur && !next) || (cur && next && cur.from === next.from && cur.to === next.to)) return;
      hoverTarget = next;
      view.dispatch({});   // no-op tx → godefPlugin.update() re-reads hoverTarget
    };

    // Ctrl/Cmd + left-click → "go to definition": open the matching HAProxy / HAPTIC docs.
    const gotoDefExt = EditorView.domEventHandlers({
      mousedown(e, view) {
        if (!onGotoDef || e.button !== 0 || !(e.ctrlKey || e.metaKey)) return false;
        const pos = view.posAtCoords({ x: e.clientX, y: e.clientY });
        const t = pos == null ? null : tokenAt(view, pos);
        if (!t) return false;
        e.preventDefault();
        onGotoDef({ token: t.token, scriggo: t.inTpl });
        return true;
      },
      mousemove(e, view) {
        godefView = view;
        if (!(e.ctrlKey || e.metaKey)) { setHover(view, null); return false; }
        const pos = view.posAtCoords({ x: e.clientX, y: e.clientY });
        const t = pos == null ? null : tokenAt(view, pos);
        setHover(view, t ? { from: t.from, to: t.to } : null);
        return false;
      },
      mouseleave(e, view) { setHover(view, null); return false; },
    });
    // Clear the hover underline when Ctrl/Cmd is released, even if the editor
    // isn't focused (CM's own keyup only fires while focused).
    document.addEventListener('keyup', (e) => { if (!(e.ctrlKey || e.metaKey) && godefView) setHover(godefView, null); });
    const mk = (parent, extra, cb, name) => {
      parent.innerHTML = '';
      const view = new EditorView({
        parent, doc: '',
        extensions: [...base, ...extra, EditorView.updateListener.of((u) => { if (u.docChanged) cb(); })],
      });
      // Cursor+scroll history with browser-style back/forward: a jump (goto)
      // pushes where we were onto `back` and clears `fwd` (a new branch); back
      // moves current→fwd and restores from back; forward is the mirror. Cleared
      // on a full document replace, where old offsets no longer mean anything.
      const back = [], fwd = [];
      const snapshot = () => ({ anchor: view.state.selection.main.anchor, scrollTop: view.scrollDOM.scrollTop });
      const restore = (p) => {
        view.dispatch({ selection: { anchor: Math.min(p.anchor, view.state.doc.length) } });
        view.focus();
        // Restore the exact prior scroll, overriding the selection's auto-scroll.
        view.requestMeasure({ read: () => {}, write: () => { view.scrollDOM.scrollTop = p.scrollTop; } });
      };
      const step = (from, to) => { const p = from.pop(); if (!p) return false; to.push(snapshot()); restore(p); notify(name); return true; };
      return {
        get: () => view.state.doc.toString(),
        set: (t) => { back.length = 0; fwd.length = 0; view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: t } }); notify(name); },
        goto: (line, col) => {
          back.push(snapshot());
          if (back.length > 200) back.shift();
          fwd.length = 0;
          const l = Math.max(1, Math.min(line | 0, view.state.doc.lines));
          const lo = view.state.doc.line(l);
          const pos = Math.min(lo.from + Math.max(0, (col | 0) - 1), lo.to);
          view.dispatch({ selection: { anchor: pos } });
          view.focus();
          // Center the line in the viewport. scrollIntoView({y:'center'}) is
          // unreliable here (the selection's own scroll wins), so set scrollTop
          // directly from the line's block geometry. Deferred to after layout.
          view.requestMeasure({
            read: () => view.lineBlockAt(pos),
            write: (block) => {
              const sc = view.scrollDOM;
              sc.scrollTop = Math.max(0, block.top - sc.clientHeight / 2 + block.height / 2);
            },
          });
          notify(name);
        },
        back: () => step(back, fwd),
        forward: () => step(fwd, back),
        canBack: () => back.length > 0,
        canForward: () => fwd.length > 0,
      };
    };
    return {
      config: mk($('ed-config'), [...configExtra, gotoDefExt, godefPlugin], onConfigChange, 'config'),
      res: mk($('ed-res'), [], onResChange, 'res'),
      kind: 'codemirror',
    };
  } catch (err) {
    console.warn('CodeMirror unavailable, using textareas:', err);
    const mk = (parent, ph, cb, name) => {
      parent.innerHTML = '';
      const ta = document.createElement('textarea');
      ta.className = 'fallback'; ta.spellcheck = false; ta.placeholder = ph;
      ta.addEventListener('input', cb); parent.appendChild(ta);
      const back = [], fwd = [];
      const snapshot = () => ({ start: ta.selectionStart, scrollTop: ta.scrollTop });
      const restore = (p) => { ta.focus(); ta.setSelectionRange(p.start, p.start); ta.scrollTop = p.scrollTop; };
      const step = (from, to) => { const p = from.pop(); if (!p) return false; to.push(snapshot()); restore(p); notify(name); return true; };
      const offOf = (line, col) => {
        const rows = ta.value.split('\n');
        const l = Math.max(1, Math.min(line | 0, rows.length));
        let off = 0;
        for (let i = 0; i < l - 1; i++) off += rows[i].length + 1;
        return off + Math.max(0, (col | 0) - 1);
      };
      return {
        get: () => ta.value,
        set: (t) => { back.length = 0; fwd.length = 0; ta.value = t; notify(name); },
        goto: (line, col) => {
          back.push(snapshot());
          if (back.length > 200) back.shift();
          fwd.length = 0;
          const off = offOf(line, col);
          ta.focus(); ta.setSelectionRange(off, off);
          notify(name);
        },
        back: () => step(back, fwd),
        forward: () => step(fwd, back),
        canBack: () => back.length > 0,
        canForward: () => fwd.length > 0,
      };
    };
    return {
      config: mk($('ed-config'), '', onConfigChange, 'config'),
      res: mk($('ed-res'), 'paste `kubectl get ingress,svc -A -o yaml` here…', onResChange, 'res'),
      kind: 'textarea',
    };
  }
}
