import { chromium } from 'playwright';
import { AxeBuilder } from '@axe-core/playwright';
const DIR = process.argv[2];
const dark = `file://${DIR}/index-dark.html`, light = `file://${DIR}/index.html`;
const runs = [['dark-desktop',dark,1440,900],['light-desktop',light,1440,900],['dark-mobile',dark,390,844]];
const b = await chromium.launch();
let total = 0;
for (const [name,url,w,h] of runs) {
  const p = await (await b.newContext({viewport:{width:w,height:h},reducedMotion:'reduce'})).newPage();
  await p.goto(url,{waitUntil:'load',timeout:15000}); await p.waitForTimeout(400);
  const { violations } = await new AxeBuilder({page:p}).withTags(['wcag2a','wcag2aa','wcag21aa','best-practice']).analyze();
  console.log(`\n=== ${name}: ${violations.length} violation type(s) ===`);
  for (const v of violations) {
    total += v.nodes.length;
    console.log(`  [${v.impact}] ${v.id}: ${v.help} (${v.nodes.length})`);
    console.log(`     e.g. ${v.nodes[0].target.join(' ')}`);
    if (/contrast/.test(v.id)) { const m=(v.nodes[0].any?.[0]?.data)||{}; console.log(`     ratio ${m.contrastRatio} fg ${m.fgColor} bg ${m.bgColor} (need ${m.expectedContrastRatio})`); }
  }
  await p.context().close();
}
console.log('\n=== code-pane overflow at in-between widths (dark) ===');
for (const w of [768,900,1100]) {
  const p = await (await b.newContext({viewport:{width:w,height:900}})).newPage();
  await p.goto(dark,{waitUntil:'load',timeout:15000}); await p.waitForTimeout(300);
  const cuts = await p.$$eval('.code-wrap, .code.shell, .term-out', els => els.filter(e=>e.scrollWidth>e.clientWidth+2).map(e=>`${Math.round(e.scrollWidth-e.clientWidth)}px ${(e.className||'').split(' ').slice(-1)}`));
  console.log(`  ${w}px: ${cuts.length? cuts.join('; ') : 'no overflow'}`);
  await p.context().close();
}
await b.close();
console.log(`\nTOTAL axe violation nodes: ${total}`);
