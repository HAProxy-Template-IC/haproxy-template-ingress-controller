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
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

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

func TestConstants(t *testing.T) {
	assert.Equal(t, "dryrun", ValidatorID)
	assert.Equal(t, 50, EventBufferSize)
}

func TestCreateOverlay(t *testing.T) {
	c := &Component{
		logger: slog.Default(),
	}

	tests := []struct {
		name                string
		operation           string
		object              interface{}
		expectAdditions     int
		expectModifications int
		expectDeletions     int
	}{
		{
			name:                "CREATE operation",
			operation:           "CREATE",
			object:              createTestIngress("test-ingress", "default"),
			expectAdditions:     1,
			expectModifications: 0,
			expectDeletions:     0,
		},
		{
			name:                "UPDATE operation",
			operation:           "UPDATE",
			object:              createTestIngress("test-ingress", "default"),
			expectAdditions:     0,
			expectModifications: 1,
			expectDeletions:     0,
		},
		{
			name:                "DELETE operation",
			operation:           "DELETE",
			object:              nil,
			expectAdditions:     0,
			expectModifications: 0,
			expectDeletions:     1,
		},
		{
			name:                "Unknown operation",
			operation:           "UNKNOWN",
			object:              createTestIngress("test-ingress", "default"),
			expectAdditions:     0,
			expectModifications: 0,
			expectDeletions:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overlay := c.createOverlay("default", "test-ingress", tt.object, tt.operation, "test-req")

			assert.Len(t, overlay.Additions, tt.expectAdditions)
			assert.Len(t, overlay.Modifications, tt.expectModifications)
			assert.Len(t, overlay.Deletions, tt.expectDeletions)
		})
	}
}

func TestSimplifyError(t *testing.T) {
	c := &Component{}

	tests := []struct {
		name     string
		phase    string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			phase:    "render",
			err:      nil,
			expected: "",
		},
		{
			name:     "render phase",
			phase:    "render",
			err:      errors.New("template error"),
			expected: "template error",
		},
		{
			name:     "syntax phase",
			phase:    "syntax",
			err:      errors.New("syntax error"),
			expected: "syntax error",
		},
		{
			name:     "unknown phase",
			phase:    "unknown",
			err:      errors.New("some error"),
			expected: "some error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.simplifyError(tt.phase, tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNew(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

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
		templating.EngineTypeScriggo,
		map[string]string{"test.cfg": "test content"},
		nil, // customFilters
		nil, // customFunctions
		nil, // postProcessorConfigs
	)
	require.NoError(t, err)

	// Create mock ProposalValidator
	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      capabilities,
		Logger:            logger,
	})

	require.NotNil(t, component)
	assert.Equal(t, cfg, component.config)
	assert.NotNil(t, component.logger)
	assert.NotNil(t, component.eventChan, "eventChan should be set by constructor")
}

func TestStart_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:     "/etc/haproxy/maps",
		SSLCertsDir: "/etc/haproxy/ssl",
		ConfigFile:  "/etc/haproxy/haproxy.cfg",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": "# empty config"},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- component.Start(ctx)
	}()

	// Give component time to start
	time.Sleep(testutil.StartupDelay)

	// Cancel context
	cancel()

	// Verify component stops cleanly
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("component did not stop after context cancellation")
	}
}

func TestHandleEvent_IgnoresNonValidationEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{}
	validationPaths := &dataplane.ValidationPaths{}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": "# empty"},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	// handleEvent with non-validation event should not panic
	// This tests the type switch in handleEvent
	component.handleEvent(&events.ConfigParsedEvent{})
}

func TestPublishResponse_Allowed(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{}
	validationPaths := &dataplane.ValidationPaths{}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": "# empty"},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Publish allowed response
	component.publishResponse("test-req-123", true, "")

	// Verify event was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-123", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.True(t, event.Allowed)
	assert.Empty(t, event.Reason)
}

func TestPublishResponse_Denied(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{}
	validationPaths := &dataplane.ValidationPaths{}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": "# empty"},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Publish denied response
	component.publishResponse("test-req-456", false, "validation failed: invalid config")

	// Verify event was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-456", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.False(t, event.Allowed)
	assert.Equal(t, "validation failed: invalid config", event.Reason)
}

func TestHandleValidationRequest_InvalidGVK(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:     "/etc/haproxy/maps",
		SSLCertsDir: "/etc/haproxy/ssl",
		ConfigFile:  "/etc/haproxy/haproxy.cfg",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": "# empty config"},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Create validation request with invalid GVK (no dot separator)
	req := &events.WebhookValidationRequest{
		ID:        "test-req-invalid-gvk",
		GVK:       "invalid", // Missing version.Kind
		Namespace: "default",
		Name:      "test-resource",
		Operation: "CREATE",
	}

	// Handle the request
	component.handleValidationRequest(req)

	// Verify error response was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-invalid-gvk", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.False(t, event.Allowed)
	assert.Contains(t, event.Reason, "unsupported resource type")
	assert.Contains(t, event.Reason, "invalid GVK")
}

// TestHandleValidationRequest_CreateSuccess tests the full flow for a CREATE operation.
func TestHandleValidationRequest_CreateSuccess(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:     "/etc/haproxy/maps",
		SSLCertsDir: "/etc/haproxy/ssl",
		ConfigFile:  "/etc/haproxy/haproxy.cfg",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   validationPaths,
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	req := &events.WebhookValidationRequest{
		ID:        "test-create",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    createTestIngress("test-ingress", "default"),
	}

	component.handleValidationRequest(req)

	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-create", event.RequestID())
	assert.True(t, event.Allowed)
	assert.Empty(t, event.Reason)
}

// TestHandleValidationRequest_UpdateSuccess tests the full flow for an UPDATE operation.
func TestHandleValidationRequest_UpdateSuccess(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine: func() templating.Engine {
			e, _ := templating.New(
				templating.EngineTypeScriggo,
				map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
				nil, nil, nil,
			)
			return e
		}(),
		ValidationPaths: &dataplane.ValidationPaths{},
		Capabilities:    dataplane.Capabilities{},
		Logger:          logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	req := &events.WebhookValidationRequest{
		ID:        "test-update",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "staging",
		Name:      "updated-ingress",
		Operation: "UPDATE",
		Object:    createTestIngress("updated-ingress", "staging"),
	}

	component.handleValidationRequest(req)

	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-update", event.RequestID())
	assert.True(t, event.Allowed)
}

// TestHandleValidationRequest_DeleteSuccess tests the full flow for a DELETE operation.
func TestHandleValidationRequest_DeleteSuccess(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine: func() templating.Engine {
			e, _ := templating.New(
				templating.EngineTypeScriggo,
				map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
				nil, nil, nil,
			)
			return e
		}(),
		ValidationPaths: &dataplane.ValidationPaths{},
		Capabilities:    dataplane.Capabilities{},
		Logger:          logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	req := &events.WebhookValidationRequest{
		ID:        "test-delete",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "DELETE",
		Object:    nil,
	}

	component.handleValidationRequest(req)

	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-delete", event.RequestID())
	assert.True(t, event.Allowed)
}

// TestHandleValidationRequest_RenderFailure tests that a render failure produces
// a denied response with a simplified error message.
func TestHandleValidationRequest_RenderFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	// Create a proposal validator with an invalid template that will fail rendering
	failingEngine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": `{{ fail("Service 'api' not found") }}`},
		nil, nil, nil,
	)
	require.NoError(t, err)

	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: failingEngine,
		Config: &config.Config{},
		Logger: logger,
	})

	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger,
		SkipDNSValidation: true,
	})

	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderService,
		Validator: validationService,
		Logger:    logger,
	})

	baseStoreProvider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": &mockStore{},
		"services":  &mockStore{},
	})

	failingProposalValidator := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStoreProvider,
		Logger:            logger,
	})

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: failingProposalValidator,
		Config:            cfg,
		Engine:            failingEngine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	req := &events.WebhookValidationRequest{
		ID:        "test-render-fail",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    createTestIngress("test-ingress", "default"),
	}

	component.handleValidationRequest(req)

	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-render-fail", event.RequestID())
	assert.False(t, event.Allowed)
	assert.Contains(t, event.Reason, "Service 'api' not found")
}

// TestHandleValidationRequest_OverlayReferencesInvalidStore tests that overlays
// referencing non-existent stores produce a denial.
func TestHandleValidationRequest_OverlayReferencesInvalidStore(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	// Create proposal validator with store provider that has NO stores
	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
		nil, nil, nil,
	)
	require.NoError(t, err)

	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: engine,
		Config: &config.Config{},
		Logger: logger,
	})

	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger,
		SkipDNSValidation: true,
	})

	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderService,
		Validator: validationService,
		Logger:    logger,
	})

	// Empty store provider — overlay for "ingresses" will fail validation
	emptyStoreProvider := stores.NewRealStoreProvider(map[string]stores.Store{})

	noStoreProposalValidator := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: emptyStoreProvider,
		Logger:            logger,
	})

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: noStoreProposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	req := &events.WebhookValidationRequest{
		ID:        "test-no-store",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    createTestIngress("test-ingress", "default"),
	}

	component.handleValidationRequest(req)

	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-no-store", event.RequestID())
	assert.False(t, event.Allowed)
	assert.Contains(t, event.Reason, "non-existent store")
}

// TestValidateDirect_Success tests the synchronous validation path.
func TestValidateDirect_Success(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason := component.ValidateDirect(
		context.Background(),
		"networking.k8s.io/v1.Ingress",
		"default",
		"test-ingress",
		createTestIngress("test-ingress", "default"),
		"CREATE",
	)

	assert.True(t, allowed)
	assert.Empty(t, reason)
}

// TestValidateDirect_InvalidGVK tests that ValidateDirect rejects invalid GVKs.
func TestValidateDirect_InvalidGVK(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		ValidationTests: map[string]config.ValidationTest{},
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
		nil, nil, nil,
	)
	require.NoError(t, err)

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason := component.ValidateDirect(
		context.Background(),
		"invalid",
		"default",
		"test",
		nil,
		"CREATE",
	)

	assert.False(t, allowed)
	assert.Contains(t, reason, "unsupported resource type")
}

// TestValidateDirect_ValidationFailure tests that ValidateDirect returns denied for failures.
func TestValidateDirect_ValidationFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	// Use a template that always fails
	failingEngine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": `{{ fail("invalid config") }}`},
		nil, nil, nil,
	)
	require.NoError(t, err)

	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: failingEngine,
		Config: &config.Config{},
		Logger: logger,
	})

	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger,
		SkipDNSValidation: true,
	})

	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderService,
		Validator: validationService,
		Logger:    logger,
	})

	baseStoreProvider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": &mockStore{},
	})

	failingProposalValidator := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStoreProvider,
		Logger:            logger,
	})

	component := New(&ComponentConfig{
		EventBus:          bus,
		ProposalValidator: failingProposalValidator,
		Config:            cfg,
		Engine:            failingEngine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason := component.ValidateDirect(
		context.Background(),
		"networking.k8s.io/v1.Ingress",
		"default",
		"test-ingress",
		createTestIngress("test-ingress", "default"),
		"CREATE",
	)

	assert.False(t, allowed)
	assert.Contains(t, reason, "invalid config")
}

// createMockProposalValidator creates a minimal ProposalValidator for testing.
func createMockProposalValidator(bus *busevents.EventBus, logger *slog.Logger) *proposalvalidator.Component {
	// Create minimal render service
	engine, _ := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": testutil.ValidHAProxyConfigTemplate},
		nil, nil, nil,
	)

	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: engine,
		Config: &config.Config{},
		Logger: logger,
	})

	// Create minimal validation service
	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger,
		SkipDNSValidation: true,
	})

	// Create pipeline
	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderService,
		Validator: validationService,
		Logger:    logger,
	})

	// Create base store provider
	baseStoreProvider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": &mockStore{},
		"services":  &mockStore{},
	})

	return proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStoreProvider,
		Logger:            logger,
	})
}

// createTestIngress creates a test unstructured ingress object.
func createTestIngress(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "Ingress",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"rules": []interface{}{},
			},
		},
	}
}

// mockStore implements stores.Store for testing purposes.
type mockStore struct{}

func (m *mockStore) Get(_ ...string) ([]interface{}, error) { return nil, nil }
func (m *mockStore) List() ([]interface{}, error)           { return nil, nil }
func (m *mockStore) Add(_ interface{}, _ []string) error    { return nil }
func (m *mockStore) Update(_ interface{}, _ []string) error { return nil }
func (m *mockStore) Delete(_ ...string) error               { return nil }
func (m *mockStore) Clear() error                           { return nil }
