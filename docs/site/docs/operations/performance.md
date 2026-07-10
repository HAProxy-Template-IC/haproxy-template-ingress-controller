# Performance Guide

This guide covers performance tuning and optimization for the HAProxy Template Ingress Controller.

## Overview

Performance optimization involves three areas:

- **Controller performance** - Template rendering, reconciliation cycles
- **HAProxy performance** - Load balancer throughput and latency
- **Kubernetes integration** - Resource watching and event handling

## Controller Resource Sizing

### Recommended Resources

| Deployment Size | CPU Request | CPU Limit | Memory Request | Memory Limit |
|-----------------|-------------|-----------|----------------|--------------|
| Small (<50 Ingresses) | 50m | 200m | 64Mi | 256Mi |
| Medium (50-200 Ingresses) | 100m | 500m | 128Mi | 512Mi |
| Large (200+ Ingresses) | 200m | 1000m | 256Mi | 1Gi |

These recommendations are based on the controller's primary memory consumers (watched resource caches, template rendering buffers, event history) and CPU consumers (template rendering, API server watch streams). Adjust based on your actual resource counts and template complexity.

!!! note "Chart defaults differ — deliberately"
    The Helm chart ships with `cpu request 100m`, **no CPU limit**, and `memory request = limit = 512Mi` (Guaranteed QoS), which differs from the table above for two reasons spelled out in the [Helm chart's HAProxy Deployment guide](../haproxy-deployment.md#resource-limits-and-container-awareness): omitting the CPU limit avoids GOMAXPROCS-aware Go workloads being throttled when bursts exceed the limit, and matching memory request to limit prevents the kernel OOM killer from preferring this pod over Burstable neighbours. The CPU-limit values in the table above are the *upper bound* you'd need if you choose to set one; you can equally well leave it unset and rely on requests + node capacity.

Configure via Helm values:

```yaml
# values.yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### Memory Considerations

Memory usage scales with:

- Number of watched resources (Ingresses, Services, Endpoints)
- Size of template library
- Event buffer size (default 1000 events)
- Number of HAProxy pods being managed

Monitor memory usage:

```promql
container_memory_working_set_bytes{container="haptic"}
```

### CPU Considerations

CPU spikes occur during:

- Template rendering (complex templates with many resources)
- Initial resource synchronization (startup)
- Burst of resource changes (rolling updates)

Monitor CPU usage:

```promql
rate(container_cpu_usage_seconds_total{container="haptic"}[5m])
```

## Reconciliation Tuning

### Debounce Interval (per-resource override, 2s default)

The resource watchers coalesce bursts of Kubernetes events via a leading-edge debouncer with a 2-second refractory period (`pkg/k8s/types.DefaultDebounceInterval`). The first change in a quiet period fires immediately, so isolated updates are fast; only subsequent changes arriving within 2 s are batched. This is the only debounce layer — the Reconciler fires immediately on every event, and reload throttling lives in the deployer's `minDeploymentInterval`.

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

Empty / invalid strings fall back to the 2s default silently; `"0"` disables debouncing so every change fires immediately. This is the only debounce layer — the Reconciler fires immediately on every event with no separate refractory window, and reload throttling lives in the deployer (see [Deployment Pacing](#deployment-pacing) below and [architecture-overview](../development/design/architecture-overview.md)).

If you need to change the global default in a custom build, edit `DefaultDebounceInterval` in `pkg/k8s/types/types.go`.

### Deployment Pacing

CRD fields on `spec.dataplane` bound how often the controller pushes configuration to HAProxy and how each push behaves:

| Field | Default | Purpose |
|-------|---------|---------|
| `dataplane.minDeploymentInterval` | 2s (Helm chart ships `5s`) | Minimum time between consecutive deployments; rate-limits rapid-fire pushes |
| `dataplane.driftPreventionInterval` | 60s | Forces a deployment if none has happened within this window; corrects external drift |
| `dataplane.configPublishInterval` | 10s | Throttle for republishing the rendered config as the `HAProxyCfg` observability CRD; not on the deployment hot path |
| `dataplane.reloadVerificationTimeout` | 10s | Maximum time the sync waits for HAProxy to confirm a graceful reload completed |
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

**Tuning guidelines:**

- Raise `minDeploymentInterval` in very high-churn environments to absorb more updates per push (trades latency for fewer Dataplane API calls).
- Keep `driftPreventionInterval` at or below 2 minutes so that a misbehaving external client cannot hold HAProxy in a drifted state for long.
- Raise `reloadVerificationTimeout` if your Dataplane API has a high `reload-delay` setting; the verification timeout must exceed it.

### Reconciliation Metrics

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

- Average reconciliation: <500ms
- P95 reconciliation: <2s
- Error rate: <1%

## Template Optimization

### Efficient Template Patterns

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
    {%- if ingress.spec.backend.service.name == service.metadata.name %}
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
  {%- var service = service_map[ingress.spec.backend.service.name] %}
  ...
{%- end %}
```

### Template Debugging

Profile template rendering with the `validate` subcommand's tracing flags (output goes to stderr):

```bash
# Top-level render order with per-template timing
./bin/haptic-controller validate -f config.yaml --trace-templates

# Full call tree including nested render/render_glob
./bin/haptic-controller validate -f config.yaml --trace-templates --profile-includes

# Combine with --verbose and --dump-rendered for end-to-end diagnosis
./bin/haptic-controller validate -f config.yaml --verbose --dump-rendered --trace-templates
```

## HAProxy Optimization

### Configuration Parameters

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
    tune.ssl.default-dh-param 2048

defaults
    timeout connect 5s
    timeout client 50s
    timeout server 50s
    timeout http-request 10s
    timeout queue 60s
```

### Connection Limits

Calculate maxconn based on expected load:

```
maxconn = (expected_concurrent_connections * safety_factor) / num_haproxy_pods
```

Example:

- Expected: 10,000 concurrent connections
- Safety factor: 1.5
- HAProxy pods: 3
- maxconn = (10,000 * 1.5) / 3 = 5,000

### Thread Configuration

Match `nbthread` to available CPU cores:

```yaml
# HAProxy pod resources
resources:
  requests:
    cpu: 2
  limits:
    cpu: 4

# HAProxy config
global
    nbthread 4  # Match CPU limit
```

### Buffer Sizing

Increase buffers for large headers or payloads:

```go
global
    tune.bufsize 32768        # 32KB for large headers
    tune.http.maxhdr 128      # Allow more headers
```

### Password Hash Performance

!!! warning "Read this if your templates use password hashes"
    Password hash validation during configuration parsing can dominate reconciliation time. Review the table below before choosing a hash algorithm.

HAProxy validates password hash formats during configuration parsing by running the full hashing algorithm. This can significantly slow down config validation when using expensive hash algorithms.

**Hash algorithm validation times:**

| Algorithm | Example | Time per hash |
|-----------|---------|---------------|
| MD5 | `$1$salt$hash` | ~0.004ms |
| SHA-256 | `$5$salt$hash` | ~3ms |
| SHA-512 | `$6$salt$hash` | ~3ms |
| bcrypt (cost 10) | `$2y$10$salt$hash` | **~85ms** |

!!! warning "bcrypt with high cost factors is expensive"
    A configuration with 200 bcrypt passwords at cost factor 10 adds **~17 seconds** to every config validation. This directly impacts reconciliation time and webhook validation latency.

**Recommendations:**

- **Prefer SHA-512 (`$6$`)** for password hashes - cryptographically strong with fast validation
- **Avoid bcrypt cost factors above 8** in high-frequency validation scenarios
- **Consolidate userlists** to avoid duplicate password entries - HAProxy validates each occurrence separately, even for identical hashes
- **Consider external authentication** (OAuth, OIDC) for large user bases instead of embedding passwords in config

**Checking your config:**

```bash
# Count expensive bcrypt hashes
grep -c '\$2[aby]\$' /path/to/haproxy.cfg

# Estimate validation overhead (bcrypt count × 85ms)
```

## Scaling Strategies

### Horizontal Scaling

Scale HAProxy pods for increased traffic:

```bash
kubectl scale deployment haptic-haproxy --replicas=5 -n haptic
```

The controller automatically discovers new pods and deploys configuration.

### Controller Scaling (HA Mode)

For high availability, run multiple controller replicas:

```yaml
# values.yaml
replicaCount: 3

controller:
  config:
    controller:
      leaderElection:
        enabled: true
```

Only the leader performs deployments; followers maintain hot-standby status.

### Resource Watching Optimization

Reduce watched resources to minimize controller load:

```yaml
# Watch a single namespace
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      namespace: production

# Or narrow by label selector on the resources themselves
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      labelSelector: "managed-by=haptic"
```

`labelSelector` is a comma-separated equality-only string (`k=v[,k=v]`) — the `matchLabels`/`matchExpressions` object form and set-based syntax (`in`, `notin`, `!`) are not supported. For label-based namespace filtering, fall back to per-namespace `Role`/`RoleBinding`s and watch each namespace explicitly, or filter inside the template against a watched `namespaces` resource.

## Deployment Performance

### Deployment Latency

Monitor deployment time:

```promql
# Average deployment duration
rate(haptic_deployment_duration_seconds_sum[5m]) /
rate(haptic_deployment_duration_seconds_count[5m])

# P95 deployment latency
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))
```

**Target metrics:**

- Average deployment: <1s per HAProxy pod
- P95 deployment: <3s

### Parallel Deployment

The controller deploys to multiple HAProxy pods in parallel. If deployment is slow:

1. Check DataPlane API responsiveness
2. Verify network connectivity to HAProxy pods
3. Consider reducing config complexity

### Drift Prevention

See [Reconciliation Tuning → Deployment Pacing](#deployment-pacing) above. Configure via `spec.dataplane.driftPreventionInterval` (default 60s).

## Event Processing

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

A sustained non-zero `haptic_events_dropped_total` rate means a subscriber is too slow to keep up with its event stream; look at the component owning that subscriber rather than trying to raise a buffer.

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

??? note "Profile-Guided Optimization (PGO)"

    The controller is built with Go's Profile-Guided Optimization (PGO) for improved performance. PGO typically provides 2-7% CPU improvement by optimizing frequently-called functions.

    **How it works:**

    A baseline CPU profile (`cmd/controller/default.pgo`) is committed to the repository. Go automatically uses this profile during builds to optimize hot paths.

    **Updating the profile:**

    To collect a fresh profile from the development environment:

    1. Start the dev environment:

        ```bash
        ./scripts/start-dev-env.sh
        ```

    2. Port-forward to the controller's debug port:

        ```bash
        kubectl -n haptic port-forward deploy/haptic-controller 8080:8080
        ```

    3. Generate workload (trigger reconciliation by modifying resources)

    4. Collect a 30-second CPU profile:

        ```bash
        make pgo-profile
        # Or manually:
        curl -o cmd/controller/default.pgo http://localhost:8080/debug/pprof/profile?seconds=30
        ```

    5. Rebuild with the new profile:

        ```bash
        make build
        ```

    **Production profiles:**

    For optimal results, collect profiles from production during representative workloads. Merge multiple profiles for broader coverage:

    ```bash
    make pgo-merge PROFILES='profile1.pgo profile2.pgo'
    ```

### Common Performance Issues

**High memory usage:**

- Check for memory leaks: growing heap over time (`/debug/pprof/heap`)
- Switch large, infrequently-accessed resources (e.g. TLS Secrets) to `store: on-demand`
- Trim noisy fields with `watchedResourcesIgnoreFields`
- Narrow watch scope via `namespace` or `labelSelector`

**High CPU usage:**

- Profile to find hot spots (`/debug/pprof/profile?seconds=30`)
- Optimize template complexity — see [Template Optimization](#template-optimization)
- Raise `dataplane.minDeploymentInterval` to absorb more updates per push, and consider raising `spec.watchedResources.<name>.debounceInterval` for high-churn resources (e.g. EndpointSlices on a large cluster) so each watcher batches more aggressively before triggering reconciliation

**Slow deployments:**

- Check Dataplane API health (`/v3/info` from inside the pod)
- Verify network latency to HAProxy pods
- Reduce config size by avoiding unnecessary nested loops in templates

## Performance Checklist

### Initial Deployment

- [ ] Set appropriate resource requests/limits
- [ ] Tune `dataplane.minDeploymentInterval` for workload, plus `spec.watchedResources.<name>.debounceInterval` per resource if the 2s default is wrong for a specific kind (e.g. slower on EndpointSlice on large clusters)
- [ ] Set HAProxy maxconn based on expected load
- [ ] Match nbthread to CPU allocation

### Ongoing Optimization

- [ ] Monitor reconciliation latency
- [ ] Monitor deployment latency
- [ ] Watch for memory growth
- [ ] Track event subscriber count

### High-Load Environments

- [ ] Scale HAProxy pods horizontally
- [ ] Enable HA mode for controller
- [ ] Limit watched namespaces
- [ ] Use label selectors to filter resources
- [ ] Profile and optimize templates

## See Also

- [Monitoring Guide](./monitoring.md) - Performance metrics and alerting
- [High Availability](./high-availability.md) - HA deployment patterns
- [Debugging Guide](./debugging.md) - Performance troubleshooting
