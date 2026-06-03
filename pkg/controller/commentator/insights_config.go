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

package commentator

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// configInsight handles ConfigParsed, ConfigValidationRequest, ConfigValidationResponse,
// ConfigValidated, ConfigInvalid, CertResourceChanged, and CertParsed events.
func (ec *EventCommentator) configInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.ConfigParsedEvent:
		return fmt.Sprintf("Configuration parsed successfully (version %s)", e.Version),
			append(attrs, "version", e.Version, "secret_version", e.SecretVersion)

	case *events.ConfigValidationRequest:
		// Get validator count from validator package constants
		validatorCount := len(validator.AllValidatorNames())
		return fmt.Sprintf("Configuration validation started (version %s, expecting %d validators)",
				e.Version, validatorCount),
			append(attrs, "version", e.Version, "validator_count", validatorCount)

	case *events.ConfigValidationResponse:
		// Show real-time validator results with performance metrics
		statusSymbol := "✓"
		statusText := "OK"
		if !e.Valid {
			statusSymbol = "✗"
			statusText = statusFailed
		}

		// Build metrics message based on validator type
		var metricsMsg string
		if !e.Valid {
			metricsMsg = fmt.Sprintf(", %d errors", len(e.Errors))
		}

		return fmt.Sprintf("Validator '%s': %s %s%s",
				e.ValidatorName, statusSymbol, statusText, metricsMsg),
			append(attrs, "validator", e.ValidatorName, "valid", e.Valid, "error_count", len(e.Errors))

	case *events.ConfigValidatedEvent:
		// Correlate: how long did validation take?
		validationRequests := ec.ringBuffer.FindByTypeInWindow(events.EventTypeConfigValidationRequest, validationLookbackWindow)
		var correlationMsg string
		if len(validationRequests) > 0 {
			duration := event.Timestamp().Sub(validationRequests[0].Timestamp())
			correlationMsg = fmt.Sprintf(" (validation completed in %v)", duration.Round(time.Millisecond))
		}
		return fmt.Sprintf("Configuration validated successfully%s", correlationMsg),
			append(attrs, "version", e.Version, "secret_version", e.SecretVersion)

	case *events.ConfigInvalidEvent:
		// Build detailed breakdown per validator for the summary message
		errorCount := 0
		var validatorBreakdown []string
		for validatorName, errs := range e.ValidationErrors {
			errorCount += len(errs)
			if len(errs) > 0 {
				// Show first error as example (truncated for message readability)
				firstError := errs[0]
				if len(firstError) > maxErrorPreviewLength {
					firstError = firstError[:maxErrorPreviewLength-3] + "..."
				}
				validatorBreakdown = append(validatorBreakdown,
					fmt.Sprintf("%s: %d errors (e.g., %q)", validatorName, len(errs), firstError))
			}
		}

		detailMsg := ""
		if len(validatorBreakdown) > 0 {
			detailMsg = fmt.Sprintf(": %s", strings.Join(validatorBreakdown, "; "))
		}

		// Include full untruncated validation errors as structured attribute for debugging
		return fmt.Sprintf("Configuration validation failed with %d errors across %d validators%s",
				errorCount, len(e.ValidationErrors), detailMsg),
			append(attrs, "version", e.Version, "validator_count", len(e.ValidationErrors), "error_count", errorCount, "validation_errors", e.ValidationErrors)

	default:
		return "", attrs
	}
}

// validationInsight handles ValidationCompleted, ValidationFailed,
// ValidationTestsStarted, ValidationTestsCompleted, and ValidationTestsFailed events.
func (ec *EventCommentator) validationInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.ValidationCompletedEvent:
		warningInfo := ""
		if len(e.Warnings) > 0 {
			warningInfo = fmt.Sprintf(" with %d warnings", len(e.Warnings))
		}
		triggerInfo := ""
		if e.TriggerReason != "" {
			triggerInfo = fmt.Sprintf(" (trigger: %s)", e.TriggerReason)
		}
		return fmt.Sprintf("HAProxy configuration validation succeeded%s (%dms)%s", warningInfo, e.DurationMs, triggerInfo),
			append(attrs, "warnings", len(e.Warnings), "duration_ms", e.DurationMs, "trigger_reason", e.TriggerReason)

	case *events.ValidationFailedEvent:
		triggerInfo := ""
		if e.TriggerReason != "" {
			triggerInfo = fmt.Sprintf(" (trigger: %s)", e.TriggerReason)
		}
		return fmt.Sprintf("HAProxy configuration validation failed with %d errors (%dms)%s",
				len(e.Errors), e.DurationMs, triggerInfo),
			append(attrs, "error_count", len(e.Errors), "duration_ms", e.DurationMs, "trigger_reason", e.TriggerReason)

	// Validation Test Events
	case *events.ValidationTestsStartedEvent:
		return fmt.Sprintf("Starting validation tests (%d tests)", e.TestCount),
			append(attrs, "test_count", e.TestCount)

	case *events.ValidationTestsCompletedEvent:
		return fmt.Sprintf("Validation tests completed: %d passed, %d failed (%dms)",
				e.PassedTests, e.FailedTests, e.DurationMs),
			append(attrs,
				"total_tests", e.TotalTests,
				"passed_tests", e.PassedTests,
				"failed_tests", e.FailedTests,
				"duration_ms", e.DurationMs)

	case *events.ValidationTestsFailedEvent:
		return fmt.Sprintf("Validation tests failed: %d tests",
				len(e.FailedTests)),
			append(attrs,
				"failed_count", len(e.FailedTests),
				"failed_tests", e.FailedTests)

	default:
		return "", attrs
	}
}
