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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReloadIDHeaderConstant(t *testing.T) {
	// Verify the header constant matches what HAProxy Data Plane API sends
	assert.Equal(t, "Reload-Id", ReloadIDHeader)
}

func TestBuildMultipartFilePayload(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
	}{
		{
			name:     "simple file",
			filename: "test.txt",
			content:  "hello world",
		},
		{
			name:     "certificate file",
			filename: "cert.pem",
			content:  "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		},
		{
			name:     "empty content",
			filename: "empty.txt",
			content:  "",
		},
		{
			name:     "binary-like content",
			filename: "data.bin",
			content:  "\x00\x01\x02\x03",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, contentType, err := buildMultipartFilePayload(tt.filename, tt.content)

			require.NoError(t, err)
			require.NotNil(t, body)
			assert.Contains(t, contentType, "multipart/form-data")
			assert.Contains(t, contentType, "boundary=")

			// Verify body contains the filename
			bodyStr := body.String()
			assert.Contains(t, bodyStr, tt.filename)
			assert.Contains(t, bodyStr, "file_upload")
		})
	}
}

func TestBuildMultipartFilePayloadWithID(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
		id       string
	}{
		{
			name:     "general file with path ID",
			filename: "error.http",
			content:  "HTTP/1.0 500 Internal Server Error",
			id:       "/etc/haproxy/errors/error.http",
		},
		{
			name:     "file with simple ID",
			filename: "test.txt",
			content:  "content",
			id:       "test-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, contentType, err := buildMultipartFilePayloadWithID(tt.filename, tt.content, tt.id)

			require.NoError(t, err)
			require.NotNil(t, body)
			assert.Contains(t, contentType, "multipart/form-data")

			// Verify body contains the filename and ID
			bodyStr := body.String()
			assert.Contains(t, bodyStr, tt.filename)
			assert.Contains(t, bodyStr, "file_upload")
			assert.Contains(t, bodyStr, tt.id)
		})
	}
}

func TestCheckCreateResponse(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		headers        http.Header
		resourceType   string
		resourceName   string
		expectErr      bool
		errContains    string
		expectReloadID string
	}{
		{
			name:           "201 Created success - no reload",
			statusCode:     http.StatusCreated,
			resourceType:   "certificate",
			resourceName:   "cert.pem",
			expectErr:      false,
			expectReloadID: "",
		},
		{
			name:           "200 OK success - no reload",
			statusCode:     http.StatusOK,
			resourceType:   "certificate",
			resourceName:   "cert.pem",
			expectErr:      false,
			expectReloadID: "",
		},
		{
			name:       "202 Accepted - with reload ID",
			statusCode: http.StatusAccepted,
			headers: http.Header{
				"Reload-Id": []string{"reload-abc123"},
			},
			resourceType:   "map file",
			resourceName:   "hosts.map",
			expectErr:      false,
			expectReloadID: "reload-abc123",
		},
		{
			name:           "202 Accepted - without reload ID header",
			statusCode:     http.StatusAccepted,
			resourceType:   "map file",
			resourceName:   "hosts.map",
			expectErr:      false,
			expectReloadID: "",
		},
		{
			name:         "409 Conflict - already exists",
			statusCode:   http.StatusConflict,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "already exists",
		},
		{
			name:         "500 Internal Server Error",
			statusCode:   http.StatusInternalServerError,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "failed with status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Header:     tt.headers,
				Body:       io.NopCloser(strings.NewReader("error body")),
			}

			reloadID, err := checkCreateResponse(resp, tt.resourceType, tt.resourceName)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectReloadID, reloadID)
			}
		})
	}
}

func TestCheckUpdateResponse(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		headers        http.Header
		resourceType   string
		resourceName   string
		expectErr      bool
		errContains    string
		expectReloadID string
	}{
		{
			name:           "200 OK success - no reload",
			statusCode:     http.StatusOK,
			resourceType:   "certificate",
			resourceName:   "cert.pem",
			expectErr:      false,
			expectReloadID: "",
		},
		{
			name:       "202 Accepted - with reload ID",
			statusCode: http.StatusAccepted,
			headers: http.Header{
				"Reload-Id": []string{"reload-xyz789"},
			},
			resourceType:   "map file",
			resourceName:   "hosts.map",
			expectErr:      false,
			expectReloadID: "reload-xyz789",
		},
		{
			name:           "202 Accepted - without reload ID header",
			statusCode:     http.StatusAccepted,
			resourceType:   "map file",
			resourceName:   "hosts.map",
			expectErr:      false,
			expectReloadID: "",
		},
		{
			name:         "404 Not Found",
			statusCode:   http.StatusNotFound,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "not found",
		},
		{
			name:         "500 Internal Server Error",
			statusCode:   http.StatusInternalServerError,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "failed with status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Header:     tt.headers,
				Body:       io.NopCloser(strings.NewReader("error body")),
			}

			reloadID, err := checkUpdateResponse(resp, tt.resourceType, tt.resourceName)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectReloadID, reloadID)
			}
		})
	}
}

func TestCheckDeleteResponse(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		resourceType string
		resourceName string
		expectErr    bool
		errContains  string
	}{
		{
			name:         "200 OK success",
			statusCode:   http.StatusOK,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    false,
		},
		{
			name:         "202 Accepted success",
			statusCode:   http.StatusAccepted,
			resourceType: "map file",
			resourceName: "hosts.map",
			expectErr:    false,
		},
		{
			name:         "204 No Content success",
			statusCode:   http.StatusNoContent,
			resourceType: "general file",
			resourceName: "error.http",
			expectErr:    false,
		},
		{
			name:         "404 Not Found",
			statusCode:   http.StatusNotFound,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "not found",
		},
		{
			name:         "500 Internal Server Error",
			statusCode:   http.StatusInternalServerError,
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "failed with status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader("")),
			}

			err := checkDeleteResponse(resp, tt.resourceType, tt.resourceName)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReadRawStorageContent(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		resourceType string
		resourceName string
		expectErr    bool
		errContains  string
		wantContent  string
	}{
		{
			name:         "200 OK with content",
			statusCode:   http.StatusOK,
			body:         "file content here",
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    false,
			wantContent:  "file content here",
		},
		{
			name:         "200 OK with empty content",
			statusCode:   http.StatusOK,
			body:         "",
			resourceType: "map file",
			resourceName: "empty.map",
			expectErr:    false,
			wantContent:  "",
		},
		{
			name:         "404 Not Found",
			statusCode:   http.StatusNotFound,
			body:         "",
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "not found",
		},
		{
			name:         "500 Internal Server Error",
			statusCode:   http.StatusInternalServerError,
			body:         "",
			resourceType: "certificate",
			resourceName: "cert.pem",
			expectErr:    true,
			errContains:  "failed with status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}

			content, err := readRawStorageContent(resp, tt.resourceType, tt.resourceName)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantContent, content)
			}
		})
	}
}

func TestSanitizeStorageName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "domain with extension",
			in:   "example.com.pem",
			want: "example_com.pem",
		},
		{
			name: "subdomain with extension",
			in:   "api.example.com.pem",
			want: "api_example_com.pem",
		},
		{
			name: "no dots",
			in:   "cert.pem",
			want: "cert.pem",
		},
		{
			name: "looks like extension (no sanitization needed)",
			in:   "example.com",
			want: "example.com", // filepath.Ext returns ".com", so no dots in base to replace
		},
		{
			name: "multiple extensions",
			in:   "cert.bundle.pem",
			want: "cert_bundle.pem",
		},
		{
			name: "map file",
			in:   "hosts.example.map",
			want: "hosts_example.map",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeStorageName(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUnsanitizeStorageName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "sanitized domain with extension",
			in:   "example_com.pem",
			want: "example.com.pem",
		},
		{
			name: "sanitized subdomain with extension",
			in:   "api_example_com.pem",
			want: "api.example.com.pem",
		},
		{
			name: "no underscores",
			in:   "cert.pem",
			want: "cert.pem",
		},
		{
			name: "no extension - unchanged",
			in:   "example_com",
			want: "example_com",
		},
		{
			name: "map file",
			in:   "hosts_example.map",
			want: "hosts.example.map",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnsanitizeStorageName(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeUnsanitizeRoundtrip(t *testing.T) {
	// Test that sanitize -> unsanitize returns the original for common cases
	names := []string{
		"example.com.pem",
		"api.example.com.pem",
		"test.crt",
		"hosts.map",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			sanitized := SanitizeStorageName(name)
			unsanitized := UnsanitizeStorageName(sanitized)
			assert.Equal(t, name, unsanitized)
		})
	}
}
