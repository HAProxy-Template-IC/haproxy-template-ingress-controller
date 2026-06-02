# Monitoring

## Overview

The controller exposes Prometheus metrics on port 9090 at the `/metrics` endpoint. This page covers how to enable and configure metrics collection via the Helm chart.

For the complete metrics reference, alerting rules, and dashboard examples, see the [Monitoring Guide](https://haproxy-haptic.org/controller/latest/operations/monitoring/) in the controller documentation.

## Metrics Overview

The controller exposes **36 Prometheus metrics**. The authoritative list lives in [`pkg/controller/metrics/metrics.go`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/pkg/controller/metrics/metrics.go); a representative subset is asserted by `TestMetrics_AllMetricsRegistered`. The full catalogue with types, labels, and update semantics is in [`pkg/controller/metrics/README.md`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/pkg/controller/metrics/README.md). High-level coverage:

- **Reconciliation pipeline**: cycles, errors, duration, queue wait
- **Deployment**: operations, errors, duration
- **Config validation** and **embedded validation tests**: totals, errors, pass/fail/duration
- **Watched resources**: per-type counts
- **Event bus**: subscribers, publishes, drops (by subscriber, critical, observability)
- **Webhook**: admission requests, validation results, cert expiry + rotations
- **Leader election**: is-leader gauge, transitions, time-as-leader
- **Parser cache**: hits and misses
- **Build info**: static gauge labelled with controller / Go / HAProxy versions

## Quick Access

Access metrics directly via port-forward:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 9090:9090
curl http://localhost:9090/metrics
```

## Prometheus ServiceMonitor

Enable Prometheus Operator integration:

```yaml
monitoring:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      prometheus: kube-prometheus  # Match your Prometheus selector
```

### With NetworkPolicy

If using NetworkPolicy, allow Prometheus to scrape metrics. See [Networking](./networking.md) for details.

### Advanced ServiceMonitor Configuration

Add custom labels and relabeling:

```yaml
monitoring:
  serviceMonitor:
    enabled: true
    interval: 15s
    labels:
      prometheus: kube-prometheus
      team: platform
    # Add cluster label to all metrics
    relabelings:
      - sourceLabels: [__address__]
        targetLabel: cluster
        replacement: production
    # Drop specific metrics
    metricRelabelings:
      - sourceLabels: [__name__]
        regex: 'haptic_event_subscribers'
        action: drop
```

## Example Prometheus Queries

```promql
# Reconciliation rate (per second)
rate(haptic_reconciliation_total[5m])

# Error rate
rate(haptic_reconciliation_errors_total[5m])

# 95th percentile reconciliation duration
histogram_quantile(0.95, rate(haptic_reconciliation_duration_seconds_bucket[5m]))

# Current HAProxy pod count
haptic_resource_count{type="haproxy-pods"}
```

## Grafana Dashboard

Create dashboards using these key metrics:

1. **Operations Overview**: reconciliation_total, deployment_total, validation_total
2. **Error Tracking**: *_errors_total counters
3. **Performance**: *_duration_seconds histograms
4. **Resource Utilization**: resource_count gauge

For complete metric definitions and more queries, see `pkg/controller/metrics/README.md` in the repository.
