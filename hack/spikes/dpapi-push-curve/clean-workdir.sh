#!/usr/bin/env bash
# Remove a prepared workdir. The Dataplane API runs as root in the container and
# leaves root-owned 0700 dirs (dataplane/, transactions) in the bind mount, so
# the delete has to happen as root too.
set -euo pipefail
IMAGE=${IMAGE:-haproxytech/haproxy-debian:3.4}
target="$1"
[[ -d "$target" ]] || exit 0
parent="$(dirname "$target")"
base="$(basename "$target")"
docker run --rm --user 0:0 -v "$parent:/w" --entrypoint /bin/sh "$IMAGE" -c "rm -rf /w/$base"
