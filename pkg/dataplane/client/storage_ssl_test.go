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

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeSSLCertName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "domain with extension",
			input:    "example.com.pem",
			expected: "example_com.pem",
		},
		{
			name:     "subdomain with extension",
			input:    "api.example.com.pem",
			expected: "api_example_com.pem",
		},
		{
			name:     "simple name",
			input:    "cert.pem",
			expected: "cert.pem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeSSLCertName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// sslStorageConfig returns the test configuration for SSL storage tests.
func sslStorageConfig() storageTestConfig {
	return storageTestConfig{
		endpoint:  "/services/haproxy/storage/ssl_certificates",
		itemNames: []string{"example_com.pem", "test_org.pem"},
		itemName:  "example_com.pem",
		content:   "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
	}
}

func getAllSSLCertificates(ctx context.Context, c *DataplaneClient) ([]string, error) {
	return c.GetAllSSLCertificates(ctx)
}

func createSSLCertificate(ctx context.Context, c *DataplaneClient, name, content string) error {
	_, err := c.CreateSSLCertificate(ctx, name, content)
	return err
}

func updateSSLCertificate(ctx context.Context, c *DataplaneClient, name, content string) error {
	_, err := c.UpdateSSLCertificate(ctx, name, content)
	return err
}

func deleteSSLCertificate(ctx context.Context, c *DataplaneClient, name string) error {
	return c.DeleteSSLCertificate(ctx, name)
}

func TestGetAllSSLCertificates_Success(t *testing.T) {
	runGetAllStorageSuccessTest(t, sslStorageConfig(), getAllSSLCertificates)
}

func TestGetAllSSLCertificates_Empty(t *testing.T) {
	runGetAllStorageEmptyTest(t, sslStorageConfig(), getAllSSLCertificates)
}

func TestGetAllSSLCertificates_ServerError(t *testing.T) {
	runGetAllStorageServerErrorTest(t, sslStorageConfig(), getAllSSLCertificates)
}

func TestGetSSLCertificateContent_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_certificates/example_com.pem": jsonResponse(`{
				"storage_name": "example_com.pem",
				"file": "/etc/haproxy/ssl/example_com.pem",
				"sha256_finger_print": "abc123def456"
			}`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	fingerprint, err := client.GetSSLCertificateContent(context.Background(), "example.com.pem")
	require.NoError(t, err)
	assert.Equal(t, "abc123def456", fingerprint)
}

func TestGetSSLCertificateContent_NotFound(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_certificates/missing_cert.pem": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetSSLCertificateContent(context.Background(), "missing.cert.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetSSLCertificateContent_EmptyResponse(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_certificates/empty_cert.pem": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				// Empty response body
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	content, err := client.GetSSLCertificateContent(context.Background(), "empty.cert.pem")
	require.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestGetSSLCertificateContent_NoFingerprint(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_certificates/no_fingerprint_cert.pem": jsonResponse(`{
				"storage_name": "no_fingerprint_cert.pem",
				"file": "/etc/haproxy/ssl/no_fingerprint_cert.pem"
			}`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	fingerprint, err := client.GetSSLCertificateContent(context.Background(), "no.fingerprint.cert.pem")
	require.NoError(t, err)
	assert.Equal(t, "__NO_FINGERPRINT__", fingerprint)
}

func TestCreateSSLCertificate_Success(t *testing.T) {
	runCreateStorageSuccessTest(t, sslStorageConfig(), createSSLCertificate)
}

func TestCreateSSLCertificate_AlreadyExists(t *testing.T) {
	runCreateStorageConflictTest(t, sslStorageConfig(), createSSLCertificate)
}

func TestUpdateSSLCertificate_Success(t *testing.T) {
	runUpdateStorageSuccessTest(t, sslStorageConfig(), updateSSLCertificate)
}

func TestUpdateSSLCertificate_NotFound(t *testing.T) {
	cfg := sslStorageConfig()
	cfg.itemName = "missing_cert.pem"
	runUpdateStorageNotFoundTest(t, cfg, updateSSLCertificate)
}

func TestDeleteSSLCertificate_Success(t *testing.T) {
	runDeleteStorageSuccessTest(t, sslStorageConfig(), deleteSSLCertificate)
}

func TestDeleteSSLCertificate_NotFound(t *testing.T) {
	cfg := sslStorageConfig()
	cfg.itemName = "missing_cert.pem"
	runDeleteStorageNotFoundTest(t, cfg, deleteSSLCertificate)
}

func TestGetAllSSLCertificates_NilStorageNames(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_certificates": jsonResponse(`[
				{"storage_name": "valid.pem"},
				{"storage_name": null},
				{"description": "no name"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	certs, err := client.GetAllSSLCertificates(context.Background())
	require.NoError(t, err)
	assert.Len(t, certs, 1)
	assert.Equal(t, "valid.pem", certs[0])
}

func TestGetSSLCertificateContent_InvalidJSON(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_certificates/invalid_cert.pem": jsonResponse(`{invalid json}`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetSSLCertificateContent(context.Background(), "invalid.cert.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode SSL certificate response")
}
