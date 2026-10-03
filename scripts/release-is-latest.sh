#!/usr/bin/env bash
# Usage: release-is-latest.sh <tag> < tags
# Reads tag names (bare `v1.2.3` or `git ls-remote --tags` lines) on stdin.
# Exit 0: <tag> is a stable release at least as high as every stable tag read,
# so `latest` may move to it. Exit 1: it is not (a prerelease, or an older line
# such as a maintenance release). Exit 2: the input does not contain <tag>.
set -euo pipefail

tag="${1:?usage: release-is-latest.sh <tag> < tags}"
stable='^v[0-9]+\.[0-9]+\.[0-9]+$'

tags="$(sed 's|^.*refs/tags/||')"
if ! grep -qxF -- "$tag" <<<"$tags"; then
    echo "release-is-latest: $tag is not in the tag list" >&2
    exit 2
fi
[[ "$tag" =~ $stable ]] || exit 1
highest="$(grep -E "$stable" <<<"$tags" | sort -V | tail -n 1)"
[[ "$highest" == "$tag" ]]
