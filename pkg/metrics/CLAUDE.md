# pkg/metrics - Metrics Infrastructure Development Context

Development context for working with Prometheus metrics infrastructure.

**API Documentation**: See `pkg/metrics/README.md`

## When to Work Here

Modify this package when:

- Adding new helper functions for metric creation
- Modifying metrics server behavior
- Changing metric naming conventions
- Adding new bucket presets (e.g., size buckets, count buckets)
- Fixing metrics server bugs

**DO NOT** modify this package for:

- Domain-specific metrics → Use `pkg/controller/metrics` or domain packages
- Business logic → This is pure infrastructure
- Application-specific configuration → Use config module

## Package Structure

```
pkg/metrics/
├── server.go          # HTTP server for /metrics endpoint
├── server_test.go     # Server tests
├── helpers.go         # Metric creation helpers
├── helpers_test.go    # Helper tests
├── README.md          # API documentation
└── CLAUDE.md          # This file
```

## Key Design Principle: Instance-Based Registries

**Critical:** This package NEVER uses `prometheus.DefaultRegisterer` or any global state.

### Why Instance-Based?

The controller uses a reinitialization pattern where configuration changes trigger complete component restart:

```go
// Main loop reinitializes on config change
for {
    // Create fresh registry for this iteration
    registry := prometheus.NewRegistry()

    // Create metrics and components
    metrics := createMetrics(registry)
    server := metrics.NewServer(":9090", registry)

    // Run until config change
    <-configChange

    // Cancel context → server stops
    // Registry goes out of scope → garbage collected
    // No cleanup needed!
}
```

**Benefits:**

- Metrics reset automatically on reinitialization
- No stale data from previous iterations
- No need for manual metric cleanup
- No global state pollution

**Consequences:**

- Must pass registry to all metric creation functions
- Cannot use `prometheus.MustRegister()` without registry parameter
- Slightly more verbose API (worth it for clean lifecycle)

## Component Details

### server.go - Metrics HTTP Server

Serves Prometheus metrics on HTTP endpoint.

**Key Features:**

1. **Instance-based design**

```go
type Server struct {
    addr     string
    registry prometheus.Gatherer  // Interface, not *Registry
    server   *http.Server
    logger   *slog.Logger
}

func NewServer(addr string, registry prometheus.Gatherer) *Server {
    // Accepts any Gatherer, not just *Registry
    // Enables testing with custom gatherers
}
```

2. **Graceful shutdown**

```go
func (s *Server) Start(ctx context.Context) error {
    go func() {
        <-ctx.Done()
        // Graceful shutdown with 5s timeout
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        s.server.Shutdown(shutdownCtx)
    }()

    return s.server.ListenAndServe()
}
```

3. **Security settings**

```go
s.server = &http.Server{
    Addr:              addr,
    Handler:           mux,
    ReadHeaderTimeout: 5 * time.Second,  // Prevent slowloris attacks
}
```

4. **OpenMetrics support**

```go
mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
    EnableOpenMetrics: true,  // Support newer format
}))
```

### helpers.go - Metric Creation Helpers

Convenience functions with consistent naming.

**Naming Convention:**

All metrics are prefixed with `haptic_`:

```go
const metricPrefix = "haptic_"

func metricName(name string) string {
    return metricPrefix + name
}
```

**Helper Types:**

```go
// Simple metrics (no labels)
NewCounter(registry, name, help)
NewGauge(registry, name, help)
NewHistogram(registry, name, help)
NewHistogramWithBuckets(registry, name, help, buckets)

// Vector metrics (with labels)
NewCounterVec(registry, name, help, labels)
NewGaugeVec(registry, name, help, labels)
```

**Bucket Presets:**

```go
// DurationBuckets returns buckets for latency metrics (10ms to 10s)
func DurationBuckets() []float64 {
    return []float64{
        0.01,  // 10ms
        0.05,  // 50ms
        0.1,   // 100ms
        0.25,  // 250ms
        0.5,   // 500ms
        1,     // 1s
        2.5,   // 2.5s
        5,     // 5s
        10,    // 10s
    }
}
```

## Testing Strategy

### Server Tests

Test server lifecycle and HTTP behavior:

```go
func TestServer_Start(t *testing.T) {
    registry := prometheus.NewRegistry()
    server := NewServer(":0", registry)  // Port 0 = random port

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    errChan := make(chan error)
    go func() {
        errChan <- server.Start(ctx)
    }()

    // Server should exit when context cancelled
    cancel()

    select {
    case err := <-errChan:
        assert.ErrorIs(t, err, http.ErrServerClosed)
    case <-time.After(10 * time.Second):
        t.Fatal("server did not shut down")
    }
}
```

### Helper Tests

Test metric creation and registration:

```go
func TestNewCounter(t *testing.T) {
    registry := prometheus.NewRegistry()

    counter := NewCounter(registry, "test_total", "Test counter")
    counter.Inc()

    // Verify registration
    gathered, err := registry.Gather()
    require.NoError(t, err)
    assert.Len(t, gathered, 1)
    assert.Equal(t, "haptic_test_total", gathered[0].GetName())

    // Verify value
    assert.Equal(t, 1.0, testutil.ToFloat64(counter))
}
```

### Instance Isolation Tests

Critical test ensuring no global state:

```go
func TestNoGlobalRegistryUsage(t *testing.T) {
    // Create instance 1
    registry1 := prometheus.NewRegistry()
    counter1 := NewCounter(registry1, "test", "Test")
    counter1.Add(10)

    // Create instance 2 with SAME metric name
    registry2 := prometheus.NewRegistry()
    counter2 := NewCounter(registry2, "test", "Test")
    counter2.Add(20)

    // Verify isolation - each registry only sees its own metrics
    assert.Equal(t, 10.0, testutil.ToFloat64(counter1))
    assert.Equal(t, 20.0, testutil.ToFloat64(counter2))

    gathered1, _ := registry1.Gather()
    gathered2, _ := registry2.Gather()

    // Each should have exactly 1 metric
    assert.Len(t, gathered1, 1)
    assert.Len(t, gathered2, 1)

    // If global registry was used, this would fail
}
```

## Common Pitfalls

### Using Global Registry

**Problem**: Metrics registered globally don't get cleaned up.

```go
// Bad - uses global registry
counter := prometheus.NewCounter(prometheus.CounterOpts{
    Name: "requests_total",
})
prometheus.MustRegister(counter)  // Global!
```

**Solution**: Always pass registry parameter.

```go
// Good - instance-based
registry := prometheus.NewRegistry()
counter := metrics.NewCounter(registry, "requests_total", "Total requests")
```

### Re-registering Metrics

**Problem**: Attempting to register same metric twice.

```go
// Bad - will panic on second call
func createMetrics(registry *prometheus.Registry) {
    counter := metrics.NewCounter(registry, "events", "Events")
    // Called again → panic: duplicate metrics collector registration
}
```

**Solution**: Create metrics once per registry instance.

```go
// Good - one registry per iteration
for {
    registry := prometheus.NewRegistry()  // Fresh registry
    counter := metrics.NewCounter(registry, "events", "Events")
    // ... use metrics ...
    // Registry goes out of scope on next iteration
}
```

### Wrong Metric Type

**Problem**: Using counter for gauge-like values.

```go
// Bad - counter only goes up, can't track current connections
connections := metrics.NewCounter(registry, "connections", "Current connections")
connections.Inc()  // Connect
connections.Dec()  // ERROR: Counter has no Dec() method!
```

**Solution**: Use appropriate metric type.

```go
// Good - gauge can increase and decrease
connections := metrics.NewGauge(registry, "active_connections", "Current connections")
connections.Inc()  // Connect
connections.Dec()  // Disconnect
```

### High Cardinality Labels

**Problem**: Creating too many unique label combinations.

```go
// Bad - creates millions of unique metrics
userCounter := metrics.NewCounterVec(
    registry,
    "user_requests",
    "Requests by user",
    []string{"user_id"},  // High cardinality!
)

// Every unique user_id creates a new time series
userCounter.WithLabelValues("user_12345").Inc()
userCounter.WithLabelValues("user_67890").Inc()
// ... millions more ...
```

**Solution**: Use labels for dimensions with bounded cardinality.

```go
// Good - limited number of methods and status codes
httpCounter := metrics.NewCounterVec(
    registry,
    "http_requests_total",
    "HTTP requests",
    []string{"method", "status"},  // Low cardinality: ~10 methods × ~10 statuses
)

httpCounter.WithLabelValues("GET", "200").Inc()
httpCounter.WithLabelValues("POST", "201").Inc()
```

### Forgetting Units in Names

**Problem**: Unclear what unit a metric measures.

```go
// Bad - is this seconds? milliseconds?
latency := metrics.NewHistogram(registry, "request_latency", "Request latency")
```

**Solution**: Include unit in metric name.

```go
// Good - clearly in seconds
latency := metrics.NewHistogram(
    registry,
    "request_duration_seconds",
    "Request duration in seconds",
)
```

## Adding New Helpers

When adding new helper functions:

1. **Follow naming pattern**

```go
func NewMetricType(registry prometheus.Registerer, name, help string) MetricType {
    metric := prometheus.NewMetricType(prometheus.MetricTypeOpts{
        Name: metricName(name),
        Help: help,
    })
    registry.MustRegister(metric)
    return metric
}
```

2. **Add tests**

```go
func TestNewMetricType(t *testing.T) {
    registry := prometheus.NewRegistry()
    metric := NewMetricType(registry, "test", "Test metric")

    // Verify registration
    gathered, err := registry.Gather()
    require.NoError(t, err)
    assert.Len(t, gathered, 1)
    assert.Equal(t, "haptic_test", gathered[0].GetName())
}
```

3. **Document in README.md**

```markdown
### NewMetricType

Creates a MetricType metric.

Usage:
\`\`\`go
metric := metrics.NewMetricType(registry, "name", "description")
\`\`\`
```

4. **Consider if preset buckets are needed**

```go
// If this is a histogram/summary, provide bucket presets
func SizeBuckets() []float64 {
    return []float64{100, 1024, 10240, 102400, 1048576}
}
```

## Metric Naming Conventions

Follow Prometheus best practices:

### Counter Metrics

Suffix with `_total`:

```go
requests := metrics.NewCounter(registry, "requests_total", "Total requests")
errors := metrics.NewCounter(registry, "errors_total", "Total errors")
```

### Histogram/Summary Metrics

Suffix with unit:

```go
duration := metrics.NewHistogram(registry, "duration_seconds", "Duration in seconds")
size := metrics.NewHistogram(registry, "size_bytes", "Size in bytes")
```

### Gauge Metrics

No suffix, represents current state:

```go
connections := metrics.NewGauge(registry, "active_connections", "Active connections")
memory := metrics.NewGauge(registry, "memory_usage_bytes", "Memory usage")
```

### Label Names

Use snake_case for label names:

```go
counter := metrics.NewCounterVec(
    registry,
    "http_requests_total",
    "HTTP requests",
    []string{"http_method", "status_code", "endpoint_path"},
)
```

## Performance Considerations

### Metric Creation

Metric creation is relatively expensive (involves locks, maps, validation). Create metrics once at initialization, not per-request:

```go
// Bad - creates new metric on every request
func handleRequest(registry *prometheus.Registry) {
    counter := metrics.NewCounter(registry, "requests", "Requests")  // Expensive!
    counter.Inc()
}

// Good - create once, reuse
type Handler struct {
    requestCounter prometheus.Counter
}

func NewHandler(registry *prometheus.Registry) *Handler {
    return &Handler{
        requestCounter: metrics.NewCounter(registry, "requests", "Requests"),
    }
}

func (h *Handler) handleRequest() {
    h.requestCounter.Inc()  // Fast!
}
```

### Label Values

WithLabelValues is relatively fast but still involves map lookups. Cache metric vectors when possible:

```go
// Good - cache commonly used label combinations
type Metrics struct {
    getRequests  prometheus.Counter
    postRequests prometheus.Counter
}

func NewMetrics(registry *prometheus.Registry) *Metrics {
    vec := metrics.NewCounterVec(registry, "requests", "Requests", []string{"method"})
    return &Metrics{
        getRequests:  vec.WithLabelValues("GET"),   // Cache
        postRequests: vec.WithLabelValues("POST"),  // Cache
    }
}

func (m *Metrics) RecordGet() {
    m.getRequests.Inc()  // No map lookup
}
```

## Integration with Controller

This package provides infrastructure. Domain metrics live in `pkg/controller/metrics`:

```go
// pkg/controller/metrics/metrics.go - Domain metrics
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    pkgmetrics "haptic/pkg/metrics"
)

type Metrics struct {
    ReconciliationTotal    prometheus.Counter
    ReconciliationDuration prometheus.Histogram
    DeploymentTotal        prometheus.Counter
    // ... more domain metrics
}

func New(registry *prometheus.Registry) *Metrics {
    return &Metrics{
        ReconciliationTotal: pkgmetrics.NewCounter(
            registry,
            "reconciliation_total",
            "Total reconciliation cycles",
        ),
        ReconciliationDuration: pkgmetrics.NewHistogram(
            registry,
            "reconciliation_duration_seconds",
            "Reconciliation duration",
        ),
        // ... create more metrics
    }
}
```

## Resources

- API documentation: `pkg/metrics/README.md`
- Prometheus documentation: <https://prometheus.io/docs/>
- Prometheus client library: <https://github.com/prometheus/client_golang>
- Domain metrics: `pkg/controller/metrics/CLAUDE.md`
