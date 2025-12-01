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
container_memory_working_set_bytes{container="haproxy-template-ic"}
```

### CPU Considerations

CPU spikes occur during:
- Template rendering (complex templates with many resources)
- Initial resource synchronization (startup)
- Burst of resource changes (rolling updates)

Monitor CPU usage:
```promql
rate(container_cpu_usage_seconds_total{container="haproxy-template-ic"}[5m])
```

## Reconciliation Tuning

### Debounce Interval

The controller debounces resource changes to avoid excessive reconciliation:

```yaml
# HAProxyTemplateConfig CRD
spec:
  controller:
    reconciliation:
      debounceInterval: 500ms  # Default
```

**Tuning guidelines:**
- **Lower (100-300ms)**: Faster response to changes, higher CPU usage
- **Default (500ms)**: Balanced for most workloads
- **Higher (1-5s)**: Better for high-churn environments with many changes

### Reconciliation Metrics

Monitor reconciliation performance:

```promql
# Average reconciliation duration
rate(haproxy_ic_reconciliation_duration_seconds_sum[5m]) /
rate(haproxy_ic_reconciliation_duration_seconds_count[5m])

# Reconciliation rate
rate(haproxy_ic_reconciliation_total[5m])

# P95 reconciliation latency
histogram_quantile(0.95, rate(haproxy_ic_reconciliation_duration_seconds_bucket[5m]))
```

**Target metrics:**
- Average reconciliation: <500ms
- P95 reconciliation: <2s
- Error rate: <1%

## Template Optimization

### Efficient Template Patterns

**Use early filtering:**
```jinja2
{#- GOOD: Filter early, process less data -#}
{%- set matching_ingresses = resources.ingresses.List() | selectattr("spec.ingressClassName", "equalto", "haproxy") | list %}
{%- for ingress in matching_ingresses %}
  ...
{%- endfor %}

{#- AVOID: Processing all ingresses then filtering -#}
{%- for ingress in resources.ingresses.List() %}
  {%- if ingress.spec.ingressClassName == "haproxy" %}
    ...
  {%- endif %}
{%- endfor %}
```

**Use compute_once for expensive operations:**
```jinja2
{%- set analysis = namespace(sorted_routes=[]) %}
{%- compute_once analysis %}
  {#- Expensive computation only runs once per render -#}
  {%- for route in resources.httproutes.List() %}
    {%- set _ = analysis.sorted_routes.append(route) %}
  {%- endfor %}
{%- endcompute_once %}
```

**Avoid nested loops when possible:**
```jinja2
{#- AVOID: O(n*m) complexity -#}
{%- for ingress in ingresses %}
  {%- for service in services %}
    {%- if ingress.spec.backend.service.name == service.metadata.name %}
      ...
    {%- endif %}
  {%- endfor %}
{%- endfor %}

{#- BETTER: Use indexing or filtering -#}
{%- set service_map = {} %}
{%- for service in services %}
  {%- set _ = service_map.update({service.metadata.name: service}) %}
{%- endfor %}
{%- for ingress in ingresses %}
  {%- set service = service_map.get(ingress.spec.backend.service.name) %}
  ...
{%- endfor %}
```

### Template Debugging

Profile template rendering:

```bash
# Enable template tracing
./bin/controller validate -f config.yaml --trace

# View trace output
cat /tmp/template-trace.log
```

## HAProxy Optimization

### Configuration Parameters

Key HAProxy parameters for performance:

```jinja2
global
    maxconn {{ controller.config.haproxy.maxconn | default(2000) }}
    nbthread {{ controller.config.haproxy.nbthread | default(4) }}
    tune.bufsize {{ controller.config.haproxy.bufsize | default(16384) }}
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

```jinja2
global
    tune.bufsize 32768        # 32KB for large headers
    tune.http.maxhdr 128      # Allow more headers
```

## Scaling Strategies

### Horizontal Scaling

Scale HAProxy pods for increased traffic:

```bash
kubectl scale deployment haproxy --replicas=5
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
      leader_election:
        enabled: true
```

Only the leader performs deployments; followers maintain hot-standby status.

### Resource Watching Optimization

Reduce watched resources to minimize controller load:

```yaml
# Only watch specific namespaces
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      namespaceSelector:
        matchNames:
          - production
          - staging

# Use label selectors
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      labelSelector:
        matchLabels:
          managed-by: haproxy-template-ic
```

## Deployment Performance

### Deployment Latency

Monitor deployment time:

```promql
# Average deployment duration
rate(haproxy_ic_deployment_duration_seconds_sum[5m]) /
rate(haproxy_ic_deployment_duration_seconds_count[5m])

# P95 deployment latency
histogram_quantile(0.95, rate(haproxy_ic_deployment_duration_seconds_bucket[5m]))
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

Configure drift prevention to avoid unnecessary deployments:

```yaml
spec:
  controller:
    deployment:
      driftPreventionInterval: 60s  # Check for drift every 60s
```

## Event Processing

### Event Buffer Sizing

The controller maintains event buffers for debugging:

```yaml
spec:
  controller:
    eventBufferSize: 1000  # Default
```

Increase for high-throughput environments if you need more event history.

### Subscriber Performance

Monitor event subscriber health:

```promql
# Event publishing rate
rate(haproxy_ic_events_published_total[5m])

# Subscriber count (should be constant)
haproxy_ic_event_subscribers
```

If subscriber count drops, components may be failing.

## Profiling

### Go Profiling

Access pprof endpoints for profiling:

```bash
# CPU profile (30 seconds)
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.pprof
go tool pprof -http=:8080 cpu.pprof

# Memory profile
curl http://localhost:6060/debug/pprof/heap > heap.pprof
go tool pprof -http=:8080 heap.pprof

# Goroutine dump
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

### Common Performance Issues

**High memory usage:**
- Check for memory leaks: growing heap over time
- Reduce event buffer size
- Limit watched resources

**High CPU usage:**
- Profile to find hot spots
- Optimize template complexity
- Increase debounce interval

**Slow deployments:**
- Check DataPlane API health
- Verify network latency to HAProxy pods
- Consider reducing config size

## Performance Checklist

### Initial Deployment
- [ ] Set appropriate resource requests/limits
- [ ] Configure debounce interval for workload
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
