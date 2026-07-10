// Render worker — keeps the wasm engine off the UI thread with a warm-engine
// protocol:
//   { type: 'load',   seq, config, schemas, haproxyVersion, resources }
//        -> hapticLoadConfig(...) (recompile) then hapticRender(resources)
//   { type: 'render', seq, resources }
//        -> hapticRender(resources) against the already-compiled warm engine
// Both reply with { type: 'result', seq, res } or { type: 'result', seq, error }.
// Send 'load' when the config / schema bundle / HAProxy version changes; send
// 'render' for resource-only edits so they skip template recompilation.

/* global Go, importScripts */
importScripts('./wasm_exec.js'); // defines Go

const go = new Go();
let ready = false;

// Download the wasm, streaming progress to the UI so a slow (hosted) fetch shows
// a loading bar. Content-Length is the on-the-wire size; when the server sends it
// brotli/gzip-compressed the browser hands us decompressed chunks that can exceed
// it, so the UI treats loaded > total as "size unknown" and shows MB instead.
async function fetchWasmWithProgress() {
  const resp = await fetch('./playground.wasm');
  if (!resp.ok) throw new Error('playground.wasm: HTTP ' + resp.status);
  const total = +resp.headers.get('Content-Length') || 0;
  if (!resp.body || !total) {
    postMessage({ type: 'progress', loaded: 0, total: 0 });   // indeterminate
    return resp.arrayBuffer();
  }
  const reader = resp.body.getReader();
  const chunks = [];
  let loaded = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    loaded += value.length;
    postMessage({ type: 'progress', loaded, total });
  }
  const bytes = new Uint8Array(loaded);
  let off = 0;
  for (const c of chunks) { bytes.set(c, off); off += c.length; }
  return bytes.buffer;
}

async function init() {
  const bytes = await fetchWasmWithProgress();
  postMessage({ type: 'progress', loaded: 1, total: 1 });   // download done → instantiating
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance); // registers globalThis.hapticRender, then blocks on select{}
  ready = typeof globalThis.hapticRender === 'function';
  postMessage(ready ? { type: 'ready' } : { type: 'error', error: 'hapticRender was not registered' });
}

onmessage = (e) => {
  const m = e.data || {};
  if (m.type !== 'load' && m.type !== 'render' && m.type !== 'runtests') return;
  if (!ready) {
    postMessage({ type: 'result', seq: m.seq, error: 'engine not ready' });
    return;
  }
  // Run the config's spec.validationTests against the already-warm engine.
  // Independent of the resources pane — each test carries its own fixtures.
  if (m.type === 'runtests') {
    try {
      const out = globalThis.hapticRunTests();
      if (out && out.error) postMessage({ type: 'testresult', seq: m.seq, error: out.error });
      else postMessage({ type: 'testresult', seq: m.seq, res: out });
    } catch (err) {
      postMessage({ type: 'testresult', seq: m.seq, error: 'wasm panic: ' + String((err && err.message) || err) });
    }
    return;
  }
  // hapticLoadConfig / hapticRender each return EITHER { error } on failure OR a
  // plain object on success. Hoist any error to the top-level `error` field the
  // UI expects; the successful render object becomes `res`. (Without this, every
  // error would be silently swallowed into an empty output.)
  try {
    if (m.type === 'load') {
      // Fresh loads (preset/share/restore) drop the reload-impact baseline; a plain
      // config edit keeps it so the edit is measured against the pinned/last render.
      if (m.resetBaseline && typeof globalThis.hapticResetBaseline === 'function') globalThis.hapticResetBaseline();
      const loaded = globalThis.hapticLoadConfig(m.config, m.schemas || '', m.haproxyVersion || '3.2');
      if (loaded && loaded.error) { postMessage({ type: 'result', seq: m.seq, error: loaded.error }); return; }
    }
    // Reload-impact baseline toggle (pin the last render / unpin), applied before
    // this render so the returned reloadImpact is computed against the new baseline.
    if (m.baseline === 'pin' && typeof globalThis.hapticPinBaseline === 'function') globalThis.hapticPinBaseline();
    else if (m.baseline === 'unpin' && typeof globalThis.hapticUnpinBaseline === 'function') globalThis.hapticUnpinBaseline();
    const out = globalThis.hapticRender(m.resources || '');
    if (out && out.error) postMessage({ type: 'result', seq: m.seq, error: out.error });
    else postMessage({ type: 'result', seq: m.seq, res: out });
  } catch (err) {
    postMessage({ type: 'result', seq: m.seq, error: 'wasm panic: ' + String((err && err.message) || err) });
  }
};

init().catch((err) => postMessage({ type: 'error', error: String((err && err.stack) || err) }));
