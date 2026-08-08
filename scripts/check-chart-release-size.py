#!/usr/bin/env python3
"""Estimate the Helm release-Secret payload size for the haptic chart and fail
if it approaches the hard Kubernetes Secret limit (1,048,576 bytes).

Helm stores a release as base64(gzip(json(release))) in a Secret's data field.
After the conditional-subchart refactor (chart MR !1105) the dominant — in fact
near-total — contributor is `release.manifest` (the rendered output: the merged
HAProxyTemplateLibrary objects carrying every *enabled* library's
validationTests + templateSnippets, plus the operator's HAProxyTemplateConfig). The parent chart's own `templates/` + `libraries/` source
contribute the rest.

CRITICAL: the moved vendor/library SOURCE under `charts/<subchart>/` is NOT
stored in the release at all — Helm keeps loaded subcharts in an unexported
`Chart.dependencies` field that never serialises to JSON, and it isn't flattened
into the parent's files either. So a disabled subchart costs zero bytes, and an
enabled one costs only its gzipped *manifest* contribution. An estimator that
counts the packaged subchart source (as `helm package` bundles it) overstates
the real Secret by ~2-3x.

This reconstructs the stored release object offline (no cluster — `helm install
--dry-run=client` still contacts the apiserver under Helm 3.x, so it can't run
in the chart-test CI job):

  * file set from `helm package` (honours .helmignore), EXCLUDING `charts/**`
    (subchart source, not stored) and Chart.yaml / values.yaml (Helm stores
    those parsed, under `metadata` / `values` — see below);
  * `metadata` and `values` as the comment-stripped JSON Helm stores, via `yq`
    (the heavy comments in values.yaml are why counting it raw over-reports);
  * `release.manifest` from `helm template --api-versions=...` so the Gateway
    library (gated on a Capabilities check) renders for the profile under test.

Accuracy: verified against real installs on 2026-06-05, within ~1.5-2.5%,
biased slightly LOW (the stored release also carries info.notes + timestamps
this doesn't reproduce). The 950,000-byte gate threshold keeps ~7% net safety
vs the 1,048,576 limit even after that bias.
  profile        estimate     real install
  lean           314,200 B    322,212 B   (-2.5%)
  default        458,576 B    465,760 B   (-1.5%)
  all libraries   483,496 B    490,932 B   (-1.5%)

Because size is driven by which libraries are *enabled*, pass the same
`--set`/`-f` flags you would to `helm install`. The gate (`make chart-size-check`)
renders the worst case: every bundled library enabled, Gateway CRDs present.

Usage: check-chart-release-size.py <chart-dir> [helm-template-args...]
  e.g. check-chart-release-size.py charts/haptic \
         --set controller.templateLibraries.nginxIngress.enabled=true
Requires: helm, yq (both in the chart-test CI image). Exits non-zero if the
estimated payload exceeds THRESHOLD.
"""
import base64
import gzip
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile

LIMIT = 1_048_576
# Conservative gate: the chart would have to roughly double from today's ~46%
# worst case to trip this. It is the "installs will start failing" ceiling, with
# ~9% headroom that absorbs the estimator's ~2% low bias plus etcd/apiserver
# request overhead.
THRESHOLD = int(os.environ.get("CHART_RELEASE_SIZE_THRESHOLD", "950000"))
# Gateway library is gated on a Capabilities.APIVersions check; declare the CRDs
# so it renders (worst case). Mirrors the api-versions set in `make lint-chart-ci`.
API_VERSIONS = [
    "--api-versions=gateway.networking.k8s.io/v1/GatewayClass",
    "--api-versions=gateway.networking.k8s.io/v1/TCPRoute",
]
# Top-level chart files Helm stores parsed (metadata / values / schema / lock),
# NOT as raw entries in chart.files.
PARSED_OUT_OF_FILES = {"Chart.yaml", "Chart.lock", "values.yaml", "values.schema.json"}
# Helm's secrets/sql storage driver gzips at gzip.BestCompression (level 9).
GZIP_LEVEL = 9


def run(cmd):
    try:
        return subprocess.run(cmd, capture_output=True, check=True).stdout
    except subprocess.CalledProcessError as e:
        # Surface the tool's own stderr — without this a helm/yq failure aborts
        # with an opaque Python traceback that hides the real error in CI logs.
        sys.stderr.write(e.stderr.decode("utf-8", "replace"))
        sys.exit(f"command failed (exit {e.returncode}): {' '.join(cmd)}")
    except FileNotFoundError:
        sys.exit(f"required tool not found: {cmd[0]} (need helm + yq on PATH)")


def yaml_to_obj(path):
    """Parse a YAML file to a Python object via yq (comment-stripped, as Helm stores)."""
    return json.loads(run(["yq", "-o=json", ".", path]))


def collect_chart_source(chart):
    """templates[] + files[] exactly as Helm's release stores them: packaged file
    set minus charts/** (subchart source, unserialised) minus the parsed-out
    top-level files."""
    templates, files = [], []
    with tempfile.TemporaryDirectory() as td:
        run(["helm", "package", chart, "-d", td])
        tgz = next((f for f in os.listdir(td) if f.endswith(".tgz")), None)
        if tgz is None:
            sys.exit(f"helm package produced no .tgz under {td} for chart {chart!r}")
        with tarfile.open(os.path.join(td, tgz)) as tar:
            for m in tar.getmembers():
                if not m.isfile():
                    continue
                rel = m.name.split("/", 1)[1] if "/" in m.name else m.name
                if rel.startswith("charts/") or rel in PARSED_OUT_OF_FILES:
                    continue
                data = tar.extractfile(m).read()
                entry = {"name": rel, "data": base64.b64encode(data).decode()}
                (templates if rel.startswith("templates/") else files).append(entry)
    return templates, files


def main():
    chart = sys.argv[1] if len(sys.argv) > 1 else "charts/haptic"
    extra = sys.argv[2:]

    manifest = run(
        ["helm", "template", "haptic", chart] + API_VERSIONS + extra
    ).decode("utf-8", "replace")
    templates, files = collect_chart_source(chart)

    release = {
        "name": "haptic", "version": 1, "namespace": "haptic",
        "info": {"status": "deployed"},
        "chart": {
            "metadata": yaml_to_obj(os.path.join(chart, "Chart.yaml")),
            "templates": templates,
            "files": files,
            "values": yaml_to_obj(os.path.join(chart, "values.yaml")),
            "schema": "",
        },
        "config": {}, "manifest": manifest,
    }

    raw = json.dumps(release, separators=(",", ":")).encode()
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode="wb", compresslevel=GZIP_LEVEL) as g:
        g.write(raw)
    payload = len(base64.b64encode(buf.getvalue()))

    profile = " ".join(extra) or "default-values"
    pct = 100 * payload / LIMIT
    headroom = LIMIT - payload
    print(f"[{profile}] estimated release-Secret payload: {payload:,} bytes "
          f"({pct:.1f}% of the {LIMIT:,} limit; {headroom:,} bytes headroom; "
          f"gate threshold {THRESHOLD:,})")
    if payload > THRESHOLD:
        print(f"  ✗ FAIL: exceeds threshold {THRESHOLD:,} — `helm install` "
              f"will fail at/near the {LIMIT:,}-byte Secret limit.")
        return 1
    print("  ✓ OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
