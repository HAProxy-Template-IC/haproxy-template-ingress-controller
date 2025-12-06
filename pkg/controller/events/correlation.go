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

package events

import "github.com/google/uuid"

// -----------------------------------------------------------------------------
// Event Correlation Support.
// -----------------------------------------------------------------------------

// CorrelatedEvent is an interface for events that support correlation tracking.
//
// Events that implement this interface can be linked together to trace
// causal chains through the reconciliation pipeline. This enables:
//   - Querying all events for a specific reconciliation cycle
//   - Building event graphs for debugging
//   - Measuring end-to-end latency for pipelines
//   - OpenTelemetry integration
//
// Not all events need to implement this interface. It's optional and primarily
// useful for events in the reconciliation pipeline.
type CorrelatedEvent interface {
	// EventID returns a unique identifier for this specific event.
	// Each event instance has its own unique EventID.
	// This is used as the CausationID for downstream events.
	EventID() string

	// CorrelationID returns a unique identifier that links related events.
	// All events in the same reconciliation cycle share the same correlation ID.
	// Returns empty string if correlation is not set.
	CorrelationID() string

	// CausationID returns the EventID of the event that triggered this one.
	// This enables building causal chains for debugging.
	// Returns empty string if causation is not set.
	CausationID() string
}

// Correlation holds event ID, correlation ID, and causation ID for event tracing.
//
// This struct is embedded in event types that need correlation support.
// It's designed to be optional - events can work without correlation,
// and consumers can check for empty IDs.
//
// The IDs serve different purposes:
//   - eventID: Unique identifier for THIS specific event instance
//   - correlationID: Shared ID linking all events in a reconciliation cycle
//   - causationID: The eventID of the event that triggered this one
type Correlation struct {
	eventID       string
	correlationID string
	causationID   string
}

// EventID returns the unique identifier for this specific event.
func (c *Correlation) EventID() string {
	return c.eventID
}

// CorrelationID returns the correlation ID, or empty string if not set.
func (c *Correlation) CorrelationID() string {
	return c.correlationID
}

// CausationID returns the causation ID, or empty string if not set.
func (c *Correlation) CausationID() string {
	return c.causationID
}

// CorrelationOption holds data for setting correlation on events.
// This is a data struct rather than a closure to ensure events are
// fully initialized in their struct literals without post-creation mutation.
type CorrelationOption struct {
	correlationID string
	causationID   string
	newChain      bool
}

// WithCorrelation sets both correlation and causation IDs on an event.
// Use this when propagating correlation context from a triggering event.
//
// Example:
//
//	triggeredEvent := events.NewReconciliationTriggeredEvent("config_change")
//	renderedEvent := events.NewTemplateRenderedEvent(...,
//	    events.WithCorrelation(triggeredEvent.CorrelationID(), triggeredEvent.EventID()))
func WithCorrelation(correlationID, causationID string) CorrelationOption {
	return CorrelationOption{
		correlationID: correlationID,
		causationID:   causationID,
	}
}

// WithNewCorrelation generates a new correlation ID and sets it on an event.
// Use this at the start of a new pipeline (e.g., ReconciliationTriggeredEvent).
//
// Example:
//
//	event := events.NewReconciliationTriggeredEvent("config_change",
//	    events.WithNewCorrelation())
func WithNewCorrelation() CorrelationOption {
	return CorrelationOption{newChain: true}
}

// NewCorrelation creates a Correlation struct from the provided options.
// This factory always generates a unique eventID for the event.
//
// This function only mutates a local variable (which is allowed),
// ensuring events are fully initialized in their struct literals.
func NewCorrelation(opts ...CorrelationOption) Correlation {
	c := Correlation{
		eventID: uuid.New().String(),
	}

	for _, opt := range opts {
		if opt.newChain {
			c.correlationID = uuid.New().String()
		}
		if opt.correlationID != "" {
			c.correlationID = opt.correlationID
		}
		if opt.causationID != "" {
			c.causationID = opt.causationID
		}
	}

	return c
}

// PropagateCorrelation extracts correlation context from a source event and
// returns a CorrelationOption that can be applied to a new event.
// If the source event doesn't implement CorrelatedEvent, returns an empty option.
//
// The correlation ID is copied from the source event (maintaining pipeline identity).
// The causation ID is set to the source event's EventID (creating causal chain).
//
// Example:
//
//	func handleEvent(sourceEvent Event) {
//	    newEvent := events.NewTemplateRenderedEvent(...,
//	        events.PropagateCorrelation(sourceEvent))
//	}
func PropagateCorrelation(source interface{}) CorrelationOption {
	if correlated, ok := source.(CorrelatedEvent); ok {
		return CorrelationOption{
			correlationID: correlated.CorrelationID(),
			// Use source's EventID as our causation ID
			// This creates a proper causal chain: source.eventID -> this.causationID
			causationID: correlated.EventID(),
		}
	}
	return CorrelationOption{}
}
