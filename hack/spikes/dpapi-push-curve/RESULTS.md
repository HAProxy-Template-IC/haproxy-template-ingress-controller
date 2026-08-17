# Spike: how HAPTIC's config-push path scales with config size

Measured 2026-08-17 on the dev workstation (16 cores, 31 GB RAM, CachyOS,
Linux 7.1.8), `haproxytech/haproxy-debian:3.4` (HAProxy 3.4.3, Dataplane API
v3.4.2), Docker 29.7.2. Everything here is runnable: `./run.sh` (real configs)
or `SOURCE=synthetic ./run.sh`; see the script headers for the one-time
generation steps.

## What was measured

Per deployment, HAPTIC today (`pkg/dataplane/orchestrator.go`,
`pkg/dataplane/client/config.go`) does:

1. `GET /v3/services/haproxy/configuration/version`
2. when runtime actions exist: `POST …/configuration/raw?skip_reload=true&version=N`
   with the whole `haproxy.cfg`, then another `GET version`
3. `POST …/configuration/raw?force_reload=true&version=N` with the whole
   `haproxy.cfg` **again** — `PushRawConfiguration` sets `force_reload`; it does
   not use the `/reloads` endpoint
4. per map file: `PUT …/storage/maps/{name}?skip_reload=true`, or runtime map
   entry commands — preceded by `auxiliaryfiles.Compare`, which lists **and
   downloads every map file** on every sync to build the diff

Each raw push makes the Dataplane API parse the config with client-native, run
`validate_cmd` (`haproxy -dr -c -f <transaction file>`), write it, and re-parse.
The proposed agent path is: write the files, then
`echo reload | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`.

## Configs: real chart renders

`scripts/test-benchmark.sh --httproute-only --steps 300,1000,3000` generated the
benchmark `validationTests` from a `helm template` of `charts/haptic`, and
`haptic validate --dump-rendered` produced the actual `haproxy.cfg`, map files,
general files and cert, extracted to disk (`render-real.sh`,
`extract_rendered.py`). Two fixups were needed:

- the benchmark fixture puts `default-ssl-cert` in namespace `default` while the
  chart resolves it in the release namespace, so the render **aborts** on the
  missing Secret (`fix-secret-ns.py`). Worth fixing in the repo:
  `scripts/test-benchmark.sh` currently benchmarks a failing render.
- `haptic validate` renders every aux path absolute under a per-test temp dir;
  production renders them relative to `default-path origin`. `prepare.py`
  restores the relative form — which is what lets the Dataplane API address a
  runtime map as `maps/host.map` (with absolute paths, runtime map ops 400).

| N routes | `haproxy.cfg` | lines | map files | map bytes |
|---|---|---|---|---|
| 300  | 542,584 B (0.52 MiB) | 6,972 | 29 | 98,941 B |
| 1000 | 1,745,332 B (1.66 MiB) | 22,372 | 29 | 317,524 B |
| 3000 | 5,240,452 B (5.00 MiB) | 66,372 | 29 | 960,744 B |

The bundled chart renders considerably more per HTTPRoute than the
"~0.5–1 MB at 3000 routes" assumption: 5 MB at 3000, ~1.75 kB per route.

A synthetic generator (`gen_synthetic.py`: N backends × 5 servers, 25 maps) gives
a second, differently-shaped point on the size axis — appendix below.

## Results — real configs, 5 runs per N, p50 / max in ms

| measurement | N=300 | N=1000 | N=3000 |
|---|---|---|---|
| **a1** DPAPI raw push, `skip_reload=true` | 198.6 / 378.2 | 421.6 / 463.9 | 1145.3 / 1289.3 |
| **a2** DPAPI raw push, `force_reload=true` | 396.4 / 472.5 | 676.6 / 720.2 | 1601.9 / 1736.0 |
| **a2′** … until the reload reports done | 442.1 / 533.0 | 722.6 / 767.9 | 1647.3 / 1785.9 |
| **a0** `GET configuration/version` | 0.5 / 12.8 | 0.4 / 4.4 | 0.5 / 4.3 |
| **b0** read back all 29 map files (diff input) | 9.9 / 10.2 | 9.2 / 9.8 | 11.4 / 12.4 |
| **b1** storage map replace, one file, `skip_reload` | 0.6 / 0.7 | 0.8 / 1.4 | 1.4 / 5.4 |
| **b2** runtime map entry add | 0.8 / 1.1 | 0.9 / 0.9 | 0.7 / 1.2 |
| **c1** write `haproxy.cfg` + all 29 maps to disk | 2.5 / 3.1 | 4.6 / 5.0 | 9.9 / 21.6 |
| **c2** master-socket `reload` after that write | 200.8 / 202.4 | 255.9 / 259.3 | 478.4 / 757.0 |
| **c** write + reload — the agent path | 203.3 / 205.3 | 260.9 / 264.1 | 487.3 / 765.7 |
| **d** `haproxy -dr -c -f` (raw, via `docker exec`) | 143.5 / 156.0 | 230.0 / 233.9 | 488.9 / 691.0 |
| **d′** `docker exec` overhead baseline | 40.6 / 43.9 | 42.2 / 43.8 | 43.0 / 46.6 |
| **d″** validation alone (d − d′) | ≈103 | ≈188 | ≈446 |
| **e** master-socket `reload`, nothing changed | 154.3 / 203.4 | 256.2 / 259.1 | 484.8 / 590.0 |

Dataplane API CPU per push (utime+stime delta of the `dataplaneapi` process,
10 ms clock-tick resolution):

| | N=300 | N=1000 | N=3000 |
|---|---|---|---|
| `skip_reload=true` push | 140 ms | 410 ms | 1230 ms |
| `force_reload=true` push | 120 ms | 410 ms | 1170 ms |

CPU tracks a1's wall time almost exactly: the push is CPU-bound inside
`dataplaneapi` (parse → validate → write → re-parse), not I/O-bound.

## Reading

1. **The push grows linearly with config size and dominates everything else.**
   a1 goes 199 → 422 → 1145 ms over 0.52 → 1.66 → 5.00 MiB; normalised it is a
   flat 17–29 ms per 1000 config lines (28.5 / 18.8 / 17.3), and the synthetic
   set lands in the same band (33.1 / 22.0 / 19.9) despite ~2.4× fewer bytes per
   line — the cost tracks *directives parsed*, not bytes shipped. At 3000 routes
   HAPTIC burns **1.1 s of Dataplane API CPU just to stage a file**.
2. **`validate_cmd` is under half of that.** `haproxy -dr -c -f` alone costs
   ≈103 / 188 / 446 ms, i.e. 52 % / 45 % / 39 % of the a1 push. The remaining
   ≈95 / 234 / 700 ms is client-native parse + serialize + write + re-parse:
   pure API overhead that buys HAPTIC nothing (HAPTIC pushes raw text and reads
   nothing structured back).
3. **The reload itself is identical in both worlds.** a2 − a1 (198 / 255 /
   457 ms) matches e (154 / 256 / 485 ms). The agent path pays the same reload;
   what it removes is the staging.
4. **The agent path replaces a 1145 ms staging step with a 10 ms one.** c1 is
   2.5 / 4.6 / 9.9 ms — 80–115× cheaper than a1. End to end, today's reload path
   (a1 + a2′, both pushes, as `applyWithReload` issues them when runtime actions
   exist) is 641 / 1144 / 2793 ms against 203 / 261 / 487 ms for write + reload:
   **3.2× / 4.4× / 5.7×**, widening with size. Against the single-push variant
   (a2′ alone) it is still 2.2× / 2.8× / 3.4×. Re-adding an out-of-band
   `haproxy -dr -c` on the agent would cost ≈103 / 188 / 446 ms and remain
   cheaper than today.
5. **Map traffic is noise in both worlds.** Storage replace 0.6–1.4 ms, runtime
   entry add 0.7–0.9 ms, and reading back all 29 map files (0.94 MB at N=3000)
   to build the diff 11 ms. The whole-config raw push is the cost, not the maps.

## Side finding: the Dataplane API does not store what it is handed

`check-roundtrip.sh` pushes the N=300 config and compares it with the file on
disk: 542,584 B / 6,972 lines in, 539,856 B / 5,424 lines out. Same 4,120
directive lines and (almost) the same comments, but blank lines are collapsed
and directives are **reordered inside sections** (`default-path origin` and
`crt-base` move ahead of `log` / `hard-stop-after` in `global`), plus
`# _version=` and `# _md5hash=` headers. What HAProxy loads is client-native's
re-rendering of HAPTIC's render, not HAPTIC's render — so on-disk inspection,
hashes, and any diff against the rendered artefact are against a derived file
today. A plain file write removes that indirection.

## Caveats

- Backends point at unroutable addresses, so health checks fail continuously in
  the background. Constant across all measurements, but it is the main source of
  the max-vs-p50 spread on the reload rows.
- **d** runs through `docker exec`; d′ measures that overhead (~41–43 ms) on the
  same container and d″ subtracts it.
- **b2** needed one deviation to be measurable: the bundled chart ships no
  `stats socket`, and with `master_runtime` alone this Dataplane API lists no
  runtime maps (`GET /v3/…/runtime/maps` → `[]`). A worker `stats socket` was
  added for that row only (`add_stats_socket.py` documents it). The map
  identifier is the storage basename (`host.map`); the API prefixes `maps/`.
- The Dataplane API config mirrors the chart
  (`charts/haptic/templates/haproxy-deployment.yaml` lines ~338–433):
  `reload_delay: 1`, `reload_strategy: custom`, `master_worker_mode: false`,
  `validate_cmd: /etc/haproxy/validate.sh` → `haproxy -dr -c -f`. The 1 s
  `reload_delay` did **not** materialise as a delay in a2′.
- Single-host measurements: no network between controller and Dataplane API. In
  a cluster a 5 MB body also crosses the pod network on every push — a cost the
  agent path avoids and this spike does not capture.

## Appendix: synthetic configs (5 runs per N, p50 / max in ms)

`gen_synthetic.py`: N backends × 5 servers (`guid`, `default-server check`), one
frontend routing through maps, 25 map files with N entries each.

| N routes | `haproxy.cfg` | lines | map files | map bytes |
|---|---|---|---|---|
| 300  | 213,387 B | 4,249 | 25 | 297,575 B |
| 1000 | 710,868 B | 14,049 | 25 | 1,002,585 B |
| 3000 | 2,159,968 B | 42,049 | 25 | 3,110,585 B |

| measurement | N=300 | N=1000 | N=3000 |
|---|---|---|---|
| **a1** DPAPI raw push, `skip_reload=true` | 140.7 / 175.3 | 309.4 / 316.1 | 838.4 / 873.4 |
| **a2** DPAPI raw push, `force_reload=true` | 293.2 / 307.3 | 464.2 / 473.7 | 1101.8 / 1158.5 |
| **a2′** … until the reload reports done | 337.2 / 351.9 | 508.0 / 516.3 | 1151.0 / 1207.8 |
| **a0** `GET configuration/version` | 0.5 / 3.3 | 0.4 / 3.1 | 0.4 / 3.7 |
| **b0** read back all 25 map files | 7.9 / 9.2 | 8.2 / 8.4 | 9.7 / 19.6 |
| **b1** storage map replace | 0.5 / 0.6 | 0.5 / 0.9 | 1.0 / 1.5 |
| **b2** runtime map entry add | 0.9 / 1.0 | 1.0 / 8.8 | 1.1 / 3.8 |
| **c1** write cfg + all 25 maps to disk | 1.7 / 2.2 | 2.3 / 4.4 | 3.9 / 10.2 |
| **c2** master-socket `reload` after write | 149.1 / 162.3 | 152.4 / 152.8 | 265.1 / 266.0 |
| **c** write + reload (agent path) | 150.9 / 163.9 | 154.6 / 155.1 | 268.9 / 270.2 |
| **d** `haproxy -dr -c -f` (via `docker exec`) | 119.0 / 120.9 | 165.4 / 167.8 | 282.7 / 305.9 |
| **d′** `docker exec` overhead baseline | 41.6 / 46.1 | 44.0 / 47.1 | 41.9 / 43.2 |
| **e** master-socket `reload`, no change | 147.6 / 149.7 | 151.5 / 156.8 | 264.3 / 266.2 |

DPAPI CPU per `skip_reload` push: 90 / 310 / 920 ms. Same shape as the real set:
the push scales with directive count, the reload with backend/server count, and
the plain write stays in single-digit milliseconds.

## Files

| file | purpose |
|---|---|
| `run.sh` | driver: prepare → dpapi phase → plain phase → report, per N |
| `render-real.sh`, `extract_rendered.py`, `fix-secret-ns.py` | real chart renders → on-disk config trees |
| `gen_synthetic.py` | synthetic config generator |
| `prepare.py` | assemble `/etc/haproxy` (config + maps + certs + `dataplaneapi.yaml` + `reload.sh` + `validate.sh`) |
| `container.sh`, `clean-workdir.sh` | container lifecycle, root-owned workdir cleanup |
| `measure.py` | the measurements (`--phase dpapi` / `--phase plain`) |
| `report.py` | results JSON → markdown table |
| `check-roundtrip.sh` | pushed bytes vs stored bytes |
| `add_stats_socket.py`, `probe-runtime-map.sh` | the runtime-map deviation and how it was found |
| `results/*.json` | raw per-run timings (every sample, not just p50/max) |
| `repo-copy/` | copy of the repo used for rendering; the repo itself was not touched |
