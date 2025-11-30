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

package commentator

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
)

func TestNewEventCommentator(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()

	ec := NewEventCommentator(bus, logger, 500)

	require.NotNil(t, ec)
	assert.NotNil(t, ec.bus)
	assert.NotNil(t, ec.logger)
	assert.NotNil(t, ec.ringBuffer)
	assert.Equal(t, 500, ec.ringBuffer.Capacity())
	assert.NotNil(t, ec.stopCh)
}

func TestEventCommentator_DetermineLogLevel(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	tests := []struct {
		name      string
		eventType string
		want      slog.Level
	}{
		// Error level events
		{
			name:      "reconciliation failed is error",
			eventType: events.EventTypeReconciliationFailed,
			want:      slog.LevelError,
		},
		{
			name:      "template render failed is error",
			eventType: events.EventTypeTemplateRenderFailed,
			want:      slog.LevelError,
		},
		{
			name:      "validation failed is error",
			eventType: events.EventTypeValidationFailed,
			want:      slog.LevelError,
		},
		{
			name:      "instance deployment failed is error",
			eventType: events.EventTypeInstanceDeploymentFailed,
			want:      slog.LevelError,
		},
		{
			name:      "storage sync failed is error",
			eventType: events.EventTypeStorageSyncFailed,
			want:      slog.LevelError,
		},
		{
			name:      "webhook validation error is error",
			eventType: events.EventTypeWebhookValidationError,
			want:      slog.LevelError,
		},

		// Warn level events
		{
			name:      "config invalid is warn",
			eventType: events.EventTypeConfigInvalid,
			want:      slog.LevelWarn,
		},
		{
			name:      "credentials invalid is warn",
			eventType: events.EventTypeCredentialsInvalid,
			want:      slog.LevelWarn,
		},
		{
			name:      "webhook validation denied is warn",
			eventType: events.EventTypeWebhookValidationDenied,
			want:      slog.LevelWarn,
		},
		{
			name:      "lost leadership is warn",
			eventType: events.EventTypeLostLeadership,
			want:      slog.LevelWarn,
		},

		// Info level events
		{
			name:      "controller started is info",
			eventType: events.EventTypeControllerStarted,
			want:      slog.LevelInfo,
		},
		{
			name:      "controller shutdown is info",
			eventType: events.EventTypeControllerShutdown,
			want:      slog.LevelInfo,
		},
		{
			name:      "config validated is info",
			eventType: events.EventTypeConfigValidated,
			want:      slog.LevelInfo,
		},
		{
			name:      "index synchronized is info",
			eventType: events.EventTypeIndexSynchronized,
			want:      slog.LevelInfo,
		},
		{
			name:      "reconciliation completed is info",
			eventType: events.EventTypeReconciliationCompleted,
			want:      slog.LevelInfo,
		},
		{
			name:      "validation completed is info",
			eventType: events.EventTypeValidationCompleted,
			want:      slog.LevelInfo,
		},
		{
			name:      "deployment completed is info",
			eventType: events.EventTypeDeploymentCompleted,
			want:      slog.LevelInfo,
		},
		{
			name:      "leader election started is info",
			eventType: events.EventTypeLeaderElectionStarted,
			want:      slog.LevelInfo,
		},
		{
			name:      "became leader is info",
			eventType: events.EventTypeBecameLeader,
			want:      slog.LevelInfo,
		},
		{
			name:      "new leader observed is info",
			eventType: events.EventTypeNewLeaderObserved,
			want:      slog.LevelInfo,
		},

		// Debug level (default)
		{
			name:      "config parsed is debug",
			eventType: events.EventTypeConfigParsed,
			want:      slog.LevelDebug,
		},
		{
			name:      "resource index updated is debug",
			eventType: events.EventTypeResourceIndexUpdated,
			want:      slog.LevelDebug,
		},
		{
			name:      "template rendered is debug",
			eventType: events.EventTypeTemplateRendered,
			want:      slog.LevelDebug,
		},
		{
			name:      "unknown event type is debug",
			eventType: "unknown.event",
			want:      slog.LevelDebug,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ec.determineLogLevel(tt.eventType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEventCommentator_GenerateInsight_LifecycleEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("ControllerStartedEvent", func(t *testing.T) {
		event := events.NewControllerStartedEvent("v1.0.0", "s1.0.0")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Controller started")
		assert.Contains(t, insight, "v1.0.0")
		assertContainsAttr(t, attrs, "config_version", "v1.0.0")
		assertContainsAttr(t, attrs, "secret_version", "s1.0.0")
	})

	t.Run("ControllerShutdownEvent", func(t *testing.T) {
		event := events.NewControllerShutdownEvent("context canceled")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "shutting down")
		assert.Contains(t, insight, "context canceled")
		assertContainsAttr(t, attrs, "reason", "context canceled")
	})
}

func TestEventCommentator_GenerateInsight_ConfigurationEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("ConfigParsedEvent", func(t *testing.T) {
		event := events.NewConfigParsedEvent(nil, nil, "v2.0.0", "s2.0.0")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Configuration parsed")
		assert.Contains(t, insight, "v2.0.0")
		assertContainsAttr(t, attrs, "version", "v2.0.0")
	})

	t.Run("ConfigValidatedEvent", func(t *testing.T) {
		event := events.NewConfigValidatedEvent(nil, nil, "v3.0.0", "s3.0.0")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Configuration validated")
		assertContainsAttr(t, attrs, "version", "v3.0.0")
	})

	t.Run("ConfigInvalidEvent with errors", func(t *testing.T) {
		validationErrors := map[string][]string{
			"basic":    {"field required", "invalid format"},
			"template": {"syntax error in template"},
		}
		event := events.NewConfigInvalidEvent("v4.0.0", validationErrors)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "validation failed")
		assert.Contains(t, insight, "3 errors")
		assert.Contains(t, insight, "2 validators")
		assertContainsAttr(t, attrs, "version", "v4.0.0")
		assertContainsAttr(t, attrs, "error_count", 3)
	})
}

func TestEventCommentator_GenerateInsight_ReconciliationEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("ReconciliationTriggeredEvent", func(t *testing.T) {
		event := events.NewReconciliationTriggeredEvent("config_change")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Reconciliation triggered")
		assert.Contains(t, insight, "config_change")
		assertContainsAttr(t, attrs, "reason", "config_change")
	})

	t.Run("ReconciliationStartedEvent", func(t *testing.T) {
		event := events.NewReconciliationStartedEvent("debounce_timer")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Reconciliation started")
		assertContainsAttr(t, attrs, "trigger", "debounce_timer")
	})

	t.Run("ReconciliationCompletedEvent", func(t *testing.T) {
		event := events.NewReconciliationCompletedEvent(123)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Reconciliation completed")
		assertContainsAttr(t, attrs, "duration_ms", int64(123))
	})

	t.Run("ReconciliationFailedEvent", func(t *testing.T) {
		// Constructor is NewReconciliationFailedEvent(err, phase string)
		event := events.NewReconciliationFailedEvent("template syntax error", "template")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Reconciliation failed")
		assert.Contains(t, insight, "template phase")
		assert.Contains(t, insight, "template syntax error")
		assertContainsAttr(t, attrs, "phase", "template")
		assertContainsAttr(t, attrs, "error", "template syntax error")
	})
}

func TestEventCommentator_GenerateInsight_TemplateEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("TemplateRenderedEvent", func(t *testing.T) {
		// haproxyConfig, validationHAProxyConfig, validationPaths, auxiliaryFiles, validationAuxiliaryFiles, auxFileCount, durationMs
		// ConfigBytes is calculated from len(haproxyConfig)
		haproxyConfig := "test haproxy config content"
		event := events.NewTemplateRenderedEvent(haproxyConfig, "validation-config", nil, nil, nil, 3, 50)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Template rendered")
		assert.Contains(t, insight, "KB")
		assert.Contains(t, insight, "3 auxiliary")
		assertContainsAttr(t, attrs, "config_bytes", len(haproxyConfig))
		assertContainsAttr(t, attrs, "aux_files", 3)
		assertContainsAttr(t, attrs, "duration_ms", int64(50))
	})

	t.Run("TemplateRenderFailedEvent", func(t *testing.T) {
		event := events.NewTemplateRenderFailedEvent("haproxy.cfg", "undefined variable 'foo'", "")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Template rendering failed")
		assert.Contains(t, insight, "undefined variable 'foo'")
		assertContainsAttr(t, attrs, "template", "haproxy.cfg")
	})
}

func TestEventCommentator_GenerateInsight_DeploymentEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("DeploymentStartedEvent", func(t *testing.T) {
		endpoints := []interface{}{"pod1", "pod2", "pod3"}
		event := events.NewDeploymentStartedEvent(endpoints)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Deployment started")
		assert.Contains(t, insight, "3 HAProxy instances")
		assertContainsAttr(t, attrs, "instance_count", 3)
	})

	t.Run("InstanceDeployedEvent with reload", func(t *testing.T) {
		// endpoint, durationMs, reloadRequired
		event := events.NewInstanceDeployedEvent("pod1", 250, true)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Instance deployed")
		assert.Contains(t, insight, "250ms")
		assert.Contains(t, insight, "reload triggered")
		assertContainsAttr(t, attrs, "reload_required", true)
	})

	t.Run("InstanceDeployedEvent without reload", func(t *testing.T) {
		event := events.NewInstanceDeployedEvent("pod1", 150, false)

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "Instance deployed")
		assert.NotContains(t, insight, "reload triggered")
	})

	t.Run("InstanceDeploymentFailedEvent retryable", func(t *testing.T) {
		// endpoint, err, retryable
		event := events.NewInstanceDeploymentFailedEvent("pod1", "connection refused", true)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "deployment failed")
		assert.Contains(t, insight, "retryable")
		assertContainsAttr(t, attrs, "retryable", true)
	})

	t.Run("DeploymentCompletedEvent", func(t *testing.T) {
		event := events.NewDeploymentCompletedEvent(3, 2, 1, 500)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Deployment completed")
		assert.Contains(t, insight, "2/3")
		assertContainsAttr(t, attrs, "total", 3)
		assertContainsAttr(t, attrs, "succeeded", 2)
		assertContainsAttr(t, attrs, "failed", 1)
	})
}

func TestEventCommentator_GenerateInsight_LeadershipEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("LeaderElectionStartedEvent", func(t *testing.T) {
		event := events.NewLeaderElectionStartedEvent("pod-123", "leader-lease", "kube-system")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Leader election started")
		assert.Contains(t, insight, "pod-123")
		assertContainsAttr(t, attrs, "identity", "pod-123")
		assertContainsAttr(t, attrs, "lease_name", "leader-lease")
	})

	t.Run("BecameLeaderEvent", func(t *testing.T) {
		event := events.NewBecameLeaderEvent("pod-123")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Became leader")
		assert.Contains(t, insight, "pod-123")
		assertContainsAttr(t, attrs, "identity", "pod-123")
	})

	t.Run("LostLeadershipEvent with reason", func(t *testing.T) {
		event := events.NewLostLeadershipEvent("pod-123", "lease expired")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Lost leadership")
		assert.Contains(t, insight, "lease expired")
		assertContainsAttr(t, attrs, "reason", "lease expired")
	})

	t.Run("NewLeaderObservedEvent self", func(t *testing.T) {
		event := events.NewNewLeaderObservedEvent("pod-123", true)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "New leader observed")
		assert.Contains(t, insight, "this replica")
		assertContainsAttr(t, attrs, "is_self", true)
	})

	t.Run("NewLeaderObservedEvent other", func(t *testing.T) {
		event := events.NewNewLeaderObservedEvent("pod-456", false)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "New leader observed")
		assert.Contains(t, insight, "another replica")
		assertContainsAttr(t, attrs, "is_self", false)
	})
}

func TestEventCommentator_GenerateInsight_UnknownEvent(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	// Use the mockEvent from the package
	event := mockEvent{
		eventType: "unknown.custom.event",
		timestamp: time.Now(),
	}

	insight, attrs := ec.generateInsight(event)

	assert.Contains(t, insight, "Event:")
	assert.Contains(t, insight, "unknown.custom.event")
	assertContainsAttr(t, attrs, "event_type", "unknown.custom.event")
}

func TestEventCommentator_ProcessEvent(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	event := events.NewControllerStartedEvent("v1", "s1")

	// Process event
	ec.processEvent(event)

	// Verify it was added to ring buffer
	assert.Equal(t, 1, ec.ringBuffer.Size())

	foundEvents := ec.ringBuffer.FindByType(events.EventTypeControllerStarted)
	require.Len(t, foundEvents, 1)
}

func TestEventCommentator_StartStop(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	ctx, cancel := context.WithCancel(context.Background())

	// Start event bus
	bus.Start()

	// Start commentator in goroutine
	done := make(chan struct{})
	go func() {
		ec.Start(ctx)
		close(done)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Publish an event
	bus.Publish(events.NewControllerStartedEvent("v1", "s1"))

	// Give it time to process
	time.Sleep(50 * time.Millisecond)

	// Verify event was processed
	assert.Equal(t, 1, ec.ringBuffer.Size())

	// Stop via context cancellation
	cancel()

	// Wait for goroutine to finish
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("commentator did not stop in time")
	}
}

func TestEventCommentator_StopViaMethod(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	ctx := context.Background()

	// Start event bus
	bus.Start()

	// Start commentator in goroutine
	done := make(chan struct{})
	go func() {
		ec.Start(ctx)
		close(done)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Stop via Stop() method
	ec.Stop()

	// Wait for goroutine to finish
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("commentator did not stop in time")
	}
}

func TestEventCommentator_EventCorrelation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	// Pre-populate ring buffer with a validation request
	validationRequest := events.NewConfigValidationRequest(nil, "v1")
	ec.ringBuffer.Add(validationRequest)

	// Wait a bit to simulate time passing
	time.Sleep(10 * time.Millisecond)

	// Create a validated event
	validatedEvent := events.NewConfigValidatedEvent(nil, nil, "v1", "s1")

	// Generate insight should include correlation info
	insight, _ := ec.generateInsight(validatedEvent)

	// The insight should contain validation completed timing
	assert.Contains(t, insight, "Configuration validated")
}

func TestEventCommentator_ReconciliationCorrelation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	// Pre-populate with a completed reconciliation
	completedEvent := events.NewReconciliationCompletedEvent(100)
	ec.ringBuffer.Add(completedEvent)

	// Small delay
	time.Sleep(10 * time.Millisecond)

	// Create a new triggered event
	triggeredEvent := events.NewReconciliationTriggeredEvent("resource_change")

	// Generate insight should mention previous reconciliation
	insight, _ := ec.generateInsight(triggeredEvent)

	assert.Contains(t, insight, "Reconciliation triggered")
	// Should mention previous reconciliation timing
	assert.Contains(t, insight, "previous reconciliation")
}

// assertContainsAttr checks if the attrs slice contains a key-value pair.
func assertContainsAttr(t *testing.T, attrs []any, key string, value any) {
	t.Helper()
	for i := 0; i < len(attrs)-1; i += 2 {
		if attrs[i] == key {
			assert.Equal(t, value, attrs[i+1], "attribute %s has wrong value", key)
			return
		}
	}
	t.Errorf("attribute %s not found in attrs", key)
}
