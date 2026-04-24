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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// ValidationStartedEvent is published when local configuration validation begins.
//
// Validation is performed locally using the HAProxy binary to check configuration syntax.
// It does not involve HAProxy endpoints - those are only used later for deployment.
//
// This event propagates the correlation ID from TemplateRenderedEvent.
type ValidationStartedEvent struct {
	timestamped

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
		timestamped: newTimestamped(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *ValidationStartedEvent) EventType() string { return EventTypeValidationStarted }

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

	timestamped

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
	return &ValidationCompletedEvent{
		Warnings:      copySlice(warnings),
		DurationMs:    durationMs,
		TriggerReason: triggerReason,
		ParsedConfig:  parsedConfig,
		coalescible:   coalescible,
		timestamped:   newTimestamped(),
		Correlation:   NewCorrelation(opts...),
	}
}

func (e *ValidationCompletedEvent) EventType() string { return EventTypeValidationCompleted }

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
		Correlation:   NewCorrelation(opts...),
	}
}

func (e *ValidationFailedEvent) EventType() string { return EventTypeValidationFailed }

// ValidationTestsStartedEvent is published when embedded validation tests begin execution.
//
// This is used for both CLI validation and webhook validation.
type ValidationTestsStartedEvent struct {
	TestCount int // Number of tests to execute
	timestamped
}

// NewValidationTestsStartedEvent creates a new ValidationTestsStartedEvent.
func NewValidationTestsStartedEvent(testCount int) *ValidationTestsStartedEvent {
	return &ValidationTestsStartedEvent{
		TestCount:   testCount,
		timestamped: newTimestamped(),
	}
}

func (e *ValidationTestsStartedEvent) EventType() string { return EventTypeValidationTestsStarted }

// ValidationTestsCompletedEvent is published when all validation tests finish execution.
//
// This event is published regardless of whether tests passed or failed.
type ValidationTestsCompletedEvent struct {
	TotalTests  int   // Total number of tests executed
	PassedTests int   // Number of tests that passed
	FailedTests int   // Number of tests that failed
	DurationMs  int64 // Time taken to execute all tests
	timestamped
}

// NewValidationTestsCompletedEvent creates a new ValidationTestsCompletedEvent.
func NewValidationTestsCompletedEvent(total, passed, failed int, durationMs int64) *ValidationTestsCompletedEvent {
	return &ValidationTestsCompletedEvent{
		TotalTests:  total,
		PassedTests: passed,
		FailedTests: failed,
		DurationMs:  durationMs,
		timestamped: newTimestamped(),
	}
}

func (e *ValidationTestsCompletedEvent) EventType() string { return EventTypeValidationTestsCompleted }

// ValidationTestsFailedEvent is published when validation tests fail during webhook validation.
//
// This event is only published during webhook validation when tests fail and admission is denied.
type ValidationTestsFailedEvent struct {
	FailedTests []string // Names of tests that failed
	timestamped
}

// NewValidationTestsFailedEvent creates a new ValidationTestsFailedEvent.
// Performs defensive copy of the failed tests slice.
func NewValidationTestsFailedEvent(failedTests []string) *ValidationTestsFailedEvent {
	return &ValidationTestsFailedEvent{
		FailedTests: copySlice(failedTests),
		timestamped: newTimestamped(),
	}
}

func (e *ValidationTestsFailedEvent) EventType() string { return EventTypeValidationTestsFailed }
