#!/usr/bin/env bash
set -euo pipefail

tools_dir="$RUNNER_TEMP/haptic-arm-tools"
mkdir -p "$tools_dir"
fetch() {
    curl --fail --location --retry 7 --retry-all-errors --output "$1" "$2"
    printf '%s  %s\n' "$3" "$1" | sha256sum --check -
}
read -r bootstrap_version bootstrap_sha < <(python3 -c '
import json
from pathlib import Path
checksums = json.loads(Path(".github/native-tool-checksums.json").read_text())
version = checksums["bootstrap_yq_version"]
print(version, checksums["yq"][version])
')
fetch "$tools_dir/yq-bootstrap" "https://github.com/mikefarah/yq/releases/download/v$bootstrap_version/yq_linux_arm64" "$bootstrap_sha"
chmod +x "$tools_dir/yq-bootstrap"
"$tools_dir/yq-bootstrap" -o=json '.variables' .gitlab/ci/build-pins.yml > "$tools_dir/pins.json"
go_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["GO_BUILD_IMAGE"])' "$tools_dir/pins.json")
docker buildx imagetools inspect "$go_image" --format '{{json .Image}}' > "$tools_dir/go-image.json"
python3 -m scripts.ci.github_native_tools plan "$tools_dir/pins.json" "$tools_dir/go-image.json" > "$tools_dir/plan.json"
while IFS=$'\t' read -r filename url digest; do
    fetch "$tools_dir/$filename" "$url" "$digest"
done < <(python3 -c '
import json,sys
for tool in json.load(open(sys.argv[1]))["tools"]:
    print(tool["filename"], tool["url"], tool["sha256"], sep="\t")
' "$tools_dir/plan.json")
tar -xzf "$tools_dir/helm.tar.gz" -C "$tools_dir" linux-arm64/helm
mv "$tools_dir/linux-arm64/helm" "$tools_dir/helm"
chmod +x "$tools_dir"/{kind,helm,kubectl,yq,cosign}
printf '%s\n' "$tools_dir" >> "$GITHUB_PATH"
python3 -c 'import json,sys; print("HAPTIC_NATIVE_GO_VERSION=" + json.load(open(sys.argv[1]))["go_version"])' \
    "$tools_dir/plan.json" >> "$GITHUB_ENV"
