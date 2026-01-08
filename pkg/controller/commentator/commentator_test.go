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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func TestNewEventCommentator(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()

	ec := NewEventCommentator(bus, logger, 500)

	require.NotNil(t, ec)
	assert.NotNil(t, ec.eventBus)
	assert.NotNil(t, ec.eventChan) // Event channel subscribed in constructor
	assert.NotNil(t, ec.logger)    // Logger is enhanced with component name
	assert.NotNil(t, ec.ringBuffer)
	assert.Equal(t, 500, ec.ringBuffer.Capacity())
	assert.NotNil(t, ec.stopCh)
}

func TestEventCommentator_DetermineLogLevel(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	tests := []struct {
		name  string
		event busevents.Event
		want  slog.Level
	}{
		// Error level events
		{
			name:  "reconciliation failed is error",
			event: mockEvent{eventType: events.EventTypeReconciliationFailed},
			want:  slog.LevelError,
		},
		{
			name:  "template render failed is error",
			event: mockEvent{eventType: events.EventTypeTemplateRenderFailed},
			want:  slog.LevelError,
		},
		{
			name:  "validation failed is error",
			event: mockEvent{eventType: events.EventTypeValidationFailed},
			want:  slog.LevelError,
		},
		{
			name:  "instance deployment failed is error",
			event: mockEvent{eventType: events.EventTypeInstanceDeploymentFailed},
			want:  slog.LevelError,
		},
		{
			name:  "storage sync failed is error",
			event: mockEvent{eventType: events.EventTypeStorageSyncFailed},
			want:  slog.LevelError,
		},
		{
			name:  "webhook validation error is error",
			event: mockEvent{eventType: events.EventTypeWebhookValidationError},
			want:  slog.LevelError,
		},
		{
			name:  "config invalid is error",
			event: mockEvent{eventType: events.EventTypeConfigInvalid},
			want:  slog.LevelError,
		},

		// Warn level events
		{
			name:  "credentials invalid is warn",
			event: mockEvent{eventType: events.EventTypeCredentialsInvalid},
			want:  slog.LevelWarn,
		},
		{
			name:  "webhook validation denied is warn",
			event: mockEvent{eventType: events.EventTypeWebhookValidationDenied},
			want:  slog.LevelWarn,
		},
		{
			name:  "lost leadership is warn",
			event: mockEvent{eventType: events.EventTypeLostLeadership},
			want:  slog.LevelWarn,
		},

		// Info level events
		{
			name:  "controller started is info",
			event: mockEvent{eventType: events.EventTypeControllerStarted},
			want:  slog.LevelInfo,
		},
		{
			name:  "controller shutdown is info",
			event: mockEvent{eventType: events.EventTypeControllerShutdown},
			want:  slog.LevelInfo,
		},
		{
			name:  "config validated is info",
			event: mockEvent{eventType: events.EventTypeConfigValidated},
			want:  slog.LevelInfo,
		},
		{
			name:  "index synchronized is info",
			event: mockEvent{eventType: events.EventTypeIndexSynchronized},
			want:  slog.LevelInfo,
		},
		{
			name:  "reconciliation completed is debug (consolidated in DeploymentCompletedEvent)",
			event: mockEvent{eventType: events.EventTypeReconciliationCompleted},
			want:  slog.LevelDebug,
		},
		{
			name:  "validation completed is debug (consolidated in DeploymentCompletedEvent)",
			event: mockEvent{eventType: events.EventTypeValidationCompleted},
			want:  slog.LevelDebug,
		},
		{
			name: "deployment completed with changes is info",
			event: events.NewDeploymentCompletedEvent(events.DeploymentResult{
				Total:              2,
				Succeeded:          2,
				ReloadsTriggered:   1,
				TotalAPIOperations: 5,
			}),
			want: slog.LevelInfo,
		},
		{
			name: "deployment completed with reloads but no ops is info",
			event: events.NewDeploymentCompletedEvent(events.DeploymentResult{
				Total:              2,
				Succeeded:          2,
				ReloadsTriggered:   1,
				TotalAPIOperations: 0,
			}),
			want: slog.LevelInfo,
		},
		{
			name: "deployment completed with ops but no reloads is info",
			event: events.NewDeploymentCompletedEvent(events.DeploymentResult{
				Total:              2,
				Succeeded:          2,
				ReloadsTriggered:   0,
				TotalAPIOperations: 3,
			}),
			want: slog.LevelInfo,
		},
		{
			name: "deployment completed with no changes is debug",
			event: events.NewDeploymentCompletedEvent(events.DeploymentResult{
				Total:              2,
				Succeeded:          2,
				ReloadsTriggered:   0,
				TotalAPIOperations: 0,
			}),
			want: slog.LevelDebug,
		},
		{
			name:  "leader election started is info",
			event: mockEvent{eventType: events.EventTypeLeaderElectionStarted},
			want:  slog.LevelInfo,
		},
		{
			name:  "became leader is info",
			event: mockEvent{eventType: events.EventTypeBecameLeader},
			want:  slog.LevelInfo,
		},
		{
			name:  "new leader observed is info",
			event: mockEvent{eventType: events.EventTypeNewLeaderObserved},
			want:  slog.LevelInfo,
		},

		// Debug level (default)
		{
			name:  "config parsed is debug",
			event: mockEvent{eventType: events.EventTypeConfigParsed},
			want:  slog.LevelDebug,
		},
		{
			name:  "resource index updated is debug",
			event: mockEvent{eventType: events.EventTypeResourceIndexUpdated},
			want:  slog.LevelDebug,
		},
		{
			name:  "template rendered is debug",
			event: mockEvent{eventType: events.EventTypeTemplateRendered},
			want:  slog.LevelDebug,
		},
		{
			name:  "unknown event type is debug",
			event: mockEvent{eventType: "unknown.event"},
			want:  slog.LevelDebug,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ec.determineLogLevel(tt.event)
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
		event := events.NewConfigInvalidEvent("v4.0.0", nil, validationErrors)

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
		// haproxyConfig, validationHAProxyConfig, validationPaths, auxiliaryFiles, validationAuxiliaryFiles, auxFileCount, durationMs, triggerReason
		// ConfigBytes is calculated from len(haproxyConfig)
		haproxyConfig := "test haproxy config content"
		event := events.NewTemplateRenderedEvent(haproxyConfig, "validation-config", nil, nil, nil, 3, 50, "")

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
		event := events.NewDeploymentCompletedEvent(events.DeploymentResult{
			Total:              3,
			Succeeded:          2,
			Failed:             1,
			DurationMs:         500,
			ReloadsTriggered:   1,
			TotalAPIOperations: 10,
		})

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Reconciliation")
		assertContainsAttr(t, attrs, "instances", "2/3")
		assertContainsAttr(t, attrs, "reloads", 1)
		assertContainsAttr(t, attrs, "ops", 10)
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

func TestEventCommentator_GenerateInsight_IndexEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("IndexSynchronizedEvent", func(t *testing.T) {
		resourceCounts := map[string]int{
			"services":  5,
			"ingresses": 10,
		}
		event := events.NewIndexSynchronizedEvent(resourceCounts)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "synchronized")
		assert.Contains(t, insight, "15 resources")
		assert.Contains(t, insight, "2 types")
		assertContainsAttr(t, attrs, "resource_types", 2)
		assertContainsAttr(t, attrs, "total_resources", 15)
	})

	t.Run("ResourceIndexUpdatedEvent", func(t *testing.T) {
		changeStats := types.ChangeStats{
			Created:       2,
			Modified:      1,
			Deleted:       0,
			IsInitialSync: false,
		}
		event := events.NewResourceIndexUpdatedEvent("services", changeStats)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Resource index updated")
		assert.Contains(t, insight, "services")
		assertContainsAttr(t, attrs, "resource_type", "services")
	})

	t.Run("ResourceIndexUpdatedEvent during initial sync", func(t *testing.T) {
		changeStats := types.ChangeStats{
			Created:       5,
			Modified:      0,
			Deleted:       0,
			IsInitialSync: true,
		}
		event := events.NewResourceIndexUpdatedEvent("ingresses", changeStats)

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "loading")
	})

	t.Run("ResourceSyncCompleteEvent", func(t *testing.T) {
		event := events.NewResourceSyncCompleteEvent("services", 25)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Initial sync complete")
		assert.Contains(t, insight, "services")
		assert.Contains(t, insight, "25 resources")
		assertContainsAttr(t, attrs, "resource_type", "services")
		assertContainsAttr(t, attrs, "initial_count", 25)
	})
}

func TestEventCommentator_GenerateInsight_ValidationEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("ConfigValidationRequest", func(t *testing.T) {
		event := events.NewConfigValidationRequest(nil, "v1.0.0")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Configuration validation started")
		assertContainsAttr(t, attrs, "version", "v1.0.0")
	})

	t.Run("ConfigValidationResponse valid", func(t *testing.T) {
		response := events.NewConfigValidationResponse(
			"req-123",
			"template",
			true,
			nil,
		)

		insight, attrs := ec.generateInsight(response)

		assert.Contains(t, insight, "Validator")
		assert.Contains(t, insight, "template")
		assertContainsAttr(t, attrs, "validator", "template")
		assertContainsAttr(t, attrs, "valid", true)
	})

	t.Run("ConfigValidationResponse invalid", func(t *testing.T) {
		response := events.NewConfigValidationResponse(
			"req-123",
			"basic",
			false,
			[]string{"field required"},
		)

		insight, attrs := ec.generateInsight(response)

		assert.Contains(t, insight, "Validator")
		assert.Contains(t, insight, "basic")
		assert.Contains(t, insight, "FAILED")
		assertContainsAttr(t, attrs, "valid", false)
		assertContainsAttr(t, attrs, "error_count", 1)
	})
}

func TestEventCommentator_GenerateInsight_CertEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("CertResourceChangedEvent", func(t *testing.T) {
		// The event takes an interface{} representing the resource
		event := events.NewCertResourceChangedEvent("cert-secret")

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "certificate Secret changed")
	})

	t.Run("CertParsedEvent", func(t *testing.T) {
		// Signature: (certPEM, keyPEM []byte, version string)
		event := events.NewCertParsedEvent([]byte("cert-data"), []byte("key-data"), "v1.0.0")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "certificates parsed")
		assert.Contains(t, insight, "v1.0.0")
		assertContainsAttr(t, attrs, "version", "v1.0.0")
	})
}

func TestEventCommentator_GenerateInsight_ValidationTestEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("ValidationStartedEvent", func(t *testing.T) {
		event := events.NewValidationStartedEvent()

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "validation started")
	})

	t.Run("ValidationCompletedEvent without warnings", func(t *testing.T) {
		event := events.NewValidationCompletedEvent(nil, 150, "")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "validation succeeded")
		assert.Contains(t, insight, "150ms")
		assertContainsAttr(t, attrs, "duration_ms", int64(150))
	})

	t.Run("ValidationCompletedEvent with warnings", func(t *testing.T) {
		event := events.NewValidationCompletedEvent([]string{"warning1", "warning2"}, 200, "")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "validation succeeded")
		assert.Contains(t, insight, "2 warnings")
		assertContainsAttr(t, attrs, "warnings", 2)
	})

	t.Run("ValidationFailedEvent", func(t *testing.T) {
		event := events.NewValidationFailedEvent([]string{"error1", "error2", "error3"}, 100, "")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "validation failed")
		assert.Contains(t, insight, "3 errors")
		assertContainsAttr(t, attrs, "error_count", 3)
	})

	t.Run("ValidationTestsStartedEvent", func(t *testing.T) {
		event := events.NewValidationTestsStartedEvent(5)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Starting validation tests")
		assert.Contains(t, insight, "5 tests")
		assertContainsAttr(t, attrs, "test_count", 5)
	})

	t.Run("ValidationTestsCompletedEvent", func(t *testing.T) {
		event := events.NewValidationTestsCompletedEvent(10, 8, 2, 500)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Validation tests completed")
		assert.Contains(t, insight, "8 passed")
		assert.Contains(t, insight, "2 failed")
		assertContainsAttr(t, attrs, "total_tests", 10)
	})

	t.Run("ValidationTestsFailedEvent", func(t *testing.T) {
		event := events.NewValidationTestsFailedEvent([]string{"test1", "test2"})

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Validation tests failed")
		assert.Contains(t, insight, "2 tests")
		assertContainsAttr(t, attrs, "failed_count", 2)
	})
}

func TestEventCommentator_GenerateInsight_StorageEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("StorageSyncStartedEvent", func(t *testing.T) {
		endpoints := []interface{}{"pod1", "pod2"}
		event := events.NewStorageSyncStartedEvent("maps", endpoints)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Auxiliary file sync started")
		assert.Contains(t, insight, "maps phase")
		assert.Contains(t, insight, "2 instances")
		assertContainsAttr(t, attrs, "phase", "maps")
	})

	t.Run("StorageSyncCompletedEvent", func(t *testing.T) {
		event := events.NewStorageSyncCompletedEvent("maps", nil, 300)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Auxiliary file sync completed")
		assert.Contains(t, insight, "maps phase")
		assertContainsAttr(t, attrs, "duration_ms", int64(300))
	})

	t.Run("StorageSyncFailedEvent", func(t *testing.T) {
		event := events.NewStorageSyncFailedEvent("maps", "connection refused")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Auxiliary file sync failed")
		assert.Contains(t, insight, "maps phase")
		assert.Contains(t, insight, "connection refused")
		assertContainsAttr(t, attrs, "error", "connection refused")
	})
}

func TestEventCommentator_GenerateInsight_HAProxyPodEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("HAProxyPodsDiscoveredEvent", func(t *testing.T) {
		endpoints := []interface{}{"pod1", "pod2", "pod3"}
		event := events.NewHAProxyPodsDiscoveredEvent(endpoints, 3)

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "HAProxy pods discovered")
		assert.Contains(t, insight, "3 instances")
		assertContainsAttr(t, attrs, "count", 3)
	})

	t.Run("HAProxyPodAddedEvent", func(t *testing.T) {
		event := events.NewHAProxyPodAddedEvent("pod-123")

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "HAProxy pod added")
	})

	t.Run("HAProxyPodRemovedEvent", func(t *testing.T) {
		event := events.NewHAProxyPodRemovedEvent("pod-123")

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "HAProxy pod removed")
	})

	t.Run("HAProxyPodTerminatedEvent", func(t *testing.T) {
		event := events.NewHAProxyPodTerminatedEvent("haproxy-123", "haproxy-system")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "HAProxy pod terminated")
		assert.Contains(t, insight, "haproxy-system/haproxy-123")
		assertContainsAttr(t, attrs, "pod_name", "haproxy-123")
	})
}

func TestEventCommentator_GenerateInsight_WebhookObservabilityEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("WebhookValidationRequestEvent", func(t *testing.T) {
		event := events.NewWebhookValidationRequestEvent("uid-123", "Ingress", "my-ingress", "default", "CREATE")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Webhook validation request")
		assert.Contains(t, insight, "CREATE")
		assert.Contains(t, insight, "Ingress")
		assertContainsAttr(t, attrs, "operation", "CREATE")
	})

	t.Run("WebhookValidationRequestEvent without namespace", func(t *testing.T) {
		event := events.NewWebhookValidationRequestEvent("uid-123", "ClusterRole", "my-role", "", "UPDATE")

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "Webhook validation request")
		assert.Contains(t, insight, "my-role")
	})

	t.Run("WebhookValidationAllowedEvent", func(t *testing.T) {
		event := events.NewWebhookValidationAllowedEvent("uid-123", "Ingress", "my-ingress", "default")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Webhook validation allowed")
		assert.Contains(t, insight, "Ingress")
		assertContainsAttr(t, attrs, "kind", "Ingress")
	})

	t.Run("WebhookValidationDeniedEvent", func(t *testing.T) {
		event := events.NewWebhookValidationDeniedEvent("uid-123", "Ingress", "my-ingress", "default", "invalid backend")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Webhook validation denied")
		assert.Contains(t, insight, "invalid backend")
		assertContainsAttr(t, attrs, "reason", "invalid backend")
	})

	t.Run("WebhookValidationDeniedEvent without namespace", func(t *testing.T) {
		event := events.NewWebhookValidationDeniedEvent("uid-123", "ClusterRole", "my-role", "", "forbidden")

		insight, _ := ec.generateInsight(event)

		assert.Contains(t, insight, "Webhook validation denied")
		assert.Contains(t, insight, "my-role")
	})

	t.Run("WebhookValidationErrorEvent", func(t *testing.T) {
		event := events.NewWebhookValidationErrorEvent("uid-123", "Ingress", "internal error")

		insight, attrs := ec.generateInsight(event)

		assert.Contains(t, insight, "Webhook validation error")
		assert.Contains(t, insight, "internal error")
		assertContainsAttr(t, attrs, "error", "internal error")
	})
}

func TestEventCommentator_GenerateInsight_CredentialsEvents(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("CredentialsInvalidEvent falls through to default", func(t *testing.T) {
		// CredentialsInvalidEvent is not explicitly handled, falls through to default case
		event := events.NewCredentialsInvalidEvent("v1.0.0", "missing password field")

		insight, attrs := ec.generateInsight(event)

		// Default case just returns "Event: {event_type}"
		assert.Contains(t, insight, "Event:")
		assert.Contains(t, insight, events.EventTypeCredentialsInvalid)
		assertContainsAttr(t, attrs, "event_type", events.EventTypeCredentialsInvalid)
	})
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

func TestEventCommentator_AppendCorrelation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	t.Run("adds all correlation fields for CorrelatedEvent", func(t *testing.T) {
		// Create event with correlation
		event := events.NewReconciliationTriggeredEvent("test", events.WithNewCorrelation())
		eventID := event.EventID()
		correlationID := event.CorrelationID()

		// Start with base attrs
		attrs := []any{"event_type", "test"}

		// Append correlation
		result := ec.appendCorrelation(event, attrs)

		// Verify event_id and correlation_id were added (6 items: event_type, test, event_id, uuid, correlation_id, uuid)
		assert.Len(t, result, 6)
		assertContainsAttr(t, result, "event_id", eventID)
		assertContainsAttr(t, result, "correlation_id", correlationID)
	})

	t.Run("adds only event_id when no correlation", func(t *testing.T) {
		// Create event without correlation - still gets event_id
		event := events.NewReconciliationTriggeredEvent("test")
		eventID := event.EventID()

		// Start with base attrs
		attrs := []any{"event_type", "test"}

		// Append correlation - should add event_id but not correlation_id
		result := ec.appendCorrelation(event, attrs)

		// Verify only event_id was added (4 items: event_type, test, event_id, uuid)
		assert.Len(t, result, 4)
		assertContainsAttr(t, result, "event_id", eventID)
	})

	t.Run("skips non-CorrelatedEvent", func(t *testing.T) {
		// Create event that does not implement CorrelatedEvent
		event := events.NewControllerStartedEvent("v1", "s1")

		// Start with base attrs
		attrs := []any{"event_type", "test"}

		// Append correlation - should not modify attrs
		result := ec.appendCorrelation(event, attrs)

		// Verify attrs unchanged
		assert.Len(t, result, 2)
	})
}

func TestEventCommentator_Name(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	assert.Equal(t, ComponentName, ec.Name())
	assert.Equal(t, "commentator", ec.Name())
}

func TestEventCommentator_FindByCorrelationID(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	// Add events with correlation
	event1 := events.NewReconciliationTriggeredEvent("test", events.WithNewCorrelation())
	correlationID := event1.CorrelationID()
	ec.ringBuffer.Add(event1)

	// Add event with same correlation (would be in same cycle)
	event2 := events.NewReconciliationStartedEvent("test", events.WithCorrelation(correlationID, event1.EventID()))
	ec.ringBuffer.Add(event2)

	// Add event with different correlation
	event3 := events.NewReconciliationTriggeredEvent("other", events.WithNewCorrelation())
	ec.ringBuffer.Add(event3)

	// Find by correlation ID
	found := ec.FindByCorrelationID(correlationID, 10)
	assert.Len(t, found, 2)

	// Find with max count = 1
	found = ec.FindByCorrelationID(correlationID, 1)
	assert.Len(t, found, 1)

	// Find with empty correlation ID
	found = ec.FindByCorrelationID("", 10)
	assert.Nil(t, found)

	// Find with non-existent correlation ID
	found = ec.FindByCorrelationID("non-existent", 10)
	assert.Empty(t, found)
}

func TestEventCommentator_FindRecent(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	ec := NewEventCommentator(bus, logger, 100)

	// Add some events
	event1 := events.NewControllerStartedEvent("v1", "s1")
	event2 := events.NewConfigParsedEvent(nil, nil, "v1", "s1")
	event3 := events.NewConfigValidatedEvent(nil, nil, "v1", "s1")

	ec.ringBuffer.Add(event1)
	time.Sleep(10 * time.Millisecond)
	ec.ringBuffer.Add(event2)
	time.Sleep(10 * time.Millisecond)
	ec.ringBuffer.Add(event3)

	// Find 2 most recent
	found := ec.FindRecent(2)
	assert.Len(t, found, 2)
	// Newest first
	assert.Equal(t, events.EventTypeConfigValidated, found[0].EventType())
	assert.Equal(t, events.EventTypeConfigParsed, found[1].EventType())

	// Find more than available
	found = ec.FindRecent(10)
	assert.Len(t, found, 3)

	// Find 0
	found = ec.FindRecent(0)
	assert.Empty(t, found)
}
