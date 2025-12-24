// Package commentator provides the Event Commentator pattern for domain-aware logging.
//
// The Event Commentator subscribes to all EventBus events and produces insightful log messages
// that apply domain knowledge to explain what's happening in the system, similar to how a
// sports commentator adds context and analysis to events.
package commentator

import (
	"sync"
	"time"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/events"
	busevents "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/events"
)

// Typical capacity: 1000 events (configurable).
type RingBuffer struct {
	events   []busevents.Event // Circular buffer (time-ordered)
	head     int               // Next write position
	size     int               // Current number of events
	capacity int               // Maximum capacity

	// typeIndex maps event types to indices in the events array.
	// Uses lazy cleanup during reads to remove stale indices.
	typeIndex map[string][]int

	// correlationIndex maps correlation IDs to indices in the events array.
	// Unlike typeIndex, correlation IDs are unique per reconciliation cycle,
	// so we must actively clean up when events are overwritten to prevent
	// memory growth.
	correlationIndex map[string][]int

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
		events:           make([]busevents.Event, capacity),
		head:             0,
		size:             0,
		capacity:         capacity,
		typeIndex:        make(map[string][]int),
		correlationIndex: make(map[string][]int),
	}
}

// Add appends an event to the buffer.
//
// If the buffer is full, the oldest event is overwritten (circular behavior).
// The type index and correlation index are updated to include the new event.
// Old correlation index entries are actively cleaned up when events are
// overwritten to prevent memory growth.
//
// This operation is O(1) amortized.
func (rb *RingBuffer) Add(event busevents.Event) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Clean up old correlation index entry before overwriting
	// This is critical because correlation IDs are unique per reconciliation
	// cycle and would otherwise accumulate indefinitely
	if rb.size == rb.capacity {
		rb.cleanupOldEventCorrelation(rb.head)
	}

	// Write event at head position
	rb.events[rb.head] = event

	// Update type index
	eventType := event.EventType()
	rb.typeIndex[eventType] = append(rb.typeIndex[eventType], rb.head)

	// Update correlation index for events that have correlation IDs
	if correlated, ok := event.(events.CorrelatedEvent); ok {
		if corrID := correlated.CorrelationID(); corrID != "" {
			rb.correlationIndex[corrID] = append(rb.correlationIndex[corrID], rb.head)
		}
	}

	// Advance head (circular)
	rb.head = (rb.head + 1) % rb.capacity

	// Update size
	if rb.size < rb.capacity {
		rb.size++
	}
}

// cleanupOldEventCorrelation removes the correlation index entry for the event
// being overwritten. This prevents memory growth from accumulating stale
// correlation ID entries.
//
// Must be called with rb.mu held.
func (rb *RingBuffer) cleanupOldEventCorrelation(idx int) {
	oldEvent := rb.events[idx]
	if oldEvent == nil {
		return
	}

	correlated, ok := oldEvent.(events.CorrelatedEvent)
	if !ok {
		return
	}

	corrID := correlated.CorrelationID()
	if corrID == "" {
		return
	}

	indices := rb.correlationIndex[corrID]
	if len(indices) == 0 {
		return
	}

	// Remove this index from the slice
	newIndices := make([]int, 0, len(indices)-1)
	for _, i := range indices {
		if i != idx {
			newIndices = append(newIndices, i)
		}
	}

	if len(newIndices) == 0 {
		// No more events with this correlation ID, remove the map entry entirely
		delete(rb.correlationIndex, corrID)
	} else {
		rb.correlationIndex[corrID] = newIndices
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
	rb.mu.Lock()
	defer rb.mu.Unlock()

	indices := rb.typeIndex[eventType]
	if len(indices) == 0 {
		return nil
	}

	result, validIndices := rb.filterValidIndices(indices, func(event busevents.Event) bool {
		return event != nil && event.EventType() == eventType
	})

	// Update index with valid indices (lazy cleanup)
	if len(validIndices) == 0 {
		delete(rb.typeIndex, eventType)
	} else {
		rb.typeIndex[eventType] = validIndices
	}

	// Return nil if no events found (consistent with early return above)
	if len(result) == 0 {
		return nil
	}

	// Reverse to get newest first
	reverseEvents(result)

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
// Complexity: O(k) where k = number of events with that correlation ID (typically 10-15 for a reconciliation cycle)
//
// Example:
//
//	// Find all events in a reconciliation cycle
//	events := rb.FindByCorrelationID("550e8400-e29b-41d4-a716-446655440000", 100)
func (rb *RingBuffer) FindByCorrelationID(correlationID string, maxCount int) []busevents.Event {
	if correlationID == "" {
		return nil
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	indices := rb.correlationIndex[correlationID]
	if len(indices) == 0 {
		return nil
	}

	result, validIndices := rb.filterValidIndices(indices, func(event busevents.Event) bool {
		if correlated, ok := event.(events.CorrelatedEvent); ok {
			return correlated.CorrelationID() == correlationID
		}
		return false
	})

	// Update index with valid indices (lazy cleanup)
	if len(validIndices) == 0 {
		delete(rb.correlationIndex, correlationID)
	} else {
		rb.correlationIndex[correlationID] = validIndices
	}

	// Return nil if no events found (consistent with early return above)
	if len(result) == 0 {
		return nil
	}

	// Apply maxCount limit (keep most recent, which are at the end before reversal)
	if maxCount > 0 && len(result) > maxCount {
		result = result[len(result)-maxCount:]
	}

	// Reverse to get newest first
	reverseEvents(result)

	return result
}

// filterValidIndices filters a slice of indices, keeping only those where the
// event at that index satisfies the predicate.
//
// Returns both the matching events and the valid indices for index cleanup.
// Must be called with rb.mu held.
func (rb *RingBuffer) filterValidIndices(indices []int, predicate func(busevents.Event) bool) (result []busevents.Event, validIndices []int) {
	result = make([]busevents.Event, 0, len(indices))
	validIndices = make([]int, 0, len(indices))

	for _, idx := range indices {
		event := rb.events[idx]
		if predicate(event) {
			result = append(result, event)
			validIndices = append(validIndices, idx)
		}
	}

	return result, validIndices
}

// reverseEvents reverses a slice of events in place.
func reverseEvents(evts []busevents.Event) {
	for i, j := 0, len(evts)-1; i < j; i, j = i+1, j-1 {
		evts[i], evts[j] = evts[j], evts[i]
	}
}
