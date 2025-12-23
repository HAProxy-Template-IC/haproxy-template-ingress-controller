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
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"

	"haptic/pkg/apis/haproxytemplate/v1alpha1"
	"haptic/pkg/controller/conversion"
	"haptic/pkg/dataplane"
	"haptic/pkg/templating"
)

// Helper function to create RawExtension from map.
func mustMarshalRawExtension(obj map[string]interface{}) runtime.RawExtension {
	data, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return runtime.RawExtension{Raw: data}
}

func TestRunner_RunTests(t *testing.T) {
	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	tests := []struct {
		name            string
		config          *v1alpha1.HAProxyTemplateConfigSpec
		testName        string
		wantErr         bool
		wantTotalTests  int
		wantPassedTests int
		wantFailedTests int
		skipValidation  bool // Skip HAProxy validation for tests without HAProxy binary
	}{
		{
			name: "simple rendering test with contains assertion",
			config: &v1alpha1.HAProxyTemplateConfigSpec{
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  maxconn 1000\n",
				},
				WatchedResources: map[string]v1alpha1.WatchedResource{
					"services": {
						APIVersion: "v1",
						Resources:  "services",
						IndexBy:    []string{"metadata.namespace", "metadata.name"},
					},
				},
				ValidationTests: map[string]v1alpha1.ValidationTest{
					"basic-rendering": {
						Description: "Test basic HAProxy rendering",
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:        "contains",
								Target:      "haproxy.cfg",
								Pattern:     "maxconn 1000",
								Description: "HAProxy config should contain maxconn 1000",
							},
						},
					},
				},
			},
			wantErr:         false,
			wantTotalTests:  1,
			wantPassedTests: 1,
			wantFailedTests: 0,
			skipValidation:  true,
		},
		{
			name: "test with failing assertion",
			config: &v1alpha1.HAProxyTemplateConfigSpec{
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  maxconn 1000\n",
				},
				WatchedResources: map[string]v1alpha1.WatchedResource{
					"services": {
						APIVersion: "v1",
						Resources:  "services",
						IndexBy:    []string{"metadata.namespace", "metadata.name"},
					},
				},
				ValidationTests: map[string]v1alpha1.ValidationTest{
					"failing-test": {
						Description: "Test with failing assertion",
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:        "contains",
								Target:      "haproxy.cfg",
								Pattern:     "this-does-not-exist",
								Description: "Should not find this pattern",
							},
						},
					},
				},
			},
			wantErr:         false,
			wantTotalTests:  1,
			wantPassedTests: 0,
			wantFailedTests: 1,
			skipValidation:  true,
		},
		{
			name: "multiple tests with mixed results",
			config: &v1alpha1.HAProxyTemplateConfigSpec{
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  maxconn 1000\n",
				},
				WatchedResources: map[string]v1alpha1.WatchedResource{
					"services": {
						APIVersion: "v1",
						Resources:  "services",
						IndexBy:    []string{"metadata.namespace", "metadata.name"},
					},
				},
				ValidationTests: map[string]v1alpha1.ValidationTest{
					"passing-test": {
						Description: "This test should pass",
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:    "contains",
								Target:  "haproxy.cfg",
								Pattern: "maxconn",
							},
						},
					},
					"failing-test": {
						Description: "This test should fail",
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:    "contains",
								Target:  "haproxy.cfg",
								Pattern: "invalid-pattern",
							},
						},
					},
				},
			},
			wantErr:         false,
			wantTotalTests:  2,
			wantPassedTests: 1,
			wantFailedTests: 1,
			skipValidation:  true,
		},
		{
			name: "filter specific test by name",
			config: &v1alpha1.HAProxyTemplateConfigSpec{
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  maxconn 1000\n",
				},
				WatchedResources: map[string]v1alpha1.WatchedResource{
					"services": {
						APIVersion: "v1",
						Resources:  "services",
						IndexBy:    []string{"metadata.namespace", "metadata.name"},
					},
				},
				ValidationTests: map[string]v1alpha1.ValidationTest{
					"test-1": {
						Description: "First test",
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:    "contains",
								Target:  "haproxy.cfg",
								Pattern: "maxconn",
							},
						},
					},
					"test-2": {
						Description: "Second test",
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:    "contains",
								Target:  "haproxy.cfg",
								Pattern: "maxconn",
							},
						},
					},
				},
			},
			testName:        "test-1",
			wantErr:         false,
			wantTotalTests:  1,
			wantPassedTests: 1,
			wantFailedTests: 0,
			skipValidation:  true,
		},
		{
			name: "non-existent test name",
			config: &v1alpha1.HAProxyTemplateConfigSpec{
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  maxconn 1000\n",
				},
				WatchedResources: map[string]v1alpha1.WatchedResource{
					"services": {
						APIVersion: "v1",
						Resources:  "services",
						IndexBy:    []string{"metadata.namespace", "metadata.name"},
					},
				},
				ValidationTests: map[string]v1alpha1.ValidationTest{
					"test-1": {
						Fixtures: map[string][]runtime.RawExtension{
							"services": {},
						},
						Assertions: []v1alpha1.ValidationAssertion{
							{
								Type:    "contains",
								Target:  "haproxy.cfg",
								Pattern: "maxconn",
							},
						},
					},
				},
			},
			testName: "non-existent",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create template engine
			templates := map[string]string{
				"haproxy.cfg": tt.config.HAProxyConfig.Template,
			}
			engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
			require.NoError(t, err)

			// Convert CRD spec to internal config format
			cfg, err := conversion.ConvertSpec(tt.config)
			require.NoError(t, err)

			// Create test runner
			runner := New(
				cfg,
				engine,
				&dataplane.ValidationPaths{}, // Empty paths for unit tests
				Options{
					TestName: tt.testName,
					Logger:   logger,
				},
			)

			// Run tests
			ctx := context.Background()
			results, err := runner.RunTests(ctx, tt.testName)

			// Check error expectation
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Verify results
			assert.Equal(t, tt.wantTotalTests, results.TotalTests, "total tests mismatch")
			assert.Equal(t, tt.wantPassedTests, results.PassedTests, "passed tests mismatch")
			assert.Equal(t, tt.wantFailedTests, results.FailedTests, "failed tests mismatch")
			assert.Len(t, results.TestResults, tt.wantTotalTests, "test results length mismatch")

			// Verify AllPassed() method
			if tt.wantFailedTests == 0 && tt.wantTotalTests > 0 {
				assert.True(t, results.AllPassed(), "AllPassed() should return true")
			} else {
				assert.False(t, results.AllPassed(), "AllPassed() should return false")
			}
		})
	}
}

func TestRunner_RunTests_WithFixtures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Test with fixtures that are used in template (Scriggo syntax with direct method calls)
	config := &v1alpha1.HAProxyTemplateConfigSpec{
		TemplatingSettings: v1alpha1.TemplatingSettings{
			Engine: "scriggo",
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: `global
  maxconn 1000

{% for _, svc := range resources.services.List() -%}
{% var svcMeta = svc.(map[string]any)["metadata"].(map[string]any) -%}
{% var svcSpec = svc.(map[string]any)["spec"].(map[string]any) -%}
backend {{ svcMeta["namespace"] }}-{{ svcMeta["name"] }}
  server {{ svcMeta["name"] }} {{ svcSpec["clusterIP"] }}:80
{% end %}
`,
		},
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"with-service-fixture": {
				Description: "Test with service fixture",
				Fixtures: map[string][]runtime.RawExtension{
					"services": {
						mustMarshalRawExtension(map[string]interface{}{
							"metadata": map[string]interface{}{
								"name":      "test-service",
								"namespace": "default",
							},
							"spec": map[string]interface{}{
								"clusterIP": "10.0.0.1",
							},
						}),
					},
				},
				Assertions: []v1alpha1.ValidationAssertion{
					{
						Type:        "contains",
						Target:      "haproxy.cfg",
						Pattern:     "backend default-test-service",
						Description: "Should contain backend for test-service",
					},
					{
						Type:        "contains",
						Target:      "haproxy.cfg",
						Pattern:     "server test-service 10.0.0.1:80",
						Description: "Should contain server entry",
					},
				},
			},
		},
	}

	templates := map[string]string{
		"haproxy.cfg": config.HAProxyConfig.Template,
	}
	engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
	require.NoError(t, err)

	// Convert CRD spec to internal config format
	cfg, err := conversion.ConvertSpec(config)
	require.NoError(t, err)

	runner := New(
		cfg,
		engine,
		&dataplane.ValidationPaths{},
		Options{Logger: logger},
	)

	ctx := context.Background()
	results, err := runner.RunTests(ctx, "")
	require.NoError(t, err)

	// Verify test passed
	assert.Equal(t, 1, results.TotalTests)
	assert.Equal(t, 1, results.PassedTests)
	assert.Equal(t, 0, results.FailedTests)
	assert.True(t, results.AllPassed())

	// Verify all assertions passed
	require.Len(t, results.TestResults, 1)
	testResult := results.TestResults[0]
	assert.True(t, testResult.Passed)
	assert.Len(t, testResult.Assertions, 2)
	for _, assertion := range testResult.Assertions {
		assert.True(t, assertion.Passed, "assertion %s should pass: %s", assertion.Type, assertion.Error)
	}
}

func TestRunner_RenderError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Test with template that causes rendering error (calls fail())
	config := &v1alpha1.HAProxyTemplateConfigSpec{
		TemplatingSettings: v1alpha1.TemplatingSettings{
			Engine: "scriggo",
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			// Use fail() to cause rendering error
			Template: `{{ fail("intentional error") }}`,
		},
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"rendering-error-test": {
				Description: "Test with rendering error",
				Fixtures: map[string][]runtime.RawExtension{
					"services": {},
				},
				Assertions: []v1alpha1.ValidationAssertion{
					{
						Type:    "contains",
						Target:  "haproxy.cfg",
						Pattern: "anything",
					},
				},
			},
		},
	}

	templates := map[string]string{
		"haproxy.cfg": config.HAProxyConfig.Template,
	}
	engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
	require.NoError(t, err)

	// Convert CRD spec to internal config format
	cfg, err := conversion.ConvertSpec(config)
	require.NoError(t, err)

	runner := New(
		cfg,
		engine,
		&dataplane.ValidationPaths{},
		Options{Logger: logger},
	)

	ctx := context.Background()
	results, err := runner.RunTests(ctx, "")
	require.NoError(t, err)

	// Verify test failed due to rendering error
	assert.Equal(t, 1, results.TotalTests)
	assert.Equal(t, 0, results.PassedTests)
	assert.Equal(t, 1, results.FailedTests)
	assert.False(t, results.AllPassed())

	// Verify rendering error is captured
	require.Len(t, results.TestResults, 1)
	testResult := results.TestResults[0]
	assert.False(t, testResult.Passed)
	assert.NotEmpty(t, testResult.RenderError, "render error should be populated")

	// Verify rendering failure is added as assertion, plus the original assertion also failed
	assert.Len(t, testResult.Assertions, 2)
	assert.Equal(t, "rendering", testResult.Assertions[0].Type)
	assert.False(t, testResult.Assertions[0].Passed)
	assert.Equal(t, "contains", testResult.Assertions[1].Type)
	assert.False(t, testResult.Assertions[1].Passed)
}

func TestTestResults_AllPassed(t *testing.T) {
	tests := []struct {
		name   string
		result *TestResults
		want   bool
	}{
		{
			name: "all tests passed",
			result: &TestResults{
				TotalTests:  2,
				PassedTests: 2,
				FailedTests: 0,
			},
			want: true,
		},
		{
			name: "some tests failed",
			result: &TestResults{
				TotalTests:  2,
				PassedTests: 1,
				FailedTests: 1,
			},
			want: false,
		},
		{
			name: "all tests failed",
			result: &TestResults{
				TotalTests:  2,
				PassedTests: 0,
				FailedTests: 2,
			},
			want: false,
		},
		{
			name: "no tests run",
			result: &TestResults{
				TotalTests:  0,
				PassedTests: 0,
				FailedTests: 0,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.AllPassed()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunner_RunTests_WithHTTPFixtures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Test with HTTP fixtures used in template
	config := &v1alpha1.HAProxyTemplateConfigSpec{
		TemplatingSettings: v1alpha1.TemplatingSettings{
			Engine: "scriggo",
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: `global
  maxconn 1000
`,
		},
		Maps: map[string]v1alpha1.MapFile{
			"blocklist.map": {
				Template: `{%- var blocklist, err = http.Fetch("http://blocklist.example.com/list.txt") -%}
{%- if err != nil %}{{ err }}{% end -%}
{%- var blocklistStr = blocklist.(string) -%}
{%- for _, line := range split(blocklistStr, "\n") -%}
{%- var value = trim(line, " \t\n\r") -%}
{%- if value != "" -%}
{{ value }} 1
{% end -%}
{%- end %}`,
			},
		},
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"http-fixture-test": {
				Description: "Test with HTTP fixture",
				Fixtures: map[string][]runtime.RawExtension{
					"services": {},
				},
				HTTPResources: []v1alpha1.HTTPResourceFixture{
					{
						URL:     "http://blocklist.example.com/list.txt",
						Content: "blocked-value-1\nblocked-value-2\nblocked-value-3",
					},
				},
				Assertions: []v1alpha1.ValidationAssertion{
					{
						Type:        "contains",
						Target:      "map:blocklist.map",
						Pattern:     "blocked-value-1",
						Description: "Blocklist map should contain blocked-value-1",
					},
					{
						Type:        "contains",
						Target:      "map:blocklist.map",
						Pattern:     "blocked-value-2",
						Description: "Blocklist map should contain blocked-value-2",
					},
					{
						Type:        "contains",
						Target:      "map:blocklist.map",
						Pattern:     "blocked-value-3",
						Description: "Blocklist map should contain blocked-value-3",
					},
				},
			},
		},
	}

	// Create template engine
	templates := map[string]string{
		"haproxy.cfg":   config.HAProxyConfig.Template,
		"blocklist.map": config.Maps["blocklist.map"].Template,
	}
	engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
	require.NoError(t, err)

	// Convert CRD spec to internal config format
	cfg, err := conversion.ConvertSpec(config)
	require.NoError(t, err)

	// Create test runner
	runner := New(
		cfg,
		engine,
		&dataplane.ValidationPaths{}, // Empty paths for unit tests
		Options{
			Logger: logger,
		},
	)

	// Run tests
	ctx := context.Background()
	results, err := runner.RunTests(ctx, "")
	require.NoError(t, err)

	// Verify results
	assert.Equal(t, 1, results.TotalTests, "should have 1 test")
	assert.Equal(t, 1, results.PassedTests, "test should pass")
	assert.Equal(t, 0, results.FailedTests, "no tests should fail")

	// Verify rendered map contains all expected values
	require.Len(t, results.TestResults, 1)
	testResult := results.TestResults[0]
	assert.True(t, testResult.Passed, "test should pass")
	assert.Len(t, testResult.Assertions, 3, "should have 3 assertions")
	for _, assertion := range testResult.Assertions {
		assert.True(t, assertion.Passed, "assertion should pass: %s", assertion.Description)
	}
}

func TestRunner_RunTests_HTTPFixtureMissing(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Test that missing HTTP fixture causes test failure
	config := &v1alpha1.HAProxyTemplateConfigSpec{
		TemplatingSettings: v1alpha1.TemplatingSettings{
			Engine: "scriggo",
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: `{%- var content, err = http.Fetch("http://missing.example.com/data.txt") -%}
{%- if err != nil %}{{ fail(err.(error).Error()) }}{% end -%}
global
  maxconn 1000
`,
		},
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"missing-http-fixture": {
				Description: "Test with missing HTTP fixture",
				Fixtures: map[string][]runtime.RawExtension{
					"services": {},
				},
				// No HTTPResources defined - should fail when http.Fetch is called
				Assertions: []v1alpha1.ValidationAssertion{
					{
						Type:        "contains",
						Target:      "haproxy.cfg",
						Pattern:     "maxconn",
						Description: "Config should contain maxconn",
					},
				},
			},
		},
	}

	// Create template engine
	templates := map[string]string{
		"haproxy.cfg": config.HAProxyConfig.Template,
	}
	engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
	require.NoError(t, err)

	// Convert CRD spec to internal config format
	cfg, err := conversion.ConvertSpec(config)
	require.NoError(t, err)

	// Create test runner
	runner := New(
		cfg,
		engine,
		&dataplane.ValidationPaths{}, // Empty paths for unit tests
		Options{
			Logger: logger,
		},
	)

	// Run tests
	ctx := context.Background()
	results, err := runner.RunTests(ctx, "")
	require.NoError(t, err)

	// Verify test failed due to missing HTTP fixture
	assert.Equal(t, 1, results.TotalTests, "should have 1 test")
	assert.Equal(t, 0, results.PassedTests, "test should fail")
	assert.Equal(t, 1, results.FailedTests, "1 test should fail")

	require.Len(t, results.TestResults, 1)
	testResult := results.TestResults[0]
	assert.False(t, testResult.Passed, "test should fail")
	assert.Contains(t, testResult.RenderError, "no fixture defined for URL")
}
