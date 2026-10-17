import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import { extname, resolve, sep } from 'node:path';
import * as playwright from '../visual-qa/node_modules/playwright/index.mjs';

const root = resolve(process.argv[2]);
const receipt = JSON.parse(await readFile(resolve(root, 'shared/pages-transport.json'), 'utf8'));
const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript',
  '.wasm': 'application/wasm', '.json': 'application/json', '.css': 'text/css' };
const server = createServer(async (request, response) => {
  try {
    const pathname = decodeURIComponent(new URL(request.url, 'http://localhost').pathname);
    if (pathname === '/favicon.ico') { response.writeHead(204).end(); return; }
    let path = resolve(root, '.' + pathname);
    if (!path.startsWith(root + sep)) { response.writeHead(403).end(); return; }
    if ((await stat(path)).isDirectory()) path = resolve(path, 'index.html');
    response.setHeader('Content-Type', types[extname(path)] || 'application/octet-stream');
    response.end(await readFile(path));
  } catch {
    response.writeHead(404).end();
  }
});
let base = process.env.PAGES_URL?.replace(/\/$/, '');
if (!base) {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${server.address().port}`;
}
try {
  const publishedReceipt = await fetch(`${base}/shared/pages-transport.json`);
  assert.equal(publishedReceipt.status, 200);
  assert.deepEqual(await publishedReceipt.json(), receipt, 'Published site differs from its verified artifact');
  const identities = Object.keys(receipt.published_files).filter(path => path.endsWith('/release-identity.json'));
  for (const path of ['index.html', 'docs/dev/index.html', 'docs/latest/index.html',
    'docs/versions.json', 'playground/versions.json', ...identities]) {
    const expected = receipt.published_files[path];
    assert.ok(expected, `Missing publication evidence for ${path}`);
    const response = await fetch(`${base}/${path}`, { cache: 'no-store' });
    assert.equal(response.status, 200, `Cannot fetch published ${path}`);
    const content = Buffer.from(await response.arrayBuffer());
    assert.equal(content.length, expected.size, `Published size differs: ${path}`);
    assert.equal(createHash('sha256').update(content).digest('hex'), expected.sha256, `Published content differs: ${path}`);
  }
  for (const [path, entries] of [['docs/versions.json', catalog => catalog],
    ['playground/versions.json', catalog => catalog.versions]]) {
    const listed = entries(JSON.parse(await readFile(resolve(root, path), 'utf8'))).map(entry => entry.version);
    assert.deepEqual(listed.filter(version => version.includes('-') && listed.includes(version.split('-')[0])), [],
      `${path} lists pre-releases of a final release`);
  }
  for (const name of (process.env.PAGES_BROWSERS || 'chromium').split(',')) {
    const browser = await playwright[name].launch({ headless: true });
    try {
      for (const engine of Object.keys(receipt.engines).sort()) {
        const version = engine.split('/')[1];
        const context = await browser.newContext();
        const page = await context.newPage();
        const errors = [];
        const requests = [];
        page.on('pageerror', error => errors.push(error.message));
        context.on('request', request => requests.push(request.url()));
        const start = performance.now();
        await page.goto(`${base}/playground/${version}/?preset=starter`);
        await page.waitForFunction(() => window.hapticPlayground?.stats()?.haproxyCfg
          || document.querySelector('#chip')?.textContent === 'error', null, { timeout: 30000 });
        const result = await page.evaluate(() => window.hapticPlayground.stats());
        assert.ok(result?.haproxyCfg?.includes('frontend http'), await page.locator('body').innerText());
        assert.ok(Object.keys(result.maps).length > 0, `${version}: rendered map missing`);
        assert.equal(result.error, undefined);
        assert.deepEqual(errors, []);
        assert.ok(requests.includes(`${base}/${engine}.br`), `${version}: Brotli engine was not requested`);
        assert.ok(!requests.some(url => new URL(url).pathname.endsWith('/playground.wasm')));
        assert.ok(requests.every(url => url.startsWith(base + '/')), `${version}: external runtime request`);
        console.log(JSON.stringify({ browser: name, version, sha256: receipt.engines[engine].sha256,
          ready_ms: Math.round(performance.now() - start), result: 'rendered' }));
        await context.close();
      }
      const page = await browser.newPage();
      await page.goto(`${base}/playground/dev/?preset=starter`);
      const engine = 'playground/dev/playground.wasm';
      const expected = { compressed: receipt.published_files[engine + '.br'], decoded: receipt.engines[engine] };
      const decoderMemory = await page.evaluate(async ({ engine, expected, base }) => {
        const native = globalThis.DecompressionStream;
        try {
          globalThis.DecompressionStream = undefined;
          const { fetchBrotliWasm } = await import(`${base}/shared/playground-wasm-loader.mjs`);
          await fetchBrotliWasm(`${base}/${engine}.br`, expected, () => {});
          const brotli = await import(`${base}/shared/brotli-dec-wasm-2.3.2/brotli_dec_wasm.js`);
          return (await brotli.default()).memory.buffer.byteLength;
        } finally {
          globalThis.DecompressionStream = native;
        }
      }, { engine, expected, base });
      assert.ok(decoderMemory < 32 * 1024 * 1024, `Decoder retained ${decoderMemory} bytes`);
      console.log(JSON.stringify({ browser: name, case: 'bounded-fallback-memory', bytes: decoderMemory, result: 'passed' }));
      for (const kind of ['truncated', 'corrupted', 'wrong-decoded-digest', 'missing']) {
        await page.route(`**/${engine}.br`, async route => {
          const bytes = await readFile(resolve(root, engine + '.br'));
          if (kind === 'corrupted') bytes[bytes.length - 1] ^= 1;
          await route.fulfill({ status: kind === 'missing' ? 404 : 200,
            body: kind === 'truncated' ? bytes.subarray(0, -1) : bytes });
        });
        const wanted = structuredClone(expected);
        if (kind === 'wrong-decoded-digest') wanted.decoded.sha256 = '0'.repeat(64);
        const error = await page.evaluate(async ({ engine, wanted, base }) => {
          const { fetchBrotliWasm } = await import(`${base}/shared/playground-wasm-loader.mjs`);
          try { await fetchBrotliWasm(`${base}/${engine}.br`, wanted, () => {}); return null; }
          catch (error) { return error.message; }
        }, { engine, wanted, base });
        assert.match(error || '', /checksum mismatch|incomplete|HTTP 404/, kind);
        console.log(JSON.stringify({ browser: name, case: kind, result: 'rejected' }));
        await page.unroute(`**/${engine}.br`);
      }
      await page.goto(`${base}/controller/latest/index.html?preserved=1#preserved`);
      await page.waitForURL(`${base}/docs/latest/?preserved=1#preserved`);
      await page.goto(`${base}/controller/latest/getting-started/index.html?preserved=1#preserved`);
      await page.waitForURL(`${base}/docs/latest/getting-started/index.html?preserved=1#preserved`);
      for (const [from, to] of Object.entries(receipt.prerelease_redirects)) {
        await page.goto(`${base}/${from}index.html?preserved=1#preserved`);
        await page.waitForURL(`${base}/${to}?preserved=1#preserved`);
        console.log(JSON.stringify({ browser: name, redirect: from, target: to, result: 'redirected' }));
      }
      await page.close();
    } finally {
      await browser.close();
    }
  }
} finally {
  if (server.listening) await new Promise(resolve => server.close(resolve));
}
