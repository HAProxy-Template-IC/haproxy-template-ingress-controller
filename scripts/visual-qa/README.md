# Visual QA (DOM geometry)

Catches the class of bug a screenshot-diff tool *can't* on a first run: a child that
overflows or crowds its container, and sibling components whose content footprint is
inconsistent (e.g. an icon glyph rendered far bigger than its neighbours).

## Setup (once)

```
cd scripts/visual-qa
npm install
npx playwright install chromium
```

## Run

```
# against the running dev server (mkdocs serve), or pass any URL / file://
node check.mjs http://127.0.0.1:8000/
```

Exits non-zero and prints findings if any geometry issue is detected. Tune the
component groups / thresholds in `check.mjs`.

## Accessibility audit (axe-core)

```
node audit.mjs <built-site-dir>   # dir containing index.html (+ index-dark.html)
```

Runs axe-core (WCAG 2 A/AA + best-practice) across dark-desktop, light-desktop and
dark-mobile, and probes code-pane overflow at the in-between widths (768/900/1100).
Reports contrast, keyboard-access, ARIA and heading issues. Target: 0 violations.
