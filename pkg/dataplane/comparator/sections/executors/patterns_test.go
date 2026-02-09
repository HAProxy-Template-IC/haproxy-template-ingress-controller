package executors

import (
	"context"
	"net/http"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

// TestBackendCreate_Success validates the top-level create executor pattern.
func TestBackendCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends": testutil.StatusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BackendCreate()
	backend := &models.Backend{BackendBase: models.BackendBase{Name: "my-backend"}}

	err := executor(context.Background(), c, "tx-123", backend, "my-backend")

	require.NoError(t, err)
}

// TestBackendCreate_ServerError validates error propagation for top-level executors.
func TestBackendCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends": testutil.StatusResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BackendCreate()
	backend := &models.Backend{BackendBase: models.BackendBase{Name: "my-backend"}}

	err := executor(context.Background(), c, "tx-123", backend, "my-backend")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// TestBackendDelete_Success validates the top-level delete executor pattern.
func TestBackendDelete_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend": testutil.StatusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BackendDelete()

	err := executor(context.Background(), c, "tx-123", nil, "my-backend")

	require.NoError(t, err)
}

// TestServerCreate_Success validates the named child create executor pattern.
func TestServerCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers": testutil.StatusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := ServerCreate("my-backend")
	srv := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	err := executor(context.Background(), c, "tx-123", "", "srv1", srv)

	require.NoError(t, err)
}

// TestServerCreate_ServerError validates error propagation for named child executors.
func TestServerCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/my-backend/servers": testutil.StatusResponse(http.StatusBadRequest),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := ServerCreate("my-backend")
	srv := &models.Server{Name: "srv1", Address: "10.0.0.1", Port: ptrInt64(8080)}

	err := executor(context.Background(), c, "tx-123", "", "srv1", srv)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

// TestACLFrontendCreate_Success validates the indexed child create executor pattern.
func TestACLFrontendCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/frontends/my-frontend/acls/0": testutil.StatusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := ACLFrontendCreate()
	acl := &models.ACL{ACLName: "is_api", Criterion: "path_beg", Value: "/api"}

	err := executor(context.Background(), c, "tx-123", "my-frontend", 0, acl)

	require.NoError(t, err)
}

// TestACLFrontendCreate_ServerError validates error propagation for indexed child executors.
func TestACLFrontendCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/frontends/my-frontend/acls/0": testutil.StatusResponse(http.StatusConflict),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := ACLFrontendCreate()
	acl := &models.ACL{ACLName: "is_api", Criterion: "path_beg", Value: "/api"}

	err := executor(context.Background(), c, "tx-123", "my-frontend", 0, acl)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "409")
}

// TestAcmeProviderCreate_Success validates the v3.2+ restricted create executor pattern.
func TestAcmeProviderCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/acme": testutil.StatusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := AcmeProviderCreate()
	acme := &models.AcmeProvider{Name: "letsencrypt"}

	err := executor(context.Background(), c, "tx-123", acme, "letsencrypt")

	require.NoError(t, err)
}

// TestAcmeProviderCreate_UnsupportedVersion validates version-restricted executors
// return an error for unsupported API versions.
func TestAcmeProviderCreate_UnsupportedVersion(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		apiVersion: "v3.0.4 abc123",
		handlers:   map[string]http.HandlerFunc{},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := AcmeProviderCreate()
	acme := &models.AcmeProvider{Name: "letsencrypt"}

	err := executor(context.Background(), c, "tx-123", acme, "letsencrypt")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "v3.2+")
}

// TestAcmeProviderDelete_Success validates the v3.2+ restricted delete executor pattern.
func TestAcmeProviderDelete_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/acme/letsencrypt": testutil.StatusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := AcmeProviderDelete()

	err := executor(context.Background(), c, "tx-123", nil, "letsencrypt")

	require.NoError(t, err)
}
