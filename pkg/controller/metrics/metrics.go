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
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

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

	// Validation metrics
	ValidationTotal  prometheus.Counter
	ValidationErrors prometheus.Counter

	// Validation test metrics
	ValidationTestsTotal     prometheus.Counter
	ValidationTestsPassTotal prometheus.Counter
	ValidationTestsFailTotal prometheus.Counter
	ValidationTestDuration   prometheus.Histogram

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
	ReconciliationQueueWait *prometheus.HistogramVec

	// Webhook metrics
	WebhookRequestsTotal   *prometheus.CounterVec
	WebhookRequestDuration prometheus.Histogram
	WebhookValidationTotal *prometheus.CounterVec
	WebhookCertExpiry      prometheus.Gauge
	WebhookCertRotations   prometheus.Counter

	// Leader election metrics
	LeaderElectionIsLeader            prometheus.Gauge
	LeaderElectionTransitionsTotal    prometheus.Counter
	LeaderElectionTimeAsLeaderSeconds prometheus.Counter

	// Parser cache metrics
	ParserCacheHits   prometheus.Counter
	ParserCacheMisses prometheus.Counter
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
	return &Metrics{
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
			pkgmetrics.DurationBuckets(),
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

		// Validation test metrics
		ValidationTestsTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_validation_tests_total",
			"Total number of validation tests executed",
		),
		ValidationTestsPassTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_validation_tests_pass_total",
			"Total number of validation tests that passed",
		),
		ValidationTestsFailTotal: pkgmetrics.NewCounter(
			registry,
			"haptic_validation_tests_fail_total",
			"Total number of validation tests that failed",
		),
		ValidationTestDuration: pkgmetrics.NewHistogramWithBuckets(
			registry,
			"haptic_validation_test_duration_seconds",
			"Time spent running validation tests",
			pkgmetrics.DurationBuckets(),
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
		ReconciliationQueueWait: pkgmetrics.NewHistogramVec(
			registry,
			"haptic_reconciliation_queue_wait_seconds",
			"Time events spent waiting in queue before processing",
			[]string{"phase"},
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
		WebhookCertExpiry: pkgmetrics.NewGauge(
			registry,
			"haptic_webhook_cert_expiry_timestamp_seconds",
			"Timestamp when webhook certificates expire",
		),
		WebhookCertRotations: pkgmetrics.NewCounter(
			registry,
			"haptic_webhook_cert_rotations_total",
			"Total number of webhook certificate rotations",
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

		// Parser cache metrics
		ParserCacheHits: pkgmetrics.NewCounter(
			registry,
			"haptic_parser_cache_hits_total",
			"Total number of parser cache hits",
		),
		ParserCacheMisses: pkgmetrics.NewCounter(
			registry,
			"haptic_parser_cache_misses_total",
			"Total number of parser cache misses",
		),
	}
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

// SetWebhookCertExpiry sets the webhook certificate expiry timestamp.
//
// Parameters:
//   - expiryTime: The time when the certificate expires
func (m *Metrics) SetWebhookCertExpiry(expiryTime int64) {
	m.WebhookCertExpiry.Set(float64(expiryTime))
}

// RecordWebhookCertRotation records a webhook certificate rotation.
func (m *Metrics) RecordWebhookCertRotation() {
	m.WebhookCertRotations.Inc()
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

// RecordValidationTests records validation test execution results.
//
// Parameters:
//   - total: Total number of tests executed
//   - passed: Number of tests that passed
//   - failed: Number of tests that failed
//   - durationSeconds: Time spent running tests (use time.Duration.Seconds())
func (m *Metrics) RecordValidationTests(total, passed, failed int, durationSeconds float64) {
	m.ValidationTestsTotal.Add(float64(total))
	m.ValidationTestsPassTotal.Add(float64(passed))
	m.ValidationTestsFailTotal.Add(float64(failed))
	m.ValidationTestDuration.Observe(durationSeconds)
}

// RecordEventDrop records an event drop due to full subscriber buffer.
// This increments both aggregate counters and per-subscriber counters.
// Call this from the drop callback registered with EventBus.SetDropCallback().
//
// Parameters:
//   - subscriberName: The name of the subscriber that dropped the event
//   - eventType: The event type that was dropped
func (m *Metrics) RecordEventDrop(subscriberName, eventType string) {
	m.EventsDropped.Inc()
	// Also increment critical drops since onDrop callback is only called for critical subscribers
	m.EventsDroppedCritical.Inc()
	m.EventsDroppedBySubscriber.WithLabelValues(subscriberName, eventType).Inc()
}

// SetObservabilityDrops sets the observability drop gauge from EventBus statistics.
// This should be called periodically since observability drops don't trigger callbacks.
func (m *Metrics) SetObservabilityDrops(count uint64) {
	m.EventsDroppedObservability.Set(float64(count))
}

// RecordQueueWait records queue wait time for a reconciliation phase.
//
// Parameters:
//   - phase: The phase name (e.g., "trigger_to_render", "render_to_validate", "validate_to_deploy")
//   - seconds: Time spent waiting in queue (use time.Duration.Seconds())
func (m *Metrics) RecordQueueWait(phase string, seconds float64) {
	m.ReconciliationQueueWait.WithLabelValues(phase).Observe(seconds)
}

// UpdateParserCacheStats updates parser cache hit/miss counters from cache statistics.
// This should be called periodically to sync Prometheus metrics with the parser cache stats.
//
// Parameters:
//   - hits: Total cache hits from parser.CacheStats()
//   - misses: Total cache misses from parser.CacheStats()
func (m *Metrics) UpdateParserCacheStats(hits, misses int64) {
	// Since these are counters, we need to track the delta since last update
	// The parser cache tracks cumulative stats, so we record the current totals
	// Prometheus counters can handle this via Add() with the delta
	currentHits := m.getCounterValue(m.ParserCacheHits)
	currentMisses := m.getCounterValue(m.ParserCacheMisses)

	if float64(hits) > currentHits {
		m.ParserCacheHits.Add(float64(hits) - currentHits)
	}
	if float64(misses) > currentMisses {
		m.ParserCacheMisses.Add(float64(misses) - currentMisses)
	}
}

// getCounterValue extracts the current value from a prometheus Counter.
// This is used for calculating deltas when syncing from external counters.
func (m *Metrics) getCounterValue(counter prometheus.Counter) float64 {
	var metric dto.Metric
	if err := counter.Write(&metric); err != nil {
		return 0
	}
	if metric.Counter != nil && metric.Counter.Value != nil {
		return *metric.Counter.Value
	}
	return 0
}
