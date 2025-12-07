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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// generateTestCertificates generates a self-signed certificate for testing.
func generateTestCertificates() (certPEM, keyPEM []byte, err error) {
	// Generate RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:              []string{"localhost"},
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Encode private key to PEM
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return certPEM, keyPEM, nil
}

func TestNewServer_Defaults(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	require.NotNil(t, server)
	assert.Equal(t, 9443, server.config.Port)
	assert.Equal(t, "0.0.0.0", server.config.BindAddress)
	assert.Equal(t, "/validate", server.config.Path)
	assert.Equal(t, 10*time.Second, server.config.ReadTimeout)
	assert.Equal(t, 10*time.Second, server.config.WriteTimeout)
	assert.NotNil(t, server.validators)
}

func TestNewServer_CustomConfig(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		Port:         8443,
		BindAddress:  "127.0.0.1",
		Path:         "/webhook",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 15 * time.Second,
		CertPEM:      certPEM,
		KeyPEM:       keyPEM,
	})

	require.NotNil(t, server)
	assert.Equal(t, 8443, server.config.Port)
	assert.Equal(t, "127.0.0.1", server.config.BindAddress)
	assert.Equal(t, "/webhook", server.config.Path)
	assert.Equal(t, 5*time.Second, server.config.ReadTimeout)
	assert.Equal(t, 15*time.Second, server.config.WriteTimeout)
}

func TestServer_RegisterValidator(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator
	server.RegisterValidator("networking.k8s.io/v1.Ingress", func(_ *ValidationContext) (bool, string, error) {
		return true, "", nil
	})

	// Verify validator is registered
	server.mu.RLock()
	_, exists := server.validators["networking.k8s.io/v1.Ingress"]
	server.mu.RUnlock()

	assert.True(t, exists)

	// Replace validator
	server.RegisterValidator("networking.k8s.io/v1.Ingress", func(_ *ValidationContext) (bool, string, error) {
		return false, "replaced", nil
	})

	// Verify it was replaced
	server.mu.RLock()
	newValidator := server.validators["networking.k8s.io/v1.Ingress"]
	server.mu.RUnlock()

	allowed, reason, err := newValidator(nil)
	assert.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "replaced", reason)
}

func TestServer_UnregisterValidator(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator
	server.RegisterValidator("v1.Service", func(_ *ValidationContext) (bool, string, error) {
		return true, "", nil
	})

	// Verify registered
	server.mu.RLock()
	_, exists := server.validators["v1.Service"]
	server.mu.RUnlock()
	assert.True(t, exists)

	// Unregister
	server.UnregisterValidator("v1.Service")

	// Verify unregistered
	server.mu.RLock()
	_, exists = server.validators["v1.Service"]
	server.mu.RUnlock()
	assert.False(t, exists)

	// Unregistering non-existent validator should not panic
	server.UnregisterValidator("non-existent")
}

func TestServer_GetGVK(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	tests := []struct {
		name     string
		request  *admissionv1.AdmissionRequest
		expected string
	}{
		{
			name: "core type",
			request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Pod",
				},
			},
			expected: "v1.Pod",
		},
		{
			name: "namespaced group",
			request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "networking.k8s.io",
					Version: "v1",
					Kind:    "Ingress",
				},
			},
			expected: "networking.k8s.io/v1.Ingress",
		},
		{
			name: "apps group",
			request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
			},
			expected: "apps/v1.Deployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.getGVK(tt.request)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestServer_HandleHealthz(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	server.handleHealthz(w, req)

	// Verify response
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := w.Body.String()
	assert.Equal(t, "ok", body)
}

func TestServer_HandleValidation_MethodNotAllowed(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Test GET request (should be rejected)
	req := httptest.NewRequest(http.MethodGet, "/validate", http.NoBody)
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestServer_HandleValidation_InvalidBody(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Test with invalid JSON body
	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestServer_HandleValidation_NoValidatorAllowsByDefault(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Create a valid AdmissionReview request for a type with no validator
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reviewBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.True(t, responseReview.Response.Allowed)
}

func TestServer_HandleValidation_ValidatorAllows(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator that allows
	server.RegisterValidator("v1.ConfigMap", func(ctx *ValidationContext) (bool, string, error) {
		assert.Equal(t, "test", ctx.Name)
		assert.Equal(t, "default", ctx.Namespace)
		assert.Equal(t, "CREATE", ctx.Operation)
		return true, "", nil
	})

	// Create a valid AdmissionReview request
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reviewBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.True(t, responseReview.Response.Allowed)
	assert.Equal(t, "test-uid", string(responseReview.Response.UID))
}

func TestServer_HandleValidation_ValidatorDenies(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator that denies
	server.RegisterValidator("v1.ConfigMap", func(_ *ValidationContext) (bool, string, error) {
		return false, "validation failed: missing required field", nil
	})

	// Create a valid AdmissionReview request
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reviewBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.False(t, responseReview.Response.Allowed)
	assert.Contains(t, responseReview.Response.Result.Message, "validation failed: missing required field")
	assert.Equal(t, int32(http.StatusForbidden), responseReview.Response.Result.Code)
}

func TestServer_HandleValidation_ValidatorError(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator that returns an error
	server.RegisterValidator("v1.ConfigMap", func(_ *ValidationContext) (bool, string, error) {
		return false, "", fmt.Errorf("internal validation error")
	})

	// Create a valid AdmissionReview request
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reviewBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.False(t, responseReview.Response.Allowed)
	assert.Contains(t, responseReview.Response.Result.Message, "internal validation error")
	assert.Equal(t, int32(http.StatusInternalServerError), responseReview.Response.Result.Code)
}

func TestServer_HandleValidation_InvalidObject(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register a validator
	server.RegisterValidator("v1.ConfigMap", func(_ *ValidationContext) (bool, string, error) {
		return true, "", nil
	})

	// Create a request with invalid object JSON directly
	// We must build the JSON manually because json.Marshal can't handle invalid Raw
	reviewJSON := `{
		"apiVersion": "admission.k8s.io/v1",
		"kind": "AdmissionReview",
		"request": {
			"uid": "test-uid",
			"kind": {
				"group": "",
				"version": "v1",
				"kind": "ConfigMap"
			},
			"operation": "CREATE",
			"object": "not-valid-object-structure"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte(reviewJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.False(t, responseReview.Response.Allowed)
	assert.Contains(t, responseReview.Response.Result.Message, "failed to parse object")
}

func TestServer_HandleValidation_UpdateWithOldObject(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator that checks old object
	server.RegisterValidator("v1.ConfigMap", func(ctx *ValidationContext) (bool, string, error) {
		assert.Equal(t, "UPDATE", ctx.Operation)
		assert.NotNil(t, ctx.Object)
		assert.NotNil(t, ctx.OldObject)

		// Verify old object metadata
		oldName := ctx.OldObject.GetName()
		assert.Equal(t, "test", oldName)

		return true, "", nil
	})

	// Create an UPDATE request with old object
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Update,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"},"data":{"new":"value"}}`),
			},
			OldObject: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"},"data":{"old":"value"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reviewBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.True(t, responseReview.Response.Allowed)
}

func TestServer_HandleValidation_InvalidOldObject(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register a validator
	server.RegisterValidator("v1.ConfigMap", func(_ *ValidationContext) (bool, string, error) {
		return true, "", nil
	})

	// Construct JSON manually to test invalid OldObject handling
	// (json.Marshal fails on RawExtension with unrecognized content)
	reviewJSON := `{
		"apiVersion": "admission.k8s.io/v1",
		"kind": "AdmissionReview",
		"request": {
			"uid": "test-uid",
			"kind": {"group": "", "version": "v1", "kind": "ConfigMap"},
			"operation": "UPDATE",
			"object": {"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}},
			"oldObject": "not-valid-object-structure"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte(reviewJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleValidation(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(resp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.False(t, responseReview.Response.Allowed)
	assert.Contains(t, responseReview.Response.Result.Message, "failed to parse old object")
}

func TestServer_Start_InvalidCertificate(t *testing.T) {
	server := NewServer(&ServerConfig{
		CertPEM: []byte("invalid cert"),
		KeyPEM:  []byte("invalid key"),
		Port:    19443,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := server.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load TLS certificate")
}

func TestServer_Start_ContextCancellation(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Port:    29443, // Use unique port to avoid conflicts
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for server to stop
	select {
	case err := <-done:
		// Should return nil on graceful shutdown
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shut down within timeout")
	}
}

func TestServer_Start_Integration(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
		Port:        39443, // Use unique port to avoid conflicts
		BindAddress: "127.0.0.1",
	})

	// Register a test validator
	server.RegisterValidator("v1.ConfigMap", func(_ *ValidationContext) (bool, string, error) {
		return true, "", nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(200 * time.Millisecond)

	// Create TLS client that trusts our self-signed cert
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(certPEM)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
		},
	}

	// Test healthz endpoint
	healthResp, err := client.Get("https://127.0.0.1:39443/healthz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, healthResp.StatusCode)
	healthResp.Body.Close()

	// Test validation endpoint
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	validateResp, err := client.Post("https://127.0.0.1:39443/validate", "application/json", bytes.NewReader(reviewBytes))
	require.NoError(t, err)
	defer validateResp.Body.Close()

	assert.Equal(t, http.StatusOK, validateResp.StatusCode)

	var responseReview admissionv1.AdmissionReview
	err = json.NewDecoder(validateResp.Body).Decode(&responseReview)
	require.NoError(t, err)

	assert.True(t, responseReview.Response.Allowed)

	// Shut down server
	cancel()

	select {
	case <-done:
		// Server shut down successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shut down within timeout")
	}
}

func TestServer_ExtractMetadata(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	tests := []struct {
		name              string
		objectJSON        string
		expectedNamespace string
		expectedName      string
	}{
		{
			name:              "namespaced resource",
			objectJSON:        `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"my-config","namespace":"production"}}`,
			expectedNamespace: "production",
			expectedName:      "my-config",
		},
		{
			name:              "cluster-scoped resource",
			objectJSON:        `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"my-namespace"}}`,
			expectedNamespace: "",
			expectedName:      "my-namespace",
		},
		{
			name:              "empty metadata",
			objectJSON:        `{"apiVersion":"v1","kind":"ConfigMap"}`,
			expectedNamespace: "",
			expectedName:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse object as unstructured (same as server does)
			var obj map[string]interface{}
			err := json.Unmarshal([]byte(tt.objectJSON), &obj)
			require.NoError(t, err)

			// Create unstructured object
			unstructuredObj := &unstructured.Unstructured{Object: obj}

			namespace, name := server.extractMetadata(unstructuredObj)
			assert.Equal(t, tt.expectedNamespace, namespace)
			assert.Equal(t, tt.expectedName, name)
		})
	}
}

func TestServer_ExtractMetadata_NilObject(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	namespace, name := server.extractMetadata(nil)
	assert.Empty(t, namespace)
	assert.Empty(t, name)
}

func TestServer_ConcurrentValidation(t *testing.T) {
	certPEM, keyPEM, err := generateTestCertificates()
	require.NoError(t, err)

	server := NewServer(&ServerConfig{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})

	// Register validator
	validationCount := 0
	var mu sync.Mutex
	server.RegisterValidator("v1.ConfigMap", func(_ *ValidationContext) (bool, string, error) {
		mu.Lock()
		validationCount++
		mu.Unlock()
		return true, "", nil
	})

	// Create test request body once
	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ConfigMap",
			},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test","namespace":"default"}}`),
			},
		},
	}

	reviewBytes, err := json.Marshal(review)
	require.NoError(t, err)

	// Run concurrent validations
	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()

			req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reviewBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleValidation(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}()
	}

	wg.Wait()

	// Verify all validations completed
	mu.Lock()
	assert.Equal(t, numRequests, validationCount)
	mu.Unlock()
}
