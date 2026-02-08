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

import (
	"time"

	"github.com/google/uuid"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// ProposalValidationRequestedEvent is published when a component wants to validate
// a hypothetical configuration change without deploying it.
//
// This is used for:
// - HTTP store content validation (validate before accepting new content)
// - Webhook admission validation (validate resource changes before admission)
//
// Contract:
//   - Published by: HTTPStore, Webhook
//   - Consumed by: ProposalValidator
//   - Response: ProposalValidationCompletedEvent with matching RequestID
type ProposalValidationRequestedEvent struct {
	// ID uniquely identifies this validation request for response correlation.
	ID string

	// Overlays maps store names to their proposed changes.
	// The ProposalValidator will create a CompositeStoreProvider using these.
	// Key: store name (e.g., "ingresses", "services")
	// Value: proposed changes for that store
	Overlays map[string]*stores.StoreOverlay

	// HTTPOverlay contains pending HTTP content changes for validation.
	// When present, the ProposalValidator includes this in the ValidationContext
	// so the render pipeline sees pending HTTP content.
	// Nil when validating K8s-only changes (e.g., webhook admission).
	HTTPOverlay stores.HTTPContentOverlay

	// Source identifies where this request originated from.
	// Examples: "httpstore", "webhook"
	Source string

	// SourceContext provides additional context about the source.
	// For httpstore: resource URL
	// For webhook: resource GVK and namespace/name
	SourceContext string

	// timestamp when the request was created.
	timestamp time.Time
}

// EventType implements the Event interface.
func (e *ProposalValidationRequestedEvent) EventType() string {
	return EventTypeProposalValidationRequested
}

// Timestamp implements the Event interface.
func (e *ProposalValidationRequestedEvent) Timestamp() time.Time {
	return e.timestamp
}

// RequestID returns the unique request identifier for correlation.
func (e *ProposalValidationRequestedEvent) RequestID() string {
	return e.ID
}

// NewProposalValidationRequestedEvent creates a new proposal validation request.
//
// Parameters:
//   - overlays: Map of store name to proposed changes (can be nil for HTTP-only validation)
//   - httpOverlay: HTTP content overlay (can be nil for K8s-only validation)
//   - source: Identifier for the request source (e.g., "httpstore", "webhook")
//   - sourceContext: Additional context about the source
//
// Returns:
//   - Immutable ProposalValidationRequestedEvent with unique ID.
func NewProposalValidationRequestedEvent(overlays map[string]*stores.StoreOverlay, httpOverlay stores.HTTPContentOverlay, source, sourceContext string) *ProposalValidationRequestedEvent {
	// Defensive copy of overlays map
	overlaysCopy := make(map[string]*stores.StoreOverlay, len(overlays))
	for name, overlay := range overlays {
		overlaysCopy[name] = overlay
	}

	return &ProposalValidationRequestedEvent{
		ID:            uuid.New().String(),
		Overlays:      overlaysCopy,
		HTTPOverlay:   httpOverlay,
		Source:        source,
		SourceContext: sourceContext,
		timestamp:     time.Now(),
	}
}

// ProposalValidationCompletedEvent is published when proposal validation completes.
//
// This event indicates whether the proposed configuration changes would result
// in a valid HAProxy configuration.
//
// Contract:
//   - Published by: ProposalValidator
//   - Consumed by: HTTPStore, Webhook (via event subscription or sync call result)
type ProposalValidationCompletedEvent struct {
	// RequestID correlates this response to the original request.
	RequestID string

	// Valid is true if the proposed configuration passed all validation phases.
	Valid bool

	// Phase indicates which validation phase failed (syntax, schema, semantic).
	// Empty if Valid is true.
	Phase string

	// Error contains the validation error message if Valid is false.
	// Empty if Valid is true.
	Error string

	// DurationMs is the total validation duration in milliseconds.
	DurationMs int64

	// timestamp when the validation completed.
	timestamp time.Time
}

// EventType implements the Event interface.
func (e *ProposalValidationCompletedEvent) EventType() string {
	return EventTypeProposalValidationCompleted
}

// Timestamp implements the Event interface.
func (e *ProposalValidationCompletedEvent) Timestamp() time.Time {
	return e.timestamp
}

// NewProposalValidationCompletedEvent creates a successful validation completion event.
//
// Parameters:
//   - requestID: ID from the corresponding ProposalValidationRequestedEvent
//   - durationMs: Total validation duration in milliseconds
//
// Returns:
//   - Immutable ProposalValidationCompletedEvent indicating success.
func NewProposalValidationCompletedEvent(requestID string, durationMs int64) *ProposalValidationCompletedEvent {
	return &ProposalValidationCompletedEvent{
		RequestID:  requestID,
		Valid:      true,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

// NewProposalValidationFailedEvent creates a failed validation completion event.
//
// Parameters:
//   - requestID: ID from the corresponding ProposalValidationRequestedEvent
//   - phase: Validation phase that failed (syntax, schema, semantic, render)
//   - err: The validation error
//   - durationMs: Total validation duration in milliseconds
//
// Returns:
//   - Immutable ProposalValidationCompletedEvent indicating failure.
func NewProposalValidationFailedEvent(requestID, phase string, err error, durationMs int64) *ProposalValidationCompletedEvent {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	return &ProposalValidationCompletedEvent{
		RequestID:  requestID,
		Valid:      false,
		Phase:      phase,
		Error:      errMsg,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}
