# Monitoring

Monitor HAPTIC with Prometheus metrics: setup, the metrics reference, alerting rules, and dashboards.

## Overview

The controller's `haptic_*` metrics cover:

- Reconciliation cycle performance and errors
- HAProxy deployment latency and success rates
- Configuration validation status
- Kubernetes resource counts
- Leader election for HA deployments

!!! note "Two metric sources"
    Most of this guide is about the **controller's** metrics — the `haptic_*` family on port `9090`, which describe reconciliation, deployment, and leader-election health. HAProxy itself exposes a *separate* Prometheus endpoint on port `8404` carrying live traffic, backend health, and response-code data — see [HAProxy Data-Plane Metrics](#haproxy-data-plane-metrics). The chart's bundled `ServiceMonitor`/`PodMonitor` scrape the controller only.

## Enabling metrics

Metrics are enabled by default. The controller serves Prometheus metrics at `/metrics` on the metrics port (default `:9090`), which is separate from the debug port. No additional configuration is needed beyond pointing Prometheus at this endpoint.

The chart sets the controller process, container port, Service, and monitors from
one value. To disable the metrics server, set `controller.ports.metrics: 0`:

```yaml
# values.yaml — disable the metrics server and monitoring resources
controller:
  ports:
    metrics: 0
```

`controller.ports.metrics=0` can't be combined with an enabled ServiceMonitor,
PodMonitor, or PrometheusRule because those resources would target a listener
that doesn't exist. The chart rejects that combination.

## Accessing metrics

### Prometheus scrape configuration

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

### ServiceMonitor (Prometheus operator)

If using Prometheus Operator, enable the ServiceMonitor in Helm:

```yaml
# values.yaml
controller:
  monitoring:
    serviceMonitor:
      enabled: true
      interval: 30s
      labels:
        release: prometheus  # Match your Prometheus selector
```

The chart also ships a `PodMonitor` (`controller.monitoring.podMonitor.enabled`) for setups that scrape pods directly instead of via the Service — enable whichever your Prometheus setup uses.

Add custom labels, a scrape timeout, `relabelings`, or `metricRelabelings` for larger setups:

```yaml
# values.yaml
controller:
  monitoring:
    serviceMonitor:
      enabled: true
      interval: 15s
      scrapeTimeout: 10s
      labels:
        release: prometheus
        team: platform
      # Stamp a cluster label onto every scraped series
      relabelings:
        - sourceLabels: [__address__]
          targetLabel: cluster
          replacement: production
      # Drop a metric you don't want to store
      metricRelabelings:
        - sourceLabels: [__name__]
          regex: 'haptic_event_subscribers'
          action: drop
```

If a NetworkPolicy is in effect, also allow Prometheus to reach the metrics port — see [Networking](./networking.md).

### Manual access

```bash
# Port-forward to metrics endpoint
kubectl port-forward -n haptic deployment/haptic-controller 9090:9090

# Fetch metrics
curl http://localhost:9090/metrics
```

### Other scrapers

Victoria Metrics accepts the same Prometheus scrape configuration shown above. For Datadog, configure the Datadog Agent to scrape Prometheus metrics:

```yaml
# datadog-agent values
datadog:
  prometheusScrape:
    enabled: true
    serviceEndpoints: true
```

## Metrics reference

### Reconciliation metrics

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

### Deployment metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_deployment_total` | Counter | Total deployment attempts |
| `haptic_deployment_duration_seconds` | Histogram | Time spent deploying to HAProxy |
| `haptic_deployment_errors_total` | Counter | Failed deployments |
| `haptic_haproxy_reloads_total` | Counter | HAProxy reloads triggered by deployments. A reload forks the HAProxy process; reload rate (vs runtime-API updates) is the canonical capacity and Service Level Objective (SLO) signal |
| `haptic_dataplane_api_operations_total` | Counter | DataPlane API operations issued across deployments (structural changes applied to HAProxy) |

**Key queries:**

```promql
# Deployment rate
rate(haptic_deployment_total[5m])

# HAProxy reload rate — the capacity/SLO signal (a reload forks the process)
rate(haptic_haproxy_reloads_total[5m])

# Share of deployments that needed a reload vs runtime-only updates
rate(haptic_haproxy_reloads_total[5m]) / rate(haptic_deployment_total[5m])

# 95th percentile deployment latency
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))

# Deployment success rate
100 * (1 - (
  rate(haptic_deployment_errors_total[5m]) /
  rate(haptic_deployment_total[5m])
))
```

### Fleet convergence & config staleness

These gauges answer "did your change reach every HAProxy pod, and if not, for how long?" They're the noise-free replacement for alerting on `rate(haptic_deployment_errors_total)`: the deploy scheduler self-heals transient failures, so a nonzero error rate no longer means the fleet is actually broken. These gauges report the *converged state*, not the *attempt outcome*.

They're populated **leader-only** — only the leader deploys. Followers reset them when they lose leadership, so `haptic_haproxy_fleet_converged < haptic_haproxy_fleet_size` is `0 < 0` (false) on followers and never false-alerts.

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_haproxy_fleet_size` | Gauge | HAProxy pods the last deployment targeted |
| `haptic_haproxy_fleet_converged` | Gauge | HAProxy pods now at the desired config. Alert on `haptic_haproxy_fleet_converged < haptic_haproxy_fleet_size` |
| `haptic_last_full_sync_timestamp_seconds` | Gauge | Unix timestamp (seconds) of the last time the whole fleet converged. Seeded to controller start time, so before the first full sync staleness reads as uptime rather than the whole epoch. In steady state (no config or pod changes) it advances with the periodic drift-prevention deploy, so any staleness threshold you alert on must exceed `spec.dataplane.driftPreventionInterval` (default `60s`) |
| `haptic_deployment_consecutive_failures` | Gauge | Consecutive deployments that didn't fully converge the fleet; resets to 0 on the first full sync |

**Key queries:**

```promql
# Pods not yet at the desired config right now (0 = fully converged)
haptic_haproxy_fleet_size - haptic_haproxy_fleet_converged

# How long since the whole fleet last converged (config staleness, seconds)
time() - haptic_last_full_sync_timestamp_seconds

# Deploys that failed to fully converge, back to back — alert on this instead of
# the error counter, now that transient deploy failures self-heal
haptic_deployment_consecutive_failures
```

The bundled `HAProxyFleetDiverged` alert (see [Alerting Rules](#alerting-rules)) fires when pods stay behind the desired config — the robust, cadence-independent signal. A staleness alert on `time() - haptic_last_full_sync_timestamp_seconds` is left to you: in steady state that value tracks the drift-prevention cadence, so a safe threshold depends on your configured `driftPreventionInterval` (a fixed default would false-fire for operators who raise it).

### Runtime fast-path metrics

The runtime fast path applies runtime-eligible server changes (weight, address, port, admin state) directly to the running HAProxy worker via the Dataplane API, bypassing a config reload. `applies` stuck at 0 while `fires` climbs means the fast path runs but the render diff never carries a runtime-eligible change.

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_runtime_fast_path_fires_total` | Counter | Runtime-eligible fast-path apply attempts (one per pod per reconcile) |
| `haptic_runtime_fast_path_applies_total` | Counter | Fast-path attempts that applied at least one runtime-eligible server update |
| `haptic_runtime_fast_path_failures_total` | Counter | Fast-path attempts that errored (best-effort; the scheduled deploy converges) |
| `haptic_runtime_fast_path_server_updates_total` | Counter | Total runtime-eligible server updates applied via the fast path |

Two more counters record when the reload-free lane was *lost* — each increment is a sync that fell back to a reload or needed a retry:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_runtime_map_divergence_total` | Counter | `map` | Runtime maps whose post-apply read-back disagreed with the desired content, forcing a reload fallback. The `map` label names the file, so a single map dominating the rate points at the template that builds it. |
| `haptic_deploy_runtime_divergence_total` | Counter | — | Endpoints whose post-reload read-back found the on-disk config structurally diverged from the pushed body — a concurrent writer clobbered a just-activated config. The fast deploy retry self-heals it; sustained growth means something outside HAPTIC is writing to the same HAProxy. |

**Key queries:**

```promql
# Fraction of fast-path attempts that carried a runtime-eligible change
rate(haptic_runtime_fast_path_applies_total[5m]) /
rate(haptic_runtime_fast_path_fires_total[5m])

# Runtime server updates applied without a reload
rate(haptic_runtime_fast_path_server_updates_total[5m])

# Maps that lost the reload-free lane, worst first
topk(5, sum by (map) (rate(haptic_runtime_map_divergence_total[5m])))
```

### Validation metrics

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

### Resource metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_resource_count` | Gauge | `type` | Current count of watched resources |
| `haptic_haproxy_pods_rejected_total` | Counter | `reason` | HAProxy pods refused admission by the discovery component. Persistent non-zero growth typically means the controller can't talk to the deployed HAProxy pods (for example, the bundled HAProxy major.minor differs from the chart's `haproxyVersion`). |
| `haptic_config_rejected_total` | Counter | `validator` | `HAProxyTemplateConfig` loads refused by the config-validation gate. The `validator` label names which check rejected it (`basic`, `template`, `jsonpath`, `validationtests`, or `coordinator` when a validator timed out). Non-zero growth means the leader is refusing new config and continuing on the last-good one — **alert on it**: the operator's latest change isn't live. |

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

### Event metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_event_subscribers` | Gauge | Active event subscribers |
| `haptic_events_published_total` | Counter | Events seen by the metrics component. It subscribes with a typed filter (17 event types), so this isn't the bus-wide publish count — event types outside that filter are never counted |

**Key queries:**

```promql
# Event publishing rate
rate(haptic_events_published_total[5m])

# Subscriber count (should be constant)
haptic_event_subscribers

# Subscriber changes (indicates component restarts)
delta(haptic_event_subscribers[5m])
```

### Leader election metrics

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

### Webhook metrics

Exposed when the validating admission webhook is enabled (`controller.webhook.enabled=true`).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_webhook_requests_total` | Counter | `gvk`, `result` | Total admission requests by GroupVersionKind and result |
| `haptic_webhook_request_duration_seconds` | Histogram | — | Time spent processing webhook requests |
| `haptic_webhook_validation_total` | Counter | `gvk`, `result` | Validation outcomes per GVK. `result` is `allowed`, `denied`, or `unregistered`. An unregistered request is denied with status 503; growth of the fixed `<unregistered>` series means a webhook rule and the installed validators disagree. |

**Key queries:**

```promql
# Denial rate per resource kind
sum by (gvk) (rate(haptic_webhook_validation_total{result="denied"}[5m]))

# 95th percentile webhook latency (must stay well under the 10s admission timeout)
histogram_quantile(0.95, rate(haptic_webhook_request_duration_seconds_bucket[5m]))
```

### Reconciliation queue

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_reconciliation_queue_wait_seconds` | Histogram | Time a triggered reconciliation waits in the coordinator queue before processing starts; rising values indicate the controller can't keep up with change volume |

### Event bus backpressure

These complement `haptic_events_published_total` / `haptic_event_subscribers` from above.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_events_dropped_total` | Counter | — | Drops from **critical** subscribers, because only those fire the bus's drop callback. It therefore tracks `haptic_events_dropped_critical_total` exactly and doesn't include observability drops |
| `haptic_events_dropped_critical_total` | Counter | — | Drops from critical subscribers; totals survive iteration reconstruction so alerts can observe the failure |
| `haptic_events_dropped_observability_total` | Gauge | — | Drops from observability-only subscribers (expected under load, non-alerting) |
| `haptic_events_dropped_by_subscriber_total` | Counter | `subscriber`, `event_type` | Per-subscriber drop counts for diagnosing which component is falling behind |

### Build info

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_build_info` | Gauge | `version`, `haproxy_version`, `go_version` | Always `1`; useful for joining build metadata into other queries |

```promql
# Pin a query to controller version 0.1.0
haptic_reconciliation_total * on() group_left(version) haptic_build_info{version="0.1.0"}
```

## HAProxy data-plane metrics

Every metric above comes from the **controller** (`haptic_*`, port `9090`) — they describe reconciliation, deployment, and leader-election health, not live traffic. HAProxy itself exposes a separate Prometheus endpoint carrying the data-plane signals operators usually watch most closely: per-frontend request rates, per-backend response-code breakdowns, and session counts.

The bundled config enables HAProxy's built-in [Prometheus exporter](https://github.com/haproxy/haproxy/tree/master/addons/promex) on the status frontend (port `8404`, path `/metrics`) by default — it's served from the always-on `status-extra-100-prometheus-exporter` snippet, so no extra flag is required.

!!! warning "The chart's ServiceMonitor and PodMonitor don't scrape HAProxy"
    Both bundled monitors collect the controller's `haptic_*` metrics only: the `PodMonitor` selects `app.kubernetes.io/component: controller`, and the `ServiceMonitor` scrapes the `metrics` port (`9090`) that only the controller Service exposes. Neither targets an HAProxy pod. Use the Vector `PodMonitor` below, or add your own scrape.

### Where to scrape

Which endpoint carries `haproxy_*` depends on whether the [Vector sidecar](../haproxy-deployment.md#vector-sidecar) is running. It's enabled by default.

**With the sidecar (`vector.enabled: true`, the default).** Vector scrapes HAProxy over loopback and re-exports everything on its own ports, so Prometheus collects one target per pod instead of one per container. HAProxy's own `/metrics` answers **only** over `127.0.0.0/8` in this mode, so a scrape aimed at `8404` from outside the pod returns nothing.

Turn on the bundled `PodMonitor`:

```yaml
vector:
  podMonitor:
    enabled: true
```

It declares both endpoints: `vector-metrics` (`9598`) carrying `haproxy_*`, `spoa_*`, `vector_*` and the request counter and duration histograms, and `vector-sizes` (`9599`) carrying the byte-size histograms. The second endpoint exists only while a size family is enabled, and both use the same `interval`, `scrapeTimeout` and relabeling settings.

**Without the sidecar (`vector.enabled: false`).** Scrape HAProxy's exporter directly, either with a `ServiceMonitor` against the HAProxy Service's `stats` port:

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

or with a plain Prometheus job that keeps HAProxy pods and their `8404` container port:

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

### Key queries

HAProxy labels frontend and backend metrics with `proxy` (the section name), and server metrics with `server`:

```promql
# Request rate per frontend
sum by (proxy) (rate(haproxy_frontend_http_requests_total[5m]))

# Active sessions per backend
sum by (proxy) (haproxy_backend_current_sessions)

# Backends with no live endpoint
sum by (proxy) (haproxy_backend_active_servers) == 0
```

The full metric set is HAProxy's own, not HAPTIC's — see the [HAProxy Prometheus exporter reference](https://github.com/haproxy/haproxy/tree/master/addons/promex) for every exposed series and its labels.

!!! note "Some families are dropped before they reach Prometheus"
    With the sidecar on, `vector.excludeMetrics` drops HAProxy's since-boot maxima and its 1024-connection rolling averages, and — while [request metrics](#request-metrics) are enabled — `haproxy_backend_http_requests_total`, `haproxy_backend_http_responses_total` and `haproxy_frontend_http_responses_total`, which those metrics supersede with an exact status code and far more dimensions. `haproxy_server_http_responses_total` is kept: it's per-server, and the request metrics carry no server dimension. See [Chart values reference](../reference.md#vector-sidecar) to turn any of it back on.

## Request metrics

These are the rate, errors, and duration signals derived from the access log and dimensioned by **route** rather than by request URI. They answer questions the `haproxy_*` families can't: which Ingress is slow, which path returns 502 responses, whether latency is the backend or the network.

They're on whenever the Vector sidecar is, and are named `haptic_ingress_controller_*` by default. The names, label set and semantics deliberately match `ingress-nginx`, so its dashboards, recording rules and alerts work against them — see [Migrating](../migrating.md#metrics) for a drop-in configuration.

### Families

| Metric | Type | Measures | Endpoint |
|--------|------|----------|----------|
| `haptic_ingress_controller_requests` | counter | One per logged request | `9598` |
| `haptic_ingress_controller_request_duration_seconds` | histogram | Total active time — what the client experienced (`%Ta`) | `9598` |
| `haptic_ingress_controller_response_duration_seconds` | histogram | The whole upstream call: connect, headers, and body transfer | `9598` |
| `haptic_ingress_controller_connect_duration_seconds` | histogram | Establishing the backend connection (`%Tc`) | `9598` |
| `haptic_ingress_controller_header_duration_seconds` | histogram | Waiting for the upstream's response headers (`%Tr`) | `9598` |
| `haptic_ingress_controller_request_size` | histogram | Request body bytes from the client (`%U`) | `9599` |
| `haptic_ingress_controller_response_size` | histogram | Bytes returned to the client (`%B`) | `9599` |

Splitting the upstream call into three timers is what makes these worth more than a single latency histogram. A rise in `connect_duration` is a saturated or unhealthy backend; a rise in `header_duration` while connect stays flat is the application itself; a rise in `request_duration` while both stay flat is the client or the network.

**The upstream timers are only recorded when the phase happened.** A request HAProxy answered itself — a deny, a redirect, a 503 with no live endpoint — increments `requests` and `request_duration_seconds` and contributes to neither `connect_duration_seconds` nor `header_duration_seconds`. Recording a zero there would report that the backend answered instantly on a request that never reached one. Look at `term` instead.

### Labels

Every family carries the same set:

| Label | Value |
|-------|-------|
| `status` | HTTP status code, exact |
| `method` | Request method |
| `path` | The matched **route** — the path template you wrote, not the request URI |
| `namespace`, `ingress` | The routing resource that owns the route; both empty when HAProxy answered the request itself |
| `service` | The Kubernetes Service behind the chosen backend |
| `host` | Request host |
| `term` | HAProxy's 4-character termination state |
| `controller_class`, `controller_namespace`, `controller_pod` | Which HAPTIC served it |

`term` is the one label `ingress-nginx` has no equivalent of, and it's usually the fastest route from "5% of requests are failing" to a cause:

| Value | Meaning |
|-------|---------|
| `----` | Normal completion |
| `SC--` | The backend refused or failed the connection |
| `sH--` | The backend accepted the connection, then never sent response headers — a server timeout |
| `sQ--` | The request timed out waiting in the queue, before any backend was picked |
| `cD--` | The client stopped reading mid-transfer |
| `PR--` | HAProxy rejected the request itself, before routing |

The full list is in HAProxy's [session state at disconnection](https://docs.haproxy.org/3.0/configuration.html#8.5) reference.

```promql
# Error rate per Ingress
sum by (namespace, ingress) (rate(haptic_ingress_controller_requests{status=~"5.."}[5m]))

# p99 latency per route
histogram_quantile(0.99, sum by (le, namespace, ingress, path) (
  rate(haptic_ingress_controller_request_duration_seconds_bucket[5m])))

# Is it the backend, or the app? Compare connect against header time.
histogram_quantile(0.95, sum by (le) (rate(haptic_ingress_controller_connect_duration_seconds_bucket[5m])))
histogram_quantile(0.95, sum by (le) (rate(haptic_ingress_controller_header_duration_seconds_bucket[5m])))

# Backends timing out or refusing connections
sum by (namespace, ingress, service, term) (
  rate(haptic_ingress_controller_requests{term=~"sH..|SC..|sQ.."}[5m])) > 0

# Bandwidth per Ingress
sum by (namespace, ingress) (rate(haptic_ingress_controller_response_size_sum[5m]))
```

### Controlling cardinality

Series per pod is roughly `routes × statuses × methods × hosts × terminations`, once per family, and the six histograms multiply that again by their bucket count. That dimensionality is the point, but it has a price. The levers, cheapest first:

```yaml
vector:
  requestMetrics:
    # Each removes a label from ALL families, so the remaining series aggregate
    # exactly as they would have without it.
    terminationStateLabel: false   # `term` — the biggest saving, it multiplies the histograms too
    pathLabel: false               # also switches off the HAProxy-side route lookup, saving per-request work
    hostLabel: false               # the equivalent of ingress-nginx's --metrics-per-host

    # Or drop whole families. The four durations are independent of each other.
    metrics:
      connect_duration_seconds: false
      header_duration_seconds: false
      request_size: false
      response_size: false
```

Bucket boundaries are the other multiplier — `durationBuckets` and `sizeBuckets` in [Chart values reference](../reference.md#vector-sidecar).

**A backstop runs by default.** `requestMetrics.cardinalityLimit` caps how many distinct values any one label may take, at 500 per metric. Past that, the offending label is dropped from new series and they collapse onto one — request totals stay correct, and only that dimension is lost. It protects against a label going unbounded despite the design: a route matched by regex, a Host header an attacker controls, a path template with an id in it. The state is in memory and resets when the sidecar restarts, so treat a tripped limit as something to fix rather than a solution.

!!! warning "The access log is lossy under back-pressure"
    These metrics are counted from access-log records, not in the data path, so they report fewer requests than were served whenever HAProxy drops records — see [The access log is lossy under back-pressure](../haproxy-deployment.md#the-access-log-is-lossy-under-back-pressure). Keep the `HAProxyAccessLogRecordsDropped` alert on. If you need a request count that stays exact through a drop, set `vector.excludeMetrics.httpRequestCounters.enabled: false` to keep HAProxy's own counters alongside these.

A route that receives no requests for over a minute drops out of the exposition and its counter restarts from zero when traffic returns. `rate()` and `increase()` handle the reset, and it keeps idle routes from accumulating series.

## Alerting rules

If you deploy via the Helm chart, it ships a built-in `PrometheusRule` (enable with `controller.monitoring.prometheusRule.enabled`) covering the ten alerts in [Shipped alerts](#shipped-alerts) below — nine on controller `haptic_*` metrics plus one on HAProxy's own access-log drop counter. The [Recommended alerts](#recommended-alerts) further down are a separate, broader example set you copy and adapt for any Prometheus setup — they're **not** what the chart deploys, and most use distinct `HAProxyIC*` names so you can run them alongside the shipped rules (`HAProxyFleetDiverged` is the one alert both sets define).

### Shipped alerts

The chart's `PrometheusRule` deploys these ten alerts when `controller.monitoring.prometheusRule.enabled: true`. Each is toggled by its own `controller.monitoring.prometheusRule.defaultRules.<key>` flag (all default to `true`):

| Alert | Toggle key (`defaultRules.<key>`) | Fires when |
|-------|-----------------------------------|------------|
| `HAProxyControllerReconciliationErrors` | `reconciliationErrors` | `rate(haptic_reconciliation_errors_total[5m]) > 0` for 5m |
| `HAProxyControllerDeploymentFailures` | `deploymentFailures` | `rate(haptic_deployment_errors_total[5m]) > 0` for 2m |
| `HAProxyFleetDiverged` | `fleetDiverged` | `haptic_haproxy_fleet_converged < haptic_haproxy_fleet_size` for 5m |
| `HAProxyControllerHighQueueDepth` | `highQueueDepth` | p95 `haptic_reconciliation_queue_wait_seconds` over `5s` for 5m |
| `HAProxyControllerNoLeader` | `leaderElectionLost` | `sum(haptic_leader_election_is_leader) == 0` for 1m |
| `HAProxyControllerConfigRejected` | `configRejected` | `increase(haptic_config_rejected_total[5m]) > 0` for 1m |
| `HAProxyControllerHAProxyPodsRejected` | `haproxyPodsRejected` | `increase(haptic_haproxy_pods_rejected_total[5m]) > 0` for 5m |
| `HAProxyControllerNoHAProxyPods` | `noHAProxyPods` | `haptic_resource_count{type="haproxy-pods"} < 1` for 5m |
| `HAProxyControllerCriticalEventsDropped` | `criticalEventsDropped` | `increase(haptic_events_dropped_critical_total[5m]) > 0` |
| `HAProxyAccessLogRecordsDropped` | `accessLogDropped` | `increase(haproxy_process_dropped_logs_total[5m]) > 0` |

Turn one rule off, or replace the whole set with your own:

```yaml
# values.yaml
controller:
  monitoring:
    prometheusRule:
      enabled: true
      defaultRules:
        highQueueDepth: false   # drop a single shipped rule; the other nine stay
      # Or set `rules:` to a non-empty list to replace ALL default rules with your own:
      # rules:
      #   - alert: MyCustomAlert
      #     expr: ...
```

The full names, toggle keys, and default thresholds also appear on the [Chart Values Reference](../reference.md#monitoring).

### Recommended alerts

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

      # Fleet diverged — some HAProxy pods are not at the desired config.
      # Prefer this over the deploy error counter: transient failures self-heal.
      - alert: HAProxyFleetDiverged
        expr: haptic_haproxy_fleet_converged < haptic_haproxy_fleet_size
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "HAProxy fleet is diverged"
          description: "Some HAProxy pods have not converged on the desired config for 5m"

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
          description: "A critical subscriber's buffer overflowed; the controller restarted its iteration to reconstruct state"
```

!!! note "Tuning alert thresholds"
    The thresholds above suit typical production environments. For high-churn environments (frequent deployments, many short-lived resources), increase the `for` duration on reconciliation and deployment alerts to avoid noise. For development clusters, consider relaxing error rate thresholds or disabling non-critical alerts entirely.

## Dashboard examples

The chart ships a complete built-in Grafana dashboard (29 panels) — enable it with `controller.monitoring.grafanaDashboard.enabled: true` (the default `useBuiltIn: true` renders `dashboards/haptic.json` into a `<release>-grafana-dashboard` ConfigMap that the Grafana sidecar auto-discovers; set a custom one via `grafanaDashboard.customDashboard`). The queries and JSON template below are for building your own dashboard or extending the bundled one.

### Grafana dashboard queries

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

### Dashboard JSON template

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

This is a starting point — add panels using the [PromQL queries](#grafana-dashboard-queries) above for more detailed views of deployment latency distribution, resource counts over time, or per-pod leader status.

## Operational Insights

### Key health indicators

| Indicator | Healthy Range | Action if Unhealthy |
|-----------|---------------|---------------------|
| Reconciliation success rate | >99% | Check logs for template/validation errors |
| Deployment success rate | >99% | Check HAProxy pod connectivity |
| P95 deployment latency | <`2s` | Check HAProxy DataPlane API performance |
| Leader count | Exactly 1 | Check HA configuration and network |
| Event subscribers | Shouldn't decrease during normal operation | Restart controller if dropping |

### Capacity planning

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

### Troubleshooting with metrics

**High reconciliation error rate:**

1. Check `haptic_validation_errors_total` - template/config issues
2. Check `haptic_deployment_errors_total` - HAProxy connectivity issues
3. Review controller logs for specific error messages

**Missing metrics:**

1. Verify the metrics server is enabled — `controller.ports.metrics` is non-zero (default `9090`), and the rendered controller container has the matching `METRICS_PORT` environment variable
2. Check ServiceMonitor selector matches Prometheus configuration
3. Verify network policies allow scraping

**Leader election issues:**

1. Check if `sum(haptic_leader_election_is_leader) != 1`
2. Review `rate(haptic_leader_election_transitions_total[1h])` for instability
3. See [High Availability Guide](./high-availability.md) for troubleshooting

## See also

- [Debugging Guide](./debugging.md) - Runtime introspection and troubleshooting
- [High Availability](./high-availability.md) - Leader election configuration
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
