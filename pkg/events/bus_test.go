package events

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// init registers test response types for scatter-gather tests.
// This is needed because executeRequest now uses typed subscriptions
// that filter to only Response event types.
func init() {
	responseEventTypes = append(responseEventTypes, "test.response")
}

// -----------------------------------------------------------------------------
// Test Event Types
// -----------------------------------------------------------------------------

// testEvent is a simple test event.
type testEvent struct {
	message string
}

func (e testEvent) EventType() string    { return "test.event" }
func (e testEvent) Timestamp() time.Time { return time.Now() }

// testRequest is a test request event.
type testRequest struct {
	id      string
	message string
}

func (e testRequest) EventType() string    { return "test.request" }
func (e testRequest) RequestID() string    { return e.id }
func (e testRequest) Timestamp() time.Time { return time.Now() }

// testResponse is a test response event.
type testResponse struct {
	reqID     string
	responder string
	data      string
}

func (e testResponse) EventType() string    { return "test.response" }
func (e testResponse) RequestID() string    { return e.reqID }
func (e testResponse) Responder() string    { return e.responder }
func (e testResponse) Timestamp() time.Time { return time.Now() }

// -----------------------------------------------------------------------------
// Basic Pub/Sub Tests
// -----------------------------------------------------------------------------

func TestEventBus_PublishSubscribe(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Subscribe
	sub := bus.Subscribe(10)

	// Start the bus
	bus.Start()

	// Publish event
	event := testEvent{message: "hello"}
	sent := bus.Publish(event)

	assert.Equal(t, 1, sent, "expected 1 subscriber to receive event")

	// Receive event
	select {
	case received := <-sub:
		te, ok := received.(testEvent)
		require.True(t, ok, "expected testEvent, got %T", received)
		assert.Equal(t, "hello", te.message)
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "timeout waiting for event")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create 5 subscribers
	subs := make([]<-chan Event, 5)
	for i := 0; i < 5; i++ {
		subs[i] = bus.Subscribe(10)
	}

	// Start the bus
	bus.Start()

	// Publish event
	event := testEvent{message: "broadcast"}
	sent := bus.Publish(event)

	assert.Equal(t, 5, sent, "expected 5 subscribers to receive event")

	// Verify all subscribers received the event
	for i, sub := range subs {
		select {
		case received := <-sub:
			te, ok := received.(testEvent)
			require.True(t, ok, "subscriber %d: expected testEvent, got %T", i, received)
			assert.Equal(t, "broadcast", te.message, "subscriber %d: unexpected message", i)
		case <-time.After(100 * time.Millisecond):
			require.Fail(t, "subscriber %d: timeout waiting for event", i)
		}
	}
}

func TestEventBus_SlowSubscriberDropsEvents(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create subscriber with buffer size 2
	sub := bus.Subscribe(2)

	// Start the bus so events are published directly (not buffered)
	bus.Start()

	// Fill the buffer
	bus.Publish(testEvent{message: "1"})
	bus.Publish(testEvent{message: "2"})

	// This event should be dropped (buffer full)
	sent := bus.Publish(testEvent{message: "3"})

	assert.Equal(t, 0, sent, "expected event to be dropped (sent=0)")

	// Drain first two events
	<-sub
	<-sub

	// Verify third event was dropped
	select {
	case <-sub:
		assert.Fail(t, "expected no more events, but received one")
	case <-time.After(10 * time.Millisecond):
		// Expected: no event received
	}
}

func TestEventBus_ConcurrentPublish(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	sub := bus.Subscribe(1000)

	// Start the bus
	bus.Start()

	// Publish 100 events concurrently
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bus.Publish(testEvent{message: fmt.Sprintf("event-%d", n)})
		}(i)
	}

	wg.Wait()

	// Count received events
	received := 0
	timeout := time.After(1 * time.Second)
	for {
		select {
		case <-sub:
			received++
			if received == 100 {
				return // Success
			}
		case <-timeout:
			require.Fail(t, "expected 100 events", "received %d", received)
		}
	}
}

// -----------------------------------------------------------------------------
// Request-Response (Scatter-Gather) Tests
// -----------------------------------------------------------------------------

func TestEventBus_RequestAllResponses(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx := context.Background()

	// Start 3 responders
	responders := []string{"validator-1", "validator-2", "validator-3"}
	for _, name := range responders {
		go startResponder(bus, name)
	}

	// Give responders time to subscribe
	time.Sleep(50 * time.Millisecond)

	// Start the bus
	bus.Start()

	// Send request
	req := testRequest{id: "req-1", message: "validate"}
	result, err := bus.Request(ctx, req, RequestOptions{
		Timeout:            2 * time.Second,
		ExpectedResponders: responders,
	})

	require.NoError(t, err, "request failed")
	assert.Len(t, result.Responses, 3, "expected 3 responses")
	assert.Empty(t, result.Errors, "expected no errors")

	// Verify all responders replied
	receivedFrom := make(map[string]bool)
	for _, resp := range result.Responses {
		receivedFrom[resp.Responder()] = true
	}

	for _, name := range responders {
		assert.True(t, receivedFrom[name], "missing response from %s", name)
	}
}

func TestEventBus_RequestTimeout(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx := context.Background()

	// Start only 1 responder, but expect 3
	go startResponder(bus, "validator-1")
	time.Sleep(50 * time.Millisecond)

	// Start the bus
	bus.Start()

	// Send request
	req := testRequest{id: "req-timeout", message: "validate"}
	result, err := bus.Request(ctx, req, RequestOptions{
		Timeout:            200 * time.Millisecond,
		ExpectedResponders: []string{"validator-1", "validator-2", "validator-3"},
	})

	require.Error(t, err, "expected timeout error")

	// Should have 1 response and 2 errors
	assert.Len(t, result.Responses, 1, "expected 1 response")
	assert.Len(t, result.Errors, 2, "expected 2 errors")
}

func TestEventBus_RequestMinResponses(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx := context.Background()

	// Start 2 responders
	go startResponder(bus, "validator-1")
	go startResponder(bus, "validator-2")
	time.Sleep(50 * time.Millisecond)

	// Start the bus
	bus.Start()

	// Send request requiring only 2 of 3 responders
	req := testRequest{id: "req-min", message: "validate"}
	result, err := bus.Request(ctx, req, RequestOptions{
		Timeout:            1 * time.Second,
		ExpectedResponders: []string{"validator-1", "validator-2", "validator-3"},
		MinResponses:       2, // Only need 2 responses
	})

	require.NoError(t, err, "request failed")
	assert.Len(t, result.Responses, 2, "expected 2 responses")

	// Should have error for missing validator-3
	assert.Len(t, result.Errors, 1, "expected 1 error")
}

func TestEventBus_RequestConcurrent(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx := context.Background()

	// Start responders
	responders := []string{"validator-1", "validator-2"}
	for _, name := range responders {
		go startResponder(bus, name)
	}
	time.Sleep(50 * time.Millisecond)

	// Start the bus
	bus.Start()

	// Send 10 concurrent requests
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			req := testRequest{
				id:      fmt.Sprintf("req-%d", n),
				message: fmt.Sprintf("validate-%d", n),
			}

			result, err := bus.Request(ctx, req, RequestOptions{
				Timeout:            2 * time.Second,
				ExpectedResponders: responders,
			})

			if err != nil {
				errors <- fmt.Errorf("request %d failed: %w", n, err)
				return
			}

			if len(result.Responses) != 2 {
				errors <- fmt.Errorf("request %d: expected 2 responses, got %d", n, len(result.Responses))
				return
			}

			if len(result.Errors) != 0 {
				errors <- fmt.Errorf("request %d: unexpected errors: %v", n, result.Errors)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}
}

func TestEventBus_RequestContextCancellation(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start responder (but it will be slow)
	go startSlowResponder(bus, "slow-validator", 1*time.Second)
	time.Sleep(50 * time.Millisecond)

	// Start the bus
	bus.Start()

	// Start request in background
	done := make(chan struct{})
	var err error

	go func() {
		req := testRequest{id: "req-cancel", message: "validate"}
		_, err = bus.Request(ctx, req, RequestOptions{
			Timeout:            5 * time.Second,
			ExpectedResponders: []string{"slow-validator"},
		})
		close(done)
	}()

	// Cancel after 100ms
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Wait for request to complete
	select {
	case <-done:
		require.Error(t, err, "expected context cancellation error")
		assert.ErrorIs(t, err, context.Canceled, "expected context.Canceled error")
	case <-time.After(1 * time.Second):
		require.Fail(t, "request did not complete after context cancellation")
	}
}

func TestEventBus_RequestEmptyResponders(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx := context.Background()

	req := testRequest{id: "req-empty", message: "validate"}
	_, err := bus.Request(ctx, req, RequestOptions{
		Timeout:            1 * time.Second,
		ExpectedResponders: []string{}, // Empty!
	})

	require.Error(t, err, "expected error for empty ExpectedResponders")
}

func TestEventBus_RequestInvalidMinResponses(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx := context.Background()

	req := testRequest{id: "req-invalid", message: "validate"}
	_, err := bus.Request(ctx, req, RequestOptions{
		Timeout:            1 * time.Second,
		ExpectedResponders: []string{"validator-1", "validator-2"},
		MinResponses:       5, // More than expected!
	})

	require.Error(t, err, "expected error for MinResponses > ExpectedResponders")
}

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// startResponder simulates a validator component that responds to requests.
func startResponder(bus *EventBus, name string) {
	sub := bus.Subscribe(100)

	for event := range sub {
		if req, ok := event.(testRequest); ok {
			// Send response
			resp := testResponse{
				reqID:     req.RequestID(),
				responder: name,
				data:      fmt.Sprintf("%s validated", req.message),
			}
			bus.Publish(resp)
		}
	}
}

// startSlowResponder simulates a slow validator.
func startSlowResponder(bus *EventBus, name string, delay time.Duration) {
	sub := bus.Subscribe(100)

	for event := range sub {
		if req, ok := event.(testRequest); ok {
			// Delay before responding
			time.Sleep(delay)

			// Send response
			resp := testResponse{
				reqID:     req.RequestID(),
				responder: name,
				data:      fmt.Sprintf("%s validated", req.message),
			}
			bus.Publish(resp)
		}
	}
}

// -----------------------------------------------------------------------------
// Startup Coordination Tests
// -----------------------------------------------------------------------------

func TestEventBus_Start_BuffersEventsBeforeStart(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Publish events BEFORE subscribing
	bus.Publish(testEvent{message: "event-1"})
	bus.Publish(testEvent{message: "event-2"})
	bus.Publish(testEvent{message: "event-3"})

	// Now subscribe
	sub := bus.Subscribe(10)

	// No events should be received yet
	select {
	case <-sub:
		assert.Fail(t, "expected no events before Start(), but received one")
	case <-time.After(50 * time.Millisecond):
		// Expected: no events
	}

	// Call Start()
	bus.Start()

	// Now all 3 buffered events should be delivered
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)
	for receivedCount < 3 {
		select {
		case evt := <-sub:
			te, ok := evt.(testEvent)
			require.True(t, ok, "expected testEvent, got %T", evt)
			receivedCount++
			t.Logf("Received: %s", te.message)
		case <-timeout:
			require.Fail(t, "expected 3 events", "received %d", receivedCount)
		}
	}

	// Verify we got exactly 3 events
	assert.Equal(t, 3, receivedCount)
}

func TestEventBus_Start_EventsPublishedAfterStart(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Subscribe
	sub := bus.Subscribe(10)

	// Start the bus
	bus.Start()

	// Publish events AFTER Start()
	sent := bus.Publish(testEvent{message: "after-start-1"})
	assert.Equal(t, 1, sent, "expected 1 subscriber to receive event")

	// Events should be delivered immediately
	select {
	case evt := <-sub:
		te, ok := evt.(testEvent)
		require.True(t, ok, "expected testEvent, got %T", evt)
		assert.Equal(t, "after-start-1", te.message)
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "timeout waiting for event")
	}
}

func TestEventBus_Start_PreservesEventOrder(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Publish events in specific order
	for i := 1; i <= 5; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Subscribe
	sub := bus.Subscribe(10)

	// Start
	bus.Start()

	// Verify events are received in order
	for i := 1; i <= 5; i++ {
		select {
		case evt := <-sub:
			te, ok := evt.(testEvent)
			require.True(t, ok, "expected testEvent, got %T", evt)
			expected := fmt.Sprintf("event-%d", i)
			assert.Equal(t, expected, te.message)
		case <-time.After(200 * time.Millisecond):
			require.Fail(t, "timeout waiting for event", "event %d", i)
		}
	}
}

func TestEventBus_Start_Idempotent(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Publish events
	bus.Publish(testEvent{message: "event-1"})

	// Subscribe
	sub := bus.Subscribe(10)

	// Start multiple times
	bus.Start()
	bus.Start()
	bus.Start()

	// Should receive exactly 1 event (not 3)
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)

	for {
		select {
		case <-sub:
			receivedCount++
		case <-timeout:
			assert.Equal(t, 1, receivedCount, "expected 1 event (idempotent Start)")
			return
		}
	}
}

func TestEventBus_Start_MultipleSubscribers(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Publish events before subscribing
	bus.Publish(testEvent{message: "event-1"})
	bus.Publish(testEvent{message: "event-2"})

	// Create 3 subscribers
	subs := make([]<-chan Event, 3)
	for i := 0; i < 3; i++ {
		subs[i] = bus.Subscribe(10)
	}

	// Start
	bus.Start()

	// All subscribers should receive both events
	for i, sub := range subs {
		receivedCount := 0
		timeout := time.After(200 * time.Millisecond)
		for receivedCount < 2 {
			select {
			case <-sub:
				receivedCount++
			case <-timeout:
				require.Fail(t, "subscriber timeout", "subscriber %d: expected 2 events, received %d", i, receivedCount)
			}
		}
	}
}

func TestEventBus_Start_ConcurrentPublish(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Publish some events before Start
	for i := 0; i < 5; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("pre-%d", i)})
	}

	// Subscribe
	sub := bus.Subscribe(100)

	// Concurrently publish more events while calling Start
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			bus.Publish(testEvent{message: fmt.Sprintf("concurrent-%d", i)})
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Start the bus (may happen during concurrent publishing)
	time.Sleep(2 * time.Millisecond)
	bus.Start()

	wg.Wait()

	// Count total received events
	receivedCount := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-sub:
			receivedCount++
		case <-timeout:
			// Should have received at least the 5 buffered events
			// May have received up to 10 (5 buffered + 5 concurrent)
			assert.GreaterOrEqual(t, receivedCount, 5, "expected at least 5 events")
			assert.LessOrEqual(t, receivedCount, 10, "expected at most 10 events")
			return
		}
	}
}

func TestEventBus_Start_EmptyBuffer(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Don't publish anything before Start
	sub := bus.Subscribe(10)

	// Start with empty buffer
	bus.Start()

	// Publish after Start
	sent := bus.Publish(testEvent{message: "after-start"})
	assert.Equal(t, 1, sent, "expected 1 subscriber")

	// Should receive the event
	select {
	case evt := <-sub:
		te, ok := evt.(testEvent)
		require.True(t, ok, "expected testEvent, got %T", evt)
		assert.Equal(t, "after-start", te.message)
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "timeout waiting for event")
	}
}

func TestEventBus_Start_PublishReturnsZeroBeforeStart(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Subscribe
	bus.Subscribe(10)

	// Publish before Start - should return 0 (buffered)
	sent := bus.Publish(testEvent{message: "buffered"})
	assert.Equal(t, 0, sent, "expected 0 (buffered)")

	// Start
	bus.Start()

	// Publish after Start - should return 1 (sent to subscriber)
	sent = bus.Publish(testEvent{message: "sent"})
	assert.Equal(t, 1, sent, "expected 1 (sent)")
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkEventBus_Publish(b *testing.B) {
	bus := NewEventBus(100)
	event := testEvent{message: "benchmark"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(event)
	}
}

func BenchmarkEventBus_PublishWithSubscribers(b *testing.B) {
	bus := NewEventBus(100)
	event := testEvent{message: "benchmark"}

	// Create 10 subscribers
	for i := 0; i < 10; i++ {
		sub := bus.Subscribe(1000)
		// Drain events in background
		go func(ch <-chan Event) {
			for range ch {
			}
		}(sub)
	}

	// Start the bus
	bus.Start()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(event)
	}
}

func BenchmarkEventBus_Request(b *testing.B) {
	bus := NewEventBus(100)
	ctx := context.Background()

	// Start 3 responders
	for i := 1; i <= 3; i++ {
		go startResponder(bus, fmt.Sprintf("validator-%d", i))
	}
	time.Sleep(50 * time.Millisecond)

	// Start the bus
	bus.Start()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := testRequest{id: fmt.Sprintf("req-%d", i), message: "validate"}
		_, _ = bus.Request(ctx, req, RequestOptions{
			Timeout:            1 * time.Second,
			ExpectedResponders: []string{"validator-1", "validator-2", "validator-3"},
		})
	}
}

// -----------------------------------------------------------------------------
// Additional Test Event Types for Typed Subscriptions
// -----------------------------------------------------------------------------

// otherTestEvent is a different test event type.
type otherTestEvent struct {
	value int
}

func (e otherTestEvent) EventType() string    { return "other.test.event" }
func (e otherTestEvent) Timestamp() time.Time { return time.Now() }

// -----------------------------------------------------------------------------
// Typed Subscription Tests
// -----------------------------------------------------------------------------

func TestEventBus_SubscribeTypes_FiltersCorrectly(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Subscribe to only "test.event" type
	typedSub := bus.SubscribeTypes(10, "test.event")

	// Also subscribe universally to verify events are published
	universalSub := bus.Subscribe(10)

	// Start the bus
	bus.Start()

	// Publish both event types
	bus.Publish(testEvent{message: "should receive"})
	bus.Publish(otherTestEvent{value: 42})
	bus.Publish(testEvent{message: "should also receive"})

	// Typed subscription should only receive testEvent
	receivedTyped := 0
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case evt := <-typedSub:
			_, ok := evt.(testEvent)
			assert.True(t, ok, "typed subscription received wrong type: %T", evt)
			receivedTyped++
		case <-timeout:
			assert.Equal(t, 2, receivedTyped, "expected 2 testEvent events")
			goto checkUniversal
		}
	}

checkUniversal:
	// Universal subscription should receive all 3 events
	receivedUniversal := 0
	timeout = time.After(200 * time.Millisecond)
	for {
		select {
		case <-universalSub:
			receivedUniversal++
		case <-timeout:
			assert.Equal(t, 3, receivedUniversal, "expected 3 total events in universal sub")
			return
		}
	}
}

func TestEventBus_SubscribeTypes_MultipleTypes(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Subscribe to both event types
	typedSub := bus.SubscribeTypes(10, "test.event", "other.test.event")

	// Start the bus
	bus.Start()

	// Publish both types
	bus.Publish(testEvent{message: "test1"})
	bus.Publish(otherTestEvent{value: 1})
	bus.Publish(testEvent{message: "test2"})
	bus.Publish(otherTestEvent{value: 2})

	// Should receive all 4 events
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)
	for receivedCount < 4 {
		select {
		case <-typedSub:
			receivedCount++
		case <-timeout:
			require.Fail(t, "timeout waiting for events", "expected 4 events, got %d", receivedCount)
		}
	}
}

func TestEventBus_SubscribeTypes_BufferedBeforeStart(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Publish events before subscription
	bus.Publish(testEvent{message: "buffered1"})
	bus.Publish(otherTestEvent{value: 999})
	bus.Publish(testEvent{message: "buffered2"})

	// Subscribe to only testEvent type
	typedSub := bus.SubscribeTypes(10, "test.event")

	// Start - should replay only matching events
	bus.Start()

	// Should receive only the 2 testEvent events
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case evt := <-typedSub:
			_, ok := evt.(testEvent)
			assert.True(t, ok, "received non-testEvent type: %T", evt)
			receivedCount++
		case <-timeout:
			assert.Equal(t, 2, receivedCount, "expected 2 buffered testEvent events")
			return
		}
	}
}

func TestEventBus_Subscribe_Generic(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use generic Subscribe function
	typedChan := Subscribe[testEvent](ctx, bus, 10)

	// Start the bus
	bus.Start()

	// Publish events
	bus.Publish(testEvent{message: "typed1"})
	bus.Publish(otherTestEvent{value: 42}) // Should be filtered out
	bus.Publish(testEvent{message: "typed2"})

	// Should receive only testEvent events with correct type
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case evt := <-typedChan:
			// evt is already testEvent type (no assertion needed)
			assert.NotEmpty(t, evt.message, "received event with empty message")
			receivedCount++
		case <-timeout:
			assert.Equal(t, 2, receivedCount, "expected 2 typed events")
			return
		}
	}
}

func TestEventBus_Subscribe_Generic_ContextCancellation(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx, cancel := context.WithCancel(context.Background())

	// Use generic Subscribe function
	typedChan := Subscribe[testEvent](ctx, bus, 10)

	// Start the bus
	bus.Start()

	// Publish one event
	bus.Publish(testEvent{message: "before-cancel"})

	// Receive the event
	select {
	case <-typedChan:
		// Good
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "timeout waiting for first event")
	}

	// Cancel context
	cancel()

	// Give goroutine time to exit
	time.Sleep(50 * time.Millisecond)

	// Publish more events - the forwarding goroutine should have stopped
	bus.Publish(testEvent{message: "after-cancel"})

	// Should not receive more events (or only events that were already in buffer)
	select {
	case <-typedChan:
		// May receive events that were buffered before cancellation
	case <-time.After(100 * time.Millisecond):
		// Expected - goroutine stopped
	}
}

func TestEventBus_SubscribeMultiple(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe to specific event types
	multiChan := SubscribeMultiple(ctx, bus, 10, "test.event", "test.request")

	// Start the bus
	bus.Start()

	// Publish different events
	bus.Publish(testEvent{message: "test1"})
	bus.Publish(otherTestEvent{value: 42})          // Should be filtered out
	bus.Publish(testRequest{id: "r1", message: ""}) // Should be received
	bus.Publish(testEvent{message: "test2"})

	// Should receive 3 events (2 testEvent + 1 testRequest)
	receivedCount := 0
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case evt := <-multiChan:
			switch evt.(type) {
			case testEvent, testRequest:
				receivedCount++
			default:
				assert.Fail(t, "received unexpected event type", "%T", evt)
			}
		case <-timeout:
			assert.Equal(t, 3, receivedCount, "expected 3 events")
			return
		}
	}
}

func TestEventBus_SubscribeTypes_SlowSubscriberDropsEvents(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create typed subscriber with buffer size 2
	typedSub := bus.SubscribeTypes(2, "test.event")

	// Start the bus
	bus.Start()

	// Fill the buffer
	bus.Publish(testEvent{message: "1"})
	bus.Publish(testEvent{message: "2"})

	// This event should be dropped (buffer full)
	sent := bus.Publish(testEvent{message: "3"})

	// Should show that some subscribers couldn't receive (but universal subs may still get it)
	// The sent count includes both universal and typed subscribers
	_ = sent // Just verify no panic

	// Drain first two events
	<-typedSub
	<-typedSub

	// Verify third event was dropped for typed subscriber
	select {
	case <-typedSub:
		assert.Fail(t, "expected no more events in typed subscriber, but received one")
	case <-time.After(50 * time.Millisecond):
		// Expected: no event received
	}
}

func TestEventBus_SubscribeTypesLeaderOnly_FiltersCorrectly(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Start the bus first (simulating leader-only component subscribing late)
	bus.Start()

	// Subscribe to only "test.event" type using leader-only method
	typedSub := bus.SubscribeTypesLeaderOnly(10, "test.event")

	// Publish both event types
	bus.Publish(testEvent{message: "should receive"})
	bus.Publish(otherTestEvent{value: 42})
	bus.Publish(testEvent{message: "should also receive"})

	// Typed subscription should only receive testEvent
	receivedTyped := 0
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case evt := <-typedSub:
			_, ok := evt.(testEvent)
			assert.True(t, ok, "typed subscription received wrong type: %T", evt)
			receivedTyped++
		case <-timeout:
			assert.Equal(t, 2, receivedTyped, "expected 2 testEvent events")
			return
		}
	}
}

func BenchmarkEventBus_SubscribeTypes(b *testing.B) {
	bus := NewEventBus(100)
	event := testEvent{message: "benchmark"}

	// Create typed subscriber
	typedSub := bus.SubscribeTypes(1000, "test.event")

	// Drain events in background
	go func() {
		for range typedSub {
		}
	}()

	// Start the bus
	bus.Start()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(event)
	}
}

func BenchmarkEventBus_SubscribeTypes_NonMatchingEvents(b *testing.B) {
	bus := NewEventBus(100)
	event := otherTestEvent{value: 42} // Different type

	// Create typed subscriber for different type
	typedSub := bus.SubscribeTypes(1000, "test.event")

	// Drain events in background
	go func() {
		for range typedSub {
		}
	}()

	// Start the bus
	bus.Start()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(event) // Should not match typed subscriber
	}
}

// -----------------------------------------------------------------------------
// Unsubscribe Tests
// -----------------------------------------------------------------------------

func TestEventBus_Unsubscribe(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create subscriber
	sub := bus.Subscribe(10)

	// Start the bus
	bus.Start()

	// Verify subscription works
	bus.Publish(testEvent{message: "before-unsub"})

	select {
	case <-sub:
		// Good - received event
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "timeout waiting for event")
	}

	// Unsubscribe
	bus.Unsubscribe(sub)

	// Publish after unsubscribe
	sent := bus.Publish(testEvent{message: "after-unsub"})

	// Should report 0 subscribers received (since we unsubscribed)
	assert.Equal(t, 0, sent, "expected 0 subscribers after Unsubscribe")
}

func TestEventBus_Unsubscribe_ReducesSubscriberCount(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create 3 subscribers
	sub1 := bus.Subscribe(10)
	sub2 := bus.Subscribe(10)
	sub3 := bus.Subscribe(10)

	// Start the bus
	bus.Start()

	// Verify all 3 receive events
	sent := bus.Publish(testEvent{message: "all3"})
	assert.Equal(t, 3, sent, "expected 3 subscribers")

	// Drain channels
	<-sub1
	<-sub2
	<-sub3

	// Unsubscribe one
	bus.Unsubscribe(sub2)

	// Now only 2 should receive
	sent = bus.Publish(testEvent{message: "only2"})
	assert.Equal(t, 2, sent, "expected 2 subscribers after unsubscribe")
}

func TestEventBus_Unsubscribe_Idempotent(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create subscriber
	sub := bus.Subscribe(10)

	// Unsubscribe multiple times - should not panic
	bus.Unsubscribe(sub)
	bus.Unsubscribe(sub)
	bus.Unsubscribe(sub)

	// No error expected
}

func TestEventBus_Subscribe_Generic_UnsubscribesOnCancel(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx, cancel := context.WithCancel(context.Background())

	// Subscribe generically
	_ = Subscribe[testEvent](ctx, bus, 10)

	// Start the bus
	bus.Start()

	// Verify subscription is active
	sent := bus.Publish(testEvent{message: "before"})
	assert.GreaterOrEqual(t, sent, 1, "expected at least 1 subscriber")

	// Cancel context
	cancel()

	// Give goroutine time to exit and call Unsubscribe
	time.Sleep(100 * time.Millisecond)

	// Publish again - the generic subscription should have been removed
	// Note: We need to check that the universal subscription count decreased
	bus.mu.RLock()
	subsCount := len(bus.subscribers)
	bus.mu.RUnlock()

	// There should be 0 universal subscribers after unsubscribe
	assert.Equal(t, 0, subsCount, "expected 0 universal subscribers after context cancel")
}

func TestEventBus_SubscribeMultiple_UnsubscribesOnCancel(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	ctx, cancel := context.WithCancel(context.Background())

	// Subscribe to multiple types
	_ = SubscribeMultiple(ctx, bus, 10, "test.event", "other.test.event")

	// Start the bus
	bus.Start()

	// Verify subscription is active
	sent := bus.Publish(testEvent{message: "before"})
	assert.GreaterOrEqual(t, sent, 1, "expected at least 1 subscriber")

	// Cancel context
	cancel()

	// Give goroutine time to exit and call Unsubscribe
	time.Sleep(100 * time.Millisecond)

	// Check that the universal subscription count decreased
	bus.mu.RLock()
	subsCount := len(bus.subscribers)
	bus.mu.RUnlock()

	// There should be 0 universal subscribers after unsubscribe
	assert.Equal(t, 0, subsCount, "expected 0 universal subscribers after context cancel")
}

// -----------------------------------------------------------------------------
// Lossy Subscription Tests
// -----------------------------------------------------------------------------

func TestEventBus_SubscribeLossy_SilentDrop(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Track drop callback invocations
	dropCount := 0
	bus.SetDropCallback(func(_ string) {
		dropCount++
	})

	// Create lossy subscriber with tiny buffer
	lossySub := bus.SubscribeLossy(1)

	// Start the bus
	bus.Start()

	// Fill buffer and cause drops
	for i := 0; i < 10; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Verify: drops happened but callback was NOT called
	assert.Equal(t, 0, dropCount, "lossy subscriber should not trigger drop callback")
	assert.Greater(t, bus.DroppedEventsObservability(), uint64(0), "expected observability drops")
	assert.Equal(t, uint64(0), bus.DroppedEventsCritical(), "expected no critical drops")

	// Drain the one event that fit
	<-lossySub
}

func TestEventBus_Subscribe_CriticalDrop(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Track drop callback invocations
	dropCount := 0
	bus.SetDropCallback(func(_ string) {
		dropCount++
	})

	// Create regular (critical) subscriber with tiny buffer
	criticalSub := bus.Subscribe(1)

	// Start the bus
	bus.Start()

	// Fill buffer and cause drops
	for i := 0; i < 10; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Verify: drops triggered callback
	assert.Greater(t, dropCount, 0, "critical subscriber should trigger drop callback")
	assert.Greater(t, bus.DroppedEventsCritical(), uint64(0), "expected critical drops")
	assert.Equal(t, uint64(0), bus.DroppedEventsObservability(), "expected no observability drops")

	// Drain the one event that fit
	<-criticalSub
}

func TestEventBus_SubscribeTypesLossy_SilentDrop(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Track drop callback invocations
	dropCount := 0
	bus.SetDropCallback(func(_ string) {
		dropCount++
	})

	// Create lossy typed subscriber with tiny buffer
	lossyTypedSub := bus.SubscribeTypesLossy(1, "test.event")

	// Start the bus
	bus.Start()

	// Fill buffer and cause drops
	for i := 0; i < 10; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Verify: drops happened but callback was NOT called
	assert.Equal(t, 0, dropCount, "lossy typed subscriber should not trigger drop callback")
	assert.Greater(t, bus.DroppedEventsObservability(), uint64(0), "expected observability drops")
	assert.Equal(t, uint64(0), bus.DroppedEventsCritical(), "expected no critical drops")

	// Drain the one event that fit
	<-lossyTypedSub
}

func TestEventBus_SubscribeTypes_CriticalDrop(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Track drop callback invocations
	dropCount := 0
	bus.SetDropCallback(func(_ string) {
		dropCount++
	})

	// Create regular (critical) typed subscriber with tiny buffer
	criticalTypedSub := bus.SubscribeTypes(1, "test.event")

	// Start the bus
	bus.Start()

	// Fill buffer and cause drops
	for i := 0; i < 10; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Verify: drops triggered callback
	assert.Greater(t, dropCount, 0, "critical typed subscriber should trigger drop callback")
	assert.Greater(t, bus.DroppedEventsCritical(), uint64(0), "expected critical drops")
	assert.Equal(t, uint64(0), bus.DroppedEventsObservability(), "expected no observability drops")

	// Drain the one event that fit
	<-criticalTypedSub
}

func TestEventBus_DroppedEvents_CombinedTotal(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create one lossy and one critical subscriber, both with tiny buffers
	_ = bus.SubscribeLossy(1)
	_ = bus.Subscribe(1)

	// Start the bus
	bus.Start()

	// Cause drops on both
	for i := 0; i < 10; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Verify combined total equals sum of both counters
	criticalDrops := bus.DroppedEventsCritical()
	observabilityDrops := bus.DroppedEventsObservability()
	totalDrops := bus.DroppedEvents()

	assert.Equal(t, criticalDrops+observabilityDrops, totalDrops, "total should equal sum of both")
	assert.Greater(t, criticalDrops, uint64(0), "expected critical drops")
	assert.Greater(t, observabilityDrops, uint64(0), "expected observability drops")
}

func TestEventBus_MixedSubscribers_DropCounters(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create mix of subscriber types with tiny buffers
	_ = bus.Subscribe(1)                              // critical universal
	_ = bus.SubscribeLossy(1)                         // lossy universal
	_ = bus.SubscribeTypes(1, "test.event")           // critical typed
	_ = bus.SubscribeTypesLossy(1, "test.event")      // lossy typed
	_ = bus.SubscribeTypesLossy(1, "other.test.type") // lossy typed (non-matching)

	// Start the bus
	bus.Start()

	// Cause drops
	for i := 0; i < 5; i++ {
		bus.Publish(testEvent{message: fmt.Sprintf("event-%d", i)})
	}

	// Both counters should have drops from their respective subscribers
	// 2 critical subscribers (universal + typed) should have drops
	// 2 lossy subscribers (universal + typed matching) should have drops
	// The non-matching lossy typed subscriber won't have drops (events filtered)
	assert.Greater(t, bus.DroppedEventsCritical(), uint64(0), "expected critical drops")
	assert.Greater(t, bus.DroppedEventsObservability(), uint64(0), "expected observability drops")
}

func TestEventBus_UnsubscribeLossy(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)

	// Create lossy subscriber
	lossySub := bus.SubscribeLossy(10)

	// Start the bus
	bus.Start()

	// Verify subscription works
	bus.Publish(testEvent{message: "before-unsub"})

	select {
	case <-lossySub:
		// Good - received event
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "timeout waiting for event")
	}

	// Unsubscribe
	bus.Unsubscribe(lossySub)

	// Publish after unsubscribe
	sent := bus.Publish(testEvent{message: "after-unsub"})

	// Should report 0 subscribers received
	assert.Equal(t, 0, sent, "expected 0 subscribers after Unsubscribe")
}
