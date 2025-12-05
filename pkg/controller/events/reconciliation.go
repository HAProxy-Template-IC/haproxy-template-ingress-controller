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

// -----------------------------------------------------------------------------
// Reconciliation Events.
// -----------------------------------------------------------------------------

// ReconciliationTriggeredEvent is published when a reconciliation cycle should start.
//
// This event is typically published by the Reconciler after the debounce timer.
// expires, or immediately for config changes.
type ReconciliationTriggeredEvent struct {
	// Reason describes why reconciliation was triggered.
	// Examples: "debounce_timer", "config_change", "manual_trigger"
	Reason    string
	timestamp time.Time
}

// NewReconciliationTriggeredEvent creates a new ReconciliationTriggeredEvent.
func NewReconciliationTriggeredEvent(reason string) *ReconciliationTriggeredEvent {
	return &ReconciliationTriggeredEvent{
		Reason:    reason,
		timestamp: time.Now(),
	}
}

func (e *ReconciliationTriggeredEvent) EventType() string    { return EventTypeReconciliationTriggered }
func (e *ReconciliationTriggeredEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationStartedEvent is published when the Executor begins a reconciliation cycle.
type ReconciliationStartedEvent struct {
	// Trigger describes what triggered this reconciliation.
	Trigger   string
	timestamp time.Time
}

// NewReconciliationStartedEvent creates a new ReconciliationStartedEvent.
func NewReconciliationStartedEvent(trigger string) *ReconciliationStartedEvent {
	return &ReconciliationStartedEvent{
		Trigger:   trigger,
		timestamp: time.Now(),
	}
}

func (e *ReconciliationStartedEvent) EventType() string    { return EventTypeReconciliationStarted }
func (e *ReconciliationStartedEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationCompletedEvent is published when a reconciliation cycle completes successfully.
type ReconciliationCompletedEvent struct {
	DurationMs int64
	timestamp  time.Time
}

// NewReconciliationCompletedEvent creates a new ReconciliationCompletedEvent.
func NewReconciliationCompletedEvent(durationMs int64) *ReconciliationCompletedEvent {
	return &ReconciliationCompletedEvent{
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *ReconciliationCompletedEvent) EventType() string    { return EventTypeReconciliationCompleted }
func (e *ReconciliationCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationFailedEvent is published when a reconciliation cycle fails.
type ReconciliationFailedEvent struct {
	Error     string
	Phase     string // Which phase failed: "render", "validate", "deploy"
	timestamp time.Time
}

// NewReconciliationFailedEvent creates a new ReconciliationFailedEvent.
func NewReconciliationFailedEvent(err, phase string) *ReconciliationFailedEvent {
	return &ReconciliationFailedEvent{
		Error:     err,
		Phase:     phase,
		timestamp: time.Now(),
	}
}

func (e *ReconciliationFailedEvent) EventType() string    { return EventTypeReconciliationFailed }
func (e *ReconciliationFailedEvent) Timestamp() time.Time { return e.timestamp }
