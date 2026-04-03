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

func TestNewKeepalivedOperations(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	require.NotNil(t, keepalived)
	assert.Equal(t, c, keepalived.client)
}

func TestKeepalivedOperations_StartTransaction_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.JSONResponse(`{"id": "keepalived-tx-123", "status": "in_progress"}`),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	txID, err := keepalived.StartTransaction(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "keepalived-tx-123", txID)
}

func TestKeepalivedOperations_StartTransaction_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusInternalServerError),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.StartTransaction(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestKeepalivedOperations_StartTransaction_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.StartTransaction(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestKeepalivedOperations_StartTransaction_NoIDInResponse(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.JSONResponse(`{"status": "in_progress"}`),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.StartTransaction(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no transaction ID")
}

func TestKeepalivedOperations_CommitTransaction_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions/keepalived-tx-123": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"id": "keepalived-tx-123"}`),
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.CommitTransaction(context.Background(), "keepalived-tx-123")

	require.NoError(t, err)
}

func TestKeepalivedOperations_CommitTransaction_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions/keepalived-tx-123": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPut: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.CommitTransaction(context.Background(), "keepalived-tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestKeepalivedOperations_DeleteTransaction_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions/keepalived-tx-123": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"id": "keepalived-tx-123"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.DeleteTransaction(context.Background(), "keepalived-tx-123")

	require.NoError(t, err)
}

func TestKeepalivedOperations_GetTransaction_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions/keepalived-tx-123": testutil.JSONResponse(`{"id": "keepalived-tx-123", "status": "in_progress"}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	tx, err := keepalived.GetTransaction(context.Background(), "keepalived-tx-123")

	require.NoError(t, err)
	require.NotNil(t, tx)
}

func TestKeepalivedOperations_GetTransaction_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/transactions/nonexistent": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetTransaction(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestKeepalivedOperations_GetAllVRRPInstances_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances": testutil.JSONResponse(`[{"name": "VI_1"}]`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	instances, err := keepalived.GetAllVRRPInstances(context.Background())

	require.NoError(t, err)
	assert.Len(t, instances, 1)
}

func TestKeepalivedOperations_GetAllVRRPInstances_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetAllVRRPInstances(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestKeepalivedOperations_GetAllVRRPInstances_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetAllVRRPInstances(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestKeepalivedOperations_GetVRRPInstance_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances/VI_1": testutil.JSONResponse(`{"name": "VI_1", "virtual_router_id": 51}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	instance, err := keepalived.GetVRRPInstance(context.Background(), "VI_1")

	require.NoError(t, err)
	require.NotNil(t, instance)
}

func TestKeepalivedOperations_GetVRRPInstance_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances/nonexistent": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetVRRPInstance(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestKeepalivedOperations_CreateVRRPInstance_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	instance := &VRRPInstance{}
	err := keepalived.CreateVRRPInstance(context.Background(), "tx-123", instance)

	require.NoError(t, err)
}

func TestKeepalivedOperations_CreateVRRPInstance_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	instance := &VRRPInstance{}
	err := keepalived.CreateVRRPInstance(context.Background(), "tx-123", instance)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestKeepalivedOperations_ReplaceVRRPInstance_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances/VI_1": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"name": "VI_1"}`),
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	instance := &VRRPInstance{}
	err := keepalived.ReplaceVRRPInstance(context.Background(), "tx-123", "VI_1", instance)

	require.NoError(t, err)
}

func TestKeepalivedOperations_DeleteVRRPInstance_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_instances/VI_1": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"name": "VI_1"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.DeleteVRRPInstance(context.Background(), "tx-123", "VI_1")

	require.NoError(t, err)
}

func TestKeepalivedOperations_DeleteVRRPInstance_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.DeleteVRRPInstance(context.Background(), "tx-123", "VI_1")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestKeepalivedOperations_GetAllVRRPSyncGroups_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups": testutil.JSONResponse(`[{"name": "VG_1"}]`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	groups, err := keepalived.GetAllVRRPSyncGroups(context.Background())

	require.NoError(t, err)
	assert.Len(t, groups, 1)
}

func TestKeepalivedOperations_GetAllVRRPSyncGroups_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetAllVRRPSyncGroups(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestKeepalivedOperations_GetAllVRRPSyncGroups_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetAllVRRPSyncGroups(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestKeepalivedOperations_GetVRRPSyncGroup_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups/VG_1": testutil.JSONResponse(`{"name": "VG_1"}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	group, err := keepalived.GetVRRPSyncGroup(context.Background(), "VG_1")

	require.NoError(t, err)
	require.NotNil(t, group)
}

func TestKeepalivedOperations_GetVRRPSyncGroup_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups/nonexistent": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetVRRPSyncGroup(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestKeepalivedOperations_CreateVRRPSyncGroup_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	group := &VRRPSyncGroup{}
	err := keepalived.CreateVRRPSyncGroup(context.Background(), "tx-123", group)

	require.NoError(t, err)
}

func TestKeepalivedOperations_CreateVRRPSyncGroup_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	group := &VRRPSyncGroup{}
	err := keepalived.CreateVRRPSyncGroup(context.Background(), "tx-123", group)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestKeepalivedOperations_DeleteVRRPSyncGroup_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_sync_groups/VG_1": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"name": "VG_1"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.DeleteVRRPSyncGroup(context.Background(), "tx-123", "VG_1")

	require.NoError(t, err)
}

func TestKeepalivedOperations_GetAllVRRPScripts_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts": testutil.JSONResponse(`[{"name": "chk_haproxy"}]`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	scripts, err := keepalived.GetAllVRRPScripts(context.Background())

	require.NoError(t, err)
	assert.Len(t, scripts, 1)
}

func TestKeepalivedOperations_GetAllVRRPScripts_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts": testutil.ErrorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetAllVRRPScripts(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestKeepalivedOperations_GetAllVRRPScripts_CommunityEdition(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: testutil.CommunityAPIVersion,
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetAllVRRPScripts(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestKeepalivedOperations_GetVRRPScript_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts/chk_haproxy": testutil.JSONResponse(`{"name": "chk_haproxy", "script": "/usr/bin/check_haproxy.sh"}`),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	script, err := keepalived.GetVRRPScript(context.Background(), "chk_haproxy")

	require.NoError(t, err)
	require.NotNil(t, script)
}

func TestKeepalivedOperations_GetVRRPScript_NotFound(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts/nonexistent": testutil.ErrorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	_, err := keepalived.GetVRRPScript(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestKeepalivedOperations_CreateVRRPScript_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	script := &VRRPScript{}
	err := keepalived.CreateVRRPScript(context.Background(), "tx-123", script)

	require.NoError(t, err)
}

func TestKeepalivedOperations_CreateVRRPScript_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: testutil.ErrorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	script := &VRRPScript{}
	err := keepalived.CreateVRRPScript(context.Background(), "tx-123", script)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestKeepalivedOperations_DeleteVRRPScript_Success(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts/chk_haproxy": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: testutil.JSONResponse(`{"name": "chk_haproxy"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.DeleteVRRPScript(context.Background(), "tx-123", "chk_haproxy")

	require.NoError(t, err)
}

func TestKeepalivedOperations_DeleteVRRPScript_ServerError(t *testing.T) {
	server := testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		Handlers: map[string]http.HandlerFunc{
			"/v3/services/keepalived/configuration/vrrp_track_scripts/chk_haproxy": testutil.MethodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: testutil.ErrorResponse(http.StatusNotFound),
			}),
		},
	})
	defer server.Close()

	c := testutil.NewTestClient(t, server)
	keepalived := NewKeepalivedOperations(c)

	err := keepalived.DeleteVRRPScript(context.Background(), "tx-123", "chk_haproxy")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}
