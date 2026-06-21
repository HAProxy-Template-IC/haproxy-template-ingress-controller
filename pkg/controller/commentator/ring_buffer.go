// Package commentator provides the Event Commentator pattern for domain-aware logging.
//
// The Event Commentator subscribes to all EventBus events and produces insightful log messages
// that apply domain knowledge to explain what's happening in the system, similar to how a
// sports commentator adds context and analysis to events.
package commentator

import (
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// RingBuffer is a fixed-capacity circular buffer of recent events used by the
// commentator for cross-event correlation. Typical capacity: 1000 events.
//
// The Find* queries run a linear scan over the buffer. They're called at most a
// handful of times per reconciliation-log-line against a buffer capped at ~1000
// entries, so the scan cost is negligible and not worth maintaining secondary
// type/correlation indices (which also have to be cleaned up on wraparound).
// Old events fall out of every query automatically as the buffer overwrites
// their slots — there is no separate index to leak.
type RingBuffer struct {
	events   []busevents.Event // Circular buffer (time-ordered)
	head     int               // Next write position
	size     int               // Current number of events
	capacity int               // Maximum capacity

	mu sync.RWMutex
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
		events:   make([]busevents.Event, capacity),
		capacity: capacity,
	}
}

// Add appends an event to the buffer.
//
// If the buffer is full, the oldest event is overwritten (circular behavior).
//
// This operation is O(1).
func (rb *RingBuffer) Add(event busevents.Event) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Write event at head position (overwriting the oldest when full)
	rb.events[rb.head] = event

	// Advance head (circular)
	rb.head = (rb.head + 1) % rb.capacity

	// Update size
	if rb.size < rb.capacity {
		rb.size++
	}
}

// Capacity returns the maximum capacity of the buffer.
func (rb *RingBuffer) Capacity() int {
	return rb.capacity
}

// FindByType returns all events of the specified type, newest first.
//
// The returned slice is a copy - modifications won't affect the buffer.
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

	return rb.scanNewestFirst(func(event busevents.Event) bool {
		return event.EventType() == eventType
	})
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
	cutoff := time.Now().Add(-window)

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	return rb.scanNewestFirst(func(event busevents.Event) bool {
		return event.EventType() == eventType && event.Timestamp().After(cutoff)
	})
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

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := rb.scanNewestFirst(func(event busevents.Event) bool {
		correlated, ok := event.(events.CorrelatedEvent)
		return ok && correlated.CorrelationID() == correlationID
	})

	// Keep the most recent maxCount events (result is already newest-first).
	if maxCount > 0 && len(result) > maxCount {
		result = result[:maxCount]
	}

	return result
}

// scanNewestFirst walks the buffer from the most-recently-written slot back to
// the oldest, returning the events that satisfy predicate in newest-first
// order. Returns nil (not an empty slice) when nothing matches, matching the
// previous index-based implementation's contract.
//
// Must be called with rb.mu held (read lock is sufficient).
func (rb *RingBuffer) scanNewestFirst(predicate func(busevents.Event) bool) []busevents.Event {
	var result []busevents.Event
	// head points one past the newest entry; walk backwards from there.
	for i := 0; i < rb.size; i++ {
		idx := (rb.head - 1 - i + rb.capacity) % rb.capacity
		event := rb.events[idx]
		if event != nil && predicate(event) {
			result = append(result, event)
		}
	}
	return result
}
