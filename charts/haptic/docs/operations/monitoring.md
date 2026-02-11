# Monitoring

## Overview

The controller exposes Prometheus metrics on port 9090 at the `/metrics` endpoint. This page covers how to enable and configure metrics collection via the Helm chart.

For the complete metrics reference, alerting rules, and dashboard examples, see the [Monitoring Guide](https://haproxy-haptic.org/controller/latest/operations/monitoring/) in the controller documentation.

## Metrics Overview

The controller exposes 11 Prometheus metrics covering:

- **Reconciliation**: Cycles, errors, and duration
- **Deployment**: Operations, errors, and duration
- **Validation**: Total validations and errors
- **Resources**: Tracked resource counts by type
- **Events**: Event bus activity and subscribers

## Quick Access

Access metrics directly via port-forward:

```bash
# Port-forward to controller pod
kubectl port-forward -n <namespace> pod/<controller-pod> 9090:9090

# Fetch metrics
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
