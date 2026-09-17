#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/scripts" "$fixture/tests/e2e"
cp "$root/scripts/check-test-inventory.sh" "$fixture/scripts/"
git -C "$fixture" init --quiet

check_inventory() {
    bash "$fixture/scripts/check-test-inventory.sh" > "$fixture/result.log" 2>&1
}

printf 'package e2e\n' > "$fixture/tests/e2e/new_test.go"
if check_inventory; then
    echo 'Inventory accepted an untracked e2e test without its build tag' >&2
    exit 1
fi
grep -q 'UNWIRED: tests/e2e/new_test.go' "$fixture/result.log"
printf '//go:build e2e\n\npackage e2e\n' > "$fixture/tests/e2e/new_test.go"
check_inventory

git -C "$fixture" add tests/e2e/new_test.go
printf 'package e2e\n' > "$fixture/tests/e2e/new_test.go"
if check_inventory; then
    echo 'Inventory accepted a tracked e2e test without its build tag' >&2
    exit 1
fi
printf '//go:build e2e\n\npackage e2e\n' > "$fixture/tests/e2e/new_test.go"
printf 'tests/e2e/ignored_test.go\n' > "$fixture/.gitignore"
printf 'package e2e\n' > "$fixture/tests/e2e/ignored_test.go"
check_inventory
printf 'Test inventory regression checks passed\n'
