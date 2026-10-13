#!/usr/bin/env bash
set -euo pipefail
if [[ "${GOCACHE:-}" == /ci-go-cache/build ]]; then
    go clean -testcache
fi

git config --global --add safe.directory "$PWD"
case "$PILOT_SUITE" in
    compile)
        make build
        bin/haptic version > .pilot/binary-version.txt
        sha256sum bin/haptic > .pilot/binary-sha256.txt
        ;;
    ci-image)
        make build-ci-image HAPROXY_VERSION="$HAPROXY_VERSION"
        image="haptic-ci:$(scripts/ci-image-input-hash.sh)-hp${HAPROXY_VERSION}"
        docker image inspect "$image" > .pilot/built-toolchain-image.json
        docker run --rm --entrypoint bash "$image" -c 'go version && haproxy -v && kind version && helm version && cosign version'
        ;;
    docs-image)
        declare -A pins
        while IFS=$'\t' read -r name value; do pins["$name"]="$value"; done < <(yq -r '.variables | to_entries[] | [.key, .value] | @tsv' .gitlab/ci/build-pins.yml)
        args=()
        for pin in $(awk '/^ARG / { split($2, value, "="); print value[1] }' .gitlab/ci/images/python-docs/Dockerfile); do
            test -n "${pins[$pin]:-}"
            args+=(--build-arg "$pin=${pins[$pin]}")
        done
        docker build "${args[@]}" --tag haptic-docs:pilot .gitlab/ci/images/python-docs
        docker image inspect haptic-docs:pilot > .pilot/built-toolchain-image.json
        docker run --rm --entrypoint bash haptic-docs:pilot -c 'mkdocs --version && mike --version'
        ;;
    coverage-combined)
        profiles=()
        for shard in renderer rest; do
            profiles+=(".pilot/coverage-inputs/ci-evidence-coverage-$shard/.pilot/coverage.out")
        done
        for version in 3.0 3.1 3.2 3.3 3.4; do
            profiles+=(".pilot/coverage-inputs/ci-evidence-integration-$version/.pilot/coverage/integration.out")
        done
        for profile in "${profiles[@]}"; do test -s "$profile"; done
        go run github.com/wadey/gocovmerge@b5bfa59ec0adc420475f97f89b58045c721d761c "${profiles[@]}" > .pilot/combined.out
        go tool cover -func=.pilot/combined.out > .pilot/combined-coverage.txt
        tail -1 .pilot/combined-coverage.txt
        gocover-cobertura < .pilot/combined.out > .pilot/combined.xml
        ;;
    helm) make validate-helm-libraries HAPROXY_VERSION="$HAPROXY_VERSION" ;;
    chart-minimum) make lint-chart-ci KUBE_VERSION=1.33.0; scripts/check-chart-upgrade-baselines.sh ;;
    docs)
        apt-get -o Acquire::Check-Valid-Until=false update
        apt-get install --yes --no-install-recommends make
        make test-agent-skill-package
        mkdocs build --strict --config-file docs/site/mkdocs.yml --site-dir /tmp/docs-check
        mkdocs build --strict --config-file docs/landing/mkdocs.yml --site-dir /tmp/landing-check
        du -sb /tmp/docs-check /tmp/landing-check > .pilot/docs-sizes.txt
        make build-docs-preview
        ;;
    wasm)
        scripts/build-playground.sh "$PWD/public/playground/dev" dev
        base=public/playground/dev
        for file in index.html editor.js migration-assets.mjs migration-report.mjs playground.worker.js wasm_exec.js \
            playground.wasm playground.wasm.br playground.wasm.gz schemas.json migration/presets.json vendor/codemirror.js; do
            test -s "$base/$file"
        done
        test "$(find "$base/presets" -name '*.config.yaml' | wc -l)" -ge 6
        sha256sum "$base"/playground.wasm* > .pilot/wasm-sha256.txt
        du -sb "$base" > .pilot/wasm-size.txt
        jq --null-input --arg commit "$PILOT_SOURCE_SHA" --arg pipeline "$GITHUB_RUN_ID" --arg job "$GITHUB_JOB" \
            '{version:"dev", commit:$commit, pipeline:$pipeline, job:$job}' > "$base/release-identity.json"
        tar -C public -czf .pilot/playground.tar.gz playground/dev
        ;;
    snapshot)
        docker buildx create --driver docker-container --use --name pilot-builder --bootstrap
        CI_PIPELINE_ID="github-${PILOT_SOURCE_SHA:0:8}" make goreleaser-snapshot
        rg --files dist > .pilot/snapshot-files.txt
        cp dist/checksums.txt dist/metadata.json dist/artifacts.json .pilot/
        du -sb dist > .pilot/snapshot-size.txt
        make test-snapshot-publication
        ;;
    spoa)
        make spoa-bundle-check spoa-hub-image SPOA_DEV_IMAGE=haptic-spoa:pilot
        docker run --detach --name pilot-registry --publish 127.0.0.1:5000:5000 \
            registry@sha256:ddf754342cfc8acc51a56d5d0ab6af06826461864460636d8bd5c546dab2a7b8
        curl --fail --silent --show-error --retry 30 --retry-delay 1 --retry-all-errors \
            http://127.0.0.1:5000/v2/
        docker tag haptic-spoa:pilot 127.0.0.1:5000/haptic-spoa:pilot
        docker push 127.0.0.1:5000/haptic-spoa:pilot
        bash scripts/smoke-spoa-image.sh 127.0.0.1:5000/haptic-spoa:pilot
        syft scan docker:haptic-spoa:pilot -o cyclonedx-json=.pilot/spoa-sbom.cdx.json
        source versions-spoa.env
        docker buildx create --driver docker-container --use --name pilot-builder --bootstrap
        docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 \
            --build-arg "SPOA_HUB_VERSION=${SPOA_HUB_VERSION#v}" --build-context plugins=plugins \
            --output type=oci,dest=/tmp/spoa-multiarch.tar --metadata-file .pilot/spoa-multiarch.json \
            --provenance=false -f Dockerfile.spoa-hub .
        sha256sum /tmp/spoa-multiarch.tar > .pilot/spoa-multiarch-sha256.txt
        ;;
    *) printf 'Unknown build suite: %s\n' "$PILOT_SUITE" >&2; exit 2 ;;
esac
