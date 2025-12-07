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

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
	"haproxy-template-ic/pkg/k8s/types"
)

// TestReconciler_DebounceResourceChanges tests that resource changes are properly debounced.
func TestReconciler_DebounceResourceChanges(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Use short debounce interval for faster tests
	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	// Subscribe to reconciliation triggered events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start reconciler in background
	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish a resource change event (not initial sync)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// Wait for debounce timer to expire and reconciliation to trigger
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, receivedEvent, "Should receive ReconciliationTriggeredEvent")
	assert.Equal(t, "debounce_timer", receivedEvent.Reason)
}

// TestReconciler_MultipleChangesResetDebounce tests that multiple changes reset the debounce timer.
func TestReconciler_MultipleChangesResetDebounce(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 200 * time.Millisecond,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Start timing from here to measure total delay
	startTime := time.Now()

	// Publish first change
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait 100ms (half of debounce interval)
	time.Sleep(testutil.DebounceWait)

	// Publish second change - this should reset the timer
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Now wait for debounce to complete (200ms from second event)
	// Total time: 100ms + 200ms = 300ms
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 400*time.Millisecond)

	elapsed := time.Since(startTime)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "debounce_timer", receivedEvent.Reason)

	// Should take at least 250ms (100ms wait + 200ms debounce, with some tolerance)
	assert.Greater(t, elapsed, 250*time.Millisecond,
		"Reconciliation should be delayed by the full debounce interval after the second change")
}

// TestReconciler_ConfigChangeImmediateTrigger tests that config changes trigger immediately.
func TestReconciler_ConfigChangeImmediateTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.EventTimeout, // Long interval
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish config change event
	bus.Publish(events.NewConfigValidatedEvent(nil, nil, "v1", "s1"))

	// Should trigger immediately (within 200ms, not after 500ms debounce)
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "config_change", receivedEvent.Reason)
}

// TestReconciler_SkipInitialSyncEvents tests that initial sync events don't trigger reconciliation.
func TestReconciler_SkipInitialSyncEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish initial sync events
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       10,
		IsInitialSync: true,
	}))

	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Created:       5,
		IsInitialSync: true,
	}))

	// Wait longer than debounce interval, should NOT receive any reconciliation triggered events
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 300*time.Millisecond)
}

// TestReconciler_ConfigCancelsDebounce tests that config changes cancel pending debounce.
func TestReconciler_ConfigCancelsDebounce(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 300 * time.Millisecond,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish resource change (starts debounce timer)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait a bit but not long enough for debounce
	time.Sleep(testutil.DebounceWait)

	// Publish config change (should trigger immediately and cancel debounce)
	bus.Publish(events.NewConfigValidatedEvent(nil, nil, "v2", "s2"))

	// Collect events for a short window
	timeout := time.After(testutil.NoEventTimeout)
	var receivedEvents []*events.ReconciliationTriggeredEvent

Loop:
	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ReconciliationTriggeredEvent); ok {
				receivedEvents = append(receivedEvents, e)
			}
		case <-timeout:
			break Loop
		}
	}

	// Should only receive one event (config_change), not the debounced resource_change
	require.Len(t, receivedEvents, 1, "Should only receive one reconciliation trigger")
	assert.Equal(t, "config_change", receivedEvents[0].Reason)
}

// TestReconciler_ContextCancellation tests graceful shutdown on context cancellation.
func TestReconciler_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger, nil)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start reconciler
	done := make(chan error, 1)
	go func() {
		done <- reconciler.Start(ctx)
	}()

	// Publish a resource change to start debounce
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Cancel context before debounce expires
	time.Sleep(testutil.StartupDelay)
	cancel()

	// Should return quickly
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-time.After(testutil.LongTimeout):
		t.Fatal("Reconciler did not shut down within timeout")
	}

	// Should not have triggered reconciliation
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
}

// TestReconciler_CustomDebounceInterval tests using a custom debounce interval.
func TestReconciler_CustomDebounceInterval(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	customInterval := 150 * time.Millisecond
	config := &Config{
		DebounceInterval: customInterval,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	startTime := time.Now()

	// Publish resource change
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for reconciliation
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.EventTimeout)

	elapsed := time.Since(startTime)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "debounce_timer", receivedEvent.Reason)

	// Verify timing is approximately correct (with tolerance)
	assert.Greater(t, elapsed, customInterval-10*time.Millisecond,
		"Should wait at least the custom debounce interval")
	assert.Less(t, elapsed, customInterval+testutil.DebounceWait,
		"Should not wait significantly longer than the custom debounce interval")
}

// TestReconciler_DefaultConfig tests using default configuration.
func TestReconciler_DefaultConfig(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Pass nil config to use defaults
	reconciler := New(bus, logger, nil)

	assert.Equal(t, DefaultDebounceInterval, reconciler.debounceInterval,
		"Should use default debounce interval when config is nil")
}

// TestReconciler_ZeroDebounceUsesDefault tests that zero debounce interval falls back to default.
func TestReconciler_ZeroDebounceUsesDefault(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 0, // Invalid - should use default
	}

	reconciler := New(bus, logger, config)

	assert.Equal(t, DefaultDebounceInterval, reconciler.debounceInterval,
		"Should use default debounce interval when config value is zero")
}

// TestReconciler_SkipHAProxyPodChanges tests that HAProxy pod changes are filtered out.
//
// HAProxy pods are deployment targets, not configuration sources. Changes to HAProxy pods
// should trigger deployment-only reconciliation via HAProxyPodsDiscoveredEvent → Deployer component,
// not full reconciliation (render + validate + deploy).
func TestReconciler_SkipHAProxyPodChanges(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish haproxy-pods resource change
	bus.Publish(events.NewResourceIndexUpdatedEvent("haproxy-pods", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// Wait longer than debounce interval, should NOT receive any reconciliation triggered events
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 300*time.Millisecond)
}

// TestReconciler_NonHAProxyPodChangesStillTrigger tests that non-HAProxy pod resource changes still trigger reconciliation.
//
// This ensures the haproxy-pods filter doesn't break reconciliation for actual configuration sources.
func TestReconciler_NonHAProxyPodChangesStillTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish ingress resource change (not haproxy-pods)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// Wait for debounce timer to expire and reconciliation to trigger
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, receivedEvent, "Should receive ReconciliationTriggeredEvent for non-HAProxy pod resources")
	assert.Equal(t, "debounce_timer", receivedEvent.Reason)
}

// TestReconciler_Name tests the Name method.
func TestReconciler_Name(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger, nil)

	assert.Equal(t, ComponentName, reconciler.Name())
}

// TestReconciler_HandleHTTPResourceChange tests HTTP resource change handling.
func TestReconciler_HandleHTTPResourceChange(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start reconciler
	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish HTTP resource updated event
	bus.Publish(events.NewHTTPResourceUpdatedEvent("http://example.com/blocklist.txt", "abc123", 1024))

	// Wait for debounce timer to expire and reconciliation to trigger
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, receivedEvent)
	// Note: The reason is "debounce_timer" because the timer fires with that reason
	// The HTTP resource change sets pendingTrigger and lastTriggerReason but
	// the actual event reason comes from triggerReconciliation("debounce_timer")
	assert.Equal(t, "debounce_timer", receivedEvent.Reason)
}

// TestReconciler_HandleHTTPResourceAccepted tests HTTP resource accepted handling.
func TestReconciler_HandleHTTPResourceAccepted(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.LongTimeout, // Long interval to show it's bypassed
	}

	reconciler := New(bus, logger, config)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start reconciler
	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish HTTP resource accepted event
	bus.Publish(events.NewHTTPResourceAcceptedEvent("http://example.com/blocklist.txt", "def456", 2048))

	// Should trigger immediately (not wait for debounce)
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "http_resource_accepted", receivedEvent.Reason)
}

// TestReconciler_HandleEvent_UnknownEvent tests unknown event handling.
func TestReconciler_HandleEvent_UnknownEvent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger, nil)

	// Should not panic for unknown events
	unknownEvent := events.NewValidationStartedEvent()
	reconciler.handleEvent(unknownEvent)
}

// TestReconciler_CleanupStopsTimer tests cleanup stops the debounce timer.
func TestReconciler_CleanupStopsTimer(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 5 * time.Second, // Long interval
	}

	reconciler := New(bus, logger, config)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start reconciler
	errChan := make(chan error, 1)
	go func() {
		errChan <- reconciler.Start(ctx)
	}()
	time.Sleep(testutil.StartupDelay)

	// Trigger resource change to start the timer
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))
	time.Sleep(testutil.StartupDelay)

	// Cancel context which should trigger cleanup
	cancel()

	// Reconciler should exit cleanly
	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(testutil.EventTimeout):
		t.Fatal("Timeout waiting for reconciler to stop")
	}
}
