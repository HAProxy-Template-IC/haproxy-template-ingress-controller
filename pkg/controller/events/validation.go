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

// ValidationFailedEvent is published when local configuration validation fails.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
//
// This event propagates the correlation ID from TemplateRenderedEvent.
type ValidationFailedEvent struct {
	Errors     []string // Validation errors from HAProxy
	DurationMs int64

	// TriggerReason is the reason that triggered this reconciliation.
	// Propagated from TemplateRenderedEvent.TriggerReason.
	// Examples: "config_change", "debounce_timer", "drift_prevention"
	// Used by DeploymentScheduler to determine fallback behavior (deploy cached config on drift prevention).
	TriggerReason string

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewValidationFailedEvent creates a new ValidationFailedEvent.
// Performs defensive copy of the errors slice.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewValidationFailedEvent(errors, durationMs, triggerReason,
//	    events.PropagateCorrelation(startedEvent))
func NewValidationFailedEvent(errors []string, durationMs int64, triggerReason string, opts ...CorrelationOption) *ValidationFailedEvent {
	return &ValidationFailedEvent{
		Errors:        copySlice(errors),
		DurationMs:    durationMs,
		TriggerReason: triggerReason,
		timestamped:   newTimestamped(),
		Correlation:   newCorrelation(opts...),
	}
}

func (e *ValidationFailedEvent) EventType() string { return EventTypeValidationFailed }
