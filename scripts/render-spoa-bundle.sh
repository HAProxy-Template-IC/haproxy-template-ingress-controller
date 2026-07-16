#!/usr/bin/env bash
# Render the bundled spoa-hub component table from versions-spoa.env into
# docs/site/docs/operations/spoa-hub.md between the sentinel comments
# `<!-- BEGIN: spoa-hub-bundle -->` and `<!-- END: spoa-hub-bundle -->`.
#
# Usage:
#   scripts/render-spoa-bundle.sh           # rewrite the doc in place
#   scripts/render-spoa-bundle.sh --check   # exit non-zero if the doc drifts
#                                           # from versions-spoa.env

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${REPO_ROOT}/versions-spoa.env"
DOC_FILE="${REPO_ROOT}/docs/site/docs/operations/spoa-hub.md"
BEGIN_MARK='<!-- BEGIN: spoa-hub-bundle -->'
END_MARK='<!-- END: spoa-hub-bundle -->'

mode="write"
case "${1:-}" in
    --check) mode="check" ;;
    "") ;;
    *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

# Source the env file in a subshell so its variable assignments are
# captured without polluting the calling environment.
# shellcheck disable=SC1090
. "${ENV_FILE}"

render_table() {
    cat <<EOF
${BEGIN_MARK}

| Component       | Pinned version                          |
| --------------- | --------------------------------------- |
| Hub               | \`${SPOA_HUB_VERSION}\`                     |
| \`api-gateway\`    | \`${SPOA_PLUGIN_API_GATEWAY_VERSION}\`      |
| \`coraza\`          | \`${SPOA_PLUGIN_CORAZA_VERSION}\`           |
| \`external-auth\`   | \`${SPOA_PLUGIN_EXTERNAL_AUTH_VERSION}\`    |
| \`fingerprinting\`  | \`${SPOA_PLUGIN_FINGERPRINTING_VERSION}\`   |
| \`maxmind\`         | \`${SPOA_PLUGIN_MAXMIND_VERSION}\`          |
| \`mirror\`          | \`${SPOA_PLUGIN_MIRROR_VERSION}\`           |
| \`otel\`            | \`${SPOA_PLUGIN_OTEL_VERSION}\`             |
| \`rate-limit\`      | \`${SPOA_PLUGIN_RATE_LIMIT_VERSION}\`       |
| \`sso-auth\`        | \`${SPOA_PLUGIN_SSO_AUTH_VERSION}\`         |

Plugin \`.so\` files target glibc \`${SPOA_PLUGIN_GLIBC_VERSION}\` (Debian bookworm).

${END_MARK}
EOF
}

# Build the new file: everything before BEGIN_MARK, then the rendered
# block, then everything after END_MARK.
new_content=$(awk -v begin="${BEGIN_MARK}" -v end="${END_MARK}" '
    BEGIN { state = "before" }
    state == "before" {
        if ($0 == begin) { state = "in"; print "__RENDERED_BLOCK__"; next }
        print
    }
    state == "in" {
        if ($0 == end) { state = "after" }
        next
    }
    state == "after" { print }
' "${DOC_FILE}")

# Replace the placeholder with the freshly rendered block.
new_content=$(awk -v block="$(render_table)" '
    /__RENDERED_BLOCK__/ { print block; next }
    { print }
' <<<"${new_content}")

if [ "${mode}" = "check" ]; then
    if ! diff -u "${DOC_FILE}" <(printf '%s\n' "${new_content}"); then
        echo >&2
        echo "ERROR: ${DOC_FILE} is out of sync with versions-spoa.env." >&2
        echo "Run: scripts/render-spoa-bundle.sh" >&2
        exit 1
    fi
    echo "${DOC_FILE} is in sync with versions-spoa.env"
else
    printf '%s\n' "${new_content}" > "${DOC_FILE}"
    echo "Wrote ${DOC_FILE}"
fi
