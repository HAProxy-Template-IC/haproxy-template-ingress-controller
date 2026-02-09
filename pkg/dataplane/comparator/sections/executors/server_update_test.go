package executors

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

func TestServerUpdateWithReloadTracking_TransactionPath(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	reloadTriggered, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "tx-123", 0)

	require.NoError(t, err)
	assert.False(t, reloadTriggered, "transaction path should not report reload at call site")
}

func TestServerUpdateWithReloadTracking_VersionProvided(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	reloadTriggered, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "", 42)

	require.NoError(t, err)
	assert.False(t, reloadTriggered, "HTTP 200 means runtime change, no reload")
}

func TestServerUpdateWithReloadTracking_VersionZero_FetchesFromAPI(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/version":                          testutil.TextResponse(http.StatusOK, "5"),
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	reloadTriggered, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "", 0)

	require.NoError(t, err)
	assert.False(t, reloadTriggered)
}

func TestServerUpdateWithReloadTracking_ReloadDetection_202(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusAccepted),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	reloadTriggered, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "", 10)

	require.NoError(t, err)
	assert.True(t, reloadTriggered, "HTTP 202 means reload was triggered")
}

func TestServerUpdateWithReloadTracking_VersionConflict_409(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Configuration-Version", "99")
				w.WriteHeader(http.StatusConflict)
			},
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	reloadTriggered, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "", 10)

	require.Error(t, err)
	assert.False(t, reloadTriggered)

	var versionErr *client.VersionConflictError
	require.ErrorAs(t, err, &versionErr)
	assert.Equal(t, int64(10), versionErr.ExpectedVersion)
	assert.Equal(t, "99", versionErr.ActualVersion)
}

func TestServerUpdateWithReloadTracking_GetVersionFailure(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/version": testutil.StatusResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	_, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestServerUpdateWithReloadTracking_DispatchError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	_, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "", 10)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestServerUpdate_DelegatesToWithReloadTracking(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			// ServerUpdate with version=0 will call GetVersion first
			"/v3/services/haproxy/configuration/version":                          testutil.TextResponse(http.StatusOK, "5"),
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := ServerUpdate("my-backend")
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	err := executor(context.Background(), c, "", "", "srv1", model)

	require.NoError(t, err)
}

func ptrInt64(v int64) *int64 {
	return &v
}

func TestServerUpdateWithReloadTracking_TransactionPath_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers/srv1": testutil.StatusResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	model := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	reloadTriggered, err := ServerUpdateWithReloadTracking(context.Background(), c, "my-backend", "srv1", model, "tx-123", 0)

	require.Error(t, err)
	assert.False(t, reloadTriggered)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d", http.StatusInternalServerError))
}
