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

func TestNewALOHAOperations(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	require.NotNil(t, aloha)
	assert.Equal(t, c, aloha.client)
}

func TestALOHAOperations_GetEndpoints_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha": testutil.JSONResponse(`[{"url": "http://10.0.0.1"}]`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	endpoints, err := aloha.GetEndpoints(context.Background())

	require.NoError(t, err)
	require.NotNil(t, endpoints)
}

func TestALOHAOperations_GetEndpoints_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetEndpoints(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestALOHAOperations_GetEndpoints_InvalidJSON(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha": testutil.JSONResponse(`[not-valid`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetEndpoints(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding")
}

func TestALOHAOperations_GetEndpoints_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetEndpoints(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestALOHAOperations_GetAllActions_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions": testutil.JSONResponse(`[{"id": "reboot", "name": "Reboot"}]`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	actions, err := aloha.GetAllActions(context.Background())

	require.NoError(t, err)
	assert.Len(t, actions, 1)
}

func TestALOHAOperations_GetAllActions_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetAllActions(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestALOHAOperations_GetAllActions_InvalidJSON(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions": testutil.JSONResponse(`not-an-array`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetAllActions(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding")
}

func TestALOHAOperations_GetAllActions_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetAllActions(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestALOHAOperations_GetAction_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions/reboot": testutil.JSONResponse(`{"id": "reboot", "name": "Reboot"}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	action, err := aloha.GetAction(context.Background(), "reboot")

	require.NoError(t, err)
	require.NotNil(t, action)
}

func TestALOHAOperations_GetAction_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions/nonexistent": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetAction(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestALOHAOperations_GetAction_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions/reboot": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetAction(context.Background(), "reboot")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestALOHAOperations_GetAction_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	_, err := aloha.GetAction(context.Background(), "reboot")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestALOHAOperations_ExecuteAction_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.JSONResponse(`{"id": "reboot", "status": "executing"}`),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	action := &ALOHAAction{}
	result, err := aloha.ExecuteAction(context.Background(), action)

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestALOHAOperations_ExecuteAction_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/aloha/actions": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	action := &ALOHAAction{}
	_, err := aloha.ExecuteAction(context.Background(), action)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestALOHAOperations_ExecuteAction_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	aloha := NewALOHAOperations(c)

	action := &ALOHAAction{}
	_, err := aloha.ExecuteAction(context.Background(), action)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}
