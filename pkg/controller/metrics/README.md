# pkg/controller/metrics

Domain metrics for the HAProxy Template Ingress Controller. Two things live here:

- A `Metrics` struct that owns every controller-defined Prometheus metric (instance-based `prometheus.Registry`, not the global default).
- A `Component` event adapter that subscribes to controller events and updates metrics accordingly.

User-facing queries, alerting rules, and dashboard templates live in [`docs/controller/docs/operations/monitoring.md`](../../../docs/controller/docs/operations/monitoring.md). This README is the authoritative developer reference for *which* metrics exist and which component owns them.

## Complete Metric Catalogue

All names are listed exactly as exported. `metrics.go` contains the authoritative list; the `TestMetrics_AllMetricsRegistered` assertion in `metrics_test.go` covers a representative subset (~12 of 32) — extend that slice when you add or rename a metric.

### Reconciliation pipeline

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_reconciliation_total` | counter | — | Reconciliation cycles triggered |
| `haptic_reconciliation_errors_total` | counter | — | Reconciliations that failed |
| `haptic_reconciliation_duration_seconds` | histogram | — | End-to-end reconciliation wall-clock |
| `haptic_reconciliation_queue_wait_seconds` | histogram | — | Time between `ReconciliationTriggeredEvent` and the pipeline actually picking it up (debounce + queue depth) |

### Deployment

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_deployment_total` | counter | — | Deployments dispatched to at least one HAProxy endpoint |
| `haptic_deployment_errors_total` | counter | — | Deployments that failed |
| `haptic_deployment_duration_seconds` | histogram | — | Deployment duration, aggregated across all parallel endpoint calls |
| `haptic_haproxy_reloads_total` | counter | — | HAProxy reloads triggered by deployments. A reload forks the HAProxy process; reload rate (vs runtime-API updates) is the key capacity/SLO signal |
| `haptic_dataplane_api_operations_total` | counter | — | DataPlane API operations issued across deployments (structural changes applied to HAProxy) |

### Runtime-eligible fast path

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_runtime_fast_path_fires_total` | counter | — | Fast-path attempts (one per HAProxy pod per reconcile) |
| `haptic_runtime_fast_path_applies_total` | counter | — | Attempts that applied ≥1 runtime-eligible server update |
| `haptic_runtime_fast_path_failures_total` | counter | — | Attempts that errored (best-effort; the scheduled deploy converges) |
| `haptic_runtime_fast_path_server_updates_total` | counter | — | Runtime-eligible server updates applied via the fast path |

`applies_total` flat at 0 while `fires_total` climbs means the fast path runs but finds no runtime-eligible change to apply (the deploy is keeping pods current) — the signal that separates a healthy-but-idle fast path from a broken one.

### Config validation

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_validation_total` | counter | — | Controller-side validations (`haproxy -c` + parser) |
| `haptic_validation_errors_total` | counter | — | Controller-side validation failures |

### Watched resources

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_resource_count` | gauge | `type` | Current size of each watched-resource store (including `haproxy-pods`) |

### HAProxy pod discovery

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_haproxy_pods_rejected_total` | counter | `reason` | HAProxy pods refused admission by the discovery component. Persistent non-zero growth typically means the controller cannot talk to the deployed HAProxy pods (e.g. bundled HAProxy major.minor differs from the chart's `haproxyVersion`). |

### Event bus

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_event_subscribers` | gauge | — | Live subscribers on the `EventBus`. Drops during normal ops usually indicate a crash |
| `haptic_events_published_total` | counter | — | Total publishes |
| `haptic_events_dropped_total` | counter | — | Publishes where the subscriber's channel was full |
| `haptic_events_dropped_critical_total` | counter | — | Drops where the buffered event was marked critical |
| `haptic_events_dropped_by_subscriber_total` | counter | `subscriber`, `event_type` | Drops attributed to each subscriber/event-type pair (the second label lets dashboards split by which event type the subscriber couldn't keep up with) |
| `haptic_events_dropped_observability_total` | gauge | — | Drops to the observability subscribers (commentator, debug buffer); expected to be low but non-zero on bursts |

### Webhook

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_webhook_requests_total` | counter | `gvk`, `result` | Admission requests by kind and allow/deny/error |
| `haptic_webhook_request_duration_seconds` | histogram | `gvk` | Per-request wall-clock |
| `haptic_webhook_validation_total` | counter | `gvk`, `result` | Validation-only tally (no timing) — handy for ratios |

### Leader election

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_leader_election_is_leader` | gauge | — | `1` if this replica holds the lease, else `0` |
| `haptic_leader_election_transitions_total` | counter | — | Leadership changes observed |
| `haptic_leader_election_time_as_leader_seconds_total` | counter | — | Cumulative seconds spent as leader |

### Dataplane parser cache

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_parser_cache_hits_total` | counter | — | client-native parser result cache hits |
| `haptic_parser_cache_misses_total` | counter | — | client-native parser result cache misses |

### Build info

| Metric | Type | Labels | What it tracks |
|--------|------|--------|----------------|
| `haptic_build_info` | gauge (always 1) | `version`, `haproxy_version`, `go_version` | Static metadata for dashboards |

## Component

`Component` subscribes to reconciliation, deployment, validation, resource, event-bus, webhook, leader-election, and parser events. Metric updates happen inside the internal `handleEvent` dispatcher — there's no direct caller path into the `Metrics` struct from other components; they emit events and this component records them.

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
    pkgmetrics "gitlab.com/haproxy-haptic/haptic/pkg/metrics"

    "github.com/prometheus/client_golang/prometheus"
)

registry := prometheus.NewRegistry()          // instance-based, swapped per controller iteration
m := metrics.NewMetrics(registry)              // single arg — Registerer only
m.SetBuildInfo(version, haproxyVersion, goVersion) // populate the haptic_build_info gauge

comp := metrics.New(m, bus)                    // (*Metrics, *EventBus) — subscribes during construction
go comp.Start(ctx)                             // runs the event loop

// Serve on the port pkg/metrics exposes
pkgmetrics.NewServer(":9090", registry).Start(ctx)
```

The `Metrics` struct is intentionally safe to use stand-alone (CLI validation, tests) without the `Component` — call the typed update methods directly when you already have a value.

### Why an instance-based registry?

The controller's reinitialisation loop creates a fresh `EventBus` on every config change. Using the global default registry would leak prior-iteration collectors into the new one and produce duplicate-registration panics. `NewMetrics` always takes a caller-provided registry, which the controller swaps per iteration; Prometheus sees a clean slate with the same metric names each time.

### Resource-count tracking

`ResourceCount` is a gauge with a `type` label. The component seeds it from `IndexSynchronizedEvent` (absolute counts per resource type) and then applies deltas from `ResourceIndexUpdatedEvent` (created − deleted), skipping events marked `IsInitialSync`. The running totals live in a `map[string]int` on the component struct, so gauge values match reality across churn without re-listing from the API server.

## Dropping or Renaming a Metric

- The `TestMetrics_AllMetricsRegistered` assertion covers a representative subset of the exported names. Update that slice when you add, rename, or remove one — and ideally extend it so every name is guarded.
- Dashboards and alert rules in `docs/controller/docs/operations/monitoring.md` reference names too; keep that file in sync or link the dashboard PR to the metric PR.

## Testing

```bash
go test ./pkg/controller/metrics/...          # unit tests
go test ./pkg/controller/metrics/... -race    # race detector
```

`component_test.go` asserts that publishing each event type produces exactly the expected metric update — no surprise side-effects, no accidental double counting.

## See Also

- [`pkg/metrics`](../../metrics/) — generic HTTP `/metrics` server + `NewCounter`/`NewHistogram`/`NewGauge` helpers
- [`pkg/controller/events`](../events/) — event catalogue; this package subscribes to nearly all of it
- [`docs/controller/docs/operations/monitoring.md`](../../../docs/controller/docs/operations/monitoring.md) — user-facing queries, alerts, dashboards

## License

Apache-2.0 — see root `LICENSE`.
