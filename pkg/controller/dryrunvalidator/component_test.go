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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// newTestRESTMapper builds a RESTMapper knowing the kinds the dryrunvalidator
// tests exercise, plus a CRD ("Mesh") whose real plural a naive English
// pluralizer gets wrong ("meshs" instead of "meshes"). The resolver must take
// the mapper's answer, never a guessed plural.
func newTestRESTMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	add := func(group, version, kind, plural, singular string) {
		m.AddSpecific(
			schema.GroupVersionKind{Group: group, Version: version, Kind: kind},
			schema.GroupVersionResource{Group: group, Version: version, Resource: plural},
			schema.GroupVersionResource{Group: group, Version: version, Resource: singular},
			meta.RESTScopeNamespace,
		)
	}
	add("networking.k8s.io", "v1", "Ingress", "ingresses", "ingress")
	add("", "v1", "Service", "services", "service")
	add("", "v1", "ConfigMap", "configmaps", "configmap")
	add("", "v1", "Secret", "secrets", "secret")
	add("", "v1", "Endpoints", "endpoints", "endpoints")
	add("discovery.k8s.io", "v1", "EndpointSlice", "endpointslices", "endpointslice")
	add("", "v1", "Pod", "pods", "pod")
	add("custom.example.io", "v1beta1", "MyResource", "myresources", "myresource")
	add("example.com", "v1", "Mesh", "meshes", "mesh") // naive pluralizer → "meshs"
	return m
}

// resettableFakeMapper simulates a deferred discovery mapper whose cache
// predates a late-registered CRD: RESTMapping returns NoMatch until Reset()
// refreshes discovery, after which it delegates to an inner mapper.
type resettableFakeMapper struct {
	meta.RESTMapper
	reset bool
}

func (m *resettableFakeMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if !m.reset {
		return nil, &meta.NoKindMatchError{GroupKind: gk}
	}
	return m.RESTMapper.RESTMapping(gk, versions...)
}

func (m *resettableFakeMapper) Reset() { m.reset = true }

// A late-registered CRD whose kind isn't in the mapper's initial discovery
// cache must resolve after the validator refreshes discovery via Reset(),
// rather than denying admission for the iteration's lifetime.
func TestMapGVKToResourceType_ResetsOnNoMatchThenRetries(t *testing.T) {
	rm := &resettableFakeMapper{RESTMapper: newTestRESTMapper()}
	c := &Component{logger: slog.Default(), restMapper: rm}

	resourceType, err := c.mapGVKToResourceType("networking.k8s.io/v1.Ingress")

	require.NoError(t, err)
	assert.True(t, rm.reset, "validator should Reset() the mapper on a NoMatch error")
	assert.Equal(t, "ingresses", resourceType)
}

func TestMapGVKToResourceType(t *testing.T) {
	// Create a minimal component for testing
	c := &Component{
		logger:     slog.Default(),
		restMapper: newTestRESTMapper(),
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
			// A naive pluralizer produces "meshs"; the mapper knows "meshes".
			name:        "irregular plural comes from the mapper",
			gvk:         "example.com/v1.Mesh",
			expected:    "meshes",
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

func TestCreateOverlay(t *testing.T) {
	c := &Component{
		logger: slog.Default(),
	}

	tests := []struct {
		name                string
		operation           string
		object              any
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
		RESTMapper:        newTestRESTMapper(),
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
}

// TestValidateDirect_UpdateSuccess tests the full flow for an UPDATE operation.
func TestValidateDirect_UpdateSuccess(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		RESTMapper:        newTestRESTMapper(),
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

	bus.Start()

	allowed, reason, _ := component.ValidateDirect(
		context.Background(),
		"networking.k8s.io/v1.Ingress",
		"staging",
		"updated-ingress",
		createTestIngress("updated-ingress", "staging"),
		"UPDATE",
	)

	assert.True(t, allowed)
	assert.Empty(t, reason)
}

// TestValidateDirect_DeleteSuccess tests the full flow for a DELETE operation.
func TestValidateDirect_DeleteSuccess(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

	proposalValidator := createMockProposalValidator(bus, logger)

	component := New(&ComponentConfig{
		RESTMapper:        newTestRESTMapper(),
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

	bus.Start()

	allowed, reason, _ := component.ValidateDirect(
		context.Background(),
		"networking.k8s.io/v1.Ingress",
		"default",
		"test-ingress",
		nil,
		"DELETE",
	)

	assert.True(t, allowed)
	assert.Empty(t, reason)
}

// TestValidateDirect_OverlayReferencesInvalidStore tests that overlays
// referencing non-existent stores produce a denial.
func TestValidateDirect_OverlayReferencesInvalidStore(t *testing.T) {
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
		RESTMapper:        newTestRESTMapper(),
		ProposalValidator: noStoreProposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason, _ := component.ValidateDirect(
		context.Background(),
		"networking.k8s.io/v1.Ingress",
		"default",
		"test-ingress",
		createTestIngress("test-ingress", "default"),
		"CREATE",
	)

	assert.False(t, allowed)
	assert.Contains(t, reason, "non-existent store")
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
		RESTMapper:        newTestRESTMapper(),
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason, _ := component.ValidateDirect(
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
		RESTMapper:        newTestRESTMapper(),
		ProposalValidator: proposalValidator,
		Config:            cfg,
		Engine:            engine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason, _ := component.ValidateDirect(
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

// TestValidateDirect_AlwaysFailingTemplate_AdmitsBecauseBaselineFails verifies
// the baseline-check semantics added to proposalvalidator.ValidateSync: when
// the proposed render fails AND the baseline render (live stores without the
// overlay) ALSO fails for the same reason, the new resource isn't the cause
// of the failure and admission is allowed.
//
// The fixture uses a template of `{{ fail("invalid config") }}` — it
// produces a render error regardless of overlay content, so both the
// proposed and baseline runs fail identically. Pre-existing broken state in
// production manifests this way (e.g., an Ingress whose Secret has been
// deleted causes every render to fail until the Ingress or Secret is fixed).
// Under the previous "deny on any failure" policy, every webhook admission
// would be denied in that situation, blocking unrelated work. The
// baseline-check policy admits unrelated proposals so they aren't gated on
// an operator fixing the pre-existing failure first.
func TestValidateDirect_AlwaysFailingTemplate_AdmitsBecauseBaselineFails(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{},
		ValidationTests:  map[string]config.ValidationTest{},
	}

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
		"ingresses": &storetest.MockStore{},
	})

	failingProposalValidator := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStoreProvider,
		Logger:            logger,
	})

	component := New(&ComponentConfig{
		RESTMapper:        newTestRESTMapper(),
		ProposalValidator: failingProposalValidator,
		Config:            cfg,
		Engine:            failingEngine,
		ValidationPaths:   &dataplane.ValidationPaths{},
		Capabilities:      dataplane.Capabilities{},
		Logger:            logger,
	})

	bus.Start()

	allowed, reason, _ := component.ValidateDirect(
		context.Background(),
		"networking.k8s.io/v1.Ingress",
		"default",
		"test-ingress",
		createTestIngress("test-ingress", "default"),
		"CREATE",
	)

	assert.True(t, allowed,
		"baseline-also-fails MUST admit the proposal — the alternative is "+
			"denying every unrelated admission whenever the cluster has any "+
			"broken pre-existing state, which is the production reliability "+
			"bug the baseline check fixes")
	assert.Empty(t, reason,
		"on admit no denial reason should be surfaced — the proposed-render "+
			"failure is logged at warn but not propagated as a denial message")
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
		"ingresses": &storetest.MockStore{},
		"services":  &storetest.MockStore{},
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
		Object: map[string]any{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "Ingress",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"rules": []any{},
			},
		},
	}
}
