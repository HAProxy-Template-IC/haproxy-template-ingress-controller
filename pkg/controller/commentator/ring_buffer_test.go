package commentator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// longWindow is a time window large enough to cover every event added in
// these tests, so FindByTypeInWindow subsumes the no-window lookup.
const longWindow = 365 * 24 * time.Hour

// mockEvent is a simple test event implementation.
type mockEvent struct {
	eventType string
	timestamp time.Time
}

func (e mockEvent) EventType() string    { return e.eventType }
func (e mockEvent) Timestamp() time.Time { return e.timestamp }

func TestNewRingBuffer(t *testing.T) {
	rb := NewRingBuffer(100)

	assert.NotNil(t, rb)
	assert.Equal(t, 100, rb.Capacity())
	assert.Equal(t, 0, rb.size)
}

func TestRingBuffer_Add(t *testing.T) {
	rb := NewRingBuffer(5)

	// Add events
	for range 3 {
		rb.Add(mockEvent{
			eventType: "test.event",
			timestamp: time.Now(),
		})
	}

	assert.Equal(t, 3, rb.size)
}

func TestRingBuffer_Add_Wraparound(t *testing.T) {
	rb := NewRingBuffer(3)

	// Fill buffer
	for i := range 3 {
		rb.Add(mockEvent{
			eventType: "test.event",
			timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	assert.Equal(t, 3, rb.size)

	// Add more to trigger wraparound
	rb.Add(mockEvent{
		eventType: "test.newer",
		timestamp: time.Now().Add(10 * time.Second),
	})

	// Size should remain at capacity
	assert.Equal(t, 3, rb.size)
}

func TestRingBuffer_FindByType(t *testing.T) {
	rb := NewRingBuffer(10)

	// Add different event types
	rb.Add(mockEvent{eventType: "config.parsed", timestamp: time.Now()})
	rb.Add(mockEvent{eventType: "config.validated", timestamp: time.Now()})
	rb.Add(mockEvent{eventType: "config.parsed", timestamp: time.Now()})
	rb.Add(mockEvent{eventType: "deployment.started", timestamp: time.Now()})

	// Find by type
	configEvents := rb.FindByTypeInWindow("config.parsed", longWindow)
	assert.Len(t, configEvents, 2)

	validatedEvents := rb.FindByTypeInWindow("config.validated", longWindow)
	assert.Len(t, validatedEvents, 1)

	deploymentEvents := rb.FindByTypeInWindow("deployment.started", longWindow)
	assert.Len(t, deploymentEvents, 1)

	// Non-existent type
	missingEvents := rb.FindByTypeInWindow("nonexistent", longWindow)
	assert.Nil(t, missingEvents)
}

func TestRingBuffer_FindByType_NewestFirst(t *testing.T) {
	rb := NewRingBuffer(10)

	now := time.Now()

	// Add events with known timestamps
	rb.Add(mockEvent{eventType: "test", timestamp: now})
	time.Sleep(1 * time.Millisecond)
	rb.Add(mockEvent{eventType: "test", timestamp: now.Add(1 * time.Second)})
	time.Sleep(1 * time.Millisecond)
	rb.Add(mockEvent{eventType: "test", timestamp: now.Add(2 * time.Second)})

	events := rb.FindByTypeInWindow("test", longWindow)
	assert.Len(t, events, 3)

	// Verify newest first
	assert.True(t, events[0].Timestamp().After(events[1].Timestamp()))
	assert.True(t, events[1].Timestamp().After(events[2].Timestamp()))
}

func TestRingBuffer_FindByTypeInWindow(t *testing.T) {
	rb := NewRingBuffer(10)

	now := time.Now()

	// Add events at different times
	rb.Add(mockEvent{eventType: "test", timestamp: now.Add(-10 * time.Minute)}) // Too old
	rb.Add(mockEvent{eventType: "test", timestamp: now.Add(-3 * time.Minute)})  // Within window
	rb.Add(mockEvent{eventType: "test", timestamp: now.Add(-1 * time.Minute)})  // Within window

	// Find events within last 5 minutes
	events := rb.FindByTypeInWindow("test", 5*time.Minute)
	assert.Len(t, events, 2)
}

func TestRingBuffer_TypeIndex_LazyCleanup(t *testing.T) {
	rb := NewRingBuffer(3)

	// Fill buffer with one type
	for range 3 {
		rb.Add(mockEvent{eventType: "type1", timestamp: time.Now()})
	}

	// Overwrite with different type (triggers wraparound)
	for range 3 {
		rb.Add(mockEvent{eventType: "type2", timestamp: time.Now()})
	}

	// type1 should have no events (cleaned up lazily)
	type1Events := rb.FindByTypeInWindow("type1", longWindow)
	assert.Nil(t, type1Events)

	// type2 should have all 3
	type2Events := rb.FindByTypeInWindow("type2", longWindow)
	assert.Len(t, type2Events, 3)
}

func TestRingBuffer_Concurrent(t *testing.T) {
	rb := NewRingBuffer(100)

	// Spawn multiple goroutines adding events
	done := make(chan bool)
	for range 5 {
		go func() {
			for range 20 {
				rb.Add(mockEvent{
					eventType: "concurrent.test",
					timestamp: time.Now(),
				})
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for range 5 {
		<-done
	}

	// Should have 100 events (capacity limit)
	assert.Equal(t, 100, rb.size)

	// Should be able to find them
	events := rb.FindByTypeInWindow("concurrent.test", longWindow)
	assert.NotNil(t, events)
}

func TestRingBuffer_FindByCorrelationID(t *testing.T) {
	rb := NewRingBuffer(10)

	// Add events with correlation using real controller events
	event1 := ctlevents.NewReconciliationTriggeredEvent("test", true, ctlevents.WithNewCorrelation())
	correlationID := event1.CorrelationID()
	rb.Add(event1)

	// Add event with same correlation
	event2 := ctlevents.NewReconciliationStartedEvent("test", ctlevents.WithCorrelation(correlationID, event1.EventID()))
	rb.Add(event2)

	// Add event with different correlation
	event3 := ctlevents.NewReconciliationTriggeredEvent("other", true, ctlevents.WithNewCorrelation())
	rb.Add(event3)

	// Add non-correlated event (mockEvent doesn't implement CorrelatedEvent)
	rb.Add(mockEvent{eventType: "test", timestamp: time.Now()})

	// Find by correlation ID
	found := rb.FindByCorrelationID(correlationID, 0) // 0 means no limit
	assert.Len(t, found, 2)

	// Find with max count = 1
	found = rb.FindByCorrelationID(correlationID, 1)
	assert.Len(t, found, 1)

	// Find with empty correlation ID returns nil
	found = rb.FindByCorrelationID("", 10)
	assert.Nil(t, found)

	// Find with non-existent correlation ID returns empty
	found = rb.FindByCorrelationID("non-existent", 10)
	assert.Empty(t, found)
}
