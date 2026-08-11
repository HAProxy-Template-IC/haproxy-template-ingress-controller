const safeSourceID = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;

export function createMigrationAssetLoader(fetchText) {
  let manifestPromise;

  async function manifest() {
    manifestPromise ||= fetchText('./migration/presets.json').then((text) => {
      const parsed = JSON.parse(text);
      if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
        throw new Error('Migration preset manifest must be an object.');
      }
      for (const [preset, sources] of Object.entries(parsed)) {
        if (!preset || !Array.isArray(sources)) {
          throw new Error(`Migration preset ${preset || '<empty>'} must declare a source list.`);
        }
        validateSourceIDs(sources);
      }
      return parsed;
    });
    return manifestPromise;
  }

  function validateSourceIDs(ids) {
    const seen = new Set();
    for (const id of ids) {
      if (typeof id !== 'string' || !safeSourceID.test(id)) {
        throw new Error(`Invalid migration source id: ${String(id)}`);
      }
      if (seen.has(id)) throw new Error(`Duplicate migration source id: ${id}`);
      seen.add(id);
    }
    return [...seen];
  }

  async function normalizeSources(ids) {
    if (!Array.isArray(ids)) throw new Error('Migration sources must be a list.');
    const sources = validateSourceIDs(ids);
    const presets = await manifest();
    const known = new Set(Object.values(presets).flat());
    for (const source of sources) {
      if (!known.has(source)) throw new Error(`Unknown migration source id: ${source}`);
    }
    return sources;
  }

  async function sourcesForPreset(id) {
    const presets = await manifest();
    if (!Object.prototype.hasOwnProperty.call(presets, id)) {
      throw new Error(`Migration preset manifest has no entry for ${id}.`);
    }
    return normalizeSources(presets[id]);
  }

  async function sourcesForState(state, presetID) {
    if (Object.prototype.hasOwnProperty.call(state, 'm')) {
      return state.m === null ? null : normalizeSources(state.m);
    }
    return presetID ? sourcesForPreset(presetID) : null;
  }

  async function loadCoverage(ids) {
    const sources = await normalizeSources(ids);
    const entries = await Promise.all(sources.map(async (source) => {
      const entry = JSON.parse(await fetchText(`./migration/${source}.json`));
      if (entry.source !== source) {
        throw new Error(`Migration asset ${source}.json declares source ${String(entry.source)}.`);
      }
      return entry;
    }));
    return { sources, json: JSON.stringify(entries) };
  }

  async function loadOptionalCoverage(resolveSources) {
    try {
      const sources = await resolveSources();
      if (sources === null) return { sources: null, json: null, error: null };
      return { ...await loadCoverage(sources), error: null };
    } catch (error) {
      return { sources: null, json: null, error };
    }
  }

  function coverageForPreset(id) {
    return loadOptionalCoverage(() => sourcesForPreset(id));
  }

  function restoreCoverage(state, presetID) {
    return loadOptionalCoverage(() => sourcesForState(state, presetID));
  }

  return { coverageForPreset, loadCoverage, normalizeSources, restoreCoverage, sourcesForPreset, sourcesForState };
}
