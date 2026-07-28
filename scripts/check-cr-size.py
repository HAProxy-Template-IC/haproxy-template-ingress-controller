#!/usr/bin/env python3
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
"""Fail if any rendered HAProxyTemplateConfig approaches etcd's per-object limit.

etcd rejects a write above ~1.5 MiB, and the apiserver surfaces it as a plain
`etcdserver: request is too large` on `helm install`. Nothing in the repo used
to check this, so the ceiling was managed by prose — three places carried a
"don't enable all three vendor libraries at once" note — and a chart change that
crossed it surfaced as an unexplained e2e failure rather than a lint error.

This is the CR-side companion to check-chart-release-size.py, which measures the
Helm *release Secret*. The two ceilings are independent: splitting the config
across one object per template library relieves this one and leaves that one
untouched, because the release Secret is driven by total manifest bytes.

Size is measured as compact stored JSON — what the apiserver serialises into
etcd — not the rendered YAML, which is larger and not what the limit applies to.

Because size is driven by which libraries are *enabled*, pass the same
`--set`/`-f` flags you would to `helm install`. The gate (`make cr-size-check`)
renders the worst case: every bundled library enabled, Gateway CRDs present.

Usage: check-cr-size.py <chart-dir> [helm-template-args...]
  e.g. check-cr-size.py charts/haptic \
         --set controller.templateLibraries.nginxIngress.enabled=true
Requires: helm, yq. Exits non-zero if any object exceeds THRESHOLD.
"""
import json
import os
import subprocess
import sys

# etcd's default --max-request-bytes, which the apiserver enforces per object.
LIMIT = 1_572_864
# Gate well below the hard limit: crossing it breaks `helm install` outright, so
# the useful signal is "a library is getting too big", not "it just broke". With
# one object per template library the worst case is ~35%, so this leaves room
# for a library to double before anyone has to think about it again.
THRESHOLD = int(os.environ.get("CR_SIZE_THRESHOLD", "1100000"))
# Gateway library is gated on a Capabilities.APIVersions check; declare the CRDs
# so it renders (worst case). Mirrors check-chart-release-size.py.
API_VERSIONS = [
    "--api-versions=gateway.networking.k8s.io/v1/GatewayClass",
    "--api-versions=gateway.networking.k8s.io/v1/TCPRoute",
]
KIND = "HAProxyTemplateConfig"


def run(cmd, stdin=None):
    try:
        return subprocess.run(cmd, input=stdin, capture_output=True, check=True).stdout
    except subprocess.CalledProcessError as e:
        # Surface the tool's own stderr — without this a helm/yq failure aborts
        # with an opaque Python traceback that hides the real error in CI logs.
        sys.stderr.write(e.stderr.decode("utf-8", "replace"))
        sys.exit(f"command failed (exit {e.returncode}): {' '.join(cmd)}")
    except FileNotFoundError:
        sys.exit(f"required tool not found: {cmd[0]} (need helm + yq on PATH)")


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    chart_dir, helm_args = sys.argv[1], sys.argv[2:]

    manifest = run(["helm", "template", chart_dir, *API_VERSIONS, *helm_args])
    # -o=json -I=0 emits one compact JSON document per line: compact because
    # that is what etcd stores, one per line so each object can be measured
    # separately.
    documents = run(
        ["yq", "-o=json", "-I=0", f'select(.kind == "{KIND}")'], stdin=manifest
    )

    objects = []
    for line in documents.decode().splitlines():
        line = line.strip()
        if line:
            objects.append((json.loads(line)["metadata"]["name"], len(line)))

    if not objects:
        sys.exit(f"no {KIND} objects rendered — check the helm arguments")

    label = " ".join(helm_args) or "(chart defaults)"
    print(f"[{label}] {len(objects)} {KIND} object(s), limit {LIMIT:,}, gate {THRESHOLD:,}")
    for name, size in sorted(objects, key=lambda o: -o[1]):
        marker = "  ✗" if size > THRESHOLD else "   "
        print(f"{marker} {name:<48} {size:>9,}  {size * 100 / LIMIT:5.1f}% of limit")

    biggest_name, biggest = max(objects, key=lambda o: o[1])
    if biggest > THRESHOLD:
        sys.exit(
            f"\n{biggest_name} is {biggest:,} bytes, over the {THRESHOLD:,} gate "
            f"({biggest * 100 / LIMIT:.1f}% of etcd's {LIMIT:,} per-object limit).\n"
            "Split the library that grew, or move content into a new one — raising the "
            "gate only postpones an install failure that has no workaround."
        )
    print(f"  ✓ OK (largest: {biggest_name}, {biggest * 100 / LIMIT:.1f}% of the limit)")


if __name__ == "__main__":
    main()
