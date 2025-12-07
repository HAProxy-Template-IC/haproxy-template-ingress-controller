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

package httpstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
)

func TestNew(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 5*time.Minute)

	require.NotNil(t, component)
	assert.Equal(t, bus, component.eventBus)
	assert.NotNil(t, component.eventChan)
	assert.NotNil(t, component.store)
	assert.NotNil(t, component.refreshers)
	assert.Equal(t, 5*time.Minute, component.evictionInterval)
}

func TestNew_NilLogger(t *testing.T) {
	bus := testutil.NewTestBus()

	component := New(bus, nil, 5*time.Minute)

	require.NotNil(t, component)
	// Should use default logger
	assert.NotNil(t, component.logger)
}

func TestComponent_Name(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	assert.Equal(t, "httpstore", component.Name())
}

func TestComponent_StartAndStop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0) // No eviction
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start component in goroutine
	done := make(chan error)
	go func() {
		done <- component.Start(ctx)
	}()

	// Give component time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context to stop
	cancel()

	// Verify component stops gracefully
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("component did not stop in time")
	}
}

func TestComponent_GetStore(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	store := component.GetStore()
	require.NotNil(t, store)
}

func TestComponent_RegisterURL_NoDelay(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// URL not in store - no delay configured
	component.RegisterURL("http://example.com")

	// Should not add refresher since delay is 0
	component.mu.Lock()
	_, exists := component.refreshers["http://example.com"]
	component.mu.Unlock()

	assert.False(t, exists)
}

func TestComponent_StopRefresher(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Manually add a refresher for testing
	component.mu.Lock()
	timer := time.NewTimer(1 * time.Hour)
	component.refreshers["http://example.com"] = timer
	component.mu.Unlock()

	// Stop the refresher
	component.StopRefresher("http://example.com")

	// Verify refresher is removed
	component.mu.Lock()
	_, exists := component.refreshers["http://example.com"]
	component.mu.Unlock()

	assert.False(t, exists)
}

func TestComponent_StopRefresher_NotExists(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Should not panic when stopping non-existent refresher
	component.StopRefresher("http://nonexistent.com")
}

func TestComponent_HandleValidationCompleted_NoPending(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Publish ValidationCompletedEvent with no pending content
	bus.Publish(events.NewValidationCompletedEvent(nil, 0))

	// Should not publish any HTTPResourceAcceptedEvent
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceAcceptedEvent); ok {
			t.Fatal("unexpected HTTPResourceAcceptedEvent when no pending content")
		}
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}

func TestComponent_HandleValidationFailed_NoPending(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Publish ValidationFailedEvent with no pending content
	bus.Publish(events.NewValidationFailedEvent([]string{"error"}, 0))

	// Should not publish any HTTPResourceRejectedEvent
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceRejectedEvent); ok {
			t.Fatal("unexpected HTTPResourceRejectedEvent when no pending content")
		}
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}

func TestComponent_HandleValidationFailed_EmptyErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Publish ValidationFailedEvent with empty errors slice
	bus.Publish(events.NewValidationFailedEvent([]string{}, 0))

	// Should not panic and not publish any events
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceRejectedEvent); ok {
			t.Fatal("unexpected HTTPResourceRejectedEvent when no pending content")
		}
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}

func TestComponent_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Publish unrelated event - should not cause any issues
	bus.Publish(events.NewConfigParsedEvent(nil, nil, "v1", ""))

	// Component should continue running
	time.Sleep(100 * time.Millisecond)
}

func TestComponent_StopAllRefreshers(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Manually add some refreshers
	component.mu.Lock()
	component.refreshers["http://example1.com"] = time.NewTimer(1 * time.Hour)
	component.refreshers["http://example2.com"] = time.NewTimer(1 * time.Hour)
	component.refreshers["http://example3.com"] = time.NewTimer(1 * time.Hour)
	component.mu.Unlock()

	// Stop all
	component.stopAllRefreshers()

	// Verify all removed
	component.mu.Lock()
	count := len(component.refreshers)
	component.mu.Unlock()

	assert.Equal(t, 0, count)
}

func TestComponent_HandleEvent_UnknownEvent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// handleEvent should not panic on unknown event types
	component.handleEvent(events.NewConfigParsedEvent(nil, nil, "v1", ""))
}

func TestComponent_WithEviction(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Use short eviction interval for testing
	component := New(bus, logger, 100*time.Millisecond)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start component in goroutine
	done := make(chan error)
	go func() {
		done <- component.Start(ctx)
	}()

	// Wait for at least one eviction cycle
	time.Sleep(200 * time.Millisecond)

	// Cancel context to stop
	cancel()

	// Verify component stops gracefully
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("component did not stop in time")
	}
}

func TestComponent_RefreshURL_ContextCancelled(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Set up cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	component.ctx = ctx

	// Should return early without panic
	component.refreshURL("http://example.com")
}

func TestComponent_RefreshURL_NilContext(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// ctx is nil
	component.ctx = nil

	// Should return early without panic
	component.refreshURL("http://example.com")
}

func TestComponent_RegisterURL_AlreadyRegistered(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Pre-register a URL in the refreshers map
	component.mu.Lock()
	timer := time.NewTimer(1 * time.Hour)
	component.refreshers["http://example.com"] = timer
	component.mu.Unlock()

	// Store should have a delay for this URL for RegisterURL to work
	// But even without delay, we're testing the already-registered path
	component.RegisterURL("http://example.com")

	// Timer should still exist (wasn't replaced)
	component.mu.Lock()
	existingTimer, exists := component.refreshers["http://example.com"]
	component.mu.Unlock()

	assert.True(t, exists)
	assert.Equal(t, timer, existingTimer)
	timer.Stop() // cleanup
}
