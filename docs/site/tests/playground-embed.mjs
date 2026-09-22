import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { after, before, test } from 'node:test';
import { chromium } from '../../../scripts/visual-qa/node_modules/playwright/index.mjs';

const fixture = `<!doctype html><html lang="en"><head>
<title>Documentation example</title><link rel="stylesheet" href="/embed.css">
<style>body { margin:0; font:16px sans-serif } main { max-width:640px; margin:auto }
header { position:fixed; top:0; background:white; z-index:99 }
.spacer { height:900px }</style></head><body>
<header>Site navigation</header><main><div class="spacer"></div>
<div class="pg-embed" data-scenario="ingress" data-controls="tabs,resources" data-title="Example route">
<p class="pg-task">Change the sample hostname and inspect the routing map.</p>
<details class="pg-solution"><summary>Show solution</summary>
<pre><code>solution config</code></pre><button id="copy-example">Copy example</button></details></div>
<div class="spacer"></div></main>
<script>document.querySelector("#copy-example").onclick = () => window.exampleCopied = true;
window.document$ = { subscribe: (callback) => window.navigate = callback };</script>
<script src="/embed.js"></script></body></html>`;

// Only the frame's engine is substituted; the embed script and CSS are real.
const frame = `<!doctype html><html><body>
<label>Template <textarea id="editor">initial config</textarea></label>
<button id="open-modal">Compare</button>
<div class="modal" hidden>Comparison</div>
<script>
window.hapticPlayground = { stateReady:true, compareSolution(text) {
 document.querySelector('.modal').textContent = document.querySelector('#editor').value + ' / ' + text;
 document.querySelector('.modal').hidden = false;
}};
document.querySelector('#open-modal').onclick = () => document.querySelector('.modal').hidden = false;
document.addEventListener('keydown', event => {
 if (event.key === 'Escape') document.querySelector('.modal').hidden = true;
});
</script></body></html>`;

let server, browser, base, failFetch = false, frameLoads = 0, realShell = false;
before(async () => {
  const js = await readFile(new URL('../../shared/playground-embed.js', import.meta.url));
  const css = await readFile(new URL('../../shared/playground-embed.css', import.meta.url));
  const shellFiles = new Map();
  for (const name of ['index.html', 'editor.js', 'migration-assets.mjs', 'tryout.js', 'vendor/codemirror.js', 'highlight/config-highlight.bundle.js']) {
    shellFiles.set('/playground/dev/' + name, await readFile(new URL('../../../cmd/playground/web/' + name, import.meta.url)));
  }
  server = createServer((req, res) => {
    const path = new URL(req.url, 'http://localhost').pathname;
    if (path === '/embed.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(js); }
    else if (path === '/embed.css') { res.setHeader('Content-Type', 'text/css'); res.end(css); }
    else if (path.includes('/presets/')) { res.statusCode = failFetch ? 503 : 200; res.end('sample config'); }
    else if (path === '/playground/dev/') { frameLoads++; res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end(realShell ? shellFiles.get('/playground/dev/index.html') : frame); }
    else if (shellFiles.has(path)) { res.setHeader('Content-Type', 'text/javascript'); res.end(shellFiles.get(path)); }
    else if (path.endsWith('/playground.worker.js')) { res.setHeader('Content-Type', 'text/javascript'); res.end('self.onmessage = () => {};'); }
    else if (path === '/resources') { res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end(fixture.replace('data-scenario="ingress"', 'data-scenario="ingress" data-input="resources"')); }
    else if (path === '/') { res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end(fixture); }
    else { res.statusCode = 404; res.end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${server.address().port}`;
  browser = await chromium.launch();
});
after(async () => {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
});

for (const viewport of [
  { width:2560, height:1440 }, { width:1440, height:900 },
  { width:768, height:1024 }, { width:390, height:844 },
]) {
  test(`viewport overlay and preserved edits at ${viewport.width}px`, async () => {
    const page = await browser.newPage({ viewport });
    try {
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      const before = frameLoads;
      await page.goto(base);
      const run = page.getByRole('button', { name:'Run live' });
      await run.scrollIntoViewIfNeeded();
      assert.equal(frameLoads, before, 'frame must load only after Run live');
      const scroll = await page.evaluate(() => scrollY);
      await run.click();
      const dialog = page.getByRole('dialog', { name:'Example route' });
      await dialog.waitFor();
      const editor = page.frameLocator('.pg-frame').getByRole('textbox', { name:'Template' });
      await editor.fill('my edited config');
      assert.equal(await dialog.locator('.pg-embed-placeholder').count(), 0);
      assert.match(await dialog.innerText(), /sample hostname/);
      assert.ok(await dialog.locator('.pg-guide').isVisible(), 'explain the example before showing its editors');
      const bounds = await dialog.boundingBox();
      assert.ok(bounds.width >= viewport.width * .95, JSON.stringify(bounds));
      assert.ok(bounds.height >= viewport.height * .95, JSON.stringify(bounds));
      assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= viewport.width + 1);
      const iframeBounds = await dialog.locator('iframe').boundingBox();
      assert.ok(iframeBounds.height >= viewport.height * .45, JSON.stringify(iframeBounds));
      await page.mouse.move(bounds.x + 4, bounds.y + 4);
      await page.mouse.wheel(0, 700);
      assert.deepEqual(await dialog.boundingBox(), bounds);
      await dialog.getByRole('button', { name:'Back to guide' }).click();
      assert.equal(await page.evaluate(() => scrollY), scroll);
      assert.equal(await run.evaluate(el => el === document.activeElement), true);
      await run.click();
      assert.equal(await editor.inputValue(), 'my edited config');
      assert.equal(frameLoads, before + 1, 'reopening must preserve the same iframe');
      await editor.press('Escape');
      assert.equal(await page.locator('.pg-dialog[open]').count(), 0);
      assert.deepEqual(errors, []);
    } finally { await page.close(); }
  });
}

test('load failure leaves a retryable example', async () => {
  const page = await browser.newPage();
  try {
    failFetch = true;
    await page.goto(base);
    await page.getByRole('button', { name:'Run live' }).click();
    await page.getByText('Could not load the playground:', { exact:false }).waitFor();
    assert.equal(await page.locator('.pg-dialog[open]').count(), 0);
    failFetch = false;
    await page.getByRole('button', { name:'Run live' }).click();
    await page.frameLocator('.pg-frame').getByRole('textbox', { name:'Template' }).waitFor();
  } finally { failFetch = false; await page.close(); }
});

test('comparison uses edits and Escape closes the innermost dialog', async () => {
  const page = await browser.newPage();
  try {
    await page.goto(base);
    await page.getByRole('button', { name:'Run live' }).click();
    const dialog = page.getByRole('dialog');
    const editor = page.frameLocator('.pg-frame').getByRole('textbox', { name:'Template' });
    await editor.fill('edited before comparison');
    await dialog.getByText('Show solution', { exact:true }).click();
    await dialog.getByRole('button', { name:'Copy example' }).click();
    assert.equal(await page.evaluate(() => window.exampleCopied), true, 'details retain their existing event handlers');
    assert.equal(await page.locator('#copy-example').count(), 1, 'details must not duplicate IDs');
    await dialog.getByRole('button', { name:'Compare with my config' }).click();
    const modal = page.frameLocator('.pg-frame').locator('.modal');
    await modal.waitFor({ state:'visible' });
    assert.equal(await modal.innerText(), 'edited before comparison / solution config');
    await editor.press('Escape');
    await modal.waitFor({ state:'hidden' });
    assert.equal(await page.locator('.pg-dialog[open]').count(), 1);
    await editor.press('Escape');
    assert.equal(await page.locator('.pg-dialog[open]').count(), 0);
  } finally { await page.close(); }
});

test('instant navigation removes the old editor and unlocks scrolling', async () => {
  const page = await browser.newPage();
  try {
    await page.goto(base);
    await page.getByRole('button', { name:'Run live' }).click();
    await page.frameLocator('.pg-frame').getByRole('textbox', { name:'Template' }).waitFor();
    await page.evaluate(() => window.navigate());
    assert.equal(await page.locator('.pg-dialog .pg-embed-head').count(), 1);
    await page.evaluate(() => { document.querySelector('main').remove(); window.navigate(); });
    assert.equal(await page.locator('.pg-dialog').count(), 0);
    assert.notEqual(await page.evaluate(() => getComputedStyle(document.documentElement).overflow), 'hidden');
  } finally { await page.close(); }
});

test('closing during loading can reopen the same pending example', async () => {
  const page = await browser.newPage();
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  let requests = 0;
  try {
    await page.route('**/presets/ingress.config.yaml', async route => {
      requests++;
      await pending;
      await route.fulfill({ body:'sample config' });
    });
    await page.goto(base);
    const run = page.getByRole('button', { name:'Run live' });
    await run.click();
    await page.getByRole('dialog').getByRole('status').waitFor();
    await page.getByRole('button', { name:'Back to guide' }).click();
    await run.click();
    await page.getByRole('dialog').getByRole('status').waitFor();
    release();
    await page.frameLocator('.pg-frame').getByRole('textbox', { name:'Template' }).waitFor();
    assert.equal(requests, 1);
    assert.equal(await page.getByRole('dialog').getByRole('status').isVisible(), false);
  } finally { release(); await page.close(); }
});

test('resource examples show one input pane and switch to templates', async () => {
  const page = await browser.newPage({ viewport:{ width:1440, height:900 } });
  try {
    realShell = true;
    await page.goto(base + '/resources');
    await page.getByRole('button', { name:'Run live' }).click();
    const shell = page.frameLocator('.pg-frame');
    await shell.getByRole('button', { name:'Resources', exact:true }).waitFor({ state:'visible' });
    const resources = shell.locator('.col.res');
    const templates = shell.locator('.col.config');
    await resources.waitFor({ state:'visible' });
    assert.equal(await templates.isVisible(), false);
    assert.equal(await shell.getByRole('button', { name:'Resources', exact:true }).getAttribute('aria-pressed'), 'true');
    await shell.getByRole('button', { name:'Templates', exact:true }).click();
    await templates.waitFor({ state:'visible' });
    assert.equal(await resources.isVisible(), false);
    await shell.getByRole('button', { name:'Resources', exact:true }).click();
    await shell.getByRole('button', { name:'Maximize Resources panel' }).click();
    assert.equal(await shell.locator('.col.out').isVisible(), false);
    await shell.getByRole('button', { name:'Maximize Resources panel' }).press('Escape');
    assert.equal(await page.locator('.pg-dialog[open]').count(), 1);
    assert.equal(await shell.locator('.col.out').isVisible(), true);
    await shell.getByRole('button', { name:'Templates', exact:true }).click();
    assert.equal(await templates.isVisible(), true);
  } finally { realShell = false; await page.close(); }
});
