#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
for series in $(yq -r '.haproxyPatchVersions | keys | .[]' charts/haptic/values.yaml); do
    patch="$(yq -r ".haproxyPatchVersions[\"$series\"]" charts/haptic/values.yaml)"
    image="$(bash scripts/chart-haproxy-image.sh "$series")"
    if [[ "$image" != "haproxytech/haproxy-debian:$patch" ]]; then
        echo "HAProxy $series image does not match chart patch $patch: $image" >&2
        exit 1
    fi
done

default_series="$(yq -r '.haproxyVersion' charts/haptic/values.yaml)"
[[ "$(bash scripts/chart-haproxy-image.sh)" == "$(bash scripts/chart-haproxy-image.sh "$default_series")" ]]
for invalid_series in 99.0 '3.4@other' null; do
    if bash scripts/chart-haproxy-image.sh "$invalid_series" >/dev/null 2>&1; then
        echo "Chart image resolver accepted invalid series $invalid_series" >&2
        exit 1
    fi
done
echo "Chart HAProxy image tests passed"
