# Monitoring Guide

This guide explains how to monitor the HAProxy Template Ingress Controller using Prometheus metrics, including setup, key metrics, alerting, and dashboards.

## Overview

The controller exposes Prometheus metrics via an HTTP endpoint, providing visibility into reconciliation performance, deployment status, resource counts, and leader election state.

**Key monitoring areas:**
- Reconciliation cycle performance and errors
- HAProxy deployment latency and success rates
- Configuration validation status
- Kubernetes resource counts
- Leader election for HA deployments

## Enabling Metrics

Metrics are enabled by default. Configure the metrics port in Helm values:

```yaml
# values.yaml
controller:
  config:
    controller:
      metrics_port: 9090  # Default
```

Set to `0` to disable metrics.

## Accessing Metrics

### Prometheus Scrape Configuration

Add a scrape config for the controller:

```yaml
scrape_configs:
  - job_name: 'haproxy-template-ic'
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
        regex: haproxy-template-ic
        action: keep
      - source_labels: [__meta_kubernetes_pod_container_port_number]
        regex: "9090"
        action: keep
```

### ServiceMonitor (Prometheus Operator)

If using Prometheus Operator, enable the ServiceMonitor in Helm:

```yaml
# values.yaml
serviceMonitor:
  enabled: true
  interval: 30s
  labels:
    release: prometheus  # Match your Prometheus selector
```

### Manual Access

```bash
# Port-forward to metrics endpoint
kubectl port-forward -n <namespace> deployment/haproxy-template-ic 9090:9090

# Fetch metrics
curl http://localhost:9090/metrics
```

## Metrics Reference

### Reconciliation Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haproxy_ic_reconciliation_total` | Counter | Total reconciliation cycles triggered |
| `haproxy_ic_reconciliation_duration_seconds` | Histogram | Time spent in reconciliation cycles |
| `haproxy_ic_reconciliation_errors_total` | Counter | Failed reconciliation cycles |

**Key queries:**
```promql
# Reconciliation rate per second
rate(haproxy_ic_reconciliation_total[5m])

# Average reconciliation duration
rate(haproxy_ic_reconciliation_duration_seconds_sum[5m]) /
rate(haproxy_ic_reconciliation_duration_seconds_count[5m])

# Success rate percentage
100 * (1 - (
  rate(haproxy_ic_reconciliation_errors_total[5m]) /
  rate(haproxy_ic_reconciliation_total[5m])
))
```

### Deployment Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haproxy_ic_deployment_total` | Counter | Total deployment attempts |
| `haproxy_ic_deployment_duration_seconds` | Histogram | Time spent deploying to HAProxy |
| `haproxy_ic_deployment_errors_total` | Counter | Failed deployments |

**Key queries:**
```promql
# Deployment rate
rate(haproxy_ic_deployment_total[5m])

# 95th percentile deployment latency
histogram_quantile(0.95, rate(haproxy_ic_deployment_duration_seconds_bucket[5m]))

# Deployment success rate
100 * (1 - (
  rate(haproxy_ic_deployment_errors_total[5m]) /
  rate(haproxy_ic_deployment_total[5m])
))
```

### Validation Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haproxy_ic_validation_total` | Counter | Total validation attempts |
| `haproxy_ic_validation_errors_total` | Counter | Failed validations |

**Key queries:**
```promql
# Validation rate
rate(haproxy_ic_validation_total[5m])

# Validation success rate
100 * (1 - (
  rate(haproxy_ic_validation_errors_total[5m]) /
  rate(haproxy_ic_validation_total[5m])
))
```

### Resource Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haproxy_ic_resource_count` | Gauge | `type` | Current count of watched resources |

**Key queries:**
```promql
# All resource counts
haproxy_ic_resource_count

# Specific resource types
haproxy_ic_resource_count{type="ingresses"}
haproxy_ic_resource_count{type="services"}
haproxy_ic_resource_count{type="haproxy-pods"}

# Resource count changes
delta(haproxy_ic_resource_count[1h])
```

### Event Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haproxy_ic_event_subscribers` | Gauge | Active event subscribers |
| `haproxy_ic_events_published_total` | Counter | Total events published |

**Key queries:**
```promql
# Event publishing rate
rate(haproxy_ic_events_published_total[5m])

# Subscriber count (should be constant)
haproxy_ic_event_subscribers

# Subscriber changes (indicates component restarts)
delta(haproxy_ic_event_subscribers[5m])
```

### Leader Election Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haproxy_ic_leader_election_is_leader` | Gauge | 1 if this replica is leader, 0 otherwise |
| `haproxy_ic_leader_election_transitions_total` | Counter | Leadership transitions (gain/loss) |
| `haproxy_ic_leader_election_time_as_leader_seconds_total` | Counter | Cumulative time as leader |

**Key queries:**
```promql
# Current leader count (should be exactly 1)
sum(haproxy_ic_leader_election_is_leader)

# Identify leader pod
haproxy_ic_leader_election_is_leader == 1

# Leadership transition rate
rate(haproxy_ic_leader_election_transitions_total[1h])

# Average time as leader per transition
haproxy_ic_leader_election_time_as_leader_seconds_total /
haproxy_ic_leader_election_transitions_total
```

## Alerting Rules

### Recommended Alerts

```yaml
groups:
  - name: haproxy-template-ic
    rules:
      # Reconciliation failures
      - alert: HAProxyICHighReconciliationErrorRate
        expr: |
          rate(haproxy_ic_reconciliation_errors_total[5m]) /
          rate(haproxy_ic_reconciliation_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High reconciliation error rate (>10%)"
          description: "Controller is failing to reconcile configurations"

      # Deployment latency
      - alert: HAProxyICHighDeploymentLatency
        expr: |
          histogram_quantile(0.95,
            rate(haproxy_ic_deployment_duration_seconds_bucket[5m])
          ) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "95th percentile deployment latency >5s"
          description: "Deploying configs to HAProxy is taking too long"

      # Validation failures
      - alert: HAProxyICValidationFailures
        expr: |
          rate(haproxy_ic_validation_errors_total[5m]) > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Configuration validation failing"
          description: "HAProxy configuration has syntax or validation errors"

      # Component crash
      - alert: HAProxyICComponentStopped
        expr: |
          delta(haproxy_ic_event_subscribers[5m]) < 0
        labels:
          severity: critical
        annotations:
          summary: "Event subscriber count decreased"
          description: "A controller component may have crashed"

      # No leader elected (HA)
      - alert: HAProxyICNoLeader
        expr: sum(haproxy_ic_leader_election_is_leader) < 1
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "No HAProxy controller leader elected"
          description: "No controller replica is elected as leader"

      # Multiple leaders (split-brain)
      - alert: HAProxyICMultipleLeaders
        expr: sum(haproxy_ic_leader_election_is_leader) > 1
        labels:
          severity: critical
        annotations:
          summary: "Multiple HAProxy controller leaders detected"
          description: "Split-brain condition - multiple replicas think they are leader"

      # Frequent leadership changes
      - alert: HAProxyICFrequentLeadershipChanges
        expr: rate(haproxy_ic_leader_election_transitions_total[1h]) > 5
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Frequent leadership transitions"
          description: "Controller leadership changing too often, may indicate cluster instability"

      # No HAProxy pods discovered
      - alert: HAProxyICNoHAProxyPods
        expr: haproxy_ic_resource_count{type="haproxy-pods"} < 1
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "No HAProxy pods discovered"
          description: "Controller cannot find any HAProxy pods to manage"
```

## Dashboard Examples

### Grafana Dashboard Queries

**Reconciliation Overview Panel:**
```promql
# Success rate (stat panel)
100 * (1 - (
  rate(haproxy_ic_reconciliation_errors_total[5m]) /
  rate(haproxy_ic_reconciliation_total[5m])
))

# Rate over time (graph)
rate(haproxy_ic_reconciliation_total[5m])
rate(haproxy_ic_reconciliation_errors_total[5m])
```

**Deployment Latency Panel:**
```promql
# P50, P95, P99 latencies
histogram_quantile(0.50, rate(haproxy_ic_deployment_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(haproxy_ic_deployment_duration_seconds_bucket[5m]))
histogram_quantile(0.99, rate(haproxy_ic_deployment_duration_seconds_bucket[5m]))
```

**Resource Count Panel:**
```promql
# All resource types
haproxy_ic_resource_count

# Stacked area chart by type
haproxy_ic_resource_count{type=~"ingresses|services|endpoints"}
```

**Leader Election Panel:**
```promql
# Current leader indicator
haproxy_ic_leader_election_is_leader == 1

# Transition count over time
increase(haproxy_ic_leader_election_transitions_total[1h])
```

### Dashboard JSON Template

A basic Grafana dashboard template can be imported from:

```json
{
  "title": "HAProxy Template IC",
  "panels": [
    {
      "title": "Reconciliation Rate",
      "targets": [
        {"expr": "rate(haproxy_ic_reconciliation_total[5m])"}
      ]
    },
    {
      "title": "Reconciliation Success Rate",
      "targets": [
        {"expr": "100 * (1 - rate(haproxy_ic_reconciliation_errors_total[5m]) / rate(haproxy_ic_reconciliation_total[5m]))"}
      ]
    },
    {
      "title": "Deployment Latency",
      "targets": [
        {"expr": "histogram_quantile(0.95, rate(haproxy_ic_deployment_duration_seconds_bucket[5m]))"}
      ]
    },
    {
      "title": "Resource Counts",
      "targets": [
        {"expr": "haproxy_ic_resource_count"}
      ]
    },
    {
      "title": "Leader Status",
      "targets": [
        {"expr": "haproxy_ic_leader_election_is_leader"}
      ]
    }
  ]
}
```

## Operational Insights

### Key Health Indicators

| Indicator | Healthy Range | Action if Unhealthy |
|-----------|---------------|---------------------|
| Reconciliation success rate | >99% | Check logs for template/validation errors |
| Deployment success rate | >99% | Check HAProxy pod connectivity |
| P95 deployment latency | <2s | Check HAProxy DataPlane API performance |
| Leader count | =1 | Check HA configuration and network |
| Event subscribers | Constant | Restart controller if dropping |

### Capacity Planning

Monitor these metrics for capacity planning:

```promql
# Reconciliation frequency (how often config changes)
rate(haproxy_ic_reconciliation_total[1h]) * 3600

# Ingress growth rate
deriv(haproxy_ic_resource_count{type="ingresses"}[1d])

# Average reconciliation overhead
avg_over_time(haproxy_ic_reconciliation_duration_seconds_sum[1d]) /
avg_over_time(haproxy_ic_reconciliation_duration_seconds_count[1d])
```

### Troubleshooting with Metrics

**High reconciliation error rate:**
1. Check `haproxy_ic_validation_errors_total` - template/config issues
2. Check `haproxy_ic_deployment_errors_total` - HAProxy connectivity issues
3. Review controller logs for specific error messages

**Missing metrics:**
1. Verify metrics port is enabled (`controller.config.controller.metrics_port`)
2. Check ServiceMonitor selector matches Prometheus configuration
3. Verify network policies allow scraping

**Leader election issues:**
1. Check if `sum(haproxy_ic_leader_election_is_leader) != 1`
2. Review `rate(haproxy_ic_leader_election_transitions_total[1h])` for instability
3. See [High Availability Guide](./high-availability.md) for troubleshooting

## Integration with Existing Monitoring

### Prometheus Operator

```yaml
# values.yaml
serviceMonitor:
  enabled: true
  interval: 30s
  scrapeTimeout: 10s
  labels:
    release: prometheus
  namespaceSelector:
    matchNames:
      - haproxy-template-ic
```

### Victoria Metrics

Use the same Prometheus scrape configuration - Victoria Metrics is compatible.

### Datadog

Configure Datadog Agent to scrape Prometheus metrics:

```yaml
# datadog-agent values
datadog:
  prometheusScrape:
    enabled: true
    serviceEndpoints: true
```

## See Also

- [Debugging Guide](./debugging.md) - Runtime introspection and troubleshooting
- [High Availability](./high-availability.md) - Leader election configuration
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
