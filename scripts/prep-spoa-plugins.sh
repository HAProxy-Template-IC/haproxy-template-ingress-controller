#!/usr/bin/env bash
# Download and verify upstream SPOA plugin .so files for all architectures
# named in versions-spoa.env, stage them into ./plugins/<arch>/<libname>.so
# for the build-spoa-image job to consume via --build-context.
#
# Verification (per file):
#   1. SHA256 against the upstream-published `SHA256SUMS` manifest
#   2. cosign verify-blob against the upstream project's tag identity
#
# Outputs (overwrites):
#   plugins/<arch>/<libname>.so
#
# Requires: bash, curl, sha256sum, cosign, awk.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck disable=SC1091
source "${REPO_ROOT}/versions-spoa.env"

GITLAB_HOST="https://gitlab.com"
GITLAB_API="${GITLAB_HOST}/api/v4"
ISSUER="${GITLAB_HOST}"

# plugin shortname -> upstream version variable
declare -A PLUGINS=(
    [api-gateway]="${SPOA_PLUGIN_API_GATEWAY_VERSION}"
    [coraza]="${SPOA_PLUGIN_CORAZA_VERSION}"
    [external-auth]="${SPOA_PLUGIN_EXTERNAL_AUTH_VERSION}"
    [fingerprinting]="${SPOA_PLUGIN_FINGERPRINTING_VERSION}"
    [maxmind]="${SPOA_PLUGIN_MAXMIND_VERSION}"
    [mirror]="${SPOA_PLUGIN_MIRROR_VERSION}"
    [rate-limit]="${SPOA_PLUGIN_RATE_LIMIT_VERSION}"
    [sso-auth]="${SPOA_PLUGIN_SSO_AUTH_VERSION}"
)

# plugin shortname -> SO_NAME prefix (matches the upstream filename before
# the -<arch>-glibc<glibc> suffix). Most plugins follow the
# `lib<name>_plugin` convention; sso-auth's Cargo `name = ...` differs and
# produces `libhaproxy_spoa_hub_plugin_sso_auth.so` instead.
declare -A LIB_NAMES=(
    [api-gateway]="libapi_gateway_plugin"
    [coraza]="libcoraza_plugin"
    [external-auth]="libexternal_auth_plugin"
    [fingerprinting]="libfingerprinting_plugin"
    [maxmind]="libmaxmind_plugin"
    [mirror]="libmirror_plugin"
    [rate-limit]="librate_limit_plugin"
    [sso-auth]="libhaproxy_spoa_hub_plugin_sso_auth"
)

ARCHES=(amd64 arm64 armv7)

fetch() {
    local target="$1" url="$2" headers
    headers="$(mktemp "${WORKDIR}/headers.XXXXXX")"
    if ! curl --fail --silent --show-error --location \
        --retry 5 --retry-delay 2 --retry-all-errors \
        --dump-header "${headers}" \
        --output "${target}" "${url}"; then
        echo "  ERROR: download failed: ${url}" >&2
        # Redirect headers can contain credentials; report only safe support identifiers.
        LC_ALL=C awk '
            { sub(/\r$/, "") }
            $1 ~ /^HTTP\/[0-9.]+$/ && $2 ~ /^[0-9][0-9][0-9]$/ {
                print "    HTTP status: " $2
            }
            tolower($1) == "x-request-id:" && NF == 2 && length($2) <= 256 &&
                $2 ~ /^[A-Za-z0-9][A-Za-z0-9._:-]*$/ {
                print "    X-Request-ID: " $2
            }
        ' "${headers}" >&2
        exit 1
    fi
}

mkdir -p plugins
WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

verified=0
for plugin in "${!PLUGINS[@]}"; do
    version="${PLUGINS[${plugin}]}"
    lib_name="${LIB_NAMES[${plugin}]}"
    project_path="haproxy-haptic/haproxy-spoa-hub-plugin-${plugin}"
    encoded_path="haproxy-haptic%2Fhaproxy-spoa-hub-plugin-${plugin}"
    pkg_base="${GITLAB_API}/projects/${encoded_path}/packages/generic/${plugin}/${version}"
    identity="${GITLAB_HOST}/${project_path}//.gitlab-ci.yml@refs/tags/${version}"

    echo "==> ${plugin} ${version}"

    # Pull the SHA256SUMS manifest as the authoritative file list for this
    # plugin/version. Each line is "<sha256>  <filename>".
    sums="${WORKDIR}/${plugin}-SHA256SUMS"
    fetch "${sums}" "${pkg_base}/SHA256SUMS"

    for arch in "${ARCHES[@]}"; do
        suffixed="${lib_name}-${arch}-glibc${SPOA_PLUGIN_GLIBC_VERSION}.so"

        # Confirm this arch is in the manifest before downloading.
        if ! grep -q "  ${suffixed}\$" "${sums}"; then
            echo "  ERROR: ${suffixed} not present in upstream SHA256SUMS" >&2
            exit 1
        fi

        archdir="plugins/${arch}"
        mkdir -p "${archdir}"
        target_so="${archdir}/${lib_name}.so"
        bundle="${WORKDIR}/${suffixed}.cosign.bundle"

        fetch "${target_so}" "${pkg_base}/${suffixed}"
        fetch "${bundle}" "${pkg_base}/${suffixed}.cosign.bundle"

        # SHA256 check: compare expected line from manifest against the
        # downloaded file. We strip the arch+glibc suffix locally, so
        # `sha256sum -c` directly is awkward — recompute and compare.
        expected=$(awk -v f="${suffixed}" '$2 == f {print $1}' "${sums}")
        actual=$(sha256sum "${target_so}" | awk '{print $1}')
        if [ "${expected}" != "${actual}" ]; then
            echo "  ERROR: SHA256 mismatch for ${suffixed}" >&2
            echo "    expected: ${expected}" >&2
            echo "    actual:   ${actual}" >&2
            exit 1
        fi

        # Cosign keyless verification anchored to this plugin's project tag.
        cosign verify-blob "${target_so}" \
            --bundle "${bundle}" \
            --certificate-identity "${identity}" \
            --certificate-oidc-issuer "${ISSUER}" \
            > /dev/null

        verified=$((verified + 1))
        echo "  ${arch}: verified ${suffixed}"
    done
done

echo
echo "OK: verified ${verified} plugin .so files across ${#PLUGINS[@]} plugins x ${#ARCHES[@]} architectures."
