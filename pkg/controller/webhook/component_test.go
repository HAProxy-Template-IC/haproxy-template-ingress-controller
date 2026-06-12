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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

// generateTestCertPEM creates a valid self-signed cert + key pair for tests
// that construct webhook.Server (NewServer eagerly parses the PEM, so the
// previous placeholder []byte("test-cert") no longer works for tests that
// actually start a server).
func generateTestCertPEM(t *testing.T) (cert, key []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"unit-test"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	require.NoError(t, err)
	cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	return
}

func TestComponent_New(t *testing.T) {
	t.Run("applies default port", func(t *testing.T) {
		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(testutil.NewTestLogger(), config, nil, nil)

		assert.Equal(t, DefaultWebhookPort, component.config.Port)
	})

	t.Run("applies default path", func(t *testing.T) {
		config := &Config{
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(testutil.NewTestLogger(), config, nil, nil)

		assert.Equal(t, DefaultWebhookPath, component.config.Path)
	})

	t.Run("preserves custom port", func(t *testing.T) {
		config := &Config{
			Port:    8443,
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(testutil.NewTestLogger(), config, nil, nil)

		assert.Equal(t, 8443, component.config.Port)
	})

	t.Run("preserves custom path", func(t *testing.T) {
		config := &Config{
			Path:    "/custom-validate",
			CertPEM: []byte("test-cert"),
			KeyPEM:  []byte("test-key"),
		}

		component := New(testutil.NewTestLogger(), config, nil, nil)

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

func TestComponent_New_WithMetrics(t *testing.T) {
	metrics := &mockMetricsRecorder{}
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}

	component := New(testutil.NewTestLogger(), config, nil, metrics)

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

func TestComponent_Start_MissingCertificate(t *testing.T) {
	config := &Config{
		// CertPEM is empty
		KeyPEM: []byte("test-key"),
	}

	component := New(testutil.NewTestLogger(), config, nil, nil)

	ctx := t.Context()
	err := component.Start(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no webhook TLS certificate configured")
}

func TestComponent_Start_MissingKey(t *testing.T) {
	config := &Config{
		CertPEM: []byte("test-cert"),
		// KeyPEM is empty
	}

	component := New(testutil.NewTestLogger(), config, nil, nil)

	ctx := t.Context()
	err := component.Start(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls private key is empty")
}

func TestComponent_resolveKind_Success(t *testing.T) {
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

	component := New(testutil.NewTestLogger(), config, mapper, nil)

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
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}

	mapper := &mockRESTMapper{
		kindForResults: map[string]string{}, // Empty - no mappings
	}

	component := New(testutil.NewTestLogger(), config, mapper, nil)

	_, err := component.resolveKind("unknown", "v1", "unknowns")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving kind")
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 9443, DefaultWebhookPort)
	assert.Equal(t, "/validate", DefaultWebhookPath)
}

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

func TestComponent_registerValidators(t *testing.T) {
	certPEM, keyPEM := generateTestCertPEM(t)
	t.Run("registers validators for all rules", func(t *testing.T) {
		mapper := &mockRESTMapper{
			kindForResults: map[string]string{
				"networking.k8s.io/v1/ingresses": "Ingress",
				"/v1/configmaps":                 "ConfigMap",
			},
		}

		config := &Config{
			CertPEM: certPEM,
			KeyPEM:  keyPEM,
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

		component := New(testutil.NewTestLogger(), config, mapper, nil)

		// Create server so validators can be registered
		component.server, _ = webhook.NewServer(&webhook.ServerConfig{
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
		// Empty mapper that will return errors for all lookups
		mapper := &mockRESTMapper{
			kindForResults: map[string]string{},
		}

		config := &Config{
			CertPEM: certPEM,
			KeyPEM:  keyPEM,
			Rules: []webhook.WebhookRule{
				{
					APIGroups:   []string{"unknown.group"},
					APIVersions: []string{"v1"},
					Resources:   []string{"unknowns"},
				},
			},
		}

		component := New(testutil.NewTestLogger(), config, mapper, nil)

		// Create server
		component.server, _ = webhook.NewServer(&webhook.ServerConfig{
			Port:    9443,
			Path:    "/validate",
			CertPEM: config.CertPEM,
			KeyPEM:  config.KeyPEM,
		})

		// This should log error but not panic
		component.registerValidators()
	})

	t.Run("handles empty rules", func(t *testing.T) {
		mapper := &mockRESTMapper{
			kindForResults: map[string]string{},
		}

		config := &Config{
			CertPEM: certPEM,
			KeyPEM:  keyPEM,
			Rules:   []webhook.WebhookRule{}, // Empty rules
		}

		component := New(testutil.NewTestLogger(), config, mapper, nil)

		// Create server
		component.server, _ = webhook.NewServer(&webhook.ServerConfig{
			Port:    9443,
			Path:    "/validate",
			CertPEM: config.CertPEM,
			KeyPEM:  config.KeyPEM,
		})

		// Should handle empty rules gracefully
		component.registerValidators()
	})
}

// mockDryRunValidator is a mock implementation of DryRunValidator.
type mockDryRunValidator struct {
	allowed  bool
	reason   string
	warnings []string
}

func (m *mockDryRunValidator) ValidateDirect(_ context.Context, _, _, _ string, _ any, _ string) (allowed bool, reason string, warnings []string) {
	return m.allowed, m.reason, m.warnings
}

func TestComponent_createResourceValidator_ReturnsFunction(t *testing.T) {
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}
	component := New(testutil.NewTestLogger(), config, nil, nil)

	validator := component.createResourceValidator("v1.ConfigMap")

	require.NotNil(t, validator)
}

func TestComponent_createResourceValidator_NilDryRunValidator(t *testing.T) {
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
		// DryRunValidator is nil - fail-open behavior
	}
	component := New(testutil.NewTestLogger(), config, nil, nil)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Create a valid unstructured object
	obj := &unstructured.Unstructured{}
	obj.SetName("test-config")

	valCtx := &webhook.ValidationContext{
		Operation: "CREATE",
		Namespace: "default",
		Name:      "test",
		Object:    obj,
	}

	allowed, reason, _, err := validator(valCtx)

	// Should allow (fail-open) when no DryRunValidator configured
	assert.True(t, allowed)
	assert.Empty(t, reason)
	assert.NoError(t, err)
}

func TestComponent_createResourceValidator_BasicValidationFails(t *testing.T) {
	config := &Config{
		CertPEM: []byte("test-cert"),
		KeyPEM:  []byte("test-key"),
	}
	component := New(testutil.NewTestLogger(), config, nil, nil)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Create an invalid object (no name or generateName)
	obj := &unstructured.Unstructured{}
	// Don't set name or generateName

	valCtx := &webhook.ValidationContext{
		Operation: "CREATE",
		Namespace: "default",
		Name:      "test",
		Object:    obj,
	}

	allowed, reason, _, err := validator(valCtx)

	// Should deny due to basic validation failure
	assert.False(t, allowed)
	assert.Contains(t, reason, "metadata.name or metadata.generateName is required")
	assert.NoError(t, err)
}

func TestComponent_createResourceValidator_DryRunValidatorAllows(t *testing.T) {
	dryRunValidator := &mockDryRunValidator{
		allowed: true,
		reason:  "",
	}
	config := &Config{
		CertPEM:         []byte("test-cert"),
		KeyPEM:          []byte("test-key"),
		DryRunValidator: dryRunValidator,
	}
	component := New(testutil.NewTestLogger(), config, nil, nil)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Create a valid unstructured object
	obj := &unstructured.Unstructured{}
	obj.SetName("test-config")

	valCtx := &webhook.ValidationContext{
		Operation: "CREATE",
		Namespace: "default",
		Name:      "test",
		Object:    obj,
	}

	allowed, reason, _, err := validator(valCtx)

	assert.True(t, allowed)
	assert.Empty(t, reason)
	assert.NoError(t, err)
}

func TestComponent_createResourceValidator_DryRunValidatorDenies(t *testing.T) {
	dryRunValidator := &mockDryRunValidator{
		allowed: false,
		reason:  "invalid configuration: HAProxy check failed",
	}
	config := &Config{
		CertPEM:         []byte("test-cert"),
		KeyPEM:          []byte("test-key"),
		DryRunValidator: dryRunValidator,
	}
	component := New(testutil.NewTestLogger(), config, nil, nil)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Create a valid unstructured object
	obj := &unstructured.Unstructured{}
	obj.SetName("test-config")

	valCtx := &webhook.ValidationContext{
		Operation: "UPDATE",
		Namespace: "test-ns",
		Name:      "my-config",
		Object:    obj,
	}

	allowed, reason, _, err := validator(valCtx)

	assert.False(t, allowed)
	assert.Contains(t, reason, "invalid configuration")
	assert.NoError(t, err)
}

func TestComponent_createResourceValidator_MetricsOnSuccess(t *testing.T) {
	dryRunValidator := &mockDryRunValidator{
		allowed: true,
		reason:  "",
	}
	metrics := &mockMetricsRecorder{}
	config := &Config{
		CertPEM:         []byte("test-cert"),
		KeyPEM:          []byte("test-key"),
		DryRunValidator: dryRunValidator,
	}
	component := New(testutil.NewTestLogger(), config, nil, metrics)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Create a valid unstructured object
	obj := &unstructured.Unstructured{}
	obj.SetName("test-config")

	valCtx := &webhook.ValidationContext{
		Operation: "CREATE",
		Namespace: "default",
		Name:      "test",
		Object:    obj,
	}

	allowed, reason, _, err := validator(valCtx)

	assert.True(t, allowed)
	assert.Empty(t, reason)
	assert.NoError(t, err)
	assert.Greater(t, metrics.requestsRecorded, 0)
	assert.Greater(t, metrics.validationsRecorded, 0)
}

func TestComponent_createResourceValidator_MetricsOnDenial(t *testing.T) {
	dryRunValidator := &mockDryRunValidator{
		allowed: false,
		reason:  "invalid configuration",
	}
	metrics := &mockMetricsRecorder{}
	config := &Config{
		CertPEM:         []byte("test-cert"),
		KeyPEM:          []byte("test-key"),
		DryRunValidator: dryRunValidator,
	}
	component := New(testutil.NewTestLogger(), config, nil, metrics)
	validator := component.createResourceValidator("v1.ConfigMap")

	// Create a valid unstructured object
	obj := &unstructured.Unstructured{}
	obj.SetName("test-config")

	valCtx := &webhook.ValidationContext{
		Operation: "UPDATE",
		Namespace: "test-ns",
		Name:      "my-config",
		Object:    obj,
	}

	allowed, reason, _, err := validator(valCtx)

	assert.False(t, allowed)
	assert.Contains(t, reason, "invalid configuration")
	assert.NoError(t, err)
	assert.Greater(t, metrics.requestsRecorded, 0)
	assert.Greater(t, metrics.validationsRecorded, 0)
}

func TestComponent_validateBasicStructure(t *testing.T) {
	component := &Component{}

	t.Run("valid object with name", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetName("test-resource")

		err := component.validateBasicStructure(obj)
		assert.NoError(t, err)
	})

	t.Run("valid object with generateName", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetGenerateName("test-resource-")

		err := component.validateBasicStructure(obj)
		assert.NoError(t, err)
	})

	t.Run("invalid object - no name or generateName", func(t *testing.T) {
		obj := &unstructured.Unstructured{}

		err := component.validateBasicStructure(obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "metadata.name or metadata.generateName is required")
	})

	t.Run("invalid object type", func(t *testing.T) {
		err := component.validateBasicStructure("not an unstructured object")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid object type")
	})
}
