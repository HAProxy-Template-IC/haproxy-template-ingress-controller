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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func TestReconciler_LeadingEdgeTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Use longer debounce interval to verify immediate trigger
	config := &Config{
		DebounceInterval: 500 * time.Millisecond,
	}

	reconciler := New(bus, logger, config)

	// Subscribe to reconciliation triggered events
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start reconciler in background
	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	startTime := time.Now()

	// Publish a resource change event (not initial sync)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// Should trigger immediately (leading-edge), not after debounce timer
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	elapsed := time.Since(startTime)

	require.NotNil(t, receivedEvent, "Should receive ReconciliationTriggeredEvent")
	assert.Equal(t, "resource_change", receivedEvent.Reason)
	assert.Less(t, elapsed, 100*time.Millisecond,
		"First change should trigger immediately, not wait for debounce timer")
}

// and trigger when refractory ends (timer does NOT reset, unlike old trailing-edge behavior).
func TestReconciler_RefractoryBatching(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 200 * time.Millisecond,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish first change - should trigger immediately (leading-edge)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for first (immediate) trigger
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	firstTriggerTime := time.Now()

	// Wait 50ms (inside refractory period)
	time.Sleep(50 * time.Millisecond)

	// Publish second change during refractory - should be queued, NOT reset timer
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Wait for second trigger (should come at ~200ms from first trigger, NOT ~250ms)
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 300*time.Millisecond)

	elapsed := time.Since(firstTriggerTime)

	require.NotNil(t, secondEvent)
	assert.Equal(t, "debounce_timer", secondEvent.Reason)

	// Timer should NOT have been reset by second change
	// Should trigger around 200ms (refractory) from first trigger, not 200ms + 50ms = 250ms
	assert.Less(t, elapsed, 220*time.Millisecond,
		"Second trigger should happen at refractory end, timer should NOT reset")
	assert.Greater(t, elapsed, 140*time.Millisecond,
		"Second trigger should wait for remaining refractory period")
}

// This is the key property of leading-edge debouncing - changes are never delayed longer than the interval.
func TestReconciler_MaxLatencyGuarantee(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	debounceInterval := 200 * time.Millisecond
	config := &Config{
		DebounceInterval: debounceInterval,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Simulate rapid changes like a rolling deployment
	// Change at t=0: triggers immediately
	changeTime := time.Now()
	bus.Publish(events.NewResourceIndexUpdatedEvent("endpoints", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Wait for immediate trigger
	testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	// Change at t=50ms: queued
	time.Sleep(50 * time.Millisecond)
	bus.Publish(events.NewResourceIndexUpdatedEvent("endpoints", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Change at t=100ms: still queued (timer not reset)
	time.Sleep(50 * time.Millisecond)
	bus.Publish(events.NewResourceIndexUpdatedEvent("endpoints", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Wait for second trigger
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 200*time.Millisecond)
	require.NotNil(t, secondEvent)

	// The change at t=100ms should be synced by ~t=200ms (debounce interval from first trigger)
	// Total latency for last change: 200ms - 100ms = 100ms < debounceInterval (200ms)
	elapsed := time.Since(changeTime)
	assert.Less(t, elapsed, debounceInterval+50*time.Millisecond,
		"All changes should be synced within debounceInterval from first trigger")
}

// trigger immediately again (not queued).
func TestReconciler_ImmediateTriggerAfterRefractoryEnds(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	debounceInterval := 100 * time.Millisecond
	config := &Config{
		DebounceInterval: debounceInterval,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First change: triggers immediately
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// Wait for refractory period to fully expire
	time.Sleep(debounceInterval + 50*time.Millisecond)

	// Second change (after refractory): should trigger immediately again
	startTime := time.Now()
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	elapsed := time.Since(startTime)

	require.NotNil(t, secondEvent)
	assert.Equal(t, "resource_change", secondEvent.Reason)
	assert.Less(t, elapsed, 50*time.Millisecond,
		"Change after refractory should trigger immediately")
}

func TestReconciler_IndexSynchronizedTriggersImmediate(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.EventTimeout, // Long interval
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish index synchronized event
	resourceCounts := map[string]int{
		"ingresses": 10,
		"services":  5,
	}
	bus.Publish(events.NewIndexSynchronizedEvent(resourceCounts))

	// Should trigger immediately (within 200ms, not after long debounce)
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "index_synchronized", receivedEvent.Reason)
}

func TestReconciler_SkipInitialSyncEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
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

// triggers immediately and cancels any pending debounce.
// With leading-edge behavior, the first resource change also triggers immediately,
// so we expect two events: resource_change (immediate) and index_synchronized.
func TestReconciler_IndexSynchronizedCancelsDebounce(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 300 * time.Millisecond,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish first resource change - triggers immediately (leading-edge)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for immediate trigger
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// Publish second resource change during refractory (queued, not immediate)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Publish index synchronized event (should trigger immediately and cancel pending)
	bus.Publish(events.NewIndexSynchronizedEvent(map[string]int{"ingresses": 10}))

	// Wait for index_synchronized event
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, secondEvent)
	assert.Equal(t, "index_synchronized", secondEvent.Reason)

	// Verify no more events (the queued resource_change was cancelled)
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
}

// Verifies that pending refractory timers are properly cleaned up on shutdown.
func TestReconciler_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 300 * time.Millisecond,
	}
	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start reconciler
	done := make(chan error, 1)
	go func() {
		done <- reconciler.Start(ctx)
	}()

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish first resource change - triggers immediately (leading-edge)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for the immediate trigger
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// Publish second resource change during refractory (starts pending timer)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Cancel context before refractory expires
	cancel()

	// Should return quickly
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-time.After(testutil.LongTimeout):
		t.Fatal("Reconciler did not shut down within timeout")
	}

	// The pending refractory trigger should NOT have fired after shutdown
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestReconciler_CustomDebounceInterval(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	customInterval := 150 * time.Millisecond
	config := &Config{
		DebounceInterval: customInterval,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish first resource change - should trigger immediately
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for immediate trigger
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	firstTriggerTime := time.Now()

	// Wait 50ms, then publish second change (during refractory)
	time.Sleep(50 * time.Millisecond)
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Wait for second trigger (should come at custom interval from first trigger)
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 200*time.Millisecond)
	elapsed := time.Since(firstTriggerTime)

	require.NotNil(t, secondEvent)
	assert.Equal(t, "debounce_timer", secondEvent.Reason)

	// Verify refractory timing uses custom interval
	assert.Greater(t, elapsed, customInterval-30*time.Millisecond,
		"Should wait for custom refractory period")
	assert.Less(t, elapsed, customInterval+50*time.Millisecond,
		"Should not wait significantly longer than custom refractory period")
}

func TestReconciler_DefaultConfig(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Pass nil config to use defaults
	reconciler := New(bus, logger, nil)

	assert.Equal(t, DefaultDebounceInterval, reconciler.debounceInterval,
		"Should use default debounce interval when config is nil")
}

func TestReconciler_ZeroDebounceUsesDefault(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 0, // Invalid - should use default
	}

	reconciler := New(bus, logger, config)

	assert.Equal(t, DefaultDebounceInterval, reconciler.debounceInterval,
		"Should use default debounce interval when config value is zero")
}

// HAProxy pods are deployment targets, not configuration sources. Changes to HAProxy pods
// should trigger deployment-only reconciliation via HAProxyPodsDiscoveredEvent → Deployer component,
// not full reconciliation (render + validate + deploy).
func TestReconciler_SkipHAProxyPodChanges(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
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

// This ensures the haproxy-pods filter doesn't break reconciliation for actual configuration sources.
func TestReconciler_NonHAProxyPodChangesStillTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
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

	// Should trigger immediately (leading-edge)
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent, "Should receive ReconciliationTriggeredEvent for non-HAProxy pod resources")
	assert.Equal(t, "resource_change", receivedEvent.Reason)
}

func TestReconciler_Name(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger, nil)

	assert.Equal(t, ComponentName, reconciler.Name())
}

func TestReconciler_HandleHTTPResourceChange(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.DebounceWait,
	}

	reconciler := New(bus, logger, config)

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start reconciler
	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish HTTP resource updated event
	bus.Publish(events.NewHTTPResourceUpdatedEvent("http://example.com/blocklist.txt", "abc123", 1024))

	// Should trigger immediately (leading-edge)
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "http_resource_change", receivedEvent.Reason)
}

func TestReconciler_HandleHTTPResourceAccepted(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.LongTimeout, // Long interval to show it's bypassed
	}

	reconciler := New(bus, logger, config)

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 50)
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

// When leadership is acquired, the reconciler should trigger an immediate reconciliation
// to ensure leader-only components (renderer, drift monitor) receive fresh state.
func TestReconciler_HandleBecameLeader(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: testutil.LongTimeout, // Long interval to show it's bypassed
	}

	reconciler := New(bus, logger, config)

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start reconciler
	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish BecameLeaderEvent
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Should trigger immediately (not wait for debounce)
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "became_leader", receivedEvent.Reason)
}

func TestReconciler_BecameLeaderCancelsDebounce(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &Config{
		DebounceInterval: 300 * time.Millisecond,
	}

	reconciler := New(bus, logger, config)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First, trigger resource change to start a pending debounce timer
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for immediate trigger
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// Trigger another change during refractory (starts pending timer)
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))

	// Now send BecameLeaderEvent - should trigger immediately and cancel pending timer
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait for became_leader event
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, secondEvent)
	assert.Equal(t, "became_leader", secondEvent.Reason)

	// Verify no more events (the pending debounce was cancelled)
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestReconciler_HandleEvent_UnknownEvent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger, nil)

	// Should not panic for unknown events
	unknownEvent := events.NewValidationStartedEvent()
	reconciler.handleEvent(unknownEvent)
}

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
