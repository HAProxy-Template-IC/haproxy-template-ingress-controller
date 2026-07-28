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

SCRIPT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Read the module path rather than restating it. A hardcoded "haptic" made every
# generator resolve haptic/pkg/apis/... , which is not a package, so all three
# failed while the script still exited 0.
MODULE_NAME=$(awk '/^module /{print $2; exit}' "${SCRIPT_ROOT}/go.mod")
[ -n "${MODULE_NAME}" ] || { echo "cannot read module path from go.mod" >&2; exit 1; }

API_PKG="${MODULE_NAME}/pkg/apis/haproxytemplate/v1alpha1"

# k8s.io/code-generator is an indirect dependency and is not vendored, so the
# repo's vendor/ directory makes `go run` on it fail. -mod=mod resolves it from
# the module cache instead. -u GOROOT matches the Makefile: an IDE-set GOROOT
# makes the generators look for our packages under the stdlib root.
GO=(env -u GOROOT go)
export GOFLAGS="${GOFLAGS:-} -mod=mod"

echo "Generating clientset, informers, and listers..."

# Generate into a staging tree and swap it in only once every generator has
# succeeded. The previous version deleted pkg/generated first, so any failure
# left the repo unbuildable with no generated code to fall back on.
# Staged inside pkg/generated so every rename below stays on one filesystem and
# is atomic. A dot-prefixed directory is ignored by the Go tool, so a partial
# tree here never enters a build.
mkdir -p "${SCRIPT_ROOT}/pkg/generated"
STAGE=$(mktemp -d "${SCRIPT_ROOT}/pkg/generated/.stage.XXXXXX")
cleanup() { rm -rf "${STAGE}"; }
trap cleanup EXIT

echo "  Generating clientset..."
"${GO[@]}" run k8s.io/code-generator/cmd/client-gen \
  --clientset-name "versioned" \
  --input-base "" \
  --input "${API_PKG}" \
  --output-dir "${STAGE}/clientset" \
  --output-pkg "${MODULE_NAME}/pkg/generated/clientset" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"

echo "  Generating listers..."
"${GO[@]}" run k8s.io/code-generator/cmd/lister-gen \
  --output-dir "${STAGE}/listers" \
  --output-pkg "${MODULE_NAME}/pkg/generated/listers" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  "${API_PKG}"

echo "  Generating informers..."
"${GO[@]}" run k8s.io/code-generator/cmd/informer-gen \
  --versioned-clientset-package "${MODULE_NAME}/pkg/generated/clientset/versioned" \
  --listers-package "${MODULE_NAME}/pkg/generated/listers" \
  --output-dir "${STAGE}/informers" \
  --output-pkg "${MODULE_NAME}/pkg/generated/informers" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  "${API_PKG}"

# A generator that writes nothing is a failure the exit code does not report.
for d in clientset listers informers; do
  [ -d "${STAGE}/${d}" ] && [ -n "$(find "${STAGE}/${d}" -name '*.go' -print -quit)" ] \
    || { echo "no Go files generated for ${d}" >&2; exit 1; }
done

# Strip the upstream boilerplate TODO code-generator emits in
# externalversions/generic.go — a comment about a hypothetical future "client
# pool" that is not actionable here and adds noise to every diff.
GENERIC_INFORMER="${STAGE}/informers/externalversions/generic.go"
if [ -f "${GENERIC_INFORMER}" ]; then
  sed -i '/^\/\/ TODO extend this to unknown resources with a client pool$/d' "${GENERIC_INFORMER}"
fi

echo "  Formatting generated code..."
gofmt -w "${STAGE}"

echo "  Installing generated code..."
# Move the old trees aside before installing any new one, and restore them if a
# later move fails. Deleting and moving one directory at a time would, on a
# failure partway through, leave that package missing entirely — the same
# "generation failed, now there is no generated code" hole this rewrite exists
# to close. BACKUP lives beside the destination so every rename stays within one
# filesystem and is therefore atomic.
BACKUP=$(mktemp -d "${SCRIPT_ROOT}/pkg/generated/.backup.XXXXXX")
restore() {
  for d in clientset listers informers; do
    if [ -d "${BACKUP}/${d}" ] && [ ! -d "${SCRIPT_ROOT}/pkg/generated/${d}" ]; then
      mv "${BACKUP}/${d}" "${SCRIPT_ROOT}/pkg/generated/${d}"
    fi
  done
  rm -rf "${BACKUP}"
  echo "install failed; previous generated code restored" >&2
}
trap 'restore; cleanup' EXIT

for d in clientset listers informers; do
  [ -d "${SCRIPT_ROOT}/pkg/generated/${d}" ] && mv "${SCRIPT_ROOT}/pkg/generated/${d}" "${BACKUP}/${d}"
done
for d in clientset listers informers; do
  mv "${STAGE}/${d}" "${SCRIPT_ROOT}/pkg/generated/${d}"
done

trap cleanup EXIT
rm -rf "${BACKUP}"

echo "✓ Code generation complete"
