#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
input_manifest="$repo_root/.gitlab/ci/image-build-inputs.txt"
mapfile -t inputs <"$input_manifest"

if ((${#inputs[@]} == 0)); then
    echo "CI image build input set is empty" >&2
    exit 1
fi

cd "$repo_root"
for input in "${inputs[@]}"; do
    if [[ ! -f $input ]]; then
        echo "CI image build input is missing: $input" >&2
        exit 1
    fi
done

sha256sum "${inputs[@]}" | sha256sum | cut -c1-64
