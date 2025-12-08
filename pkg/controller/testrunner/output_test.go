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

package testrunner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFormatResults_Summary(t *testing.T) {
	results := &TestResults{
		TotalTests:  2,
		PassedTests: 1,
		FailedTests: 1,
		Duration:    2500 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName:    "test-pass",
				Description: "Passing test",
				Passed:      true,
				Duration:    1000 * time.Millisecond,
				Assertions: []AssertionResult{
					{Type: "contains", Description: "Has pattern", Passed: true},
				},
			},
			{
				TestName:    "test-fail",
				Description: "Failing test",
				Passed:      false,
				Duration:    1500 * time.Millisecond,
				Assertions: []AssertionResult{
					{Type: "contains", Description: "Missing pattern", Passed: false, Error: "pattern not found"},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary})
	require.NoError(t, err)

	// Check pass/fail symbols
	assert.Contains(t, output, "✓ test-pass")
	assert.Contains(t, output, "✗ test-fail")
	// Check summary line
	assert.Contains(t, output, "Tests: 1 passed, 1 failed, 2 total")
	// Check descriptions
	assert.Contains(t, output, "Passing test")
	assert.Contains(t, output, "Failing test")
	// Check error message
	assert.Contains(t, output, "pattern not found")
}

func TestFormatResults_SummaryVerbose(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName: "test-verbose",
				Passed:   false,
				Duration: 100 * time.Millisecond,
				Assertions: []AssertionResult{
					{
						Type:          "contains",
						Description:   "Check pattern",
						Passed:        false,
						Error:         "not found",
						Target:        "haproxy.cfg",
						TargetSize:    500,
						TargetPreview: "global\n  maxconn 1000",
					},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary, Verbose: true})
	require.NoError(t, err)

	// Check verbose fields
	assert.Contains(t, output, "Target: haproxy.cfg (500 bytes)")
	assert.Contains(t, output, "Content preview:")
	assert.Contains(t, output, "global")
	assert.Contains(t, output, "Hint: Use --dump-rendered to see full content")
}

func TestFormatResults_SummaryNoTests(t *testing.T) {
	results := &TestResults{
		TotalTests: 0,
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary})
	require.NoError(t, err)

	assert.Contains(t, output, "No tests found")
}

func TestFormatResults_SummaryWithRenderError(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName:    "render-error-test",
				Passed:      false,
				Duration:    100 * time.Millisecond,
				RenderError: "undefined filter 'foo'",
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary})
	require.NoError(t, err)

	assert.Contains(t, output, "Template rendering failed")
	assert.Contains(t, output, "undefined filter 'foo'")
}

func TestFormatResults_JSON(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		PassedTests: 1,
		FailedTests: 0,
		Duration:    1500 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName:    "json-test",
				Description: "JSON output test",
				Passed:      true,
				Duration:    1500 * time.Millisecond,
				Assertions: []AssertionResult{
					{Type: "contains", Description: "Has data", Passed: true},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatJSON})
	require.NoError(t, err)

	// Verify valid JSON
	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err)

	// Check fields
	assert.Equal(t, float64(1), parsed["totalTests"])
	assert.Equal(t, float64(1), parsed["passedTests"])
	assert.Equal(t, float64(0), parsed["failedTests"])
	assert.Equal(t, 1.5, parsed["duration"]) // Seconds

	// Check tests array
	tests := parsed["tests"].([]interface{})
	require.Len(t, tests, 1)
	test := tests[0].(map[string]interface{})
	assert.Equal(t, "json-test", test["testName"])
	assert.Equal(t, true, test["passed"])
}

func TestFormatResults_YAML(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		PassedTests: 1,
		FailedTests: 0,
		Duration:    2 * time.Second,
		TestResults: []TestResult{
			{
				TestName:    "yaml-test",
				Description: "YAML output test",
				Passed:      true,
				Duration:    2 * time.Second,
				Assertions: []AssertionResult{
					{Type: "equals", Description: "Match value", Passed: true},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatYAML})
	require.NoError(t, err)

	// Verify valid YAML
	var parsed map[string]interface{}
	err = yaml.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err)

	// Check fields (YAML parses integers as int, floats as float64)
	assert.EqualValues(t, 1, parsed["totalTests"])
	assert.EqualValues(t, 1, parsed["passedTests"])
	assert.EqualValues(t, 0, parsed["failedTests"])
	assert.EqualValues(t, 2.0, parsed["duration"]) // Seconds

	// Check tests array
	tests := parsed["tests"].([]interface{})
	require.Len(t, tests, 1)
	test := tests[0].(map[string]interface{})
	assert.Equal(t, "yaml-test", test["testName"])
	assert.Equal(t, true, test["passed"])
}

func TestFormatResults_UnknownFormat(t *testing.T) {
	results := &TestResults{}

	_, err := FormatResults(results, OutputOptions{Format: "invalid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown output format")
}

func TestFormatResults_SummaryAssertionWithoutDescription(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		PassedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName: "test",
				Passed:   true,
				Duration: 100 * time.Millisecond,
				Assertions: []AssertionResult{
					{Type: "haproxy_valid", Passed: true}, // No description
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary})
	require.NoError(t, err)

	// Should fall back to type name
	assert.Contains(t, output, "✓ haproxy_valid")
}

func TestFormatResults_SummaryFailedAssertionWithoutDescription(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName: "test",
				Passed:   false,
				Duration: 100 * time.Millisecond,
				Assertions: []AssertionResult{
					{Type: "contains", Passed: false, Error: "not found"}, // No description
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary})
	require.NoError(t, err)

	// Should fall back to type name for failed assertion
	assert.Contains(t, output, "✗ contains")
}

func TestFormatResults_JSONWithRenderError(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName:    "render-fail",
				Passed:      false,
				Duration:    100 * time.Millisecond,
				RenderError: "template error",
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatJSON})
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err)

	tests := parsed["tests"].([]interface{})
	test := tests[0].(map[string]interface{})
	assert.Equal(t, "template error", test["renderError"])
}

func TestFormatResults_VerboseWithLargeTarget(t *testing.T) {
	// Target > 200 bytes should show hint
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName: "test",
				Passed:   false,
				Duration: 100 * time.Millisecond,
				Assertions: []AssertionResult{
					{
						Type:          "contains",
						Passed:        false,
						Target:        "haproxy.cfg",
						TargetSize:    250,
						TargetPreview: "preview content",
					},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary, Verbose: true})
	require.NoError(t, err)

	assert.Contains(t, output, "Hint: Use --dump-rendered")
}

func TestFormatResults_VerboseWithSmallTarget(t *testing.T) {
	// Target <= 200 bytes should not show hint
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName: "test",
				Passed:   false,
				Duration: 100 * time.Millisecond,
				Assertions: []AssertionResult{
					{
						Type:          "contains",
						Passed:        false,
						Target:        "haproxy.cfg",
						TargetSize:    100,
						TargetPreview: "preview",
					},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary, Verbose: true})
	require.NoError(t, err)

	assert.NotContains(t, output, "Hint: Use --dump-rendered")
}

func TestFormatResults_MultilinePreview(t *testing.T) {
	results := &TestResults{
		TotalTests:  1,
		FailedTests: 1,
		Duration:    100 * time.Millisecond,
		TestResults: []TestResult{
			{
				TestName: "test",
				Passed:   false,
				Duration: 100 * time.Millisecond,
				Assertions: []AssertionResult{
					{
						Type:          "contains",
						Passed:        false,
						Target:        "haproxy.cfg",
						TargetSize:    300,
						TargetPreview: "line1\nline2\nline3",
					},
				},
			},
		},
	}

	output, err := FormatResults(results, OutputOptions{Format: OutputFormatSummary, Verbose: true})
	require.NoError(t, err)

	// Each line should be indented
	lines := strings.Split(output, "\n")
	var foundLine1, foundLine2, foundLine3 bool
	for _, line := range lines {
		if strings.Contains(line, "line1") {
			foundLine1 = true
			assert.True(t, strings.HasPrefix(strings.TrimLeft(line, " "), "line1") || strings.Contains(line, "      line1"))
		}
		if strings.Contains(line, "line2") {
			foundLine2 = true
		}
		if strings.Contains(line, "line3") {
			foundLine3 = true
		}
	}
	assert.True(t, foundLine1, "should contain line1")
	assert.True(t, foundLine2, "should contain line2")
	assert.True(t, foundLine3, "should contain line3")
}
