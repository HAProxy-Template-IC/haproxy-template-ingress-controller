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

	"haptic/pkg/dataplane/client"
)

func TestNewUDPLBOperations(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	require.NotNil(t, udp)
	assert.Equal(t, c, udp.client)
}

// =============================================================================
// UDP Load Balancer Tests
// =============================================================================

func TestUDPLBOperations_GetAllUDPLbs_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs": jsonResponse(`[{"name": "dns"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	lbs, err := udp.GetAllUDPLbs(context.Background(), "tx-123")

	require.NoError(t, err)
	assert.Len(t, lbs, 1)
}

func TestUDPLBOperations_GetAllUDPLbs_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllUDPLbs(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestUDPLBOperations_GetAllUDPLbs_InvalidJSON(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs": jsonResponse(`not-array`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllUDPLbs(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestUDPLBOperations_GetAllUDPLbs_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllUDPLbs(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestUDPLBOperations_GetUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns": jsonResponse(`{"name": "dns"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	lb, err := udp.GetUDPLb(context.Background(), "tx-123", "dns")

	require.NoError(t, err)
	require.NotNil(t, lb)
}

func TestUDPLBOperations_GetUDPLb_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetUDPLb(context.Background(), "tx-123", "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestUDPLBOperations_GetUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestUDPLBOperations_CreateUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	lb := &UDPLb{}
	err := udp.CreateUDPLb(context.Background(), "tx-123", lb)

	require.NoError(t, err)
}

func TestUDPLBOperations_CreateUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	lb := &UDPLb{}
	err := udp.CreateUDPLb(context.Background(), "tx-123", lb)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestUDPLBOperations_CreateUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	lb := &UDPLb{}
	err := udp.CreateUDPLb(context.Background(), "tx-123", lb)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestUDPLBOperations_ReplaceUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "dns"}`),
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	lb := &UDPLb{}
	err := udp.ReplaceUDPLb(context.Background(), "tx-123", "dns", lb)

	require.NoError(t, err)
}

func TestUDPLBOperations_DeleteUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "dns"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteUDPLb(context.Background(), "tx-123", "dns")

	require.NoError(t, err)
}

func TestUDPLBOperations_DeleteUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

// =============================================================================
// UDP LB ACL Tests (v3.2+ only)
// =============================================================================

func TestUDPLBOperations_GetAllACLsUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/acls": jsonResponse(`[{"acl_name": "is_dns"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	acls, err := udp.GetAllACLsUDPLb(context.Background(), "tx-123", "dns")

	require.NoError(t, err)
	assert.Len(t, acls, 1)
}

func TestUDPLBOperations_GetAllACLsUDPLb_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllACLsUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUDPLBACLsRequiresV32))
}

func TestUDPLBOperations_GetAllACLsUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllACLsUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
}

func TestUDPLBOperations_CreateACLUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/acls/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	acl := &ACL{}
	err := udp.CreateACLUDPLb(context.Background(), "tx-123", "dns", 0, acl)

	require.NoError(t, err)
}

func TestUDPLBOperations_CreateACLUDPLb_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	acl := &ACL{}
	err := udp.CreateACLUDPLb(context.Background(), "tx-123", "dns", 0, acl)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUDPLBACLsRequiresV32))
}

func TestUDPLBOperations_DeleteACLUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/acls/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteACLUDPLb(context.Background(), "tx-123", "dns", 0)

	require.NoError(t, err)
}

func TestUDPLBOperations_DeleteACLUDPLb_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteACLUDPLb(context.Background(), "tx-123", "dns", 0)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUDPLBACLsRequiresV32))
}

// =============================================================================
// UDP LB Dgram Bind Tests
// =============================================================================

func TestUDPLBOperations_GetAllDgramBindsUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/dgram_binds": jsonResponse(`[{"name": "udp-bind"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	binds, err := udp.GetAllDgramBindsUDPLb(context.Background(), "tx-123", "dns")

	require.NoError(t, err)
	assert.Len(t, binds, 1)
}

func TestUDPLBOperations_GetAllDgramBindsUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/dgram_binds": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllDgramBindsUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestUDPLBOperations_GetAllDgramBindsUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllDgramBindsUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestUDPLBOperations_CreateDgramBindUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/dgram_binds": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	bind := &DgramBind{}
	err := udp.CreateDgramBindUDPLb(context.Background(), "tx-123", "dns", bind)

	require.NoError(t, err)
}

func TestUDPLBOperations_CreateDgramBindUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/dgram_binds": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	bind := &DgramBind{}
	err := udp.CreateDgramBindUDPLb(context.Background(), "tx-123", "dns", bind)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestUDPLBOperations_DeleteDgramBindUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/dgram_binds/bind1": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "bind1"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteDgramBindUDPLb(context.Background(), "tx-123", "dns", "bind1")

	require.NoError(t, err)
}

func TestUDPLBOperations_DeleteDgramBindUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/dgram_binds/bind1": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: errorResponse(http.StatusNotFound),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteDgramBindUDPLb(context.Background(), "tx-123", "dns", "bind1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

// =============================================================================
// UDP LB Log Target Tests
// =============================================================================

func TestUDPLBOperations_GetAllLogTargetsUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/log_targets": jsonResponse(`[{"index": 0}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	targets, err := udp.GetAllLogTargetsUDPLb(context.Background(), "tx-123", "dns")

	require.NoError(t, err)
	assert.Len(t, targets, 1)
}

func TestUDPLBOperations_GetAllLogTargetsUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/log_targets": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllLogTargetsUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestUDPLBOperations_GetAllLogTargetsUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllLogTargetsUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestUDPLBOperations_CreateLogTargetUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/log_targets/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	target := &LogTarget{}
	err := udp.CreateLogTargetUDPLb(context.Background(), "tx-123", "dns", 0, target)

	require.NoError(t, err)
}

func TestUDPLBOperations_CreateLogTargetUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/log_targets/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	target := &LogTarget{}
	err := udp.CreateLogTargetUDPLb(context.Background(), "tx-123", "dns", 0, target)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestUDPLBOperations_DeleteLogTargetUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/log_targets/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteLogTargetUDPLb(context.Background(), "tx-123", "dns", 0)

	require.NoError(t, err)
}

func TestUDPLBOperations_DeleteLogTargetUDPLb_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/log_targets/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: errorResponse(http.StatusNotFound),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteLogTargetUDPLb(context.Background(), "tx-123", "dns", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

// =============================================================================
// UDP LB Server Switching Rule Tests (v3.2+ only)
// =============================================================================

func TestUDPLBOperations_GetAllServerSwitchingRulesUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/server_switching_rules": jsonResponse(`[{"index": 0}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	rules, err := udp.GetAllServerSwitchingRulesUDPLb(context.Background(), "tx-123", "dns")

	require.NoError(t, err)
	assert.Len(t, rules, 1)
}

func TestUDPLBOperations_GetAllServerSwitchingRulesUDPLb_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllServerSwitchingRulesUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUDPLBServerSwitchingRequiresV32))
}

func TestUDPLBOperations_GetAllServerSwitchingRulesUDPLb_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	_, err := udp.GetAllServerSwitchingRulesUDPLb(context.Background(), "tx-123", "dns")

	require.Error(t, err)
}

func TestUDPLBOperations_CreateServerSwitchingRuleUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/server_switching_rules/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	rule := &ServerSwitchingRule{}
	err := udp.CreateServerSwitchingRuleUDPLb(context.Background(), "tx-123", "dns", 0, rule)

	require.NoError(t, err)
}

func TestUDPLBOperations_CreateServerSwitchingRuleUDPLb_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	rule := &ServerSwitchingRule{}
	err := udp.CreateServerSwitchingRuleUDPLb(context.Background(), "tx-123", "dns", 0, rule)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUDPLBServerSwitchingRequiresV32))
}

func TestUDPLBOperations_DeleteServerSwitchingRuleUDPLb_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/udp_lbs/dns/server_switching_rules/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteServerSwitchingRuleUDPLb(context.Background(), "tx-123", "dns", 0)

	require.NoError(t, err)
}

func TestUDPLBOperations_DeleteServerSwitchingRuleUDPLb_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	udp := NewUDPLBOperations(c)

	err := udp.DeleteServerSwitchingRuleUDPLb(context.Background(), "tx-123", "dns", 0)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUDPLBServerSwitchingRequiresV32))
}
