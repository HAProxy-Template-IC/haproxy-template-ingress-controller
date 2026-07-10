/* Interactive HAPTIC playground embeds for the docs sites.
 *
 * Turns a `.pg-embed` block into a facade (title + the shown config + a "Run
 * live" button); clicking swaps in an <iframe> of the playground in ?embed=1
 * mode. The wasm only downloads when the reader clicks, so page load stays
 * cheap. The scenario is encoded client-side into the same #s= fragment the
 * Share button produces (JSON {c,r,v,s,p} -> gzip -> base64url), so what the
 * doc shows IS what runs — no pre-generated URLs to drift.
 *
 * Authoring (raw HTML in Markdown; the fenced block inside is the config shown
 * and run):
 *
 *   <div class="pg-embed" data-title="HSTS on every TLS response"
 *        data-resources="ingress" data-tab="haproxy.cfg" data-focus="42-48">
 *   ```yaml
 *   <a COMPLETE, small HAProxyTemplateConfig>
 *   ```
 *   </div>
 *
 * Attributes (all optional except a config source):
 *   data-base       playground bundle base URL (default window.HAPTIC_PG_BASE or /playground/dev/)
 *   data-scenario   preset name -> fetch BOTH config+resources from the bundle (no inline block needed)
 *   data-resources  preset name -> resources when the config is inline
 *   data-version    HAProxy target version (default 3.4)
 *   data-tab        output tab to open on (haproxy.cfg|maps|files|certs|status|applied|resources|trace|tests)
 *                   — "tests" auto-runs the config's spec.validationTests on load
 *   data-focus      [file:]start-end | file — highlight/scroll to the important lines
 *   data-controls   comma list re-enabling controls in the embed (tabs,resources,tools,nav,max,reload,provenance,dots)
 *   data-height     iframe height in px (default 460)
 *   data-title      header label
 *   data-difficulty 1-3 -> shown as stars
 *
 * Challenge: add a `<details class="pg-solution">` containing a solution config
 * block; a "Load solution" button reruns the embed with it.
 */
(function () {
  'use strict';
  function withSlash(b) { return b.slice(-1) === '/' ? b : b + '/'; }

  // Which playground bundle an embed targets. A versioned docs page
  // (/controller/<v>/…, /helm-chart/<v>/…) uses the matching /playground/<v>/ so
  // the shown render matches that release's engine; anything else (the "latest"
  // alias, the landing page) falls back to the dev shell. Override per-embed with
  // data-base, or per-site with window.HAPTIC_PG_BASE.
  function embedBase(el) {
    if (el.dataset.base) return withSlash(el.dataset.base);
    if (window.HAPTIC_PG_BASE) return withSlash(window.HAPTIC_PG_BASE);
    var m = location.pathname.match(/\/(?:controller|helm-chart)\/([^/]+)\//);
    if (m && /^[0-9]/.test(m[1])) return '/playground/' + m[1] + '/';
    return '/playground/dev/';
  }

  // JSON -> gzip -> base64url, matching the playground's gzipB64().
  async function gzipB64(str) {
    var cs = new CompressionStream('gzip');
    var buf = new Uint8Array(await new Response(new Blob([str]).stream().pipeThrough(cs)).arrayBuffer());
    var bin = '';
    for (var i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode.apply(null, buf.subarray(i, i + 0x8000));
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  // Raw source text of a mkdocs/pymdownx highlight block (spans concatenate;
  // avoid line-number tables on embed configs).
  function codeText(block) {
    var code = block.querySelector('pre code') || block.querySelector('code') || block.querySelector('pre');
    return code ? code.textContent.replace(/\n$/, '') : '';
  }

  function fetchText(url) {
    return fetch(url).then(function (r) { if (!r.ok) throw new Error(url); return r.text(); });
  }

  function stars(n) {
    n = Math.max(0, Math.min(3, parseInt(n, 10) || 0));
    return n ? '★'.repeat(n) : '';
  }

  /* --- facade config highlighting (mirrors the playground editor) ---
   * mkdocs renders the HAProxy config inside a `template: |` block scalar as one
   * flat YAML string. Re-highlight it here so the shown config matches the live
   * editor: HAProxy tokens inside HP-group template blocks, Scriggo tags, and a
   * light YAML pass for the surrounding keys. Token classes map to mkdocs-material
   * code colours (theme-aware). hpTokens is ported verbatim from editor.js. */
  var HP_SECTIONS = new Set(['global', 'defaults', 'frontend', 'backend', 'listen', 'peers',
    'resolvers', 'cache', 'ring', 'userlist', 'mailers', 'program', 'http-errors', 'fcgi-app',
    'log-forward', 'crt-store', 'traces', 'acme', 'ruleset']);
  var HP_GROUPS = new Set(['haproxyConfig', 'templateSnippets']);
  // Scriggo control-flow keywords + the inner-token grammar, ported from the live
  // editor (editor.js) so tags inside the facade colour the same way: group 1 a
  // `//` comment, 2 a string, 3 a number, 4 an identifier (keyword if in CTRL).
  var CTRL = new Set(['for', 'in', 'if', 'else', 'end', 'range', 'break', 'continue',
    'return', 'var', 'const', 'show', 'switch', 'case', 'default', 'fallthrough',
    'macro', 'extends', 'import', 'using', 'defer', 'and', 'or', 'not', 'contains',
    'true', 'false', 'nil']);
  var INNER_RE = /(\/\/[^\n]*)|(`[^`]*`|"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')|(\b\d+(?:\.\d+)?\b)|([A-Za-z_]\w*)/g;

  function escHtml(s) { return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }

  function hpTokens(line) {
    var toks = [], code = line, commentStart = -1;
    var hash = line.indexOf('#');
    if (hash >= 0) { code = line.slice(0, hash); commentStart = hash; }
    var lead = code.match(/^\s*/)[0];
    var rest = code.slice(lead.length);
    if (rest) {
      var parts = rest.split(/(\s+)/), pos = lead.length, firstWord = true, afterSection = false;
      for (var i = 0; i < parts.length; i++) {
        var tok = parts[i], start = pos; pos += tok.length;
        if (tok === '' || /^\s+$/.test(tok)) continue;
        if (firstWord) {
          firstWord = false;
          if (lead === '' && HP_SECTIONS.has(tok.toLowerCase())) { toks.push({ start: start, end: pos, cls: 'sec' }); afterSection = true; }
          else toks.push({ start: start, end: pos, cls: 'kw' });
        } else if (afterSection) { afterSection = false; toks.push({ start: start, end: pos, cls: 'fn' }); }
        else if (/^-?\d[\w.:/]*$/.test(tok)) toks.push({ start: start, end: pos, cls: 'num' });
        else if (/^["']/.test(tok)) toks.push({ start: start, end: pos, cls: 'str' });
      }
    }
    if (commentStart >= 0) toks.push({ start: commentStart, end: line.length, cls: 'cmt' });
    return toks;
  }

  // Fill each token's char range with its class, only where still unset (an
  // earlier pass — a masked scriggo tag/comment — always wins).
  function fillToks(cls, off, toks) {
    for (var t = 0; t < toks.length; t++)
      for (var k = toks[t].start; k < toks[t].end; k++)
        if (cls[off + k] == null) cls[off + k] = toks[t].cls;
  }

  // A YAML line → token ranges: a leading '#' comment, or the `key` of `key:`.
  // Values are left plain (their scriggo tags are already masked by markRanges).
  function yamlTokens(line) {
    var cm = /^(\s*)(#.*)$/.exec(line);
    if (cm) return [{ start: cm[1].length, end: line.length, cls: 'cmt' }];
    var kv = /^(\s*)(- )?([\w.\/-]+):/.exec(line);
    if (kv) { var s = kv[1].length + (kv[2] ? kv[2].length : 0); return [{ start: s, end: s + kv[3].length, cls: 'key' }]; }
    return [];
  }

  // Mark every char of each match of `re` with class `c`, only where still null.
  // `re` uses [\s\S] so matches SPAN LINES — this is what makes a multi-line
  // {# … #} comment or {%% … %%} block highlight as one unit instead of its
  // interior lines bleeding into the per-line HAProxy/YAML tokenizer.
  function markRanges(text, cls, re, c) {
    var m; re.lastIndex = 0;
    while ((m = re.exec(text))) {
      for (var k = m.index; k < m.index + m[0].length; k++) if (cls[k] == null) cls[k] = c;
      if (m[0] === '') re.lastIndex++;
    }
  }

  // Scriggo tags {%%…%%} / {%…%} / {{…}} (multi-line): base-colour the whole tag
  // 'tpl', then overlay inner tokens (strings, numbers, control keywords, //
  // comments) so the tag reads like code, matching the live editor. Runs after
  // the {# … #} comment pass, so a tag inside a comment stays dimmed.
  function markTags(text, cls) {
    var TAG = /\{%%[\s\S]*?%%\}|\{%[\s\S]*?%\}|\{\{[\s\S]*?\}\}/g, m;
    while ((m = TAG.exec(text))) {
      var s = m.index, tag = m[0], any = false;
      for (var k = 0; k < tag.length; k++) if (cls[s + k] == null) { cls[s + k] = 'tpl'; any = true; }
      if (any) {   // only tokenize inner content of a tag that wasn't already inside a comment
        INNER_RE.lastIndex = 0; var im;
        while ((im = INNER_RE.exec(tag))) {
          var c = im[1] ? 'cmt' : im[2] ? 'str' : im[3] ? 'num' : (im[4] && CTRL.has(im[4]) ? 'kw' : null);
          if (c) for (var j = im.index; j < im.index + im[0].length; j++) if (cls[s + j] === 'tpl') cls[s + j] = c;
        }
      }
      if (tag === '') TAG.lastIndex++;
    }
  }

  function hpTemplateLines(lines) {       // line indices inside an HP-group `template: |` block
    var set = new Set(), group = '', inBlock = false, keyIndent = 0;
    for (var i = 0; i < lines.length; i++) {
      var text = lines[i], ind = /^\s*/.exec(text)[0].length, blank = text.trim() === '';
      if (inBlock) {
        if (!blank && ind <= keyIndent) inBlock = false;   // dedent → reprocess
        else { if (!blank) set.add(i); continue; }
      }
      var g = /^(?: {2})?([A-Za-z][\w-]*):\s*$/.exec(text);
      if (g) { group = g[1]; continue; }
      var tm = /^(\s*)template:\s*\|/.exec(text);
      if (tm && HP_GROUPS.has(group)) { inBlock = true; keyIndent = tm[1].length; }
    }
    return set;
  }

  // Render the whole text as <span> runs from a per-char class array.
  function renderRunsGlobal(text, cls) {
    var html = '', i = 0, n = text.length;
    while (i < n) {
      var c = cls[i], j = i; while (j < n && cls[j] === c) j++;
      html += c ? '<span class="pg-cf-' + c + '">' + escHtml(text.slice(i, j)) + '</span>' : escHtml(text.slice(i, j));
      i = j;
    }
    return html;
  }

  // Stateful, whole-text highlighter (mirrors the live editor's layering):
  //   1. mask scriggo comments {# … #} (dim), then tags {%%…%%}/{%…%}/{{…}},
  //      each scanned across the FULL text so multi-line ones stay one unit;
  //   2. tokenize the remaining code per line — HAProxy inside HP-group
  //      `template: |` blocks, else YAML — with already-masked tag/comment
  //      chars blanked so they never re-tokenize (no bleed, no comment interior).
  function highlightConfigHTML(text, allTemplate) {
    var cls = new Array(text.length).fill(null);
    markRanges(text, cls, /\{#[\s\S]*?#\}/g, 'cmt');   // comments first (dim, win over tags)
    markTags(text, cls);                                // then tags, with inner tokenization
    var lines = text.split('\n');
    var hp = allTemplate ? null : hpTemplateLines(lines);
    var off = 0;
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i], masked = '';
      for (var k = 0; k < line.length; k++) masked += cls[off + k] == null ? line[k] : ' ';
      fillToks(cls, off, (allTemplate || (hp && hp.has(i))) ? hpTokens(masked) : yamlTokens(masked));
      off += line.length + 1;
    }
    return renderRunsGlobal(text, cls);
  }

  // Build the #s= fragment + query for one run.
  async function buildSrc(el, config, resources) {
    var v = el.dataset.version || '3.4';
    var scriggo = 'scriggo' in el.dataset;   // pure-template mode: no schema, no resources
    var state = { c: config, r: scriggo ? '' : (resources || ''), v: v, s: scriggo ? null : './schemas.json', p: el.dataset.scenario || '' };
    var frag = '#s=' + await gzipB64(JSON.stringify(state));
    var qs = ['embed=1'];
    if (scriggo) {
      qs.push('scriggo=1');
    } else {
      if (el.dataset.controls) qs.push('controls=' + encodeURIComponent(el.dataset.controls));
      if (el.dataset.tab) qs.push('tab=' + encodeURIComponent(el.dataset.tab));
      if (el.dataset.focus) qs.push('focus=' + encodeURIComponent(el.dataset.focus));
    }
    return embedBase(el) + '?' + qs.join('&') + frag;
  }

  // Resolve config + resources for a run (inline block or preset fetch).
  async function resolveScenario(el, configOverride) {
    var base = embedBase(el);
    var config = configOverride;
    if (config == null) {
      if (el.dataset.scenario) config = await fetchText(base + 'presets/' + el.dataset.scenario + '.config.yaml');
      else config = codeText(el._configBlock);
    }
    var resources = '';
    var resPreset = el.dataset.resources || el.dataset.scenario;
    if (el._resourcesBlock) resources = codeText(el._resourcesBlock);
    else if (resPreset) resources = await fetchText(base + 'presets/' + resPreset + '.resources.yaml').catch(function () { return ''; });
    return { config: config, resources: resources };
  }

  async function run(el, configOverride) {
    var frame = el.querySelector('.pg-frame');
    var loading = el.querySelector('.pg-loading');
    if (loading) loading.hidden = false;
    el.classList.add('pg-running');
    try {
      var sc = await resolveScenario(el, configOverride);
      var src = await buildSrc(el, sc.config, sc.resources);
      // Force a reload even when a re-run (e.g. "Load solution") changes only the
      // #s= fragment — changing an iframe's hash alone does not reload it.
      el._runN = (el._runN || 0) + 1;
      src = src.replace('?', '?_r=' + el._runN + '&');
      if (!frame) {
        frame = document.createElement('iframe');
        frame.className = 'pg-frame';
        frame.setAttribute('loading', 'lazy');
        frame.setAttribute('title', el.dataset.title || 'HAPTIC playground');
        frame.style.height = (parseInt(el.dataset.height, 10) || 460) + 'px';
        el.appendChild(frame);
      }
      frame.src = src;
    } catch (e) {
      el.classList.remove('pg-running');
      if (loading) { loading.hidden = false; loading.textContent = 'Could not load the playground: ' + e.message; }
      if (el._runBtn) { el._runBtn.disabled = false; el._runBtn.textContent = '▶ Run live'; }  // let them retry
      return false;
    }
    if (loading) loading.hidden = true;
    if (el._runBtn) el._runBtn.hidden = true;   // the iframe is the content now
    return true;
  }

  function enhance(el) {
    if (el._pgReady) return;
    el._pgReady = true;

    // Find the config block: first highlight/code block NOT inside a details
    // (hint/solution). Descendant search is robust to md_in_html wrapping.
    var blocks = [].slice.call(el.querySelectorAll('.highlight, pre.highlight, .highlighttable'))
      .filter(function (b) { return !b.closest('details'); });
    el._configBlock = blocks[0] || null;
    el._resourcesBlock = blocks[1] || null; // an optional second block = resources
    // Only the config is shown in the facade. A resources block is still read (for
    // the run) but hidden — the reader sees it in the playground's Resources pane.
    // Without this it lingers, unstyled, below the iframe after Run.
    for (var i = 1; i < blocks.length; i++) blocks[i].style.display = 'none';

    // Re-highlight the shown config: mkdocs renders the HAProxy config inside a
    // `template: |` block as a flat YAML scalar; this matches the live editor. A
    // scriggo embed's block is a bare template, so highlight all of it as HAProxy.
    if (el._configBlock) {
      var codeEl = el._configBlock.querySelector('pre code') || el._configBlock.querySelector('code');
      if (codeEl) codeEl.innerHTML = highlightConfigHTML(codeText(el._configBlock), 'scriggo' in el.dataset);
      var wrap = document.createElement('div');
      wrap.className = 'pg-embed-code';
      el._configBlock.parentNode.insertBefore(wrap, el._configBlock);
      wrap.appendChild(el._configBlock);
    }

    // Header with title, difficulty, Run button.
    var head = document.createElement('div');
    head.className = 'pg-embed-head';
    var title = document.createElement('span');
    title.className = 'pg-embed-title';
    title.textContent = el.dataset.title || 'Live example';
    head.appendChild(title);
    var diff = stars(el.dataset.difficulty);
    if (diff) { var d = document.createElement('span'); d.className = 'pg-embed-diff'; d.textContent = diff; head.appendChild(d); }
    var sp = document.createElement('span'); sp.className = 'pg-embed-spacer'; head.appendChild(sp);
    var runBtn = document.createElement('button');
    runBtn.type = 'button';
    runBtn.className = 'pg-btn pg-btn-play';
    runBtn.textContent = '▶ Run live';
    runBtn.addEventListener('click', function () { runBtn.disabled = true; runBtn.textContent = 'Loading…'; run(el); });
    head.appendChild(runBtn);
    el._runBtn = runBtn;   // run() restores it on failure, hides it once the iframe is up
    el.insertBefore(head, el.firstChild);

    // A hidden loading line for the iframe stage.
    var loading = document.createElement('div');
    loading.className = 'pg-loading';
    loading.hidden = true;
    loading.textContent = 'Loading the playground…';
    el.appendChild(loading);

    // Challenge: a "Load solution" button inside a <details class="pg-solution">.
    var sol = el.querySelector('details.pg-solution');
    if (sol) {
      var solBlock = sol.querySelector('.highlight, pre, .highlighttable');
      if (solBlock) {
        var loadSol = document.createElement('button');
        loadSol.type = 'button';
        loadSol.className = 'pg-btn pg-btn-ghost';
        loadSol.style.margin = '0 0.8rem 0.6rem';
        loadSol.textContent = '▶ Load solution';
        loadSol.addEventListener('click', function () { run(el, codeText(solBlock)); });
        sol.appendChild(loadSol);
      }
    }
  }

  function init() {
    [].forEach.call(document.querySelectorAll('.pg-embed'), enhance);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
  // mkdocs-material instant navigation: re-run on page change if available.
  if (window.document$ && typeof window.document$.subscribe === 'function') window.document$.subscribe(init);
})();
