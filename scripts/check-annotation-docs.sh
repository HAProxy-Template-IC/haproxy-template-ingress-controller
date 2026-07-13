#!/usr/bin/env bash
# check-annotation-docs.sh — pin the vendor annotation-library reference pages
# against the libraries' migrationCoverage declarations.
#
# For each vendor library the script:
#   1. Extracts the DECLARED annotation inventory (key + status) from the
#      library's `migrationCoverage:` block — the same block
#      check-migration-coverage.sh pins against the template read-set, so the
#      inventory is guaranteed to match what the chart actually ships.
#   2. For every annotation with status `supported` or `different` (i.e. the
#      chart acts on it), requires the corresponding docs page to document it:
#        - a heading of the full annotation (`### <prefix>/<key>`), or
#        - an inline-code mention of the key (`` `<key>` `` or
#          `` `<prefix>/<key>` ``) — grouped family tables document
#          annotations this way.
#      `dropped` annotations are accepted-but-inert; the pages cover them in
#      prose/unsupported sections, so they are not required to have an entry.
#   3. Fails listing every undocumented annotation, so adding an annotation
#      handler without a reference entry fails `make lint`.
#
# Wired into `make lint` (after check-migration-coverage.sh).
set -euo pipefail

# Stable byte-wise collation so sorted output is locale-independent.
export LC_ALL=C

cd "$(dirname "$0")/.."

CHARTS=charts/haptic/charts
DOCS=docs/site/docs/libraries
FAILED=0

# extract_declared <coverage-file> — `<key> <status>` pairs from the
# migrationCoverage block (same extraction as check-migration-coverage.sh).
extract_declared() {
  awk '
    /^migrationCoverage:/ { in_cov = 1; next }
    in_cov && /^[A-Za-z_]/ { in_cov = 0 }
    !in_cov { next }
    /^[ ]+[a-z0-9.-]+\/[a-z0-9.-]+:[ ]*$/ {
      key = $1
      sub(/:$/, "", key)
      next
    }
    key != "" && /^[ ]+status:[ ]*/ {
      status = $2
      print key, status
      key = ""
    }
  ' "$1" | sort -u
}

# escape_ere <string> — escape ERE metacharacters so annotation keys (dots in
# the prefix) match literally.
escape_ere() {
  printf '%s' "$1" | sed 's/[.[\*^$()+?{|]/\\&/g'
}

# check_library <name> <prefix> <coverage-file> <docs-page>
check_library() {
  local name="$1" prefix="$2" coverage_file="$3" docs_page="$4"

  if [ ! -f "$coverage_file" ]; then
    echo "FAIL [$name]: coverage file $coverage_file not found"
    FAILED=1
    return
  fi
  if [ ! -f "$docs_page" ]; then
    echo "FAIL [$name]: docs page $docs_page not found"
    FAILED=1
    return
  fi

  local declared
  declared="$(extract_declared "$coverage_file")"
  if [ -z "$declared" ]; then
    echo "FAIL [$name]: migrationCoverage block in $coverage_file declares no annotations"
    FAILED=1
    return
  fi

  # Mentions inside an "Unsupported ..." section must not satisfy the gate:
  # a key can be name-dropped there as context while its reference entry is
  # missing. Scan a copy of the page with those sections stripped.
  local page_filtered
  page_filtered="$(mktemp)"
  awk '/^## Unsupported/{skip=1; next} /^## /{skip=0} !skip' "$docs_page" >"$page_filtered"

  local missing=""
  local total=0 checked=0
  while IFS=' ' read -r key status; do
    total=$((total + 1))
    case "$status" in
      supported | different) ;;
      *) continue ;;
    esac
    checked=$((checked + 1))
    local short="${key#"$prefix"/}"
    local esc_key esc_short
    esc_key="$(escape_ere "$key")"
    esc_short="$(escape_ere "$short")"
    if ! grep -qE "^#{1,6} ${esc_key}[[:space:]]*$" "$page_filtered" \
      && ! grep -qE "\`(${esc_key}|${esc_short})\`" "$page_filtered"; then
      missing="${missing}    ${key}"$'\n'
    fi
  done <<<"$declared"
  rm -f "$page_filtered"

  if [ -n "$missing" ]; then
    echo "FAIL [$name]: shipped annotations with no entry on $docs_page."
    echo "  Document these (a '### $prefix/<key>' heading or an inline-code mention):"
    printf '%s' "$missing"
    FAILED=1
    return
  fi

  echo "OK [$name]: all $checked supported/different annotations (of $total declared) are documented on $docs_page"
}

check_library haptic-annotations haproxy-haptic.org \
  "$CHARTS/haptic-annotations/90-migration-coverage.yaml" \
  "$DOCS/haptic-annotations.md"

check_library nginx-ingress nginx.ingress.kubernetes.io \
  "$CHARTS/nginx-ingress/90-migration-coverage.yaml" \
  "$DOCS/nginx-ingress.md"

check_library haproxy-ingress haproxy-ingress.github.io \
  "$CHARTS/haproxy-ingress/90-migration-coverage.yaml" \
  "$DOCS/haproxy-ingress.md"

check_library haproxytech haproxy.org \
  "$CHARTS/haproxytech/library.yaml" \
  "$DOCS/haproxytech.md"

if [ "$FAILED" -ne 0 ]; then
  echo
  echo "Annotation docs drift detected. Every supported/different annotation a"
  echo "vendor library declares in its migrationCoverage must have a reference"
  echo "entry on the library's docs page."
  exit 1
fi
