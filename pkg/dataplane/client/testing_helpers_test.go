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
	require.NoError(t, err, "creating test client")
	return c
}

// newTestClientWithHandler spins up an httptest.Server with the given handler
// and returns a client connected to it plus a cleanup function.
func newTestClientWithHandler(t *testing.T, handler http.HandlerFunc) (client *DataplaneClient, cleanup func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	return newTestClient(t, server), server.Close
}

func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, body)
	}
}

func textResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
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
	endpoint         string
	itemNames        []string
	itemName         string
	notFoundItemName string
	content          string
}

// storageTestFuncs groups the CRUD function adapters for a storage type.
type storageTestFuncs struct {
	getAll func(context.Context, *DataplaneClient) ([]string, error)
	create func(context.Context, *DataplaneClient, string, string) error
	update func(context.Context, *DataplaneClient, string, string) error
	delete func(context.Context, *DataplaneClient, string) error
}

// runGetAllStorageTests runs the standard GetAll subtests (success, empty, server error, invalid JSON).
func runGetAllStorageTests(t *testing.T, cfg *storageTestConfig, getAllFunc func(context.Context, *DataplaneClient) ([]string, error)) {
	t.Helper()

	t.Run("Success", func(t *testing.T) {
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
	})

	t.Run("Empty", func(t *testing.T) {
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
	})

	t.Run("ServerError", func(t *testing.T) {
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
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		server := newMockServer(t, mockServerConfig{
			handlers: map[string]http.HandlerFunc{
				cfg.endpoint: jsonResponse(`{invalid json}`),
			},
		})
		defer server.Close()

		client := newTestClient(t, server)

		_, err := getAllFunc(context.Background(), client)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decoding")
	})
}

// runCreateStorageTests runs the standard Create subtests (success, conflict/already exists).
func runCreateStorageTests(t *testing.T, cfg *storageTestConfig, createFunc func(context.Context, *DataplaneClient, string, string) error) {
	t.Helper()

	t.Run("Success", func(t *testing.T) {
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
	})

	t.Run("AlreadyExists", func(t *testing.T) {
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
	})
}

// runDeleteStorageTests runs the standard Delete subtests (success, not found).
func runDeleteStorageTests(t *testing.T, cfg *storageTestConfig, deleteFunc func(context.Context, *DataplaneClient, string) error) {
	t.Helper()

	t.Run("Success", func(t *testing.T) {
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
	})

	notFoundName := cfg.notFoundItemName
	if notFoundName == "" {
		notFoundName = cfg.itemName
	}

	t.Run("NotFound", func(t *testing.T) {
		server := newMockServer(t, mockServerConfig{
			handlers: map[string]http.HandlerFunc{
				cfg.endpoint + "/" + notFoundName: func(w http.ResponseWriter, r *http.Request) {
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

		err := deleteFunc(context.Background(), client, notFoundName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// runUpdateStorageTests runs the standard Update subtests (success, not found).
func runUpdateStorageTests(t *testing.T, cfg *storageTestConfig, updateFunc func(context.Context, *DataplaneClient, string, string) error) {
	t.Helper()

	t.Run("Success", func(t *testing.T) {
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
	})

	notFoundName := cfg.notFoundItemName
	if notFoundName == "" {
		notFoundName = cfg.itemName
	}

	t.Run("NotFound", func(t *testing.T) {
		server := newMockServer(t, mockServerConfig{
			handlers: map[string]http.HandlerFunc{
				cfg.endpoint + "/" + notFoundName: func(w http.ResponseWriter, r *http.Request) {
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

		err := updateFunc(context.Background(), client, notFoundName, cfg.content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// runAllStorageCRUDTests runs all standard CRUD subtests for a storage type.
func runAllStorageCRUDTests(t *testing.T, cfg *storageTestConfig, funcs storageTestFuncs) {
	t.Helper()

	t.Run("GetAll", func(t *testing.T) {
		runGetAllStorageTests(t, cfg, funcs.getAll)
	})
	t.Run("Create", func(t *testing.T) {
		runCreateStorageTests(t, cfg, funcs.create)
	})
	t.Run("Delete", func(t *testing.T) {
		runDeleteStorageTests(t, cfg, funcs.delete)
	})
	t.Run("Update", func(t *testing.T) {
		runUpdateStorageTests(t, cfg, funcs.update)
	})
}
