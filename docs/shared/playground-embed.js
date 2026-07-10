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
 *   data-height     minimum running-iframe height in px (default 460); the running
 *                   embed breaks out of the article column and grows to ~76vh
 *   data-title      header label
 *   data-difficulty 1-3 -> shown as stars
 *
 * Challenge: add a `<details class="pg-solution">` containing a solution config
 * block; a "Compare with my config" button opens the playground's side-by-side
 * diff of the reader's current config vs the solution (applying it is an
 * explicit action inside that view).
 */
(function () {
  'use strict';
  function withSlash(b) { return b.slice(-1) === '/' ? b : b + '/'; }

  // Which playground bundle an embed targets. A versioned docs page
  // (/docs/<v>/…) uses the matching /playground/<v>/ so the shown render matches
  // that release's engine; anything else (the "latest" alias, the landing page)
  // falls back to the dev shell. Override per-embed with data-base, or per-site
  // with window.HAPTIC_PG_BASE.
  function embedBase(el) {
    if (el.dataset.base) return withSlash(el.dataset.base);
    if (window.HAPTIC_PG_BASE) return withSlash(window.HAPTIC_PG_BASE);
    var m = location.pathname.match(/\/docs\/([^/]+)\//);
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

  /* --- facade config highlighting (shares the live editor's grammar) ---
   * mkdocs renders the config inside a `template: |` block as one flat YAML
   * string, and its own YAML lexer miscolours header lines in a files block
   * (e.g. `Cache-Control: no-cache`) as keys. Re-highlight it with the SAME
   * Lezer grammar the live editor uses (cmd/playground/web/highlight/), shipped
   * as a self-contained ESM bundle deployed beside this script. One grammar
   * drives both surfaces, so the shown config and the live editor match by
   * construction. If the bundle fails to load, mkdocs' YAML stays as fallback. */
  var SELF = document.currentScript && document.currentScript.src;
  var HL_URL = (SELF ? SELF.replace(/[^/]*$/, '') : '/shared/') + 'config-highlight.bundle.js';
  var _hl;   // cached import promise → the highlight module, or null if it fails
  function loadHL() {
    if (!_hl) _hl = import(HL_URL).catch(function () { return null; });
    return _hl;
  }
  // Re-highlight a config <code> element once the bundle loads. `scriggo` picks
  // the bare-template parser (HAProxy + tags) over the full-config parser.
  function highlightConfig(codeEl, text, scriggo) {
    loadHL().then(function (m) {
      if (m) codeEl.innerHTML = scriggo ? m.highlightTemplateToHTML(text) : m.highlightToHTML(text);
    });
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
        // Height comes from CSS: max(--pg-hmin, min(76vh, 880px)) — the author's
        // data-height is only the minimum (see .pg-embed.pg-running .pg-frame).
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
    if (el._closeBtn) el._closeBtn.hidden = false;
    el._loaded = true;                          // Close keeps the iframe; reopening is instant
    el.scrollIntoView({ block: 'nearest' });    // the box just grew — keep its header on screen
    return true;
  }

  // Open the solution comparison inside the running playground. Starts the
  // playground first if needed — comparing against the STARTING config is the
  // point then: the diff reads as "what the solution adds". The iframe is
  // same-origin (docs and playground ship from one site), so the shell's
  // hapticPlayground API is reachable directly; a versioned playground too old
  // to have compareSolution falls back to loading the solution outright.
  async function compareSolution(el, solution) {
    if (!el._loaded) {
      if (!(await run(el))) return;
    } else if (!el.classList.contains('pg-running')) {
      el.classList.add('pg-running');
      if (el._runBtn) el._runBtn.hidden = true;
      if (el._closeBtn) el._closeBtn.hidden = false;
    }
    el.scrollIntoView({ block: 'nearest' });
    var frame = el.querySelector('.pg-frame');
    var t0 = Date.now();
    (function poll() {
      var api = null;
      try { api = frame.contentWindow && frame.contentWindow.hapticPlayground; } catch (e) { /* treat as not ready */ }
      if (api && api.compareSolution) {
        // New shell: also wait for the initial config to be IN the editor
        // (stateReady) so the diff never runs against an empty pane.
        if (api.stateReady) { api.compareSolution(solution); return; }
      } else if (api && api.setConfig && Date.now() - t0 > 4000) {
        api.setConfig(solution); return;   // old versioned shell without the compare API
      }
      if (Date.now() - t0 < 20000) { setTimeout(poll, 250); return; }
      // Timed out: tell the reader instead of dead-ending silently.
      var loading = el.querySelector('.pg-loading');
      if (loading) {
        loading.hidden = false;
        loading.textContent = 'could not open the comparison — try again once the playground has rendered';
        setTimeout(function () { loading.hidden = true; }, 5000);
      }
    })();
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
      if (codeEl) highlightConfig(codeEl, codeText(el._configBlock), 'scriggo' in el.dataset);
      var wrap = document.createElement('div');
      wrap.className = 'pg-embed-code';
      el._configBlock.parentNode.insertBefore(wrap, el._configBlock);
      wrap.appendChild(el._configBlock);
    } else if (el.dataset.scenario) {
      // A preset embed fetches its (large) bundled config on Run, so there's no
      // inline block to show. Fill the facade with an inviting placeholder
      // instead of leaving it blank.
      var ph = document.createElement('div');
      ph.className = 'pg-embed-placeholder';
      ph.textContent = 'press ▶ Run live — renders the bundled '
        + el.dataset.scenario.replace(/-/g, ' ') + ' example in your browser, nothing uploaded';
      el.appendChild(ph);
    }

    // Header with title, difficulty, Run button.
    var head = document.createElement('div');
    head.className = 'pg-embed-head';
    var mark = document.createElement('span');
    mark.className = 'pg-embed-mark';
    mark.textContent = '{%%}';
    mark.setAttribute('aria-hidden', 'true');
    head.appendChild(mark);
    var title = document.createElement('span');
    title.className = 'pg-embed-title';
    title.textContent = el.dataset.title || 'Live example';
    head.appendChild(title);
    var diffN = Math.max(0, Math.min(3, parseInt(el.dataset.difficulty, 10) || 0));
    if (diffN) {
      var word = { 1: 'beginner', 2: 'intermediate', 3: 'advanced' }[diffN];
      var d = document.createElement('span'); d.className = 'pg-embed-diff';
      d.textContent = stars(diffN) + ' ' + word;
      d.setAttribute('aria-label', 'Difficulty: ' + word);
      head.appendChild(d);
    }
    var sp = document.createElement('span'); sp.className = 'pg-embed-spacer'; head.appendChild(sp);
    var runBtn = document.createElement('button');
    runBtn.type = 'button';
    runBtn.className = 'pg-btn pg-btn-play';
    runBtn.textContent = '▶ Run live';
    runBtn.addEventListener('click', function () {
      if (el._loaded) {           // already ran once: reopen the live iframe instantly
        el.classList.add('pg-running');
        runBtn.hidden = true;
        if (el._closeBtn) el._closeBtn.hidden = false;
        el.scrollIntoView({ block: 'nearest' });
        return;
      }
      runBtn.disabled = true; runBtn.textContent = 'Loading…'; run(el);
    });
    head.appendChild(runBtn);
    el._runBtn = runBtn;   // run() restores it on failure, hides it once the iframe is up
    // "Close" collapses the running playground back to the clean static example.
    var closeBtn = document.createElement('button');
    closeBtn.type = 'button';
    closeBtn.className = 'pg-btn pg-btn-close';
    closeBtn.textContent = '✕ Close';
    closeBtn.hidden = true;
    closeBtn.addEventListener('click', function () {
      el.classList.remove('pg-running');
      closeBtn.hidden = true;
      runBtn.hidden = false; runBtn.disabled = false; runBtn.textContent = '▶ Run live';
      el.scrollIntoView({ block: 'nearest' });
    });
    head.appendChild(closeBtn);
    el._closeBtn = closeBtn;
    // The author's data-height becomes the running iframe's MINIMUM height (CSS
    // grows it to working size — see .pg-embed.pg-running .pg-frame).
    el.style.setProperty('--pg-hmin', (parseInt(el.dataset.height, 10) || 460) + 'px');
    el.insertBefore(head, el.firstChild);

    // A hidden loading line for the iframe stage.
    var loading = document.createElement('div');
    loading.className = 'pg-loading';
    loading.hidden = true;
    loading.textContent = 'booting the render engine…';
    el.appendChild(loading);

    // Challenge: a "Compare with solution" button inside a <details class="pg-solution">.
    // Non-destructive: it opens the playground's side-by-side diff (your current
    // config vs the solution) so the reader sees what's missing; replacing the
    // editor is an explicit action inside that comparison, not the default.
    var sol = el.querySelector('details.pg-solution');
    if (sol) {
      var solBlock = sol.querySelector('.highlight, pre, .highlighttable');
      if (solBlock) {
        var cmpBtn = document.createElement('button');
        cmpBtn.type = 'button';
        cmpBtn.className = 'pg-btn pg-btn-ghost pg-btn-solution';
        cmpBtn.textContent = '⇆ Compare with my config';
        cmpBtn.addEventListener('click', function () { compareSolution(el, codeText(solBlock)); });
        sol.appendChild(cmpBtn);
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
