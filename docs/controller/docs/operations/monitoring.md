# Monitoring Guide

This guide explains how to monitor HAPTIC (HAProxy Template Ingress Controller) using Prometheus metrics, including setup, key metrics, alerting, and dashboards.

## Overview

The controller exposes Prometheus metrics via an HTTP endpoint, providing visibility into reconciliation performance, deployment status, resource counts, and leader election state.

**Key monitoring areas:**

- Reconciliation cycle performance and errors
- HAProxy deployment latency and success rates
- Configuration validation status
- Kubernetes resource counts
- Leader election for HA deployments

!!! note "Two metric sources"
    Most of this guide is about the **controller's** metrics — the `haptic_*` family on port `9090`, which describe reconciliation, deployment, and leader-election health. HAProxy itself exposes a *separate* Prometheus endpoint on port `8404` carrying live traffic, backend health, and response-code data — see [HAProxy Data-Plane Metrics](#haproxy-data-plane-metrics). The chart's bundled `ServiceMonitor`/`PodMonitor` scrape the controller only.

## Enabling Metrics

Metrics are enabled by default. The controller serves Prometheus metrics at `/metrics` on the metrics port (separate from the debug port — by default `:9090`). No additional configuration is needed beyond pointing Prometheus at this endpoint.

The controller reads its metrics port from the `METRICS_PORT` env var (default `9090`). To disable the metrics server, set `METRICS_PORT=0` on the controller container. Via Helm, use `extraEnv`:

```yaml
# values.yaml — disable the metrics server entirely
extraEnv:
  - name: METRICS_PORT
    value: "0"
```

The CRD has a `controller.metricsPort` field, but the chart strips it from the serialized CRD (it's only used for chart-side templating like `NOTES.txt`). Setting `controller.config.controller.metricsPort: 0` in Helm values does *not* disable the metrics server.

## Accessing Metrics

### Prometheus Scrape Configuration

Add a scrape config for the controller:

```yaml
scrape_configs:
  - job_name: 'haptic'
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
        regex: haptic
        action: keep
      - source_labels: [__meta_kubernetes_pod_container_port_number]
        regex: "9090"
        action: keep
```

### ServiceMonitor (Prometheus Operator)

If using Prometheus Operator, enable the ServiceMonitor in Helm:

```yaml
# values.yaml
monitoring:
  serviceMonitor:
    enabled: true
    interval: 30s
    labels:
      release: prometheus  # Match your Prometheus selector
```

The chart also ships a `PodMonitor` (`monitoring.podMonitor.enabled`) for setups that scrape pods directly instead of via the Service — enable whichever your Prometheus setup uses.

### Manual Access

```bash
# Port-forward to metrics endpoint
kubectl port-forward -n haptic deployment/haptic-controller 9090:9090

# Fetch metrics
curl http://localhost:9090/metrics
```

## Metrics Reference

### Reconciliation Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_reconciliation_total` | Counter | Total reconciliation cycles triggered |
| `haptic_reconciliation_duration_seconds` | Histogram | Time spent in reconciliation cycles |
| `haptic_reconciliation_errors_total` | Counter | Failed reconciliation cycles |

**Key queries:**

```promql
# Reconciliation rate per second
rate(haptic_reconciliation_total[5m])

# Average reconciliation duration
rate(haptic_reconciliation_duration_seconds_sum[5m]) /
rate(haptic_reconciliation_duration_seconds_count[5m])

# Success rate percentage
100 * (1 - (
  rate(haptic_reconciliation_errors_total[5m]) /
  rate(haptic_reconciliation_total[5m])
))
```

### Deployment Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_deployment_total` | Counter | Total deployment attempts |
| `haptic_deployment_duration_seconds` | Histogram | Time spent deploying to HAProxy |
| `haptic_deployment_errors_total` | Counter | Failed deployments |

**Key queries:**

```promql
# Deployment rate
rate(haptic_deployment_total[5m])

# 95th percentile deployment latency
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))

# Deployment success rate
100 * (1 - (
  rate(haptic_deployment_errors_total[5m]) /
  rate(haptic_deployment_total[5m])
))
```

### Runtime Fast-Path Metrics

The runtime fast path applies runtime-eligible server changes (address, port, admin state) directly to the running HAProxy worker via the Dataplane API, bypassing a config reload. `applies` stuck at 0 while `fires` climbs means the fast path runs but the render diff never carries a runtime-eligible change.

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_runtime_fast_path_fires_total` | Counter | Runtime-eligible fast-path apply attempts (one per pod per reconcile) |
| `haptic_runtime_fast_path_applies_total` | Counter | Fast-path attempts that applied at least one runtime-eligible server update |
| `haptic_runtime_fast_path_failures_total` | Counter | Fast-path attempts that errored (best-effort; the scheduled deploy converges) |
| `haptic_runtime_fast_path_server_updates_total` | Counter | Total runtime-eligible server updates applied via the fast path |

**Key queries:**

```promql
# Fraction of fast-path attempts that carried a runtime-eligible change
rate(haptic_runtime_fast_path_applies_total[5m]) /
rate(haptic_runtime_fast_path_fires_total[5m])

# Runtime server updates applied without a reload
rate(haptic_runtime_fast_path_server_updates_total[5m])
```

### Validation Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_validation_total` | Counter | Total validation attempts |
| `haptic_validation_errors_total` | Counter | Failed validations |

**Key queries:**

```promql
# Validation rate
rate(haptic_validation_total[5m])

# Validation success rate
100 * (1 - (
  rate(haptic_validation_errors_total[5m]) /
  rate(haptic_validation_total[5m])
))
```

### Resource Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_resource_count` | Gauge | `type` | Current count of watched resources |
| `haptic_haproxy_pods_rejected_total` | Counter | `reason` | HAProxy pods refused admission by the discovery component. Persistent non-zero growth typically means the controller cannot talk to the deployed HAProxy pods (for example, the bundled HAProxy major.minor differs from the chart's `haproxyVersion`). |
| `haptic_config_rejected_total` | Counter | `validator` | `HAProxyTemplateConfig` loads refused by the config-validation gate. The `validator` label names which check rejected it (`basic`, `template`, `jsonpath`, `validationtests`, or `coordinator` when a validator timed out). Non-zero growth means the leader is refusing new config and continuing on the last-good one — **alert on it**: the operator's latest change is not live. |

**Key queries:**

```promql
# All resource counts
haptic_resource_count

# Specific resource types
haptic_resource_count{type="ingresses"}
haptic_resource_count{type="services"}
haptic_resource_count{type="haproxy-pods"}

# Resource count changes
delta(haptic_resource_count[1h])

# Rejected HAProxy pods, broken down by reason
sum by (reason) (rate(haptic_haproxy_pods_rejected_total[5m]))

# Config rejected (leader refusing new config) — alert if > 0
sum by (validator) (rate(haptic_config_rejected_total[5m]))
```

### Event Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_event_subscribers` | Gauge | Active event subscribers |
| `haptic_events_published_total` | Counter | Total events published |

**Key queries:**

```promql
# Event publishing rate
rate(haptic_events_published_total[5m])

# Subscriber count (should be constant)
haptic_event_subscribers

# Subscriber changes (indicates component restarts)
delta(haptic_event_subscribers[5m])
```

### Leader Election Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_leader_election_is_leader` | Gauge | 1 if this replica is leader, 0 otherwise |
| `haptic_leader_election_transitions_total` | Counter | Leadership transitions (gain/loss) |
| `haptic_leader_election_time_as_leader_seconds_total` | Counter | Cumulative time as leader |

**Key queries:**

```promql
# Current leader count (should be exactly 1)
sum(haptic_leader_election_is_leader)

# Identify leader pod
haptic_leader_election_is_leader == 1

# Leadership transition rate
rate(haptic_leader_election_transitions_total[1h])

# Average time as leader per transition
haptic_leader_election_time_as_leader_seconds_total /
haptic_leader_election_transitions_total
```

### Webhook Metrics

Exposed when the validating admission webhook is enabled (`webhook.enabled=true`).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_webhook_requests_total` | Counter | `gvk`, `result` | Total admission requests by GroupVersionKind and result |
| `haptic_webhook_request_duration_seconds` | Histogram | — | Time spent processing webhook requests |
| `haptic_webhook_validation_total` | Counter | `gvk`, `result` | Validation outcomes (allowed / rejected / error) per GVK |

**Key queries:**

```promql
# Reject rate per resource kind
sum by (gvk) (rate(haptic_webhook_validation_total{result="rejected"}[5m]))

# 95th percentile webhook latency (must stay well under the 10s admission timeout)
histogram_quantile(0.95, rate(haptic_webhook_request_duration_seconds_bucket[5m]))
```

### Reconciliation Queue & Parser Cache

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_reconciliation_queue_wait_seconds` | Histogram | Time a triggered reconciliation waits in the coordinator queue before processing starts; rising values indicate the controller can't keep up with change volume |
| `haptic_parser_cache_hits_total` | Counter | Parser cache hits — the controller caches parsed HAProxy configs so repeated reconciliations don't re-parse unchanged input |
| `haptic_parser_cache_misses_total` | Counter | Parser cache misses |

```promql
# Parser cache hit ratio
rate(haptic_parser_cache_hits_total[5m]) /
(rate(haptic_parser_cache_hits_total[5m]) + rate(haptic_parser_cache_misses_total[5m]))
```

### Event Bus Backpressure

These complement `haptic_events_published_total` / `haptic_event_subscribers` from above.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_events_dropped_total` | Counter | — | Total events dropped because a subscriber's buffer was full |
| `haptic_events_dropped_critical_total` | Counter | — | Drops from critical subscribers (alert if > 0 — indicates lost reconciliation work) |
| `haptic_events_dropped_observability_total` | Gauge | — | Drops from observability-only subscribers (expected under load, non-alerting) |
| `haptic_events_dropped_by_subscriber_total` | Counter | `subscriber`, `event_type` | Per-subscriber drop counts for diagnosing which component is falling behind |

### Build Info

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_build_info` | Gauge | `version`, `haproxy_version`, `go_version` | Always `1`; useful for joining build metadata into other queries |

```promql
# Pin a query to controller version 0.1.0
haptic_reconciliation_total * on() group_left(version) haptic_build_info{version="0.1.0"}
```

## HAProxy Data-Plane Metrics

Every metric above comes from the **controller** (`haptic_*`, port `9090`) — they describe reconciliation, deployment, and leader-election health, not live traffic. HAProxy itself exposes a separate Prometheus endpoint carrying the data-plane signals operators usually watch most closely: per-frontend request rates, per-backend response-code breakdowns, and session counts.

The bundled config enables HAProxy's built-in [Prometheus exporter](https://github.com/haproxy/haproxy/tree/master/addons/promex) on the status frontend (port `8404`, path `/metrics`) by default — it is served from the always-on `status-extra-100-prometheus-exporter` snippet, so no extra flag is required. The HAProxy Service exposes that port under the name `stats`.

!!! warning "The chart's ServiceMonitor / PodMonitor do not scrape HAProxy"
    Both bundled monitors collect the controller's `haptic_*` metrics only: the `PodMonitor` selects `app.kubernetes.io/component: controller`, and the `ServiceMonitor` scrapes the `metrics` port (`9090`) that only the controller Service exposes. Neither targets HAProxy's `8404`. To collect HAProxy's `haproxy_*` metrics, add a separate scrape (or `ServiceMonitor`) pointed at the HAProxy pods.

### Scrape Configuration

Plain Prometheus — keep HAProxy pods (`component: loadbalancer`) and scrape their `8404` container port:

```yaml
scrape_configs:
  - job_name: 'haproxy'
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_component]
        regex: loadbalancer
        action: keep
      - source_labels: [__meta_kubernetes_pod_container_port_number]
        regex: "8404"
        action: keep
```

Prometheus Operator — a `ServiceMonitor` against the HAProxy Service's `stats` port:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: haproxy
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: haptic
      app.kubernetes.io/component: loadbalancer
  endpoints:
    - port: stats   # 8404
      path: /metrics
```

### Key Queries

HAProxy labels frontend/backend metrics with `proxy` (the section name) and server metrics with `server`:

```promql
# Request rate per frontend
sum by (proxy) (rate(haproxy_frontend_http_requests_total[5m]))

# 5xx response rate per backend
sum by (proxy) (rate(haproxy_backend_http_responses_total{code="5xx"}[5m]))

# Active sessions per backend
sum by (proxy) (haproxy_backend_current_sessions)
```

The full metric set is HAProxy's own, not HAPTIC's — see the [HAProxy Prometheus exporter reference](https://github.com/haproxy/haproxy/tree/master/addons/promex) for every exposed series and its labels.

## Alerting Rules

If you deploy via the Helm chart, it ships a built-in `PrometheusRule` (enable with `monitoring.prometheusRule.enabled`) covering a core set of controller alerts. The rules below are a broader recommended set you can copy and adapt for any Prometheus setup.

### Recommended Alerts

```yaml
groups:
  - name: haptic
    rules:
      # Reconciliation failures
      - alert: HAProxyICHighReconciliationErrorRate
        expr: |
          rate(haptic_reconciliation_errors_total[5m]) /
          rate(haptic_reconciliation_total[5m]) > 0.1
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
            rate(haptic_deployment_duration_seconds_bucket[5m])
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
          rate(haptic_validation_errors_total[5m]) > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Configuration validation failing"
          description: "HAProxy configuration has syntax or validation errors"

      # Component crash
      - alert: HAProxyICComponentStopped
        expr: |
          delta(haptic_event_subscribers[5m]) < 0
        labels:
          severity: critical
        annotations:
          summary: "Event subscriber count decreased"
          description: "A controller component may have crashed"

      # No leader elected (HA)
      - alert: HAProxyICNoLeader
        expr: sum(haptic_leader_election_is_leader) < 1
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "No HAProxy controller leader elected"
          description: "No controller replica is elected as leader"

      # Multiple leaders (split-brain)
      - alert: HAProxyICMultipleLeaders
        expr: sum(haptic_leader_election_is_leader) > 1
        labels:
          severity: critical
        annotations:
          summary: "Multiple HAProxy controller leaders detected"
          description: "Split-brain condition - multiple replicas think they are leader"

      # Frequent leadership changes
      - alert: HAProxyICFrequentLeadershipChanges
        expr: rate(haptic_leader_election_transitions_total[1h]) > 5
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Frequent leadership transitions"
          description: "Controller leadership changing too often, may indicate cluster instability"

      # No HAProxy pods discovered
      - alert: HAProxyICNoHAProxyPods
        expr: haptic_resource_count{type="haproxy-pods"} < 1
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "No HAProxy pods discovered"
          description: "Controller cannot find any HAProxy pods to manage"

      # Critical events dropped (lost reconciliation work)
      - alert: HAProxyICCriticalEventsDropped
        expr: increase(haptic_events_dropped_critical_total[5m]) > 0
        labels:
          severity: critical
        annotations:
          summary: "Critical events dropped from event bus"
          description: "A critical subscriber's buffer overflowed; reconciliation work has been lost"
```

!!! note "Tuning alert thresholds"
    The thresholds above suit typical production environments. For high-churn environments (frequent deployments, many short-lived resources), increase the `for` duration on reconciliation and deployment alerts to avoid noise. For development clusters, consider relaxing error rate thresholds or disabling non-critical alerts entirely.

## Dashboard Examples

The chart ships a complete built-in Grafana dashboard (29 panels) — enable it with `monitoring.grafanaDashboard.enabled: true` (the default `useBuiltIn: true` renders `dashboards/haptic.json` into a `<release>-grafana-dashboard` ConfigMap that the Grafana sidecar auto-discovers; set a custom one via `grafanaDashboard.customDashboard`). The queries and JSON template below are for building your own dashboard or extending the bundled one.

### Grafana Dashboard Queries

**Reconciliation Overview Panel:**

```promql
# Success rate (stat panel)
100 * (1 - (
  rate(haptic_reconciliation_errors_total[5m]) /
  rate(haptic_reconciliation_total[5m])
))

# Rate over time (graph)
rate(haptic_reconciliation_total[5m])
rate(haptic_reconciliation_errors_total[5m])
```

**Deployment Latency Panel:**

```promql
# P50, P95, P99 latencies
histogram_quantile(0.50, rate(haptic_deployment_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))
histogram_quantile(0.99, rate(haptic_deployment_duration_seconds_bucket[5m]))
```

**Resource Count Panel:**

```promql
# All resource types
haptic_resource_count

# Stacked area chart by type
haptic_resource_count{type=~"ingresses|services|endpoints"}
```

**Leader Election Panel:**

```promql
# Current leader indicator
haptic_leader_election_is_leader == 1

# Transition count over time
increase(haptic_leader_election_transitions_total[1h])
```

### Dashboard JSON Template

Example Grafana dashboard structure (use as a starting point):

```json
{
  "title": "HAPTIC",
  "panels": [
    {
      "title": "Reconciliation Rate",
      "targets": [
        {"expr": "rate(haptic_reconciliation_total[5m])"}
      ]
    },
    {
      "title": "Reconciliation Success Rate",
      "targets": [
        {"expr": "100 * (1 - rate(haptic_reconciliation_errors_total[5m]) / rate(haptic_reconciliation_total[5m]))"}
      ]
    },
    {
      "title": "Deployment Latency",
      "targets": [
        {"expr": "histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))"}
      ]
    },
    {
      "title": "Resource Counts",
      "targets": [
        {"expr": "haptic_resource_count"}
      ]
    },
    {
      "title": "Leader Status",
      "targets": [
        {"expr": "haptic_leader_election_is_leader"}
      ]
    }
  ]
}
```

This is a starting point -- add additional panels using the [PromQL queries](#grafana-dashboard-queries) above for more detailed views of deployment latency distribution, resource counts over time, or per-pod leader status.

## Operational Insights

### Key Health Indicators

| Indicator | Healthy Range | Action if Unhealthy |
|-----------|---------------|---------------------|
| Reconciliation success rate | >99% | Check logs for template/validation errors |
| Deployment success rate | >99% | Check HAProxy pod connectivity |
| P95 deployment latency | <2s | Check HAProxy DataPlane API performance |
| Leader count | Exactly 1 | Check HA configuration and network |
| Event subscribers | Should not decrease during normal operation | Restart controller if dropping |

### Capacity Planning

Monitor these metrics for capacity planning:

```promql
# Reconciliation frequency (how often config changes)
rate(haptic_reconciliation_total[1h]) * 3600

# Ingress growth rate
deriv(haptic_resource_count{type="ingresses"}[1d])

# Average reconciliation overhead
avg_over_time(haptic_reconciliation_duration_seconds_sum[1d]) /
avg_over_time(haptic_reconciliation_duration_seconds_count[1d])
```

### Troubleshooting with Metrics

**High reconciliation error rate:**

1. Check `haptic_validation_errors_total` - template/config issues
2. Check `haptic_deployment_errors_total` - HAProxy connectivity issues
3. Review controller logs for specific error messages

**Missing metrics:**

1. Verify the metrics server is enabled — `METRICS_PORT` env var on the controller container is non-zero (default `9090`; the `metricsPort` field on the CRD is *not* read by the controller)
2. Check ServiceMonitor selector matches Prometheus configuration
3. Verify network policies allow scraping

**Leader election issues:**

1. Check if `sum(haptic_leader_election_is_leader) != 1`
2. Review `rate(haptic_leader_election_transitions_total[1h])` for instability
3. See [High Availability Guide](./high-availability.md) for troubleshooting

## Integration with Existing Monitoring

### Prometheus Operator

```yaml
# values.yaml
monitoring:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      release: prometheus
```

The chart renders a `ServiceMonitor` that targets the controller Service in the release namespace — no explicit namespace selector is needed.

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

- [Helm Chart Monitoring Configuration](https://haproxy-haptic.org/helm-chart/latest/operations/monitoring/) - ServiceMonitor setup via Helm values
- [Debugging Guide](./debugging.md) - Runtime introspection and troubleshooting
- [High Availability](./high-availability.md) - Leader election configuration
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
