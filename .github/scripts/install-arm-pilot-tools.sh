#!/usr/bin/env bash
set -euo pipefail

tools_dir="$RUNNER_TEMP/haptic-arm-tools"
mkdir -p "$tools_dir"
cd "$tools_dir"
fetch() {
    curl --fail --location --retry 3 --output "$1" "$2"
    printf '%s  %s\n' "$3" "$1" | sha256sum --check -
}
fetch kind https://github.com/kubernetes-sigs/kind/releases/download/v0.32.0/kind-linux-arm64 b92cd615e97585de8ddade28ed5cd7feb4248d717c233eea5b03c37298900f5d
fetch helm.tar.gz https://get.helm.sh/helm-v3.20.1-linux-arm64.tar.gz 56b9d1b0e0efbb739be6e68a37860ace8ec9c7d3e6424e3b55d4c459bc3a0401
fetch kubectl https://dl.k8s.io/release/v1.36.3/bin/linux/arm64/kubectl 3d86f24401c41ae5a46ac50eef8865fe891d3647d324a0836f6c63757a126e62
fetch yq https://github.com/mikefarah/yq/releases/download/v4.53.3/yq_linux_arm64 578648e463a11c1b6db6010cbf41eafed6bee79466fcffa1bb446672cf7945ea
tar -xzf helm.tar.gz linux-arm64/helm
mv linux-arm64/helm helm
chmod +x kind helm kubectl yq
printf '%s\n' "$tools_dir" >> "$GITHUB_PATH"
