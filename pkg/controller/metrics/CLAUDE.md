# pkg/controller/metrics - Controller Metrics Development Context

Development context for controller domain-specific metrics.

**API Documentation**: See `pkg/controller/metrics/README.md`

## When to Work Here

Modify this package when:

- Adding new controller metrics (reconciliation, deployment, validation, etc.)
- Modifying metric update logic
- Adding new event types to track
- Changing resource count tracking
- Fixing metrics component bugs

**DO NOT** modify this package for:

- Generic metrics infrastructure → Use `pkg/metrics`
- Event bus infrastructure → Use `pkg/events`
- Event type definitions → Use `pkg/controller/events`
- Business logic → Use appropriate controller sub-package

## Package Structure

```
pkg/controller/metrics/
├── metrics.go          # Domain metrics definitions
├── metrics_test.go     # Metrics unit tests
├── component.go        # Event adapter component
├── component_test.go   # Component integration tests
├── README.md           # API documentation
└── CLAUDE.md           # This file
```

## Key Design Pattern: Event Adapter

This package follows the **Event Adapter Pattern** used throughout the controller:

```
┌──────────────────────────────────────────────────┐
│         Pure Component (metrics.go)              │
│  - No event dependencies                         │
│  - Pure business logic                           │
│  - Can be used standalone                        │
│  - Easy to test                                  │
└──────────────────┬───────────────────────────────┘
                   │
                   │ Wrapped by
                   ▼
┌──────────────────────────────────────────────────┐
│       Event Adapter (component.go)               │
│  - Subscribes to events                          │
│  - Calls pure component methods                  │
│  - Translates events → metric updates            │
│  - Coordinates with other components             │
└──────────────────────────────────────────────────┘
```

**Benefits:**

- Pure metrics can be tested without event bus
- Event adapter can be tested with mock events
- Clean separation of concerns
- Reusable patterns across controller

## Component Details

### metrics.go - Pure Metrics

Defines and creates Prometheus metrics without any event dependencies.

**Structure:**

```go
type Metrics struct {
    // All Prometheus metric types
    ReconciliationDuration prometheus.Histogram
    ReconciliationTotal    prometheus.Counter
    // ... more metrics
}

// Constructor creates all metrics and registers them
func NewMetrics(registry prometheus.Registerer) *Metrics {
    return &Metrics{
        ReconciliationDuration: pkgmetrics.NewHistogram(
            registry,
            "reconciliation_duration_seconds",
            "Time spent in reconciliation cycles",
        ),
        // ... create more metrics
    }
}

// Helper methods for updating metrics. Note the duration unit is *seconds*
// (Prometheus convention), not milliseconds — the event adapter converts the
// int64 millisecond value from events.ReconciliationCompletedEvent before
// calling this method.
func (m *Metrics) RecordReconciliation(durationSeconds float64, success bool) {
    m.ReconciliationTotal.Inc()
    if !success {
        m.ReconciliationErrors.Inc()
    }
    m.ReconciliationDuration.Observe(durationSeconds)
}
```

**Why Pure?**

- Can be used outside event-driven context
- Easy to test without event infrastructure
- Clear API for metric updates
- Reusable in different contexts

### component.go - Event Adapter

Subscribes to events and updates metrics accordingly.

**Structure:**

```go
type Component struct {
    metrics        *Metrics                    // Pure component
    eventBus       *pkgevents.EventBus         // Event coordination
    eventChan      <-chan pkgevents.Event      // Pre-subscribed channel
    resourceCounts map[string]int              // Stateful tracking
}

// Constructor — subscribes during construction, before bus.Start().
// The subscription uses SubscribeTypes for a typed filter (only the
// event types the component actually handles), with a buffer of 200.
func New(metrics *Metrics, eventBus *pkgevents.EventBus) *Component {
    eventChan := eventBus.SubscribeTypes(ComponentName, 200,
        events.EventTypeReconciliationCompleted,
        events.EventTypeReconciliationFailed,
        // ... and ~12 more types — see component.go for the full list
    )

    return &Component{
        metrics:        metrics,
        eventBus:       eventBus,
        eventChan:      eventChan,
        resourceCounts: make(map[string]int),
        triggeredAt:    make(map[string]time.Time),
    }
}

// Start runs the event processing loop. Blocks until ctx is cancelled.
// A periodic ticker also pulls bus / parser-cache stats into gauges.
func (c *Component) Start(ctx context.Context) error {
    ticker := time.NewTicker(timeouts.TickerPollInterval)
    defer ticker.Stop()

    for {
        select {
        case event := <-c.eventChan:
            c.handleEvent(event)
        case <-ticker.C:
            c.metrics.SetObservabilityDrops(c.eventBus.DroppedEventsObservability())
            // ... parser cache + subscriber count gauges
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

**Why Event Adapter?**

- Decouples metrics from event sources
- Centralizes metric update logic
- Easy to add new event handlers
- Testable with mock events

## Event Handling

### Event to Metric Mapping

Each event type maps to specific metric updates:

```go
func (c *Component) handleEvent(event pkgevents.Event) {
    // Increment event counter for all events
    c.metrics.RecordEvent()

    switch e := event.(type) {
    case *events.ReconciliationCompletedEvent:
        c.metrics.RecordReconciliation(msToSeconds(e.DurationMs), true)

    case *events.ReconciliationFailedEvent:
        c.metrics.RecordReconciliation(0, false)

    case *events.DeploymentCompletedEvent:
        // The deployment event reports Total / Succeeded / Failed counts; the
        // component tracks "did anything succeed" rather than "did everything
        // succeed" because partial rollouts still register as forward progress.
        c.metrics.RecordDeployment(msToSeconds(e.DurationMs), e.Succeeded > 0)

    case *events.ValidationCompletedEvent:
        // Warnings are informational; ValidationCompletedEvent itself signals
        // success. Failure is a separate ValidationFailedEvent.
        c.metrics.RecordValidation(true)

    case *events.IndexSynchronizedEvent:
        // Initialize resource counts
        for resourceType, count := range e.ResourceCounts {
            c.resourceCounts[resourceType] = count
            c.metrics.SetResourceCount(resourceType, count)
        }

    case *events.ResourceIndexUpdatedEvent:
        // Update resource count incrementally
        if !e.ChangeStats.IsInitialSync {
            delta := e.ChangeStats.Created - e.ChangeStats.Deleted
            newCount := c.resourceCounts[e.ResourceTypeName] + delta
            c.resourceCounts[e.ResourceTypeName] = newCount
            c.metrics.SetResourceCount(e.ResourceTypeName, newCount)
        }
    }
}
```

### Resource Count Tracking

Resource counts require stateful tracking because events provide deltas, not absolutes:

```go
type Component struct {
    // ...
    resourceCounts map[string]int  // Track current counts
}

// Initialize from IndexSynchronizedEvent
case *events.IndexSynchronizedEvent:
    for resourceType, count := range e.ResourceCounts {
        c.resourceCounts[resourceType] = count
        c.metrics.SetResourceCount(resourceType, count)
    }

// Update from ResourceIndexUpdatedEvent
case *events.ResourceIndexUpdatedEvent:
    if !e.ChangeStats.IsInitialSync {
        // Calculate new count: old + created - deleted
        delta := e.ChangeStats.Created - e.ChangeStats.Deleted
        newCount := c.resourceCounts[e.ResourceTypeName] + delta
        c.resourceCounts[e.ResourceTypeName] = newCount
        c.metrics.SetResourceCount(e.ResourceTypeName, newCount)
    }
```

## Testing Strategy

### Unit Tests (metrics_test.go)

Test pure metrics without events:

```go
func TestMetrics_RecordReconciliation(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := NewMetrics(registry)

    // Record successful reconciliation (1.5 seconds)
    metrics.RecordReconciliation(1.5, true)

    // Verify counters
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationTotal))
    assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ReconciliationErrors))

    // Record failed reconciliation
    metrics.RecordReconciliation(0, false)

    // Verify error counter incremented
    assert.Equal(t, 2.0, testutil.ToFloat64(metrics.ReconciliationTotal))
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationErrors))
}
```

### Integration Tests (component_test.go)

Test event-driven updates:

```go
func TestComponent_ReconciliationEvents(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := NewMetrics(registry)
    eventBus := pkgevents.NewEventBus(100)

    component := New(metrics, eventBus)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Subscription happened in New() — just start the bus, then the loop.
    eventBus.Start()
    go component.Start(ctx)

    // Publish event
    eventBus.Publish(events.NewReconciliationCompletedEvent(1500))

    // Give component time to process
    time.Sleep(100 * time.Millisecond)

    // Verify metrics updated
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationTotal))
    assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ReconciliationErrors))
}
```

### Testing Resource Count Tracking

Test stateful resource count updates:

```go
func TestComponent_ResourceEvents(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := NewMetrics(registry)
    eventBus := pkgevents.NewEventBus(100)

    component := New(metrics, eventBus)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    eventBus.Start()
    go component.Start(ctx)

    // Initialize counts
    eventBus.Publish(events.NewIndexSynchronizedEvent(map[string]int{
        "ingresses": 10,
        "services":  5,
    }))

    time.Sleep(100 * time.Millisecond)

    ingresses, _ := metrics.ResourceCount.GetMetricWithLabelValues("ingresses")
    assert.Equal(t, 10.0, testutil.ToFloat64(ingresses))

    // Update: add 3, delete 1 → 10 + 3 - 1 = 12
    eventBus.Publish(events.NewResourceIndexUpdatedEvent(
        "ingresses",
        types.ChangeStats{
            Created:       3,
            Deleted:       1,
            IsInitialSync: false,
        },
    ))

    time.Sleep(100 * time.Millisecond)

    ingresses, _ = metrics.ResourceCount.GetMetricWithLabelValues("ingresses")
    assert.Equal(t, 12.0, testutil.ToFloat64(ingresses))
}
```

## Common Pitfalls

### Constructing Subscribers After bus.Start()

**Problem**: Calling `New(...)` after the bus is already running. Any events published in between are lost.

```go
// Bad - race condition
bus.Start()                     // Events start flowing
component := New(metrics, bus)  // Subscribes too late, may miss events
go component.Start(ctx)
```

**Solution**: The metrics component subscribes inside `New(...)`. Construct every subscriber before `bus.Start()`.

```go
// Good
component := New(metrics, bus)  // Subscribes during construction
bus.Start()                     // Events now reach the existing subscriber
go component.Start(ctx)         // Process events
```

### Using time.Sleep in Tests

**Problem**: Tests using sleep to wait for async processing.

```go
// Bad - brittle, slow
eventBus.Publish(event)
time.Sleep(100 * time.Millisecond)  // Hope it's enough time
assert.Equal(t, expected, actual)
```

**Solution**: While we currently use small sleeps (100ms), ideally we'd use synchronization:

```go
// Better (future improvement)
done := make(chan struct{})
component := &Component{
    metrics: metrics,
    onEvent: func() { close(done) },  // Signal when processed
}

eventBus.Publish(event)
select {
case <-done:
    // Event processed
case <-time.After(1 * time.Second):
    t.Fatal("timeout")
}
```

### Incorrect Resource Count Calculations

**Problem**: Not handling initial sync correctly.

```go
// Bad - processes initial sync as delta
case *events.ResourceIndexUpdatedEvent:
    delta := e.ChangeStats.Created - e.ChangeStats.Deleted
    newCount := c.resourceCounts[e.ResourceTypeName] + delta
    c.resourceCounts[e.ResourceTypeName] = newCount
```

**Solution**: Check IsInitialSync flag.

```go
// Good - skip initial sync
case *events.ResourceIndexUpdatedEvent:
    if !e.ChangeStats.IsInitialSync {
        delta := e.ChangeStats.Created - e.ChangeStats.Deleted
        newCount := c.resourceCounts[e.ResourceTypeName] + delta
        c.resourceCounts[e.ResourceTypeName] = newCount
        c.metrics.SetResourceCount(e.ResourceTypeName, newCount)
    }
```

### Forgetting to Update All Metrics

**Problem**: New event added but metrics not updated.

```go
// New event published
eventBus.Publish(events.NewCacheClearedEvent())

// But component doesn't handle it → metric never updated
```

**Solution**: Add handler for every relevant event.

```go
func (c *Component) handleEvent(event pkgevents.Event) {
    c.metrics.RecordEvent()  // Always increment event counter

    switch e := event.(type) {
    // ... existing cases ...

    case *events.CacheClearedEvent:
        c.metrics.RecordCacheClear()  // Add new metric update
    }
}
```

### Wrong Metric Type for Operation

**Problem**: Using gauge for cumulative count.

```go
// Bad - reconciliation count should accumulate
ReconciliationCount prometheus.Gauge

func (m *Metrics) RecordReconciliation() {
    m.ReconciliationCount.Inc()  // Works but semantically wrong
}
```

**Solution**: Use counter for cumulative values.

```go
// Good - counter accumulates
ReconciliationTotal prometheus.Counter

func (m *Metrics) RecordReconciliation() {
    m.ReconciliationTotal.Inc()
}
```

## Adding New Metrics

When adding new metrics:

1. **Define in Metrics struct**

```go
type Metrics struct {
    // ... existing metrics ...

    // New metric
    CacheHitTotal prometheus.Counter
    CacheMissTotal prometheus.Counter
}
```

2. **Create in New() constructor**

```go
func NewMetrics(registry prometheus.Registerer) *Metrics {
    return &Metrics{
        // ... existing metrics ...

        CacheHitTotal: pkgmetrics.NewCounter(
            registry,
            "cache_hit_total",
            "Total cache hits",
        ),
        CacheMissTotal: pkgmetrics.NewCounter(
            registry,
            "cache_miss_total",
            "Total cache misses",
        ),
    }
}
```

3. **Add helper method**

```go
func (m *Metrics) RecordCacheAccess(hit bool) {
    if hit {
        m.CacheHitTotal.Inc()
    } else {
        m.CacheMissTotal.Inc()
    }
}
```

4. **Add event handler (if event-driven)**

```go
func (c *Component) handleEvent(event pkgevents.Event) {
    // ... existing cases ...

    case *events.CacheAccessEvent:
        c.metrics.RecordCacheAccess(e.Hit)
}
```

5. **Add unit test**

```go
func TestMetrics_RecordCacheAccess(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := NewMetrics(registry)

    metrics.RecordCacheAccess(true)
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.CacheHitTotal))
    assert.Equal(t, 0.0, testutil.ToFloat64(metrics.CacheMissTotal))

    metrics.RecordCacheAccess(false)
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.CacheHitTotal))
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.CacheMissTotal))
}
```

6. **Add component test (if event-driven)**

```go
func TestComponent_CacheEvents(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := NewMetrics(registry)
    eventBus := pkgevents.NewEventBus(100)
    component := New(metrics, eventBus)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    eventBus.Start()
    go component.Start(ctx)

    eventBus.Publish(events.NewCacheAccessEvent(true))
    time.Sleep(100 * time.Millisecond)

    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.CacheHitTotal))
}
```

7. **Update README.md with metric documentation**

```markdown
### Cache Metrics

**haptic_parser_cache_hits_total** (counter)
- Total number of cache hits

**haptic_parser_cache_misses_total** (counter)
- Total number of cache misses

**Example Queries:**
\`\`\`promql
# Cache hit rate
rate(haptic_parser_cache_hits_total[5m]) /
(rate(haptic_parser_cache_hits_total[5m]) + rate(haptic_parser_cache_misses_total[5m]))
\`\`\`
```

## Performance Considerations

### Event Buffer Size

The component subscribes with buffer size 200 (inside `New(...)`):

```go
eventChan := eventBus.SubscribeTypes(ComponentName, 200, /* event-type list */)
```

**Why 200?**

- Metrics component processes all events (high volume)
- Processing is very fast (just incrementing counters)
- Large buffer prevents blocking event publishers
- Trade-off: Memory vs responsiveness

If metrics processing becomes slow, consider:

1. Increasing buffer size
2. Batching metric updates
3. Using async metric updates

### Memory Usage

Resource count map grows with number of resource types:

```go
type Component struct {
    resourceCounts map[string]int  // ~10-20 entries typical
}
```

**Current size:** Negligible (10-20 entries)
**If growth is concern:** Consider bounded map with LRU eviction

## Integration Points

### Controller Startup (pkg/controller/controller.go)

```go
// Sketch — see pkg/controller/iteration.go for the actual sequencing.
// metricsComponent is constructed (and subscribes) before the bus starts;
// Start(ctx) blocks on the event loop, so it goes in its own goroutine.
bus.Start()

go func() {
    if err := metricsComponent.Start(iterCtx); err != nil {
        logger.Error("metrics component failed", "error", err)
    }
}()

// Metrics HTTP server is wired separately in pkg/controller (see infrastructure.go).
```

### Event Types (pkg/controller/events/types.go)

All event types that trigger metric updates:

```go
// Reconciliation
type ReconciliationCompletedEvent struct { DurationMs int64 /* + Correlation, timestamp */ }
type ReconciliationFailedEvent    struct { Error string  /* + Correlation, timestamp */ }

// Deployment (real fields — see pkg/controller/events/deployment.go)
type DeploymentCompletedEvent struct {
    Total, Succeeded, Failed int
    DurationMs               int64
    ReloadsTriggered         int
    TotalAPIOperations       int
    OperationBreakdown       map[string]int
    BackendDiffFields        string
}
type InstanceDeploymentFailedEvent struct { Endpoint string; Error string }

// Validation
type ValidationCompletedEvent struct {
    Warnings   []string
    DurationMs int64
    ParsedConfig *parser.StructuredConfig // pre-parsed so deployer can skip the parse
}
type ValidationFailedEvent struct { Errors []string; DurationMs int64 }

// Resources
type IndexSynchronizedEvent     struct { ResourceCounts map[string]int }
type ResourceIndexUpdatedEvent  struct { ResourceTypeName string; ChangeStats types.ChangeStats }
```

## Resources

- API documentation: `pkg/controller/metrics/README.md`
- Generic metrics: `pkg/metrics/CLAUDE.md`
- Event types: `pkg/controller/events/types.go`
- Controller orchestration: `pkg/controller/CLAUDE.md`
- Prometheus documentation: <https://prometheus.io/docs/>
