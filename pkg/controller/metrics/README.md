# pkg/controller/metrics - Controller Domain Metrics

Domain-specific Prometheus metrics for the HAProxy Template Ingress Controller.

## Overview

This package provides controller-specific metrics and an event adapter component that translates controller events into Prometheus metric updates.

**Architecture:**

- **metrics.go** - Defines controller-specific Prometheus metrics
- **component.go** - Event adapter that subscribes to controller events and updates metrics

## Metrics

### Reconciliation Metrics

Track reconciliation cycle performance and errors.

**haptic_reconciliation_total** (counter)

- Total number of reconciliation cycles triggered
- Increments on both successful and failed reconciliations

**haptic_reconciliation_duration_seconds** (histogram)

- Time spent in reconciliation cycles
- Buckets: 10ms to 10s (see pkg/metrics.DurationBuckets)

**haptic_reconciliation_errors_total** (counter)

- Total number of failed reconciliation cycles
- Increments when reconciliation fails due to template errors, validation failures, etc.

**Example Queries:**

```promql
# Reconciliation rate per second
rate(haptic_reconciliation_total[5m])

# Average reconciliation duration
rate(haptic_reconciliation_duration_seconds_sum[5m]) /
rate(haptic_reconciliation_duration_seconds_count[5m])

# Error rate
rate(haptic_reconciliation_errors_total[5m])

# Success rate percentage
100 * (1 - (
  rate(haptic_reconciliation_errors_total[5m]) /
  rate(haptic_reconciliation_total[5m])
))
```

### Deployment Metrics

Track HAProxy configuration deployment performance.

**haptic_deployment_total** (counter)

- Total number of deployment attempts
- Increments regardless of success/failure

**haptic_deployment_duration_seconds** (histogram)

- Time spent deploying configurations to HAProxy instances
- Buckets: 10ms to 10s

**haptic_deployment_errors_total** (counter)

- Total number of failed deployments
- Increments when deployment to at least one instance fails

**Example Queries:**

```promql
# Deployment rate
rate(haptic_deployment_total[5m])

# 95th percentile deployment latency
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))

# Failed deployment rate
rate(haptic_deployment_errors_total[5m])
```

### Validation Metrics

Track configuration validation performance.

**haptic_validation_total** (counter)

- Total number of validation attempts
- Increments for both successful and failed validations

**haptic_validation_errors_total** (counter)

- Total number of failed validations
- Increments when configuration has syntax errors or validation warnings

**Example Queries:**

```promql
# Validation rate
rate(haptic_validation_total[5m])

# Validation error rate
rate(haptic_validation_errors_total[5m])

# Validation success rate
100 * (1 - (
  rate(haptic_validation_errors_total[5m]) /
  rate(haptic_validation_total[5m])
))
```

### Resource Metrics

Track Kubernetes resources being watched.

**haptic_resource_count** (gauge with `type` label)

- Current number of resources indexed by type
- Labels: `type` (e.g., "ingresses", "services", "endpoints", "haproxy-pods")
- Updates on every index change

**Example Queries:**

```promql
# Current resource counts
haptic_resource_count

# Ingress count
haptic_resource_count{type="ingresses"}

# Resource count over time
haptic_resource_count{type="services"}[1h]
```

### Event Metrics

Track event bus activity.

**haptic_event_subscribers** (gauge)

- Current number of active event subscribers
- Reflects component health (subscribers should remain constant)

**haptic_events_published_total** (counter)

- Total number of events published to the event bus
- Indicates overall controller activity level

**Example Queries:**

```promql
# Event publishing rate
rate(haptic_events_published_total[5m])

# Current subscribers (should be constant)
haptic_event_subscribers

# Subscriber changes (indicator of component restarts)
delta(haptic_event_subscribers[5m])
```

### Leader Election Metrics

Track leadership status and transitions for high availability deployments.

**haptic_leader_election_is_leader** (gauge)

- Indicates if this replica is currently the leader
- Values: 1 (leader), 0 (follower)
- Only one replica should report 1 across all controller instances

**haptic_leader_election_transitions_total** (counter)

- Total number of leadership transitions (becoming leader or losing leadership)
- Increments on both gain and loss of leadership
- Frequent transitions may indicate cluster instability

**haptic_leader_election_time_as_leader_seconds_total** (counter)

- Cumulative time this replica has spent as leader (in seconds)
- Updates when losing leadership
- Useful for understanding leadership distribution

**Example Queries:**

```promql
# Current leader count (should be 1 across all replicas)
sum(haptic_leader_election_is_leader)

# Leadership transition rate
rate(haptic_leader_election_transitions_total[1h])

# Average time as leader per transition
haptic_leader_election_time_as_leader_seconds_total /
haptic_leader_election_transitions_total

# Identify current leader pod
haptic_leader_election_is_leader{pod=~".*"} == 1

# Alert on split-brain (multiple leaders)
sum(haptic_leader_election_is_leader) > 1

# Alert on no leader
sum(haptic_leader_election_is_leader) < 1

# Alert on frequent leadership changes (> 5 per hour)
rate(haptic_leader_election_transitions_total[1h]) > 5
```

**Operational Notes:**

- In single-replica deployments (leader election disabled), metrics still exist
- Normal failover causes 1 transition (old leader loses, new leader gains)
- High transition rates may indicate: clock skew, network issues, or resource contention
- Leadership distribution should be relatively balanced over time

## Component Architecture

### Metrics Struct

Holds all controller-specific Prometheus metrics:

```go
type Metrics struct {
    ReconciliationDuration prometheus.Histogram
    ReconciliationTotal    prometheus.Counter
    ReconciliationErrors   prometheus.Counter
    DeploymentDuration     prometheus.Histogram
    DeploymentTotal        prometheus.Counter
    DeploymentErrors       prometheus.Counter
    ValidationTotal        prometheus.Counter
    ValidationErrors       prometheus.Counter
    ResourceCount          *prometheus.GaugeVec  // type label
    EventSubscribers       prometheus.Gauge
    EventsPublished        prometheus.Counter
    LeaderElectionIsLeader       prometheus.Gauge
    LeaderElectionTransitionsTotal prometheus.Counter
    LeaderElectionTimeAsLeaderSeconds prometheus.Counter
}
```

### Component (Event Adapter)

Subscribes to controller events and updates metrics accordingly:

```go
type Component struct {
    metrics        *Metrics
    eventBus       *events.EventBus
    eventChan      <-chan events.Event
    resourceCounts map[string]int  // Track resource counts
}
```

**Lifecycle:**

1. Create component: `NewComponent(metrics, eventBus)`
2. Subscribe to events: `component.Start()`
3. Start event loop: `go component.Run(ctx)`
4. Stop on context cancellation

## Usage

### Basic Setup

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "haptic/pkg/controller/metrics"
    "haptic/pkg/events"
)

// Create instance-based registry
registry := prometheus.NewRegistry()

// Create controller metrics
domainMetrics := metrics.NewMetrics(registry)

// Create event bus
bus := events.NewEventBus(100)

// Create metrics component (event adapter)
metricsComponent := metrics.New(domainMetrics, bus)

// Subscribe before starting event bus (prevents race)
metricsComponent.Start()

// Start event bus
bus.Start()

// Start metrics component event loop
go metricsComponent.Run(ctx)
```

### Direct Metric Updates

You can also update metrics directly (without events):

```go
// Record reconciliation
metrics.RecordReconciliation(durationMs, success)

// Record deployment
metrics.RecordDeployment(durationMs, success)

// Record validation
metrics.RecordValidation(success)

// Update resource count
metrics.SetResourceCount("ingresses", 42)

// Update event subscribers
metrics.SetEventSubscribers(10)

// Record event published
metrics.RecordEvent()
```

### Event-Driven Updates

The component automatically updates metrics based on these events:

**Reconciliation Events:**

- `ReconciliationCompletedEvent` → Increments total, records duration
- `ReconciliationFailedEvent` → Increments total and errors

**Deployment Events:**

- `DeploymentCompletedEvent` → Increments total, records duration
- `InstanceDeploymentFailedEvent` → Increments total and errors

**Validation Events:**

- `ValidationCompletedEvent` → Increments total (success)
- `ValidationFailedEvent` → Increments total and errors

**Resource Events:**

- `IndexSynchronizedEvent` → Initializes resource counts
- `ResourceIndexUpdatedEvent` → Updates resource counts incrementally

**Leader Election Events:**

- `BecameLeaderEvent` → Sets is_leader to 1, increments transitions, starts time tracking
- `LostLeadershipEvent` → Sets is_leader to 0, increments transitions, records time as leader

## Testing

### Metrics Tests

Test metric creation and updates:

```go
func TestMetrics_RecordReconciliation(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := New(registry)

    // Record successful reconciliation
    metrics.RecordReconciliation(1500, true)

    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationTotal))
    assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ReconciliationErrors))
}
```

### Component Tests

Test event-driven metric updates:

```go
func TestComponent_ReconciliationEvents(t *testing.T) {
    registry := prometheus.NewRegistry()
    metrics := New(registry)
    eventBus := events.NewEventBus(100)

    component := NewComponent(metrics, eventBus)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    component.Start()
    eventBus.Start()
    go component.Run(ctx)

    // Publish event
    eventBus.Publish(events.NewReconciliationCompletedEvent(1500))

    time.Sleep(100 * time.Millisecond)

    // Verify metrics updated
    assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationTotal))
}
```

## Alerting Examples

### Prometheus Alerts

```yaml
groups:
  - name: haptic
    rules:
      - alert: HighReconciliationErrorRate
        expr: |
          rate(haptic_reconciliation_errors_total[5m]) /
          rate(haptic_reconciliation_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High reconciliation error rate (>10%)"

      - alert: HighDeploymentLatency
        expr: |
          histogram_quantile(0.95,
            rate(haptic_deployment_duration_seconds_bucket[5m])
          ) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "95th percentile deployment latency >5s"

      - alert: ValidationFailures
        expr: |
          rate(haptic_validation_errors_total[5m]) > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Configuration validation failing"

      - alert: ComponentStopped
        expr: |
          delta(haptic_event_subscribers[5m]) < 0
        labels:
          severity: critical
        annotations:
          summary: "Event subscriber count decreased (component crash?)"
```

## Dashboard Examples

### Grafana Queries

**Reconciliation Performance:**

```promql
# Reconciliation rate
rate(haptic_reconciliation_total[5m])

# Success rate
100 * (1 - (
  rate(haptic_reconciliation_errors_total[5m]) /
  rate(haptic_reconciliation_total[5m])
))

# P50, P95, P99 latencies
histogram_quantile(0.50, rate(haptic_reconciliation_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(haptic_reconciliation_duration_seconds_bucket[5m]))
histogram_quantile(0.99, rate(haptic_reconciliation_duration_seconds_bucket[5m]))
```

**Deployment Performance:**

```promql
# Deployment rate
rate(haptic_deployment_total[5m])

# Average deployment duration
rate(haptic_deployment_duration_seconds_sum[5m]) /
rate(haptic_deployment_duration_seconds_count[5m])

# Deployment success rate
100 * (1 - (
  rate(haptic_deployment_errors_total[5m]) /
  rate(haptic_deployment_total[5m])
))
```

**Resource Tracking:**

```promql
# All resource counts
haptic_resource_count

# Ingress count
haptic_resource_count{type="ingresses"}

# HAProxy pod count
haptic_resource_count{type="haproxy-pods"}
```

## Best Practices

### DO

- ✅ Record duration for all async operations
- ✅ Increment error counters for all failure cases
- ✅ Update resource counts on every index change
- ✅ Keep metrics simple and focused
- ✅ Use histogram for latency, counter for totals

### DON'T

- ❌ Create metrics with unbounded labels (e.g., pod names)
- ❌ Skip error tracking (every failure should increment error counter)
- ❌ Use gauges for cumulative values (use counters instead)
- ❌ Update metrics manually in business logic (use events)

## Architecture Integration

This package integrates with the controller architecture:

- **Pure metrics** (metrics.go) - No event dependencies
- **Event adapter** (component.go) - Bridges events to metrics
- **Controller orchestration** (pkg/controller) - Wires everything together

## Resources

- Development context: `pkg/controller/metrics/CLAUDE.md`
- Generic metrics utilities: `pkg/metrics/README.md`
- Controller events: `pkg/controller/events/types.go`
- Prometheus documentation: <https://prometheus.io/docs/>
