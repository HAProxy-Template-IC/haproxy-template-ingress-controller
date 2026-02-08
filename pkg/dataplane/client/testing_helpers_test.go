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

const (
	testAPIVersion = "v3.2.6 87ad0bcf"
	testUsername   = "admin"
	testPassword   = "password"
)

type mockServerConfig struct {
	apiVersion string
	handlers   map[string]http.HandlerFunc
}

func newMockServer(t *testing.T, cfg mockServerConfig) *httptest.Server {
	t.Helper()

	apiVersion := cfg.apiVersion
	if apiVersion == "" {
		apiVersion = testAPIVersion
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"api":{"version":"%s"}}`, apiVersion)
			return
		}

		if handler, ok := cfg.handlers[r.URL.Path]; ok {
			handler(w, r)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}

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

func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, body)
	}
}

func textResponse(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func errorResponse(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}
}

// storageTestConfig defines configuration for storage API tests.
type storageTestConfig struct {
	endpoint  string
	itemNames []string
	itemName  string
	content   string
}

func runGetAllStorageSuccessTest(t *testing.T, cfg storageTestConfig, getAllFunc func(context.Context, *DataplaneClient) ([]string, error)) {
	t.Helper()

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
