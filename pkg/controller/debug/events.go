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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/events/ringbuffer"
)

// ComponentName is the unique identifier for the event buffer component.
const ComponentName = "event-buffer"

// Event represents a debug event with timestamp and details.
//
// This is a simplified representation of controller events for debug purposes.
// It captures the essential information without exposing internal event structures.
type Event struct {
	Timestamp     time.Time   `json:"timestamp"`
	Type          string      `json:"type"`
	Summary       string      `json:"summary"`
	Details       interface{} `json:"details,omitempty"`
	CorrelationID string      `json:"correlation_id,omitempty"`
}

// EventBuffer maintains a ring buffer of recent events for debug purposes.
//
// This is separate from the EventCommentator's ring buffer to avoid coupling
// debug functionality to the commentator component. It subscribes to the EventBus
// and stores simplified event representations.
type EventBuffer struct {
	buffer    *ringbuffer.RingBuffer[Event]
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event // Subscribed in constructor per CLAUDE.md guidelines
}

// NewEventBuffer creates a new event buffer with the specified capacity.
//
// The buffer subscribes to all events from the EventBus and stores the last
// N events (where N is the size parameter).
//
// Note: The buffer subscribes during construction per CLAUDE.md guidelines
// to ensure subscription happens before EventBus.Start() is called.
//
// Example:
//
//	eventBuffer := debug.NewEventBuffer(1000, eventBus)
//	go eventBuffer.Start(ctx)
func NewEventBuffer(size int, eventBus *busevents.EventBus) *EventBuffer {
	// Subscribe in constructor per CLAUDE.md guidelines to ensure subscription
	// happens before EventBus.Start() is called.
	// Use SubscribeLossy because event buffer is an observability component where
	// occasional event drops are acceptable and should not trigger WARN logs.
	eventChan := eventBus.SubscribeLossy(ComponentName, buffers.Observability())

	return &EventBuffer{
		buffer:    ringbuffer.New[Event](size),
		eventBus:  eventBus,
		eventChan: eventChan,
	}
}

// Start begins collecting events from the EventBus.
//
// This method blocks until the context is cancelled. It processes events
// from the pre-subscribed channel. It should be run in a goroutine.
//
// Example:
//
//	go eventBuffer.Start(ctx)
func (eb *EventBuffer) Start(ctx context.Context) error {
	for {
		select {
		case event := <-eb.eventChan:
			// Convert to debug Event
			debugEvent := eb.convertEvent(event)
			eb.buffer.Add(debugEvent)

		case <-ctx.Done():
			return nil
		}
	}
}

// GetLast returns the last n events in chronological order.
//
// Example:
//
//	recent := eventBuffer.GetLast(100)  // Last 100 events
func (eb *EventBuffer) GetLast(n int) []Event {
	return eb.buffer.GetLast(n)
}

// GetAll returns all events in the buffer.
func (eb *EventBuffer) GetAll() []Event {
	return eb.buffer.GetAll()
}

// Len returns the current number of events in the buffer.
func (eb *EventBuffer) Len() int {
	return eb.buffer.Len()
}

// FindByCorrelationID returns events matching the specified correlation ID.
//
// This method searches through all events in the buffer and returns those
// that have a matching correlation ID. Events are returned in chronological order.
//
// Example:
//
//	events := eventBuffer.FindByCorrelationID("550e8400-e29b-41d4-a716-446655440000")
func (eb *EventBuffer) FindByCorrelationID(correlationID string) []Event {
	if correlationID == "" {
		return nil
	}

	allEvents := eb.buffer.GetAll()
	var result []Event

	for _, event := range allEvents {
		if event.CorrelationID == correlationID {
			result = append(result, event)
		}
	}

	return result
}

// convertEvent converts a controller event to a debug Event.
//
// This extracts the event type and creates a summary string.
// It intentionally doesn't expose all internal event details to keep
// the debug API stable and simple.
func (eb *EventBuffer) convertEvent(event busevents.Event) Event {
	// busevents.Event always provides EventType
	eventType := event.EventType()

	// Extract correlation ID if available
	var correlationID string
	if correlated, ok := event.(events.CorrelatedEvent); ok {
		correlationID = correlated.CorrelationID()
	}

	// Create summary based on event type
	summary := eb.createSummary(event, eventType)

	return Event{
		Timestamp:     time.Now(),
		Type:          eventType,
		Summary:       summary,
		Details:       nil, // Avoid exposing full event details for stability
		CorrelationID: correlationID,
	}
}

// createSummary generates a human-readable summary for an event.
func (eb *EventBuffer) createSummary(event interface{}, eventType string) string {
	// For now, just use the event type as the summary
	// In the future, we could add more sophisticated summarization
	// based on specific event types
	return eventType
}

// EventsVar exposes recent events as a debug variable.
//
// Returns a JSON array of recent events.
//
// Example response:
//
//	[
//	  {
//	    "timestamp": "2025-01-15T10:30:45Z",
//	    "type": "config.validated",
//	    "summary": "config.validated"
//	  },
//	  {
//	    "timestamp": "2025-01-15T10:30:46Z",
//	    "type": "reconciliation.triggered",
//	    "summary": "reconciliation.triggered"
//	  }
//	]
type EventsVar struct {
	buffer       *EventBuffer
	defaultLimit int
}

// Get implements introspection.Var.
func (v *EventsVar) Get() (interface{}, error) {
	return v.buffer.GetLast(v.defaultLimit), nil
}
