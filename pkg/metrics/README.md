# pkg/metrics - Prometheus Metrics Infrastructure

Generic, reusable utilities for Prometheus metrics with instance-based registry support.

## Overview

This package provides infrastructure for exposing Prometheus metrics via HTTP endpoints. It follows instance-based design patterns to support application reinitialization without global state pollution.

**Key Design Principle:** All metrics use instance-based `prometheus.Registry` instead of the global default registry. This allows metrics to be garbage collected when components are reinitialized.

## Components

### Server

HTTP server for exposing Prometheus metrics via `/metrics` endpoint.

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "haproxy-template-ic/pkg/metrics"
)

// Create instance-based registry
registry := prometheus.NewRegistry()

// Register your metrics with the registry
counter := prometheus.NewCounter(prometheus.CounterOpts{
    Name: "my_counter",
    Help: "Example counter",
})
registry.MustRegister(counter)

// Create and start metrics server
server := metrics.NewServer(":9090", registry)
if err := server.Start(ctx); err != nil {
    log.Fatal(err)
}
```

**Features:**

- Instance-based (not global)
- Graceful shutdown support
- OpenMetrics format support
- Security headers (read timeout)
- Helpful root handler with links

### Helpers

Convenience functions for creating Prometheus metrics with consistent naming.

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "haproxy-template-ic/pkg/metrics"
)

registry := prometheus.NewRegistry()

// Create metrics with helpers
counter := metrics.NewCounter(registry, "requests_total", "Total requests")
histogram := metrics.NewHistogram(registry, "request_duration_seconds", "Request duration")
gauge := metrics.NewGauge(registry, "active_connections", "Active connections")

// Create metrics with labels
statusCounter := metrics.NewCounterVec(
    registry,
    "http_requests_total",
    "HTTP requests by status",
    []string{"status", "method"},
)

// Use custom buckets for histograms
latencyHistogram := metrics.NewHistogramWithBuckets(
    registry,
    "api_latency_seconds",
    "API latency",
    metrics.DurationBuckets(),
)
```

## Usage Patterns

### Instance-Based Registry

Always use instance-based registries to support reinitialization:

```go
// Good - instance-based
registry := prometheus.NewRegistry()
counter := metrics.NewCounter(registry, "operations", "Operations")

// Bad - uses global registry
counter := prometheus.NewCounter(prometheus.CounterOpts{
    Name: "operations",
})
prometheus.MustRegister(counter)  // Global registration
```

### Lifecycle Management

Metrics are tied to application lifecycle:

```go
func runIteration(ctx context.Context) error {
    // Create fresh registry for this iteration
    registry := prometheus.NewRegistry()

    // Create metrics
    counter := metrics.NewCounter(registry, "events", "Events processed")

    // Start server
    server := metrics.NewServer(":9090", registry)
    go server.Start(ctx)

    // When context is cancelled, server stops
    // Registry and metrics are garbage collected
    <-ctx.Done()
    return nil
}
```

### Standard Metric Naming

Follow Prometheus naming conventions:

```go
// Counters - suffix with _total
requests := metrics.NewCounter(registry, "requests_total", "Total requests")

// Histograms - suffix with unit
duration := metrics.NewHistogram(registry, "duration_seconds", "Request duration")

// Gauges - no suffix, current state
connections := metrics.NewGauge(registry, "active_connections", "Active connections")

// Add subsystem prefix for clarity
deploymentCounter := metrics.NewCounter(
    registry,
    "deployment_total",
    "Total deployments",
)
// Actual metric name: "haproxy_ic_deployment_total"
```

### Custom Bucket Sizes

Use appropriate bucket sizes for your metrics:

```go
// Duration metrics - use DurationBuckets()
latency := metrics.NewHistogramWithBuckets(
    registry,
    "request_duration_seconds",
    "Request duration",
    metrics.DurationBuckets(),  // 10ms to 10s
)

// Size metrics - use custom buckets
size := metrics.NewHistogramWithBuckets(
    registry,
    "response_size_bytes",
    "Response size",
    []float64{100, 1000, 10000, 100000, 1000000},
)
```

## Server Endpoints

### GET /metrics

Exposes metrics in Prometheus/OpenMetrics format.

**Response:**

```
# HELP haproxy_ic_requests_total Total requests
# TYPE haproxy_ic_requests_total counter
haproxy_ic_requests_total 42

# HELP haproxy_ic_duration_seconds Request duration
# TYPE haproxy_ic_duration_seconds histogram
haproxy_ic_duration_seconds_bucket{le="0.01"} 10
haproxy_ic_duration_seconds_bucket{le="0.05"} 25
haproxy_ic_duration_seconds_bucket{le="+Inf"} 42
haproxy_ic_duration_seconds_sum 1.5
haproxy_ic_duration_seconds_count 42
```

### GET /

Returns helpful information about available endpoints.

## Testing

### Unit Tests

Test metric creation and registration:

```go
func TestMetricCreation(t *testing.T) {
    registry := prometheus.NewRegistry()

    counter := metrics.NewCounter(registry, "test_total", "Test counter")
    counter.Inc()

    // Verify value
    assert.Equal(t, 1.0, testutil.ToFloat64(counter))
}
```

### Integration Tests

Test server functionality:

```go
func TestMetricsServer(t *testing.T) {
    registry := prometheus.NewRegistry()
    counter := metrics.NewCounter(registry, "test_total", "Test")
    counter.Add(42)

    server := metrics.NewServer(":0", registry)  // Random port
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go server.Start(ctx)
    time.Sleep(100 * time.Millisecond)

    // Fetch metrics
    resp, err := http.Get(fmt.Sprintf("http://localhost:%d/metrics", server.Port()))
    require.NoError(t, err)
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    assert.Contains(t, string(body), "haproxy_ic_test_total 42")
}
```

## Best Practices

### DO

- ✅ Use instance-based registries
- ✅ Follow Prometheus naming conventions
- ✅ Use appropriate metric types (counter, gauge, histogram)
- ✅ Choose meaningful bucket sizes for histograms
- ✅ Add labels for dimensions, but keep cardinality low
- ✅ Document what each metric measures

### DON'T

- ❌ Use global `prometheus.DefaultRegisterer`
- ❌ Create metrics with high cardinality labels (e.g., user IDs)
- ❌ Re-register metrics on the same registry
- ❌ Mix metric types (don't use counter for gauge-like values)
- ❌ Use generic metric names (be specific about what's measured)

## Prometheus Queries

### Counter Metrics

Rate of events per second:

```promql
rate(haproxy_ic_requests_total[5m])
```

Total events in time range:

```promql
increase(haproxy_ic_requests_total[1h])
```

### Histogram Metrics

Average latency:

```promql
rate(haproxy_ic_duration_seconds_sum[5m]) /
rate(haproxy_ic_duration_seconds_count[5m])
```

95th percentile latency:

```promql
histogram_quantile(0.95, rate(haproxy_ic_duration_seconds_bucket[5m]))
```

### Gauge Metrics

Current value:

```promql
haproxy_ic_active_connections
```

Maximum value over time:

```promql
max_over_time(haproxy_ic_active_connections[5m])
```

## Architecture

This package provides generic infrastructure. Domain-specific metrics should be implemented in their respective packages:

- **pkg/metrics** - Generic utilities (this package)
- **pkg/controller/metrics** - Controller domain metrics
- **pkg/dataplane/metrics** - HAProxy integration metrics (future)

## Resources

- Development context: `pkg/metrics/CLAUDE.md`
- Prometheus documentation: <https://prometheus.io/docs/>
- Prometheus naming best practices: <https://prometheus.io/docs/practices/naming/>
- Prometheus client library: <https://github.com/prometheus/client_golang>
