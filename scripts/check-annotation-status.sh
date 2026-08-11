#!/usr/bin/env bash
# check-annotation-status.sh — pin each vendor annotation-library reference
# page's per-annotation **Status** label against the library's _migrationCoverage
# status, so the two can't drift apart.
#
# check-annotation-docs.sh already guarantees every shipped annotation is
# *documented*. This script guards the *label*: an annotation documented with a
# `**Status**: ✅ Supported` line must actually be acted on by the chart
# (_migrationCoverage `supported` or `different`), and one marked
# `**Status**: ❌ Not Implemented` must actually be inert (`dropped`).
#
# The _migrationCoverage status is four-way (supported / different / dropped /
# fails — see cmd/playground/internal/migratecheck); the docs label is coarser (Supported
# / Not Implemented). We collapse to "does the chart usefully act on it?" and
# flag only the unambiguous contradictions:
#   - doc "Supported"       but coverage "dropped"/"fails"        → lies about support
#   - doc "Not Implemented" but coverage "supported"/"different"  → hides real support
# ("fails" = setting the annotation fails the render, so a "Supported" label is
# especially misleading — it's grouped with "dropped" on the not-acted-on side.)
# The supported-vs-different distinction is intentionally not gated here: both
# mean the chart acts on the annotation, and the behavioural difference from the
# source controller is surfaced in the generated migrating.md tables.
#
# Only annotations documented with an individual heading + **Status** line are
# checked; family-table annotations (no per-annotation label) are covered by
# check-annotation-docs.sh's presence gate.
#
# Wired into `make lint` (after check-annotation-docs.sh).
set -euo pipefail

cd "$(dirname "$0")/.."

python3 - "$@" <<'PY'
import re, sys, pathlib

ROOT = pathlib.Path(".")
VENDORS = [
    ("haptic-annotations", "haproxy-haptic.org",
     "charts/haptic/charts/haptic-annotations/90-migration-coverage.yaml",
     "docs/site/docs/libraries/haptic-annotations.md"),
    ("nginx-ingress", "nginx.ingress.kubernetes.io",
     "charts/haptic/charts/nginx-ingress/90-migration-coverage.yaml",
     "docs/site/docs/libraries/nginx-ingress.md"),
    ("haproxy-ingress", "haproxy-ingress.github.io",
     "charts/haptic/charts/haproxy-ingress/90-migration-coverage.yaml",
     "docs/site/docs/libraries/haproxy-ingress.md"),
    ("haproxytech", "haproxy.org",
     "charts/haptic/charts/haproxytech/library.yaml",
     "docs/site/docs/libraries/haproxytech.md"),
]


def coverage(path):
    """key -> status from the library's _migrationCoverage block."""
    out, incov, key = {}, False, None
    for line in (ROOT / path).read_text().splitlines():
        if re.match(r'^_migrationCoverage:', line):
            incov = True
            continue
        if incov and re.match(r'^[A-Za-z_]', line):
            incov = False
        if not incov:
            continue
        m = re.match(r'^\s+([a-z0-9.-]+/[a-z0-9.-]+):\s*$', line)
        if m:
            key = m.group(1)
            continue
        m = re.match(r'^\s+status:\s*(\w+)', line)
        if m and key:
            out[key] = m.group(1)
            key = None
    return out


def doc_status(path, prefix):
    """key -> label, associating each **Status** line with the nearest preceding
    `<h> prefix/key` heading (reset by any other heading)."""
    out, curkey = {}, None
    head = re.compile(r'^#{2,6}\s+`?(' + re.escape(prefix) + r'/[a-z0-9.-]+)`?\s*$')
    for line in (ROOT / path).read_text().splitlines():
        m = head.match(line)
        if m:
            curkey = m.group(1)
            continue
        if re.match(r'^#{1,6}\s', line):
            curkey = None
        m = re.search(r'\*\*Status\*\*:\s*(.+?)\s*$', line)
        if m and curkey:
            out[curkey] = m.group(1)
    return out


def acts_on(label):
    if '❌' in label or 'Not Implemented' in label:
        return False
    if '✅' in label or 'Supported' in label:
        return True
    return None  # unrecognised label — don't gate


failed = 0
for name, prefix, cov_path, doc_path in VENDORS:
    cov = coverage(cov_path)
    docs = doc_status(doc_path, prefix)
    contradictions = []
    for key, label in docs.items():
        st = cov.get(key)
        if st is None:
            continue  # deprecated alias / not in coverage
        act = acts_on(label)
        if act is None:
            continue
        if st in ('dropped', 'fails') and act:
            contradictions.append(f"{key}: doc '{label}' but coverage={st} (not acted on)")
        elif st in ('supported', 'different') and not act:
            contradictions.append(f"{key}: doc '{label}' but coverage={st}")
    if contradictions:
        failed = 1
        print(f"FAIL [{name}]: {len(contradictions)} Status/coverage contradiction(s) on {doc_path}")
        for c in contradictions:
            print(f"    {c}")
    else:
        print(f"OK [{name}]: {len(docs)} labelled annotations agree with _migrationCoverage")

if failed:
    print()
    print("Annotation **Status** labels drifted from _migrationCoverage. A doc that")
    print("says 'Supported' must map to a supported/different annotation; 'Not")
    print("Implemented' must map to a dropped one. Fix the label or the coverage.")
    sys.exit(1)
PY
