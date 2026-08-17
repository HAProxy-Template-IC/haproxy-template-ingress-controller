# Render curve spike — HTTPRoute and Ingress at 300 / 1000 / 1500 / 3000 routes

Measured 2026-08-17 against a copy of branch `fix/expected-render-error-output`
(`49396944`). The repository at `/home/phil/Quellcode/haptic/.claude/worktrees/tidy-nibbling-tower`
was **not modified**; everything ran in `…/scratchpad/spikes/render-curve/repo`, a `cp -r`
of that tree.

## Machine

| | |
|---|---|
| CPU | AMD Ryzen 7 5700X3D, 8 cores / **16 logical CPUs**, `performance` governor |
| RAM | 32 GiB (≈14 GiB in use, 24 GiB swap in use during the runs) |
| Go | 1.26.6 linux/amd64, **default `GOMAXPROCS` = 16** |
| HAProxy | local binary `/usr/bin/haproxy` **3.4.3** (no docker; container start-up would have polluted the timing) |
| OS | CachyOS/Arch, Linux 7.1.8 |
| Background load | The desktop was **not idle**: a game (`Megabonk.x86_64`, ~1 full core), Steam and Firefox were running; load average 8–14 throughout. Medians below are stable (±3 %), but absolute numbers are likely 5–15 % pessimistic versus an idle machine. Contention results are round-robin interleaved so drift hits every condition equally. |

## Blocker found: `scripts/test-benchmark.sh` is broken on `main`

The documented reproduce command in `docs/site/docs/operations/performance.md` fails today:

```
Error: benchmark for test "benchmark-httproute-10" failed: warm-up render failed:
rendering cert:default.pem: rendering template 'default.pem':
TLS Secret not found: haproxy-haptic/default-ssl-cert.
```

`create_all_tests_config()` does `yq eval 'del(.spec.validationTests)'` on **both** the
`HAProxyTemplateConfig` and the `HAProxyTemplateLibrary` objects. Post config-split (ADR-0016)
the `_global` validationTest — which pins the synthetic default certificate to namespace
`haptic` and supplies its Secret fixture — lives in the libraries, so the delete removes it
and every render fails on the default cert.

The spike ran a patched copy, `scripts/bench-spike.sh` (in the copied tree only), whose only
functional change is:

```diff
-    yq eval 'del(.spec.validationTests)' "$base_config" …
+    yq eval '.spec.validationTests |= with_entries(select(.key == "_global"))' "$base_config" …
```

plus a `KEEP_CONFIG` / `GEN_ONLY` hook so the generated config can be reused instead of
regenerated per run. **This is a real bug in the repo and should be fixed separately.**

## What the benchmark does and does not measure

`haptic benchmark` (`cmd/haptic/benchmark.go`, `benchmark_render.go`) compiles the templates
once, builds the fixture stores once, does a warm-up render, then times `--iterations`
renders of `haproxy.cfg` + every declared map + general file + certificate. It reports
**render only — no validation at all**. So "validate ms" here was measured two ways:

- **`haproxy -dr -c`** — the semantic phase, timed directly on the reconstructed artifact tree.
- **full 3-phase validation** — client-native syntax parse + OpenAPI schema check +
  `haproxy -c`, obtained as the delta between `haptic validate` with and without a
  `haproxy_valid` assertion on the same test. This is what `pkg/dataplane.ValidateConfiguration`
  costs, and it is what the pipeline (`pkg/controller/pipeline`, phase 2) runs after every render.

## 1. Render curve (isolated)

`scripts/(bench-spike|test-benchmark).sh --steps 300,1000,3000` runs all steps in one process.
Numbers below are the medians of **9 samples** (3 processes × 3 iterations). "Run A" is the
combined-config layout the script actually produces; "Run D" is one config per step, which is
what makes peak RSS attributable. They agree to within ~8 %; Run D is used for the derived
figures because its RSS is meaningful.

### HTTPRoute (Gateway API)

| Routes | Render total (ms) | `haproxy.cfg` (ms) | all maps (ms) | `haproxy -dr -c` (ms) | full validate (ms) | cfg bytes | cfg lines | map files | map bytes | peak RSS |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 300 | **37.9** | 33.3 | 5.0 | 55 | 99 | 544,348 | 6,974 | 29 | 98,941 | 90 MiB |
| 1,000 | **116.0** | 102.7 | 11.8 | 127 | 226 | 1,747,177 | 22,374 | 29 | 317,524 | 143 MiB |
| 1,500 | **161.2** | 146.1 | 14.9 | 176 | 375 | 2,620,737 | 33,374 | 29 | 478,109 | 188 MiB |
| 3,000 | **359.7** | 322.3 | 34.9 | 348 | 689 | 5,242,297 | 66,374 | 29 | 960,744 | 291 MiB |

Run A (combined config) totals: 37.8 / 111.8 / — / 331.8 ms.

### Ingress

| Routes | Render total (ms) | `haproxy.cfg` (ms) | all maps (ms) | `haproxy -dr -c` (ms) | full validate (ms) | cfg bytes | cfg lines | map files | map bytes | peak RSS |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 300 | **26.1** | 14.7 | 10.9 | 47 | 75 | 276,109 | 4,384 | 29 | 96,510 | 86 MiB |
| 1,000 | **88.6** | 45.0 | 42.8 | 81 | 187 | 824,481 | 13,491 | 29 | 309,479 | 140 MiB |
| 1,500 | **134.8** | 61.8 | 72.4 | 103 | 281 | 1,224,546 | 19,996 | 29 | 466,554 | 198 MiB |
| 3,000 | **401.5** | 127.8 | 273.3 | 186 | 552 | 2,425,621 | 39,511 | 29 | 938,659 | 358 MiB |

Run A (combined config) totals: 25.2 / 82.7 / — / 380.8 ms.

Every general-file set was 10 files / 15,402 bytes at every step (they don't scale with routes).
All `haproxy -dr -c` runs exited 0.

### Where the time goes (median ms, Run D)

| template | 300 | 1,000 | 1,500 | 3,000 | growth 300→3000 |
|---|---:|---:|---:|---:|---:|
| **HTTPRoute** `haproxy.cfg` | 33.28 | 102.70 | 146.14 | 322.34 | 9.7× |
| `map:pod-names.map` | 1.43 | 5.83 | 7.78 | 17.94 | 12.5× |
| `map:path-regex.map` | 1.43 | 2.25 | 2.98 | 6.79 | 4.7× |
| **Ingress** `haproxy.cfg` | 14.69 | 45.01 | 61.75 | 127.78 | 8.7× |
| `map:path-prefix.map` | 3.55 | 12.16 | 24.51 | **85.88** | 24.2× |
| `map:path-prefix-exact.map` | 2.31 | 9.29 | 14.46 | **80.37** | 34.8× |
| `map:path-exact.map` | 1.50 | 9.38 | 18.89 | **74.80** | 49.9× |
| `map:pod-names.map` | 1.51 | 7.03 | 7.76 | 18.81 | 12.5× |

HTTPRoute is linear: 0.126 / 0.116 / 0.107 / 0.120 ms per route, with `haproxy.cfg` carrying
90 % of the cost (the fixture routes match on header + query, which lands in the config, not
in maps).

Ingress is **not** linear: 0.087 / 0.089 / 0.090 / **0.134** ms per route. `haproxy.cfg` itself
stays linear (8.7× for 10× objects); the three path maps go 24–50× for 10× objects and are
**60 % of the whole render at 3,000** (241 of 401 ms). This is the same superlinearity
`performance.md` already flags for `path-prefix-exact`, now confirmed on all three path maps
and much sharper between 1,500 and 3,000 than below it.

## 2. Contention at 3,000 routes

5 rounds, each round running every condition once (3 iterations each) so machine drift is
shared. 15 samples per condition (30 for C2, both processes counted).

| Condition | HTTPRoute median (max) | ×C0 | Ingress median (max) | ×C0 |
|---|---:|---:|---:|---:|
| **C0** isolated, 16 CPUs | **337.9** (363.6) | 1.00 | **376.3** (407.2) | 1.00 |
| **C1** + background `haproxy -dr -c` loop on the same 3,000-route config | 357.2 (406.9) | **1.06** | 392.5 (437.5) | **1.04** |
| **C2** two concurrent render processes | 388.6 (418.0) | **1.15** | 570.8 (606.9) | **1.52** |
| **C3** isolated, pinned to 2 CPUs (`taskset -c 0,1`) | 458.2 (502.1) | 1.36 | 329.1 (354.2) | 0.87 |
| **C4** 2 CPUs + `haproxy -dr -c` loop on the same 2 CPUs | 619.0 (693.9) | **1.83** | 429.6 (500.4) | **1.14** |

`haproxy -dr -c` on the 3,000-route configs, isolated, median of 5:
**HTTPRoute 348 ms, Ingress 186 ms** (300 routes: 55 / 47 ms; 1,500: 176 / 103 ms).
The loop managed 28 checks during a C1 window and 42 during a C4 window.

C3 being *faster* than C0 for Ingress is not noise — it is a `GOMAXPROCS` effect. Pinning to
2 CPUs drops `GOMAXPROCS` to 2 and removes 16-way GC work from a machine that has other
tenants. A `GOMAXPROCS` sweep on the same configs (9 samples each) separates it from CPU count:

| `GOMAXPROCS` | HTTPRoute 3,000 (ms) | peak RSS | Ingress 3,000 (ms) | peak RSS |
|---:|---:|---:|---:|---:|
| 1 | **665.0** | 345 MiB | **438.6** | 291 MiB |
| 2 | 445.2 | 304 MiB | 363.9 | 276 MiB |
| 4 | 424.1 | 304 MiB | **330.5** | 292 MiB |
| 8 | **358.6** | 287 MiB | 345.5 | 311 MiB |
| 16 | 379.4 | 298 MiB | 460.4 | 356 MiB |

Render is **not** single-threaded work: `GOMAXPROCS=1` costs 1.85× (HTTPRoute) / 1.33×
(Ingress) versus the best setting. A 1-CPU controller pod pays that penalty. The sweet spot is
4–8; 16 is already past it for Ingress on a loaded host.

One structural fact that bounds admission concurrency more than any of the above:
`pkg/dataplane/validate_haproxy.go:30` is `var haproxyCheckGate = make(chan struct{}, 1)`.
Every `haproxy -c` in the process is **globally serialized**. Concurrent admission requests
queue behind one another for the semantic phase regardless of how many CPUs the pod has.

## 3. Admission latency estimate

The webhook path (`pkg/controller/dryrunvalidator` → `proposalvalidator` →
`pkg/controller/pipeline`) renders the full configuration for the proposed state, then runs
the same 3-phase validation. Estimate = isolated render + full validation:

| Routes | HTTPRoute render | + validation | **admission ≈** | Ingress render | + validation | **admission ≈** |
|---:|---:|---:|---:|---:|---:|---:|
| 300 | 37.9 | 99 | **137 ms** | 26.1 | 75 | **101 ms** |
| 1,000 | 116.0 | 226 | **342 ms** | 88.6 | 187 | **276 ms** |
| 1,500 | 161.2 | 375 | **536 ms** | 134.8 | 281 | **416 ms** |
| 3,000 | 359.7 | 689 | **1,049 ms** | 401.5 | 552 | **954 ms** |

Validation is the larger half from 1,000 routes upward, and roughly half of *it* is
`haproxy -c` (348 of 689 ms at HTTPRoute 3,000; 186 of 552 at Ingress 3,000) — the rest is the
client-native syntax parse plus OpenAPI schema check of a 2.4–5.2 MB config.

These are single-request, uncontended numbers on a 16-CPU host. Under the C4 shape (2 CPUs,
`haproxy -c` running concurrently) the render half alone rises 1.8×, and the `haproxyCheckGate`
serializes the validation half across concurrent admissions.

## Exact commands

```bash
# Repo copy (the original tree is untouched)
cp -r /home/phil/…/tidy-nibbling-tower "$SPIKE/repo" && rm -f "$SPIKE/repo/.git"
cd "$SPIKE/repo" && go build -o ./bin/haptic ./cmd/haptic

# Config generation (patched script; --steps N per file so RSS is attributable)
GEN_ONLY=1 KEEP_CONFIG=$SPIKE/configs/httproute-3000.yaml \
  ./scripts/bench-spike.sh --httproute-only --steps 3000
GEN_ONLY=1 KEEP_CONFIG=$SPIKE/configs/ingress-3000.yaml \
  ./scripts/bench-spike.sh --ingress-only  --steps 3000

# Render (Run D). Run A is the same binary on the combined 300,1000,3000 config.
./bin/haptic benchmark --file $SPIKE/configs/httproute-3000.yaml \
  --iterations 3 --schema-dir $SPIKE/repo/tests/schemas

# Rendered artifacts -> disk, then sizes
./bin/haptic validate -f $SPIKE/configs/httproute-3000.yaml \
  --test benchmark-httproute-3000 --schema-dir $SPIKE/repo/tests/schemas --dump-rendered
python3 extract.py raw/dump-httproute-3000.txt /tmp/rc/httproute-3000

# Semantic phase
cd /tmp/rc/httproute-3000 && haproxy -dr -c -f haproxy.cfg

# Full 3-phase validation = (validate with haproxy_valid) - (validate without)
yq '(select(.kind=="HAProxyTemplateConfig") | .spec.validationTests."benchmark-httproute-3000".assertions) += [{"type":"haproxy_valid"}]' \
  configs/httproute-3000.yaml > configs/httproute-3000-hv.yaml
./bin/haptic validate -f configs/httproute-3000-hv.yaml --test benchmark-httproute-3000 --schema-dir …

# Contention + GOMAXPROCS sweep
bash run-contend2.sh          # C0..C4, 5 interleaved rounds
KIND=ingress bash run-contend2.sh
bash run-gomax.sh
```

Driver scripts in this directory: `gen.sh`, `gen-single.sh`, `run-a.sh`, `run-c.sh`, `run-d.sh`,
`run-1500.sh`, `run-contend2.sh`, `run-gomax.sh`, `run-validate.sh`, `hc.sh`, `hcloop.sh`,
`extract.py`, `fixcrt.sh`, `runmax.py`, `analyze.py`, `permap.py`. Raw output in `raw/`,
aggregated medians in `summary.json`.

Two reconstruction caveats: `validate --dump-rendered` does not dump the `fileRegistry`
`crt-list` category, so `general/certificate-list.txt` (one line, the default certificate) is
recreated by `fixcrt.sh`; and the artifact tree is written under `/tmp/rc/...` (a symlink to
`out/`) because HAProxy's 97-character `unix@` socket-path limit rejects the full scratchpad
path.

## Reading

1. **HTTPRoute render is linear** (~0.12 ms/route, 9.7× for 10× objects); **Ingress render is
   not** — the three path maps grow 24–50× for 10× objects and are 60 % of the 3,000-route
   render, so Ingress crosses over and becomes the more expensive kind above ~2,000 routes.
2. **The 3,000-route number is ~360 ms (HTTPRoute) / ~400 ms (Ingress) for render alone**, and
   **~1.05 s / ~0.95 s for render + full validation**, which is the per-admission cost.
3. **Contention barely matters for a background `haproxy -c` (+4–6 %)**, matters moderately for
   a second concurrent render (+15 % HTTPRoute, +52 % Ingress), and matters a lot when both are
   squeezed onto 2 CPUs (+83 % HTTPRoute). `GOMAXPROCS=1` alone costs 1.3–1.9×, and
   `haproxyCheckGate` (capacity 1) serializes every `haproxy -c` in the process, so concurrent
   admissions queue on validation no matter the CPU budget.
4. **p50 < 300 ms at ~1,500 routes is plausible for render alone** (161 ms HTTPRoute, 135 ms
   Ingress, both with ≥45 % headroom even under C2-style contention) **but not for a
   render+validate probe** — that is 536 ms (HTTPRoute) / 416 ms (Ingress) at 1,500, already
   1.4–1.8× over the budget before any contention.
5. **Verdict on memoisation: needed only if the probe includes validation.** A route-add probe
   that only re-renders fits the 300 ms budget at 1,500 routes with room to spare, so do not
   build render memoisation for it. If the probe (or the webhook) must validate, memoising the
   render does not help the dominant half — validation — and the lever to pull first is the
   validation cache / `haproxyCheckGate`, not the renderer. Revisit if the probe target moves
   above ~2,500 routes, where render alone reaches ~300 ms.
