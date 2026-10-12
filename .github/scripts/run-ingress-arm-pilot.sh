#!/usr/bin/env bash
set -euo pipefail

fixture_dir="$PWD/.pilot/echo-source"
mkdir -p "$fixture_dir"
revision=d920ed36a0076e169a9a329a850844ab3a695ae8
for file in Dockerfile Makefile go.mod echoserver.go; do
    curl --fail --location --retry 3 \
        "https://raw.githubusercontent.com/kubernetes-sigs/ingress-controller-conformance/$revision/images/echoserver/$file" \
        --output "$fixture_dir/$file"
done
printf '%s\n' "$revision" > "$fixture_dir/source-revision.txt"
make -C "$fixture_dir" build-image PLATFORMS=linux/arm64 REGISTRY=haptic TAG=pilot
docker image inspect haptic/echoserver:pilot > .pilot/echo-image.json
kind load docker-image haptic/echoserver:pilot --name haptic-e2e
python3 - <<'PY'
from pathlib import Path
patch = Path('tests/conformance/patches/0002-use-mirrored-echoserver-image.patch')
source = patch.read_text()
old = 'registry.gitlab.com/haproxy-haptic/haptic/ingress-conformance-echo:v0.0.1@sha256:9b34b17f391f87fb2155f01da2f2f90b7a4a5c1110ed84cb5379faa4f570dc52'
assert source.count(old) == 1
patch.write_text(source.replace(old, 'haptic/echoserver:pilot'))
PY
set +e
make test-ingress-conformance 2>&1 | tee .pilot/ingress-conformance.log
result=${PIPESTATUS[0]}
set -e
printf '%s\n' "$result" > .pilot/ingress-conformance.exit
docker exec haptic-e2e-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf \
    logs -n haptic -l app.kubernetes.io/component=controller --all-containers=true \
    --prefix --tail=-1 --max-log-requests=20 > .pilot/ingress-controller.log 2>&1 || true
exit "$result"
