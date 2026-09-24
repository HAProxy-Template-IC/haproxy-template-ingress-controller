import assert from 'node:assert/strict';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { resolve, join, extname, sep } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn, spawnSync } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import { chromium } from '../visual-qa/node_modules/playwright/index.mjs';

const runtime = ['docker', 'podman'].find((name) => spawnSync(name, ['info'], { stdio: 'ignore' }).status === 0);
assert.ok(runtime, 'Docker or Podman must be running');

async function checkLocalRun(script, workdir, url, status) {
  assert.notEqual(spawnSync(runtime, ['inspect', 'haptic-tryout'], { stdio: 'ignore' }).status, 0,
    'Stop the existing haptic-tryout container before running this test');
  const child = spawn('bash', [script], { env: { ...process.env, HAPTIC_TRYOUT_DIR: workdir } });
  let output = '';
  child.stdout.on('data', (chunk) => { output += chunk; });
  child.stderr.on('data', (chunk) => { output += chunk; });
  const exited = new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('close', resolve);
  });
  child.stdin.end('2\n');
  try {
    const deadline = Date.now() + 20000;
    while (Date.now() < deadline) {
      assert.equal(child.exitCode, null, output);
      let response;
      try { response = await fetch(url, { headers: { Connection: 'close' }, signal: AbortSignal.timeout(1000) }); } catch {}
      if (response) {
        await response.arrayBuffer();
        assert.equal(response.status, status, output);
        return;
      }
      await delay(100);
    }
    assert.fail(`HAProxy did not answer ${url}\n${output}`);
  } finally {
    const inspected = spawnSync(runtime, ['inspect', 'haptic-tryout'], { encoding: 'utf8' });
    if (inspected.status === 0) {
      const container = JSON.parse(inspected.stdout)[0];
      assert.ok(container.Mounts.some((mount) => mount.Source === workdir), 'Refusing to stop a different container');
      const stopped = spawnSync(runtime, ['stop', '--signal', 'SIGINT', '--time', '2', container.Id], { encoding: 'utf8', timeout: 10000 });
      assert.equal(stopped.status, 0, stopped.stdout + stopped.stderr);
    }
    const code = await Promise.race([exited, delay(10000).then(() => { throw new Error('Try-out script did not exit'); })]);
    assert.equal(code, 128 + 2, output);
  }
}

const bundle = resolve(process.argv[2]);
const contentTypes = {
  '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript',
  '.wasm': 'application/wasm', '.json': 'application/json',
};
const server = createServer(async (request, response) => {
  const pathname = decodeURIComponent(new URL(request.url, 'http://localhost').pathname);
  const path = resolve(bundle, '.' + (pathname === '/' ? '/index.html' : pathname));
  if (!path.startsWith(bundle + sep)) { response.writeHead(403).end(); return; }
  try {
    const content = await readFile(path);
    response.setHeader('Content-Type', contentTypes[extname(path)] || 'text/plain');
    response.end(content);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const base = `http://127.0.0.1:${server.address().port}`;
const root = await mkdtemp(join(tmpdir(), 'haptic-playground-export-'));
let browser;
try {
  browser = await chromium.launch();
  for (const preset of ['starter', 'crd', 'ingress']) {
    const page = await browser.newPage();
    const pageErrors = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    await page.goto(preset === 'starter' ? base : `${base}/?preset=${preset}`);
    if (preset === 'starter') await page.locator('#help-close').click();
    await page.waitForFunction(() => window.hapticPlayground?.stats()?.haproxyCfg
      || document.querySelector('#chip')?.textContent === 'error', null, { timeout: 30000 });
    const result = await page.evaluate(() => window.hapticPlayground.stats());
    assert.ok(result?.haproxyCfg, await page.locator('body').innerText());
    assert.equal(result.error, undefined, result.error);
    assert.equal(await page.locator('#preset').inputValue(), preset);
    assert.ok(Object.keys(result.maps).length > 0, `${preset}: missing rendered maps`);
    if (preset === 'ingress') {
      assert.ok(Object.keys(result.files).length > 0, 'missing rendered error files');
      assert.ok(Object.keys(result.certs).length > 0, 'missing rendered certificates');
    }
    const script = join(root, `${preset}.sh`);
    const workdir = join(root, preset);
    await page.locator('#tryout').click();
    const pendingDownload = page.waitForEvent('download');
    await page.locator('#tryout-download').click();
    const download = await pendingDownload;
    await download.saveAs(script);
    if (preset === 'starter') {
      await page.waitForFunction(() => localStorage.getItem('haptic-playground:last'));
      const savedConfig = await page.evaluate(() => {
        const state = JSON.parse(localStorage.getItem('haptic-playground:last'));
        state.s = null;
        localStorage.setItem('haptic-playground:last', JSON.stringify(state));
        return state.c;
      });
      await page.reload();
      await page.waitForFunction(() => window.hapticPlayground?.stats()?.haproxyCfg
        || document.querySelector('#chip')?.textContent === 'error', null, { timeout: 30000 });
      assert.equal(await page.evaluate(() => window.hapticPlayground.stats()?.haproxyCfg), result.haproxyCfg);
      assert.equal(await page.evaluate(() => window.hapticPlayground.getConfig()), savedConfig);
    }
    assert.deepEqual(pageErrors, []);
    await page.close();
    const check = spawnSync('bash', [script, 'check'], {
      env: { ...process.env, HAPTIC_TRYOUT_DIR: workdir },
      encoding: 'utf8', timeout: 60000,
    });
    assert.ifError(check.error);
    assert.equal(check.status, 0, check.stdout + check.stderr);
    assert.equal(await readFile(join(workdir, 'haproxy.cfg'), 'utf8'), result.haproxyCfg);
    for (const [kind, directory] of [['maps', 'maps'], ['files', 'general'], ['certs', 'ssl'], ['crtLists', 'general']]) {
      for (const [name, content] of Object.entries(result[kind] || {})) {
        const path = name.includes('/') ? name : `${directory}/${name}`;
        assert.equal(await readFile(join(workdir, path), 'utf8'), content, `${preset}: ${path}`);
      }
    }
    const [url, status] = preset === 'ingress'
      ? ['http://127.0.0.1:8404/healthz', 200]
      : ['http://127.0.0.1:8080/', 404];
    await checkLocalRun(script, workdir, url, status);
    console.log(`${preset}: browser render, downloaded export, native validation, and menu option 2 HTTP response passed`);
  }
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
  await rm(root, { recursive: true, force: true });
}
