# Performance

## Overview

Tune HAPTIC in three areas:

- **Controller performance** - Template rendering, reconciliation cycles
- **HAProxy performance** - Load balancer throughput and latency
- **Kubernetes integration** - Resource watching and event handling

## Measured render cost by object count

Every render walks the whole watched-object store, so render time scales with cluster size. These numbers come from `scripts/test-benchmark.sh` against the bundled chart's default libraries, with a realistic mix of one Ingress, one Service, and two EndpointSlices per step:

| Ingresses | Total render | Per Ingress | `haproxy.cfg` | Path maps |
|---|---|---|---|---|
| 100 | 15 ms | 0.15 ms | 10 ms | 2.6 ms |
| 1,000 | 113 ms | 0.11 ms | 77 ms | 20 ms |
| 5,000 | 742 ms | 0.15 ms | 386 ms | 302 ms |

Reproduce them with:

```bash
./scripts/test-benchmark.sh --ingress-only --steps 100,1000,5000 --iterations 3
```

Two things to read off this table. The overall cost is close to linear at roughly 0.11–0.15 ms per Ingress, so a 5,000-Ingress cluster spends under a second per render. But the **path maps grow faster than the object count** — `path-prefix-exact` alone goes from 6 ms at N=1,000 to 94 ms at N=5,000, a 15× rise for 5× the objects — and by N=5,000 the three path maps are about 40% of the render.

The admission webhook renders the entire configuration once per admitted object, so this is also the per-admission cost. On a cluster where a single render approaches `controller.webhook.timeoutSeconds` (10 seconds by default), a burst of `kubectl apply`s starts failing admission under `failurePolicy: Fail`. Measure your own cluster with the command above before assuming headroom.

## Gateway API implementation benchmark

`make bench-gateway-api` runs the exact probe, route-change, and route-scale programs used by the [`gateway-api-bench` Part 2 report](https://github.com/howardjohn/gateway-api-bench/blob/e81292ed876472804e0a2245876a7c445ab80881/README-v2.md). The runner checks out commit `e81292ed876472804e0a2245876a7c445ab80881` by default, verifies it, and builds the upstream Go programs in a temporary directory. For that snapshot, it also verifies and installs the Gateway API `v1.4.0` experimental bundle by its exact manifest digest.

Pinned source doesn't make the resulting numbers interchangeable with the public report. The report used joined, multi-controller runs with shared status-write contention and imported results through VictoriaMetrics. This runner uses one HAPTIC target in a dedicated Kind cluster, executes each self-paced upstream program from a static sibling container, and analyzes its raw logs directly. Use the result to find material gaps; don't present it as a reproduced public score.

The supported scenarios retain the upstream workload behavior and add HAPTIC-specific evidence:

- `probe` applies 3,000 HTTPRoutes sequentially and waits for each route's first HTTP `200` before applying the next one. Before the program starts, the runner pre-creates its backend Deployment and Service from the exact manifest in the pinned source and waits for a ready endpoint, so the first sample, route `0`, measures route propagation rather than the backend's image pull and pod start-up; the program's own apply of the same manifest is a no-op. (The published joined runs shared one long-lived backend across tests.) The raw-log analyzer requires one unique latency sample for every Gateway and route ID from `0` through `2999`. Unexpected responses, including HTTP `5xx`, remain attached diagnostics: the upstream program continues until `200`, while HAPTIC's separate scenario-quality verdict fails if any unexpected status occurred. Complete evidence with a `5xx` is a valid negative measurement, not a broken run.
- `routechange` uses the upstream availability test: it sends continuous traffic while applying 20 route changes 200 ms apart. An upstream request failure during active churn produces a negative availability result. HAPTIC adds a separate non-vacuity gate that must observe `my-added-header: added-value` through every Gateway and every HAProxy pod while the matching route variant is live. The pinned workload places this response modifier on a `backendRef`. HAPTIC now implements backendRef-level response modifiers as a map-driven, per-backend line, so the marker is emitted for the exact workload; flipping the `routechange` result from product-negative to a pass is pending a bench run that confirms the marker reaches every Gateway and every HAProxy pod. It isn't weakened to a generic HTTP response check. After cleanup, the same pods and Services must return `404` without the marker, and the `HAProxyCfg` and map checksums must return to their baseline. Reload counts remain diagnostic only.
- `scale` captures the Part 2 route-scale configuration and substitutes only the HAPTIC Gateway list. The defaults create 50 namespaces with 100 applications each, one Pod, Service, and HTTPRoute per application, plus 20 simulated nodes, a 500 ms grace period, 1 s configuration jitter, and 2 s workload jitter. The upstream sync marker and 5,000-object count aren't the readiness verdict. Every HTTPRoute must have HAPTIC-owned `Accepted=True` and `ResolvedRefs=True` status at or beyond the synchronized snapshot generation; a new `HAProxyCfg` and all referenced maps must be deployed to the exact HAProxy fleet; and two reads of the current HAProxy worker in every load-balancer pod must show runtime `host.map` entries that exactly match the snapshot's route hostnames and Gateway target ports. The runner re-resolves the current map checksum, path, and fleet-runtime token after those reads and retries if that semantic token moved; unrelated config generations may advance while churn remains active.

Only after the scale readiness proof does the runner start its HAPTIC-defined 10-minute analysis window. If valid observations reach the scale-startup deadline at the route-status, exact-current configuration, referenced-map, runtime-map, or semantic-token gate, the runner records a stage-specific negative result, stops the workload, and performs full cleanup without reporting steady CPU or memory. Evidence acquisition failures and cleanup deadlines still invalidate the run. At both window boundaries, it captures the exact 5,000-route UID, generation, and `PathPrefix` path inventory, plus the upstream log line boundary. The same UIDs must remain, at least one route's generation and path must advance, and the bounded log segment must contain at least one `refreshed config HTTPRoute/...` line. Controller identities must remain unchanged, all captured counters must be monotonic per pod, reconciliation must advance, and at least one deployment, apply, or HAProxy reload counter must advance. Together, these checks prove that route mutation and upstream refresh output occurred within the captured interval that brackets HAPTIC activity and resource sampling; they don't prove per-mutation causality. `scale/steady-activity.json` records this under `route_refresh_activity` with `temporal_overlap: true` and `causal_mapping: false`. Deltas for the reconciliation errors, deployment errors, validation errors, fast-path failures, deployment/runtime divergence, runtime-map divergence, and dropped events must remain zero for the outcome-quality verdict. The same window supplies strict CPU and memory samples. Cleanup must restore the route, namespace, node, configuration, and map baselines.

The probe and route-change programs' upstream backend uses an untagged, mutable image reference; the runner pre-creates it from the program's own manifest and records the applied manifest's digest and the ready pod. Before deleting it, the runner records the Deployment, owning ReplicaSet, and exact ready pod identity, including the declared image, runtime image, immutable image digest, container ID, and restart count. Raw workload inventories prove that no matching ReplicaSet or pod existed before the scenario and none remained after cleanup.

Before every workload, the runner derives the supervised `spoa-hub` and `vector` child topology from the live load-balancer Deployment and Pods. It requires a healthy, unique child with the expected executable and executable-file identity, boot ID, process identifier, parent process identifier, and `/proc` start time. After cleanup, it repeats the capture and listener health checks. Missing or malformed baseline evidence invalidates the run; a stable Kubernetes container whose child restarted, disappeared, became ambiguous, or ended unhealthy remains a complete measurement but makes HAPTIC's scenario-quality verdict negative. Each scenario retains timestamped logs from every load-balancer container.

These are the Part 2 control-plane scenarios that this target covers. It doesn't run the attached-routes, traffic, ListenerSet, or backend-failover tests, or the 100-route propagation variant concurrently with route scale. Don't describe its output as a result for those tests or for the entire upstream suite.

Read `runner-summary.json` for the result. Its `measured_result.pass` and `negative_scenarios` fields report HAPTIC quality gaps independently from `harness.pass`. The runner finalizes `harness.pass` and `harness.final_exit_code` only after its terminal cleanup and artifact-security gates. When the evidence and cleanup complete, a probe with `5xx` diagnostics, route-change downtime, or adverse scale outcome counters keeps the runner exit code at `0` and remains a negative measured result. A nonzero exit means the workload, provenance, identities, cleanup, or evidence were incomplete or invalid; it doesn't mean HAPTIC was merely slower than another controller.

The runner also installs the Prometheus manifest from the same upstream commit. For each metric, it retains a 5-second `query_range` value matrix and a companion `timestamp(selector)` matrix. The analyzer requires identical labels and evaluation grids, advancing source timestamps within the workload window, and a maximum source age of 20 seconds (four scrape intervals, accommodating exporter timestamps from the node metrics exporter). This prevents repeated stale values from looking like fresh samples.

The `upstream_compatible_pod_cgroups` summary uses the pod-root CPU and working-set series selected by the upstream dashboard. It also requires empty image and name labels so current kubelet metrics don't include the pause sandbox as a second pod series. The separate `haptic_container_diagnostics` summary uses exact real-container CPU, working-set, and resident-set-size series for every controller and load-balancer container. Both report time-aligned mean, p95, maximum, and last values. CPU also reports the counter delta, sampled window, and normalized cores. Missing, duplicate, restarted, replaced, stale, or timestamp-misaligned series fail analysis.

The `probe` and `routechange` resource windows cover only the upstream process. After discarding leading evaluations whose underlying samples predate that process, every series needs at least two retained evaluations and two distinct source timestamps. Genuine sample insufficiency produces a non-gating `resources.json` with `analysis_status: not_gated`, `gating: false`, and `pass: null`; malformed or stale samples still fail. The `scale` resource summary is strict when the proven steady-churn window starts; a readiness deadline makes that summary not applicable.

The fresh-cluster path resets the release to chart defaults before applying only the local runtime identity, benchmark timings, load-balancer exposure, and no-limit measurement settings. Because the pinned CRD bundle is Experimental, it also enables `controller.templateLibraries.gateway.experimentalChannel` and verifies that HAPTIC retained the experimental-field validation tests. The runner removes CPU and memory limits and explicit `GOMEMLIMIT` from measured HAPTIC pods so those controls don't cap the observation. Redacted Helm values and manifests, effective timings, and resource methodology record the resulting profile. The result directory also includes upstream and HAPTIC commits, the parameterized scale configuration and its upstream diff, image, binary, pod, container, and supervised-child identities, process-boundary timestamps, raw workload and load-balancer logs, scenario analyses, Prometheus responses, and the terminal runner exit code and timestamp.

Before it appends fixed terminal metadata and accepts a result, the runner scans the workload artifact tree for exact byte sequences matching the raw and base64 forms of sensitive live Kubernetes Secret values that are at least eight bytes long. It excludes only the `path` metadata key in controller-owned HAPTIC SSL auxiliary Secrets because that value names the deployed certificate file and appears in the rendered configuration; the `certificate` value in the same Secret remains covered. A selected match is replaced with `<redacted>`, recorded in `cluster/artifact-secret-scan.json`, and fails the run. If the runner can't establish a complete live Secret inventory, complete the scan, or verify the redaction, it replaces the artifact tree with `artifact-security-invalid.json`, `runner-summary.json`, and runner exit and timestamp files. Structured redaction also removes agent passwords, Secret data, and webhook CA bundles from captured Helm data.

The manual `gateway-api-benchmark-smoke` GitLab job is a hosted-runner integration smoke, not the controlled default profile. It pins the published upstream commit, Gateway API v1.4.0 Experimental bundle, HAPTIC Gateway, HAProxy 3.4, 50-by-100 scale workload, and 20-by-200 ms route-change workload. It reduces the probe to 300 routes with a 45-minute timeout and sets the deployment and watcher intervals to 100 ms. The scale-startup, scale-window, and route-change bounds are 20, 10, and 10 minutes. A 135-minute outer watchdog, followed by at most 20 minutes of forced-termination grace, leaves 10 minutes for safe artifact staging before the job's 2-hour-45-minute limit. These bounds fit below GitLab.com's three-hour limit; the full 3,000-route product-default run remains local. Its metadata must report `published_workload_inputs_match: false` and `controlled_default_profile: false`, so don't use the CI smoke for Part 2 gap numbers. The job writes raw results outside `CI_PROJECT_DIR`. It stages a full result tree only when the terminal summary has `secret_inventory_trusted: true` and the final artifact scan report passed, or stages the runner's five-file invalid verdict after validating its exact names and contents. Every other shape produces only `ci-wrapper-invalid.json`. The job preserves a nonzero runner exit code; a staging failure after a zero runner exit changes the job exit to `1`.

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
| Small (<50 Ingresses) | 50m | 200m | 64Mi | 256Mi |
| Medium (50-200 Ingresses) | 100m | 500m | 128Mi | 512Mi |
| Large (200+ Ingresses) | 200m | 1000m | 256Mi | 1Gi |
| Very large (thousands of Ingresses) | 500m | 2000m | 512Mi | 2Gi |

These recommendations are based on the controller's primary memory consumers (watched resource caches, template rendering buffers, event history) and CPU consumers (template rendering, API server watch streams). Adjust based on your actual resource counts and template complexity.

!!! tip "Scaling past a few thousand Ingresses"
    At the very-large scale, the resource numbers above are a starting point, not the main lever — the controller holds every watched resource in memory and re-renders the whole config on change, so what keeps that bounded is *watching less*, not sizing bigger. Reach for these first:

    - **Narrow the watch** to the namespaces or labels that actually route through HAPTIC, so unrelated Ingresses, Services, and EndpointSlices never enter the cache — see [Resource watching optimization](#resource-watching-optimization).
    - **Move large, infrequently read resources to the on-demand store** (TLS Secrets especially) so their bodies aren't held resident — see [Watching resources — store types](../watching-resources.md). Both cut memory and per-render CPU more than raising limits does.

    HAProxy-side, watch `haproxy.shmStats.maxObjects` if you enabled shm-stats — thousands of backends and servers can exhaust the fixed-size stats file (see [Troubleshooting — Shared Memory Stats Limit](../troubleshooting.md#shared-memory-stats-limit)).

!!! note "Chart defaults differ — deliberately"
    The Helm chart ships with `cpu request 100m`, **no CPU limit**, and `memory request = limit = 512Mi` (Burstable QoS — no CPU limit, by design), which differs from the table above for two reasons: omitting the CPU limit avoids GOMAXPROCS-aware Go workloads being throttled when bursts exceed the limit, and matching memory request to limit prevents the kernel's out-of-memory killer from preferring this pod over Burstable neighbours (see [Robusta on Kubernetes memory limits](https://home.robusta.dev/blog/kubernetes-memory-limit) for the rationale). The CPU-limit values in the table above are the *upper bound* you'd need if you choose to set one; you can equally well leave it unset and rely on requests + node capacity.

Configure via Helm values. `controller.resources` applies to the controller pod; HAProxy and the agent have their own blocks under `haproxy.resources` and `haproxy.agent.resources` (see [HAProxy Deployment](../haproxy-deployment.md)):

```yaml
# values.yaml
controller:
  resources:
    requests:
      cpu: 100m
      memory: 512Mi
    limits:
      # No CPU limit — avoids throttling GOMAXPROCS-aware Go under bursts.
      memory: 512Mi   # memory request == limit; no CPU limit → Burstable QoS (by design)
```

### Container awareness (`GOMAXPROCS` and `GOMEMLIMIT`)

The controller automatically detects and respects the limits you set above — no tuning env vars are needed:

- **CPU limits (GOMAXPROCS):** native cgroup-aware GOMAXPROCS (added upstream in Go 1.25; the controller currently builds with Go 1.26). The Go runtime detects cgroup CPU limits (v1 and v2), sets GOMAXPROCS to match the container's CPU limit rather than the host's core count, and adjusts dynamically if the limit changes at runtime. Proper GOMAXPROCS prevents over-scheduling goroutines and the CPU throttling that comes with it.
- **Memory limits (GOMEMLIMIT):** the controller uses the `automemlimit` library to set GOMEMLIMIT to 90% of the container memory limit (10% headroom for non-heap memory), with both cgroups v1 and v2. GOMEMLIMIT helps the Go GC keep heap memory under control and prevents out-of-memory kills.

At startup the controller logs the detected limits, for example:

```
INFO HAPTIC starting ... gomaxprocs=8 gomemlimit="483183820 bytes (460.80 MiB)"
```

`gomemlimit` is 90% of the 512Mi memory limit (≈460.8 MiB). Because the chart omits a CPU limit, `gomaxprocs` matches the node's core count (8 here) rather than a container CPU limit — you only see `gomaxprocs=1` when you set a 1-CPU limit.

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
container_memory_working_set_bytes{container="haptic"}
```

### CPU considerations

CPU spikes occur during:

- Template rendering (complex templates with many resources)
- Initial resource synchronization (startup)
- Burst of resource changes (rolling updates)

Monitor CPU usage:

```promql
rate(container_cpu_usage_seconds_total{container="haptic"}[5m])
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
    debounceInterval: "0"      # fire immediately — pod-IP rotations reach HAProxy instantly (chart default)
```

Empty / invalid strings fall back to the `100ms` default silently; `"0"` disables debouncing so every change fires immediately. This is the only debounce layer — the Reconciler fires immediately on every event with no separate refractory window, and reload throttling lives in the deployer (see [Deployment Pacing](#deployment-pacing) below and [architecture-overview](../development/design/architecture-overview.md)).

### Deployment pacing

CRD fields on `spec.dataplane` bound how often each pod reloads and how long the controller waits for it:

| Field | Default | Purpose |
|-------|---------|---------|
| `dataplane.minDeploymentInterval` | `2s` (Helm chart ships `5s`) | Shortest interval between two reloads of one pod. A reload inside the window is scheduled, never dropped |
| `dataplane.driftPreventionInterval` | `60s` | How often each pod re-hashes its tree and the controller re-applies on a disagreement; corrects external drift |
| `dataplane.configPublishInterval` | `10s` | Throttle for republishing the rendered config as the `HAProxyCfg` observability CRD; not on the deployment hot path |
| `dataplane.reloadVerificationTimeout` | `60s` (the agent's ceiling) | How long the agent waits for HAProxy to confirm a graceful reload before restoring the last known good file set |
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

A resource deletion follows the same front end as any change: the watch delete fires, the leading-edge debouncer forwards it (the first change in a quiet window fires immediately, so an isolated delete isn't held for the refractory window), and the reconciler re-renders without the resource. Whether that re-render reloads HAProxy depends on the route, exactly as adding one does — see [Reload-free route changes](#reload-free-route-changes) below. On HAProxy 3.4, deleting a plain route drains and removes its backend over the runtime API with no reload; a route whose backend carries a filter, and any route on HAProxy 3.0-3.3, reloads paced by `minDeploymentInterval`. An isolated deletion converges in well under a second when it's reload-free, or in about one to a few seconds when it reloads.

**Tuning guidelines:**

- Raise `minDeploymentInterval` in very high-churn environments to absorb more updates per reload (trades latency for fewer reloads), up to the agent's 60-second ceiling. It doesn't pace reload-free applies, which never fork the process.
- Keep `driftPreventionInterval` at or below 2 minutes so that a misbehaving external client can't hold HAProxy in a drifted state for long.
- Lower `reloadVerificationTimeout` to fail a stuck reload sooner and restore the last known good file set earlier. You can't raise it: the default is already the agent's 60-second ceiling, and the agent exits at startup on a larger value.

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
  the CPU quota so threads aren't throttled. In the cloud, where the node is
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

!!! warning "Read this if your templates use password hashes"
    Password hash validation during configuration parsing can dominate reconciliation time. Review the table below before choosing a hash algorithm.

HAProxy validates password hash formats during configuration parsing by running the full hashing algorithm. This can significantly slow down config validation when using expensive hash algorithms.

**Hash algorithm validation times:**

| Algorithm | Example | Time per hash |
|-----------|---------|---------------|
| MD5 | `$1$salt$hash` | ~0.004 ms |
| SHA-256 | `$5$salt$hash` | ~3 ms |
| SHA-512 | `$6$salt$hash` | ~3 ms |
| bcrypt (cost 10) | `$2y$10$salt$hash` | **~85 ms** |

!!! warning "bcrypt with high cost factors is expensive"
    A configuration with 200 bcrypt passwords at cost factor 10 adds **~17 seconds** to every config validation. This directly impacts reconciliation time and webhook validation latency.

**Recommendations:**

- **Prefer SHA-512 (`$6$`)** for password hashes - cryptographically strong with fast validation
- **Avoid bcrypt cost factors above 8** in high-frequency validation scenarios
- **Consolidate userlists** to avoid duplicate password entries - HAProxy validates each occurrence separately, even for identical hashes
- **Consider external authentication** (OAuth, OpenID Connect) for large user bases instead of embedding passwords in config

**Checking your config:**

```bash
# Count expensive bcrypt hashes
grep -c '\$2[aby]\$' /path/to/haproxy.cfg

# Estimate validation overhead (bcrypt count × 85ms)
```

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

A backend is *plain* when its section is only `from`/`guid`/`server` lines. A `filter` keeps it structural — most commonly **response compression, which is on by default for Ingress** (`haproxy-haptic.org/compress-enable`), and also a stick-table rate limit or a raw operator injection. Set `compress-enable: "false"` on a route to make its add and remove reload-free on 3.4. Server (pod) churn within an existing backend is always reload-free on every supported version.

Watch the fleet's reload rate to confirm route churn isn't reloading:

```promql
# Reloads across the fleet — flat while plain routes are added and removed on 3.4
rate(haptic_haproxy_reloads_total[5m])
```

The `tests/e2e` reload-free suites (`gateway_reloadfree_test.go`, `ingress_reloadfree_test.go`) assert a zero reload delta across route add/remove cycles on 3.4, and `TestScale` records `haproxy_reloads_total_delta` over its single-change churn as a trend. For latency at scale, run [`make bench-gateway-api`](#gateway-api-implementation-benchmark) — its `routechange` scenario measures availability while a route is repeatedly changed.

### Parallel Deployment

The controller deploys to multiple HAProxy pods in parallel. If deployment is slow:

1. Check the agent's apply latency (`haptic_deploy_apply_total`, the pod's logs at `debug`)
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
