#!/usr/bin/env bash
# check-values-docs.sh — pin the chart values reference (docs/site/docs/
# reference.md, "Every Helm value the chart accepts") against the leaf keys
# that charts/haptic/values.yaml actually defines.
#
# The script:
#   1. Extracts the LEAF set from values.yaml via yq: every scalar, list, or
#      empty-map key, as a dotted path. Sequences are leaves (their elements
#      are data, not parameters), and paths inside sequences are skipped.
#   2. Extracts the DOCUMENTED set from reference.md: the backticked first
#      cell of every table row.
#   3. Fails listing every leaf key that no documented parameter covers.
#   4. Compares every documented DEFAULT against the value values.yaml holds,
#      for rows where both sides are a scalar. Key coverage alone does not
#      catch a default that rots: Renovate bumps values.yaml and the reference
#      keeps advertising the old image tag.
#
# A leaf is covered when:
#   - a documented parameter matches it exactly, or
#   - a documented parameter with a `<name>` placeholder matches it with
#     `<name>` standing for one path segment (e.g. `spoaHub.plugins.<name>.
#     enabled` covers `spoaHub.plugins.coraza.enabled`), or
#   - a documented parameter is a dotted-path ancestor of it. This is how a
#     documented map row covers the free-form data inside it (label selectors,
#     version-pin maps, podSecurityContext passthrough blocks). It
#     self-maintains: delete the map row from reference.md and its subtree
#     fails the gate again.
#
# One direction only: reference.md may document values that are commented out
# in values.yaml (optional knobs like `haproxy.nbthread` or `controller.
# config.logging.level`) and per-entry field tables, so absence from the leaf
# set is not drift.
#
# Wired into `make lint`.
set -euo pipefail

# Byte-wise sort/grep semantics regardless of locale.
export LC_ALL=C

cd "$(dirname "$0")/.."

VALUES=charts/haptic/values.yaml
DOCS=docs/site/docs/reference.md

# ---------------------------------------------------------------------------
# Prefix allowlist — free-form value subtrees whose keys are data entries, not
# chart parameters, and that no documented row is an ancestor of. Keep this
# minimal: every entry needs a justification.
# ---------------------------------------------------------------------------
ALLOWLIST=(
  # Free-form map of watched-resource entries (the chart ships services /
  # endpoints / secrets defaults; template libraries and operators contribute
  # more). The per-entry schema is documented once, as the field table in
  # reference.md's "Watched Resources" section — not as one row per entry.
  "controller.config.watchedResources."
)

# Leaf keys: every scalar / sequence / empty-map node, as a dotted path.
# `all_c(tag == "!!str")` keeps only paths made of string keys, dropping
# paths that descend into sequence elements (their components are !!int).
leaf_keys() {
  yq eval '[
      .. | select(tag != "!!map" or length == 0)
         | path
         | select(length > 0 and all_c(tag == "!!str"))
         | join(".")
    ] | sort | .[]' "$VALUES"
}

# Documented parameters: the backticked first cell of every table row.
documented_params() {
  grep -oE '^\| `[^`]+`' "$DOCS" | sed 's/^| `//; s/`$//' | sort -u
}

in_allowlist() {
  local leaf="$1" prefix
  for prefix in "${ALLOWLIST[@]}"; do
    case "$leaf" in
      "$prefix"*) return 0 ;;
    esac
  done
  return 1
}

undocumented="$(awk '
  NR == FNR {
    if (index($0, "<name>") > 0) {
      # Placeholder row -> regex with <name> as one path segment.
      re = $0
      gsub(/\./, "\\.", re)
      gsub(/<name>/, "[^.]+", re)
      wild[++nw] = "^" re "$"
    } else {
      doc[$0] = 1
    }
    next
  }
  {
    leaf = $0
    if (leaf in doc) next
    # Ancestor coverage: a documented map row covers its subtree.
    n = split(leaf, parts, ".")
    prefix = ""
    for (i = 1; i < n; i++) {
      prefix = (i == 1) ? parts[1] : prefix "." parts[i]
      if (prefix in doc) next
    }
    for (i = 1; i <= nw; i++) {
      if (leaf ~ wild[i]) next
    }
    print leaf
  }
' <(documented_params) <(leaf_keys))"

failed=""
while IFS= read -r leaf; do
  [ -z "$leaf" ] && continue
  if ! in_allowlist "$leaf"; then
    failed="$failed$leaf"$'\n'
  fi
done <<<"$undocumented"

if [ -n "$failed" ]; then
  echo "FAIL: $VALUES defines values that $DOCS does not document:"
  printf '  %s\n' $failed
  echo
  echo "The chart values reference claims to list every Helm value the chart"
  echo "accepts. Add a table row for each key above (or, for a free-form data"
  echo "subtree, a row for its parent map — see the coverage rules in"
  echo "scripts/check-values-docs.sh)."
  exit 1
fi

total="$(leaf_keys | wc -l)"
echo "OK: all $total values.yaml leaf keys are covered by the chart values reference"

# ---------------------------------------------------------------------------
# Documented defaults must equal the values.yaml scalar they describe.
#
# Scalars only, both sides: a row documenting a map or list writes a summary
# ({}, `see below`) rather than the literal, and a commented-out key has no
# value to compare against. Those rows are skipped, not failed — this gate
# tightens the rows it can prove, it does not demand a literal everywhere.
# ---------------------------------------------------------------------------

# `key<TAB>value` for every scalar leaf, same path filter as leaf_keys.
leaf_values() {
  yq eval '.. | select(tag != "!!map" and tag != "!!seq")
              | select((path | length) > 0 and (path | all_c(tag == "!!str")))
              | (path | join(".")) + "\t" + (. | tostring)' "$VALUES"
}

# `key<TAB>default` for every row whose default cell is backticked.
documented_defaults() {
  grep -oE '^\| `[^`]+` \| [^|]* \| `[^`]*` \|' "$DOCS" \
    | sed -E 's/^\| `([^`]+)` \| [^|]* \| `([^`]*)` \|$/\1\t\2/'
}

# Either extraction coming back empty would make every row "skipped" and the
# comparison pass unconditionally. Fail instead — a silent pass is worse than
# no gate, because it reads as coverage.
for extractor in leaf_values documented_defaults; do
  if [ "$($extractor | wc -l)" -eq 0 ]; then
    echo "FAIL: $extractor extracted nothing — the gate cannot compare anything."
    echo "Its yq/grep expression no longer matches the file it parses; fix it"
    echo "rather than letting the comparison pass on an empty set."
    exit 1
  fi
done

drifted="$(awk -F'\t' '
  NR == FNR { val[$1] = $2; seen[$1] = 1; next }
  {
    key = $1; documented = $2
    if (index(key, "<name>") > 0) next     # placeholder row, no single value
    if (!(key in seen)) next               # map/list/commented-out: nothing to compare
    # A quoted YAML scalar is documented with its quotes; compare the content.
    gsub(/^"|"$/, "", documented)
    if (documented != val[key]) printf "%s\tdocumented %s\tactual %s\n", key, documented, val[key]
  }
' <(leaf_values) <(documented_defaults))"

if [ -n "$drifted" ]; then
  echo
  echo "FAIL: $DOCS documents defaults that $VALUES does not hold:"
  printf '  %s\n' "$drifted"
  echo
  echo "Update the reference row to the current default (and its description, if"
  echo "the change invalidated it). If a bot bumped values.yaml, every other copy"
  echo "of that pin needs the same bump — see the customManagers in renovate.json."
  exit 1
fi

compared="$(documented_defaults | wc -l)"
echo "OK: every documented scalar default matches values.yaml ($compared rows checked)"
