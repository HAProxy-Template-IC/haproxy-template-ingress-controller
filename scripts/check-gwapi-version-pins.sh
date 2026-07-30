#!/usr/bin/env bash
# The Gateway API release must be read from go.mod, never written as a literal.
#
# The conformance suite refuses to start when the installed CRDs' bundle-version
# annotation disagrees with its own module version, so a hand-written copy turns
# every renovate bump of sigs.k8s.io/gateway-api into a red pipeline nobody can
# fix without knowing about the second pin (job 15627486657: "the installed CRDs
# version is different from the suite version"). Both installers now derive it —
# tests/e2e from sigs.k8s.io/gateway-api/pkg/consts.BundleVersion, the dev-env
# script from `go list -m` — and this keeps a third from appearing.
#
# Scope is executable paths only. charts/haptic/values.yaml documents a
# prerequisite install command for operators; that version is a supported
# MINIMUM, not a must-match pin (TestGatewayAPIReleaseMatrix covers older
# releases), so pinning it is correct and it is deliberately not checked here.
#
# Usage: scripts/check-gwapi-version-pins.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

# `|| true`: no matches is the passing state, and grep exits 1 on it.
hits="$(grep -rnE 'gateway-api/releases/download/v[0-9]' \
	--include='*.sh' --include='*.go' --include='*.yml' --include='Makefile' \
	scripts tests .gitlab-ci.yml Makefile 2>/dev/null || true)"

if [ -n "$hits" ]; then
	echo "Hardcoded Gateway API release version:" >&2
	echo "$hits" >&2
	echo >&2
	echo "Read it from go.mod instead — a second pin goes stale on the next" >&2
	echo "sigs.k8s.io/gateway-api bump and the conformance suite then refuses" >&2
	echo "to start. Go: sigs.k8s.io/gateway-api/pkg/consts.BundleVersion." >&2
	echo "Shell: go list -m -f '{{.Version}}' sigs.k8s.io/gateway-api." >&2
	exit 1
fi

echo "  OK: Gateway API release version has no hardcoded copies"
