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
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
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
// sortSnippetNames Tests
// =============================================================================

func TestSortSnippetNames(t *testing.T) {
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
				"snippet-a": {},
			},
			expected: []string{"snippet-a"},
		},
		{
			name: "alphabetical sorting",
			snippets: map[string]config.TemplateSnippet{
				"zebra":   {},
				"alpha":   {},
				"charlie": {},
			},
			expected: []string{"alpha", "charlie", "zebra"},
		},
		{
			name: "ordering encoded in names with numbers",
			snippets: map[string]config.TemplateSnippet{
				"features-500-ssl":            {},
				"features-050-initialization": {},
				"features-150-crtlist":        {},
			},
			expected: []string{
				"features-050-initialization",
				"features-150-crtlist",
				"features-500-ssl",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rendercontext.SortSnippetNames(tt.snippets)
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

	// EventBus is required since constructor subscribes
	component := New(bus, nil, cfg, engine, validationPaths, capabilities, logger)

	require.NotNil(t, component)
	assert.Equal(t, cfg, component.config)
	assert.NotNil(t, component.testRunner)
	assert.NotNil(t, component.logger)
	assert.NotNil(t, component.eventChan, "eventChan should be set by constructor")
}

// =============================================================================
// Start() Tests
// =============================================================================

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

	component := New(bus, nil, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

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

// =============================================================================
// handleEvent() Tests
// =============================================================================

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

	component := New(bus, nil, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	// handleEvent with non-validation event should not panic
	// This tests the type switch in handleEvent
	component.handleEvent(&events.ConfigParsedEvent{})
}

// =============================================================================
// publishResponse() Tests
// =============================================================================

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

	component := New(bus, nil, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
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

	component := New(bus, nil, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
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

// =============================================================================
// buildRenderingContext() Tests
// =============================================================================

func TestBuildRenderingContext(t *testing.T) {
	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{
			"snippet-a": {},
			"snippet-b": {},
		},
	}

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           "/etc/haproxy/maps",
		SSLCertsDir:       "/etc/haproxy/ssl",
		CRTListDir:        "/etc/haproxy/crt-list",
		GeneralStorageDir: "/etc/haproxy/general",
	}

	logger := testutil.NewTestLogger()

	component := &Component{
		config:          cfg,
		validationPaths: validationPaths,
		logger:          logger,
	}

	// Create mock stores
	stores := map[string]types.Store{
		"ingresses": &mockStore{},
		"services":  &mockStore{},
	}

	ctx := component.buildRenderingContext(stores)

	// Verify context structure
	assert.NotNil(t, ctx["resources"])
	assert.NotNil(t, ctx["templateSnippets"])
	assert.NotNil(t, ctx["pathResolver"])

	// Verify templateSnippets are sorted
	snippets, ok := ctx["templateSnippets"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"snippet-a", "snippet-b"}, snippets)

	// Verify pathResolver
	resolver, ok := ctx["pathResolver"].(*templating.PathResolver)
	require.True(t, ok)
	assert.Equal(t, "/etc/haproxy/maps", resolver.MapsDir)
	assert.Equal(t, "/etc/haproxy/ssl", resolver.SSLDir)
	assert.Equal(t, "/etc/haproxy/crt-list", resolver.CRTListDir)
	assert.Equal(t, "/etc/haproxy/general", resolver.GeneralDir)
}

// =============================================================================
// renderAuxiliaryFiles() Tests
// =============================================================================

func TestRenderAuxiliaryFiles_MapFiles(t *testing.T) {
	cfg := &config.Config{
		Maps: map[string]config.MapFile{
			"hosts.map": {},
		},
	}

	// Create engine with map template
	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"hosts.map": "example.com backend1\ntest.com backend2",
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	component := &Component{
		config: cfg,
		engine: engine,
		logger: testutil.NewTestLogger(),
	}

	renderCtx := map[string]interface{}{}

	auxFiles, err := component.renderAuxiliaryFiles(renderCtx)

	require.NoError(t, err)
	require.Len(t, auxFiles.MapFiles, 1)
	assert.Equal(t, "hosts.map", auxFiles.MapFiles[0].Path)
	assert.Contains(t, auxFiles.MapFiles[0].Content, "example.com")
}

func TestRenderAuxiliaryFiles_GeneralFiles(t *testing.T) {
	cfg := &config.Config{
		Files: map[string]config.GeneralFile{
			"custom.txt": {},
		},
	}

	// Create engine with general file template
	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"custom.txt": "custom content here",
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	component := &Component{
		config: cfg,
		engine: engine,
		logger: testutil.NewTestLogger(),
	}

	renderCtx := map[string]interface{}{}

	auxFiles, err := component.renderAuxiliaryFiles(renderCtx)

	require.NoError(t, err)
	require.Len(t, auxFiles.GeneralFiles, 1)
	assert.Equal(t, "custom.txt", auxFiles.GeneralFiles[0].Filename)
	// Scriggo adds trailing newline
	assert.Equal(t, "custom content here\n", auxFiles.GeneralFiles[0].Content)
}

func TestRenderAuxiliaryFiles_SSLCertificates(t *testing.T) {
	cfg := &config.Config{
		SSLCertificates: map[string]config.SSLCertificate{
			"server.pem": {},
		},
	}

	// Create engine with SSL cert template
	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"server.pem": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	component := &Component{
		config: cfg,
		engine: engine,
		logger: testutil.NewTestLogger(),
	}

	renderCtx := map[string]interface{}{}

	auxFiles, err := component.renderAuxiliaryFiles(renderCtx)

	require.NoError(t, err)
	require.Len(t, auxFiles.SSLCertificates, 1)
	assert.Equal(t, "server.pem", auxFiles.SSLCertificates[0].Path)
	assert.Contains(t, auxFiles.SSLCertificates[0].Content, "BEGIN CERTIFICATE")
}

func TestRenderAuxiliaryFiles_RenderError(t *testing.T) {
	cfg := &config.Config{
		Maps: map[string]config.MapFile{
			"missing.map": {},
		},
	}

	// Create engine WITHOUT the map template
	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": "# empty",
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	component := &Component{
		config: cfg,
		engine: engine,
		logger: testutil.NewTestLogger(),
	}

	renderCtx := map[string]interface{}{}

	_, err = component.renderAuxiliaryFiles(renderCtx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to render map file")
}

func TestRenderAuxiliaryFiles_Empty(t *testing.T) {
	cfg := &config.Config{
		Maps:            map[string]config.MapFile{},
		Files:           map[string]config.GeneralFile{},
		SSLCertificates: map[string]config.SSLCertificate{},
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{},
		nil, nil, nil,
	)
	require.NoError(t, err)

	component := &Component{
		config: cfg,
		engine: engine,
		logger: testutil.NewTestLogger(),
	}

	renderCtx := map[string]interface{}{}

	auxFiles, err := component.renderAuxiliaryFiles(renderCtx)

	require.NoError(t, err)
	assert.Empty(t, auxFiles.MapFiles)
	assert.Empty(t, auxFiles.GeneralFiles)
	assert.Empty(t, auxFiles.SSLCertificates)
}

// =============================================================================
// Mock Store for Testing
// =============================================================================

// mockStore implements types.Store for testing purposes.
type mockStore struct{}

func (m *mockStore) Get(_ ...string) ([]interface{}, error)           { return nil, nil }
func (m *mockStore) List() ([]interface{}, error)                     { return nil, nil }
func (m *mockStore) Add(resource interface{}, keys []string) error    { return nil }
func (m *mockStore) Update(resource interface{}, keys []string) error { return nil }
func (m *mockStore) Delete(keys ...string) error                      { return nil }
func (m *mockStore) Clear() error                                     { return nil }

// =============================================================================
// handleValidationRequest() Tests
// =============================================================================

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

	// Create component without store manager
	component := New(bus, nil, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
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

func TestHandleValidationRequest_StoreNotRegistered(t *testing.T) {
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

	// Create a store manager without any stores registered
	storeManager := resourcestore.NewManager()
	// Intentionally NOT registering any stores

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	// Create validation request for Ingress (store not registered)
	req := &events.WebhookValidationRequest{
		ID:        "test-req-no-store",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
	}

	// Handle the request
	component.handleValidationRequest(req)

	// Verify error response was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-no-store", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.False(t, event.Allowed)
	assert.Contains(t, event.Reason, "no store registered for ingresses")
}

func TestHandleValidationRequest_RenderingError(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           "/etc/haproxy/maps",
		SSLCertsDir:       "/etc/haproxy/ssl",
		ConfigFile:        "/etc/haproxy/haproxy.cfg",
		CRTListDir:        "/etc/haproxy/crt-list",
		GeneralStorageDir: "/etc/haproxy/general",
	}

	// Create engine with template that will fail rendering
	// Use fail() function to trigger rendering error
	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": `{{ fail("intentional rendering error") }}`,
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	// Create store manager with mock store
	storeManager := resourcestore.NewManager()
	storeManager.RegisterStore("ingresses", &mockStore{})

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	// Create validation request
	req := &events.WebhookValidationRequest{
		ID:        "test-req-render-error",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "test-ingress"}},
	}

	// Handle the request
	component.handleValidationRequest(req)

	// Verify error response was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-render-error", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.False(t, event.Allowed)
	// The error should indicate a rendering failure
	assert.NotEmpty(t, event.Reason)
}

func TestHandleValidationRequest_Success(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	// Create temp directories for validation paths
	tmpDir := t.TempDir()

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           tmpDir + "/maps",
		SSLCertsDir:       tmpDir + "/ssl",
		ConfigFile:        tmpDir + "/haproxy.cfg",
		CRTListDir:        tmpDir + "/crt-list",
		GeneralStorageDir: tmpDir + "/general",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": testutil.ValidHAProxyConfigTemplate,
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	// Create store manager with mock store
	storeManager := resourcestore.NewManager()
	storeManager.RegisterStore("ingresses", &mockStore{})

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	// Create validation request
	req := &events.WebhookValidationRequest{
		ID:        "test-req-success",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "test-ingress"}},
	}

	// Handle the request
	component.handleValidationRequest(req)

	// Verify success response was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-success", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.True(t, event.Allowed)
	assert.Empty(t, event.Reason)
}

func TestHandleValidationRequest_UpdateOperation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	// Create temp directories for validation paths
	tmpDir := t.TempDir()

	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           tmpDir + "/maps",
		SSLCertsDir:       tmpDir + "/ssl",
		ConfigFile:        tmpDir + "/haproxy.cfg",
		CRTListDir:        tmpDir + "/crt-list",
		GeneralStorageDir: tmpDir + "/general",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": testutil.ValidHAProxyConfigTemplate,
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	// Create store manager with mock store for services
	storeManager := resourcestore.NewManager()
	storeManager.RegisterStore("services", &mockStore{})

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	// Create UPDATE validation request
	req := &events.WebhookValidationRequest{
		ID:        "test-req-update",
		GVK:       "v1.Service",
		Namespace: "default",
		Name:      "my-service",
		Operation: "UPDATE",
		Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "my-service"}},
	}

	// Handle the request
	component.handleValidationRequest(req)

	// Verify success response was published
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test-req-update", event.RequestID())
	assert.Equal(t, ValidatorID, event.ValidatorID)
	assert.True(t, event.Allowed)
	assert.Empty(t, event.Reason)
}

func TestHandleValidationRequest_DifferentResourceTypes(t *testing.T) {
	tests := []struct {
		name         string
		gvk          string
		resourceType string
	}{
		{
			name:         "Ingress",
			gvk:          "networking.k8s.io/v1.Ingress",
			resourceType: "ingresses",
		},
		{
			name:         "Service",
			gvk:          "v1.Service",
			resourceType: "services",
		},
		{
			name:         "ConfigMap",
			gvk:          "v1.ConfigMap",
			resourceType: "configmaps",
		},
		{
			name:         "Secret",
			gvk:          "v1.Secret",
			resourceType: "secrets",
		},
		{
			name:         "EndpointSlice",
			gvk:          "discovery.k8s.io/v1.EndpointSlice",
			resourceType: "endpointslices",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()

			cfg := &config.Config{
				TemplateSnippets: map[string]config.TemplateSnippet{},
				ValidationTests:  map[string]config.ValidationTest{},
			}

			tmpDir := t.TempDir()
			validationPaths := &dataplane.ValidationPaths{
				MapsDir:           tmpDir + "/maps",
				SSLCertsDir:       tmpDir + "/ssl",
				ConfigFile:        tmpDir + "/haproxy.cfg",
				CRTListDir:        tmpDir + "/crt-list",
				GeneralStorageDir: tmpDir + "/general",
			}

			engine, err := templating.New(
				templating.EngineTypeScriggo,
				map[string]string{
					"haproxy.cfg": testutil.ValidHAProxyConfigTemplate,
				},
				nil, nil, nil,
			)
			require.NoError(t, err)

			// Register the specific resource type store
			storeManager := resourcestore.NewManager()
			storeManager.RegisterStore(tt.resourceType, &mockStore{})

			component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

			eventChan := bus.Subscribe(10)
			bus.Start()

			req := &events.WebhookValidationRequest{
				ID:        "test-req-" + tt.resourceType,
				GVK:       tt.gvk,
				Namespace: "default",
				Name:      "test-resource",
				Operation: "CREATE",
				Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "test-resource"}},
			}

			component.handleValidationRequest(req)

			event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.EventTimeout)
			assert.Equal(t, "test-req-"+tt.resourceType, event.RequestID())
			assert.True(t, event.Allowed, "Expected allowed for %s", tt.name)
		})
	}
}

// =============================================================================
// runValidationTests() Tests
// =============================================================================

func TestRunValidationTests_AllTestsPass(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create config with validation tests that will pass
	// HAProxyConfig.Template is used by testRunner to render config for validation tests
	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		HAProxyConfig: config.HAProxyConfig{
			Template: testutil.ValidHAProxyConfigTemplate,
		},
		// WatchedResources is required for testRunner to create fixture stores
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]config.ValidationTest{
			"test-contains-backend": {
				Description: "Test that backend exists",
				Fixtures: map[string][]interface{}{
					"ingresses": {},
				},
				Assertions: []config.ValidationAssertion{
					{
						Type:        "contains",
						Description: "Config contains backend",
						Target:      "haproxy.cfg",
						Pattern:     "backend test_backend",
					},
				},
			},
		},
	}

	tmpDir := t.TempDir()
	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           tmpDir + "/maps",
		SSLCertsDir:       tmpDir + "/ssl",
		ConfigFile:        tmpDir + "/haproxy.cfg",
		CRTListDir:        tmpDir + "/crt-list",
		GeneralStorageDir: tmpDir + "/general",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": testutil.ValidHAProxyConfigTemplate,
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	storeManager := resourcestore.NewManager()
	storeManager.RegisterStore("ingresses", &mockStore{})

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	// Create validation request (validation tests run when c.config.ValidationTests > 0)
	req := &events.WebhookValidationRequest{
		ID:        "test-req-with-tests-pass",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "test-ingress"}},
	}

	component.handleValidationRequest(req)

	// Verify success response (validation tests passed)
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "test-req-with-tests-pass", event.RequestID())
	assert.True(t, event.Allowed, "Expected allowed when validation tests pass")
	assert.Empty(t, event.Reason)
}

func TestRunValidationTests_TestsFail(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create config with validation tests that will fail
	// (HAProxy config does NOT contain the pattern "nonexistent_backend_that_does_not_exist")
	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		// HAProxyConfig.Template is used by testRunner to render config for validation tests
		HAProxyConfig: config.HAProxyConfig{
			Template: testutil.ValidHAProxyConfigTemplate,
		},
		// WatchedResources is required for testRunner to create fixture stores
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]config.ValidationTest{
			"test-contains-nonexistent": {
				Description: "Test that nonexistent backend exists",
				Fixtures: map[string][]interface{}{
					"ingresses": {},
				},
				Assertions: []config.ValidationAssertion{
					{
						Type:        "contains",
						Description: "Config contains nonexistent backend",
						Target:      "haproxy.cfg",
						Pattern:     "backend nonexistent_backend_that_does_not_exist",
					},
				},
			},
		},
	}

	tmpDir := t.TempDir()
	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           tmpDir + "/maps",
		SSLCertsDir:       tmpDir + "/ssl",
		ConfigFile:        tmpDir + "/haproxy.cfg",
		CRTListDir:        tmpDir + "/crt-list",
		GeneralStorageDir: tmpDir + "/general",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": testutil.ValidHAProxyConfigTemplate,
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	storeManager := resourcestore.NewManager()
	storeManager.RegisterStore("ingresses", &mockStore{})

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	// Create validation request
	req := &events.WebhookValidationRequest{
		ID:        "test-req-with-tests-fail",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "test-ingress"}},
	}

	component.handleValidationRequest(req)

	// Verify rejection response (validation tests failed)
	event := testutil.WaitForEvent[*events.WebhookValidationResponse](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "test-req-with-tests-fail", event.RequestID())
	assert.False(t, event.Allowed, "Expected denied when validation tests fail")
	assert.Contains(t, event.Reason, "tests failed")
}

func TestRunValidationTests_PublishesEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create config with validation tests
	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		// HAProxyConfig.Template is used by testRunner to render config for validation tests
		HAProxyConfig: config.HAProxyConfig{
			Template: testutil.ValidHAProxyConfigTemplate,
		},
		// WatchedResources is required for testRunner to create fixture stores
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		ValidationTests: map[string]config.ValidationTest{
			"test-basic": {
				Description: "Basic test",
				Fixtures: map[string][]interface{}{
					"ingresses": {},
				},
				Assertions: []config.ValidationAssertion{
					{
						Type:        "contains",
						Description: "Config contains defaults",
						Target:      "haproxy.cfg",
						Pattern:     "defaults",
					},
				},
			},
		},
	}

	tmpDir := t.TempDir()
	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           tmpDir + "/maps",
		SSLCertsDir:       tmpDir + "/ssl",
		ConfigFile:        tmpDir + "/haproxy.cfg",
		CRTListDir:        tmpDir + "/crt-list",
		GeneralStorageDir: tmpDir + "/general",
	}

	engine, err := templating.New(
		templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": testutil.ValidHAProxyConfigTemplate,
		},
		nil, nil, nil,
	)
	require.NoError(t, err)

	storeManager := resourcestore.NewManager()
	storeManager.RegisterStore("ingresses", &mockStore{})

	component := New(bus, storeManager, cfg, engine, validationPaths, dataplane.Capabilities{}, logger)

	eventChan := bus.Subscribe(20)
	bus.Start()

	req := &events.WebhookValidationRequest{
		ID:        "test-req-events",
		GVK:       "networking.k8s.io/v1.Ingress",
		Namespace: "default",
		Name:      "test-ingress",
		Operation: "CREATE",
		Object:    map[string]interface{}{"metadata": map[string]interface{}{"name": "test-ingress"}},
	}

	component.handleValidationRequest(req)

	// Collect all events for verification
	receivedEvents := make([]interface{}, 0)
	timeout := time.After(testutil.LongTimeout)

	for {
		select {
		case event := <-eventChan:
			receivedEvents = append(receivedEvents, event)
			// Check if we have all expected events
			if _, ok := event.(*events.WebhookValidationResponse); ok {
				goto done
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
done:

	// Verify we received ValidationTestsStartedEvent
	foundStarted := false
	foundCompleted := false
	foundResponse := false

	for _, evt := range receivedEvents {
		switch evt.(type) {
		case *events.ValidationTestsStartedEvent:
			foundStarted = true
		case *events.ValidationTestsCompletedEvent:
			foundCompleted = true
		case *events.WebhookValidationResponse:
			foundResponse = true
		}
	}

	assert.True(t, foundStarted, "Expected ValidationTestsStartedEvent")
	assert.True(t, foundCompleted, "Expected ValidationTestsCompletedEvent")
	assert.True(t, foundResponse, "Expected WebhookValidationResponse")
}
