package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper constants for mock server configuration.
const (
	testAPIVersion = "v3.2.6 87ad0bcf"
	testUsername   = "admin"
	testPassword   = "password"
)

// mockServerConfig configures a mock Dataplane API server for tests.
type mockServerConfig struct {
	// apiVersion is returned by the /v3/info endpoint.
	// Defaults to testAPIVersion if empty.
	apiVersion string

	// handlers maps URL paths to handler functions.
	handlers map[string]http.HandlerFunc
}

// newMockServer creates a test server that mimics the Dataplane API.
// It always provides a /v3/info endpoint returning the configured API version.
func newMockServer(t *testing.T, cfg mockServerConfig) *httptest.Server {
	t.Helper()

	apiVersion := cfg.apiVersion
	if apiVersion == "" {
		apiVersion = testAPIVersion
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle /v3/info specially - always required for client initialization
		if r.URL.Path == "/v3/info" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"api":{"version":"%s"}}`, apiVersion)
			return
		}

		// Check for custom handler
		if handler, ok := cfg.handlers[r.URL.Path]; ok {
			handler(w, r)
			return
		}

		// Default: 404
		w.WriteHeader(http.StatusNotFound)
	}))
}

// newTestClient creates a client connected to a test server.
func newTestClient(t *testing.T, server *httptest.Server) *DataplaneClient {
	t.Helper()

	c, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: testUsername,
		Password: testPassword,
	})
	require.NoError(t, err, "failed to create test client")
	return c
}

// jsonResponse creates a handler that returns JSON with http.StatusOK.
func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, body)
	}
}

// textResponse creates a handler that returns plain text with the given status code.
func textResponse(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// errorResponse creates a handler that returns an error status.
func errorResponse(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}
}

// storageTestConfig defines configuration for storage API tests.
type storageTestConfig struct {
	// endpoint is the API endpoint path (e.g., "/services/haproxy/storage/general")
	endpoint string
	// itemNames are the expected storage_name values in list responses
	itemNames []string
	// itemName is the specific file name for get/create/update/delete tests
	itemName string
	// content is the file content for create/update tests
	content string
}

// runGetAllStorageSuccessTest tests that GetAll returns expected items.
func runGetAllStorageSuccessTest(t *testing.T, cfg storageTestConfig, getAllFunc func(context.Context, *DataplaneClient) ([]string, error)) {
	t.Helper()

	// Build JSON response
	items := make([]string, 0, len(cfg.itemNames))
	for _, name := range cfg.itemNames {
		items = append(items, fmt.Sprintf(`{"storage_name": %q}`, name))
	}
	jsonResp := "[" + strings.Join(items, ",") + "]"

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(jsonResp),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	files, err := getAllFunc(context.Background(), client)
	require.NoError(t, err)
	assert.Len(t, files, len(cfg.itemNames))
	for _, name := range cfg.itemNames {
		assert.Contains(t, files, name)
	}
}

// runGetAllStorageEmptyTest tests that GetAll returns empty slice for empty response.
func runGetAllStorageEmptyTest(t *testing.T, cfg storageTestConfig, getAllFunc func(context.Context, *DataplaneClient) ([]string, error)) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`[]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	files, err := getAllFunc(context.Background(), client)
	require.NoError(t, err)
	assert.Empty(t, files)
}

// runGetAllStorageServerErrorTest tests that GetAll returns error on server error.
func runGetAllStorageServerErrorTest(t *testing.T, cfg storageTestConfig, getAllFunc func(context.Context, *DataplaneClient) ([]string, error)) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := getAllFunc(context.Background(), client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed with status 500")
}

// runGetAllStorageInvalidJSONTest tests that GetAll returns error on invalid JSON.
func runGetAllStorageInvalidJSONTest(t *testing.T, cfg storageTestConfig, getAllFunc func(context.Context, *DataplaneClient) ([]string, error)) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`{invalid json}`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := getAllFunc(context.Background(), client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

// runCreateStorageSuccessTest tests successful file creation.
func runCreateStorageSuccessTest(t *testing.T, cfg storageTestConfig, createFunc func(context.Context, *DataplaneClient, string, string) error) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusCreated)
					fmt.Fprintf(w, `{"storage_name": "%s"}`, cfg.itemName)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := createFunc(context.Background(), client, cfg.itemName, cfg.content)
	require.NoError(t, err)
}

// runCreateStorageConflictTest tests that Create returns error when file exists.
func runCreateStorageConflictTest(t *testing.T, cfg storageTestConfig, createFunc func(context.Context, *DataplaneClient, string, string) error) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: func(w http.ResponseWriter, r *http.Request) {
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

	err := createFunc(context.Background(), client, cfg.itemName, cfg.content)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// runDeleteStorageSuccessTest tests successful file deletion.
func runDeleteStorageSuccessTest(t *testing.T, cfg storageTestConfig, deleteFunc func(context.Context, *DataplaneClient, string) error) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/" + cfg.itemName: func(w http.ResponseWriter, r *http.Request) {
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

	err := deleteFunc(context.Background(), client, cfg.itemName)
	require.NoError(t, err)
}

// runDeleteStorageNotFoundTest tests that Delete returns error when file not found.
func runDeleteStorageNotFoundTest(t *testing.T, cfg storageTestConfig, deleteFunc func(context.Context, *DataplaneClient, string) error) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/" + cfg.itemName: func(w http.ResponseWriter, r *http.Request) {
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

	err := deleteFunc(context.Background(), client, cfg.itemName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// runUpdateStorageSuccessTest tests successful file update.
func runUpdateStorageSuccessTest(t *testing.T, cfg storageTestConfig, updateFunc func(context.Context, *DataplaneClient, string, string) error) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/" + cfg.itemName: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := updateFunc(context.Background(), client, cfg.itemName, cfg.content)
	require.NoError(t, err)
}

// runUpdateStorageNotFoundTest tests that Update returns error when file not found.
func runUpdateStorageNotFoundTest(t *testing.T, cfg storageTestConfig, updateFunc func(context.Context, *DataplaneClient, string, string) error) {
	t.Helper()

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/" + cfg.itemName: func(w http.ResponseWriter, r *http.Request) {
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

	err := updateFunc(context.Background(), client, cfg.itemName, cfg.content)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
