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

package dryrunvalidator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
)

// runValidationTests executes validation tests and returns an error if tests
// fail. It publishes the corresponding lifecycle events so the rest of the
// system (commentator, metrics) sees the test run.
func (c *Component) runValidationTests(requestID string) error {
	c.logger.Debug("Running validation tests",
		"request_id", requestID,
		"test_count", len(c.config.ValidationTests))

	testStartTime := time.Now()

	// Publish ValidationTestsStartedEvent
	c.eventBus.Publish(events.NewValidationTestsStartedEvent(len(c.config.ValidationTests)))

	// Run all validation tests with timeout
	ctx, cancel := context.WithTimeout(context.Background(), TestExecutionTimeout)
	defer cancel()
	testResults, err := c.testRunner.RunTests(ctx, "")
	testDuration := time.Since(testStartTime)

	if err != nil {
		c.logger.Info("Validation tests execution failed",
			"request_id", requestID,
			"error", err)
		return fmt.Errorf("validation test execution failed: %w", err)
	}

	// Publish ValidationTestsCompletedEvent
	c.eventBus.Publish(events.NewValidationTestsCompletedEvent(
		testResults.TotalTests,
		testResults.PassedTests,
		testResults.FailedTests,
		testDuration.Milliseconds(),
	))

	// If any tests failed, reject the admission
	if !testResults.AllPassed() {
		c.logger.Info("Validation tests failed",
			"request_id", requestID,
			"total_tests", testResults.TotalTests,
			"passed_tests", testResults.PassedTests,
			"failed_tests", testResults.FailedTests)

		// Collect failed test names
		failedTestNames := make([]string, 0, testResults.FailedTests)
		for i := range testResults.TestResults {
			result := &testResults.TestResults[i]
			if !result.Passed {
				failedTestNames = append(failedTestNames, result.TestName)
			}
		}

		// Publish ValidationTestsFailedEvent
		c.eventBus.Publish(events.NewValidationTestsFailedEvent(failedTestNames))

		// Build detailed error message
		return c.buildTestFailureError(testResults)
	}

	c.logger.Debug("Validation tests passed",
		"request_id", requestID,
		"total_tests", testResults.TotalTests,
		"duration_ms", testDuration.Milliseconds())

	return nil
}

// buildTestFailureError builds a detailed error message from test results,
// surfacing rendering errors and failed assertions for the webhook response.
func (c *Component) buildTestFailureError(testResults *testrunner.TestResults) error {
	var errorMsg strings.Builder
	fmt.Fprintf(&errorMsg, "Validation tests failed: %d/%d tests failed\n\nFailed tests:\n",
		testResults.FailedTests, testResults.TotalTests)

	for i := range testResults.TestResults {
		result := &testResults.TestResults[i]
		if !result.Passed {
			fmt.Fprintf(&errorMsg, "\n- Test: %s\n", result.TestName)
			if result.RenderError != "" {
				fmt.Fprintf(&errorMsg, "  Rendering failed: %s\n", result.RenderError)
			}
			for _, assertion := range result.Assertions {
				if !assertion.Passed {
					fmt.Fprintf(&errorMsg, "  Assertion failed: %s - %s\n",
						assertion.Description, assertion.Error)
				}
			}
		}
	}

	return fmt.Errorf("%s", errorMsg.String())
}
