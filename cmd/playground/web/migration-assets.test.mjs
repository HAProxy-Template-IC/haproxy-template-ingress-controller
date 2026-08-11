import test from 'node:test';
import assert from 'node:assert/strict';

import { createMigrationAssetLoader } from './migration-assets.mjs';

function loader(files) {
  return createMigrationAssetLoader(async (path) => {
    if (!Object.prototype.hasOwnProperty.call(files, path)) throw new Error(`missing ${path}`);
    return files[path];
  });
}

const files = {
  './migration/presets.json': JSON.stringify({ starter: [], vendor: ['acme'] }),
  './migration/acme.json': JSON.stringify({ source: 'acme', detect: {}, annotations: {} }),
};

test('loads the preset source asset separately', async () => {
  const assets = loader(files);
  assert.deepEqual(await assets.sourcesForPreset('vendor'), ['acme']);
  assert.deepEqual(await assets.loadCoverage(['acme']), {
    sources: ['acme'],
    json: JSON.stringify([JSON.parse(files['./migration/acme.json'])]),
  });
  assert.deepEqual(await assets.coverageForPreset('vendor'), {
    sources: ['acme'],
    json: JSON.stringify([JSON.parse(files['./migration/acme.json'])]),
    error: null,
  });
});

test('requires every selected preset in the manifest', async () => {
  await assert.rejects(loader(files).sourcesForPreset('missing'), /no entry for missing/);
});

test('restores new and legacy shared-state source selection', async () => {
  const assets = loader(files);
  assert.deepEqual(await assets.sourcesForState({ m: ['acme'] }, 'starter'), ['acme']);
  assert.deepEqual(await assets.sourcesForState({}, 'vendor'), ['acme']);
  assert.equal(await assets.sourcesForState({}, null), null);
});

test('contains an unavailable shared-state source to migration coverage', async () => {
  const restored = await loader(files).restoreCoverage({ m: ['other'] }, 'starter');
  assert.equal(restored.sources, null);
  assert.equal(restored.json, null);
  assert.match(restored.error.message, /Unknown migration source id/);
});

test('contains unavailable preset coverage to the optional asset', async () => {
  const missingAsset = { ...files };
  delete missingAsset['./migration/acme.json'];

  const coverage = await loader(missingAsset).coverageForPreset('vendor');
  assert.equal(coverage.sources, null);
  assert.equal(coverage.json, null);
  assert.match(coverage.error.message, /missing .*acme\.json/);
});

test('rejects unsafe, duplicate, and unknown shared-state source ids', async () => {
  const assets = loader(files);
  await assert.rejects(assets.normalizeSources(['../secret']), /Invalid migration source id/);
  await assert.rejects(assets.normalizeSources(['acme', 'acme']), /Duplicate migration source id/);
  await assert.rejects(assets.normalizeSources(['other']), /Unknown migration source id/);
});

test('rejects an asset whose declared source differs from its filename', async () => {
  const bad = { ...files, './migration/acme.json': JSON.stringify({ source: 'other' }) };
  await assert.rejects(loader(bad).loadCoverage(['acme']), /declares source other/);
});
