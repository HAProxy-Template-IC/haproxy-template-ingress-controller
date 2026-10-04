const DOCS_BASE = 'https://haproxy-haptic.org/docs/';

const escapeHTML = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

// Coverage links are docs-root-relative Markdown paths (libraries/base.md#x).
function docsURL(href) {
  if (/^https?:\/\//.test(href)) return href;
  const [path, anchor] = href.split('#');
  const page = path.replace(/(^|\/)index\.md$/, '$1').replace(/\.md$/, '/');
  return DOCS_BASE + page + (anchor ? '#' + anchor : '');
}

function inlineCode(text) {
  return text.split(/(`[^`]*`)/).map((part) =>
    part.length > 1 && part.startsWith('`') && part.endsWith('`')
      ? `<code>${escapeHTML(part.slice(1, -1))}</code>`
      : escapeHTML(part)).join('');
}

// inlineMarkdownHTML renders the inline Markdown that coverage notes use —
// `code` and [text](link) — as HTML; everything else is escaped text.
export function inlineMarkdownHTML(text) {
  const link = /\[([^\]]+)\]\(([^)\s]+)\)/g;
  let html = '', last = 0, m;
  while ((m = link.exec(text))) {
    html += inlineCode(text.slice(last, m.index))
      + `<a href="${escapeHTML(docsURL(m[2]))}" target="_blank" rel="noopener">${inlineCode(m[1])}</a>`;
    last = m.index + m[0].length;
  }
  return html + inlineCode(text.slice(last));
}

function problemHTML(f) {
  return `<div class="mig-note mig-problem">⚠ <code>${escapeHTML(f.key)}</code>: ${escapeHTML(f.problem)}</div>`;
}

// configMapSectionHTML renders the source-controller ConfigMaps of one source:
// attention keys with their HAPTIC setting and note, supported keys as chips.
export function configMapSectionHTML(configMaps, statusClass) {
  let html = '';
  for (const cm of configMaps || []) {
    const findings = cm.findings || [];
    const attention = findings.filter((f) => f.status !== 'supported');
    const supported = findings.filter((f) => f.status === 'supported');
    html += `<div class="mig-ing"><div class="mig-ing-h">${escapeHTML(cm.namespace)}/<b>${escapeHTML(cm.name)}</b>`
      + ` <span class="mig-class">controller ConfigMap</span></div>`;
    if (cm.problem) html += `<div class="mig-ok mig-problem">⚠ ${escapeHTML(cm.problem)}</div>`;
    for (const f of attention) {
      html += `<div class="mig-row"><span class="mig-badge ${statusClass[f.status] || 'dim'}">${escapeHTML(f.status)}</span>`
        + `<code class="mig-ann">${escapeHTML(f.key)}: ${escapeHTML(f.value)}</code></div>`;
      if (f.setting) html += `<div class="mig-note">HAPTIC: ${inlineMarkdownHTML(f.setting)}</div>`;
      if (f.note) html += `<div class="mig-note">${inlineMarkdownHTML(f.note)}</div>`;
      if (f.problem) html += problemHTML(f);
    }
    if (supported.length) {
      html += `<div class="mig-ok">✓ ${supported.length === 1 ? '1 key carries' : `${supported.length} keys carry`} over</div><div class="mig-sup">`;
      for (const f of supported) {
        html += `<code class="mig-chip" title="${escapeHTML(f.setting ? 'HAPTIC: ' + f.setting : '')}">${escapeHTML(f.key)}</code>`;
      }
      html += '</div>';
      for (const f of supported.filter((s) => s.problem)) html += problemHTML(f);
    }
    if (!findings.length && !cm.problem) html += '<div class="mig-ok">no data keys</div>';
    html += '</div>';
  }
  if (html) {
    html += '<div class="mig-note mig-legend"><code>extraContext.</code> is short for <code>controller.config.templatingSettings.extraContext.</code></div>';
  }
  return html;
}
