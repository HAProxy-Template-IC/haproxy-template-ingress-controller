#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
# shellcheck source=scripts/lib/cluster.sh
. scripts/lib/cluster.sh
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
export KUBECONFIG="$test_dir/kubeconfig"

kind() {
  [ "$1" = create ] || return 0
  printf '%s\n' "$@" > "$test_dir/kind-args"
  printf 'server: https://0.0.0.0:12345\n' > "$KUBECONFIG"
  return "${create_result:-0}"
}
kubectl() { printf '%s\n' "$*" >> "$test_dir/kubectl-args"; }
docker() { :; }

for DOCKER_HOST in "" tcp://docker:2375; do
  for KIND_NODE_IMAGE in "" kindest/node:v1.33.0@sha256:test-digest; do
    kind_create_cluster minimum-test
    python3 - "$test_dir/kind-args" "$DOCKER_HOST" "$KIND_NODE_IMAGE" <<'PY'
import pathlib
import sys

args = pathlib.Path(sys.argv[1]).read_text().splitlines()
assert args[:4] == ["create", "cluster", "--name", "minimum-test"], args
assert ("--config" in args) == bool(sys.argv[2]), args
image = sys.argv[3]
if image:
    assert args[args.index("--image") + 1] == image, args
else:
    assert "--image" not in args, args
PY
    if [ -n "$DOCKER_HOST" ]; then
      grep -q 'https://docker:12345' "$KUBECONFIG"
    fi
  done
done

rm "$test_dir/kubectl-args"
create_result=1
if kind_create_cluster minimum-test; then
  echo "Cluster creation failure was ignored" >&2
  exit 1
fi
[ ! -e "$test_dir/kubectl-args" ]
echo "Kind node image selection tests passed"
