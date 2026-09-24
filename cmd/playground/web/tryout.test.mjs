import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { renderTryoutScript } from './tryout.js';

const template = await readFile(new URL('./tryout-template.sh', import.meta.url), 'utf8');
const config = 'global\n  daemon\ndefaults\n  mode http\nfrontend example\n  bind :8080\n  http-request return status 200\n';

async function fixture(t, result, runtime = 'docker', runtimeExit = 0) {
  const root = await mkdtemp(join(tmpdir(), 'haptic-tryout-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bin = join(root, 'bin');
  await mkdir(bin);
  const runtimePath = join(bin, runtime);
  await writeFile(runtimePath, `#!/bin/sh\n[ "$1" != info ] || exit 0\nprintf '%s\\n' "$@" > "$RUNTIME_ARGS"\nexit ${runtimeExit}\n`, { mode: 0o755 });
  if (runtime === 'podman') await writeFile(join(bin, 'docker'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  const script = join(root, 'tryout.sh');
  await writeFile(script, renderTryoutScript(template, result, '3.4'));
  const argsFile = join(root, 'runtime-args');
  return {
    root,
    run(args = [], input = '') {
      return spawnSync('bash', [script, ...args], {
        cwd: root, input, encoding: 'utf8', timeout: 10000,
        env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, HAPTIC_TRYOUT_DIR: join(root, 'files'), RUNTIME_ARGS: argsFile },
      });
    },
    runtimeArgs: () => readFile(argsFile, 'utf8'),
  };
}

function passed(result) {
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stdout + result.stderr);
}

test('writes every rendered file without changing its contents', async (t) => {
  const result = {
    haproxyCfg: config,
    maps: { 'host.map': 'shop.example.com shop\n', 'empty.map': '' },
    files: { '400.http': 'HTTP/1.1 400 Bad Request\r\n\r\nBad request — ü\n' },
    certs: { 'site.pem': 'certificate\nkey\n' },
    crtLists: { 'general/ingress.crt-list': 'site.pem shop.example.com\n' },
  };
  const f = await fixture(t, result);
  passed(f.run(['files']));
  for (const [path, content] of Object.entries({
    'haproxy.cfg': config, 'maps/host.map': result.maps['host.map'], 'maps/empty.map': '',
    'general/400.http': result.files['400.http'], 'ssl/site.pem': result.certs['site.pem'],
    'general/ingress.crt-list': result.crtLists['general/ingress.crt-list'],
  })) assert.equal(await readFile(join(f.root, 'files', path), 'utf8'), content, path);
});

for (const runtime of ['docker', 'podman']) {
  test(`menu option 2 reaches ${runtime} without a hostname map`, async (t) => {
    const f = await fixture(t, { haproxyCfg: config }, runtime);
    const result = f.run([], '2\n');
    passed(result);
    assert.match(result.stdout, /Host: your-app\.example\.com/);
    const args = await f.runtimeArgs();
    assert.match(args, /haproxy\n-db\n-f\n\/etc\/haproxy\/haproxy.cfg/);
    assert.match(args, /-i\n/);
    assert.doesNotMatch(args, /-it\n/);
    assert.match(args, /-p\n127\.0\.0\.1:8080:8080\n/);
  });
}

test('run accepts Unix-socket-only frontends with no published TCP ports', async (t) => {
  const f = await fixture(t, { haproxyCfg: config.replace('bind :8080', 'bind /etc/haproxy/http.sock') });
  passed(f.run(['run']));
  assert.doesNotMatch(await f.runtimeArgs(), /\n-p\n/);
});

test('run discovers all comma-separated and IPv6 bind ports once', async (t) => {
  const f = await fixture(t, { haproxyCfg: config.replace('bind :8080', 'bind :8080,[::1]:8081\n  bind 127.0.0.1:8080') });
  passed(f.run(['run']));
  const args = await f.runtimeArgs();
  assert.equal((args.match(/8080:8080/g) || []).length, 1);
  assert.equal((args.match(/8081:8081/g) || []).length, 1);
});

test('empty and large hostname maps do not terminate the script', async (t) => {
  const f = await fixture(t, { haproxyCfg: config, maps: {
    'a-host.map': '# no hosts yet\n',
    'host.map': 'shop.example.com shop\n'.repeat(10000),
  } });
  const result = f.run(['run']);
  passed(result);
  assert.match(result.stdout, /Host: shop\.example\.com/);
});

test('validation rejects an invalid config with the runtime exit status visible', async (t) => {
  const f = await fixture(t, { haproxyCfg: config }, 'docker', 1);
  const result = f.run(['check']);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Config is INVALID/);
  assert.match(await f.runtimeArgs(), /haproxy\n-c\n-f\n/);
});

test('run preserves container startup failures', async (t) => {
  const f = await fixture(t, { haproxyCfg: config }, 'docker', 42);
  assert.equal(f.run(['run']).status, 42);
});
