// Package commentator provides the Event Commentator pattern for domain-aware logging.
//
// The Event Commentator subscribes to all EventBus events and produces insightful log messages
// that apply domain knowledge to explain what's happening in the system, similar to how a
// sports commentator adds context and analysis to events.
package commentator

import (
	"sync"
	"time"

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
)

// Typical capacity: 1000 events (configurable).
type RingBuffer struct {
	events    []busevents.Event // Circular buffer (time-ordered)
	head      int               // Next write position
	size      int               // Current number of events
	capacity  int               // Maximum capacity
	typeIndex map[string][]int  // Event type -> slice of indices in events[]
	mu        sync.RWMutex
}

// NewRingBuffer creates a new ring buffer with the specified capacity.
//
// Parameters:
//   - capacity: Maximum number of events to store (recommended: 1000)
//
// Returns:
//   - *RingBuffer ready for use
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		events:    make([]busevents.Event, capacity),
		head:      0,
		size:      0,
		capacity:  capacity,
		typeIndex: make(map[string][]int),
	}
}

// Add appends an event to the buffer.
//
// If the buffer is full, the oldest event is overwritten (circular behavior).
// The type index is updated to include the new event.
//
// This operation is O(1) with lazy cleanup of stale indices.
func (rb *RingBuffer) Add(event busevents.Event) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Write event at head position
	rb.events[rb.head] = event

	// Update type index
	eventType := event.EventType()
	rb.typeIndex[eventType] = append(rb.typeIndex[eventType], rb.head)

	// Advance head (circular)
	rb.head = (rb.head + 1) % rb.capacity

	// Update size
	if rb.size < rb.capacity {
		rb.size++
	}
}

// FindByType returns all events of the specified type, newest first.
//
// The returned slice is a copy - modifications won't affect the buffer.
//
// Complexity: O(k) where k = number of events of that type (typically small)
//
// Example:
//
//	events := rb.FindByType("config.validated")
//	for _, evt := range events {
//	    // Process events (newest first)
//	}
func (rb *RingBuffer) FindByType(eventType string) []busevents.Event {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	indices := rb.typeIndex[eventType]
	if len(indices) == 0 {
		return nil
	}

	// Filter out stale indices (lazy cleanup)
	var result []busevents.Event
	validIndices := make([]int, 0, len(indices))

	for _, idx := range indices {
		event := rb.events[idx]
		// Check if this index still contains an event of the expected type
		if event != nil && event.EventType() == eventType {
			result = append(result, event)
			validIndices = append(validIndices, idx)
		}
	}

	// Update index with valid indices (lazy cleanup)
	rb.mu.RUnlock()
	rb.mu.Lock()
	rb.typeIndex[eventType] = validIndices
	rb.mu.Unlock()
	rb.mu.RLock()

	// Reverse to get newest first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

// FindByTypeInWindow returns events of the specified type within the time window, newest first.
//
// Parameters:
//   - eventType: The event type to filter by
//   - window: Time duration to look back (e.g., 5 * time.Minute)
//
// Returns:
//   - Slice of events matching the type and within the window, newest first
//
// Example:
//
//	// Find all config validations in the last 5 minutes
//	events := rb.FindByTypeInWindow("config.validated", 5*time.Minute)
func (rb *RingBuffer) FindByTypeInWindow(eventType string, window time.Duration) []busevents.Event {
	allEvents := rb.FindByType(eventType)
	if len(allEvents) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-window)
	var result []busevents.Event

	for _, evt := range allEvents {
		if evt.Timestamp().After(cutoff) {
			result = append(result, evt)
		}
	}

	return result
}

// FindRecent returns the N most recent events of any type, newest first.
//
// Parameters:
//   - n: Maximum number of events to return
//
// Returns:
//   - Slice of up to N most recent events, newest first
//
// Example:
//
//	// Get last 10 events for debugging
//	recent := rb.FindRecent(10)
func (rb *RingBuffer) FindRecent(n int) []busevents.Event {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if n > rb.size {
		n = rb.size
	}

	result := make([]busevents.Event, 0, n)

	// Start from most recent (head - 1) and go backwards
	for i := 0; i < n; i++ {
		idx := (rb.head - 1 - i + rb.capacity) % rb.capacity
		if rb.events[idx] != nil {
			result = append(result, rb.events[idx])
		}
	}

	return result
}

// FindRecentByPredicate returns recent events matching a predicate, newest first.
//
// Parameters:
//   - maxCount: Maximum number of matching events to return
//   - predicate: Function that returns true for events to include
//
// Returns:
//   - Slice of matching events, newest first
//
// Example:
//
//	// Find recent deployment events
//	deployments := rb.FindRecentByPredicate(5, func(e busevents.Event) bool {
//	    return strings.HasPrefix(e.EventType(), "deployment.")
//	})
func (rb *RingBuffer) FindRecentByPredicate(maxCount int, predicate func(busevents.Event) bool) []busevents.Event {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := make([]busevents.Event, 0, maxCount)

	// Start from most recent and go backwards
	for i := 0; i < rb.size && len(result) < maxCount; i++ {
		idx := (rb.head - 1 - i + rb.capacity) % rb.capacity
		event := rb.events[idx]
		if event != nil && predicate(event) {
			result = append(result, event)
		}
	}

	return result
}

// Size returns the current number of events in the buffer.
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Capacity returns the maximum capacity of the buffer.
func (rb *RingBuffer) Capacity() int {
	return rb.capacity
}

// FindByCorrelationID returns events with the specified correlation ID, newest first.
//
// Parameters:
//   - correlationID: The correlation ID to search for
//   - maxCount: Maximum number of events to return (0 = no limit)
//
// Returns:
//   - Slice of events matching the correlation ID, newest first
//
// Example:
//
//	// Find all events in a reconciliation cycle
//	events := rb.FindByCorrelationID("550e8400-e29b-41d4-a716-446655440000", 100)
func (rb *RingBuffer) FindByCorrelationID(correlationID string, maxCount int) []busevents.Event {
	if correlationID == "" {
		return nil
	}

	predicate := func(e busevents.Event) bool {
		if correlated, ok := e.(events.CorrelatedEvent); ok {
			return correlated.CorrelationID() == correlationID
		}
		return false
	}

	if maxCount <= 0 {
		maxCount = rb.capacity // Return all matching events
	}

	return rb.FindRecentByPredicate(maxCount, predicate)
}
