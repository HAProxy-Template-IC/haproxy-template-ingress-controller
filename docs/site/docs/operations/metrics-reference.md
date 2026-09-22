# Controller and agent metrics

Use this reference to build queries and investigate configuration delivery. For
scraping, dashboards, and alerts, start with [monitoring setup](monitoring.md).
Scope queries to your installation when Prometheus monitors more than one release.

## Reconciliation metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_reconciliation_total` | Counter | Total reconciliation cycles triggered |
| `haptic_reconciliation_duration_seconds` | Histogram | Time spent in reconciliation cycles |
| `haptic_reconciliation_errors_total` | Counter | Failed reconciliation cycles |
| `haptic_render_profiles` | Gauge | Distinct backend profiles (`defaults haptic-be-*`) in the most recent render. Backends of the same shape collapse onto one profile, so this tracks the config's structural size independently of the raw backend count. Leader-only; `0` on followers |
| `haptic_render_warnings` | Gauge | Current template-recorded warnings, labeled by `reason`, from the last successful reconciliation. Only the leader reports them; resolved reasons disappear. Alert on `sum by (reason) (haptic_render_warnings) > 0` to detect degraded routes such as `ServicePortNotFound` or `BackendUnresolved` |
| `haptic_render_total` | Counter | Reconcile renders by `cache_state`: `cold` re-evaluated every template, `warm` reused the incremental graph, `replay` reused the previous output unchanged. Counts on every replica, because followers render to keep their graph warm for a leadership change. A replica that keeps counting `cold` pays the full render cost on every change |

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

## Deployment metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_deployment_total` | Counter | Total deployment attempts |
| `haptic_deployment_duration_seconds` | Histogram | Time spent deploying to HAProxy |
| `haptic_deployment_errors_total` | Counter | Failed deployments |
| `haptic_haproxy_reloads_total` | Counter | HAProxy reloads triggered by deployments; compare with runtime apply counts when investigating reload frequency |
| `haptic_deploy_apply_total` | Counter | Applies an agent accepted, by `pod` and by the `mode` it reported: `runtime`, `file_only`, `reload`, `scheduled` or `noop`. The reload-free share of a rollout is the `runtime`+`file_only`+`noop` fraction |
| `haptic_apply_rejected_total` | Counter | Applies an agent refused or rolled back, by `pod`. Every increment carries HAProxy's own message in a Warning event and the pod's status condition |
| `haptic_agent_version_skew_total` | Counter | Applies degraded to full state plus a reload because the pod's agent speaks a different API major or doesn't execute an op kind. Can increase during a rolling upgrade; should stop increasing once versions match |

**Key queries:**

```promql
# Deployment rate
rate(haptic_deployment_total[5m])

# HAProxy reload rate — the capacity/SLO signal (a reload forks the process)
rate(haptic_haproxy_reloads_total[5m])

# Applies by what they did to the pod — `runtime` is the reload-free lane
sum by (mode) (rate(haptic_deploy_apply_total[5m]))

# Pods rejecting applies, worst first
topk(5, sum by (pod) (rate(haptic_apply_rejected_total[5m])))

# 95th percentile deployment latency
histogram_quantile(0.95, rate(haptic_deployment_duration_seconds_bucket[5m]))

# Deployment success rate
100 * (1 - (
  rate(haptic_deployment_errors_total[5m]) /
  rate(haptic_deployment_total[5m])
))
```

## Fleet convergence & config staleness

Use fleet convergence to check whether a change reached every HAProxy pod.
A failed attempt can recover on retry; persistent divergence means some pods
still lack the desired configuration. These gauges report only on the leader.

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

The bundled [`HAProxyFleetDiverged` alert](monitoring.md#alerting-rules) detects pods that remain behind the desired configuration. If you add an alert on `time() - haptic_last_full_sync_timestamp_seconds`, set its threshold above your `driftPreventionInterval` to avoid alerting between scheduled checks.

## Runtime operation metrics

The controller counts what it asked the fleet to run without a reload. A change
the render couldn't express as a runtime op is a reload, and the reasons for
that are in the pod's status.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_runtime_backend_ops_total` | Counter | `op` | Backend lifecycle operations the fleet applied at runtime, by op kind |
| `haptic_runtime_server_ops_total` | Counter | `op` | Server lifecycle operations the fleet applied at runtime, by op kind |
| `haptic_runtime_backend_fallback_total` | Counter | `reason` | Runtime backend batches a pod reloaded instead of running, by reason (`name_collision`: a fresh backend whose name a not-yet-deleted one still holds; `op_rejected`: any other refusal) |
| `haptic_runtime_map_divergence_total` | Counter | `map` | Runtime maps whose post-apply read-back disagreed with the desired content, forcing a reload fallback. The `map` label names the file, so one map dominating the rate points at the template that builds it |

**Key queries:**

```promql
# Server changes applied without a reload
sum by (op) (rate(haptic_runtime_server_ops_total[5m]))

# Route adds and removes that stayed reload-free
sum by (op) (rate(haptic_runtime_backend_ops_total[5m]))

# Backend batches that fell back to a reload, by reason
sum by (reason) (rate(haptic_runtime_backend_fallback_total[5m]))
```

## Agent metrics

Each agent serves its own `/metrics` on the pod's `agent-metrics` port, scraped
by the bundled PodMonitor. These are per-pod facts the controller can't see:
what the agent did with an apply after it accepted it.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_agent_apply_total` | Counter | `mode` | Applies this agent completed, by outcome mode |
| `haptic_agent_apply_rejected_total` | Counter | `stage` | Applies this agent refused or rolled back, by the stage that failed |
| `haptic_agent_reloads_total` | Counter | `result` | Reloads this agent asked the master process for |
| `haptic_agent_rollbacks_total` | Counter | — | Applies whose file set was restored to the last known good |
| `haptic_agent_op_errors_total` | Counter | `kind` | Runtime ops HAProxy rejected, by op kind |
| `haptic_agent_invariant_violations_total` | Counter | `name` | Invariants the agent observed failing. Any increment is a defect — alert on it |
| `haptic_agent_deferred_deletes_total` | Counter | `kind`, `outcome` | Deferred runtime deletes, by object kind and whether they completed |
| `haptic_agent_generation` | Gauge | — | The agent's apply generation, which increases by one per successful apply |
| `haptic_agent_map_divergence_total` | Counter | — | Read-backs that found the running state different from the desired one; the controller counts the same events per `map` in `haptic_runtime_map_divergence_total` |

**Key queries:**

```promql
# Reload-free applies, fleet-wide
sum(rate(haptic_agent_apply_total{mode="runtime"}[5m]))

# Where applies are failing, by stage
sum by (stage) (rate(haptic_agent_apply_rejected_total[5m]))

# Any invariant violation at all
sum by (name) (increase(haptic_agent_invariant_violations_total[1h])) > 0
```

The controller and the agents count the same applies from either end: `haptic_deploy_apply_total{pod,mode}` on the controller, `haptic_agent_apply_total{mode}` on each pod. They agree in steady state; a difference is an apply one side never saw.

## Where the old metrics went

| Removed | Replacement |
|---------|-------------|
| `haptic_dataplane_api_operations_total` | `haptic_deploy_apply_total{pod,mode}` — applies, by what the pod did with them |
| `haptic_runtime_fast_path_fires_total` | `haptic_deploy_apply_total{mode="runtime"}` |
| `haptic_runtime_fast_path_applies_total` | `haptic_runtime_server_ops_total` |
| `haptic_runtime_fast_path_failures_total` | `haptic_agent_op_errors_total{kind}` on the pod, `haptic_apply_rejected_total{pod}` on the controller |
| `haptic_runtime_fast_path_server_updates_total` | `haptic_runtime_server_ops_total{op}` |
| `haptic_deploy_runtime_divergence_total` | `haptic_runtime_map_divergence_total{map}`, which now also covers what the agent reads back after its own ops |

Track HAProxy validation failures with
`haptic_config_rejected_total{validator="haproxy"}`.

## Validation metrics

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

## Resource metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_resource_count` | Gauge | `type` | Current count of watched resources |
| `haptic_haproxy_pods_rejected_total` | Counter | `reason` | HAProxy pods refused admission by the discovery component. Persistent non-zero growth typically means the controller can't talk to the deployed HAProxy pods (for example, the bundled HAProxy major.minor differs from the chart's `haproxyVersion`). |
| `haptic_config_rejected_total` | Counter | `validator` | Configuration refused by a validation gate. The `validator` label names which check rejected it: `basic`, `template`, `jsonpath` or `validationtests` for a `HAProxyTemplateConfig` load, `coordinator` when a validator timed out, and `haproxy` when the render gate's own `haproxy -c` refused a rendered config. Non-zero growth means the leader is refusing new config and continuing on the last-good one — **alert on it**: the operator's latest change isn't live. |
| `haptic_config_pinned` | Gauge | | `1` while the render gate holds renders HAProxy refused twice in a row. The pods keep serving the last configuration HAProxy accepted, and nothing new reaches them until the input the `ConfigValidated` condition names is fixed. Leader-only; `0` on followers. |
| `haptic_component_mailbox_depth` | Gauge | `component` | Events waiting in a component's coalescing mailbox. A sustained increase indicates that the component is falling behind. |

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

# Renders held because HAProxy refused two in a row — alert if > 0
haptic_config_pinned
```

## Event metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_event_subscribers` | Gauge | Active event subscribers |
| `haptic_events_published_total` | Counter | Selected controller events; not a count of every internal event |

**Key queries:**

```promql
# Event publishing rate
rate(haptic_events_published_total[5m])

# Subscriber count (should be constant)
haptic_event_subscribers

# Subscriber changes (indicates component restarts)
delta(haptic_event_subscribers[5m])
```

## Leader election metrics

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_leader_election_is_leader` | Gauge | 1 if this replica is leader, 0 otherwise |
| `haptic_leader_election_transitions_total` | Counter | Leadership transitions (gain/loss) |
| `haptic_leader_election_time_as_leader_seconds_total` | Counter | Cumulative time as leader |

**Key queries:**

```promql
# Current leader count (should be exactly 1)
sum by (namespace, job) (haptic_leader_election_is_leader)

# Identify leader pod
haptic_leader_election_is_leader == 1

# Leadership transition rate
rate(haptic_leader_election_transitions_total[1h])

# Average time as leader per transition
haptic_leader_election_time_as_leader_seconds_total /
haptic_leader_election_transitions_total
```

Scope these queries to one Helm release. The bundled ServiceMonitor uses a
separate Service for each release. If you customize scraping, retain an
equivalent release label when counting leaders.

## Webhook metrics

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

## Reconciliation queue

| Metric | Type | Description |
|--------|------|-------------|
| `haptic_reconciliation_queue_wait_seconds` | Histogram | Time a triggered reconciliation waits in the coordinator queue before processing starts; rising values indicate the controller can't keep up with change volume |

## Event bus backpressure

These complement `haptic_events_published_total` / `haptic_event_subscribers` from above.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_events_dropped_total` | Counter | — | Critical event drops; equivalent to `haptic_events_dropped_critical_total` |
| `haptic_events_dropped_critical_total` | Counter | — | Critical event drops that can interrupt configuration delivery |
| `haptic_events_dropped_observability_total` | Gauge | — | Drops from observability-only subscribers (expected under load, non-alerting) |
| `haptic_events_dropped_by_subscriber_total` | Counter | `subscriber`, `event_type` | Per-subscriber drop counts for diagnosing which component is falling behind |

## Build info

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `haptic_build_info` | Gauge | `version`, `haproxy_version`, `go_version` | Always `1`; useful for joining build metadata into other queries |

```promql
# Attach the version from the same scraped controller
haptic_reconciliation_total * on(job, instance) group_left(version) haptic_build_info
```
