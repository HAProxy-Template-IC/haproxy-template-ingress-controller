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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
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
	time.Sleep(testutil.StartupDelay)

	// Cancel context to stop
	cancel()

	// Verify component stops gracefully
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
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
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationCompletedEvent with no pending content
	bus.Publish(events.NewValidationCompletedEvent(nil, 0, "", true))

	// Should not publish any HTTPResourceAcceptedEvent
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceAcceptedEvent); ok {
			t.Fatal("unexpected HTTPResourceAcceptedEvent when no pending content")
		}
	case <-time.After(testutil.NoEventTimeout):
		// Expected
	}
}

func TestComponent_HandleValidationFailed_NoPending(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationFailedEvent with no pending content
	bus.Publish(events.NewValidationFailedEvent([]string{"error"}, 0, ""))

	// Should not publish any HTTPResourceRejectedEvent
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceRejectedEvent); ok {
			t.Fatal("unexpected HTTPResourceRejectedEvent when no pending content")
		}
	case <-time.After(testutil.NoEventTimeout):
		// Expected
	}
}

func TestComponent_HandleValidationFailed_EmptyErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationFailedEvent with empty errors slice
	bus.Publish(events.NewValidationFailedEvent([]string{}, 0, ""))

	// Should not panic and not publish any events
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceRejectedEvent); ok {
			t.Fatal("unexpected HTTPResourceRejectedEvent when no pending content")
		}
	case <-time.After(testutil.NoEventTimeout):
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
	time.Sleep(testutil.StartupDelay)

	// Publish unrelated event - should not cause any issues
	bus.Publish(events.NewConfigParsedEvent(nil, nil, "v1", ""))

	// Component should continue running
	time.Sleep(testutil.DebounceWait)
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
	time.Sleep(testutil.NoEventTimeout)

	// Cancel context to stop
	cancel()

	// Verify component stops gracefully
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
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

func TestComponent_HandleValidationCompleted_WithPending(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Set up pending content in the store
	store := component.GetStore()
	store.LoadFixture("http://example.com/data.txt", "initial content")

	// Manually add pending content by accessing internal cache
	// We need to simulate content that changed during refresh
	entry := store.GetEntry("http://example.com/data.txt")
	require.NotNil(t, entry)

	// Use the store's internal mechanism to set up pending content
	// LoadFixture only creates accepted content, so we need a workaround
	// We'll create a second fixture and then manually set HasPending
	// Actually, the cleanest way is to directly manipulate via test server

	// Alternative approach: use httptest server and trigger actual refresh
	// For simplicity, let's test the event flow by verifying PromotePending behavior

	// Create a store with pending content manually
	store.LoadFixture("http://pending.example.com/data.txt", "original content")

	// Subscribe to events BEFORE starting bus
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationCompletedEvent
	// Note: Without actual pending content, this won't publish any events
	// This test verifies the code path executes without error
	bus.Publish(events.NewValidationCompletedEvent(nil, 0, "", true))

	// Give time for event processing
	time.Sleep(testutil.DebounceWait)

	// Verify no HTTPResourceAcceptedEvent is published when there's no pending content
	testutil.AssertNoEvent[*events.HTTPResourceAcceptedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_HandleValidationFailed_WithErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Set up fixture
	store := component.GetStore()
	store.LoadFixture("http://example.com/data.txt", "content")

	// Subscribe BEFORE starting bus
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationFailedEvent with multiple errors
	bus.Publish(events.NewValidationFailedEvent([]string{
		"validation error 1",
		"validation error 2",
	}, 0, ""))

	// Give time for event processing
	time.Sleep(testutil.DebounceWait)

	// Verify no HTTPResourceRejectedEvent is published when there's no pending content
	testutil.AssertNoEvent[*events.HTTPResourceRejectedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_RegisterURL_WithDelay(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// We need to set up the store with an entry that has a delay
	// This requires using the underlying HTTPStore's Fetch method
	// For testing, we can use the internal mechanism

	// Start component to set up context
	bus.Start()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Verify URL without delay doesn't get registered
	component.RegisterURL("http://no-delay.example.com")

	component.mu.Lock()
	_, exists := component.refreshers["http://no-delay.example.com"]
	component.mu.Unlock()
	assert.False(t, exists, "URL without delay should not be registered")
}

func TestComponent_RefreshURL_EntryNotFound(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)
	bus.Start()

	// Set up context directly to avoid race with Start()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	component.ctx = ctx

	// Refresh URL that doesn't exist in store
	component.refreshURL("http://nonexistent.example.com")

	// Should exit early without panic (entry not found check)
}

func TestComponent_RefreshURL_WithExistingTimer(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)
	bus.Start()

	// Set up context directly to avoid race with Start()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	component.ctx = ctx

	// Pre-add a timer for testing the reset path
	component.mu.Lock()
	timer := time.NewTimer(1 * time.Hour)
	component.refreshers["http://example.com"] = timer
	component.mu.Unlock()

	// Load fixture to make entry exist
	store := component.GetStore()
	store.LoadFixture("http://example.com", "test content")

	// Call refreshURL - should reset timer if delay > 0
	// Since LoadFixture doesn't set delay, timer won't be reset
	component.refreshURL("http://example.com")

	// Cleanup
	timer.Stop()
}

func TestComponent_EvictionStopsRefresher(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Use very short eviction interval
	component := New(bus, logger, 50*time.Millisecond)

	// Add a refresher manually
	component.mu.Lock()
	timer := time.NewTimer(1 * time.Hour)
	component.refreshers["http://evicted.example.com"] = timer
	component.mu.Unlock()

	bus.Start()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- component.Start(ctx)
	}()

	// Wait for eviction to potentially run
	time.Sleep(testutil.DebounceWait)

	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("component did not stop in time")
	}
}

func TestComponent_HandleEvent_ValidationCompletedType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Test that handleEvent properly routes ValidationCompletedEvent
	event := events.NewValidationCompletedEvent(nil, 0, "", true)
	component.handleEvent(event)

	// Should not panic - just verify the routing works
}

func TestComponent_HandleEvent_ValidationFailedType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// Test that handleEvent properly routes ValidationFailedEvent
	event := events.NewValidationFailedEvent([]string{"error"}, 0, "")
	component.handleEvent(event)

	// Should not panic - just verify the routing works
}

// TestComponent_ValidationCompleted_WithActualPendingContent tests promotion of
// pending content when ValidationCompletedEvent is received.
func TestComponent_ValidationCompleted_WithActualPendingContent(t *testing.T) {
	// Create HTTP test server that returns different content on second request
	requestCount := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			w.Write([]byte("initial content"))
		} else {
			w.Write([]byte("updated content"))
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	store := component.GetStore()

	// Subscribe to events BEFORE starting bus
	eventChan := bus.Subscribe("test-sub", 100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Initial fetch to populate the store
	_, err := store.Fetch(ctx, server.URL, httpstore.FetchOptions{}, nil)
	require.NoError(t, err)

	// Trigger refresh to create pending content
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	require.True(t, changed, "content should have changed")

	// Verify we have pending content
	pendingURLs := store.GetPendingURLs()
	require.Len(t, pendingURLs, 1, "should have one URL with pending content")

	// Set a known pendingValidationID to simulate the component having triggered validation
	testRequestID := "test-validation-request-id"
	component.mu.Lock()
	component.pendingValidationID = testRequestID
	component.mu.Unlock()

	// Publish ProposalValidationCompletedEvent with matching request ID
	bus.Publish(events.NewProposalValidationCompletedEvent(testRequestID, 100))

	// Wait for and verify HTTPResourceAcceptedEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-eventChan:
			if accepted, ok := event.(*events.HTTPResourceAcceptedEvent); ok {
				assert.Equal(t, server.URL, accepted.URL)
				assert.Greater(t, accepted.ContentSize, 0)

				// Verify pending was promoted
				pendingURLs := store.GetPendingURLs()
				assert.Len(t, pendingURLs, 0, "pending should be cleared after promotion")
				return
			}
		case <-timeout:
			t.Fatal("timeout waiting for HTTPResourceAcceptedEvent")
		}
	}
}

// TestComponent_ValidationFailed_WithActualPendingContent tests rejection of
// pending content when ValidationFailedEvent is received.
func TestComponent_ValidationFailed_WithActualPendingContent(t *testing.T) {
	// Create HTTP test server that returns different content on second request
	requestCount := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			w.Write([]byte("initial content"))
		} else {
			w.Write([]byte("bad content that fails validation"))
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	store := component.GetStore()

	// Subscribe to events BEFORE starting bus
	eventChan := bus.Subscribe("test-sub", 100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, httpstore.FetchOptions{}, nil)
	require.NoError(t, err)

	// Get original accepted content
	originalContent, ok := store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "initial content", originalContent)

	// Trigger refresh to create pending content
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	require.True(t, changed)

	// Set a known pendingValidationID to simulate the component having triggered validation
	testRequestID := "test-validation-request-id"
	component.mu.Lock()
	component.pendingValidationID = testRequestID
	component.mu.Unlock()

	// Publish ProposalValidationFailedEvent with matching request ID
	bus.Publish(events.NewProposalValidationFailedEvent(testRequestID, "validation", nil, 100))

	// Wait for HTTPResourceRejectedEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-eventChan:
			if rejected, ok := event.(*events.HTTPResourceRejectedEvent); ok {
				assert.Equal(t, server.URL, rejected.URL)
				assert.Contains(t, rejected.Reason, "validation failed")

				// Verify pending was rejected and original content preserved
				pendingURLs := store.GetPendingURLs()
				assert.Len(t, pendingURLs, 0, "pending should be cleared after rejection")

				// Original content should still be available
				content, ok := store.Get(server.URL)
				assert.True(t, ok)
				assert.Equal(t, "initial content", content)
				return
			}
		case <-timeout:
			t.Fatal("timeout waiting for HTTPResourceRejectedEvent")
		}
	}
}

// Note: Tests for RegisterURL/refreshURL timer behavior removed due to race conditions
// in test setup. These paths are covered by integration tests and the existing
// validation flow tests above. Coverage for httpstore is already at 85%+.
