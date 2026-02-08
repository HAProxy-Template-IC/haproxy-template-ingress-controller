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

import "time"

// ReconciliationTriggeredEvent is published when a reconciliation cycle should start.
//
// This event is typically published by the Reconciler after the debounce timer.
// expires, or immediately for config changes.
//
// This event starts a new correlation chain. Downstream events (TemplateRenderedEvent,
// ValidationCompletedEvent, DeploymentScheduledEvent, etc.) should propagate
// the correlation ID to enable end-to-end tracing.
//
// This event implements CoalescibleEvent. The coalescible flag is set by the emitter
// (Reconciler) based on the trigger context:
//   - coalescible=true for state updates (debounce_timer, resource_change)
//   - coalescible=false for commands (index_synchronized, drift_prevention)
type ReconciliationTriggeredEvent struct {
	// Reason describes why reconciliation was triggered.
	// Examples: "debounce_timer", "config_change", "manual_trigger"
	Reason    string
	timestamp time.Time

	// coalescible indicates if this event can be safely skipped when a newer
	// event of the same type is available. Set by the emitter (Reconciler).
	coalescible bool

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationTriggeredEvent creates a new ReconciliationTriggeredEvent.
//
// The coalescible parameter is set by the emitter based on trigger context:
//   - true for state updates where only the latest matters (debounce_timer, resource_change)
//   - false for commands that must be processed (index_synchronized, drift_prevention)
//
// Use WithNewCorrelation() to start a new correlation chain:
//
//	event := events.NewReconciliationTriggeredEvent("config_change", true,
//	    events.WithNewCorrelation())
func NewReconciliationTriggeredEvent(reason string, coalescible bool, opts ...CorrelationOption) *ReconciliationTriggeredEvent {
	return &ReconciliationTriggeredEvent{
		Reason:      reason,
		coalescible: coalescible,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *ReconciliationTriggeredEvent) EventType() string    { return EventTypeReconciliationTriggered }
func (e *ReconciliationTriggeredEvent) Timestamp() time.Time { return e.timestamp }

// Coalescible returns true if this event can be safely skipped when a newer
// event of the same type is available. This implements the CoalescibleEvent interface.
func (e *ReconciliationTriggeredEvent) Coalescible() bool { return e.coalescible }

// ReconciliationStartedEvent is published when the Executor begins a reconciliation cycle.
//
// This event propagates the correlation ID from ReconciliationTriggeredEvent.
type ReconciliationStartedEvent struct {
	// Trigger describes what triggered this reconciliation.
	Trigger   string
	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationStartedEvent creates a new ReconciliationStartedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewReconciliationStartedEvent(trigger,
//	    events.PropagateCorrelation(triggeredEvent))
func NewReconciliationStartedEvent(trigger string, opts ...CorrelationOption) *ReconciliationStartedEvent {
	return &ReconciliationStartedEvent{
		Trigger:     trigger,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *ReconciliationStartedEvent) EventType() string    { return EventTypeReconciliationStarted }
func (e *ReconciliationStartedEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationCompletedEvent is published when a reconciliation cycle completes successfully.
//
// This event propagates the correlation ID from the reconciliation chain.
type ReconciliationCompletedEvent struct {
	DurationMs int64
	timestamp  time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationCompletedEvent creates a new ReconciliationCompletedEvent.
//
// Use WithCorrelation() to propagate correlation from the pipeline:
//
//	event := events.NewReconciliationCompletedEvent(durationMs,
//	    events.WithCorrelation(correlationID, causationID))
func NewReconciliationCompletedEvent(durationMs int64, opts ...CorrelationOption) *ReconciliationCompletedEvent {
	return &ReconciliationCompletedEvent{
		DurationMs:  durationMs,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *ReconciliationCompletedEvent) EventType() string    { return EventTypeReconciliationCompleted }
func (e *ReconciliationCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationFailedEvent is published when a reconciliation cycle fails.
//
// This event propagates the correlation ID from the reconciliation chain.
type ReconciliationFailedEvent struct {
	Error     string
	Phase     string // Which phase failed: "render", "validate", "deploy"
	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationFailedEvent creates a new ReconciliationFailedEvent.
//
// Use WithCorrelation() to propagate correlation from the pipeline:
//
//	event := events.NewReconciliationFailedEvent(err, phase,
//	    events.WithCorrelation(correlationID, causationID))
func NewReconciliationFailedEvent(err, phase string, opts ...CorrelationOption) *ReconciliationFailedEvent {
	return &ReconciliationFailedEvent{
		Error:       err,
		Phase:       phase,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *ReconciliationFailedEvent) EventType() string    { return EventTypeReconciliationFailed }
func (e *ReconciliationFailedEvent) Timestamp() time.Time { return e.timestamp }
