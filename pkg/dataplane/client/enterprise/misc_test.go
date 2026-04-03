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

package enterprise

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

func TestNewMiscOperations(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	require.NotNil(t, misc)
	assert.Equal(t, c, misc.client)
}

func TestMiscOperations_GetFacts_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			// Facts is map[string]string
			"/v3/facts": testutil.JSONResponse(`{"os_name": "Linux", "hostname": "test-node"}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	facts, err := misc.GetFacts(context.Background(), false)

	require.NoError(t, err)
	require.NotNil(t, facts)
	assert.Equal(t, "Linux", (*facts)["os_name"])
}

func TestMiscOperations_GetFacts_WithRefresh(t *testing.T) {
	refreshCalled := false
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/facts": func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("refresh") == "true" {
					refreshCalled = true
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"refreshed": "true"}`))
			},
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetFacts(context.Background(), true)

	require.NoError(t, err)
	assert.True(t, refreshCalled)
}

func TestMiscOperations_GetFacts_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/facts": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetFacts(context.Background(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestMiscOperations_GetFacts_InvalidJSON(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/facts": testutil.JSONResponse(`{invalid`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetFacts(context.Background(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding")
}

func TestMiscOperations_GetFacts_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetFacts(context.Background(), false)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestMiscOperations_Ping_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: "v3.2.6-ee1", // v3.2 required for ping
		Handlers: map[string]http.HandlerFunc{
			"/v3/ping": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	err := misc.Ping(context.Background())

	require.NoError(t, err)
}

func TestMiscOperations_Ping_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: "v3.2.6-ee1",
		Handlers: map[string]http.HandlerFunc{
			"/v3/ping": testutil.ErrorResponse(http.StatusServiceUnavailable),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	err := misc.Ping(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 503")
}

func TestMiscOperations_Ping_RequiresV32(t *testing.T) {
	// v3.1 doesn't support ping endpoint
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	err := misc.Ping(context.Background())

	require.Error(t, err)
	assert.Equal(t, ErrPingRequiresV32, err)
}

func TestMiscOperations_Ping_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	err := misc.Ping(context.Background())

	// First check is for v3.2 version requirement
	require.Error(t, err)
}

func TestMiscOperations_GetStructuredConfig_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			// Structured config is a complex type - use minimal valid JSON
			"/v3/services/haproxy/configuration/structured": testutil.JSONResponse(`{}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	config, err := misc.GetStructuredConfig(context.Background(), "")

	require.NoError(t, err)
	require.NotNil(t, config)
}

func TestMiscOperations_GetStructuredConfig_WithTransaction(t *testing.T) {
	var receivedTxID string
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/structured": func(w http.ResponseWriter, r *http.Request) {
				receivedTxID = r.URL.Query().Get("transaction_id")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetStructuredConfig(context.Background(), "tx-123")

	require.NoError(t, err)
	assert.Equal(t, "tx-123", receivedTxID)
}

func TestMiscOperations_GetStructuredConfig_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/structured": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetStructuredConfig(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestMiscOperations_GetStructuredConfig_InvalidJSON(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/structured": testutil.JSONResponse(`not-json`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetStructuredConfig(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding")
}

func TestMiscOperations_GetStructuredConfig_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	_, err := misc.GetStructuredConfig(context.Background(), "")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestMiscOperations_ReplaceStructuredConfig_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/structured": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
				http.MethodGet: testutil.JSONResponse(`{}`),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	config := &StructuredConfig{}
	err := misc.ReplaceStructuredConfig(context.Background(), "tx-123", config)

	require.NoError(t, err)
}

func TestMiscOperations_ReplaceStructuredConfig_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/structured": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPut: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	config := &StructuredConfig{}
	err := misc.ReplaceStructuredConfig(context.Background(), "tx-123", config)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestMiscOperations_ReplaceStructuredConfig_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	misc := NewMiscOperations(c)

	config := &StructuredConfig{}
	err := misc.ReplaceStructuredConfig(context.Background(), "tx-123", config)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}
