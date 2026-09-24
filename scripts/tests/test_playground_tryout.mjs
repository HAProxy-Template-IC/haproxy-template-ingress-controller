import assert from 'node:assert/strict';
import { readFile, writeFile, mkdtemp, rm } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { tmpdir } from 'node:os';
import { spawn, spawnSync } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';

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
const { renderTryoutScript } = await import(pathToFileURL(join(bundle, 'tryout.js')));
await import(pathToFileURL(join(bundle, 'wasm_exec.js')));
const go = new globalThis.Go();
const wasm = await WebAssembly.instantiate(await readFile(join(bundle, 'playground.wasm')), go.importObject);
go.run(wasm.instance);
const schemas = await readFile(join(bundle, 'schemas.json'), 'utf8');
const template = await readFile(join(bundle, 'tryout-template.sh'), 'utf8');
const root = await mkdtemp(join(tmpdir(), 'haptic-playground-export-'));
try {
  for (const preset of ['starter', 'crd', 'ingress']) {
    const prefix = preset === 'ingress' ? `presets/${preset}` : preset;
    const config = await readFile(join(bundle, `${prefix}.config.yaml`), 'utf8');
    const resources = await readFile(join(bundle, `${prefix}.resources.yaml`), 'utf8');
    const loaded = globalThis.hapticLoadConfig(config, schemas, '3.4');
    assert.equal(loaded.error, undefined, loaded.error);
    const result = globalThis.hapticRender(resources);
    assert.equal(result.error, undefined, result.error);
    assert.ok(Object.keys(result.maps).length > 0, `${preset}: missing rendered maps`);
    if (preset === 'ingress') {
      assert.ok(Object.keys(result.files).length > 0, 'missing rendered error files');
      assert.ok(Object.keys(result.certs).length > 0, 'missing rendered certificates');
    }
    const script = join(root, `${preset}.sh`);
    const workdir = join(root, preset);
    await writeFile(script, renderTryoutScript(template, result, '3.4'));
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
    console.log(`${preset}: WASM render, complete export, native validation, and menu option 2 HTTP response passed`);
  }
} finally {
  await rm(root, { recursive: true, force: true });
}
process.exit(0);
