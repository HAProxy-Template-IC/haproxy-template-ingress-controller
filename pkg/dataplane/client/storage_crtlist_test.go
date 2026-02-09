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

func TestSanitizeCRTListName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "domain with extension",
			input:    "example.com.crtlist",
			expected: "example_com.crtlist",
		},
		{
			name:     "subdomain with extension",
			input:    "api.example.com.crtlist",
			expected: "api_example_com.crtlist",
		},
		{
			name:     "simple name",
			input:    "mycrtlist.crtlist",
			expected: "mycrtlist.crtlist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeCRTListName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUnsanitizeCRTListName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "underscore with extension",
			input:    "example_com.crtlist",
			expected: "example.com.crtlist",
		},
		{
			name:     "multiple underscores",
			input:    "api_example_com.crtlist",
			expected: "api.example.com.crtlist",
		},
		{
			name:     "no underscores",
			input:    "mycrtlist.crtlist",
			expected: "mycrtlist.crtlist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unsanitizeCRTListName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetAllCRTListFiles_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists": jsonResponse(`[
				{"storage_name": "example_com.crtlist", "description": "Example cert list"},
				{"storage_name": "api_example_com.crtlist", "description": "API cert list"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	files, err := client.GetAllCRTListFiles(context.Background())
	require.NoError(t, err)
	assert.Len(t, files, 2)
	// Names should be unsanitized
	assert.Contains(t, files, "example.com.crtlist")
	assert.Contains(t, files, "api.example.com.crtlist")
}

func TestGetAllCRTListFiles_Empty(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists": jsonResponse(`[]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	files, err := client.GetAllCRTListFiles(context.Background())
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestGetAllCRTListFiles_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetAllCRTListFiles(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed with status 500")
}

func TestGetAllCRTListFiles_UnsupportedVersion(t *testing.T) {
	// v3.1 doesn't support crt-list
	server := newMockServer(t, mockServerConfig{
		apiVersion: "v3.1.0 abcd1234",
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetAllCRTListFiles(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v3.2+")
}

func TestGetCRTListFileContent_Success(t *testing.T) {
	expectedContent := "/etc/haproxy/ssl/example_com.pem [verify required]\n"

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists/example_com.crtlist": textResponse(expectedContent),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	// Use unsanitized name - it should be sanitized before API call
	content, err := client.GetCRTListFileContent(context.Background(), "example.com.crtlist")
	require.NoError(t, err)
	assert.Equal(t, expectedContent, content)
}

func TestGetCRTListFileContent_NotFound(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists/missing_com.crtlist": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetCRTListFileContent(context.Background(), "missing.com.crtlist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCreateCRTListFile_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					// Verify it's multipart
					contentType := r.Header.Get("Content-Type")
					if contentType == "" || len(contentType) < 10 {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.WriteHeader(http.StatusCreated)
					w.Write([]byte(`{"storage_name": "new_com.crtlist"}`))
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.CreateCRTListFile(context.Background(), "new.com.crtlist", "/etc/haproxy/ssl/cert.pem\n")
	require.NoError(t, err)
}

func TestCreateCRTListFile_AlreadyExists(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusConflict)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.CreateCRTListFile(context.Background(), "existing.com.crtlist", "content")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCreateCRTListFile_UnsupportedVersion(t *testing.T) {
	// v3.1 doesn't support crt-list
	server := newMockServer(t, mockServerConfig{
		apiVersion: "v3.1.0 abcd1234",
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.CreateCRTListFile(context.Background(), "new.com.crtlist", "content")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v3.2+")
}

func TestUpdateCRTListFile_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists/example_com.crtlist": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					// Verify content-type is text/plain
					contentType := r.Header.Get("Content-Type")
					if contentType != "text/plain" {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.WriteHeader(http.StatusAccepted)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.UpdateCRTListFile(context.Background(), "example.com.crtlist", "updated content\n")
	require.NoError(t, err)
}

func TestUpdateCRTListFile_NotFound(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists/missing_com.crtlist": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.UpdateCRTListFile(context.Background(), "missing.com.crtlist", "content")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteCRTListFile_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists/example_com.crtlist": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := client.DeleteCRTListFile(context.Background(), "example.com.crtlist")
	require.NoError(t, err)
}

func TestDeleteCRTListFile_NotFound(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists/missing_com.crtlist": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := client.DeleteCRTListFile(context.Background(), "missing.com.crtlist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetAllCRTListFiles_NilStorageNames(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/ssl_crt_lists": jsonResponse(`[
				{"storage_name": "valid_com.crtlist"},
				{"storage_name": null},
				{"description": "no name"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	files, err := client.GetAllCRTListFiles(context.Background())
	require.NoError(t, err)
	// Only the valid entry should be returned
	assert.Len(t, files, 1)
	assert.Equal(t, "valid.com.crtlist", files[0])
}
