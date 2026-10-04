import test from 'node:test';
import assert from 'node:assert/strict';

import { configMapSectionHTML, inlineMarkdownHTML } from './migration-report.mjs';

const CLS = { supported: 'ok', different: 'warn', dropped: 'bad', unknown: 'dim' };

test('renders coverage-note code spans and docs links', () => {
  assert.equal(
    inlineMarkdownHTML('Set `a<b>` — see [the `x` page](libraries/base.md#timeouts) or [home](index.md).'),
    'Set <code>a&lt;b&gt;</code> — see '
      + '<a href="https://haproxy-haptic.org/docs/libraries/base/#timeouts" target="_blank" rel="noopener">the <code>x</code> page</a>'
      + ' or <a href="https://haproxy-haptic.org/docs/" target="_blank" rel="noopener">home</a>.',
  );
});

test('escapes everything outside code spans and links', () => {
  assert.equal(inlineMarkdownHTML('<script>"x"</script>'), '&lt;script&gt;&quot;x&quot;&lt;/script&gt;');
  assert.equal(
    inlineMarkdownHTML('[x](https://example.com/?a="b")'),
    '<a href="https://example.com/?a=&quot;b&quot;" target="_blank" rel="noopener">x</a>',
  );
});

test('renders attention keys with setting and note, supported keys as chips', () => {
  const html = configMapSectionHTML([{
    namespace: 'ingress-nginx',
    name: 'ingress-nginx-controller',
    findings: [
      { key: 'hsts', value: 'true', status: 'different', setting: '`extraContext.tls.hsts.enabled`', note: 'Off by default.' },
      { key: 'keep-alive', value: '75', status: 'supported', setting: '`extraContext.timeout_http_keep_alive`' },
      { key: 'lua-shared-dicts', value: 'x', status: 'unknown', note: 'Not in the table.' },
    ],
  }], CLS);

  assert.match(html, /ingress-nginx\/<b>ingress-nginx-controller<\/b>/);
  assert.match(html, /<span class="mig-badge warn">different<\/span><code class="mig-ann">hsts: true<\/code>/);
  assert.match(html, /HAPTIC: <code>extraContext.tls.hsts.enabled<\/code>/);
  assert.match(html, /<span class="mig-badge dim">unknown<\/span><code class="mig-ann">lua-shared-dicts: x<\/code>/);
  assert.match(html, /1 key carries over/);
  assert.match(html, /<code class="mig-chip" title="HAPTIC: `extraContext.timeout_http_keep_alive`">keep-alive<\/code>/);
  assert.match(html, /is short for <code>controller.config.templatingSettings.extraContext.<\/code>/);
});

test('surfaces value and data problems', () => {
  const html = configMapSectionHTML([
    { namespace: 'a', name: 'c1', findings: [
      { key: 'keep-alive', value: '75', status: 'supported', problem: 'Quote it.' },
      { key: 'hsts', value: 'true', status: 'different', note: 'Off.', problem: 'Quote <it>.' },
    ] },
    { namespace: 'a', name: 'c2', problem: 'data is a list.', findings: [] },
  ], CLS);
  assert.match(html, /⚠ <code>keep-alive<\/code>: Quote it\./);
  assert.match(html, /⚠ <code>hsts<\/code>: Quote &lt;it&gt;\./);
  assert.match(html, /mig-problem">⚠ data is a list\.<\/div>/);
  assert.doesNotMatch(html, /no data keys/);
});

test('renders nothing without ConfigMaps', () => {
  assert.equal(configMapSectionHTML(undefined, CLS), '');
  assert.equal(configMapSectionHTML([], CLS), '');
});
