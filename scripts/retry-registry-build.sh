#!/usr/bin/env bash
set -euo pipefail

log="$(mktemp)"
trap 'rm -f "$log"' EXIT
for attempt in 1 2 3; do
    result=0
    # A `> >(tee)` substitution can still be writing when the log is read; a pipeline cannot.
    "$@" 2>&1 | tee "$log" || result=${PIPESTATUS[0]}
    [[ "$result" != 0 ]] || exit 0
    if [[ "$attempt" == 3 ]] || ! grep -Eq \
        'ERROR:.*(failed to resolve|failed to fetch|unexpected status from (HEAD|GET) request).*https://[^ /]+/(v2/|token).*(429 Too Many Requests|500 Internal Server Error|502 Bad Gateway|503 Service Unavailable|504 Gateway Timeout)' "$log"; then
        exit "$result"
    fi
    printf 'Container registry request failed; retrying build (%s/3)\n' "$attempt" >&2
    sleep "$((attempt * 5))"
done
