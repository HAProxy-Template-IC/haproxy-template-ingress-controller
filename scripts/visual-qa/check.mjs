// DOM-geometry visual QA for the HAPTIC landing page.
// Catches: (1) a child overflowing/crowding its container, (2) sibling components
// whose content footprint is inconsistent. These are layout facts, measured from
// getBoundingClientRect — not pixels — so they fire on the FIRST occurrence (no
// baseline needed, unlike screenshot-diff tools).
import { chromium } from 'playwright';

const URL = process.argv[2] || 'http://127.0.0.1:8000/';
const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'mobile',  width: 390,  height: 844 },
];

// Sibling groups whose icon/glyph content should sit comfortably and consistently.
const TILE_GROUPS = [
  { sel: '.rf-tile',  minClearance: 10, label: 'flow node tiles' },
  { sel: '.feat-ico', minClearance: 6,  label: 'feature card icons' },
];
// Containers whose direct children must not exceed the container box.
const OVERFLOW_CONTAINERS = ['.rf-node', '.feat', '.doc-card', '.terminal', '.rf-tile'];

const round = n => Math.round(n * 10) / 10;

// Returns, per element in `sel`, the content footprint + clearance to the inner (content) box.
async function measureTiles(page, sel) {
  return page.$$eval(sel, els => els.map(el => {
    const r = el.getBoundingClientRect();
    const cs = getComputedStyle(el);
    const f = p => parseFloat(cs[p]) || 0;
    const inner = {
      left:   r.left   + f('borderLeftWidth')   + f('paddingLeft'),
      right:  r.right  - f('borderRightWidth')  - f('paddingRight'),
      top:    r.top    + f('borderTopWidth')    + f('paddingTop'),
      bottom: r.bottom - f('borderBottomWidth') - f('paddingBottom'),
    };
    const child = el.querySelector('img, svg');
    let c;
    if (child) { const cr = child.getBoundingClientRect(); c = { left: cr.left, right: cr.right, top: cr.top, bottom: cr.bottom }; }
    else { const rng = document.createRange(); rng.selectNodeContents(el); const cr = rng.getBoundingClientRect(); c = { left: cr.left, right: cr.right, top: cr.top, bottom: cr.bottom }; }
    return {
      label: (child && child.getAttribute && child.getAttribute('alt')) || el.textContent.trim() || '(content)',
      clearL: c.left - inner.left, clearR: inner.right - c.right,
      clearT: c.top - inner.top, clearB: inner.bottom - c.bottom,
      contentW: c.right - c.left, contentH: c.bottom - c.top,
    };
  }));
}

async function findOverflows(page, containers) {
  return page.$$eval(containers.join(','), conts => {
    const out = [];
    for (const c of conts) {
      const p = c.getBoundingClientRect();
      for (const ch of c.children) {
        const cs = getComputedStyle(ch);
        if (cs.position === 'absolute' || cs.position === 'fixed') continue; // glows/decoration
        const r = ch.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) continue;
        const over = Math.max(p.left - r.left, r.right - p.right, p.top - r.top, r.bottom - p.bottom);
        if (over > 2) out.push(`${(c.className||c.tagName).toString().split(' ')[0]} › ${(ch.className||ch.tagName).toString().split(' ')[0]} overflows by ${Math.round(over)}px`);
      }
    }
    return out;
  });
}

const browser = await chromium.launch();
const issues = [];
for (const vp of VIEWPORTS) {
  const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, reducedMotion: 'reduce' });
  const page = await ctx.newPage();
  await page.goto(URL, { waitUntil: 'load', timeout: 15000 });
  await page.waitForTimeout(400); // let fonts/layout settle

  for (const g of TILE_GROUPS) {
    const tiles = await measureTiles(page, g.sel);
    if (!tiles.length) continue;
    for (const t of tiles) {
      const minClear = Math.min(t.clearL, t.clearR, t.clearT, t.clearB);
      if (minClear < g.minClearance)
        issues.push(`[${vp.name}] ${g.label} — "${t.label}" content only ${round(minClear)}px from inner edge (want ≥ ${g.minClearance}px)`);
    }
    const ws = tiles.map(t => t.contentW);
    const max = Math.max(...ws), min = Math.min(...ws);
    if (max - min > 0.20 * max)
      issues.push(`[${vp.name}] ${g.label} — content widths inconsistent across siblings: ${ws.map(round).join(', ')}px (> 20% spread)`);
  }

  for (const o of await findOverflows(page, OVERFLOW_CONTAINERS))
    issues.push(`[${vp.name}] overflow: ${o}`);

  // code panes that scroll horizontally on DESKTOP = a clipped/truncated line
  // (mobile horizontal code-scroll is accepted as terminal-authentic, so skip it)
  if (vp.name === 'desktop') {
    const cuts = await page.$$eval('.code-wrap, .code.shell', els => els
      .filter(el => el.scrollWidth > el.clientWidth + 2)
      .map(el => `code clipped by ${Math.round(el.scrollWidth - el.clientWidth)}px: "${el.textContent.trim().split('\n')[0].slice(0,46)}…"`));
    for (const c of cuts) issues.push(`[${vp.name}] ${c}`);
  }

  await ctx.close();
}
await browser.close();

if (issues.length) {
  console.log(`\nVISUAL-QA: ${issues.length} issue(s) at ${URL}\n`);
  for (const i of issues) console.log('  ✗ ' + i);
  console.log('');
  process.exit(1);
}
console.log(`VISUAL-QA: no geometry issues found at ${URL}`);
