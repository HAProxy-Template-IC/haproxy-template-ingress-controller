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

package debug

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/events"
)

// testEvent is a simple event type for testing.
type testEvent struct {
	name      string
	timestamp time.Time
}

func (e *testEvent) EventType() string {
	return e.name
}

func (e *testEvent) Timestamp() time.Time {
	if e.timestamp.IsZero() {
		return time.Now()
	}
	return e.timestamp
}

// correlatedTestEvent is a test event that implements events.CorrelatedEvent.
// This must implement all three methods: EventID(), CorrelationID(), CausationID().
type correlatedTestEvent struct {
	name          string
	correlationID string
	eventID       string
	causationID   string
	timestamp     time.Time
}

func (e *correlatedTestEvent) EventType() string {
	return e.name
}

func (e *correlatedTestEvent) Timestamp() time.Time {
	if e.timestamp.IsZero() {
		return time.Now()
	}
	return e.timestamp
}

func (e *correlatedTestEvent) CorrelationID() string {
	return e.correlationID
}

func (e *correlatedTestEvent) EventID() string {
	if e.eventID == "" {
		return "test-event-id"
	}
	return e.eventID
}

func (e *correlatedTestEvent) CausationID() string {
	return e.causationID
}

func TestNewEventBuffer(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(10, bus)

	require.NotNil(t, buffer)
	assert.NotNil(t, buffer.buffer)
	assert.Equal(t, bus, buffer.bus)
}

func TestEventBuffer_StartAndCapture(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the buffer first (it subscribes during Start)
	go buffer.Start(ctx)

	// Small delay to ensure subscription is active
	time.Sleep(10 * time.Millisecond)

	// Start the bus
	bus.Start()

	// Publish test events
	bus.Publish(&testEvent{name: "event.one"})
	bus.Publish(&testEvent{name: "event.two"})
	bus.Publish(&testEvent{name: "event.three"})

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify events were captured
	allEvents := buffer.GetAll()
	assert.GreaterOrEqual(t, len(allEvents), 3)

	// Check event types
	var found int
	for _, e := range allEvents {
		switch e.Type {
		case "event.one", "event.two", "event.three":
			found++
		}
	}
	assert.Equal(t, 3, found)
}

func TestEventBuffer_GetLast(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go buffer.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	bus.Start()

	// Publish 5 events
	for i := 0; i < 5; i++ {
		bus.Publish(&testEvent{name: "test.event"})
	}

	time.Sleep(50 * time.Millisecond)

	// Get last 3
	last3 := buffer.GetLast(3)
	assert.LessOrEqual(t, len(last3), 3)

	// Get all
	all := buffer.GetLast(100)
	assert.GreaterOrEqual(t, len(all), 5)
}

func TestEventBuffer_Len(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go buffer.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	bus.Start()

	// Initially empty (before our test events)
	initialLen := buffer.Len()

	// Publish 3 events
	bus.Publish(&testEvent{name: "test1"})
	bus.Publish(&testEvent{name: "test2"})
	bus.Publish(&testEvent{name: "test3"})

	time.Sleep(50 * time.Millisecond)

	// Should have at least 3 more events
	assert.GreaterOrEqual(t, buffer.Len(), initialLen+3)
}

func TestEventBuffer_FindByCorrelationID(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go buffer.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	bus.Start()

	// Publish events with different correlation IDs
	bus.Publish(&correlatedTestEvent{name: "correlated.event", correlationID: "abc-123"})
	bus.Publish(&correlatedTestEvent{name: "correlated.event", correlationID: "abc-123"})
	bus.Publish(&correlatedTestEvent{name: "correlated.event", correlationID: "xyz-789"})
	bus.Publish(&testEvent{name: "uncorrelated.event"})

	time.Sleep(50 * time.Millisecond)

	// Find events with correlation ID "abc-123"
	matching := buffer.FindByCorrelationID("abc-123")
	assert.Len(t, matching, 2)

	for _, e := range matching {
		assert.Equal(t, "abc-123", e.CorrelationID)
	}

	// Find events with correlation ID "xyz-789"
	matching2 := buffer.FindByCorrelationID("xyz-789")
	assert.Len(t, matching2, 1)

	// Empty correlation ID returns nil
	noMatch := buffer.FindByCorrelationID("")
	assert.Nil(t, noMatch)

	// Non-existent correlation ID returns empty
	noMatch2 := buffer.FindByCorrelationID("non-existent")
	assert.Empty(t, noMatch2)
}

func TestEventBuffer_ConvertEvent(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(10, bus)

	tests := []struct {
		name             string
		event            events.Event
		expectedType     string
		expectedCorrelID string
		hasCorrelationID bool
	}{
		{
			name:             "simple event",
			event:            &testEvent{name: "simple.test"},
			expectedType:     "simple.test",
			hasCorrelationID: false,
		},
		{
			name:             "correlated event",
			event:            &correlatedTestEvent{name: "correlated.test", correlationID: "test-corr-id"},
			expectedType:     "correlated.test",
			expectedCorrelID: "test-corr-id",
			hasCorrelationID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buffer.convertEvent(tt.event)

			assert.Equal(t, tt.expectedType, result.Type)
			assert.Equal(t, tt.expectedType, result.Summary) // Summary defaults to type
			assert.NotZero(t, result.Timestamp)

			if tt.hasCorrelationID {
				assert.Equal(t, tt.expectedCorrelID, result.CorrelationID)
			} else {
				assert.Empty(t, result.CorrelationID)
			}
		})
	}
}

func TestEventBuffer_RingBufferOverflow(t *testing.T) {
	bus := events.NewEventBus(100)
	// Small buffer size to test overflow
	buffer := NewEventBuffer(5, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go buffer.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	bus.Start()

	// Publish more events than buffer can hold
	for i := 0; i < 10; i++ {
		bus.Publish(&testEvent{name: "overflow.test"})
	}

	time.Sleep(50 * time.Millisecond)

	// Buffer should only contain last 5 events (ring buffer behavior)
	assert.LessOrEqual(t, buffer.Len(), 5)
}

func TestEventBuffer_ContextCancellation(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- buffer.Start(ctx)
	}()

	time.Sleep(10 * time.Millisecond)

	// Cancel context
	cancel()

	// Should exit gracefully
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Start did not exit after context cancellation")
	}
}

func TestEventsVar_Get(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go buffer.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	bus.Start()

	// Publish some events
	for i := 0; i < 5; i++ {
		bus.Publish(&testEvent{name: "var.test"})
	}

	time.Sleep(50 * time.Millisecond)

	// Create EventsVar with default limit
	eventsVar := &EventsVar{
		buffer:       buffer,
		defaultLimit: 10,
	}

	result, err := eventsVar.Get()

	require.NoError(t, err)
	eventsList, ok := result.([]Event)
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(eventsList), 5)
}

func TestEventsVar_Get_WithLimit(t *testing.T) {
	bus := events.NewEventBus(100)
	buffer := NewEventBuffer(100, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go buffer.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	bus.Start()

	// Publish 20 events
	for i := 0; i < 20; i++ {
		bus.Publish(&testEvent{name: "limit.test"})
	}

	time.Sleep(50 * time.Millisecond)

	// Create EventsVar with limit of 5
	eventsVar := &EventsVar{
		buffer:       buffer,
		defaultLimit: 5,
	}

	result, err := eventsVar.Get()

	require.NoError(t, err)
	eventsList := result.([]Event)
	// Should return at most defaultLimit events
	assert.LessOrEqual(t, len(eventsList), 5)
}

func TestEvent_Fields(t *testing.T) {
	now := time.Now()
	event := Event{
		Timestamp:     now,
		Type:          "test.type",
		Summary:       "Test summary",
		Details:       map[string]string{"key": "value"},
		CorrelationID: "corr-123",
	}

	assert.Equal(t, now, event.Timestamp)
	assert.Equal(t, "test.type", event.Type)
	assert.Equal(t, "Test summary", event.Summary)
	assert.NotNil(t, event.Details)
	assert.Equal(t, "corr-123", event.CorrelationID)
}
