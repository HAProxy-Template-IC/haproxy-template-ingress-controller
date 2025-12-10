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

package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
	busevents "haproxy-template-ic/pkg/events"
)

// TestExecutor_BasicReconciliationFlow tests the basic event flow of a reconciliation cycle.
// The flow is: ReconciliationTriggered -> ReconciliationStarted -> ValidationCompleted -> ReconciliationCompleted.
func TestExecutor_BasicReconciliationFlow(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to all events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation with correlation ID
	triggeredEvent := events.NewReconciliationTriggeredEvent("test_trigger", events.WithNewCorrelation())
	bus.Publish(triggeredEvent)

	// Wait for started event
	receivedStarted := testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)
	require.NotNil(t, receivedStarted, "Should receive ReconciliationStartedEvent")

	// Simulate validation completing (with same correlation ID)
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(triggeredEvent)))

	// Wait for completed event (now published after validation)
	receivedCompleted := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	require.NotNil(t, receivedCompleted, "Should receive ReconciliationCompletedEvent")

	// Verify event content
	assert.Equal(t, "test_trigger", receivedStarted.Trigger)
	assert.GreaterOrEqual(t, receivedCompleted.DurationMs, int64(0))
}

// TestExecutor_EventOrder tests that events are published in the correct order.
// The flow is: ReconciliationTriggered -> ReconciliationStarted -> ValidationCompleted -> ReconciliationCompleted.
func TestExecutor_EventOrder(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation with correlation ID
	triggeredEvent := events.NewReconciliationTriggeredEvent("order_test", events.WithNewCorrelation())
	bus.Publish(triggeredEvent)

	// Wait for started event first
	startedEvent := testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)
	require.NotNil(t, startedEvent, "Should receive ReconciliationStartedEvent")

	// Simulate validation completing (with same correlation ID)
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(triggeredEvent)))

	// Collect events in order
	timeout := time.After(testutil.EventTimeout)
	var orderedEvents []busevents.Event
	orderedEvents = append(orderedEvents, startedEvent) // Already received

	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.ReconciliationCompletedEvent); ok {
				orderedEvents = append(orderedEvents, event)
			}

			// Stop after receiving completed event
			if len(orderedEvents) >= 2 {
				goto Done
			}

		case <-timeout:
			t.Fatal("Timeout waiting for reconciliation events")
		}
	}

Done:
	require.Len(t, orderedEvents, 2, "Should receive exactly 2 reconciliation events")

	// Verify order: Started comes before Completed
	_, isStarted := orderedEvents[0].(*events.ReconciliationStartedEvent)
	_, isCompleted := orderedEvents[1].(*events.ReconciliationCompletedEvent)

	assert.True(t, isStarted, "First event should be ReconciliationStartedEvent")
	assert.True(t, isCompleted, "Second event should be ReconciliationCompletedEvent")
}

// TestExecutor_MultipleReconciliations tests handling of multiple reconciliation triggers.
// Each reconciliation requires a matching ValidationCompletedEvent with the same correlation ID.
func TestExecutor_MultipleReconciliations(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger multiple reconciliations with correlation IDs
	trigger1 := events.NewReconciliationTriggeredEvent("trigger_1", events.WithNewCorrelation())
	trigger2 := events.NewReconciliationTriggeredEvent("trigger_2", events.WithNewCorrelation())
	trigger3 := events.NewReconciliationTriggeredEvent("trigger_3", events.WithNewCorrelation())

	bus.Publish(trigger1)
	bus.Publish(trigger2)
	bus.Publish(trigger3)

	// Wait for started events
	time.Sleep(testutil.StartupDelay)

	// Simulate validation completing for all triggers (with matching correlation IDs)
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(trigger1)))
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(trigger2)))
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(trigger3)))

	// Collect completed events
	timeout := time.After(testutil.LongTimeout)
	var completedEvents []*events.ReconciliationCompletedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ReconciliationCompletedEvent); ok {
				completedEvents = append(completedEvents, e)
			}

			// Stop after receiving 3 completed events
			if len(completedEvents) >= 3 {
				goto Done
			}

		case <-timeout:
			t.Fatalf("Timeout waiting for reconciliation events, received %d", len(completedEvents))
		}
	}

Done:
	assert.Len(t, completedEvents, 3, "Should complete all 3 reconciliations")

	// Verify all have valid durations
	for i, event := range completedEvents {
		assert.GreaterOrEqual(t, event.DurationMs, int64(0),
			"Reconciliation %d should have non-negative duration", i+1)
	}
}

// TestExecutor_DurationMeasurement tests that reconciliation duration is measured.
// Duration is measured from trigger to validation completion.
func TestExecutor_DurationMeasurement(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation with correlation ID
	triggeredEvent := events.NewReconciliationTriggeredEvent("duration_test", events.WithNewCorrelation())
	bus.Publish(triggeredEvent)

	// Wait a bit to simulate render+validate time
	time.Sleep(50 * time.Millisecond)

	// Simulate validation completing (with same correlation ID)
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(triggeredEvent)))

	// Wait for completion event
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, completedEvent)

	// Duration should be measured (at least 50ms due to the sleep)
	assert.GreaterOrEqual(t, completedEvent.DurationMs, int64(50))
	// Duration should be reasonable (< 500ms for test)
	assert.Less(t, completedEvent.DurationMs, int64(500),
		"Duration should be reasonable for test")
}

// TestExecutor_ContextCancellation tests graceful shutdown on context cancellation.
func TestExecutor_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start executor
	done := make(chan error, 1)
	go func() {
		done <- executor.Start(ctx)
	}()

	// Give it time to start
	time.Sleep(testutil.StartupDelay)

	// Trigger a reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("cancel_test"))

	// Wait a bit for the reconciliation to start
	time.Sleep(testutil.StartupDelay)

	// Cancel context
	cancel()

	// Should return quickly
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-time.After(testutil.LongTimeout):
		t.Fatal("Executor did not shut down within timeout")
	}

	// The reconciliation that was in progress should have completed
	// (our stub implementation is fast enough to finish before cancellation)
	// But we shouldn't crash or hang
	select {
	case event := <-eventChan:
		// It's okay to receive events that were published before cancellation
		_ = event
	default:
		// It's also okay if the channel is empty
	}
}

// TestExecutor_IgnoresUnrelatedEvents tests that executor only handles expected events.
func TestExecutor_IgnoresUnrelatedEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish various unrelated events
	bus.Publish(events.NewConfigParsedEvent(nil, nil, "v1", "s1"))
	bus.Publish(events.NewIndexSynchronizedEvent(map[string]int{"ingresses": 10}))

	// Wait a bit
	time.Sleep(testutil.DebounceWait)

	// Should not receive any reconciliation events
	select {
	case event := <-eventChan:
		switch event.(type) {
		case *events.ReconciliationStartedEvent, *events.ReconciliationCompletedEvent:
			t.Fatal("Should not process unrelated events")
		}
	default:
		// Expected - no reconciliation events
	}

	// Now trigger actual reconciliation with correlation ID
	triggeredEvent := events.NewReconciliationTriggeredEvent("real_trigger", events.WithNewCorrelation())
	bus.Publish(triggeredEvent)

	// Wait for started event
	startedEvent := testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)
	assert.NotNil(t, startedEvent, "Should receive ReconciliationStartedEvent")

	// Simulate validation completing (with same correlation ID)
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", events.PropagateCorrelation(triggeredEvent)))

	// Should receive reconciliation completed event
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.NotNil(t, completedEvent, "Should process ReconciliationTriggeredEvent")
}

// TestExecutor_Name tests the Name() method.
func TestExecutor_Name(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	assert.Equal(t, ComponentName, executor.Name())
	assert.Equal(t, "executor", executor.Name())
}

// TestExecutor_HandleTemplateRendered tests handling of template rendered event.
func TestExecutor_HandleTemplateRendered(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish template rendered event with correct signature
	bus.Publish(events.NewTemplateRenderedEvent(
		"config content",    // haproxyConfig
		"validation config", // validationHAProxyConfig
		nil,                 // validationPaths
		nil,                 // auxiliaryFiles
		nil,                 // validationAuxiliaryFiles
		2,                   // auxFileCount
		100,                 // durationMs
		"",                  // triggerReason
	))

	// Wait a bit for processing
	time.Sleep(testutil.DebounceWait)

	// The executor logs the event but doesn't publish new events
	// Just verify it doesn't crash and event was processed
	// Drain the event channel
	select {
	case <-eventChan:
		// Event received (the TemplateRenderedEvent we published)
	default:
		// No problem if nothing received
	}
}

// TestExecutor_HandleTemplateRenderFailed tests handling of template render failed event.
func TestExecutor_HandleTemplateRenderFailed(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish template render failed event
	bus.Publish(events.NewTemplateRenderFailedEvent("haproxy.cfg", "template syntax error at line 42", ""))

	// Wait for ReconciliationFailedEvent
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, failedEvent)
	assert.Equal(t, "template syntax error at line 42", failedEvent.Error)
	assert.Equal(t, "render", failedEvent.Phase)
}

// TestExecutor_HandleValidationCompleted tests handling of validation completed event.
// When validation completes with a tracked correlation ID, ReconciliationCompletedEvent is published.
func TestExecutor_HandleValidationCompleted(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First trigger a reconciliation to track the correlation ID
	triggeredEvent := events.NewReconciliationTriggeredEvent("test", events.WithNewCorrelation())
	bus.Publish(triggeredEvent)
	time.Sleep(testutil.StartupDelay)

	// Publish validation completed event with matching correlation ID
	bus.Publish(events.NewValidationCompletedEvent([]string{"warning: unused acl"}, 50, "", events.PropagateCorrelation(triggeredEvent)))

	// Should receive ReconciliationCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	require.NotNil(t, completedEvent, "Should receive ReconciliationCompletedEvent")
	assert.GreaterOrEqual(t, completedEvent.DurationMs, int64(0))
}

// TestExecutor_HandleValidationCompleted_NoWarnings tests handling with no warnings.
// When validation completes with a tracked correlation ID, ReconciliationCompletedEvent is published.
func TestExecutor_HandleValidationCompleted_NoWarnings(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First trigger a reconciliation to track the correlation ID
	triggeredEvent := events.NewReconciliationTriggeredEvent("test", events.WithNewCorrelation())
	bus.Publish(triggeredEvent)
	time.Sleep(testutil.StartupDelay)

	// Publish validation completed event with no warnings but matching correlation ID
	bus.Publish(events.NewValidationCompletedEvent(nil, 30, "", events.PropagateCorrelation(triggeredEvent)))

	// Should receive ReconciliationCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	require.NotNil(t, completedEvent, "Should receive ReconciliationCompletedEvent")
}

// TestExecutor_HandleValidationFailed tests handling of validation failed event.
func TestExecutor_HandleValidationFailed(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish validation failed event
	bus.Publish(events.NewValidationFailedEvent([]string{"invalid backend reference", "duplicate frontend name"}, 75, ""))

	// Wait for ReconciliationFailedEvent
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, failedEvent)
	assert.Equal(t, "invalid backend reference", failedEvent.Error) // First error
	assert.Equal(t, "validate", failedEvent.Phase)
}

// TestExecutor_HandleValidationFailed_NoErrors tests handling with empty errors.
func TestExecutor_HandleValidationFailed_NoErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start executor in background
	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish validation failed event with empty errors
	bus.Publish(events.NewValidationFailedEvent([]string{}, 50, ""))

	// Wait for ReconciliationFailedEvent
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, failedEvent)
	assert.Equal(t, "validation failed", failedEvent.Error) // Fallback message
	assert.Equal(t, "validate", failedEvent.Phase)
}

// TestExecutor_ReasonPropagation tests that trigger reason is propagated to started event.
func TestExecutor_ReasonPropagation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	executor := New(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go executor.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Test various trigger reasons
	testReasons := []string{
		"debounce_timer",
		"config_change",
		"manual_trigger",
		"special_reason_123",
	}

	for _, reason := range testReasons {
		// Trigger reconciliation
		bus.Publish(events.NewReconciliationTriggeredEvent(reason))

		// Wait for started event
		startedEvent := testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)

		require.NotNil(t, startedEvent, "Should receive started event for reason: %s", reason)
		assert.Equal(t, reason, startedEvent.Trigger,
			"Started event should have same trigger reason: %s", reason)

		// Drain remaining events before next iteration
		testutil.DrainChannel(eventChan)
	}
}
