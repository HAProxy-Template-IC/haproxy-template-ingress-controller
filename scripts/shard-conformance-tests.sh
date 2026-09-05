#!/usr/bin/env bash
#
# Emit a Go test `-run` regex for one shard of the upstream Gateway-API
# conformance suite. Used by `.gitlab-ci.yml`'s `test-gateway-conformance`
# job, which runs with `parallel: <N>` so GitLab CI provides
# `$CI_NODE_INDEX` (1..N) and `$CI_NODE_TOTAL` (=N) per job. Each
# parallel job runs:
#
#   TEST_RUN_PATTERN="$(scripts/shard-conformance-tests.sh "$CI_NODE_INDEX" "$CI_NODE_TOTAL")" \
#     make test-gateway-conformance
#
# The result is a regex of the shape
#   ^TestGatewayAPIConformance/(NameA|NameB|...|NameN)$
# anchoring against the parent test (`TestGatewayAPIConformance`) and
# the conformance suite's ShortName for each test (which is what the
# upstream framework uses for `t.Run`).
#
# Sharding strategy
# -----------------
# Each test's ShortName is mapped to a shard index via a coreutils
# `cksum` (CRC32) hash modulo `$CI_NODE_TOTAL`. The
# result is deterministic: the same test always lands on the same
# shard for the same total. Adding new upstream tests redistributes,
# but doesn't shift existing assignments unpredictably enough to hurt
# (a test moves at most once when a new sibling lands).
#
# Test enumeration
# ----------------
# The upstream conformance suite has a single top-level Go test
# (`TestGatewayAPIConformance`) that dispatches sub-tests dynamically.
# `go test -list` doesn't see those — so we grep the upstream module
# (`sigs.k8s.io/gateway-api/conformance/tests/*.go`) for the
# `ShortName: "..."` fields. The module path is resolved from
# `go env GOMODCACHE` + the version pinned in this repo's `go.mod`.
#
# Sub-sharding
# ------------
# The optional third and fourth arguments split one shard across several
# jobs while covering exactly the tests that shard already covered. That
# is not the same as asking for more shards: the partition is
# `hash % total`, so shards 1 and 2 of 8 are the tests hashing to 0 and 1
# mod 8, whereas shard 1 of 4 is 0 and 4 mod 8 — a different set. Only
# sub-sharding keeps the covered set fixed while adding parallelism, so
# splitting a job never silently changes which tests gate a merge.
#
# The split is round-robin over the shard's sorted test list, so the
# halves stay within one test of each other however the hash landed.
#
# Usage
# -----
#   scripts/shard-conformance-tests.sh <shard-index> <shard-total> [sub-index] [sub-total]
#
#   shard-index   1-based shard ID (matches CI_NODE_INDEX)
#   shard-total   total shard count (matches CI_NODE_TOTAL)
#   sub-index     1-based sub-shard ID within the selected shard
#   sub-total     number of sub-shards the selected shard is split into
#
# Outputs the test-name regex on stdout. Exits non-zero on
# inconsistent inputs or if the upstream module can't be located.

set -euo pipefail

if [[ $# -ne 2 && $# -ne 4 ]]; then
  echo "usage: $0 <shard-index> <shard-total> [sub-index] [sub-total]" >&2
  exit 2
fi

SHARD_INDEX="$1"
SHARD_TOTAL="$2"
SUB_INDEX="${3:-1}"
SUB_TOTAL="${4:-1}"

for pair in "shard-index:$SHARD_INDEX" "shard-total:$SHARD_TOTAL" \
            "sub-index:$SUB_INDEX" "sub-total:$SUB_TOTAL"; do
  if ! [[ "${pair#*:}" =~ ^[1-9][0-9]*$ ]]; then
    echo "error: ${pair%%:*} must be a positive integer (got ${pair#*:})" >&2
    exit 2
  fi
done

if (( SHARD_INDEX > SHARD_TOTAL )); then
  echo "error: shard-index ($SHARD_INDEX) > shard-total ($SHARD_TOTAL)" >&2
  exit 2
fi

if (( SUB_INDEX > SUB_TOTAL )); then
  echo "error: sub-index ($SUB_INDEX) > sub-total ($SUB_TOTAL)" >&2
  exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Resolve the conformance module path from go.mod's pinned version.
# `sigs.k8s.io/gateway-api/conformance` is a separate Go submodule with
# its own version, distinct from the parent `sigs.k8s.io/gateway-api`.
# Match the `conformance` line specifically.
GW_API_CONF_VERSION="$(awk '/sigs\.k8s\.io\/gateway-api\/conformance[[:space:]]+v/{print $2; exit}' go.mod)"
if [[ -z "${GW_API_CONF_VERSION:-}" ]]; then
  echo "error: could not resolve sigs.k8s.io/gateway-api/conformance version from go.mod" >&2
  exit 1
fi

GOMODCACHE="$(go env GOMODCACHE)"
TESTS_DIR="${GOMODCACHE}/sigs.k8s.io/gateway-api/conformance@${GW_API_CONF_VERSION}/tests"
if [[ ! -d "$TESTS_DIR" ]]; then
  echo "error: upstream conformance tests directory not found at $TESTS_DIR" >&2
  echo "       (run \`go mod download\` to populate the cache)" >&2
  exit 1
fi

# Enumerate every ShortName in the upstream tests. Sorted + de-duplicated
# so shard assignment is stable across runs.
mapfile -t TEST_NAMES < <(
  grep -hE 'ShortName:\s*"' "$TESTS_DIR"/*.go \
    | sed -E 's/.*ShortName:[[:space:]]*"([^"]+)".*/\1/' \
    | sort -u
)

if [[ ${#TEST_NAMES[@]} -eq 0 ]]; then
  echo "error: no ShortName entries found in $TESTS_DIR" >&2
  exit 1
fi

# Deterministic, host-independent hash via coreutils `cksum` (CRC32).
# `cksum` emits the same CRC32 for the same bytes on every machine, so
# shard assignment stays stable across runs and hosts.
crc32() {
  printf '%s' "$1" | cksum | awk '{print $1}'
}

# Partition tests into the shard.
SHARD_TESTS=()
for name in "${TEST_NAMES[@]}"; do
  hash="$(crc32 "$name")"
  # GitLab's CI_NODE_INDEX is 1-based; our modulo is 0-based.
  bucket=$(( (hash % SHARD_TOTAL) + 1 ))
  if (( bucket == SHARD_INDEX )); then
    SHARD_TESTS+=("$name")
  fi
done

# Round-robin the shard's tests across its sub-shards. Position in the
# sorted list, not the hash again: re-hashing would drop tests, while
# every position lands in exactly one sub-shard, so the sub-shards
# reassemble the shard exactly.
if (( SUB_TOTAL > 1 )); then
  SUB_TESTS=()
  for position in "${!SHARD_TESTS[@]}"; do
    if (( (position % SUB_TOTAL) + 1 == SUB_INDEX )); then
      SUB_TESTS+=("${SHARD_TESTS[$position]}")
    fi
  done
  SHARD_TESTS=(${SUB_TESTS[@]+"${SUB_TESTS[@]}"})
fi

if [[ ${#SHARD_TESTS[@]} -eq 0 ]]; then
  # An empty shard should still produce a regex that matches nothing —
  # not the empty string, which Go test treats as "match all" and the
  # whole suite would run on this shard. Use a guaranteed-no-match
  # pattern instead.
  echo '^TestGatewayAPIConformance/__no_tests_in_this_shard__$'
  exit 0
fi

# Concatenate with `|` and anchor against `TestGatewayAPIConformance`'s
# sub-test path. Each conformance test runs as
# `TestGatewayAPIConformance/<ShortName>/...`, so the trailing `(/.*)?`
# is optional — Go's `-run` flag matches a path prefix by default, but
# we anchor explicitly so a longer-named sibling test doesn't get
# pulled in (e.g. `Foo` shouldn't drag in `FooBar`).
joined="$(IFS='|'; printf '%s' "${SHARD_TESTS[*]}")"
echo "^TestGatewayAPIConformance/(${joined})\$"
