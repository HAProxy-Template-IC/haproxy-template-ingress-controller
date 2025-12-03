#!/usr/bin/env bash

# Copyright 2025 Philipp Hossner
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
MODULE_NAME="haproxy-template-ic"

echo "Generating clientset, informers, and listers..."

# Ensure output directories exist
mkdir -p "${SCRIPT_ROOT}/pkg/generated/clientset"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/listers"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/informers"

# Clean previous generation to avoid stale files
rm -rf "${SCRIPT_ROOT}/pkg/generated/clientset"
rm -rf "${SCRIPT_ROOT}/pkg/generated/listers"
rm -rf "${SCRIPT_ROOT}/pkg/generated/informers"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/clientset"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/listers"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/informers"

# Generate clientset (skip gofmt to allow fixing hyphens first)
echo "  Generating clientset..."
go run k8s.io/code-generator/cmd/client-gen \
  --clientset-name "versioned" \
  --input-base "" \
  --input "${MODULE_NAME}/pkg/apis/haproxytemplate/v1alpha1" \
  --output-dir "${SCRIPT_ROOT}/pkg/generated/clientset" \
  --output-pkg "${MODULE_NAME}/pkg/generated/clientset" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  2>&1 || true  # Ignore formatting errors - we'll fix them next

# Generate listers (skip gofmt to allow fixing hyphens first)
echo "  Generating listers..."
go run k8s.io/code-generator/cmd/lister-gen \
  --output-dir "${SCRIPT_ROOT}/pkg/generated/listers" \
  --output-pkg "${MODULE_NAME}/pkg/generated/listers" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  "${MODULE_NAME}/pkg/apis/haproxytemplate/v1alpha1" \
  2>&1 || true  # Ignore formatting errors

# Generate informers (skip gofmt to allow fixing hyphens first)
echo "  Generating informers..."
go run k8s.io/code-generator/cmd/informer-gen \
  --versioned-clientset-package "${MODULE_NAME}/pkg/generated/clientset/versioned" \
  --listers-package "${MODULE_NAME}/pkg/generated/listers" \
  --output-dir "${SCRIPT_ROOT}/pkg/generated/informers" \
  --output-pkg "${MODULE_NAME}/pkg/generated/informers" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  "${MODULE_NAME}/pkg/apis/haproxytemplate/v1alpha1" \
  2>&1 || true  # Ignore formatting errors

# Fix hyphenated identifiers (client-gen doesn't handle module names with hyphens well)
# The generator creates invalid Go identifiers with hyphens. We need to fix all of them
# EXCEPT the import paths and domain names (which are in quotes/comments).
# Strategy: Replace all occurrences, then restore the import paths and domains.
# NOTE: gofmt runs during generation and adds spaces around hyphens it thinks are operators,
# so we also need to fix patterns like "Haproxy - template - ic" -> "HaproxyTemplateIC"
echo "  Fixing hyphenated identifiers..."
find "${SCRIPT_ROOT}/pkg/generated" -name "*.go" -type f -exec sed -i \
  -e 's/Haproxy - template - ic/HaproxyTemplateIC/g' \
  -e 's/Haproxy-template-ic/HaproxyTemplateIC/g' \
  -e 's/haproxy - template - ic/haproxytemplateic/g' \
  -e 's/haproxy-template-ic/haproxytemplateic/g' \
  -e 's/"haproxytemplateic\//"haproxy-template-ic\//g' \
  -e 's/haproxytemplateic\.gitlab\.io/haproxy-template-ic.gitlab.io/g' \
  {} +

# Format the fixed files
echo "  Formatting generated code..."
gofmt -w "${SCRIPT_ROOT}/pkg/generated"

echo "✓ Code generation complete"
