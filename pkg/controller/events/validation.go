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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// ValidationStartedEvent is published when local configuration validation begins.
//
// Validation is performed locally using the HAProxy binary to check configuration syntax.
// It does not involve HAProxy endpoints - those are only used later for deployment.
//
// This event propagates the correlation ID from TemplateRenderedEvent.
type ValidationStartedEvent struct {
	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewValidationStartedEvent creates a new ValidationStartedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewValidationStartedEvent(
//	    events.PropagateCorrelation(renderedEvent))
func NewValidationStartedEvent(opts ...CorrelationOption) *ValidationStartedEvent {
	return &ValidationStartedEvent{
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *ValidationStartedEvent) EventType() string    { return EventTypeValidationStarted }
func (e *ValidationStartedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationCompletedEvent is published when local configuration validation succeeds.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
//
// This event propagates the correlation ID from ValidationStartedEvent.
//
// This event implements CoalescibleEvent. The coalescible flag is propagated from
// TemplateRenderedEvent to enable coalescing throughout the reconciliation pipeline.
type ValidationCompletedEvent struct {
	Warnings   []string // Non-fatal warnings from HAProxy validation
	DurationMs int64

	// TriggerReason is the reason that triggered this reconciliation.
	// Propagated from TemplateRenderedEvent.TriggerReason.
	// Examples: "config_change", "debounce_timer", "drift_prevention"
	// Used by DeploymentScheduler to determine fallback behavior on validation failure.
	TriggerReason string

	// ParsedConfig is the pre-parsed desired configuration from syntax validation.
	// May be nil if validation cache was used.
	// When non-nil, can be passed to downstream sync operations to avoid re-parsing.
	ParsedConfig *parser.StructuredConfig

	// coalescible indicates if this event can be safely skipped when a newer
	// event of the same type is available. Propagated from TemplateRenderedEvent.
	coalescible bool

	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewValidationCompletedEvent creates a new ValidationCompletedEvent.
// Performs defensive copy of the warnings slice.
//
// The coalescible parameter should be propagated from TemplateRenderedEvent.Coalescible()
// to enable coalescing throughout the reconciliation pipeline.
//
// The parsedConfig parameter contains the pre-parsed desired configuration from syntax
// validation. Pass nil if validation cache was used or if the parsed config is not available.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewValidationCompletedEvent(warnings, durationMs, triggerReason, parsedConfig, coalescible,
//	    events.PropagateCorrelation(startedEvent))
func NewValidationCompletedEvent(warnings []string, durationMs int64, triggerReason string, parsedConfig *parser.StructuredConfig, coalescible bool, opts ...CorrelationOption) *ValidationCompletedEvent {
	// Defensive copy of warnings slice
	var warningsCopy []string
	if len(warnings) > 0 {
		warningsCopy = make([]string, len(warnings))
		copy(warningsCopy, warnings)
	}

	return &ValidationCompletedEvent{
		Warnings:      warningsCopy,
		DurationMs:    durationMs,
		TriggerReason: triggerReason,
		ParsedConfig:  parsedConfig,
		coalescible:   coalescible,
		timestamp:     time.Now(),
		Correlation:   NewCorrelation(opts...),
	}
}

func (e *ValidationCompletedEvent) EventType() string    { return EventTypeValidationCompleted }
func (e *ValidationCompletedEvent) Timestamp() time.Time { return e.timestamp }

// Coalescible returns true if this event can be safely skipped when a newer
// event of the same type is available. This implements the CoalescibleEvent interface.
func (e *ValidationCompletedEvent) Coalescible() bool { return e.coalescible }

// ValidationFailedEvent is published when local configuration validation fails.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
//
// This event propagates the correlation ID from ValidationStartedEvent.
type ValidationFailedEvent struct {
	Errors     []string // Validation errors from HAProxy
	DurationMs int64

	// TriggerReason is the reason that triggered this reconciliation.
	// Propagated from TemplateRenderedEvent.TriggerReason.
	// Examples: "config_change", "debounce_timer", "drift_prevention"
	// Used by DeploymentScheduler to determine fallback behavior (deploy cached config on drift prevention).
	TriggerReason string

	timestamp time.Time

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
	// Defensive copy of errors slice
	var errorsCopy []string
	if len(errors) > 0 {
		errorsCopy = make([]string, len(errors))
		copy(errorsCopy, errors)
	}

	return &ValidationFailedEvent{
		Errors:        errorsCopy,
		DurationMs:    durationMs,
		TriggerReason: triggerReason,
		timestamp:     time.Now(),
		Correlation:   NewCorrelation(opts...),
	}
}

func (e *ValidationFailedEvent) EventType() string    { return EventTypeValidationFailed }
func (e *ValidationFailedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationTestsStartedEvent is published when embedded validation tests begin execution.
//
// This is used for both CLI validation and webhook validation.
type ValidationTestsStartedEvent struct {
	TestCount int // Number of tests to execute
	timestamp time.Time
}

// NewValidationTestsStartedEvent creates a new ValidationTestsStartedEvent.
func NewValidationTestsStartedEvent(testCount int) *ValidationTestsStartedEvent {
	return &ValidationTestsStartedEvent{
		TestCount: testCount,
		timestamp: time.Now(),
	}
}

func (e *ValidationTestsStartedEvent) EventType() string    { return EventTypeValidationTestsStarted }
func (e *ValidationTestsStartedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationTestsCompletedEvent is published when all validation tests finish execution.
//
// This event is published regardless of whether tests passed or failed.
type ValidationTestsCompletedEvent struct {
	TotalTests  int   // Total number of tests executed
	PassedTests int   // Number of tests that passed
	FailedTests int   // Number of tests that failed
	DurationMs  int64 // Time taken to execute all tests
	timestamp   time.Time
}

// NewValidationTestsCompletedEvent creates a new ValidationTestsCompletedEvent.
func NewValidationTestsCompletedEvent(total, passed, failed int, durationMs int64) *ValidationTestsCompletedEvent {
	return &ValidationTestsCompletedEvent{
		TotalTests:  total,
		PassedTests: passed,
		FailedTests: failed,
		DurationMs:  durationMs,
		timestamp:   time.Now(),
	}
}

func (e *ValidationTestsCompletedEvent) EventType() string    { return EventTypeValidationTestsCompleted }
func (e *ValidationTestsCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationTestsFailedEvent is published when validation tests fail during webhook validation.
//
// This event is only published during webhook validation when tests fail and admission is denied.
type ValidationTestsFailedEvent struct {
	FailedTests []string // Names of tests that failed
	timestamp   time.Time
}

// NewValidationTestsFailedEvent creates a new ValidationTestsFailedEvent.
// Performs defensive copy of the failed tests slice.
func NewValidationTestsFailedEvent(failedTests []string) *ValidationTestsFailedEvent {
	// Defensive copy of slice
	var failedCopy []string
	if len(failedTests) > 0 {
		failedCopy = make([]string, len(failedTests))
		copy(failedCopy, failedTests)
	}

	return &ValidationTestsFailedEvent{
		FailedTests: failedCopy,
		timestamp:   time.Now(),
	}
}

func (e *ValidationTestsFailedEvent) EventType() string    { return EventTypeValidationTestsFailed }
func (e *ValidationTestsFailedEvent) Timestamp() time.Time { return e.timestamp }
