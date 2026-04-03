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

func TestNewDynamicUpdateOperations(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	require.NotNil(t, du)
	assert.Equal(t, c, du.client)
}

func TestDynamicUpdateOperations_GetSection_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.JSONResponse(`{}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.GetSection(context.Background(), "tx-123")

	require.NoError(t, err)
}

func TestDynamicUpdateOperations_GetSection_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.GetSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestDynamicUpdateOperations_GetSection_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.GetSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestDynamicUpdateOperations_GetSection_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.GetSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestDynamicUpdateOperations_CreateSection_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.CreateSection(context.Background(), "tx-123")

	require.NoError(t, err)
}

func TestDynamicUpdateOperations_CreateSection_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.CreateSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestDynamicUpdateOperations_CreateSection_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.CreateSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestDynamicUpdateOperations_DeleteSection_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.DeleteSection(context.Background(), "tx-123")

	require.NoError(t, err)
}

func TestDynamicUpdateOperations_DeleteSection_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_section": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.DeleteSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestDynamicUpdateOperations_DeleteSection_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.DeleteSection(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestDynamicUpdateOperations_GetAllRules_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules": testutil.JSONResponse(`[{"index": 0}]`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	rules, err := du.GetAllRules(context.Background(), "tx-123")

	require.NoError(t, err)
	assert.Len(t, rules, 1)
}

func TestDynamicUpdateOperations_GetAllRules_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	_, err := du.GetAllRules(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestDynamicUpdateOperations_GetAllRules_InvalidJSON(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules": testutil.JSONResponse(`[not-valid`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	_, err := du.GetAllRules(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestDynamicUpdateOperations_GetAllRules_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	_, err := du.GetAllRules(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestDynamicUpdateOperations_GetRule_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/0": testutil.JSONResponse(`{"index": 0}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	rule, err := du.GetRule(context.Background(), "tx-123", 0)

	require.NoError(t, err)
	require.NotNil(t, rule)
}

func TestDynamicUpdateOperations_GetRule_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/99": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	_, err := du.GetRule(context.Background(), "tx-123", 99)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestDynamicUpdateOperations_GetRule_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/0": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	_, err := du.GetRule(context.Background(), "tx-123", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestDynamicUpdateOperations_GetRule_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	_, err := du.GetRule(context.Background(), "tx-123", 0)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestDynamicUpdateOperations_CreateRule_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/0": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"index": 0}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	rule := &DynamicUpdateRule{}
	err := du.CreateRule(context.Background(), "tx-123", 0, rule)

	require.NoError(t, err)
}

func TestDynamicUpdateOperations_CreateRule_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/0": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	rule := &DynamicUpdateRule{}
	err := du.CreateRule(context.Background(), "tx-123", 0, rule)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestDynamicUpdateOperations_CreateRule_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	rule := &DynamicUpdateRule{}
	err := du.CreateRule(context.Background(), "tx-123", 0, rule)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestDynamicUpdateOperations_DeleteRule_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/0": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"index": 0}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.DeleteRule(context.Background(), "tx-123", 0)

	require.NoError(t, err)
}

func TestDynamicUpdateOperations_DeleteRule_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/dynamic_update_rules/0": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.DeleteRule(context.Background(), "tx-123", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestDynamicUpdateOperations_DeleteRule_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	du := NewDynamicUpdateOperations(c)

	err := du.DeleteRule(context.Background(), "tx-123", 0)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}
