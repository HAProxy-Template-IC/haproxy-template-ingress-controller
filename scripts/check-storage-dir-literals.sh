#!/usr/bin/env bash
# An HAProxy storage path must come from pathResolver, never from a literal
# directory segment concatenated onto GetBaseDir().
#
# dataplane.mapsDir / sslCertsDir / generalStorageDir are operator knobs, and
# pathResolver.GetPath(name, type) is built from whatever they are set to.
# Concatenating "general" instead points the rendered directive at a directory
# the files were never pushed to. Nothing errors: the config renders, the files
# deploy, and the directive silently matches nothing — for the CRS ruleset that
# surfaces only as an admission rejection from the empty-glob gate, on a
# deployment whose only unusual act was renaming a path.
#
# Write:   pathResolver.GetBaseDir() + "/" + tostring(rel)   # rel from GetPath
# Not:     pathResolver.GetBaseDir() + "/general/..."
#
# A test cannot guard this. Assertions that pin the default directory fail
# against a renamed one even when the code is right, and assertions loose enough
# to survive both also match the wrong directory — which is exactly how the
# first attempt at a rendering profile for this passed with the bug reinstated.
#
# Usage: scripts/check-storage-dir-literals.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

# `|| true`: no matches is the passing state, and grep exits 1 on it.
hits="$(grep -rnE 'GetBaseDir\(\)[[:space:]]*\+[[:space:]]*"/(general|maps|ssl)' \
	--include='*.yaml' --include='*.yml' \
	charts/ 2>/dev/null || true)"

if [[ -n "$hits" ]]; then
	echo "ERROR: storage path built from a literal directory segment:" >&2
	echo "$hits" >&2
	echo >&2
	echo "Use pathResolver.GetPath(name, \"file\"|\"map\"|\"cert\"|\"crt-list\") — it" >&2
	echo "resolves against dataplane.generalStorageDir / mapsDir / sslCertsDir," >&2
	echo "which operators can rename. A literal points at a directory the files" >&2
	echo "were never pushed to, and the directive then matches nothing silently." >&2
	exit 1
fi

echo "OK: no storage paths built from literal directory segments"
