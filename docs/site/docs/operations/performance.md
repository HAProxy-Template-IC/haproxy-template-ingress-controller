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

Configure via Helm values. `controller.resources` applies to the controller pod; HAProxy and the Dataplane API sidecar have their own blocks under `haproxy.resources` and `haproxy.dataplane.resources` (see [HAProxy Deployment](../haproxy-deployment.md)):

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

### Debounce interval (per-resource override, `2s` default)

The resource watchers coalesce bursts of Kubernetes events via a leading-edge debouncer with a 2-second refractory period (`pkg/k8s/types.DefaultDebounceInterval`). The first change in a quiet period fires immediately, so isolated updates are fast; only subsequent changes arriving within 2 s are batched.

Each watched resource can override the window via `spec.watchedResources.<name>.debounceInterval`:

```yaml
watchedResources:
  httproutes:
    apiVersion: gateway.networking.k8s.io/v1
    resources: httproutes
    debounceInterval: "200ms"  # react fast on canary rollouts
  endpointslices:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    debounceInterval: "0"      # fire immediately — pod-IP rotations reach HAProxy instantly (chart default)
```

Empty / invalid strings fall back to the `2s` default silently; `"0"` disables debouncing so every change fires immediately. This is the only debounce layer — the Reconciler fires immediately on every event with no separate refractory window, and reload throttling lives in the deployer (see [Deployment Pacing](#deployment-pacing) below and [architecture-overview](../development/design/architecture-overview.md)).

### Deployment pacing

CRD fields on `spec.dataplane` bound how often the controller pushes configuration to HAProxy and how each push behaves:

| Field | Default | Purpose |
|-------|---------|---------|
| `dataplane.minDeploymentInterval` | `2s` (Helm chart ships `5s`) | Minimum time between consecutive deployments; rate-limits rapid-fire pushes |
| `dataplane.driftPreventionInterval` | `60s` | Forces a deployment if none has happened within this window; corrects external drift |
| `dataplane.configPublishInterval` | `10s` | Throttle for republishing the rendered config as the `HAProxyCfg` observability CRD; not on the deployment hot path |
| `dataplane.reloadVerificationTimeout` | `10s` | Maximum time the sync waits for HAProxy to confirm a graceful reload completed |
| `dataplane.syncTimeout` | 2m | Overall per-endpoint sync timeout (parse + diff + apply + reload-verify) |

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

Resource deletions take the same path as any structural change: the watch delete fires, the leading-edge debouncer forwards it (the first change in a quiet window fires immediately, so an isolated delete isn't held for the 2 s window), the reconciler re-renders without the resource, and the deployer pushes a reload paced by `minDeploymentInterval`. Unlike EndpointSlice pod-IP rotations, a deletion isn't runtime-fast-path eligible — that path covers only server weight, address, port, and admin-state updates — so it always reloads. An isolated deletion typically converges in about one to a few seconds.

**Tuning guidelines:**

- Raise `minDeploymentInterval` in very high-churn environments to absorb more updates per push (trades latency for fewer Dataplane API calls).
- Keep `driftPreventionInterval` at or below 2 minutes so that a misbehaving external client can't hold HAProxy in a drifted state for long.
- Raise `reloadVerificationTimeout` if your Dataplane API has a high `reload-delay` setting; the verification timeout must exceed it.

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
./bin/haptic-controller validate -f config.yaml --trace-templates

# Full call tree including nested render/render_glob
./bin/haptic-controller validate -f config.yaml --trace-templates --profile-includes

# Combine with --verbose and --dump-rendered for end-to-end diagnosis
./bin/haptic-controller validate -f config.yaml --verbose --dump-rendered --trace-templates
```

### Measuring render time (`benchmark`)

`--trace-templates` tells you where a single render spends its time. The `benchmark` subcommand tells you whether a change made rendering faster or slower, by rendering the same validation test repeatedly and timing each pass. It separates template *compilation* from *rendering*, so a cold first render doesn't hide a warm-path regression:

```bash
# Every validation test in the config, 2 iterations each (the default)
./bin/haptic-controller benchmark -f config.yaml

# One test, more iterations for a tighter median
./bin/haptic-controller benchmark -f config.yaml --test benchmark-ingress-100 --iterations 10

# Rank the 20 slowest template includes
./bin/haptic-controller benchmark -f config.yaml --profile-includes
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

### Parallel Deployment

The controller deploys to multiple HAProxy pods in parallel. If deployment is slow:

1. Check DataPlane API responsiveness
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
- Raise `dataplane.minDeploymentInterval` to absorb more updates per push, and consider raising `spec.watchedResources.<name>.debounceInterval` for high-churn resources (for example, EndpointSlices on a large cluster) so each watcher batches more aggressively before triggering reconciliation

**Slow deployments:**

- Check Dataplane API health (`/v3/info` from inside the pod)
- Verify network latency to HAProxy pods
- Reduce config size by avoiding unnecessary nested loops in templates

## Performance checklist

### Initial Deployment

- [ ] Set appropriate resource requests/limits
- [ ] Tune `dataplane.minDeploymentInterval` for workload, plus `spec.watchedResources.<name>.debounceInterval` per resource if the `2s` default is wrong for a specific kind (for example, slower on EndpointSlice on large clusters)
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
