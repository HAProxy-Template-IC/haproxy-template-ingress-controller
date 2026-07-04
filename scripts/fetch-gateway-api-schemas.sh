#!/usr/bin/env bash
# Fetch a Gateway API release's CRDs and split them into per-CRD schema files
# consumable by `controller validate --schema-dir` (pkg/k8s/schemafetcher's
# DirFetcher parses one CRD per file, non-recursive).
#
# Usage: scripts/fetch-gateway-api-schemas.sh <release> <channel> <outdir>
#   e.g. scripts/fetch-gateway-api-schemas.sh v1.1.0 standard tests/schemas-ga-v1.1
#
# The output directory contains ONLY the gateway CRDs of that release.
# Test harnesses merge it with the canonical non-gateway schemas from
# tests/schemas/ into a temp dir (see scripts/test-templates.sh --gateway-api).
set -euo pipefail

RELEASE="${1:?release, e.g. v1.1.0}"
CHANNEL="${2:?channel: standard|experimental}"
OUTDIR="${3:?output directory}"

URL="https://github.com/kubernetes-sigs/gateway-api/releases/download/${RELEASE}/${CHANNEL}-install.yaml"
mkdir -p "${OUTDIR}"

tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT
curl -fsSL "${URL}" -o "${tmp}"

# Split the multi-doc install manifest into one file per CRD, named after the
# CRD (matching the tests/schemas/ convention).
docs=$(yq eval-all '[.metadata.name] | length' "${tmp}")
i=0
while [ "${i}" -lt "${docs}" ]; do
  name=$(yq "select(documentIndex == ${i}) | .metadata.name" "${tmp}")
  kind=$(yq "select(documentIndex == ${i}) | .kind" "${tmp}")
  if [ "${kind}" = "CustomResourceDefinition" ]; then
    yq "select(documentIndex == ${i})" "${tmp}" > "${OUTDIR}/${name}.yaml"
    echo "wrote ${OUTDIR}/${name}.yaml"
  fi
  i=$((i + 1))
done
