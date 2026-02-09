package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func logProfilesStorageConfig() storageTestConfig {
	return storageTestConfig{
		endpoint:  "/services/haproxy/configuration/log_profiles",
		itemNames: []string{"default", "debug"},
		itemName:  "default",
		content:   `{"name":"default","log_tag":"haptic"}`,
	}
}

func TestGetAllLogProfiles_Success(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`[{"name":"default"},{"name":"debug"}]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	names, err := client.GetAllLogProfiles(context.Background())
	require.NoError(t, err)
	assert.Len(t, names, 2)
	assert.Contains(t, names, "default")
	assert.Contains(t, names, "debug")
}

func TestGetAllLogProfiles_Empty(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`[]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	names, err := client.GetAllLogProfiles(context.Background())
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestGetAllLogProfiles_ServerError(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetAllLogProfiles(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestGetAllLogProfiles_InvalidJSON(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`{invalid`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetAllLogProfiles(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestGetAllLogProfiles_NilNames(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`[{"name":"valid"},{"name":null},{"log_tag":"orphan"}]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	names, err := client.GetAllLogProfiles(context.Background())
	require.NoError(t, err)
	assert.Len(t, names, 1)
	assert.Equal(t, "valid", names[0])
}

func TestGetLogProfile_Success(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": jsonResponse(`{"name":"default","log_tag":"haptic"}`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	profile, err := client.GetLogProfile(context.Background(), "default")
	require.NoError(t, err)
	assert.Equal(t, "default", profile.Name)
	assert.Equal(t, "haptic", profile.LogTag)
}

func TestGetLogProfile_NotFound(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/missing": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetLogProfile(context.Background(), "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetLogProfile_ServerError(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetLogProfile(context.Background(), "default")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestGetLogProfile_InvalidJSON(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": jsonResponse(`{invalid`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetLogProfile(context.Background(), "default")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestCreateLogProfile_Success(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusCreated)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := client.CreateLogProfile(context.Background(), &LogProfile{Name: "test", LogTag: "myapp"}, "tx-123")
	require.NoError(t, err)
}

func TestCreateLogProfile_ServerError(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := client.CreateLogProfile(context.Background(), &LogProfile{Name: "test"}, "tx-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestUpdateLogProfile_Success(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": func(w http.ResponseWriter, r *http.Request) {
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

	err := client.UpdateLogProfile(context.Background(), "default", &LogProfile{Name: "default", LogTag: "updated"}, "tx-123")
	require.NoError(t, err)
}

func TestUpdateLogProfile_ServerError(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := client.UpdateLogProfile(context.Background(), "default", &LogProfile{Name: "default"}, "tx-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestDeleteLogProfile_Success(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": func(w http.ResponseWriter, r *http.Request) {
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

	err := client.DeleteLogProfile(context.Background(), "default", "tx-123")
	require.NoError(t, err)
}

func TestDeleteLogProfile_NotFound_Idempotent(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/missing": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	// DeleteLogProfile treats 404 as success (already deleted)
	err := client.DeleteLogProfile(context.Background(), "missing", "tx-123")
	require.NoError(t, err)
}

func TestDeleteLogProfile_ServerError(t *testing.T) {
	cfg := logProfilesStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/default": func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusMethodNotAllowed)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	err := client.DeleteLogProfile(context.Background(), "default", "tx-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}
