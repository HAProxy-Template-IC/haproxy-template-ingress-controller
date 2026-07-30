#!/usr/bin/env bash
# Execute the span-building VRL from the rendered chart and assert the span
# GEOMETRY, not just that the template contains some text.
#
# This exists because the geometry is derived arithmetic over HAProxy's timers,
# and getting it wrong is silent: a malformed or mis-computed transform still
# emits spans, they are just positioned wrongly. The bug this locks in place had
# the upstream span end at `Tc + Tr` — the response HEADERS (config.txt 8.4) —
# so a response with a body closed the span early and an instrumented backend's
# own span overran it. With a 40ms body the span ended ~40ms too soon.
#
# Nothing offline can catch that: validationTests render templates but never run
# vector, and a helm-unittest text assertion would pass on any arithmetic that
# happens to contain the right identifiers.
#
# Usage: scripts/test-vector-spans.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="$REPO/charts/haptic"
VECTOR_IMAGE="${VECTOR_IMAGE:-timberio/vector:0.57.0-debian}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
info() { echo "==> $*" >&2; }

# No skip path. A leg that vanishes when a tool is missing reports green while
# testing nothing, which is how the bug above reached a live cluster.
command -v docker >/dev/null 2>&1 || fail "docker is required to run the VRL under vector"

info "rendering the chart and extracting the span transform"
helm template haptic "$CHART" --namespace haptic \
  --set controller.config.templatingSettings.extraContext.tracing.enabled=true \
  --set controller.config.templatingSettings.extraContext.tracing.otlp.endpoint=http://tempo:4318/v1/traces \
  > "$WORK/render.yaml" 2>/dev/null || fail "helm template failed"

python3 - "$WORK" <<'PY' || exit 1
import json, pathlib, sys, yaml
work = pathlib.Path(sys.argv[1])
tpl = None
for d in yaml.safe_load_all((work / "render.yaml").read_text()):
    if d and d.get("kind") == "HAProxyTemplateConfig":
        tpl = d["spec"]["files"]["vector.yaml"]["template"]
if tpl is None:
    sys.exit("no HAProxyTemplateConfig in the render")
try:
    i = tpl.index("m, merr = string(.message)")
    j = tpl.index(". = spans", i) + len(". = spans")
except ValueError:
    sys.exit("span transform not found in the rendered vector.yaml — did the VRL move?")
body = tpl[i:j].split("\n")
indent = min((len(l) - len(l.lstrip())) for l in body if l.strip())
(work / "prog.vrl").write_text("\n".join(l[indent:] if len(l) > indent else l for l in body))

# ts is microseconds now, so the fixture exercises a 6-digit fraction.
base = {"ts": "2026-07-30T09:00:00.123456Z", "req_id": "r", "trace_id": "a"*32,
        "span_id": "b"*16, "upstream_span_id": "c"*16, "parent_span_id": "",
        "trace_flags": "01", "handshake_time_ms": 5, "idle_time_ms": 0,
        "request_time_ms": 2, "queue_time_ms": 0, "retries": 0, "method": "GET",
        "path": "/x", "host": "h", "client_ip": "1.2.3.4", "term": "----",
        "denied_by": ""}   # `resource` is set per case: it identifies the batch
cases = {
    # The regression case: a 40ms response body. Tc+Tr would close at 6ms.
    "body":  dict(base, resource="case/body", connect_time_ms=1, response_time_ms=3, transfer_time_ms=40,
                  total_time_ms=46, status=200, backend="be", server="s1"),
    "504":   dict(base, resource="case/504", connect_time_ms=1, response_time_ms=-1, transfer_time_ms=-1,
                  total_time_ms=30, status=504, backend="be", server="s1", term="sH--"),
    "deny":  dict(base, resource="case/deny", connect_time_ms=-1, response_time_ms=-1, transfer_time_ms=-1,
                  total_time_ms=5, status=403, backend="", server="",
                  denied_by="waf:942100", term="PR--"),
    "unsampled": dict(base, resource="case/unsampled", connect_time_ms=1, response_time_ms=3, transfer_time_ms=0,
                      total_time_ms=5, status=200, backend="be", server="s1",
                      trace_flags="00"),
}
(work / "in.json").write_text("\n".join(json.dumps({"message": json.dumps(c)}) for c in cases.values()) + "\n")
(work / "names.json").write_text(json.dumps(list(cases)))
PY

info "running the VRL under $VECTOR_IMAGE"
# The program goes in as an argv element and the records on stdin, deliberately
# NOT as a bind mount. Under GitLab's docker:dind the daemon is a separate
# container, so `-v "$WORK:/w"` names a path only the JOB container has and
# vector fails with "io error: No such file or directory". argv and stdin are
# streams, so they behave identically on a laptop and under dind.
docker run --rm -i "$VECTOR_IMAGE" vrl "$(cat "$WORK/prog.vrl")" \
  < "$WORK/in.json" 2>"$WORK/vrl.err" | grep -v "INFO vector" > "$WORK/out.txt" || true
if ! grep -q '^\[' "$WORK/out.txt"; then
  echo "--- vector stderr ---"; cat "$WORK/vrl.err"
  fail "the VRL produced no spans — it did not compile, or every record aborted"
fi

python3 - "$WORK" <<'PY'
import json, pathlib, sys
work = pathlib.Path(sys.argv[1])
names = json.loads((work / "names.json").read_text())
batches = [json.loads(l) for l in (work / "out.txt").read_text().splitlines() if l.strip().startswith("[")]
# Match batches by the case's own resource, never by position: a case that
# legitimately emits nothing (an unsampled record) shifts every later index and
# would silently point the assertions at the wrong case.
got = {}
for b in batches:
    srv = [s for s in b if s["kind"] == 2]
    if not srv:
        continue
    tag = srv[0]["name"].split(" ", 1)[-1]          # "GET case/body" -> "case/body"
    got[tag.split("/", 1)[-1]] = b
unknown = set(got) - set(names)
if unknown:
    sys.exit(f"unrecognised case tags in output: {sorted(unknown)}")
errs = []

def spans(case):
    return got.get(case, [])

def one(case, kind):
    return [s for s in spans(case) if s["kind"] == kind]

# An unsampled record must produce nothing: HAProxy already decided, and
# re-deciding downstream is how half-sampled traces happen.
if "unsampled" in got:
    errs.append("an unsampled record (trace_flags=00) produced spans")

for case in ("body", "504"):
    srv, up = one(case, 2), one(case, 3)
    if len(srv) != 1: errs.append(f"{case}: expected 1 SERVER span, got {len(srv)}"); continue
    if len(up) != 1: errs.append(f"{case}: expected 1 upstream span, got {len(up)}"); continue
    s0, s1 = int(srv[0]["startTimeUnixNano"]), int(srv[0]["endTimeUnixNano"])
    u0, u1 = int(up[0]["startTimeUnixNano"]), int(up[0]["endTimeUnixNano"])
    # THE regression assertion: the upstream span ends exactly where the server
    # span ends. Summing Tc+Tr instead closes at the response headers.
    if u1 != s1:
        errs.append(f"{case}: upstream ends {(s1-u1)/1e6:+.2f}ms before the server span "
                    f"(must be equal — Ta-(TR+Tw) is Tc+Tr+Td)")
    if not (s0 <= u0 and u1 <= s1):
        errs.append(f"{case}: upstream span is not enclosed by the server span")
    if up[0].get("parentSpanId") != srv[0]["spanId"]:
        errs.append(f"{case}: upstream span is not a child of the SERVER span")
    if up[0]["spanId"] != "c" * 16:
        errs.append(f"{case}: upstream span must carry upstream_span_id (the id propagated in traceparent)")
    # Sub-millisecond anchor must survive: ts .123456 + Th 5ms.
    if s0 % 1_000_000 != 456_000:
        errs.append(f"{case}: microsecond precision lost from the anchor (start={s0})")

# 40ms body: the span must actually cover it, not close at Tc+Tr = 4ms.
if one("body", 3):
    up = one("body", 3)[0]
    dur = (int(up["endTimeUnixNano"]) - int(up["startTimeUnixNano"])) / 1e6
    if dur < 40:
        errs.append(f"body: upstream span is {dur:.2f}ms — the 40ms payload transfer is outside it")
    at = {a["key"]: list(a["value"].values())[0] for a in up["attributes"]}
    if at.get("haproxy.time.transfer_ms") != "40":
        errs.append(f"body: transfer_ms attribute missing or wrong: {at.get('haproxy.time.transfer_ms')}")

# 504: connected, no response. The span must exist and must not claim 0ms.
if one("504", 3):
    at = {a["key"]: list(a["value"].values())[0] for a in one("504", 3)[0]["attributes"]}
    if at.get("haproxy.time.connect_ms") != "1":
        errs.append("504: connect_ms should be 1")
    for absent in ("haproxy.time.response_ms", "haproxy.time.transfer_ms"):
        if absent in at:
            errs.append(f"504: {absent} must be omitted for a phase that never ran, not reported as 0")

# deny: HAProxy generated the response itself. One span, no upstream leg.
if one("deny", 3):
    errs.append("deny: an upstream span was emitted for a request that never reached a backend")
if len(one("deny", 2)) != 1:
    errs.append("deny: expected exactly 1 SERVER span")
else:
    at = {a["key"]: list(a["value"].values())[0] for a in one("deny", 2)[0]["attributes"]}
    if at.get("haptic.denied_by") != "waf:942100":
        errs.append("deny: denied_by attribute is missing — the coverage claim depends on it")
# And an empty attribute must be dropped rather than shipped.
if one("body", 2):
    at = {a["key"] for a in one("body", 2)[0]["attributes"]}
    if "haptic.denied_by" in at:
        errs.append("body: empty haptic.denied_by was not filtered out")

if errs:
    print("\n".join("  " + e for e in errs), file=sys.stderr)
    sys.exit(1)
print("  span geometry OK: %d cases" % len(got), file=sys.stderr)
PY

info "ALL CHECKS PASSED"
