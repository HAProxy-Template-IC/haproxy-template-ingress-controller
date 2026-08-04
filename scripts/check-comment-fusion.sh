#!/usr/bin/env bash
# check-comment-fusion.sh — a comment must not swallow the newline in front of
# a line of rendered output.
#
# `-#}` strips ALL following whitespace including the newline, so an own-line
# comment that ends that way and is followed by output fuses that output onto
# whatever preceded the comment:
#
#   http-request return status 503 if ... -m int 500:599 }
#   {#- why the next rule is gated -#}
#   http-request set-var(txn.denied_by) str(waf) if ...
#
# renders as `...500:599 }http-request set-var(...)` — one directive with
# garbage appended, one directive gone. The comment stripper in _libraries.tpl
# deletes the line first, so this is invisible under Helm and breaks only where
# the CR is used directly: a hand-written HAProxyTemplateConfig, and every path
# once the stripper is removed.
#
# Two forms are exempt because they cannot fuse:
#   - `{# ... -#}`, which keeps the newline on its left. That newline becomes
#     the next line's separator and its indent — the correct idiom for deleting
#     a comment line from a whitespace-sensitive template.
#   - anything followed by a `{% ... %}` statement, which emits nothing; the
#     newline after its own `%}` still separates what comes next.
#
# Provenance: introduced by hand in !1544, one hour after !1543 documented the
# distinction. Reading for it does not work; this does.
set -euo pipefail
cd "$(dirname "$0")/.."

python3 - <<'PY'
import pathlib, re, sys

roots = [pathlib.Path("charts/haptic/charts"), pathlib.Path("charts/haptic/libraries")]
files = sorted(p for r in roots if r.is_dir() for p in r.rglob("*.yaml"))

bad = []
for f in files:
    text = f.read_text()
    for m in re.finditer(r"\{#.*?#\}", text, re.S):
        a, b = m.start(), m.end()
        line_start = text.rfind("\n", 0, a) + 1
        line_end = text.find("\n", b)
        line_end = len(text) if line_end < 0 else line_end
        # own-line only: nothing but whitespace on either side of it
        if text[line_start:a].strip() or text[b:line_end].strip():
            continue
        # Both sides must strip. `{# ... -#}` keeps the newline on its left, so
        # that newline becomes the following line's separator and its indent —
        # the correct idiom for deleting a comment line from a whitespace-
        # sensitive template.
        if text[b - 3 : b] != "-#}" or text[a : a + 3] != "{#-":
            continue
        rest = text[line_end + 1 :]
        nxt = next((l for l in rest.split("\n") if l.strip()), "")
        s = nxt.lstrip()
        # Chained comment blocks: the one that touches output is the one to flag.
        if s.startswith("{#"):
            continue
        # A statement emits nothing; the newline after its `%}` still separates.
        if s.startswith("{%"):
            continue
        # A show expression that strips backward would have eaten this whitespace
        # anyway, so the comment's marker changes nothing.
        if s.startswith("{{-"):
            continue
        bad.append((f, text[:a].count("\n") + 1, nxt.strip()[:70]))

if bad:
    print("FAIL: comments whose `-#}` swallows the newline before rendered output:")
    for f, ln, nxt in bad:
        print(f"  {f}:{ln}  fuses onto: {nxt}")
    print()
    print("Close with `#}` instead. It leaves a blank line, which is inert in a")
    print("rendered config, where `-#}` deletes the newline separating two")
    print("directives. `-#}` is right only when a statement or a stripping show")
    print("expression follows, neither of which emits output here.")
    sys.exit(1)

print(f"OK: no line-fusing comments in {len(files)} template-library files")
PY
