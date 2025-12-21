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

package webhook

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/webhook"
)

// testLogger creates a slog logger for tests that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestComponent_New(t *testing.T) {
	t.Run("applies default port", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)
		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(eventBus, testLogger(), config, nil, nil)

		assert.Equal(t, DefaultWebhookPort, component.config.Port)
	})

	t.Run("applies default path", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)
		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(eventBus, testLogger(), config, nil, nil)

		assert.Equal(t, DefaultWebhookPath, component.config.Path)
	})

	t.Run("preserves custom port", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)
		config := &Config{
			Port:    8443,
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(eventBus, testLogger(), config, nil, nil)

		assert.Equal(t, 8443, component.config.Port)
	})

	t.Run("preserves custom path", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)
		config := &Config{
			Path:    "/custom-validate",
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(eventBus, testLogger(), config, nil, nil)

		assert.Equal(t, "/custom-validate", component.config.Path)
	})
}

func TestComponent_buildGVK(t *testing.T) {
	component := &Component{}

	tests := []struct {
		name     string
		apiGroup string
		version  string
		kind     string
		expected string
	}{
		{
			name:     "core API group",
			apiGroup: "",
			version:  "v1",
			kind:     "ConfigMap",
			expected: "v1.ConfigMap",
		},
		{
			name:     "networking API group",
			apiGroup: "networking.k8s.io",
			version:  "v1",
			kind:     "Ingress",
			expected: "networking.k8s.io/v1.Ingress",
		},
		{
			name:     "apps API group",
			apiGroup: "apps",
			version:  "v1",
			kind:     "Deployment",
			expected: "apps/v1.Deployment",
		},
		{
			name:     "gateway API group",
			apiGroup: "gateway.networking.k8s.io",
			version:  "v1",
			kind:     "HTTPRoute",
			expected: "gateway.networking.k8s.io/v1.HTTPRoute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := component.buildGVK(tt.apiGroup, tt.version, tt.kind)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComponent_aggregateResponses(t *testing.T) {
	component := &Component{}

	t.Run("all validators allow", func(t *testing.T) {
		responses := []busevents.Response{
			events.NewWebhookValidationResponse("req1", "basic", true, ""),
			events.NewWebhookValidationResponse("req1", "dryrun", true, ""),
		}

		allowed, reason := component.aggregateResponses(responses)

		assert.True(t, allowed)
		assert.Empty(t, reason)
	})

	t.Run("one validator denies", func(t *testing.T) {
		responses := []busevents.Response{
			events.NewWebhookValidationResponse("req1", "basic", true, ""),
			events.NewWebhookValidationResponse("req1", "dryrun", false, "invalid config"),
		}

		allowed, reason := component.aggregateResponses(responses)

		assert.False(t, allowed)
		assert.Contains(t, reason, "dryrun")
		assert.Contains(t, reason, "invalid config")
	})

	t.Run("all validators deny", func(t *testing.T) {
		responses := []busevents.Response{
			events.NewWebhookValidationResponse("req1", "basic", false, "missing name"),
			events.NewWebhookValidationResponse("req1", "dryrun", false, "invalid config"),
		}

		allowed, reason := component.aggregateResponses(responses)

		assert.False(t, allowed)
		assert.Contains(t, reason, "basic")
		assert.Contains(t, reason, "dryrun")
		assert.Contains(t, reason, "missing name")
		assert.Contains(t, reason, "invalid config")
	})

	t.Run("empty responses allows", func(t *testing.T) {
		responses := []busevents.Response{}

		allowed, reason := component.aggregateResponses(responses)

		assert.True(t, allowed)
		assert.Empty(t, reason)
	})

	t.Run("ignores non-validation responses", func(t *testing.T) {
		// Create a non-WebhookValidationResponse event
		responses := []busevents.Response{
			events.NewWebhookValidationResponse("req1", "basic", true, ""),
			&otherResponse{}, // This should be ignored
		}

		allowed, reason := component.aggregateResponses(responses)

		assert.True(t, allowed)
		assert.Empty(t, reason)
	})
}

// otherResponse implements busevents.Response for testing.
type otherResponse struct{}

func (o *otherResponse) EventType() string    { return "test.other" }
func (o *otherResponse) Timestamp() time.Time { return time.Now() }
func (o *otherResponse) RequestID() string    { return "other" }
func (o *otherResponse) Responder() string    { return "test" }

func TestComponent_New_WithMetrics(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	metrics := &mockMetricsRecorder{}
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}

	component := New(eventBus, testLogger(), config, nil, metrics)

	require.NotNil(t, component.metrics)
}

// mockMetricsRecorder is a mock implementation of MetricsRecorder.
type mockMetricsRecorder struct {
	requestsRecorded    int
	validationsRecorded int
}

func (m *mockMetricsRecorder) RecordWebhookRequest(gvk, result string, durationSeconds float64) {
	m.requestsRecorded++
}

func (m *mockMetricsRecorder) RecordWebhookValidation(gvk, result string) {
	m.validationsRecorded++
}

// =============================================================================
// Start() Tests - Error Cases
// =============================================================================

func TestComponent_Start_MissingCertificate(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	config := &Config{
		// CertPEM is empty
		KeyPEM: []byte("test-key"),
	}

	component := New(eventBus, testLogger(), config, nil, nil)

	ctx := t.Context()
	err := component.Start(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls certificate is empty")
}

func TestComponent_Start_MissingKey(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	config := &Config{
		CertPEM: []byte("test-cert"),
		// KeyPEM is empty
	}

	component := New(eventBus, testLogger(), config, nil, nil)

	ctx := t.Context()
	err := component.Start(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls private key is empty")
}

// =============================================================================
// RegisterValidator() Tests
// =============================================================================

func TestComponent_RegisterValidator_BeforeServerCreated(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}

	component := New(eventBus, testLogger(), config, nil, nil)

	// Server is nil at this point, should log warning but not panic
	component.RegisterValidator("v1.ConfigMap", func(_ *webhook.ValidationContext) (bool, string, error) {
		return true, "", nil
	})

	// Verify server is still nil
	assert.Nil(t, component.server)
}

// =============================================================================
// resolveKind() Tests
// =============================================================================

func TestComponent_resolveKind_Success(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}

	mapper := &mockRESTMapper{
		kindForResults: map[string]string{
			"networking.k8s.io/v1/ingresses": "Ingress",
			"/v1/configmaps":                 "ConfigMap",
		},
	}

	component := New(eventBus, testLogger(), config, mapper, nil)

	t.Run("ingress resource", func(t *testing.T) {
		kind, err := component.resolveKind("networking.k8s.io", "v1", "ingresses")
		require.NoError(t, err)
		assert.Equal(t, "Ingress", kind)
	})

	t.Run("core configmap resource", func(t *testing.T) {
		kind, err := component.resolveKind("", "v1", "configmaps")
		require.NoError(t, err)
		assert.Equal(t, "ConfigMap", kind)
	})
}

func TestComponent_resolveKind_Error(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}

	mapper := &mockRESTMapper{
		kindForResults: map[string]string{}, // Empty - no mappings
	}

	component := New(eventBus, testLogger(), config, mapper, nil)

	_, err := component.resolveKind("unknown", "v1", "unknowns")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve kind")
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestConstants(t *testing.T) {
	assert.Equal(t, 9443, DefaultWebhookPort)
	assert.Equal(t, "/validate", DefaultWebhookPath)
	assert.Equal(t, 50, EventBufferSize)
}

// =============================================================================
// Mock RESTMapper
// =============================================================================

// mockRESTMapper is a minimal mock for testing resolveKind.
type mockRESTMapper struct {
	kindForResults map[string]string // key: "group/version/resource", value: kind
}

func (m *mockRESTMapper) KindFor(resource schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	key := resource.Group + "/" + resource.Version + "/" + resource.Resource
	kind, ok := m.kindForResults[key]
	if !ok {
		return schema.GroupVersionKind{}, fmt.Errorf("no kind mapping for %v", resource)
	}
	return schema.GroupVersionKind{
		Group:   resource.Group,
		Version: resource.Version,
		Kind:    kind,
	}, nil
}

// Implement remaining RESTMapper interface methods as no-ops.
func (m *mockRESTMapper) KindsFor(schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockRESTMapper) ResourceFor(schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, fmt.Errorf("not implemented")
}
func (m *mockRESTMapper) ResourcesFor(schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockRESTMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockRESTMapper) RESTMappings(schema.GroupKind, ...string) ([]*meta.RESTMapping, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockRESTMapper) ResourceSingularizer(string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

// =============================================================================
// registerValidators() Tests
// =============================================================================

func TestComponent_registerValidators(t *testing.T) {
	t.Run("registers validators for all rules", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)

		mapper := &mockRESTMapper{
			kindForResults: map[string]string{
				"networking.k8s.io/v1/ingresses": "Ingress",
				"/v1/configmaps":                 "ConfigMap",
			},
		}

		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
			Rules: []webhook.WebhookRule{
				{
					APIGroups:   []string{"networking.k8s.io"},
					APIVersions: []string{"v1"},
					Resources:   []string{"ingresses"},
				},
				{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"configmaps"},
				},
			},
		}

		component := New(eventBus, testLogger(), config, mapper, nil)

		// Create server so validators can be registered
		component.server = webhook.NewServer(&webhook.ServerConfig{
			Port:    9443,
			Path:    "/validate",
			CertPEM: config.CertPEM,
			KeyPEM:  config.KeyPEM,
		})

		// This should not panic and should register validators
		component.registerValidators()

		// We can't directly verify registered validators without more mocking,
		// but at least we verify it doesn't error
	})

	t.Run("skips rules with RESTMapper errors", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)

		// Empty mapper that will return errors for all lookups
		mapper := &mockRESTMapper{
			kindForResults: map[string]string{},
		}

		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
			Rules: []webhook.WebhookRule{
				{
					APIGroups:   []string{"unknown.group"},
					APIVersions: []string{"v1"},
					Resources:   []string{"unknowns"},
				},
			},
		}

		component := New(eventBus, testLogger(), config, mapper, nil)

		// Create server
		component.server = webhook.NewServer(&webhook.ServerConfig{
			Port:    9443,
			Path:    "/validate",
			CertPEM: config.CertPEM,
			KeyPEM:  config.KeyPEM,
		})

		// This should log error but not panic
		component.registerValidators()
	})

	t.Run("handles empty rules", func(t *testing.T) {
		eventBus := busevents.NewEventBus(10)

		mapper := &mockRESTMapper{
			kindForResults: map[string]string{},
		}

		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
			Rules:   []webhook.WebhookRule{}, // Empty rules
		}

		component := New(eventBus, testLogger(), config, mapper, nil)

		// Create server
		component.server = webhook.NewServer(&webhook.ServerConfig{
			Port:    9443,
			Path:    "/validate",
			CertPEM: config.CertPEM,
			KeyPEM:  config.KeyPEM,
		})

		// Should handle empty rules gracefully
		component.registerValidators()
	})
}

// =============================================================================
// createResourceValidator() Tests
// =============================================================================

// mockValidationResponse defines the response a mock responder should send.
type mockValidationResponse struct {
	responderID string
	allowed     bool
	message     string
}

// startMockResponder starts a goroutine that responds to validation requests.
// Returns a done channel that closes when the responder finishes.
func startMockResponder(eventBus *busevents.EventBus, eventChan <-chan busevents.Event, responses []mockValidationResponse) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range eventChan {
			req, ok := event.(*events.WebhookValidationRequest)
			if !ok {
				continue
			}
			for _, resp := range responses {
				eventBus.Publish(events.NewWebhookValidationResponse(
					req.RequestID(),
					resp.responderID,
					resp.allowed,
					resp.message,
				))
			}
			return // Exit after handling the request
		}
	}()
	return done
}

func TestComponent_createResourceValidator_ReturnsFunction(t *testing.T) {
	eventBus := busevents.NewEventBus(100)
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}
	component := New(eventBus, testLogger(), config, nil, nil)

	validator := component.createResourceValidator("v1.ConfigMap")

	require.NotNil(t, validator)
}

func TestComponent_createResourceValidator_Timeout(t *testing.T) {
	eventBus := busevents.NewEventBus(100)
	eventBus.Start()

	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}
	component := New(eventBus, testLogger(), config, nil, nil)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Call validator - no responders so it will timeout
	valCtx := &webhook.ValidationContext{
		Operation: "CREATE",
		Namespace: "default",
		Name:      "test",
		Object:    nil,
	}

	allowed, reason, err := validator(valCtx)

	// Should fail due to timeout (no responders)
	assert.False(t, allowed)
	assert.Contains(t, reason, "timeout")
	assert.NoError(t, err)
}

func TestComponent_createResourceValidator_MetricsOnSuccess(t *testing.T) {
	eventBus := busevents.NewEventBus(100)
	metrics := &mockMetricsRecorder{}
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}
	component := New(eventBus, testLogger(), config, nil, metrics)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Subscribe mock responder BEFORE starting EventBus (per project pattern)
	eventChan := eventBus.Subscribe(10)
	eventBus.Start()

	done := startMockResponder(eventBus, eventChan, []mockValidationResponse{
		{responderID: "basic", allowed: true, message: ""},
		{responderID: "dryrun", allowed: true, message: ""},
	})

	valCtx := &webhook.ValidationContext{
		Operation: "CREATE",
		Namespace: "default",
		Name:      "test",
		Object:    nil,
	}

	allowed, reason, err := validator(valCtx)
	<-done

	assert.True(t, allowed)
	assert.Empty(t, reason)
	assert.NoError(t, err)
	assert.Greater(t, metrics.requestsRecorded, 0)
	assert.Greater(t, metrics.validationsRecorded, 0)
}

func TestComponent_createResourceValidator_MetricsOnDenial(t *testing.T) {
	eventBus := busevents.NewEventBus(100)
	metrics := &mockMetricsRecorder{}
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}
	component := New(eventBus, testLogger(), config, nil, metrics)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Subscribe mock responder BEFORE starting EventBus (per project pattern)
	eventChan := eventBus.Subscribe(10)
	eventBus.Start()

	done := startMockResponder(eventBus, eventChan, []mockValidationResponse{
		{responderID: "basic", allowed: true, message: ""},
		{responderID: "dryrun", allowed: false, message: "invalid configuration"},
	})

	valCtx := &webhook.ValidationContext{
		Operation: "UPDATE",
		Namespace: "test-ns",
		Name:      "my-config",
		Object:    nil,
	}

	allowed, reason, err := validator(valCtx)
	<-done

	assert.False(t, allowed)
	assert.Contains(t, reason, "dryrun")
	assert.Contains(t, reason, "invalid configuration")
	assert.NoError(t, err)
	assert.Greater(t, metrics.requestsRecorded, 0)
	assert.Greater(t, metrics.validationsRecorded, 0)
}
