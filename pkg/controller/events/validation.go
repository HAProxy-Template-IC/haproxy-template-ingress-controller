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
// Validation Events.
// -----------------------------------------------------------------------------

// ValidationStartedEvent is published when local configuration validation begins.
//
// Validation is performed locally using the HAProxy binary to check configuration syntax.
// It does not involve HAProxy endpoints - those are only used later for deployment.
type ValidationStartedEvent struct {
	timestamp time.Time
}

// NewValidationStartedEvent creates a new ValidationStartedEvent.
func NewValidationStartedEvent() *ValidationStartedEvent {
	return &ValidationStartedEvent{
		timestamp: time.Now(),
	}
}

func (e *ValidationStartedEvent) EventType() string    { return EventTypeValidationStarted }
func (e *ValidationStartedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationCompletedEvent is published when local configuration validation succeeds.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
type ValidationCompletedEvent struct {
	Warnings   []string // Non-fatal warnings from HAProxy validation
	DurationMs int64
	timestamp  time.Time
}

// NewValidationCompletedEvent creates a new ValidationCompletedEvent.
// Performs defensive copy of the warnings slice.
func NewValidationCompletedEvent(warnings []string, durationMs int64) *ValidationCompletedEvent {
	// Defensive copy of warnings slice
	var warningsCopy []string
	if len(warnings) > 0 {
		warningsCopy = make([]string, len(warnings))
		copy(warningsCopy, warnings)
	}

	return &ValidationCompletedEvent{
		Warnings:   warningsCopy,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *ValidationCompletedEvent) EventType() string    { return EventTypeValidationCompleted }
func (e *ValidationCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationFailedEvent is published when local configuration validation fails.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
type ValidationFailedEvent struct {
	Errors     []string // Validation errors from HAProxy
	DurationMs int64
	timestamp  time.Time
}

// NewValidationFailedEvent creates a new ValidationFailedEvent.
// Performs defensive copy of the errors slice.
func NewValidationFailedEvent(errors []string, durationMs int64) *ValidationFailedEvent {
	// Defensive copy of errors slice
	var errorsCopy []string
	if len(errors) > 0 {
		errorsCopy = make([]string, len(errors))
		copy(errorsCopy, errors)
	}

	return &ValidationFailedEvent{
		Errors:     errorsCopy,
		DurationMs: durationMs,
		timestamp:  time.Now(),
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
