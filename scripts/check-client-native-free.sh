#!/usr/bin/env bash
# check-client-native-free.sh — no production binary parses HAProxy configuration.
#
# The generator declares what it generated and HAProxy parses what it is given
# (ADR-0022). client-native's config-parser, its configuration client and its
# models survive in exactly two places: the differential CI test, and the
# browser playground's `haproxy_valid` fallback behind the `playground` build
# tag. depguard enforces that at the import site; this gate enforces it at the
# link site, where a transitive import would still slip through.
#
# `runtime` is exempt: the agent speaks the HAProxy master socket with it, and
# the controller image carries the agent as a subcommand. `models` rides along
# with `runtime` and parses nothing on its own; depguard is what keeps it out of
# HAPTIC's own imports.
set -euo pipefail
cd "$(dirname "$0")/.."

BANNED='github.com/haproxytech/client-native/v6/(configuration|config-parser)'

fail=0

check() {
  local label="$1" pkg="$2"
  shift 2
  local deps
  deps="$(env "$@" go list -deps "$pkg")"
  local hits
  hits="$(printf '%s\n' "$deps" | grep -E "$BANNED" || true)"
  if [ -n "$hits" ]; then
    echo "FAIL: $label links a HAProxy config parser:"
    printf '  %s\n' $hits
    fail=1
  else
    echo "OK: $label links no HAProxy config parser"
  fi
}

# Every production binary, at the tags it actually ships with.
check "cmd/haptic" ./cmd/haptic GOOS=linux GOARCH=amd64
check "cmd/playground (untagged)" ./cmd/playground GOOS=js GOARCH=wasm

# The playground bundle is the one build that may link it, and only with the
# tag. A build that stops needing the tag means the fallback was lost.
if ! GOOS=js GOARCH=wasm go list -deps -tags=playground ./cmd/playground | grep -qE "$BANNED"; then
  echo "FAIL: the playground no longer links the syntax + schema check it answers haproxy_valid with"
  fail=1
else
  echo "OK: the playground links the syntax + schema check behind -tags=playground"
fi

if [ "$fail" -ne 0 ]; then
  echo ""
  echo "check-client-native-free: FAILED"
  exit 1
fi
echo "check-client-native-free: OK"
