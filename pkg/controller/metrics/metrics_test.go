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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.ReconciliationDuration)
	assert.NotNil(t, metrics.ReconciliationTotal)
	assert.NotNil(t, metrics.ReconciliationErrors)
	assert.NotNil(t, metrics.DeploymentDuration)
	assert.NotNil(t, metrics.DeploymentTotal)
	assert.NotNil(t, metrics.DeploymentErrors)
	assert.NotNil(t, metrics.ValidationTotal)
	assert.NotNil(t, metrics.ValidationErrors)
	assert.NotNil(t, metrics.ResourceCount)
	assert.NotNil(t, metrics.EventSubscribers)
	assert.NotNil(t, metrics.EventsPublished)
}

func TestMetrics_RecordReconciliation(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record successful reconciliation
	metrics.RecordReconciliation(1.5, true)

	// Verify total counter incremented
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationTotal))

	// Verify error counter not incremented
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ReconciliationErrors))

	// Verify histogram was created (can't easily check count)
	assert.NotNil(t, metrics.ReconciliationDuration)

	// Record failed reconciliation
	metrics.RecordReconciliation(0, false)

	// Verify total counter incremented
	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.ReconciliationTotal))

	// Verify error counter incremented
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationErrors))
}

func TestMetrics_RecordDeployment(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record successful deployment
	metrics.RecordDeployment(2.5, true)

	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.DeploymentTotal))
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.DeploymentErrors))
	assert.NotNil(t, metrics.DeploymentDuration)

	// Record failed deployment
	metrics.RecordDeployment(0, false)

	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.DeploymentTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.DeploymentErrors))
}

func TestMetrics_RecordValidation(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record successful validation
	metrics.RecordValidation(true)

	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ValidationTotal))
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ValidationErrors))

	// Record failed validation
	metrics.RecordValidation(false)

	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.ValidationTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ValidationErrors))
}

func TestMetrics_SetResourceCount(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Set counts for different resource types
	metrics.SetResourceCount("ingresses", 10)
	metrics.SetResourceCount("services", 25)

	// Verify values
	ingresses, err := metrics.ResourceCount.GetMetricWithLabelValues("ingresses")
	require.NoError(t, err)
	assert.Equal(t, 10.0, testutil.ToFloat64(ingresses))

	services, err := metrics.ResourceCount.GetMetricWithLabelValues("services")
	require.NoError(t, err)
	assert.Equal(t, 25.0, testutil.ToFloat64(services))

	// Update counts
	metrics.SetResourceCount("ingresses", 15)
	ingresses, err = metrics.ResourceCount.GetMetricWithLabelValues("ingresses")
	require.NoError(t, err)
	assert.Equal(t, 15.0, testutil.ToFloat64(ingresses))
}

func TestMetrics_SetEventSubscribers(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	metrics.SetEventSubscribers(5)
	assert.Equal(t, 5.0, testutil.ToFloat64(metrics.EventSubscribers))

	metrics.SetEventSubscribers(10)
	assert.Equal(t, 10.0, testutil.ToFloat64(metrics.EventSubscribers))
}

func TestMetrics_RecordEvent(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	metrics.RecordEvent()
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.EventsPublished))

	metrics.RecordEvent()
	metrics.RecordEvent()
	assert.Equal(t, 3.0, testutil.ToFloat64(metrics.EventsPublished))
}

func TestMetrics_InstanceBased(t *testing.T) {
	// Test that metrics are instance-based (not global)
	// This validates the design for application reinitialization

	// Instance 1
	registry1 := prometheus.NewRegistry()
	metrics1 := NewMetrics(registry1)
	metrics1.RecordReconciliation(1.0, true)

	// Instance 2
	registry2 := prometheus.NewRegistry()
	metrics2 := NewMetrics(registry2)
	metrics2.RecordReconciliation(2.0, true)

	// Verify instances are independent
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics1.ReconciliationTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics2.ReconciliationTotal))

	// Verify each instance only has its own metrics
	gatheredMetrics1, err := registry1.Gather()
	require.NoError(t, err)

	gatheredMetrics2, err := registry2.Gather()
	require.NoError(t, err)

	// Both should have the same metric names but different values
	assert.Len(t, gatheredMetrics1, len(gatheredMetrics2))
}

func TestMetrics_MultipleOperations(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Simulate a reconciliation cycle
	metrics.RecordReconciliation(1.5, true)
	metrics.RecordValidation(true)
	metrics.RecordDeployment(2.0, true)
	metrics.SetResourceCount("ingresses", 5)
	metrics.SetEventSubscribers(3)
	metrics.RecordEvent()

	// Verify all metrics recorded
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ReconciliationTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.ValidationTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.DeploymentTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.EventsPublished))

	ingresses, _ := metrics.ResourceCount.GetMetricWithLabelValues("ingresses")
	assert.Equal(t, 5.0, testutil.ToFloat64(ingresses))

	assert.Equal(t, 3.0, testutil.ToFloat64(metrics.EventSubscribers))
}

func TestMetrics_AllMetricsRegistered(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// GaugeVec metrics don't appear in registry until used with a label value
	// Initialize them to ensure they're registered
	metrics.SetResourceCount("test", 0)
	metrics.SetEventSubscribers(0)

	// Gather all metrics
	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	// Expected metrics
	expectedMetrics := []string{
		"haptic_reconciliation_duration_seconds",
		"haptic_reconciliation_total",
		"haptic_reconciliation_errors_total",
		"haptic_deployment_duration_seconds",
		"haptic_deployment_total",
		"haptic_deployment_errors_total",
		"haptic_validation_total",
		"haptic_validation_errors_total",
		"haptic_resource_count",
		"haptic_event_subscribers",
		"haptic_events_published_total",
	}

	// Collect registered metric names
	registeredMetrics := make(map[string]bool)
	for _, mf := range metricFamilies {
		registeredMetrics[mf.GetName()] = true
	}

	// Verify all expected metrics are registered
	for _, expected := range expectedMetrics {
		assert.True(t, registeredMetrics[expected],
			"metric %s not registered", expected)
	}
}

func TestMetrics_RecordWebhookRequest(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record successful webhook request
	metrics.RecordWebhookRequest("v1.ConfigMap", "allowed", 0.5)

	// Verify total counter incremented
	webhookTotal, err := metrics.WebhookRequestsTotal.GetMetricWithLabelValues("v1.ConfigMap", "allowed")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(webhookTotal))

	// Record denied webhook request
	metrics.RecordWebhookRequest("v1.ConfigMap", "denied", 0.3)

	webhookDenied, err := metrics.WebhookRequestsTotal.GetMetricWithLabelValues("v1.ConfigMap", "denied")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(webhookDenied))

	// Record error webhook request
	metrics.RecordWebhookRequest("v1.Secret", "error", 0.1)

	webhookError, err := metrics.WebhookRequestsTotal.GetMetricWithLabelValues("v1.Secret", "error")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(webhookError))
}

func TestMetrics_RecordWebhookValidation(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record allowed validation
	metrics.RecordWebhookValidation("v1.ConfigMap", "allowed")

	validation, err := metrics.WebhookValidationTotal.GetMetricWithLabelValues("v1.ConfigMap", "allowed")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(validation))

	// Record denied validation
	metrics.RecordWebhookValidation("v1.ConfigMap", "denied")

	denied, err := metrics.WebhookValidationTotal.GetMetricWithLabelValues("v1.ConfigMap", "denied")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(denied))

	// Multiple validations of same type
	metrics.RecordWebhookValidation("v1.ConfigMap", "allowed")
	metrics.RecordWebhookValidation("v1.ConfigMap", "allowed")

	validation, err = metrics.WebhookValidationTotal.GetMetricWithLabelValues("v1.ConfigMap", "allowed")
	require.NoError(t, err)
	assert.Equal(t, 3.0, testutil.ToFloat64(validation))
}

func TestMetrics_SetWebhookCertExpiry(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Set certificate expiry
	expiryTime := int64(1735689600) // Some future timestamp
	metrics.SetWebhookCertExpiry(expiryTime)

	assert.Equal(t, float64(expiryTime), testutil.ToFloat64(metrics.WebhookCertExpiry))

	// Update certificate expiry
	newExpiryTime := int64(1767225600) // Later timestamp
	metrics.SetWebhookCertExpiry(newExpiryTime)

	assert.Equal(t, float64(newExpiryTime), testutil.ToFloat64(metrics.WebhookCertExpiry))
}

func TestMetrics_RecordWebhookCertRotation(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	metrics.RecordWebhookCertRotation()
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.WebhookCertRotations))

	metrics.RecordWebhookCertRotation()
	metrics.RecordWebhookCertRotation()
	assert.Equal(t, 3.0, testutil.ToFloat64(metrics.WebhookCertRotations))
}

func TestMetrics_SetIsLeader(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Not leader initially
	metrics.SetIsLeader(false)
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.LeaderElectionIsLeader))

	// Become leader
	metrics.SetIsLeader(true)
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.LeaderElectionIsLeader))

	// Lose leadership
	metrics.SetIsLeader(false)
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.LeaderElectionIsLeader))
}

func TestMetrics_RecordLeadershipTransition(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record transitions
	metrics.RecordLeadershipTransition()
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.LeaderElectionTransitionsTotal))

	metrics.RecordLeadershipTransition()
	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.LeaderElectionTransitionsTotal))

	metrics.RecordLeadershipTransition()
	assert.Equal(t, 3.0, testutil.ToFloat64(metrics.LeaderElectionTransitionsTotal))
}

func TestMetrics_AddTimeAsLeader(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Add time as leader
	metrics.AddTimeAsLeader(100.5)
	assert.Equal(t, 100.5, testutil.ToFloat64(metrics.LeaderElectionTimeAsLeaderSeconds))

	// Add more time
	metrics.AddTimeAsLeader(50.25)
	assert.Equal(t, 150.75, testutil.ToFloat64(metrics.LeaderElectionTimeAsLeaderSeconds))
}

func TestMetrics_RecordEventDrop(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record a drop from reconciler subscriber
	metrics.RecordEventDrop("reconciler", "ResourceIndexUpdatedEvent")

	// Verify aggregate counters incremented
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.EventsDropped))
	assert.Equal(t, 1.0, testutil.ToFloat64(metrics.EventsDroppedCritical))

	// Verify per-subscriber counter
	counter, err := metrics.EventsDroppedBySubscriber.GetMetricWithLabelValues("reconciler", "ResourceIndexUpdatedEvent")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(counter))

	// Record another drop from a different subscriber
	metrics.RecordEventDrop("deployer", "ReconciliationCompletedEvent")

	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.EventsDropped))
	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.EventsDroppedCritical))

	deployer, err := metrics.EventsDroppedBySubscriber.GetMetricWithLabelValues("deployer", "ReconciliationCompletedEvent")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(deployer))

	// Record multiple drops from same subscriber
	metrics.RecordEventDrop("reconciler", "ResourceIndexUpdatedEvent")
	metrics.RecordEventDrop("reconciler", "ConfigValidatedEvent")

	reconcilerIndex, err := metrics.EventsDroppedBySubscriber.GetMetricWithLabelValues("reconciler", "ResourceIndexUpdatedEvent")
	require.NoError(t, err)
	assert.Equal(t, 2.0, testutil.ToFloat64(reconcilerIndex))

	reconcilerConfig, err := metrics.EventsDroppedBySubscriber.GetMetricWithLabelValues("reconciler", "ConfigValidatedEvent")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(reconcilerConfig))
}

func TestMetrics_RecordValidationTests(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Record test results
	metrics.RecordValidationTests(10, 8, 2, 1.5)

	assert.Equal(t, 10.0, testutil.ToFloat64(metrics.ValidationTestsTotal))
	assert.Equal(t, 8.0, testutil.ToFloat64(metrics.ValidationTestsPassTotal))
	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.ValidationTestsFailTotal))

	// Record more test results
	metrics.RecordValidationTests(5, 5, 0, 0.5)

	assert.Equal(t, 15.0, testutil.ToFloat64(metrics.ValidationTestsTotal))
	assert.Equal(t, 13.0, testutil.ToFloat64(metrics.ValidationTestsPassTotal))
	assert.Equal(t, 2.0, testutil.ToFloat64(metrics.ValidationTestsFailTotal))
}

func TestMetrics_UpdateParserCacheStats(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	// Initial state should be 0
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ParserCacheHits))
	assert.Equal(t, 0.0, testutil.ToFloat64(metrics.ParserCacheMisses))

	// First update with cumulative stats
	metrics.UpdateParserCacheStats(10, 5)

	assert.Equal(t, 10.0, testutil.ToFloat64(metrics.ParserCacheHits))
	assert.Equal(t, 5.0, testutil.ToFloat64(metrics.ParserCacheMisses))

	// Second update should add the delta
	metrics.UpdateParserCacheStats(15, 7)

	assert.Equal(t, 15.0, testutil.ToFloat64(metrics.ParserCacheHits))
	assert.Equal(t, 7.0, testutil.ToFloat64(metrics.ParserCacheMisses))

	// Update with same values should not change counters
	metrics.UpdateParserCacheStats(15, 7)

	assert.Equal(t, 15.0, testutil.ToFloat64(metrics.ParserCacheHits))
	assert.Equal(t, 7.0, testutil.ToFloat64(metrics.ParserCacheMisses))
}
