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
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// OutputFormat specifies the output format for test results.
type OutputFormat string

const (
	// OutputFormatSummary outputs a human-readable summary.
	OutputFormatSummary OutputFormat = "summary"

	// OutputFormatJSON outputs structured JSON.
	OutputFormatJSON OutputFormat = "json"

	// OutputFormatYAML outputs structured YAML.
	OutputFormatYAML OutputFormat = "yaml"
)

// OutputOptions configures output formatting.
type OutputOptions struct {
	// Format specifies the output format (summary, json, yaml).
	Format OutputFormat

	// Verbose enables showing rendered content previews for failed assertions.
	Verbose bool
}

// FormatResults formats test results according to the specified options.
func FormatResults(results *TestResults, options OutputOptions) (string, error) {
	switch options.Format {
	case OutputFormatSummary:
		return formatSummary(results, options.Verbose), nil
	case OutputFormatJSON:
		return formatJSON(results)
	case OutputFormatYAML:
		return formatYAML(results)
	default:
		return "", fmt.Errorf("unknown output format: %s", options.Format)
	}
}

// formatMultilineError formats error messages with proper indentation for multi-line content.
// For single-line errors, returns the error as-is.
// For multi-line errors, indents all lines after the first to align with the error prefix.
// Empty lines are preserved without indentation to avoid trailing whitespace.
func formatMultilineError(errorMsg, indent string) string {
	if !strings.Contains(errorMsg, "\n") {
		// Single line - return as-is
		return errorMsg
	}

	// Multi-line - indent all non-empty lines after the first
	lines := strings.Split(errorMsg, "\n")
	result := lines[0] + "\n"
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			// Non-empty line - add indent
			result += indent + lines[i]
		}
		// Add newline if not the last line
		if i < len(lines)-1 {
			result += "\n"
		}
	}
	return result
}

// formatSummary formats results as a human-readable summary.
//
//nolint:revive // Complexity acceptable for formatting with multiple output conditions
func formatSummary(results *TestResults, verbose bool) string {
	var out strings.Builder

	if results.TotalTests == 0 {
		out.WriteString("No tests found\n")
		return out.String()
	}

	// Print each test result
	for i := range results.TestResults {
		test := &results.TestResults[i]
		// Test header
		if test.Passed {
			out.WriteString(fmt.Sprintf("✓ %s (%.3fs)\n", test.TestName, test.Duration.Seconds()))
		} else {
			out.WriteString(fmt.Sprintf("✗ %s (%.3fs)\n", test.TestName, test.Duration.Seconds()))
		}

		// Test description if present
		if test.Description != "" {
			out.WriteString(fmt.Sprintf("  %s\n", test.Description))
		}

		// Render error if present
		if test.RenderError != "" {
			out.WriteString("  ✗ Template rendering failed\n")
			out.WriteString(fmt.Sprintf("    Error: %s\n", formatMultilineError(test.RenderError, "           ")))
		}

		// Print assertions
		for _, assertion := range test.Assertions {
			if assertion.Passed {
				if assertion.Description != "" {
					out.WriteString(fmt.Sprintf("  ✓ %s\n", assertion.Description))
				} else {
					out.WriteString(fmt.Sprintf("  ✓ %s\n", assertion.Type))
				}
			} else {
				if assertion.Description != "" {
					out.WriteString(fmt.Sprintf("  ✗ %s\n", assertion.Description))
				} else {
					out.WriteString(fmt.Sprintf("  ✗ %s\n", assertion.Type))
				}
				if assertion.Error != "" {
					out.WriteString(fmt.Sprintf("    Error: %s\n", formatMultilineError(assertion.Error, "           ")))
				}

				// Verbose mode: show target metadata for failed assertions
				if verbose {
					if assertion.Target != "" {
						out.WriteString(fmt.Sprintf("    Target: %s (%d bytes)\n", assertion.Target, assertion.TargetSize))
					}
					if assertion.TargetPreview != "" {
						out.WriteString("    Content preview:\n")
						// Indent each line of the preview
						lines := strings.Split(assertion.TargetPreview, "\n")
						for _, line := range lines {
							out.WriteString(fmt.Sprintf("      %s\n", line))
						}
					}
					if assertion.TargetSize > 200 {
						out.WriteString("    Hint: Use --dump-rendered to see full content\n")
					}
				}
			}
		}

		out.WriteString("\n")
	}

	// Summary line
	out.WriteString(fmt.Sprintf("Tests: %d passed, %d failed, %d total (%.3fs)\n",
		results.PassedTests,
		results.FailedTests,
		results.TotalTests,
		results.Duration.Seconds()))

	return out.String()
}

// formatJSON formats results as JSON.
func formatJSON(results *TestResults) (string, error) {
	// Convert duration to seconds for JSON output
	type jsonTestResult struct {
		TestName    string            `json:"testName"`
		Description string            `json:"description,omitempty"`
		Passed      bool              `json:"passed"`
		Duration    float64           `json:"duration"`
		Assertions  []AssertionResult `json:"assertions,omitempty"`
		RenderError string            `json:"renderError,omitempty"`
	}

	type jsonResults struct {
		TotalTests  int              `json:"totalTests"`
		PassedTests int              `json:"passedTests"`
		FailedTests int              `json:"failedTests"`
		Duration    float64          `json:"duration"`
		Tests       []jsonTestResult `json:"tests"`
	}

	jr := jsonResults{
		TotalTests:  results.TotalTests,
		PassedTests: results.PassedTests,
		FailedTests: results.FailedTests,
		Duration:    results.Duration.Seconds(),
		Tests:       make([]jsonTestResult, 0, len(results.TestResults)),
	}

	for i := range results.TestResults {
		test := &results.TestResults[i]
		jr.Tests = append(jr.Tests, jsonTestResult{
			TestName:    test.TestName,
			Description: test.Description,
			Passed:      test.Passed,
			Duration:    test.Duration.Seconds(),
			Assertions:  test.Assertions,
			RenderError: test.RenderError,
		})
	}

	data, err := json.MarshalIndent(jr, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return string(data), nil
}

// formatYAML formats results as YAML.
func formatYAML(results *TestResults) (string, error) {
	// Convert duration to seconds for YAML output
	type yamlTestResult struct {
		TestName    string            `yaml:"testName"`
		Description string            `yaml:"description,omitempty"`
		Passed      bool              `yaml:"passed"`
		Duration    float64           `yaml:"duration"`
		Assertions  []AssertionResult `yaml:"assertions,omitempty"`
		RenderError string            `yaml:"renderError,omitempty"`
	}

	type yamlResults struct {
		TotalTests  int              `yaml:"totalTests"`
		PassedTests int              `yaml:"passedTests"`
		FailedTests int              `yaml:"failedTests"`
		Duration    float64          `yaml:"duration"`
		Tests       []yamlTestResult `yaml:"tests"`
	}

	yr := yamlResults{
		TotalTests:  results.TotalTests,
		PassedTests: results.PassedTests,
		FailedTests: results.FailedTests,
		Duration:    results.Duration.Seconds(),
		Tests:       make([]yamlTestResult, 0, len(results.TestResults)),
	}

	for i := range results.TestResults {
		test := &results.TestResults[i]
		yr.Tests = append(yr.Tests, yamlTestResult{
			TestName:    test.TestName,
			Description: test.Description,
			Passed:      test.Passed,
			Duration:    test.Duration.Seconds(),
			Assertions:  test.Assertions,
			RenderError: test.RenderError,
		})
	}

	data, err := yaml.Marshal(yr)
	if err != nil {
		return "", fmt.Errorf("failed to marshal YAML: %w", err)
	}

	return string(data), nil
}
