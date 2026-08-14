// Package commentator provides the Event Commentator pattern for domain-aware logging.
//
// The Event Commentator subscribes to all EventBus events and produces insightful log messages
// that apply domain knowledge to explain what's happening in the system, similar to how a
// sports commentator adds context and analysis to events.
package commentator

import (
	"strings"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type historyEntry struct {
	eventType     string
	timestamp     time.Time
	correlated    bool
	eventID       string
	correlationID string
	causationID   string
	trigger       string
	durationMs    int64
}

func (e *historyEntry) EventType() string    { return e.eventType }
func (e *historyEntry) Timestamp() time.Time { return e.timestamp }

type correlatedHistoryEntry struct {
	historyEntry
}

func (e *correlatedHistoryEntry) EventID() string       { return e.eventID }
func (e *correlatedHistoryEntry) CorrelationID() string { return e.correlationID }
func (e *correlatedHistoryEntry) CausationID() string   { return e.causationID }

func newHistoryEntry(event busevents.Event) historyEntry {
	entry := historyEntry{
		eventType: strings.Clone(event.EventType()),
		timestamp: event.Timestamp(),
	}

	if correlated, ok := event.(events.CorrelatedEvent); ok {
		entry.correlated = true
		entry.eventID = strings.Clone(correlated.EventID())
		entry.correlationID = strings.Clone(correlated.CorrelationID())
		entry.causationID = strings.Clone(correlated.CausationID())
	}

	switch typed := event.(type) {
	case *events.ReconciliationTriggeredEvent:
		entry.trigger = strings.Clone(typed.Reason)
	case *events.TemplateRenderedEvent:
		entry.durationMs = typed.DurationMs
	case *events.ValidationCompletedEvent:
		entry.durationMs = typed.DurationMs
	}

	return entry
}

// RingBuffer is a fixed-capacity circular buffer of recent events used by the
// commentator for cross-event correlation. It stores scalar projections, not
// the events or their payloads.
//
// The Find* queries run a linear scan over the buffer. They're called at most a
// handful of times per reconciliation log line against a fixed-size buffer,
// so the scan cost is negligible and not worth maintaining secondary
// type/correlation indices (which also have to be cleaned up on wraparound).
// Old events fall out of every query automatically as the buffer overwrites
// their slots — there is no separate index to leak.
type RingBuffer struct {
	entries  []historyEntry // Circular buffer (time-ordered)
	head     int            // Next write position
	size     int            // Current number of events
	capacity int            // Maximum capacity

	mu sync.RWMutex
}

// NewRingBuffer creates a new ring buffer with the specified capacity.
//
// Parameters:
//   - capacity: Maximum number of events to store (recommended: 500)
//
// Returns:
//   - *RingBuffer ready for use
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		entries:  make([]historyEntry, capacity),
		capacity: capacity,
	}
}

// Add appends an event to the buffer.
//
// If the buffer is full, the oldest event is overwritten (circular behavior).
//
// This operation is O(1).
func (rb *RingBuffer) Add(event busevents.Event) {
	entry := newHistoryEntry(event)

	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Write event at head position (overwriting the oldest when full)
	rb.entries[rb.head] = entry

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
	return boxHistoryEntries(rb.findByTypeInWindow(eventType, window))
}

func (rb *RingBuffer) findByTypeInWindow(eventType string, window time.Duration) []historyEntry {
	cutoff := time.Now().Add(-window)

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	return rb.scanNewestFirst(func(entry historyEntry) bool {
		return entry.eventType == eventType && entry.timestamp.After(cutoff)
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
	return boxHistoryEntries(rb.findByCorrelationID(correlationID, maxCount))
}

func (rb *RingBuffer) findByCorrelationID(correlationID string, maxCount int) []historyEntry {
	if correlationID == "" {
		return nil
	}

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := rb.scanNewestFirst(func(entry historyEntry) bool {
		return entry.correlationID == correlationID
	})

	// Keep the most recent maxCount events (result is already newest-first).
	if maxCount > 0 && len(result) > maxCount {
		result = result[:maxCount]
	}

	return result
}

func boxHistoryEntries(entries []historyEntry) []busevents.Event {
	if len(entries) == 0 {
		return nil
	}

	result := make([]busevents.Event, len(entries))
	for i, entry := range entries {
		if entry.correlated {
			result[i] = &correlatedHistoryEntry{historyEntry: entry}
		} else {
			result[i] = &entry
		}
	}
	return result
}

// scanNewestFirst walks the buffer from the most-recently-written slot back to
// the oldest, returning the events that satisfy predicate in newest-first
// order. Returns nil (not an empty slice) when nothing matches, matching the
// previous index-based implementation's contract.
//
// Must be called with rb.mu held (read lock is sufficient).
func (rb *RingBuffer) scanNewestFirst(predicate func(historyEntry) bool) []historyEntry {
	var result []historyEntry
	// head points one past the newest entry; walk backwards from there.
	for i := 0; i < rb.size; i++ {
		idx := (rb.head - 1 - i + rb.capacity) % rb.capacity
		entry := rb.entries[idx]
		if predicate(entry) {
			result = append(result, entry)
		}
	}
	return result
}
