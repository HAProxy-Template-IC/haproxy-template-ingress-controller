#!/usr/bin/env bash
#
# The conformance shard partition is `hash % total`, so shards 1 and 2 of 8
# cover a DIFFERENT set of tests than shard 1 of 4. Sub-sharding exists so a
# job can be split across runners while covering exactly what it covered
# before. These assert that property directly: if someone ever replaces the
# MR smoke's sub-shards with two shards of eight, the covered set changes
# silently and the merge gate is no longer the one that was reviewed.
set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/shard-conformance-tests.sh"

fail() { printf '%s\n' "$*" >&2; exit 1; }

# Test names, one per line, for a shard invocation.
names() {
    "$script" "$@" \
        | sed -E 's/^\^TestGatewayAPIConformance\/\(//; s/\)\$$//' \
        | tr '|' '\n' \
        | grep -v '^$' \
        | sort
}

# The script reads the upstream module out of the Go module cache. A fresh
# clone that has not downloaded it cannot exercise any of this, so skipping
# keeps the suite runnable there. CI sets SHARD_CONFORMANCE_TESTS_REQUIRED
# after downloading the module: a gate that can quietly skip itself is not a
# gate, and this one exists to stop a silent change to what gates a merge.
if ! "$script" 1 4 >/dev/null 2>&1; then
    if [[ -n "${SHARD_CONFORMANCE_TESTS_REQUIRED:-}" ]]; then
        fail "upstream gateway-api conformance module unavailable, so the shard partition went unchecked"
    fi
    echo "SKIP: upstream gateway-api conformance module not in the module cache"
    exit 0
fi

# --- sub-shards reassemble their shard exactly -------------------------------
for total in 2 3; do
    combined="$(mktemp)"
    : > "$combined"
    for index in $(seq 1 "$total"); do
        names 1 4 "$index" "$total" >> "$combined"
    done
    sort -o "$combined" "$combined"

    diff <(names 1 4) "$combined" > /dev/null \
        || fail "sub-shards of 1/4 into $total parts do not reassemble shard 1/4"

    duplicates="$(sort "$combined" | uniq -d | wc -l)"
    [[ "$duplicates" -eq 0 ]] \
        || fail "sub-sharding 1/4 into $total parts runs $duplicates test(s) twice"
    rm -f "$combined"
done

# --- the split is balanced ---------------------------------------------------
first="$(names 1 4 1 2 | wc -l)"
second="$(names 1 4 2 2 | wc -l)"
(( first > 0 && second > 0 )) || fail "a half of shard 1/4 is empty ($first/$second)"
(( first - second <= 1 && second - first <= 1 )) \
    || fail "halves of shard 1/4 differ by more than one test ($first vs $second)"

# --- asking for the whole shard is the old behaviour -------------------------
diff <(names 1 4) <(names 1 4 1 1) > /dev/null \
    || fail "sub-shard 1 of 1 is not the whole shard"

# --- a shard of eight is NOT two shards of four ------------------------------
# The distinction this whole mechanism exists for. If this ever passes, the
# partition changed and sub-sharding is no longer needed — or, far more
# likely, the test is wrong.
if diff <(names 1 4) <(cat <(names 1 8) <(names 2 8) | sort) > /dev/null 2>&1; then
    fail "shards 1+2 of 8 equal shard 1 of 4 — the partition is not hash-modulo any more"
fi

# --- input validation --------------------------------------------------------
"$script" 1 4 5 2 >/dev/null 2>&1 && fail "sub-index above sub-total was accepted"
"$script" 1 4 0 2 >/dev/null 2>&1 && fail "sub-index of zero was accepted"
"$script" 1 4 1 >/dev/null 2>&1 && fail "an odd argument count was accepted"

echo "shard-conformance-tests: OK"
