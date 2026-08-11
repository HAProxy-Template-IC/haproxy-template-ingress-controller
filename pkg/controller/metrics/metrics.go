// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	pkgmetrics "gitlab.com/haproxy-haptic/haptic/pkg/metrics"
)

// Metrics holds all controller-specific Prometheus metrics.
//
// IMPORTANT: Create one instance per application iteration.
// When the iteration ends (e.g., on config reload), metrics are garbage collected.
// This prevents stale state from surviving across reinitialization cycles.
type Metrics struct {
	// Reconciliation metrics
	ReconciliationDuration prometheus.Histogram
	ReconciliationTotal    prometheus.Counter
	ReconciliationErrors   prometheus.Counter

	// Deployment metrics
	DeploymentDuration prometheus.Histogram
	DeploymentTotal    prometheus.Counter
	DeploymentErrors   prometheus.Counter

	// HAProxy reload + DataPlane API operation counters, populated from
	// DeploymentCompletedEvent. Reloads are the canonical capacity/SLO signal: a
	// reload momentarily forks the HAProxy process, so a high reload rate (vs
	// runtime-API updates) is what to capacity-plan and alert on. The data is
	// already carried on the event; these surface it as cumulative counters.
	HAProxyReloadsTotal         prometheus.Counter
	DataplaneAPIOperationsTotal prometheus.Counter

	// Fleet-convergence + config-staleness gauges. Populated LEADER-ONLY from
	// DeploymentCompletedEvent (only the leader deploys), so followers hold the
	// reset values below and never false-alert. Unlike alerting on
	// rate(haptic_deployment_errors_total) — which the self-healing scheduler
	// makes noisy, since transient deploy failures now self-heal — these give a
	// true "is the fleet converged, and for how long has it not been" signal:
	//   - FleetSize / FleetConverged: an operator alerts on converged < size.
	//   - LastFullSyncTimestamp: staleness is time() - this gauge.
	//   - DeploymentConsecutiveFailures: the noise-free replacement for alerting
	//     on the error counter (resets to 0 the moment the fleet fully converges).
	HAProxyFleetSize              prometheus.Gauge
	HAProxyFleetConverged         prometheus.Gauge
	LastFullSyncTimestamp         prometheus.Gauge
	DeploymentConsecutiveFailures prometheus.Gauge

	// consecutiveFailures is the running count backing DeploymentConsecutiveFailures.
	// Touched only from the metrics component's single-goroutine event loop
	// (SetFleetConvergence / ResetFleetConvergence), so it needs no lock.
	consecutiveFailures int

	// Runtime-eligible fast-path metrics. Fires counts every fast-path attempt
	// (one per pod per reconcile); Applies counts the subset that actually
	// applied >=1 runtime-eligible server update. Applies stuck at 0 while
	// Fires climbs means the fast path runs but the render diff never carries a
	// runtime-eligible change.
	RuntimeFastPathFires         prometheus.Counter
	RuntimeFastPathApplies       prometheus.Counter
	RuntimeFastPathFailures      prometheus.Counter
	RuntimeFastPathServerUpdates prometheus.Counter

	// DeployRuntimeDivergence counts endpoints whose post-reload read-back
	// found the on-disk config structurally diverged from the pushed body
	// (issue #84) — a concurrent writer clobbered a just-activated config.
	// The fast deploy retry self-heals each occurrence, but steady growth
	// means runtime-bypass pushes are racing structural deploys.
	DeployRuntimeDivergence prometheus.Counter

	// RuntimeMapDivergence counts runtime maps whose post-apply read-back
	// still disagreed with the desired content, each costing that sync its
	// reload-free lane (issue #48). Labelled by map because which one
	// degrades is the actionable part. The reload fallback is convergent, so
	// occasional counts during pod churn are expected; steady growth in
	// STEADY STATE means endpoint changes are reloading HAProxy, defeating
	// the runtime lane.
	RuntimeMapDivergence *prometheus.CounterVec

	// Validation metrics
	ValidationTotal  prometheus.Counter
	ValidationErrors prometheus.Counter

	// Resource metrics
	ResourceCount *prometheus.GaugeVec

	// Event metrics
	EventSubscribers           prometheus.Gauge
	EventsPublished            prometheus.Counter
	EventsDropped              prometheus.Counter     // Total drops (backwards compatible)
	EventsDroppedCritical      prometheus.Counter     // Drops from critical subscribers (alert-worthy)
	EventsDroppedBySubscriber  *prometheus.CounterVec // Drops by subscriber and event type
	EventsDroppedObservability prometheus.Gauge       // Drops from observability subscribers (polled, expected)

	// Queue wait metrics - time events spend waiting in channels before processing
	ReconciliationQueueWait prometheus.Histogram

	// Webhook metrics
	WebhookRequestsTotal   *prometheus.CounterVec
	WebhookRequestDuration prometheus.Histogram
	WebhookValidationTotal *prometheus.CounterVec

	// Leader election metrics
	LeaderElectionIsLeader            prometheus.Gauge
	LeaderElectionTransitionsTotal    prometheus.Counter
	LeaderElectionTimeAsLeaderSeconds prometheus.Counter

	// Discovery metrics — surfaces rejection of HAProxy pods that the
	// controller refuses to talk to (most commonly version-incompatible).
	// Without this counter, a misconfigured cluster (e.g., controller image
	// bundling HAProxy 3.3 while chart deploys 3.2) presents only as
	// "deployment.skipped" in the pipeline status, masking a real fault.
	HAProxyPodsRejectedTotal *prometheus.CounterVec

	// ConfigRejectedTotal counts HAProxyTemplateConfig loads refused by the
	// config-validation gate, labelled by the validator that rejected it
	// (basic / template / jsonpath / validationtests), or "coordinator" when the
	// scatter-gather itself failed — a validator timed out or didn't respond —
	// rather than a specific validator refusing the config. Persistent growth
	// means the controller is repeatedly refusing a new config and continuing to
	// serve the last-good one — the operator's config never took effect.
	ConfigRejectedTotal *prometheus.CounterVec

	// Build info metric
	BuildInfo *prometheus.GaugeVec
}

// New creates all controller metrics and registers them with the provided registry.
//
// IMPORTANT: Pass an instance-based registry (prometheus.NewRegistry()), NOT
// prometheus.DefaultRegisterer. Metrics are scoped to the registry's lifetime.
// When the registry is garbage collected (iteration ends), metrics are freed.
//
// This is critical for supporting application reinitialization on configuration
// changes without leaking metrics or accumulating stale state.
//
// Example:
//
//	registry := prometheus.NewRegistry()  // Create per iteration
//	metrics := metrics.NewMetrics(registry)  // Metrics tied to iteration
//	// ... use metrics ...
//	// When iteration ends, both registry and metrics are GC'd
func NewMetrics(registry prometheus.Registerer) *Metrics {
	m := &Metrics{
		// Reconciliation metrics
		ReconciliationDuration: pkgmetrics.NewHistogramWithBuckets(
			registry,
			"haptic_reconciliation_duration_seconds",
			"Time spent in reconciliation cycles",
			pkgmetrics.DurationBuckets(),
		),
		ReconciliationTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_reconciliation_total",
			"Total number of reconciliation cycles",
		),
		ReconciliationErrors: pkgmetrics.NewCounter(
			registry,
			"haptic_reconciliation_errors_total",
			"Total number of failed reconciliation cycles",
		),

		// Deployment metrics
		DeploymentDuration: pkgmetrics.NewHistogramWithBuckets(
			registry,
			"haptic_deployment_duration_seconds",
			"Time spent deploying configurations",
			pkgmetrics.DeploymentDurationBuckets(),
		),
		DeploymentTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_deployment_total",
			"Total number of deployment attempts",
		),
		DeploymentErrors: pkgmetrics.NewCounter(
			registry,
			"haptic_deployment_errors_total",
			"Total number of failed deployments",
		),
		HAProxyReloadsTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_haproxy_reloads_total",
			"Total HAProxy reloads triggered by config deployments. A reload forks the HAProxy process; a high reload rate (vs runtime-API server updates) is the key capacity/SLO signal.",
		),
		DataplaneAPIOperationsTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_dataplane_api_operations_total",
			"Total DataPlane API operations issued across config deployments (structural changes applied to HAProxy pods).",
		),

		// Fleet-convergence + config-staleness gauges (leader-only; see struct docs).
		HAProxyFleetSize: pkgmetrics.NewGauge(
			registry,
			"haptic_haproxy_fleet_size",
			"Number of HAProxy pods the last deployment targeted (leader-only; 0 on followers).",
		),
		HAProxyFleetConverged: pkgmetrics.NewGauge(
			registry,
			"haptic_haproxy_fleet_converged",
			"Number of HAProxy pods now at the desired config after the last deployment. Alert on haptic_haproxy_fleet_converged < haptic_haproxy_fleet_size (leader-only; 0 on followers).",
		),
		LastFullSyncTimestamp: pkgmetrics.NewGauge(
			registry,
			"haptic_last_full_sync_timestamp_seconds",
			"Unix timestamp (seconds) of the last time the whole HAProxy fleet converged on the desired config. Query staleness as time() - haptic_last_full_sync_timestamp_seconds. Seeded to controller start time so pre-first-sync staleness reads as uptime.",
		),
		DeploymentConsecutiveFailures: pkgmetrics.NewGauge(
			registry,
			"haptic_deployment_consecutive_failures",
			"Count of consecutive deployments that did not fully converge the fleet; resets to 0 on the first full sync. The noise-free replacement for alerting on rate(haptic_deployment_errors_total) now that transient deploy failures self-heal (leader-only; 0 on followers).",
		),

		// Runtime-eligible fast-path metrics
		RuntimeFastPathFires: pkgmetrics.NewCounter(
			registry,
			"haptic_runtime_fast_path_fires_total",
			"Total runtime-eligible fast-path apply attempts (one per pod per reconcile)",
		),
		RuntimeFastPathApplies: pkgmetrics.NewCounter(
			registry,
			"haptic_runtime_fast_path_applies_total",
			"Fast-path attempts that applied at least one runtime-eligible server update",
		),
		RuntimeFastPathFailures: pkgmetrics.NewCounter(
			registry,
			"haptic_runtime_fast_path_failures_total",
			"Fast-path attempts that errored (best-effort; the scheduled deploy converges)",
		),
		RuntimeFastPathServerUpdates: pkgmetrics.NewCounter(
			registry,
			"haptic_runtime_fast_path_server_updates_total",
			"Total runtime-eligible server updates applied via the fast path",
		),
		DeployRuntimeDivergence: pkgmetrics.NewCounter(
			registry,
			"haptic_deploy_runtime_divergence_total",
			"Endpoints whose post-reload read-back found the on-disk config structurally diverged from the pushed body (a concurrent writer clobbered a just-activated config; the fast deploy retry self-heals it).",
		),

		RuntimeMapDivergence: pkgmetrics.NewCounterVec(
			registry,
			"haptic_runtime_map_divergence_total",
			"Runtime maps whose post-apply read-back disagreed with the desired content, forcing a reload fallback and so losing the reload-free lane for that sync.",
			[]string{"map"},
		),

		// Validation metrics
		ValidationTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_validation_total",
			"Total number of validation attempts",
		),
		ValidationErrors: pkgmetrics.NewCounter(
			registry,
			"haptic_validation_errors_total",
			"Total number of failed validations",
		),

		// Resource metrics
		ResourceCount: pkgmetrics.NewGaugeVec(
			registry,
			"haptic_resource_count",
			"Number of resources by type",
			[]string{"type"},
		),

		// Event metrics
		EventSubscribers: pkgmetrics.NewGauge(
			registry,
			"haptic_event_subscribers",
			"Number of active event subscribers",
		),
		EventsPublished: pkgmetrics.NewCounter(
			registry,
			"haptic_events_published_total",
			"Total number of events published",
		),
		EventsDropped: pkgmetrics.NewCounter(
			registry,
			"haptic_events_dropped_total",
			"Total number of events dropped due to full subscriber buffers",
		),
		EventsDroppedCritical: pkgmetrics.NewCounter(
			registry,
			"haptic_events_dropped_critical_total",
			"Events dropped from critical subscribers (alert if > 0)",
		),
		EventsDroppedBySubscriber: pkgmetrics.NewCounterVec(
			registry,
			"haptic_events_dropped_by_subscriber_total",
			"Events dropped per subscriber and event type",
			[]string{"subscriber", "event_type"},
		),
		EventsDroppedObservability: pkgmetrics.NewGauge(
			registry,
			"haptic_events_dropped_observability_total",
			"Events dropped from observability subscribers (expected, non-alerting)",
		),

		// Queue wait metrics
		ReconciliationQueueWait: pkgmetrics.NewHistogramWithBuckets(
			registry,
			"haptic_reconciliation_queue_wait_seconds",
			"Time a reconciliation event spends waiting in the coordinator queue before processing starts",
			pkgmetrics.DurationBuckets(),
		),

		// Webhook metrics
		WebhookRequestsTotal: pkgmetrics.NewCounterVec(
			registry,
			"haptic_webhook_requests_total",
			"Total number of webhook admission requests",
			[]string{"gvk", "result"},
		),
		WebhookRequestDuration: pkgmetrics.NewHistogramWithBuckets(
			registry,
			"haptic_webhook_request_duration_seconds",
			"Time spent processing webhook requests",
			pkgmetrics.DurationBuckets(),
		),
		WebhookValidationTotal: pkgmetrics.NewCounterVec(
			registry,
			"haptic_webhook_validation_total",
			"Total number of webhook validation results",
			[]string{"gvk", "result"},
		),

		// Leader election metrics
		LeaderElectionIsLeader: pkgmetrics.NewGauge(
			registry,
			"haptic_leader_election_is_leader",
			"Indicates if this replica is the leader (1) or follower (0)",
		),
		LeaderElectionTransitionsTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_leader_election_transitions_total",
			"Total number of leadership transitions",
		),
		LeaderElectionTimeAsLeaderSeconds: pkgmetrics.NewCounter(
			registry,
			"haptic_leader_election_time_as_leader_seconds_total",
			"Cumulative time spent as leader in seconds",
		),

		// Discovery metrics
		HAProxyPodsRejectedTotal: pkgmetrics.NewCounterVec(
			registry,
			"haptic_haproxy_pods_rejected_total",
			"Total number of HAProxy pods refused admission by the discovery component, labelled by reason. Persistent non-zero growth indicates the controller cannot talk to the deployed HAProxy pods (e.g., bundled HAProxy major.minor differs from the chart's haproxyVersion).",
			[]string{"reason"},
		),

		ConfigRejectedTotal: pkgmetrics.NewCounterVec(
			registry,
			"haptic_config_rejected_total",
			"Total number of HAProxyTemplateConfig loads refused by the config-validation gate, labelled by the validator that rejected the config (basic, template, jsonpath, validationtests) or \"coordinator\" when a validator timed out / didn't respond. Non-zero growth means the controller is refusing a new config and continuing on the last-good one.",
			[]string{"validator"},
		),

		// Build info metric
		BuildInfo: pkgmetrics.NewGaugeVec(
			registry,
			"haptic_build_info",
			"Controller build information (version labels, value always 1)",
			[]string{"version", "haproxy_version", "go_version"},
		),
	}

	// Seed last-full-sync to construction time so that, before the first full
	// fleet sync, staleness (time() - gauge) reads as uptime rather than the
	// entire Unix epoch.
	m.LastFullSyncTimestamp.Set(float64(time.Now().Unix()))

	return m
}

// RecordReconciliation records a completed reconciliation cycle.
//
// Parameters:
//   - durationSeconds: Time spent in reconciliation (use time.Since(start).Seconds())
//   - success: Whether the reconciliation completed successfully
func (m *Metrics) RecordReconciliation(durationSeconds float64, success bool) {
	m.ReconciliationTotal.Inc()
	m.ReconciliationDuration.Observe(durationSeconds)
	if !success {
		m.ReconciliationErrors.Inc()
	}
}

// RecordDeployment records a deployment attempt.
//
// Parameters:
//   - durationSeconds: Time spent deploying (use time.Since(start).Seconds())
//   - success: Whether the deployment completed successfully
func (m *Metrics) RecordDeployment(durationSeconds float64, success bool) {
	m.DeploymentTotal.Inc()
	m.DeploymentDuration.Observe(durationSeconds)
	if !success {
		m.DeploymentErrors.Inc()
	}
}

// RecordDeploymentOperations records the HAProxy reload count and DataPlane API
// operation count from a completed deployment. Both are cumulative; reloads is
// the headline capacity/SLO signal (see the metric help text). Zero values are
// skipped so a no-op deployment doesn't perturb the counters.
func (m *Metrics) RecordDeploymentOperations(reloads, apiOperations int) {
	if reloads > 0 {
		m.HAProxyReloadsTotal.Add(float64(reloads))
	}
	if apiOperations > 0 {
		m.DataplaneAPIOperationsTotal.Add(float64(apiOperations))
	}
}

// SetFleetConvergence records the outcome of a completed fleet deployment onto
// the fleet-convergence + config-staleness gauges. It is O(1) and must be
// called LEADER-ONLY (DeploymentCompletedEvent is leader-only).
//
// Given the deploy's total / succeeded / failed instance counts it:
//   - sets haptic_haproxy_fleet_size      = total
//   - sets haptic_haproxy_fleet_converged = succeeded
//   - on a full sync (succeeded == total && total > 0): resets the consecutive-
//     failure count to 0 and stamps haptic_last_full_sync_timestamp_seconds with now
//   - otherwise, on any failure (failed > 0): increments the consecutive-failure count
//
// A full sync is checked first so that succeeded == total always wins over a
// stray failed > 0 (the two are mutually exclusive for a well-formed result).
func (m *Metrics) SetFleetConvergence(total, succeeded, failed int) {
	m.HAProxyFleetSize.Set(float64(total))
	m.HAProxyFleetConverged.Set(float64(succeeded))

	switch {
	case total > 0 && succeeded == total:
		m.consecutiveFailures = 0
		m.LastFullSyncTimestamp.Set(float64(time.Now().Unix()))
	case failed > 0:
		m.consecutiveFailures++
	}
	m.DeploymentConsecutiveFailures.Set(float64(m.consecutiveFailures))
}

// ResetFleetConvergence clears the fleet-convergence gauges when this replica is
// no longer the leader. DeploymentCompletedEvent is leader-only, so a follower
// must not keep reporting the values it set while it was leader:
//   - fleet_size / fleet_converged / consecutive_failures → 0
//     (so converged < fleet_size becomes 0 < 0, i.e. false, and can't false-alert)
//   - last_full_sync → now, so a former leader doesn't accrue growing staleness
func (m *Metrics) ResetFleetConvergence() {
	m.consecutiveFailures = 0
	m.HAProxyFleetSize.Set(0)
	m.HAProxyFleetConverged.Set(0)
	m.DeploymentConsecutiveFailures.Set(0)
	m.LastFullSyncTimestamp.Set(float64(time.Now().Unix()))
}

// RecordRuntimeFastPath records one runtime-eligible fast-path apply attempt:
// serverUpdates is how many server updates it applied (0 = fired but nothing to
// do), failed reports whether it errored.
func (m *Metrics) RecordRuntimeFastPath(serverUpdates int, failed bool) {
	m.RuntimeFastPathFires.Inc()
	if failed {
		m.RuntimeFastPathFailures.Inc()
		return
	}
	if serverUpdates > 0 {
		m.RuntimeFastPathApplies.Inc()
		m.RuntimeFastPathServerUpdates.Add(float64(serverUpdates))
	}
}

// RecordDeployRuntimeDivergence counts one endpoint whose post-reload
// read-back found the on-disk config structurally diverged from the pushed
// body (issue #84).
func (m *Metrics) RecordDeployRuntimeDivergence() {
	m.DeployRuntimeDivergence.Inc()
}

// RecordRuntimeMapDivergence records one runtime map that failed its
// post-apply read-back and so forced a reload fallback.
func (m *Metrics) RecordRuntimeMapDivergence(mapName string) {
	m.RuntimeMapDivergence.WithLabelValues(mapName).Inc()
}

// RecordValidation records a validation attempt.
//
// Parameters:
//   - success: Whether the validation passed
func (m *Metrics) RecordValidation(success bool) {
	m.ValidationTotal.Inc()
	if !success {
		m.ValidationErrors.Inc()
	}
}

// SetResourceCount sets the count for a specific resource type.
//
// Parameters:
//   - resourceType: The type of resource (e.g., "ingresses", "services")
//   - count: The current number of resources of this type
func (m *Metrics) SetResourceCount(resourceType string, count int) {
	m.ResourceCount.WithLabelValues(resourceType).Set(float64(count))
}

// SetEventSubscribers sets the number of active event subscribers.
//
// Parameters:
//   - count: The current number of event subscribers
func (m *Metrics) SetEventSubscribers(count int) {
	m.EventSubscribers.Set(float64(count))
}

// RecordEvent records an event publication.
// Call this for every event published to the EventBus.
func (m *Metrics) RecordEvent() {
	m.EventsPublished.Inc()
}

// RecordWebhookRequest records a webhook admission request.
//
// Parameters:
//   - gvk: The GVK of the resource being validated (e.g., "v1.ConfigMap")
//   - result: The result of the request ("allowed", "denied", or "error")
//   - durationSeconds: Time spent processing the request
func (m *Metrics) RecordWebhookRequest(gvk, result string, durationSeconds float64) {
	m.WebhookRequestsTotal.WithLabelValues(gvk, result).Inc()
	m.WebhookRequestDuration.Observe(durationSeconds)
}

// RecordWebhookValidation records a webhook validation result.
//
// Parameters:
//   - gvk: The GVK of the resource being validated
//   - result: The validation result ("allowed", "denied", or "error")
func (m *Metrics) RecordWebhookValidation(gvk, result string) {
	m.WebhookValidationTotal.WithLabelValues(gvk, result).Inc()
}

// RecordHAProxyPodRejected increments the rejection counter for a stable
// reason label (e.g. "version_mismatch_older", "version_check_failed").
// Discovery publishes HAProxyPodRejectedEvent; the metrics component
// translates each event into a counter increment.
func (m *Metrics) RecordHAProxyPodRejected(reason string) {
	m.HAProxyPodsRejectedTotal.WithLabelValues(reason).Inc()
}

// RecordConfigRejected increments the config-rejection counter for the given
// validator (the one whose check failed).
func (m *Metrics) RecordConfigRejected(validator string) {
	m.ConfigRejectedTotal.WithLabelValues(validator).Inc()
}

// SetIsLeader sets whether this replica is the leader.
//
// Parameters:
//   - isLeader: true if this replica is the leader, false otherwise
func (m *Metrics) SetIsLeader(isLeader bool) {
	if isLeader {
		m.LeaderElectionIsLeader.Set(1)
	} else {
		m.LeaderElectionIsLeader.Set(0)
	}
}

// RecordLeadershipTransition records a leadership state change.
// Call this whenever leadership is gained or lost.
func (m *Metrics) RecordLeadershipTransition() {
	m.LeaderElectionTransitionsTotal.Inc()
}

// AddTimeAsLeader adds time spent as leader to the cumulative counter.
//
// Parameters:
//   - seconds: Time spent as leader in seconds
func (m *Metrics) AddTimeAsLeader(seconds float64) {
	m.LeaderElectionTimeAsLeaderSeconds.Add(seconds)
}

// RecordEventDrop records an event drop due to full subscriber buffer.
// This increments both aggregate counters and per-subscriber counters.
// Call this from the drop callback registered with EventBus.SetDropCallback().
//
// Parameters:
//   - subscriberName: The name of the subscriber that dropped the event
//   - eventType: The event type that was dropped
func (m *Metrics) RecordEventDrop(subscriberName, eventType string) {
	m.AddEventDrops(subscriberName, eventType, 1)
}

// AddEventDrops restores process-lifetime critical-drop totals into a new
// per-iteration registry.
func (m *Metrics) AddEventDrops(subscriberName, eventType string, count uint64) {
	increment := float64(count)
	m.EventsDropped.Add(increment)
	m.EventsDroppedCritical.Add(increment)
	m.EventsDroppedBySubscriber.WithLabelValues(subscriberName, eventType).Add(increment)
}

// SetObservabilityDrops sets the observability drop gauge from EventBus statistics.
// This should be called periodically since observability drops don't trigger callbacks.
func (m *Metrics) SetObservabilityDrops(count uint64) {
	m.EventsDroppedObservability.Set(float64(count))
}

// SetBuildInfo sets the build info metric with version labels.
// Call once at startup with the version information for this binary.
//
// Parameters:
//   - version: The controller version (e.g., "0.1.0-alpha.10")
//   - haproxyVersion: The HAProxy major.minor version (e.g., "3.2")
//   - goVersion: The Go runtime version (e.g., "go1.26.1")
func (m *Metrics) SetBuildInfo(version, haproxyVersion, goVersion string) {
	m.BuildInfo.WithLabelValues(version, haproxyVersion, goVersion).Set(1)
}

// RecordQueueWait records how long a reconciliation event waited in the coordinator
// queue before processing started.
func (m *Metrics) RecordQueueWait(seconds float64) {
	m.ReconciliationQueueWait.Observe(seconds)
}
