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
        # The transform lives in its own snippet so Helm can drop it from the
        # config object when no OTLP endpoint is set. The chart renders one
        # object per library (ADR-0016); only the vector shard carries it.
        snip = (d.get("spec", {}).get("templateSnippets") or {}).get("vector-span-transform")
        if snip:
            tpl = snip["template"]
if tpl is None:
    sys.exit("no HAProxyTemplateConfig carries the vector-span-transform snippet")
try:
    i = tpl.index("m, merr = string(.message)")
    j = tpl.index(". = spans", i) + len(". = spans")
except ValueError:
    sys.exit("span transform not found in the rendered vector.yaml — did the VRL move?")
# The slice is Scriggo template source. It only behaves as VRL because the
# transform contains no template constructs; one `{%- ... %}` inside it and we
# would be feeding vector something it can never parse, from a test that is
# supposed to prove the opposite.
region = tpl[i:j]
if "{%" in region or "{#" in region or "{{" in region:
    sys.exit("the span transform contains template constructs — this harness executes the "
             "TEMPLATE, so the VRL must stay free of them (or switch to --dump-rendered)")
body = region.split("\n")
indent = min((len(l) - len(l.lstrip())) for l in body if l.strip())
(work / "prog.vrl").write_text("\n".join(l[indent:] if len(l) > indent else l for l in body))

# ts is microseconds now, so the fixture exercises a 6-digit fraction.
base = {"ts": "2026-07-30T09:00:00.123456Z", "req_id": "r", "trace_id": "a"*32,
        "span_id": "b"*16, "upstream_span_id": "c"*16, "parent_span_id": "",
        "trace_flags": "01", "handshake_time_ms": 5, "idle_time_ms": 0,
        "request_time_ms": 2, "queue_time_ms": 0, "retries": 0, "method": "GET",
        "path": "/p/unset", "host": "h", "frontend": "fe_https",
        "tls_version": "TLSv1.3", "tls_resumed": True,
        "destination_ip": "10.0.0.9", "instance_pod": "hap-1", "mtls_verify": "0", "client_ip": "1.2.3.4", "term": "----",
        "denied_by": "",
        # te_us: the exact end in epoch microseconds. ts is ...00.123456, so
        # this is deliberately NOT ts + Th+Ti+Ta — it carries the sub-millisecond
        # detail Ta's truncation drops, which is the whole point of the field.
        "te_us": 1785402000176900, "server_pod": "echo-abc-123", "service": "echo", "namespace": "default"}   # `resource` per case identifies the batch
cases = {
    # The regression case: a 40ms response body. Tc+Tr would close at 6ms.
    "body":  dict(base, path="/p/body", resource="case/body", connect_time_ms=1, response_time_ms=3, transfer_time_ms=40,
                  total_time_ms=46, status=200, backend="be", server="s1"),
    "504":   dict(base, path="/p/504", resource="case/504", connect_time_ms=1, response_time_ms=-1, transfer_time_ms=-1,
                  total_time_ms=30, status=504, backend="be", server="s1", term="sH--"),
    "deny":  dict(base, path="/p/deny", resource="case/deny", connect_time_ms=-1, response_time_ms=-1, transfer_time_ms=-1,
                  total_time_ms=5, status=403, backend="", server="",
                  denied_by="waf:942100", term="PR--"),
    # te_us=0 is the rollout window: vector has the new transform while HAProxy
    # still emits the previous log-format, so the req+Ta fallback is a real path.
    "no_te": dict(base, path="/p/no_te", resource="case/no_te", connect_time_ms=1, response_time_ms=3,
                  transfer_time_ms=0, total_time_ms=5, status=200, backend="be",
                  server="s1", te_us=0),
    # Ta=-1 (aborted transaction) clamps to 0, and with te_us absent the end
    # lands at req_ns while TR+Tw pushes the upstream start past it — the
    # negative-duration span this guards against.
    "aborted": dict(base, path="/p/aborted", resource="case/aborted", connect_time_ms=0, response_time_ms=-1,
                    transfer_time_ms=-1, total_time_ms=-1, status=-1, backend="be",
                    server="s1", te_us=0, request_time_ms=2, queue_time_ms=1),
    # A matched route: name and http.route come from the route TEMPLATE, not
    # the deeper URI path. The name keeps the host, http.route does not.
    "routed": dict(base, path="/p/routed/deep/uri", resource="case/routed", route="h.example/p/routed/*", connect_time_ms=1,
                   response_time_ms=3, transfer_time_ms=0, total_time_ms=5, status=200,
                   backend="be", server="s1"),
    # No route and no owning resource: a 404 to the default backend, which is
    # the bulk of internet-facing traffic. semconv wants the bare method here;
    # bracketing an empty resource named 34 of 40 live spans "GET []".
    "notarget": dict(base, path="/p/notarget", resource="", connect_time_ms=1,
                     response_time_ms=3, transfer_time_ms=0, total_time_ms=5,
                     status=404, backend="default_backend", server="<NOSRV>"),
    "unsampled": dict(base, path="/p/unsampled", resource="case/unsampled", connect_time_ms=1, response_time_ms=3, transfer_time_ms=0,
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
    at = {a["key"]: list(a["value"].values())[0] for a in srv[0]["attributes"]}
    parts = (at.get("url.path") or "").split("/")        # "/p/body" -> ["", "p", "body"]
    if len(parts) > 2:
        got[parts[2]] = b
unknown = set(got) - set(names)
if unknown:
    sys.exit(f"unrecognised case tags in output: {sorted(unknown)}")
errs = []
# ts = 2026-07-30T09:00:00.123456Z, Th=5, Ti=0 -> request start; Ta=5 for no_te.
ACCEPT_NS = 1785402000123456000
REQ_NS = ACCEPT_NS + 5 * 1_000_000
TE_NS = 1785402000176900 * 1000

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
    # The upstream span covers Tc+Tr+Td — HAProxy's own accounting for the
    # upstream leg — capped by the transaction end. It must NOT run to the
    # transaction end when the timers say the leg was shorter: that end includes
    # writing the response to the CLIENT, which is not upstream time.
    at_up = {a["key"]: list(a["value"].values())[0] for a in up[0]["attributes"]}
    legs = sum(int(at_up.get(f"haproxy.time.{k}_ms", 0)) for k in ("connect", "response", "transfer"))
    expect = min(u0 + legs * 1_000_000, s1)
    if u1 != expect:
        errs.append(f"{case}: upstream ends at {u1}, expected {expect} "
                    f"(Tc+Tr+Td = {legs}ms from its start, capped at the server end)")
    if not (s0 <= u0 and u1 <= s1):
        errs.append(f"{case}: upstream span is not enclosed by the server span")
    if up[0].get("parentSpanId") != srv[0]["spanId"]:
        errs.append(f"{case}: upstream span is not a child of the SERVER span")
    if up[0]["spanId"] != "c" * 16:
        errs.append(f"{case}: upstream span must carry upstream_span_id (the id propagated in traceparent)")
    # Sub-millisecond anchor must survive: ts .123456 + Th 5ms.
    if s0 % 1_000_000 != 456_000:
        errs.append(f"{case}: microsecond precision lost from the anchor (start={s0})")
    # The end must come from `te`, not from req+Ta: Ta is whole milliseconds and
    # closing on it loses whatever the truncation discarded.
    if s1 != TE_NS:
        errs.append(f"{case}: span end is not the exact te_us instant (end={s1}) — "
                    f"req+Ta truncation is back")

# http.route and the span name. Unbounded-cardinality names (the URI path) are
# what semconv forbids, so this pins both the routed and the fallback shape.
srv = one("routed", 2)
if len(srv) != 1:
    errs.append(f"routed: expected 1 SERVER span, got {len(srv)}")
else:
    at = {a["key"]: list(a["value"].values())[0] for a in srv[0]["attributes"]}
    if srv[0]["name"] != "GET h.example/p/routed/*":
        errs.append(f"routed: span name is {srv[0]['name']!r}, expected 'GET h.example/p/routed/*'")
    # The host belongs to the NAME only: http.route is the path part alone, or
    # the attribute stops being the semconv route template.
    if at.get("http.route") != "/p/routed/*":
        errs.append(f"routed: http.route is {at.get('http.route')!r}, expected '/p/routed/*'")
    if at.get("url.path") != "/p/routed/deep/uri":
        errs.append("routed: url.path must still carry the full request path")
    up = one("routed", 3)
    if up and up[0]["name"] != "Server session [be]":
        errs.append(f"routed: upstream span name is {up[0]['name']!r}, expected 'Server session [be]'")

# No route matched: the name falls back to the bracketed owning resource, and
# http.route must be ABSENT rather than present-and-empty.
srv = one("body", 2)
if len(srv) == 1:
    at = {a["key"]: list(a["value"].values())[0] for a in srv[0]["attributes"]}
    if srv[0]["name"] != "GET [case/body]":
        errs.append(f"body: span name is {srv[0]['name']!r}, expected 'GET [case/body]'")
    if "http.route" in at:
        errs.append("body: http.route is present with no route matched — empty attributes must be dropped")

# No route, no resource: the name is the bare method. Not "GET []", and not
# "GET " with a trailing space.
srv = one("notarget", 2)
if len(srv) != 1:
    errs.append(f"notarget: expected 1 SERVER span, got {len(srv)}")
else:
    at = {a["key"]: list(a["value"].values())[0] for a in srv[0]["attributes"]}
    if srv[0]["name"] != "GET":
        errs.append(f"notarget: span name is {srv[0]['name']!r}, expected 'GET'")
    if "http.route" in at:
        errs.append("notarget: http.route must be absent when nothing matched")

# No client IP reaches a span. Both the forwarded address and the raw TCP peer
# are personal data, and a trace travels further and lives longer than an access
# log. The fixture's client_ip is 1.2.3.4, so this catches the value leaking
# under ANY key, not just the two obvious ones.
BANNED_KEYS = {"client.address", "network.peer.address", "client.socket.address",
               "net.peer.ip", "http.client_ip"}
for case, batch in got.items():
    for sp in batch:
        for a in sp.get("attributes", []):
            if a["key"] in BANNED_KEYS:
                errs.append(f"{case}: {a['key']} exports a client IP")
            v = a.get("value", {}).get("stringValue")
            if isinstance(v, str) and "1.2.3.4" in v:
                errs.append(f"{case}: attribute {a['key']} leaks the client IP ({v})")

# Attribute placement. A client-side timer on the upstream span (or an
# upstream-leg one on the SERVER span) reads as a measurement of the wrong
# thing, which is worse than omitting it.
# denied_by is deliberately absent: a successful request has no denial, so
# forcing one here to satisfy the non-vacuity guard below would be a fixture
# that lies. The deny case asserts it separately.
CLIENT_ONLY = {"haproxy.time.client_handshake_ms", "haproxy.time.idle_ms",
               "haproxy.time.request_ms", "tls.protocol.version", "tls.resumed",
               "haproxy.frontend", "network.local.address", "haproxy.mtls_verify",
               "haptic.instance_pod"}
UPSTREAM_ONLY = {"haproxy.server", "haproxy.retries", "haproxy.time.queue_ms",
                 "k8s.pod.name", "k8s.service.name", "k8s.namespace.name",
                 "haproxy.time.connect_ms", "haproxy.time.response_ms",
                 "haproxy.time.transfer_ms"}
for case in ("body", "routed"):
    srv, up = one(case, 2), one(case, 3)
    if len(srv) != 1 or len(up) != 1:
        continue
    skeys = {a["key"] for a in srv[0]["attributes"]}
    ukeys = {a["key"] for a in up[0]["attributes"]}
    for k in sorted(CLIENT_ONLY & ukeys):
        errs.append(f"{case}: {k} is client-side but appears on the upstream span")
    for k in sorted(UPSTREAM_ONLY & skeys):
        errs.append(f"{case}: {k} describes the upstream leg but appears on the SERVER span")
    # A placement rule for an attribute no fixture emits can never fail. Assert
    # the inputs exist, so the checks above cannot quietly become decorative.
    for k in sorted(CLIENT_ONLY - skeys):
        errs.append(f"{case}: {k} is asserted client-side but no fixture emits it — the check is vacuous")
    for k in sorted(UPSTREAM_ONLY - ukeys):
        errs.append(f"{case}: {k} is asserted upstream but no fixture emits it — the check is vacuous")
    if "haproxy.server" not in ukeys:
        errs.append(f"{case}: the upstream span must name the server it called")
    # The slot name (SRV_1) is not the pod; both belong on the upstream span.
    if "k8s.pod.name" not in ukeys:
        errs.append(f"{case}: the upstream span must carry the backend pod name")
    if "k8s.pod.name" in skeys:
        errs.append(f"{case}: the pod name describes the upstream leg, not the SERVER span")
    # semconv: bare names, namespace separate. "default/echo" in either would
    # be a namespace-qualified value the convention does not use.
    at_up = {a["key"]: list(a["value"].values())[0] for a in up[0]["attributes"]}
    if at_up.get("k8s.service.name") != "echo":
        errs.append(f"{case}: k8s.service.name is {at_up.get('k8s.service.name')!r}, expected the bare 'echo'")
    if at_up.get("k8s.namespace.name") != "default":
        errs.append(f"{case}: k8s.namespace.name is {at_up.get('k8s.namespace.name')!r}, expected 'default'")
    if "k8s.service.name" in skeys:
        errs.append(f"{case}: the Service describes the upstream leg, not the SERVER span")
    if "haproxy.time.request_ms" not in skeys:
        errs.append(f"{case}: the SERVER span must keep the client-side request timer")

# The fallback: with te_us absent the end must come from req+Ta, and must NOT
# silently be the exact instant (which would mean the branch never ran).
srv = one("no_te", 2)
if len(srv) != 1:
    errs.append(f"no_te: expected 1 SERVER span, got {len(srv)}")
else:
    s1 = int(srv[0]["endTimeUnixNano"])
    expected = REQ_NS + 5 * 1_000_000
    if s1 == TE_NS:
        errs.append("no_te: end is the te_us instant — the fallback branch did not run")
    elif s1 != expected:
        errs.append(f"no_te: fallback end is {s1}, expected req+Ta = {expected}")
    up = one("no_te", 3)
    if len(up) == 1 and int(up[0]["endTimeUnixNano"]) != s1:
        errs.append("no_te: upstream span must still close with the SERVER span under the fallback")

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

# Applies to every case: OTLP consumers reject or mis-render a span whose end
# precedes its start, and it is the failure mode when two different measurement
# paths (an exact stamp vs summed whole-ms timers) are combined.
for case, batch in got.items():
    for sp in batch:
        s0, s1 = int(sp["startTimeUnixNano"]), int(sp["endTimeUnixNano"])
        if s1 < s0:
            errs.append(f"{case}: span {sp['name']!r} has a negative duration "
                        f"({(s1-s0)/1e6:.3f}ms)")

if errs:
    print("\n".join("  " + e for e in errs), file=sys.stderr)
    sys.exit(1)
print("  span geometry OK: %d cases" % len(got), file=sys.stderr)
PY

info "ALL CHECKS PASSED"
