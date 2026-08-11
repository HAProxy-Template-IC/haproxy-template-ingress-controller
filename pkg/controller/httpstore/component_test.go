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

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationCompletedEvent with no pending content
	bus.Publish(events.NewValidationCompletedEvent(nil, 0, "", nil, true))

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

	ctx := t.Context()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationFailedEvent with no pending content
	bus.Publish(events.NewValidationFailedEvent([]string{"error"}, 0, ""))

	// A failed validation must not promote content.
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceAcceptedEvent); ok {
			t.Fatal("unexpected HTTPResourceAcceptedEvent when no pending content")
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

	ctx := t.Context()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationFailedEvent with empty errors slice
	bus.Publish(events.NewValidationFailedEvent([]string{}, 0, ""))

	// A failed validation must not promote content.
	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HTTPResourceAcceptedEvent); ok {
			t.Fatal("unexpected HTTPResourceAcceptedEvent when no pending content")
		}
	case <-time.After(testutil.NoEventTimeout):
		// Expected
	}
}

func TestComponent_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)
	bus.Start()

	ctx := t.Context()

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

	component.ctx = nil

	// Should return early without panic
	component.refreshURL("http://example.com")
}

func TestComponent_RegisterURL_AlreadyRegistered(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	_, err := component.store.Fetch(t.Context(), server.URL, httpstore.FetchOptions{Delay: time.Hour}, nil)
	require.NoError(t, err)
	component.RegisterURL(server.URL)
	component.mu.Lock()
	timer := component.refreshers[server.URL]
	component.mu.Unlock()
	require.NotNil(t, timer)
	component.RegisterURL(server.URL)

	component.mu.Lock()
	existingTimer, exists := component.refreshers[server.URL]
	component.mu.Unlock()

	assert.True(t, exists)
	assert.Equal(t, timer, existingTimer)
	component.StopRefresher(server.URL)
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

	ctx := t.Context()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationCompletedEvent
	// Note: Without actual pending content, this won't publish any events
	// This test verifies the code path executes without error
	bus.Publish(events.NewValidationCompletedEvent(nil, 0, "", nil, true))

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

	ctx := t.Context()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ValidationFailedEvent with multiple errors
	bus.Publish(events.NewValidationFailedEvent([]string{
		"validation error 1",
		"validation error 2",
	}, 0, ""))

	// Give time for event processing
	time.Sleep(testutil.DebounceWait)

	// Verify failed validation does not promote content when nothing is pending.
	testutil.AssertNoEvent[*events.HTTPResourceAcceptedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_RegisterURL_WithDelay(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	component := New(bus, logger, 0)

	// We need to set up the store with an entry that has a delay
	// This requires using the underlying HTTPStore's Fetch method
	// For testing, we can use the internal mechanism

	// Start component to set up context
	bus.Start()
	ctx := t.Context()

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
	ctx := t.Context()
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
	ctx := t.Context()
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
	event := events.NewValidationCompletedEvent(nil, 0, "", nil, true)
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

	eventChan := bus.Subscribe("test-sub", 100)
	bus.Start()

	ctx := t.Context()

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

	component.triggerProposalValidation(server.URL)
	request := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, eventChan, testutil.EventTimeout)

	// Publish ProposalValidationCompletedEvent with matching request ID
	bus.Publish(events.NewProposalValidationCompletedEvent(request.ID, 100))

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
	requestChan := bus.SubscribeTypes("test-requests", 1, events.EventTypeProposalValidationRequested)

	bus.Start()

	ctx := t.Context()

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

	component.triggerProposalValidation(server.URL)
	request := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requestChan, testutil.EventTimeout)

	component.handleProposalValidationCompleted(
		events.NewProposalValidationFailedEvent(request.ID, "validation", nil, 100),
	)

	pendingURLs := store.GetPendingURLs()
	assert.Len(t, pendingURLs, 0, "pending should be cleared after rejection")

	content, ok := store.Get(server.URL)
	assert.True(t, ok)
	assert.Equal(t, "initial content", content)
}

func TestValidationVerdictFinalizesOnlyItsSnapshot(t *testing.T) {
	newChangingServer := func(initial, updated string) *httptest.Server {
		var requests atomic.Int32
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				_, _ = w.Write([]byte(initial))
				return
			}
			_, _ = w.Write([]byte(updated))
		}))
	}

	serverA := newChangingServer("accepted-a", "validated-a")
	defer serverA.Close()
	serverB := newChangingServer("accepted-b", "unvalidated-b")
	defer serverB.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	requests := bus.SubscribeTypes("test-requests", 2, events.EventTypeProposalValidationRequested)
	bus.Start()

	ctx := t.Context()
	_, err := component.store.Fetch(ctx, serverA.URL, httpstore.FetchOptions{}, nil)
	require.NoError(t, err)
	_, err = component.store.Fetch(ctx, serverB.URL, httpstore.FetchOptions{}, nil)
	require.NoError(t, err)

	changed, err := component.store.RefreshURL(ctx, serverA.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(serverA.URL)
	requestA := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requests, testutil.EventTimeout)
	require.True(t, requestA.HTTPOverlay.HasPendingURL(serverA.URL))
	require.False(t, requestA.HTTPOverlay.HasPendingURL(serverB.URL))

	changed, err = component.store.RefreshURL(ctx, serverB.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.handleProposalValidationCompleted(events.NewProposalValidationCompletedEvent(requestA.ID, 100))

	acceptedA, ok := component.store.Get(serverA.URL)
	require.True(t, ok)
	assert.Equal(t, "validated-a", acceptedA)
	acceptedB, ok := component.store.Get(serverB.URL)
	require.True(t, ok)
	assert.Equal(t, "accepted-b", acceptedB)
	assert.Equal(t, []string{serverB.URL}, component.store.GetPendingURLs())

	requestB := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requests, testutil.EventTimeout)
	assert.NotEqual(t, requestA.ID, requestB.ID)
	assert.False(t, requestB.HTTPOverlay.HasPendingURL(serverA.URL))
	assert.True(t, requestB.HTTPOverlay.HasPendingURL(serverB.URL))
}

func TestSupersededPendingVersionStartsNewValidationBatch(t *testing.T) {
	responses := []string{"accepted", "first-pending", "replacement-pending"}
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		index := min(int(requestCount.Add(1)-1), len(responses)-1)
		_, _ = w.Write([]byte(responses[index]))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	requests := bus.SubscribeTypes("test-requests", 2, events.EventTypeProposalValidationRequested)
	bus.Start()

	ctx := t.Context()
	_, err := component.store.Fetch(ctx, server.URL, httpstore.FetchOptions{}, nil)
	require.NoError(t, err)
	changed, err := component.store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(server.URL)
	firstRequest := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requests, testutil.EventTimeout)

	require.True(t, component.store.RejectPending(server.URL))
	changed, err = component.store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(server.URL)
	replacementRequest := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requests, testutil.EventTimeout)
	require.NotEqual(t, firstRequest.ID, replacementRequest.ID)
	replacementContent, ok := replacementRequest.HTTPOverlay.GetContent(server.URL)
	require.True(t, ok)
	assert.Equal(t, "replacement-pending", replacementContent)

	component.handleProposalValidationCompleted(events.NewProposalValidationCompletedEvent(firstRequest.ID, 100))
	accepted, ok := component.store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "accepted", accepted)
	assert.Equal(t, []string{server.URL}, component.store.GetPendingURLs())

	component.handleProposalValidationCompleted(events.NewProposalValidationCompletedEvent(replacementRequest.ID, 100))
	accepted, ok = component.store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "replacement-pending", accepted)
	assert.Empty(t, component.store.GetPendingURLs())
}

func TestSourceReplacementRetiresBatchAndValidatesSurvivingPendingContent(t *testing.T) {
	newChangingServer := func(initial, updated string) *httptest.Server {
		var requests atomic.Int32
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				_, _ = w.Write([]byte(initial))
				return
			}
			_, _ = w.Write([]byte(updated))
		}))
	}

	serverA := newChangingServer("accepted-a", "pending-a")
	defer serverA.Close()
	serverB := newChangingServer("accepted-b", "pending-b")
	defer serverB.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	requests := bus.SubscribeTypes("test-source-replacement", 3, events.EventTypeProposalValidationRequested)
	bus.Start()

	oldAuth := &httpstore.AuthConfig{Type: httpstore.AuthTypeBearer, Token: "old"}
	_, err := component.store.Fetch(t.Context(), serverA.URL, httpstore.FetchOptions{Critical: true}, oldAuth)
	require.NoError(t, err)
	_, err = component.store.Fetch(t.Context(), serverB.URL, httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	changed, err := component.store.RefreshURL(t.Context(), serverA.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(serverA.URL)
	retiredRequest := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requests, testutil.EventTimeout)

	changed, err = component.store.RefreshURL(t.Context(), serverB.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(serverB.URL)

	_, err = component.ReconcileSource(serverA.URL, httpstore.FetchOptions{Critical: true}, &httpstore.AuthConfig{
		Type:  httpstore.AuthTypeBearer,
		Token: "replacement",
	})
	require.NoError(t, err)
	replacementRequest := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, requests, testutil.EventTimeout)
	require.NotEqual(t, retiredRequest.ID, replacementRequest.ID)
	assert.False(t, replacementRequest.HTTPOverlay.HasPendingURL(serverA.URL))
	assert.True(t, replacementRequest.HTTPOverlay.HasPendingURL(serverB.URL))

	component.handleProposalValidationCompleted(events.NewProposalValidationCompletedEvent(retiredRequest.ID, 100))
	acceptedB, ok := component.store.Get(serverB.URL)
	require.True(t, ok)
	assert.Equal(t, "accepted-b", acceptedB)
	assert.Equal(t, []string{serverB.URL}, component.store.GetPendingURLs())

	component.handleProposalValidationCompleted(events.NewProposalValidationCompletedEvent(replacementRequest.ID, 100))
	acceptedB, ok = component.store.Get(serverB.URL)
	require.True(t, ok)
	assert.Equal(t, "pending-b", acceptedB)
	assert.Empty(t, component.store.GetPendingURLs())
}

func TestSourceReplacementFailureDoesNotBlockNextPendingValidation(t *testing.T) {
	responses := []string{"accepted", "pending"}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(responses[min(int(requests.Add(1)-1), len(responses)-1)]))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	validationRequests := bus.SubscribeTypes("test-failed-replacement", 2, events.EventTypeProposalValidationRequested)
	bus.Start()

	_, err := component.store.Fetch(t.Context(), server.URL, httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	changed, err := component.store.RefreshURL(t.Context(), server.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(server.URL)
	retiredRequest := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, validationRequests, testutil.EventTimeout)

	_, err = component.ReconcileSource(server.URL, httpstore.FetchOptions{Critical: true}, &httpstore.AuthConfig{
		Type:  httpstore.AuthTypeBearer,
		Token: "replacement",
	})
	require.NoError(t, err)
	component.mu.Lock()
	assert.Nil(t, component.pendingValidation)
	component.mu.Unlock()
	component.handleProposalValidationCompleted(events.NewProposalValidationCompletedEvent(retiredRequest.ID, 100))
	failedCtx, cancel := context.WithCancel(t.Context())
	cancel()
	failedWrapper := NewHTTPStoreWrapper(failedCtx, component, logger, nil, SourceModeAuthoritative)
	_, err = failedWrapper.Fetch(server.URL, map[string]any{"critical": true}, map[string]any{
		"type":  "bearer",
		"token": "replacement",
	})
	require.ErrorIs(t, err, context.Canceled)
	failedWrapper.InputTransaction().Abort()

	var otherRequests atomic.Int32
	otherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if otherRequests.Add(1) == 1 {
			_, _ = w.Write([]byte("other-accepted"))
			return
		}
		_, _ = w.Write([]byte("other-pending"))
	}))
	defer otherServer.Close()
	_, err = component.store.Fetch(t.Context(), otherServer.URL, httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	changed, err = component.store.RefreshURL(t.Context(), otherServer.URL)
	require.NoError(t, err)
	require.True(t, changed)
	component.triggerProposalValidation(otherServer.URL)
	nextRequest := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, validationRequests, testutil.EventTimeout)
	assert.NotEqual(t, retiredRequest.ID, nextRequest.ID)
	assert.True(t, nextRequest.HTTPOverlay.HasPendingURL(otherServer.URL))
}
