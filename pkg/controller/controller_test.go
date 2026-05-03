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

package controller

import (
	"encoding/base64"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseSecret(t *testing.T) {
	tests := []struct {
		name      string
		resource  *unstructured.Unstructured
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid secret with credentials",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name":      "test-secret",
						"namespace": "default",
					},
					"data": map[string]any{
						"dataplane_username": base64.StdEncoding.EncodeToString([]byte("admin")),
						"dataplane_password": base64.StdEncoding.EncodeToString([]byte("secret123")),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "secret without data field",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "test-secret",
					},
				},
			},
			wantErr:   true,
			errSubstr: "has no data field",
		},
		{
			name: "secret with invalid base64",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "test-secret",
					},
					"data": map[string]any{
						"dataplane_username": "not-valid-base64!!!",
					},
				},
			},
			wantErr:   true,
			errSubstr: "decoding base64",
		},
		{
			name: "secret missing required credentials",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "test-secret",
					},
					"data": map[string]any{
						"some_other_key": base64.StdEncoding.EncodeToString([]byte("value")),
					},
				},
			},
			wantErr:   true,
			errSubstr: "missing required secret key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := parseSecret(tt.resource)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, creds)
		})
	}
}

func TestParseWebhookCertSecret(t *testing.T) {
	validCert := []byte("-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAKHBfp...")
	validKey := []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...")

	tests := []struct {
		name      string
		resource  *unstructured.Unstructured
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid TLS secret",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name":            "webhook-certs",
						"namespace":       "default",
						"resourceVersion": "12345",
					},
					"type": "kubernetes.io/tls",
					"data": map[string]any{
						"tls.crt": base64.StdEncoding.EncodeToString(validCert),
						"tls.key": base64.StdEncoding.EncodeToString(validKey),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "secret without data field",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
				},
			},
			wantErr:   true,
			errSubstr: "has no data field",
		},
		{
			name: "secret missing tls.crt",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
					"data": map[string]any{
						"tls.key": base64.StdEncoding.EncodeToString(validKey),
					},
				},
			},
			wantErr:   true,
			errSubstr: "missing 'tls.crt'",
		},
		{
			name: "secret missing tls.key",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
					"data": map[string]any{
						"tls.crt": base64.StdEncoding.EncodeToString(validCert),
					},
				},
			},
			wantErr:   true,
			errSubstr: "missing 'tls.key'",
		},
		{
			name: "secret with invalid base64 cert",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
					"data": map[string]any{
						"tls.crt": "not-valid-base64!!!",
						"tls.key": base64.StdEncoding.EncodeToString(validKey),
					},
				},
			},
			wantErr:   true,
			errSubstr: "decoding base64 tls.crt",
		},
		{
			name: "secret with invalid base64 key",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
					"data": map[string]any{
						"tls.crt": base64.StdEncoding.EncodeToString(validCert),
						"tls.key": "not-valid-base64!!!",
					},
				},
			},
			wantErr:   true,
			errSubstr: "decoding base64 tls.key",
		},
		// Note: Tests for non-string tls.crt/tls.key values are not included because
		// unstructured.NestedMap panics on non-JSON types (like bare integers).
		// The production code handles these cases, but we cannot test them via
		// unstructured.Unstructured objects.
		{
			name: "secret with empty tls.crt",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
					"data": map[string]any{
						"tls.crt": base64.StdEncoding.EncodeToString([]byte("")),
						"tls.key": base64.StdEncoding.EncodeToString(validKey),
					},
				},
			},
			wantErr:   true,
			errSubstr: "tls.crt is empty",
		},
		{
			name: "secret with empty tls.key",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "webhook-certs",
					},
					"data": map[string]any{
						"tls.crt": base64.StdEncoding.EncodeToString(validCert),
						"tls.key": base64.StdEncoding.EncodeToString([]byte("")),
					},
				},
			},
			wantErr:   true,
			errSubstr: "tls.key is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certs, err := parseWebhookCertSecret(tt.resource)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, certs)
			assert.Equal(t, validCert, certs.CertPEM)
			assert.Equal(t, validKey, certs.KeyPEM)
			assert.Equal(t, "12345", certs.Version)
		})
	}
}

func TestWebhookCertificates(t *testing.T) {
	// Test the WebhookCertificates struct
	certs := &WebhookCertificates{
		CertPEM: []byte("cert-data"),
		KeyPEM:  []byte("key-data"),
		Version: "v1",
	}

	assert.Equal(t, []byte("cert-data"), certs.CertPEM)
	assert.Equal(t, []byte("key-data"), certs.KeyPEM)
	assert.Equal(t, "v1", certs.Version)
}

func TestStopLeaderOnlyComponents_NilComponents(t *testing.T) {
	logger := slog.Default()

	// Should not panic with nil components
	stopLeaderOnlyComponents(nil, logger)
}

func TestStopLeaderOnlyComponents_NilCancel(t *testing.T) {
	logger := slog.Default()

	// Should not panic with nil cancel
	components := &leaderOnlyComponents{
		cancel: nil,
	}
	stopLeaderOnlyComponents(components, logger)
}

func TestLeaderCallbackState_ConcurrentAccess(t *testing.T) {
	// Test that leaderCallbackState is thread-safe
	state := &leaderCallbackState{}

	// Simulate concurrent reads and writes
	done := make(chan bool)

	go func() {
		for range 100 {
			state.mu.Lock()
			state.components = &leaderOnlyComponents{}
			state.mu.Unlock()
		}
		done <- true
	}()

	go func() {
		for range 100 {
			state.mu.Lock()
			_ = state.components
			state.mu.Unlock()
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done
}

// TestComponentSetup_CleanupOrderAndIdempotence locks the contract
// that AddCleanup-registered callbacks fire in reverse-registration
// (LIFO) order — mirrors `defer` semantics so callers can layer
// dependencies cleanly — and that RunCleanups is idempotent (so
// teardown paths can call it without worrying about double-fire).
//
// Regression: the pluggable-validator manager registers Close() via
// AddCleanup so its connection pools get drained on teardown. If
// RunCleanups silently no-ops or fires in the wrong order, file
// descriptors leak across iteration restarts.
func TestComponentSetup_CleanupOrderAndIdempotence(t *testing.T) {
	setup := &componentSetup{}
	var calls []string
	setup.AddCleanup(func() { calls = append(calls, "first") })
	setup.AddCleanup(func() { calls = append(calls, "second") })
	setup.AddCleanup(func() { calls = append(calls, "third") })

	setup.RunCleanups()
	if len(calls) != 3 || calls[0] != "third" || calls[1] != "second" || calls[2] != "first" {
		t.Fatalf("cleanups fired in wrong order: %v (want LIFO: third, second, first)", calls)
	}

	// Second call must be a no-op — already drained.
	setup.RunCleanups()
	if len(calls) != 3 {
		t.Fatalf("RunCleanups not idempotent; second call re-fired callbacks: %v", calls)
	}
}

// TestComponentSetup_AddCleanupNilSafe locks that AddCleanup ignores
// nil callbacks. Callers don't have to nil-check before registering.
func TestComponentSetup_AddCleanupNilSafe(t *testing.T) {
	setup := &componentSetup{}
	setup.AddCleanup(nil)
	setup.RunCleanups() // must not panic
}
