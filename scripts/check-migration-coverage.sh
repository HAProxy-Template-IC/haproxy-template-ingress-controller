#!/usr/bin/env bash
# check-migration-coverage.sh — pin the vendor annotation libraries' declared
# _migrationCoverage against the annotation keys their templates actually read.
#
# For each vendor library the script:
#   1. Extracts the READ set: every `"<prefix>/<key>"` QUOTED string literal in
#      the library's template fragments. Code reads annotations as quoted
#      literals — `Annotations["<prefix>/<key>"]` or a `"<prefix>/<key>"` macro
#      argument — whereas documentation comments write the key unquoted
#      (`- <prefix>/<key>: "value"`, `.../annotations/#<key>`), so the quote
#      anchor cleanly separates real reads from doc mentions without any
#      brittle comment-stripping. If the library imports
#      `util-emit-annotation-cors`, the CORS suffixes that the shared macro
#      reads dynamically (`prefix + "/cors-..."`) are added.
#   2. Extracts the DECLARED set from the library's `_migrationCoverage:` block
#      (key + status).
#   3. Fails on drift in either direction:
#        - undeclared-read: the template reads a key the coverage map doesn't
#          declare → add the key to the library's _migrationCoverage.
#        - declared-unread: the coverage map declares a key with status
#          supported/different/fails that no template reads → the declaration
#          is stale (or the status should be `dropped`, the one status that
#          legitimately has no read).
#      It also rejects unknown statuses and keys outside the library's prefix.
#
# Wired into `make lint`.
set -euo pipefail

# Stable byte-wise collation so `sort` and `comm` agree regardless of the
# caller's locale (a UTF-8 locale orders '.', '-' and '/' differently).
export LC_ALL=C

cd "$(dirname "$0")/.."

CHARTS=charts/haptic/charts
COMPAT_LIB=charts/haptic/charts/ingress-annotations-compat/library.yaml
FAILED=0

# The CORS suffixes read dynamically by util-emit-annotation-cors as
# `Annotations[prefix + "/<suffix>"]`.
cors_suffixes() {
  perl -ne 'print "$1\n" if /Annotations\[prefix \+ "\/([a-z0-9-]+)"\]/' "$COMPAT_LIB" | sort -u
}

# strip_non_template <files...> — emit file contents with top-level
# `validationTests:` and `_migrationCoverage:` blocks removed, so annotation
# keys that appear ONLY as test fixtures or in the coverage declaration are
# not miscounted as template "reads" (that would let a fixture silently
# satisfy the stale-declaration guard). Both are column-0 keys; strip until
# the next column-0 key or EOF.
strip_non_template() {
  awk '
    /^(validationTests|_migrationCoverage):/ { skip = 1; next }
    skip && /^[A-Za-z]/ { skip = 0 }
    !skip { print }
  ' "$@"
}

# extract_read <prefix> <files...> — sorted unique annotation keys READ by the
# rendering templates: every `"<prefix>/<key>"` quoted string literal, plus
# the CORS suffixes when the library imports the shared CORS macro. Fixture
# and coverage blocks are stripped first.
extract_read() {
  local prefix="$1"
  shift
  local body
  body="$(strip_non_template "$@")"
  # Here-strings, not `printf | grep`: under `set -o pipefail`, `grep -q`
  # closes the pipe on its first match and the upstream `printf` dies with
  # SIGPIPE (141), which pipefail would surface as a pipeline failure —
  # making the CORS-import check a false negative. A here-string has no
  # upstream producer to signal.
  {
    grep -oE "\"${prefix//./\\.}/[a-z0-9][a-z0-9.-]*\"" <<<"$body" | tr -d '"' || true
    if grep -qlE 'import "util-(emit|ingress)-annotation-cors[a-z-]*"' <<<"$body"; then
      cors_suffixes | sed "s|^|${prefix}/|"
    fi
    # A library's own dynamic reads count the same as the shared macro's.
    perl -ne 'print "$1\n" if /Annotations\[prefix \+ "\/([a-z0-9-]+)"\]/' <<<"$body" |
      sed "s|^|${prefix}/|" || true
  } | sort -u
}

# extract_declared <coverage-file> — `<key> <status>` pairs from the
# _migrationCoverage block (any indentation; keys are `<something>/<name>:`
# lines, the status is the next `status:` line).
extract_declared() {
  awk '
    /^_migrationCoverage:/ { in_cov = 1; next }
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

# check_library <name> <prefix> <coverage-file> <template-files...>
check_library() {
  local name="$1" prefix="$2" coverage_file="$3"
  shift 3

  if [ ! -f "$coverage_file" ]; then
    echo "FAIL [$name]: coverage file $coverage_file not found"
    FAILED=1
    return
  fi
  if ! grep -q '^_migrationCoverage:' "$coverage_file"; then
    echo "FAIL [$name]: $coverage_file has no top-level _migrationCoverage: block"
    FAILED=1
    return
  fi

  local read_keys declared
  read_keys="$(extract_read "$prefix" "$@")"
  declared="$(extract_declared "$coverage_file")"

  if [ -z "$declared" ]; then
    echo "FAIL [$name]: _migrationCoverage block in $coverage_file declares no annotations"
    FAILED=1
    return
  fi

  # Validate statuses and prefix ownership.
  while IFS=' ' read -r key status; do
    case "$status" in
      supported | different | dropped | fails) ;;
      *)
        echo "FAIL [$name]: $key declares unknown status '$status' (allowed: supported, different, dropped, fails)"
        FAILED=1
        ;;
    esac
    case "$key" in
      "$prefix"/*) ;;
      *)
        echo "FAIL [$name]: declared key $key is outside the library's annotation prefix $prefix/"
        FAILED=1
        ;;
    esac
  done <<<"$declared"

  local declared_keys
  declared_keys="$(printf '%s\n' "$declared" | cut -d' ' -f1)"

  # Direction 1: every read key must be declared. (set difference read \ declared)
  local undeclared
  undeclared="$(awk 'NR==FNR { d[$0]=1; next } !($0 in d)' \
    <(printf '%s\n' "$declared_keys") <(printf '%s\n' "$read_keys"))"
  if [ -n "$undeclared" ]; then
    echo "FAIL [$name]: templates read annotations that _migrationCoverage does not declare."
    echo "  Add these keys to $coverage_file:"
    printf '    %s\n' $undeclared
    FAILED=1
  fi

  # Direction 2: every declared supported/different/fails key must be read.
  # (status `dropped` legitimately has no read — that is what it documents.)
  # set difference (declared non-dropped) \ read.
  local unread
  unread="$(awk 'NR==FNR { r[$0]=1; next } $2 != "dropped" && !($1 in r) { print $1 }' \
    <(printf '%s\n' "$read_keys") <(printf '%s\n' "$declared"))"
  if [ -n "$unread" ]; then
    echo "FAIL [$name]: _migrationCoverage declares non-dropped annotations that no template reads."
    echo "  Remove them from $coverage_file, or reclassify as 'dropped' if intentionally inert:"
    printf '    %s\n' $unread
    FAILED=1
  fi

  local total
  total="$(printf '%s\n' "$declared" | wc -l)"
  echo "OK [$name]: $total annotations declared, read-set and coverage agree"
}

check_library haptic-annotations haproxy-haptic.org \
  "$CHARTS/haptic-annotations/90-migration-coverage.yaml" \
  "$CHARTS"/haptic-annotations/_index.yaml "$CHARTS"/haptic-annotations/[0-9]*.yaml

check_library nginx-ingress nginx.ingress.kubernetes.io \
  "$CHARTS/nginx-ingress/90-migration-coverage.yaml" \
  "$CHARTS"/nginx-ingress/_index.yaml "$CHARTS"/nginx-ingress/[0-9]*.yaml

check_library haproxy-ingress haproxy-ingress.github.io \
  "$CHARTS/haproxy-ingress/90-migration-coverage.yaml" \
  "$CHARTS"/haproxy-ingress/_index.yaml "$CHARTS"/haproxy-ingress/[0-9]*.yaml

check_library haproxytech haproxy.org \
  "$CHARTS/haproxytech/library.yaml" \
  "$CHARTS/haproxytech/library.yaml"

if [ "$FAILED" -ne 0 ]; then
  echo
  echo "Migration coverage drift detected. Every annotation a vendor library reads"
  echo "must be classified in its _migrationCoverage declaration (and vice versa)."
  exit 1
fi
