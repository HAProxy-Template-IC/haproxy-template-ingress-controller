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

package executors

import (
	"context"
	"net/http"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

// patterns_test.go covers TestBackendCreate_Success, TestBackendCreate_ServerError,
// and TestBackendDelete_Success as representative tests for the top-level
// transaction pattern; this file parameterizes BackendUpdate and the remaining
// Frontend/Defaults executors across every supported API version (community +
// enterprise) declared in /versions.env.

// --- Backend Update ---

func TestBackendUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/backends/api": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := BackendUpdate()(context.Background(), c, "tx-1",
			&models.Backend{BackendBase: models.BackendBase{Name: "api"}}, "api")
		require.NoError(t, err)
	})
}

func TestBackendUpdate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/api": testutil.StatusResponse(http.StatusNotFound),
		},
	})
	defer server.Close()
	c := newTestClient(t, server)

	err := BackendUpdate()(context.Background(), c, "tx-1",
		&models.Backend{BackendBase: models.BackendBase{Name: "api"}}, "api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

// --- Frontend ---

func TestFrontendCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/frontends": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := FrontendCreate()(context.Background(), c, "tx-1",
			&models.Frontend{FrontendBase: models.FrontendBase{Name: "http"}}, "http")
		require.NoError(t, err)
	})
}

func TestFrontendUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/frontends/http": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := FrontendUpdate()(context.Background(), c, "tx-1",
			&models.Frontend{FrontendBase: models.FrontendBase{Name: "http"}}, "http")
		require.NoError(t, err)
	})
}

func TestFrontendDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/frontends/http": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := FrontendDelete()(context.Background(), c, "tx-1", nil, "http")
		require.NoError(t, err)
	})
}

// --- Defaults ---

func TestDefaultsCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/defaults": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := DefaultsCreate()(context.Background(), c, "tx-1",
			&models.Defaults{DefaultsBase: models.DefaultsBase{Name: "default"}}, "default")
		require.NoError(t, err)
	})
}

func TestDefaultsUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/defaults/default": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := DefaultsUpdate()(context.Background(), c, "tx-1",
			&models.Defaults{DefaultsBase: models.DefaultsBase{Name: "default"}}, "default")
		require.NoError(t, err)
	})
}

func TestDefaultsDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/defaults/default": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := DefaultsDelete()(context.Background(), c, "tx-1", nil, "default")
		require.NoError(t, err)
	})
}
