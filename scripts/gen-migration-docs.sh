#!/usr/bin/env bash
# gen-migration-docs.sh — render the per-source annotation-support tables in
# docs/site/docs/migrating.md FROM the vendor libraries' _migrationCoverage
# declarations, so the migration guide can never drift from the template code
# (whose reads are in turn pinned to the coverage by check-migration-coverage.sh).
#
# Each source's table lives between marker comments:
#   <!-- BEGIN generated: migration-coverage <source> -->
#   ... generated table ...
#   <!-- END generated: migration-coverage <source> -->
# The prose around the markers is hand-written and left untouched.
#
# Modes:
#   (no args)  regenerate the blocks in place.
#   --check    fail (exit 1) if regeneration would change migrating.md — used by
#              `make lint` to pin the doc against the coverage data.
set -euo pipefail

cd "$(dirname "$0")/.."

DOC=docs/site/docs/migrating.md
CHARTS=charts/haptic/charts

CHECK=0
if [ "${1:-}" = "--check" ]; then
  CHECK=1
elif [ -n "${1:-}" ]; then
  echo "usage: $0 [--check]" >&2
  exit 2
fi

python3 - "$CHECK" "$DOC" \
  "$CHARTS/nginx-ingress/90-migration-coverage.yaml" \
  "$CHARTS/haproxy-ingress/90-migration-coverage.yaml" \
  "$CHARTS/haproxytech/library.yaml" <<'PY'
import re
import sys

import yaml

check = sys.argv[1] == "1"
doc_path = sys.argv[2]
coverage_files = sys.argv[3:]

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


with open(doc_path, encoding="utf-8") as fh:
    doc = fh.read()

new_doc = doc
for source in SOURCE_ORDER:
    if source not in sources:
        continue
    begin = f"<!-- BEGIN generated: migration-coverage {source} -->"
    end = f"<!-- END generated: migration-coverage {source} -->"
    block = render_table(sources[source])
    replacement = f"{begin}\n{block}\n{end}"
    pattern = re.compile(
        re.escape(begin) + r".*?" + re.escape(end), re.DOTALL
    )
    if not pattern.search(new_doc):
        raise SystemExit(
            f"{doc_path}: missing marker block for source '{source}' "
            f"(expected '{begin}' ... '{end}')"
        )
    new_doc = pattern.sub(lambda _m, r=replacement: r, new_doc, count=1)

if check:
    if new_doc != doc:
        sys.stderr.write(
            "migrating.md is out of date with _migrationCoverage.\n"
            "Run scripts/gen-migration-docs.sh and commit the result.\n"
        )
        sys.exit(1)
    print("migrating.md generated tables are up-to-date.")
else:
    if new_doc != doc:
        with open(doc_path, "w", encoding="utf-8") as fh:
            fh.write(new_doc)
        print(f"Regenerated migration-coverage tables in {doc_path}.")
    else:
        print(f"{doc_path} already up-to-date.")
PY
