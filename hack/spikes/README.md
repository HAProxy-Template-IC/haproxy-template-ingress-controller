# Spikes (0-pre of the reload-free propagation plan, 2026-08-17)

Measurements and HAProxy behaviour experiments that fixed the numbers and the
facts in ADR-0022 (HAPTIC agent, `deployplan`, dynamic backends). Each directory
holds the runnable scripts and a `RESULTS.md` with the evidence; the raw logs
they quote are reproduced by running the scripts (Docker + the
`haproxytech/haproxy-debian:3.0 … 3.4` images).

| directory | question | headline |
|---|---|---|
| `dpapi-push-curve/` | how the Data Plane API raw push scales vs. write + master reload | push 199 / 422 / 1145 ms at 300 / 1000 / 3000 routes (CPU-bound in dataplaneapi); write + reload 203 / 261 / 487 ms |
| `render-curve/` | HTTPRoute / Ingress render at 300–3000 routes, isolated and contended | HTTPRoute linear ~0.12 ms/route (360 ms at 3000); Ingress path maps superlinear; a background `haproxy -c` costs +4–6 % |
| `master-cli/` | master-socket framing, named-defaults inheritance, `add server` keywords, deferred deletes | `@1 c1; c2` relays only `c1`; `@@` absent on 3.0/3.1; `add backend … from` inherits `http-request` rules; `disable server` empties the idle pool |
| `maps-cli/` | runtime map forms, ordering, versioned replace, CLI limits, cert/crt-list ops | line form mangles values, payload form is byte-exact; `prepare/commit map` is atomic; 3.0 caps payloads at `tune.bufsize`; `add ssl crt-list` needs the payload form for options |

How to run: `bash <dir>/run.sh …` (see each `RESULTS.md` header). The
`render-curve/` scripts expect a copy of this repository at `$SPIKE/repo` with
`bin/haptic` built (`go build -o bin/haptic ./cmd/haptic`); set `SPIKE` to
override the directory. Nothing here runs in CI.
