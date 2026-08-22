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
this doesn't reproduce).
  profile        estimate     real install
  lean           314,200 B    322,212 B   (-2.5%)
  default        458,576 B    465,760 B   (-1.5%)
  all libraries   483,496 B    490,932 B   (-1.5%)

`make chart-size-check` gates REALISTIC install profiles, not the synthetic
"every library enabled" config no operator runs. A real operator enables at
most one vendor annotation-compat layer — the one matching their existing
controller — never all three, and never customCrdExample (a test/example
library). Measured 2026-08-22 with this estimator (bytes, % of the
1,048,576-byte hard limit):
  profile                                  bytes      pct
  default (ingress+gateway+haptic-annot.)  831,712    79.3%
  default + haproxytech                    850,992    81.2%
  default + haproxyIngress                 885,120    84.4%
  default + nginxIngress                   895,220    85.4%  <- realistic max
  all libraries at once (INFORMATIONAL)    961,236    91.7%
nginxIngress is heaviest because enabling it also auto-pulls the WAF/spoa-hub
sidecar (its modsecurity-snippet-style annotations need Coraza) — a real
consequence of that choice, not an artifact of this estimator.

THRESHOLD gates the default and realistic-max profiles (BLOCKING, `--label`,
no `--informational`); the all-libraries figure is rendered too but passed
`--informational` — printed as a high-water mark, never fails the build. See
the THRESHOLD constant below for why 90% of the hard limit was chosen.

Because size is driven by which libraries are *enabled*, pass the same
`--set`/`-f` flags you would to `helm install`.

Usage: check-chart-release-size.py <chart-dir> [--label TEXT] [--informational]
                                    [helm-template-args...]
  e.g. check-chart-release-size.py charts/haptic \
         --set controller.templateLibraries.nginxIngress.enabled=true
Requires: helm, yq (both in the chart-test CI image). Exits non-zero if the
estimated payload exceeds THRESHOLD, unless --informational is given (then it
always exits 0 and prints the number as a high-water mark instead).
"""
import base64
import gzip
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import tarfile
import tempfile

LIMIT = 1_048_576
# THRESHOLD gates the two REALISTIC profiles from the module docstring table:
# default (831,712 B, 79.3%) and default+nginxIngress, the realistic max
# (895,220 B, 85.4%). 90% of the hard limit (943,718) clears the realistic
# max by ~48,500 bytes (~46 KB) — comfortably over the ~30 KB margin this band
# is built to guarantee, so 90% is the number in use rather than a tighter one.
# This estimate also runs ~2% low vs a real install (see Accuracy above), which
# the margin already absorbs. Recompute both profiles (this script) before
# ever raising this: if the realistic-max margin drops under ~30 KB, move to
# realistic-max + ~40 KB instead — never above ~95% of the hard limit. Past
# that ceiling, split the chart into multiple Helm releases (separate Secrets,
# since the limit is per-Secret) instead of raising the threshold further.
THRESHOLD = int(os.environ.get("CHART_RELEASE_SIZE_THRESHOLD", "943718"))
# Gateway library is gated on a Capabilities.APIVersions check; declare the
# CRDs so it renders for every profile. Mirrors `make lint-chart-ci`.
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


# genSelfSignedCert/randAlphaNum regenerate fresh, incompressible randomness on
# every `helm template` run (a real install generates each once); normalized
# below to fixed same-size-class filler so the estimate is deterministic
# without understating the real, incompressible bytes a release also pays for.
def _filler(tag, n_chars):
    """Deterministic base64 filler of exactly `n_chars` characters."""
    raw = b""
    counter = 0
    while (len(raw) * 4) // 3 < n_chars:
        raw += hashlib.sha256(f"{tag}-{counter}".encode()).digest()
        counter += 1
    return base64.b64encode(raw).decode()[:n_chars]


# Lengths are the largest observed across repeated renders (RSA key DER
# encoding can wobble a few bytes run to run) so the filler never
# understates a real cert's size.
WEBHOOK_CERT_B64 = _filler("webhook-cert", 1728)
WEBHOOK_KEY_B64 = _filler("webhook-key", 2240)
DEFAULT_SSL_CERT_B64 = _filler("default-ssl-cert", 1520)
DEFAULT_SSL_KEY_B64 = _filler("default-ssl-key", 2240)
# 32 hex chars mirrors randAlphaNum(32)'s length; pinned via --set in main().
FIXED_DATAPLANE_PASSWORD = hashlib.sha256(b"chart-release-size-estimate-password").hexdigest()[:32]
FIXED_DATAPLANE_PASSWORD_B64 = base64.b64encode(FIXED_DATAPLANE_PASSWORD.encode()).decode()

SOURCE_RE = re.compile(r"^# Source: (\S+)\n", re.M)


def _wrapped_pem_body(b64, indent="        ", width=64):
    """Re-wrap `b64` at `width` columns under `indent` — the shape
    `.Values | toYaml` gives the webhook cert inside the pre-rollout
    validation hook's dumped values.yaml (raw PEM, not the Secret's b64enc)."""
    lines = [indent + b64[i:i + width] for i in range(0, len(b64), width)]
    return "\n".join(lines) + "\n"


def _patch_webhook_secret(body):
    body, n1 = re.subn(r"(?m)^(\s*tls\.crt:\s*)\S+", r"\1" + WEBHOOK_CERT_B64, body)
    body, n2 = re.subn(r"(?m)^(\s*tls\.key:\s*)\S+", r"\1" + WEBHOOK_KEY_B64, body)
    body, n3 = re.subn(r"(?m)^(\s*ca\.crt:\s*)\S+", r"\1" + WEBHOOK_CERT_B64, body)
    return body, {"tls.crt": n1, "tls.key": n2, "ca.crt": n3}


def _patch_default_ssl_cert(body):
    body, n1 = re.subn(r"(?m)^(\s*tls\.crt:\s*)\S+", r"\1" + DEFAULT_SSL_CERT_B64, body)
    body, n2 = re.subn(r"(?m)^(\s*tls\.key:\s*)\S+", r"\1" + DEFAULT_SSL_KEY_B64, body)
    return body, {"tls.crt": n1, "tls.key": n2}


def _patch_validating_webhook_configuration(body):
    body, n = re.subn(r"(?m)^(\s*caBundle:\s*)\S+", r"\1" + WEBHOOK_CERT_B64, body)
    return body, {"caBundle": n}


def _patch_pre_rollout_values_dump(body):
    """Normalize the webhook cert's second appearance: `pre-rollout-validation-
    hook.yaml` dumps the whole `.Values` tree (including the memoised
    _webhookSelfSignedCert), so the same random cert renders again here as raw
    PEM instead of the Secret's base64."""
    counts = {}
    for key, filler in (("ca", WEBHOOK_CERT_B64), ("crt", WEBHOOK_CERT_B64), ("key", WEBHOOK_KEY_B64)):
        body, counts[key] = re.subn(
            r"(?m)^(      " + key + r": \|\n)(?:        .*\n)+",
            lambda m, f=filler: m.group(1) + _wrapped_pem_body(f),
            body,
        )
    return body, counts


# Each patcher returns (patched_body, {field_name: match_count}). A source
# file can render more than one object (pre-rollout-validation-hook.yaml
# renders 5: only its Secret carries the values.yaml dump), so a single
# occurrence legitimately seeing zero matches is fine — normalize_random_content
# below aggregates counts across every occurrence of a source before judging
# whether a field actually matched nowhere.
PATCHERS = {
    "haptic/templates/webhook-cert-secret.yaml": _patch_webhook_secret,
    "haptic/templates/default-ssl-cert.yaml": _patch_default_ssl_cert,
    "haptic/templates/validatingwebhookconfiguration.yaml": _patch_validating_webhook_configuration,
    "haptic/templates/pre-rollout-validation-hook.yaml": _patch_pre_rollout_values_dump,
}


def normalize_random_content(manifest):
    """Replace every rendered appearance of the two chart-generated
    self-signed certs with fixed filler, scoped to each object's own
    `# Source:` block so no unrelated content is touched.

    Asserts each patcher's field matched at least once SOMEWHERE across the
    manifest: a silent 0-match wouldn't break the byte-identical-run invariant
    (the filler is the same size class as the random bytes it replaces) — it
    would just quietly let the chart's randomness back through. A future
    template refactor that breaks a patcher's regex fails loudly here instead.
    """
    parts = SOURCE_RE.split(manifest)
    out = [parts[0]]
    totals = {}
    for i in range(1, len(parts), 2):
        src, body = parts[i], parts[i + 1]
        patch = PATCHERS.get(src)
        if patch:
            body, counts = patch(body)
            agg = totals.setdefault(src, {})
            for field, n in counts.items():
                agg[field] = agg.get(field, 0) + n
        out.append(f"# Source: {src}\n{body}")
    for src, counts in totals.items():
        missing = [field for field, n in counts.items() if n == 0]
        assert not missing, (
            f"{src}: {', '.join(missing)} no longer match anywhere in the "
            "render — its YAML shape changed, update the patcher for this "
            "template in check-chart-release-size.py"
        )
    return "".join(out)


def main():
    argv = sys.argv[1:]
    informational = False
    label = None
    positional = []
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--informational":
            informational = True
        elif arg == "--label":
            i += 1
            label = argv[i]
        else:
            positional.append(arg)
        i += 1
    chart = positional[0] if positional else "charts/haptic"
    extra = positional[1:]

    # Pinned before `extra` so an explicit --set from the caller still wins;
    # this only fixes the common case (no password set) where randAlphaNum
    # would otherwise vary the Secret, both checksum/secret annotations, and
    # the pre-rollout-validation-hook's values.yaml dump on every render.
    manifest = run(
        ["helm", "template", "haptic", chart] + API_VERSIONS
        + ["--set", f"credentials.dataplane.password={FIXED_DATAPLANE_PASSWORD}"]
        + extra
    ).decode("utf-8", "replace")
    password_overridden = any("credentials.dataplane.password=" in a for a in extra)
    if not password_overridden:
        assert FIXED_DATAPLANE_PASSWORD_B64 in manifest, (
            "templates/secret.yaml: dataplane_password no longer carries the "
            "--set pin (values path renamed?) — update the --set in main()"
        )
    manifest = normalize_random_content(manifest)
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
    with gzip.GzipFile(fileobj=buf, mode="wb", compresslevel=GZIP_LEVEL, mtime=0) as g:
        g.write(raw)
    payload = len(base64.b64encode(buf.getvalue()))

    profile = label or " ".join(extra) or "default-values"
    pct = 100 * payload / LIMIT
    headroom = LIMIT - payload

    if informational:
        print(f"[{profile}] informational high-water mark: {payload:,} bytes "
              f"({pct:.1f}% of the {LIMIT:,} limit; {headroom:,} bytes headroom) "
              "— does not gate the build")
        return 0

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
