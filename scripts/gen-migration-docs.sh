#!/usr/bin/env bash
# gen-migration-docs.sh — render the per-source annotation-support tables in
# docs/site/docs/annotation-compatibility.md, and each source's controller
# ConfigMap table in docs/site/docs/migrating.md, FROM the vendor libraries'
# _migrationCoverage declarations, so the docs can never drift from the data
# the playground's migration report classifies with (annotation reads are in
# turn pinned to the coverage by check-migration-coverage.sh).
#
# Each table lives between marker comments:
#   <!-- BEGIN generated: migration-coverage <source> -->            (annotations)
#   <!-- BEGIN generated: migration-configmap-coverage <source> -->  (ConfigMap)
#   ... generated table ...
#   <!-- END generated: ... <source> -->
# The prose around the markers is hand-written and left untouched.
#
# Modes:
#   (no args)  regenerate the blocks in place.
#   --check    fail (exit 1) if regeneration would change either doc — used by
#              `make lint` to pin the docs against the coverage data.
set -euo pipefail

cd "$(dirname "$0")/.."

DOC=docs/site/docs/annotation-compatibility.md
CONFIGMAP_DOC=docs/site/docs/migrating.md
CHARTS=charts/haptic/charts

CHECK=0
if [ "${1:-}" = "--check" ]; then
  CHECK=1
elif [ -n "${1:-}" ]; then
  echo "usage: $0 [--check]" >&2
  exit 2
fi

python3 - "$CHECK" "$DOC" "$CONFIGMAP_DOC" \
  "$CHARTS/nginx-ingress/90-migration-coverage.yaml" \
  "$CHARTS/haproxy-ingress/90-migration-coverage.yaml" \
  "$CHARTS/haproxytech/library.yaml" <<'PY'
import re
import sys

import yaml

check = sys.argv[1] == "1"
doc_path = sys.argv[2]
configmap_doc_path = sys.argv[3]
coverage_files = sys.argv[4:]

STATUS_LABEL = {
    "different": "Behaviour differs",
    "dropped": "Not carried over",
    "fails": "Fails the render",
}
# Order sources deterministically for stable output.
SOURCE_ORDER = ["ingress-nginx", "haproxy-ingress", "haproxytech"]


def load_coverage_block(path):
    """Return the _migrationCoverage list from a file that may also contain
    unrelated top-level YAML (haproxytech's library.yaml). Slices the block
    from the `_migrationCoverage:` line at column 0 to the next column-0 key
    (or EOF) and parses just that."""
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    lines = text.splitlines(keepends=True)
    out, capturing = [], False
    for line in lines:
        if line.startswith("_migrationCoverage:"):
            capturing = True
            out.append(line)
            continue
        if capturing:
            # A non-indented, non-comment, non-blank line ends the block.
            if line and not line[0].isspace() and not line.startswith("#"):
                break
            out.append(line)
    if not out:
        raise SystemExit(f"{path}: no top-level _migrationCoverage block found")
    data = yaml.safe_load("".join(out))
    return data["_migrationCoverage"]


sources = {}
for path in coverage_files:
    for entry in load_coverage_block(path):
        sources[entry["source"]] = entry


def render_table(entry):
    anns = entry.get("annotations", {})
    counts = {"supported": 0, "different": 0, "dropped": 0, "fails": 0}
    for meta in anns.values():
        counts[meta["status"]] = counts.get(meta["status"], 0) + 1
    total = len(anns)
    prefix = (entry.get("detect", {}).get("annotationPrefixes") or ["?/"])[0]

    lines = []
    summary = (
        f"The library classifies {total} `{prefix}*` annotations: "
        f"{counts['supported']} supported, "
        f"{counts['different']} with behaviour differences, "
        f"{counts['dropped']} not carried over, "
        f"{counts['fails']} failing."
    )
    lines.append(summary)
    lines.append("")

    # Only the non-supported annotations need a warning row; supported ones
    # are covered by the linked library reference.
    rows = [
        (key, meta)
        for key, meta in sorted(anns.items())
        if meta["status"] != "supported"
    ]
    if not rows:
        lines.append("All supported annotations carry over without caveats.")
        return "\n".join(lines)

    lines.append("| Annotation | Status | What to check |")
    lines.append("|------------|--------|---------------|")
    for key, meta in rows:
        label = STATUS_LABEL[meta["status"]]
        note = (meta.get("note") or "").replace("|", "\\|").strip()
        lines.append(f"| `{key}` | {label} | {note} |")
    return "\n".join(lines)


def render_configmap_table(entry):
    lines = [
        f"| {entry['source']} key | HAPTIC setting | Status | What to check |",
        "|-------------------|----------------|--------|---------------|",
    ]
    for setting in entry["configMap"]["settings"]:
        keys = ", ".join(
            f"`{key['name']}`" + (f" (`{key['default']}`)" if "default" in key else "")
            for key in setting["keys"]
        )
        status = setting["status"]
        label = status if status == "supported" else f"**{status}**"
        cells = [keys, setting.get("setting") or "—", label, setting.get("note") or ""]
        lines.append("| " + " | ".join(c.replace("|", "\\|").strip() for c in cells) + " |")
    return "\n".join(lines)


def regenerate(path, blocks):
    """Replace each (marker, source, block) region in path; return (old, new)."""
    with open(path, encoding="utf-8") as fh:
        doc = fh.read()
    new_doc = doc
    for marker, source, block in blocks:
        begin = f"<!-- BEGIN generated: {marker} {source} -->"
        end = f"<!-- END generated: {marker} {source} -->"
        replacement = f"{begin}\n{block}\n{end}"
        pattern = re.compile(re.escape(begin) + r".*?" + re.escape(end), re.DOTALL)
        if not pattern.search(new_doc):
            raise SystemExit(
                f"{path}: missing marker block for source '{source}' "
                f"(expected '{begin}' ... '{end}')"
            )
        new_doc = pattern.sub(lambda _m, r=replacement: r, new_doc, count=1)
    return doc, new_doc


ordered = [sources[s] for s in SOURCE_ORDER if s in sources]
docs = {
    doc_path: [("migration-coverage", e["source"], render_table(e)) for e in ordered],
    configmap_doc_path: [
        ("migration-configmap-coverage", e["source"], render_configmap_table(e))
        for e in ordered
        if e.get("configMap")
    ],
}

stale = False
for path, blocks in docs.items():
    doc, new_doc = regenerate(path, blocks)
    if new_doc == doc:
        if not check:
            print(f"{path} already up-to-date.")
        continue
    if check:
        sys.stderr.write(
            f"{path} is out of date with _migrationCoverage.\n"
            "Run scripts/gen-migration-docs.sh and commit the result.\n"
        )
        stale = True
    else:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(new_doc)
        print(f"Regenerated migration-coverage tables in {path}.")

if stale:
    sys.exit(1)
if check:
    print("Generated migration-coverage tables are up-to-date.")
PY
