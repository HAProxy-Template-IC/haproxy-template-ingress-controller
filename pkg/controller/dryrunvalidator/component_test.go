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
	"log/slog"
	"testing"

	"haproxy-template-ic/pkg/controller/testrunner"
	"haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/dataplane"
	"haproxy-template-ic/pkg/templating"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// mapGVKToResourceType Tests
// =============================================================================

func TestMapGVKToResourceType(t *testing.T) {
	// Create a minimal component for testing
	c := &Component{
		logger: slog.Default(),
	}

	tests := []struct {
		name        string
		gvk         string
		expected    string
		expectError bool
	}{
		{
			name:        "Ingress - networking.k8s.io",
			gvk:         "networking.k8s.io/v1.Ingress",
			expected:    "ingresses",
			expectError: false,
		},
		{
			name:        "Service - core v1",
			gvk:         "v1.Service",
			expected:    "services",
			expectError: false,
		},
		{
			name:        "ConfigMap - core v1",
			gvk:         "v1.ConfigMap",
			expected:    "configmaps",
			expectError: false,
		},
		{
			name:        "Secret - core v1",
			gvk:         "v1.Secret",
			expected:    "secrets",
			expectError: false,
		},
		{
			name:        "Endpoints - core v1",
			gvk:         "v1.Endpoints",
			expected:    "endpoints",
			expectError: false,
		},
		{
			name:        "EndpointSlice - discovery.k8s.io",
			gvk:         "discovery.k8s.io/v1.EndpointSlice",
			expected:    "endpointslices",
			expectError: false,
		},
		{
			name:        "Pod - core v1",
			gvk:         "v1.Pod",
			expected:    "pods",
			expectError: false,
		},
		{
			name:        "Custom resource with group",
			gvk:         "custom.example.io/v1beta1.MyResource",
			expected:    "myresources",
			expectError: false,
		},
		{
			name:        "Invalid GVK - no dot",
			gvk:         "invalid",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Invalid GVK - only version",
			gvk:         "v1",
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.mapGVKToResourceType(tt.gvk)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid GVK")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// =============================================================================
// sortSnippetsByPriority Tests
// =============================================================================

func TestSortSnippetsByPriority(t *testing.T) {
	tests := []struct {
		name     string
		snippets map[string]config.TemplateSnippet
		expected []string
	}{
		{
			name:     "empty snippets",
			snippets: map[string]config.TemplateSnippet{},
			expected: []string{},
		},
		{
			name: "single snippet",
			snippets: map[string]config.TemplateSnippet{
				"snippet-a": {Priority: 100},
			},
			expected: []string{"snippet-a"},
		},
		{
			name: "sorted by priority ascending",
			snippets: map[string]config.TemplateSnippet{
				"snippet-c": {Priority: 300},
				"snippet-a": {Priority: 100},
				"snippet-b": {Priority: 200},
			},
			expected: []string{"snippet-a", "snippet-b", "snippet-c"},
		},
		{
			name: "same priority sorted alphabetically",
			snippets: map[string]config.TemplateSnippet{
				"charlie": {Priority: 100},
				"alpha":   {Priority: 100},
				"bravo":   {Priority: 100},
			},
			expected: []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "default priority (0 becomes 500)",
			snippets: map[string]config.TemplateSnippet{
				"explicit-high": {Priority: 600},
				"default-prio":  {Priority: 0}, // Default becomes 500
				"explicit-low":  {Priority: 100},
			},
			expected: []string{"explicit-low", "default-prio", "explicit-high"},
		},
		{
			name: "mixed priorities and names",
			snippets: map[string]config.TemplateSnippet{
				"z-low":    {Priority: 100},
				"a-medium": {Priority: 500},
				"m-low":    {Priority: 100},
				"b-high":   {Priority: 900},
			},
			expected: []string{"m-low", "z-low", "a-medium", "b-high"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Component{
				config: &config.Config{
					TemplateSnippets: tt.snippets,
				},
			}

			result := c.sortSnippetsByPriority()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// buildTestFailureError Tests
// =============================================================================

func TestBuildTestFailureError(t *testing.T) {
	tests := []struct {
		name           string
		testResults    *testrunner.TestResults
		expectedSubstr []string
	}{
		{
			name: "single failed test with render error",
			testResults: &testrunner.TestResults{
				TotalTests:  1,
				PassedTests: 0,
				FailedTests: 1,
				TestResults: []testrunner.TestResult{
					{
						TestName:    "test-render-failure",
						Passed:      false,
						RenderError: "template 'missing.cfg' not found",
						Assertions:  []testrunner.AssertionResult{},
					},
				},
			},
			expectedSubstr: []string{
				"1/1 tests failed",
				"test-render-failure",
				"Rendering failed",
				"missing.cfg",
			},
		},
		{
			name: "single failed test with assertion failure",
			testResults: &testrunner.TestResults{
				TotalTests:  1,
				PassedTests: 0,
				FailedTests: 1,
				TestResults: []testrunner.TestResult{
					{
						TestName:    "test-assertion-failure",
						Passed:      false,
						RenderError: "",
						Assertions: []testrunner.AssertionResult{
							{
								Description: "check backend exists",
								Passed:      false,
								Error:       "backend 'api' not found",
							},
						},
					},
				},
			},
			expectedSubstr: []string{
				"1/1 tests failed",
				"test-assertion-failure",
				"Assertion failed",
				"check backend exists",
				"backend 'api' not found",
			},
		},
		{
			name: "multiple failed tests",
			testResults: &testrunner.TestResults{
				TotalTests:  3,
				PassedTests: 1,
				FailedTests: 2,
				TestResults: []testrunner.TestResult{
					{
						TestName: "test-pass",
						Passed:   true,
					},
					{
						TestName:    "test-fail-1",
						Passed:      false,
						RenderError: "error 1",
					},
					{
						TestName:    "test-fail-2",
						Passed:      false,
						RenderError: "error 2",
					},
				},
			},
			expectedSubstr: []string{
				"2/3 tests failed",
				"test-fail-1",
				"test-fail-2",
				"error 1",
				"error 2",
			},
		},
		{
			name: "test with multiple assertion failures",
			testResults: &testrunner.TestResults{
				TotalTests:  1,
				PassedTests: 0,
				FailedTests: 1,
				TestResults: []testrunner.TestResult{
					{
						TestName: "multi-assert-test",
						Passed:   false,
						Assertions: []testrunner.AssertionResult{
							{
								Description: "check 1",
								Passed:      true,
							},
							{
								Description: "check 2",
								Passed:      false,
								Error:       "assertion 2 failed",
							},
							{
								Description: "check 3",
								Passed:      false,
								Error:       "assertion 3 failed",
							},
						},
					},
				},
			},
			expectedSubstr: []string{
				"multi-assert-test",
				"check 2",
				"check 3",
				"assertion 2 failed",
				"assertion 3 failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Component{}

			err := c.buildTestFailureError(tt.testResults)
			require.Error(t, err)

			errStr := err.Error()
			for _, substr := range tt.expectedSubstr {
				assert.Contains(t, errStr, substr)
			}
		})
	}
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestConstants(t *testing.T) {
	assert.Equal(t, "dryrun", ValidatorID)
	assert.Equal(t, 50, EventBufferSize)
}

// =============================================================================
// New() Tests
// =============================================================================

func TestNew(t *testing.T) {
	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:     "/etc/haproxy/maps",
		SSLCertsDir: "/etc/haproxy/ssl",
		ConfigFile:  "/etc/haproxy/haproxy.cfg",
	}

	capabilities := dataplane.Capabilities{}

	// Create minimal engine for test
	engine, err := templating.New(
		templating.EngineTypeGonja,
		map[string]string{"test.cfg": "test content"},
		nil, // customFilters
		nil, // customFunctions
		nil, // postProcessorConfigs
	)
	require.NoError(t, err)

	component := New(nil, nil, cfg, engine, validationPaths, capabilities, slog.Default())

	require.NotNil(t, component)
	assert.Equal(t, cfg, component.config)
	assert.NotNil(t, component.testRunner)
	assert.NotNil(t, component.logger)
}
