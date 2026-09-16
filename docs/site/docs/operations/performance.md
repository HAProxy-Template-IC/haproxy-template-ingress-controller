# Performance

## Overview

Tune HAPTIC in three areas:

- **Controller performance** - Template rendering, reconciliation cycles
- **HAProxy performance** - Load balancer throughput and latency
- **Kubernetes integration** - Resource watching and event handling

## Measured render cost by object count

A full render walks the whole watched-object store, so its cost scales with cluster size. This cost applies when the render cache is cold, such as on startup or after a configuration change. For the cost of a steady-state render after a single object changes, see [Incremental render cost](#incremental-render-cost).

These numbers come from `scripts/test-benchmark.sh` against the bundled chart's default libraries, with a realistic mix of one Ingress, one Service, and two EndpointSlices per step:

| Ingresses | Total render | Per Ingress | `haproxy.cfg` | Path maps |
|---|---|---|---|---|
| 100 | 15 ms | 0.15 ms | 10 ms | 2.6 ms |
| 1,000 | 113 ms | 0.11 ms | 77 ms | 20 ms |
| 5,000 | 742 ms | 0.15 ms | 386 ms | 302 ms |

Reproduce them with:

```bash
./scripts/test-benchmark.sh --ingress-only --steps 100,1000,5000 --iterations 3
```

In this measurement, total cost grows roughly with object count. Path-map rendering accounts for about 40% of the 5,000-Ingress render; measure your templates to identify their dominant cost.

Admission uses the same render service and can reuse its warm graph. Each admission still runs synchronous HAProxy validation, so render-only timings aren't admission latency. Measure the complete request against `controller.webhook.timeoutSeconds` (10 seconds by default).

## Incremental render cost

When template snippets declare `incremental`, a reconcile render re-executes only the components whose recorded inputs changed. The numbers below come from `cmd/haptic`'s incremental render-service benchmark against the bundled chart's Gateway API libraries on a 16-thread x86-64 desktop CPU. The routes are plain path-prefix routes, the shape the `gateway-api-bench` workloads create; a route with header and query matches costs within 10% of the same column.

| HTTPRoutes | First render (cold) | Nothing changed | One route changed | One route added | One endpoint changed |
|---|---|---|---|---|---|
| 300 | 600 ms | 2.2 ms | 14 ms | 19 ms | 3.3 ms |
| 1,000 | 1,022 ms | 2.3 ms | 18 ms | 26 ms | 3.6 ms |
| 3,000 | 2,415 ms | 2.3 ms | 27 ms | 50 ms | 3.9 ms |

Reproduce them with:

```bash
HAPTIC_BENCHMARK_BARE_ENGINE=1 HAPTIC_BENCHMARK_SKIP_ORACLE=1 \
  BENCH='^BenchmarkBundledChartHTTPRouteIncrementalRenderService$' PKG=./cmd/haptic make bench
```

`HAPTIC_BENCHMARK_BARE_ENGINE=1` renders on the chart's engine, as the controller does. Without it the benchmark wraps the engine to count component executions, and the document caches refuse a wrapped engine, so the timings then describe a controller without them. `HAPTIC_BENCHMARK_SKIP_ORACLE=1` skips the cold render the benchmark otherwise runs after every iteration to prove the incremental output equal to a cold one; leave it unset to run that check.

Cold renders cost more as the watched set grows. A follower also renders changes
to keep its graph warm, so it pays render CPU and cache memory even while only
the leader deploys.

In this measurement, unchanged output costs about 2.3 ms across all three sizes.
A changed route re-executes 13 components; an added route re-executes 14. Total
latency still rises with route count because document assembly and changed maps
also contribute. These are measurements of this workload, not fixed costs for
other templates.

## Gateway API implementation benchmark

`make bench-gateway-api` runs the exact probe, route-change, and route-scale programs used by the [`gateway-api-bench` Part 2 report](https://github.com/howardjohn/gateway-api-bench/blob/e81292ed876472804e0a2245876a7c445ab80881/README-v2.md). The runner checks out commit `e81292ed876472804e0a2245876a7c445ab80881` by default, verifies it, and builds the upstream Go programs in a temporary directory. For that snapshot, it also verifies and installs the Gateway API `v1.4.0` experimental bundle by its exact manifest digest.

Pinned source doesn't make the resulting numbers interchangeable with the public report. The report used joined, multi-controller runs with shared status-write contention and imported results through VictoriaMetrics. This runner uses one HAPTIC target in a dedicated Kind cluster, executes each self-paced upstream program from a static sibling container, and analyzes its raw logs directly. Use the result to find material gaps; don't present it as a reproduced public score.

### Workloads and verdicts

| Scenario | Upstream workload | Additional HAPTIC checks |
|----------|-------------------|--------------------------|
| `probe` | Create 3,000 HTTPRoutes sequentially; wait for each route's first HTTP `200` | Start with a ready backend. Require one sample per route and Gateway. Unexpected HTTP responses make the result negative even if each route eventually returns `200`. |
| `routechange` | Send continuous traffic through 20 route changes, 200 ms apart | Observe the response-header marker on every Gateway and HAProxy pod while its route variant is active. After cleanup, require `404`, no marker, and baseline config and map checksums. |
| `scale` | Create 50 namespaces with 100 applications each: one Pod, Service, and HTTPRoute per application, plus 20 simulated nodes | Require current route status, deployed config and maps, and two matching runtime-map reads from every current HAProxy worker before starting the 10-minute measurement window. |

Scale retains the upstream 500 ms grace period, 1 s configuration jitter, and
2 s workload jitter. During the measurement window, route identities must stay
stable, route paths and generations must advance, upstream refresh logs must
show activity, and HAPTIC must reconcile and deploy changes. These checks prove
temporal overlap between churn and controller activity; they don't map each
mutation to a deployment. A readiness deadline produces a negative result
without steady-state CPU or memory figures.

Across every scenario, error-counter increases or restarted/unhealthy supervised
children make the product result negative. Missing evidence, changed controller
identities, decreasing counters, or incomplete cleanup invalidate the run.
Cleanup must restore the workload, configuration, and map baselines.

Read `runner-summary.json`:

| Field | Meaning |
|-------|---------|
| `measured_result.pass`, `negative_scenarios` | Product quality: propagation, availability, convergence, and error outcomes |
| `harness.pass`, `harness.final_exit_code` | Evidence and cleanup validity, finalized after terminal checks |

A valid negative measurement exits `0`. A nonzero exit means the evidence or
cleanup is incomplete or invalid. **Check the measured result as well as the
process exit code.** Reload counts are diagnostic, not an availability verdict.

The runner covers these three Part 2 control-plane scenarios. It doesn't cover
attached routes, traffic throughput, ListenerSet, backend failover, or the
100-route propagation test running alongside scale.

### Resource measurements and provenance

The runner installs the pinned upstream Prometheus manifest and samples every
5 seconds. It retains values and source timestamps, requiring matching labels
and grids, advancing timestamps, and source ages no greater than 20 seconds.
Missing, duplicate, restarted, replaced, stale, or misaligned series fail analysis.

- `upstream_compatible_pod_cgroups` uses pod-root CPU and working-set series,
  excluding pause-container duplicates.
- `haptic_container_diagnostics` reports CPU, working set, and resident set for
  each real container.
- Both report time-aligned mean, p95, maximum, and last values. CPU also reports
  counter deltas, window length, and normalized cores.

Probe and route-change resource windows cover the upstream process only. Too few
fresh samples yield `analysis_status: not_gated`, `gating: false`, and `pass: null`;
malformed or stale samples still invalidate the evidence. Scale requires a
complete resource series for its proven steady-churn window.

The default profile starts from chart defaults, enables experimental Gateway API
fields to match the pinned CRDs, and removes CPU/memory limits and explicit
`GOMEMLIMIT` from measured pods. Artifacts retain effective settings, source digests, manifest digests,
image/binary/pod/container identities, child-process identities,
logs, metric responses, and terminal status. The mutable upstream backend image
is recorded by its actual runtime digest. The local SPOA bundle is checked
against `versions-spoa.env` before and after measurement.

Before accepting artifacts, the runner scans for raw and base64 forms of live
Secret values at least eight bytes long. Only the controller-owned SSL Secret's
`path` metadata is exempt; its certificate remains covered. A match is redacted
and fails the run. An incomplete Secret inventory, scan, or redaction replaces
the artifact tree with five files recording the invalid result. Helm captures
also redact passwords, Secret data, and webhook CA bundles.

### CI smoke profile

The manual `gateway-api-benchmark-smoke` GitLab job uses the same pinned source,
Gateway API bundle, HAProxy 3.4, 5,000-route scale workload, and route-change
workload. It reduces the probe to 300 routes and sets deployment and watcher
intervals to 100 ms. Its probe, scale-startup, scale-window, and route-change
bounds are 45, 20, 10, and 10 minutes. An outer watchdog and cleanup grace leave
time for checked artifact staging within the 2-hour-45-minute job limit.

This hosted-runner smoke records `published_workload_inputs_match: false` and
`controlled_default_profile: false`. Use it to check runner integration; use the
local 3,000-route default profile for Part 2 gap measurements. CI publishes full
artifacts only after trusted Secret inventory and scan verdicts, otherwise the
validated five-file failure result or `ci-wrapper-invalid.json`. It preserves
runner failures and fails if artifact staging fails.

### Results

Measured 2026-09-06 with the controlled default profile on a 16-thread `AMD Ryzen 7 5700X3D` CPU with 31 GB RAM, single-node Kind, HAProxy 3.4, Gateway API v1.4.0 Experimental, HAPTIC commit `eed0cf9c`. The published Part 2 numbers come from a different machine (a 16-core `AMD Ryzen 9 9950X` CPU with 96 GB) and a joined multi-controller run, so compare shapes, not digits.

Route propagation (`probe`, 3,000 sequential HTTPRoute creates, time from apply to first `200`):

| Implementation | Mean | Median | p99 | Max | HTTP errors |
|---|---:|---:|---:|---:|---:|
| HAPTIC | 133 ms | 118 ms | 253 ms | 329 ms | 0 |
| `Agentgateway` (published) | 16.6 ms | — | — | 74.6 ms | 0 |
| `Istio` (published) | 221 ms | — | — | 1.24 s | 0 |
| `Envoy Gateway` (published) | 320 ms | — | — | 1.21 s | 14,808 |
| `Nginx` (published) | 508 ms | — | — | 919 ms | 0 |

HAPTIC's mean by route count: 104 ms at 0–499 routes, 87 ms at 500–999, 104 ms at 1,000–1,499, 135 ms at 1,500–1,999, 166 ms at 2,000–2,499, and 201 ms at 2,500–2,999. The remaining slope is the per-change assembly and deployment of a configuration that grows with the route count; the render itself stays warm.

Before the 2026-09 fixes the same run on the same machine measured a 601 ms median and a 1,961 ms p99, and admission of a single HTTPRoute took over a second past 2,000 routes. Two costs grew with the route count: the controller's own Gateway `attachedRoutes` status write echoed back as a Gateway update and re-rendered every route, twice per create, and the admission webhook rendered on a private render service that never had a warm graph. The gateway library now ignores that counter with `ignoreFields`, and admission renders on the reconciliation service.

Route scale (`scale`, `BENCH_SCALE_NAMESPACES=20`, 2,000 routes under `pilot-load` churn, 10-minute steady window after the readiness proof). The published run creates 5,000 routes; on this machine that workload reached 4,804 routes at the 20-minute startup deadline because the per-change render and deployment both grow linearly with the route count (45 ms per change at 0 routes, 364 ms at 4,500), so the numbers below cover 2,000 routes and don't compare directly. The upstream window also spans the ramp-up, while HAPTIC's covers only steady churn.

| Component | Working set (mean) | CPU (mean cores) |
|---|---:|---:|
| HAPTIC controller, leader | 1.5 GiB | 0.89 |
| HAPTIC controller, standby | 1.0 GiB | 0.47 |
| HAPTIC HAProxy pod, each of two | 205 MiB | 0.06 |
| `kgateway` control plane (published, 5,000 routes) | 428 MiB | 0.10 |
| `istiod` (published, 5,000 routes) | 570 MiB | 0.21 |
| `Nginx Gateway Fabric` control plane (published, 5,000 routes) | 447 MiB | 0.41 |
| `Envoy Gateway` control plane (published, 5,000 routes) | 2.38 GiB | 3.70 |

During HAPTIC's window the leader completed 1,809 reconciliations and 1,153 deployments with no HAProxy reload, 3,186 runtime server operations, and zero adverse counter deltas. The standby's share is its own warm render graph, which is what makes a leader change start warm. The control-plane figures put HAPTIC between `Nginx Gateway Fabric` and `Envoy Gateway` at 40% of their route count; the linear per-change cost is the next lever.

Route change (`routechange`, 20 backend flips 200 ms apart under continuous traffic): 20,757 requests, 0 failures, which passes the upstream availability check. HAPTIC's stricter header-observation gate stays product-negative: adding a header name the Gateway's frontend doesn't carry yet needs a paced reload, and the workload re-adds it every other flip. Ten filter flips on a serving Gateway, timed from the start of the `kubectl apply` call to the first response reflecting the change, polled every 20 ms:

| Change | Apply call | Visible after apply returned | Mechanism |
|---|---:|---:|---|
| Remove the response header filter | ~300 ms | 40 ms | Runtime map delete |
| Add it back | ~300 ms | 2.4 s | Frontend directive, next paced reload |

The apply call covers the admission dry-run render and `haproxy -c`.

### Choose the appropriate performance test

| Test | Use it for | Don't use it for |
|---|---|---|
| `make bench-gateway-api` | Finding ballpark gaps against Part 2 with the pinned upstream programs and a controlled, isolated HAPTIC profile | Treating the local and published numbers as interchangeable scores, claiming full-suite coverage, or enforcing HAPTIC-specific budgets |
| `TestScale` | HAPTIC regression testing for full convergence, single-change latency at scale, controller memory, reloads, and explicit budgets | Cross-controller comparison with published `gateway-api-bench` results |
| `TestGatewayChurn` | Sustained parallel Gateway and HTTPRoute create/delete correctness, allocator isolation, oscillation bounds, and final quiescence | Published cross-controller latency, CPU, or memory comparison |

Run all three scenarios from the repository root:

```bash
make bench-gateway-api
```

Set `BENCH_SCENARIOS` to run a subset:

```bash
BENCH_SCENARIOS=routechange make bench-gateway-api
```

Set both HAPTIC timing controls to measure a tuned profile:

```bash
BENCH_DEPLOY_INTERVAL=100ms \
BENCH_WATCH_DEBOUNCE=100ms \
make bench-gateway-api
```

The artifacts record the requested, product-default, configured, and effective timing values. Any timing override makes the run a profile deviation, which is recorded in `metadata.json`.

The runner accepts these environment variables:

| Variable | Default | Effect |
|---|---|---|
| `BENCH_REF` | `e81292ed876472804e0a2245876a7c445ab80881` | Exact `gateway-api-bench` commit to check out and record |
| `BENCH_GATEWAY_API_VERSION` | `v1.4.0` | Gateway API release whose experimental CRD bundle is installed and verified |
| `BENCH_GATEWAY_API_CHANNEL` | `experimental` | Gateway API release channel (`experimental` or `standard`) |
| `BENCH_SCENARIOS` | `probe,scale,routechange` | Comma-separated scenario subset and execution order |
| `BENCH_OUTPUT_DIR` | `artifacts/gateway-api-bench/<YYYYMMDDtHHMMSSz>-<runner PID>` | Per-run result directory |
| `BENCH_GATEWAYS` | `haptic-bench/haptic` | Comma-separated namespace/name Gateway targets |
| `BENCH_PROBE_ROUTES` | `3000` | Sequential routes in the propagation scenario |
| `BENCH_PROBE_TIMEOUT` | `6h` | Hard timeout for the propagation program |
| `BENCH_ROUTECHANGE_ITERATIONS` | `20` | Route updates while traffic continues |
| `BENCH_ROUTECHANGE_GRACE_PERIOD` | `200ms` | Delay between route updates |
| `BENCH_ROUTECHANGE_TIMEOUT` | `10m` | Hard timeout for the route-change program |
| `BENCH_SCALE_NAMESPACES` | `50` | Namespaces in the scale workload |
| `BENCH_SCALE_ROUTES_PER_NAMESPACE` | `100` | Applications and routes per scale namespace |
| `BENCH_SCALE_DURATION` | `10m` | HAPTIC analysis duration after the scale readiness proof; accepts a positive integer followed by `s`, `m`, or `h` |
| `BENCH_SCALE_STARTUP_TIMEOUT` | `20m` | Maximum time for the scale workload to pass the HAPTIC readiness proof |
| `BENCH_DEPLOY_INTERVAL` | unset; chart default (`5s` at this commit) | Override HAPTIC's minimum structural-deployment interval |
| `BENCH_WATCH_DEBOUNCE` | unset; controller default (`100ms` at this commit) | Override the Gateway and HTTPRoute watcher debounce |
| `BENCH_KEEP_CLUSTER` | `false` | Keep a cluster that this runner created |
| `BENCH_ALLOW_DIRTY` | `false` | Allow an uncommitted HAPTIC tree and mark the result non-comparable |
| `BENCH_ALLOW_COSCHEDULED_CLUSTERS` | `false` | Allow other Kind clusters for a non-comparable smoke or debug run |
| `REUSE_CLUSTER` | `false` | Reuse an owned benchmark cluster and mark the result non-comparable |
| `BENCH_CLUSTER_NAME` | none | Existing benchmark cluster name required with `REUSE_CLUSTER=true` |
| `BENCH_DOCKER_NETWORK` | none | Existing benchmark Docker network required with `REUSE_CLUSTER=true` |
| `BENCH_CLUSTER_TOKEN` | none | Ownership token required with `REUSE_CLUSTER=true` |
| `BUILD_ONLY` | `false` | Verify the upstream checkout and build its programs without using a cluster |
| `HAPROXY_VERSION` | `3.4` from `versions.env` | HAProxy version for the fresh HAPTIC environment |

`BENCH_GATEWAY_API_CHANNEL` defaults to `experimental`; use `standard` only when intentionally measuring a different Gateway API schema profile. The Make target passes no positional arguments to the runner.

By default, the runner creates a unique `haptic-gwbench-*` Kind cluster and Docker network with an ownership token. Its kubeconfig is `/tmp/<cluster-name>.kubeconfig`, and its static workload container joins only that network. Cleanup verifies the ownership token before deleting the cluster or network. The controlled default profile rejects every other active Kind cluster, including `haptic-e2e` and `haptic-dev`, but never owns, reuses, or changes them. Set `BENCH_ALLOW_COSCHEDULED_CLUSTERS=true` only for smoke or debug runs; metadata marks their CPU, memory, and latency results non-comparable.

Set `BENCH_KEEP_CLUSTER=true` to retain a newly created benchmark cluster and its mode `0600` kubeconfig for inspection. Reuse requires `REUSE_CLUSTER=true` plus the exact cluster name, Docker network, and ownership token recorded by that run. Before replacing the retained kubeconfig, the runner generates a temporary one and uses it to verify the network, in-cluster ownership record, and HAPTIC release. Reused runs are recorded as non-comparable because they inherit cluster state.

The default clean-tree check stops uncommitted source from being mistaken for the recorded HAPTIC commit. `BENCH_ALLOW_DIRTY=true` marks the run non-comparable and retains a binary patch of tracked changes. For `untracked` files, it retains only the path list and content hashes, not their contents. Don't publish the artifacts when the tracked patch contains credentials or other private data.

`metadata.json` reports both `published_workload_inputs_match` and `controlled_default_profile`. The first means the pinned upstream commit, Gateway API bundle, Gateway target, and selected workload sizes match this wrapper's public-snapshot inputs. The second also requires a clean fresh cluster, no co-scheduling, product-default HAProxy and HAPTIC timings, and no reuse. Neither field claims that the local topology or score reproduces the joined public run.

The published Part 2 results are a reference, not a hardware-normalized score. They came from a single-node Kind cluster on a 16-core `AMD Ryzen 9 9950X` CPU with 96 GB RAM. Your CPU, memory, container runtime, co-scheduled workloads, Kubernetes version, isolated topology, and raw-log analysis affect absolute values. Retain the provenance artifacts and treat the published controller numbers as a ballpark.

Part 2's `Agentgateway` result uses `kgateway` as its control plane, so it's the relevant result in that report when looking for a HAPTIC control-plane gap. The [Part 1 result for `kgateway` v2.0.1](https://github.com/howardjohn/gateway-api-bench/blob/95b8373e4e2994c4c8c4b3119340cfa98af645fe/README.md) came from an older benchmark commit and isn't directly comparable with the default Part 2 profile.

## Controller resource sizing

### Recommended resources

| Deployment Size | CPU Request | CPU Limit | Memory Request | Memory Limit |
|-----------------|-------------|-----------|----------------|--------------|
| Small (<50 Ingresses) | 50m | 200m | 1Gi | 1Gi |
| Medium (50-200 Ingresses) | 100m | 500m | 1Gi | 1Gi |
| Large (200+ Ingresses) | 200m | 1000m | 1Gi | 2Gi |
| Very large (thousands of Ingresses) | 500m | 2000m | 2Gi | 4Gi |

Memory has a floor that no amount of shrinking the workload gets under: on every
config load the controller runs the bundled `validationTests`, which peaks at
514 MiB on chart defaults and 605 MiB with every template library enabled. That
is why the small and medium rows don't drop below 1Gi — the number is set by the
configuration being validated, not by how many Ingresses you serve. Above the
floor, the consumers that scale with your workload are the watched-resource
caches and render buffers (memory) and rendering plus watch streams (CPU).

!!! tip "Scaling past a few thousand Ingresses"
    Start by measuring the watched-resource cache, render graph, and startup tests. Narrow watches to relevant resources and use on-demand storage for large, infrequently read objects; see [Resource watching optimization](#resource-watching-optimization). Check `haproxy.shmStats.maxObjects` if you enable shared-memory stats.

!!! note "Chart defaults"
    The controller container requests `100m` CPU and `1Gi` memory, limits memory to `1Gi`, and has no CPU limit. The table above provides starting points if you choose CPU limits; measure your workload before adopting them.

Configure via Helm values. `controller.resources` applies to the controller container; HAProxy and the agent have their own blocks under `haproxy.resources` and `haproxy.agent.resources` (see [HAProxy Deployment](../haproxy-deployment.md)):

```yaml
# values.yaml
controller:
  resources:
    requests:
      cpu: 100m
      memory: 1Gi
    limits:
      # No CPU limit — avoids throttling GOMAXPROCS-aware Go under bursts.
      memory: 1Gi   # memory request == limit; no CPU limit → Burstable QoS (by design)
```

### Container awareness (`GOMAXPROCS` and `GOMEMLIMIT`)

The controller automatically detects and respects the limits you set above — no tuning env vars are needed:

- **CPU limits (GOMAXPROCS):** native cgroup-aware GOMAXPROCS (added upstream in Go 1.25; the controller currently builds with Go 1.27). The runtime adjusts GOMAXPROCS using the CPU quota and available cores, with a minimum of two unless fewer cores are available. This reduces scheduling overhead but doesn't prevent quota throttling.
- **Memory limits (GOMEMLIMIT):** the controller uses the `automemlimit` library to set GOMEMLIMIT to 90% of the container memory limit (10% headroom for non-heap memory), with both cgroups v1 and v2. GOMEMLIMIT is a soft garbage-collection target; it doesn't prevent all out-of-memory kills.

At startup the controller logs the detected limits, for example:

```
INFO HAPTIC starting ... gomaxprocs=8 gomemlimit="966367641 bytes (921.60 MiB)"
```

`gomemlimit` is about 90% of the 1Gi memory limit (921.6 MiB). Without a CPU limit, `gomaxprocs` follows the CPU cores available to the process. Check the startup log for the effective values.

The `AUTOMEMLIMIT` environment variable adjusts the memory limit ratio (default: 0.9; valid range `0.0 < AUTOMEMLIMIT <= 1.0`). Set `AUTOMEMLIMIT=off` to skip the automatic detection entirely; setting `GOMEMLIMIT` yourself also takes precedence, and the controller then leaves it alone. Set it via the chart's `controller.extraEnv` list, which is injected into the controller container:

```yaml
controller:
  extraEnv:
    - name: AUTOMEMLIMIT
      value: "0.8"   # Set GOMEMLIMIT to 80% of container memory limit
```

### Memory considerations

Memory usage scales with:

- Number of watched resources (Ingresses, Services, Endpoints)
- Size of template library
- Event buffer size (default 1000 events)
- Number of HAProxy pods being managed

Monitor memory usage:

```promql
container_memory_working_set_bytes{container="controller"}
```

### CPU considerations

CPU spikes occur during:

- Template rendering (complex templates with many resources)
- Initial resource synchronization (startup)
- Burst of resource changes (rolling updates)

Monitor CPU usage:

```promql
rate(container_cpu_usage_seconds_total{container="controller"}[5m])
```

## Reconciliation tuning

### Debounce interval (per-resource override, `100ms` default)

The resource watchers coalesce bursts of Kubernetes events via a leading-edge debouncer with a 100-millisecond refractory period (`pkg/k8s/types.DefaultDebounceInterval`). The first change in a quiet period fires immediately, so isolated updates are fast; only subsequent changes arriving within 100 ms are batched.

Each watched resource can override the window via `spec.watchedResources.<name>.debounceInterval`:

```yaml
watchedResources:
  httproutes:
    apiVersion: gateway.networking.k8s.io/v1
    resources: httproutes
    debounceInterval: "1s"     # batch harder where route churn is noisy
  endpointslices:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    debounceInterval: "0"      # fire immediately — no watcher delay for pod-IP changes (chart default)
```

Empty or invalid durations use the `100ms` default; `"0"` disables watcher
debouncing. The Reconciler adds no timer, but its coordinator coalesces triggers
that arrive during a render. Reload pacing is separate; see
[Deployment pacing](#deployment-pacing).

### Deployment pacing

CRD fields on `spec.dataplane` bound how often each pod reloads and how long the controller waits for it:

| Field | Default | Purpose |
|-------|---------|---------|
| `dataplane.minDeploymentInterval` | `2s` (Helm chart ships `5s`) | Shortest interval between two reloads of one pod. A reload inside the window is scheduled, never dropped |
| `dataplane.driftPreventionInterval` | `60s` | How often each pod re-hashes its tree and the controller re-applies on a disagreement; corrects external drift |
| `dataplane.configPublishInterval` | `10s` | Throttle for republishing the rendered config as the `HAProxyCfg` observability CRD; not on the deployment hot path |
| `dataplane.reloadVerificationTimeout` | `10s` (Helm chart ships `60s`, the agent's ceiling) | How long the agent waits for HAProxy to confirm a graceful reload before restoring the last known good file set |
| `dataplane.syncTimeout` | 2m | How long the controller waits for one pod to answer an apply |

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haptic-config
spec:
  dataplane:
    minDeploymentInterval: "2s"
    driftPreventionInterval: "60s"
```

### Graceful reload drain bound

HAProxy normally lets an old worker drain established connections after a
reload. HAPTIC bounds that drain with `hard-stop-after 10s` so a persistent
connection or master-socket subscriber can't retain stale worker generations
indefinitely. The chart's bootstrap worker uses the same bound when the
controller installs the first rendered configuration.

Tune both bootstrap and rendered configurations with one Helm value:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        hardStopAfter: 30s
```

Set `hardStopAfter: ""` to disable the bound. This isn't recommended for
production because a connection that never drains can otherwise retain an old
worker until its pod restarts. If you replace `haproxy.initialConfig` entirely,
include your own `hard-stop-after` directive in that custom bootstrap config.

Resource deletions trigger reconciliation through the same watch and debounce path as updates. Removing a dynamic backend can use the Runtime API on HAProxy 3.4; removing a structural backend requires a paced reload. Deleting a route that shares its backend may only change maps. See [Reload-free route changes](#reload-free-route-changes).

**Tuning guidelines:**

- Raise `minDeploymentInterval` in very high-churn environments to absorb more updates per reload (trades latency for fewer reloads), up to the agent's 60-second ceiling. It doesn't pace reload-free applies, which never fork the process.
- Keep `driftPreventionInterval` at or below 2 minutes so that a misbehaving external client can't hold HAProxy in a drifted state for long.
- Lower `reloadVerificationTimeout` to fail a stuck reload sooner and restore the last known good file set earlier. The agent rejects values above 60 seconds; the Helm chart already uses that ceiling.

### Reconciliation metrics

Monitor reconciliation performance:

```promql
# Average reconciliation duration
rate(haptic_reconciliation_duration_seconds_sum[5m]) /
rate(haptic_reconciliation_duration_seconds_count[5m])

# Reconciliation rate
rate(haptic_reconciliation_total[5m])

# P95 reconciliation latency
histogram_quantile(0.95, rate(haptic_reconciliation_duration_seconds_bucket[5m]))
```

**Target metrics:**

- Average reconciliation: <500 ms
- P95 reconciliation: <2 s
- Error rate: <1%

## Template optimization

### Efficient template patterns

**Use early filtering:**

```go
{#- GOOD: Filter early, process less data -#}
{%- var matching_ingresses = []any{} %}
{%- for _, ingress := range resources.ingresses.List() %}
  {%- if ingress.spec.ingressClassName == "haptic" %}
    {%- matching_ingresses = append(matching_ingresses, ingress) %}
  {%- end %}
{%- end %}
{%- for _, ingress := range matching_ingresses %}
  ...
{%- end %}

{#- ALTERNATIVE: Process with inline filtering -#}
{%- for _, ingress := range resources.ingresses.List() %}
  {%- if ingress.spec.ingressClassName == "haptic" %}
    ...
  {%- end %}
{%- end %}
```

**Use caching for expensive operations:**

The template engine exposes a thread-safe `shared` cache via `ComputeIfAbsent(key, factory)` / `Get(key)`. `ComputeIfAbsent` guarantees the factory runs exactly once per render even across concurrent template sections:

```go
{%- var _, _ = shared.ComputeIfAbsent("sorted_routes", func() any {
  var sorted = []any{}
  for _, route := range resources.httproutes.List() {
    sorted = append(sorted, route)
  }
  return sorted
}) -%}
{%- var analysis_routes = shared.Get("sorted_routes") %}
```

There is no `Set` method on the shared cache — this is deliberate and prevents racy check-then-act patterns. Use the `shared.ComputeIfAbsent` / `shared.Get` pair shown above for compute-once and read-only access respectively.

**Avoid nested loops when possible:**

```go
{#- AVOID: O(n*m) complexity -#}
{%- for _, ingress := range ingresses %}
  {%- for _, service := range services %}
    {%- if ingress.spec.defaultBackend.service.name == service.metadata.name %}
      ...
    {%- end %}
  {%- end %}
{%- end %}

{#- BETTER: Use indexing or filtering -#}
{%- var service_map = map[string]any{} %}
{%- for _, service := range services %}
  {%- service_map[service.metadata.name] = service %}
{%- end %}
{%- for _, ingress := range ingresses %}
  {%- var service = service_map[ingress.spec.defaultBackend.service.name] %}
  ...
{%- end %}
```

### Template debugging

Profile template rendering with the `validate` subcommand's tracing flags (the trace and include profile print to stdout; log lines go to stderr):

```bash
# Top-level render order with per-template timing
./bin/haptic validate -f config.yaml --trace-templates

# Full call tree including nested render/render_glob
./bin/haptic validate -f config.yaml --trace-templates --profile-includes

# Combine with --verbose and --dump-rendered for end-to-end diagnosis
./bin/haptic validate -f config.yaml --verbose --dump-rendered --trace-templates
```

### Measuring render time (`benchmark`)

`--trace-templates` tells you where a single render spends its time. The `benchmark` subcommand tells you whether a change made rendering faster or slower, by rendering the same validation test repeatedly and timing each pass. It separates template *compilation* from *rendering*, so a cold first render doesn't hide a warm-path regression:

```bash
# Every validation test in the config, 2 iterations each (the default)
./bin/haptic benchmark -f config.yaml

# One test, more iterations for a tighter median
./bin/haptic benchmark -f config.yaml --test benchmark-ingress-100 --iterations 10

# Rank the 20 slowest template includes
./bin/haptic benchmark -f config.yaml --profile-includes
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-f`, `--file` | — (required) | `HAProxyTemplateConfig` YAML to benchmark |
| `--test` | all tests | Validation test name to benchmark; repeatable |
| `--iterations` | `2` | Render passes per test |
| `--profile-includes` | `false` | Show include timing statistics (top 20 slowest) |
| `--schema-dir` | `$HAPTIC_SCHEMA_DIR` | Schemas for typed resource access. Without it, typed access falls back to untyped `resources["name"].List()`, which benchmarks a different code path than production |

Render the chart first so you benchmark what the controller actually assembles — see [Validate before deploying](./validate-before-deploy.md).

## HAProxy optimization

### Configuration parameters

Key HAProxy parameters for performance. Surface them as `extraContext` values in your HAProxyTemplateConfig so they can be tuned without editing templates:

```yaml
# HAProxyTemplateConfig
spec:
  templatingSettings:
    extraContext:
      maxconn: 2000
      nbthread: 4
      bufsize: 16384
```

Then reference them in your template (or override a built-in `global-settings-*` snippet):

```go
global
    maxconn {{ fallback(maxconn, 2000) }}
    nbthread {{ fallback(nbthread, 4) }}
    tune.bufsize {{ fallback(bufsize, 16384) }}

defaults
    timeout connect 5s
    timeout client 50s
    timeout server 50s
    timeout http-request 10s
    timeout queue 60s
```

### Connection limits

Calculate `maxconn` based on expected load:

```
maxconn = (expected_concurrent_connections * safety_factor) / num_haproxy_pods
```

Example:

- Expected: 10,000 concurrent connections
- Safety factor: 1.5
- HAProxy pods: 3
- `maxconn` = (10,000 * 1.5) / 3 = 5,000

### Thread configuration

The chart sizes `nbthread` for you — you rarely set it by hand:

- **No CPU limit (default).** The `nbthread` directive is omitted and HAProxy
  auto-detects all node cores from its CPU affinity. On a static on-prem node
  this uses every core without inflating CPU requests (which only fence off CPU
  from other pods rather than granting more cores).
- **CPU limit set.** The chart renders `nbthread = ceil(limits.cpu)`, matching
  the thread count to the CPU quota. The quota can still throttle busy threads. In the cloud, where the node is
  sized to fit the pod, set a limit to pin threads to that size.

```yaml
# Cap HAProxy to 4 threads (and 4 cores of CPU quota):
haproxy:
  resources:
    limits:
      cpu: 4        # chart renders `nbthread 4`
```

Set `haproxy.nbthread` explicitly only to override both branches (a positive
int pins the value; `0` force-omits the directive).

### Buffer sizing

Increase buffers for large headers or payloads:

```go
global
    tune.bufsize 32768        # 32KB for large headers
    tune.http.maxhdr 128      # Allow more headers
```

### Response compression

Responses are gzip-compressed by default. HAProxy compresses only what the backend left uncompressed, and only for the content types in the list — see [Compression](../libraries/haptic-annotations.md#compression) for the annotations that change the algorithm, the type list, or turn it off for one Ingress.

Compression costs CPU on the HAProxy pods. Two global limits bound that cost, both reachable through the `haproxy-haptic.org/config-global` annotation:

```yaml
metadata:
  annotations:
    haproxy-haptic.org/config-global: |
      maxcompcpuusage 80
      tune.comp.maxlevel 1
```

`maxcompcpuusage` stops compressing new sessions once compression exceeds that share of process CPU, so a traffic spike degrades to uncompressed responses instead of slowing every request. `tune.comp.maxlevel` is the compression level each session starts at; HAProxy's default of `1` is the cheapest and is what the chart runs with.

To measure the effect before changing anything, watch `haproxy_backend_http_comp_bytes_in_total` against `haproxy_backend_http_comp_bytes_out_total` — the ratio is the bandwidth you're actually saving, per backend.

### Password hash performance

HAProxy checks password hashes while parsing `userlist` entries. Expensive hashes
therefore affect `haproxy -c`, admission latency, reload time, and authentication
requests. Cost depends on the algorithm, work factor, user count, and CPU; measure
the complete configuration with the HAProxy binary you deploy.

For example, if one hash check takes 85 ms on your machine, 200 occurrences add
about 17 seconds to a parse. This is a sizing example, not a portable benchmark.
Avoid duplicate userlist entries. For large user sets, consider external
authentication rather than reducing password protection to fit a validation
budget.

[`htpasswd`](https://httpd.apache.org/docs/2.4/programs/htpasswd.html) uses `-B` for
bcrypt, `-2` for SHA-256 crypt, and `-5` for SHA-512 crypt. `-C` sets bcrypt's cost
factor; `-r` sets SHA-2 rounds. Its default Apache MD5 format (`$apr1$`) isn't
supported by HAProxy's system crypt implementation. Choose the algorithm and
work factor to meet your authentication policy, then measure the resulting cost.

## Scaling strategies

### Horizontal scaling

Scale HAProxy pods for increased traffic:

```bash
kubectl scale deployment haptic-haproxy --replicas=5 -n haptic
```

The controller automatically discovers new pods and deploys configuration.

### Controller scaling (HA mode)

Running multiple controller replicas adds failover and webhook capacity, not render/deploy throughput — only the leader deploys. See [High Availability](./high-availability.md) for configuration and sizing.

### Resource watching optimization

Reduce watched resources to minimize controller load:

```yaml
# Pin a watch to a single namespace (fieldSelector is a client-side
# JSONPath equality filter — see Watching Resources)
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      fieldSelector: "metadata.namespace=production"

# Or narrow by label selector on the resources themselves
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      labelSelector: "managed-by=haptic"
```

`labelSelector` is a comma-separated equality-only string (`k=v[,k=v]`) — the `matchLabels`/`matchExpressions` object form and set-based syntax (`in`, `notin`, `!`) aren't supported. For label-based namespace filtering, fall back to per-namespace `Role`/`RoleBinding`s and watch each namespace explicitly, or filter inside the template against a watched `namespaces` resource.

## Deployment performance

### Deployment latency

Monitor deployment time:

```promql
# Average deployment duration
rate(haptic_deployment_duration_seconds_sum[5m]) /
rate(haptic_deployment_duration_seconds_count[5m])

# P95 deployment latency
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))
```

**Target metrics:**

- Average deployment: <1 s per HAProxy pod
- P95 deployment: <3 s

### Reload-free route changes

Adding or removing a route can update the running HAProxy worker over the runtime API instead of forking a new process, so the change takes effect without dropping in-flight connections and without a reload. Whether a given route qualifies depends on the HAProxy version and the route's backend:

| HAProxy version | Plain route (empty backend body, map-driven logic) | Route whose backend carries a filter |
|---|---|---|
| 3.4 | Add and remove are reload-free — the backend is created and published, or drained and deleted, over the runtime API | Reloads on add and remove |
| 3.0-3.3 | Reloads on add and remove; the backend's servers are still added and removed at runtime | Reloads on add and remove |

A plain backend has no custom body directives. Shared settings live in a named
`defaults` profile; adding the first backend with a new profile requires a
reload, while later backends can reuse it.

Map-based header values, redirects, timeouts, and routing changes can run at
runtime when their processing rules already exist. Introducing a new rule,
header name, userlist, or raw configuration directive can require a reload.
An IPv6 allowlist or a realm that needs escaping keeps `satisfy: any` in the
backend and requires a reload. Server additions also depend on the balance
algorithm and server keywords.
See [Reload-free routing](../libraries/reload-free.md) for the complete
conditions.

Watch the fleet's reload rate to confirm route churn isn't reloading:

```promql
# Reloads across the fleet — flat while plain routes are added and removed on 3.4
rate(haptic_haproxy_reloads_total[5m])
```

The `tests/e2e` reload-free suites (`gateway_reloadfree_test.go`, `ingress_reloadfree_test.go`) assert a zero reload delta across route add/remove cycles on 3.4, and `TestScale` records `haproxy_reloads_total_delta` over its single-change churn as a trend. For latency at scale, run [`make bench-gateway-api`](#gateway-api-implementation-benchmark) — its `routechange` scenario measures availability while a route is repeatedly changed.

### Parallel Deployment

The controller deploys to multiple HAProxy pods in parallel. If deployment is slow:

1. Check apply timings in the agent logs and deployment summaries; `haptic_deploy_apply_total` counts applies, not latency
2. Verify network connectivity to HAProxy pods
3. Consider reducing config complexity

## Event processing

The controller's in-process event bus uses per-subscriber buffers sized at construction time (see `pkg/events/bus.go`); there is no CRD field to tune them. Monitor the event subsystem via the standard metrics:

```promql
# Per-event-type publish rate
rate(haptic_events_published_total[5m])

# Dropped events — subscriber channel was full (should be 0)
rate(haptic_events_dropped_total[5m])

# Critical drops — dropped event was flagged critical (should always be 0)
rate(haptic_events_dropped_critical_total[5m])

# Drops per subscriber — pinpoint which component can't keep up
rate(haptic_events_dropped_by_subscriber_total[5m])
```

A non-zero `haptic_events_dropped_total` rate means a critical subscriber was too slow to keep up. The controller restarts that iteration from authoritative state instead of continuing after lost coordination work. Use the per-subscriber metric to identify the component causing repeated restarts.

## Profiling

??? note "Go profiling with pprof"

    Access pprof endpoints for profiling:

    ```bash
    # CPU profile (30 seconds)
    curl http://localhost:8080/debug/pprof/profile?seconds=30 > cpu.pprof
    go tool pprof -http=:9999 cpu.pprof

    # Memory profile
    curl http://localhost:8080/debug/pprof/heap > heap.pprof
    go tool pprof -http=:9999 heap.pprof

    # Goroutine dump
    curl http://localhost:8080/debug/pprof/goroutine?debug=1
    ```

??? note "Finding what retains memory (`/debug/heapdump`)"

    `pprof` reports where memory was allocated, not what still holds it. When a
    heap profile shows a large block whose allocation site is not the problem —
    memory that survives a forced GC and never comes back — take a heap dump
    instead. It contains every object, the pointer edges between them, and the
    roots, so a reader can walk the retainer chain back to whatever is holding
    the memory.

    ```bash
    curl http://localhost:8080/debug/heapdump > heap.dump
    ```

    Read it with a heap-dump reader such as
    [heapspurs](https://github.com/adamroach/heapspurs): `--owners` prints the
    chain of objects keeping a given address alive, and `--anchors` names the
    root — a global, a stack frame, or a finalizer — that the chain ends at.

    The heap is collected before the dump is written. Without that the dump is
    dominated by unreachable objects, and an unreachable object has no retainer
    to report, so `--anchors` correctly returns nothing for most of it.

    Writing the dump **stops the world** for its duration — seconds on a
    multi-gigabyte heap — so treat it as a deliberate diagnostic on one replica,
    never as something to poll. While the world is stopped the controller answers
    neither health checks nor admission requests, and with `failurePolicy: Fail`
    the latter rejects writes to watched resources cluster-wide. A second request
    while one is running is refused with `409`.

    The endpoint answers on loopback only, like `/debug/pprof`, so reach it with
    `kubectl port-forward`.

    The dump is written to a temporary file first — `WriteHeapDump` forbids a pipe
    whose reader is in the same process — and is roughly heap-sized. That file
    lands in `$TMPDIR`, normally the container's writable layer, which counts
    against the pod's `ephemeral-storage` limit. The endpoint refuses with `507`
    rather than filling the filesystem when there is not enough room; set
    `HAPTIC_HEAPDUMP_DIR` to a mounted volume for heaps larger than that
    allowance.

    Both the estimate that drives that refusal and the completeness of the
    written file are checked, because the Go runtime ignores write errors while
    dumping — a filesystem that fills mid-dump would otherwise hand you a
    truncated object graph with a `200`. A short dump is reported as `507` too.

Controller images ship built with Profile-Guided Optimization (PGO), which typically yields 2-7% CPU improvement on hot paths — contributors updating the committed profile should see [Deployment — Build Optimizations](../development/design/deployment.md#build-optimizations-contributors).

### Common performance issues

**High memory usage:**

- Check for memory leaks: growing heap over time (`/debug/pprof/heap`)
- Switch large, infrequently accessed resources (for example, TLS Secrets) to `store: on-demand`
- Trim noisy fields with `watchedResourcesIgnoreFields`
- Narrow watch scope via `fieldSelector` or `labelSelector` (see [Resource Watching Optimization](#resource-watching-optimization))

**High CPU usage:**

- Profile to find hot spots (`/debug/pprof/profile?seconds=30`)
- Optimize template complexity — see [Template Optimization](#template-optimization)
- Raise `dataplane.minDeploymentInterval` (up to the agent's 60-second ceiling) to absorb more updates per push, and consider raising `spec.watchedResources.<name>.debounceInterval` for high-churn resources (for example, EndpointSlices on a large cluster) so each watcher batches more aggressively before triggering reconciliation

**Slow deployments:**

- Check the agent's health (`curl localhost:5555/v1/state` from inside the pod)
- Verify network latency to HAProxy pods
- Reduce config size by avoiding unnecessary nested loops in templates

## Performance checklist

### Initial Deployment

- [ ] Set appropriate resource requests/limits
- [ ] Tune `dataplane.minDeploymentInterval` for workload, plus `spec.watchedResources.<name>.debounceInterval` per resource if the `100ms` default is wrong for a specific kind (for example, slower on EndpointSlice on large clusters)
- [ ] Set HAProxy `maxconn` based on expected load
- [ ] Match `nbthread` to CPU allocation

### Ongoing optimization

- [ ] Monitor reconciliation latency
- [ ] Monitor deployment latency
- [ ] Watch for memory growth
- [ ] Track event subscriber count

### High-load environments

- [ ] Scale HAProxy pods horizontally
- [ ] Enable HA mode for controller
- [ ] Limit watched namespaces
- [ ] Use label selectors to filter resources
- [ ] Profile and optimize templates

## See also

- [Monitoring Guide](./monitoring.md) - Performance metrics and alerting
- [High Availability](./high-availability.md) - HA deployment patterns
- [Debugging Guide](./debugging.md) - Performance troubleshooting
