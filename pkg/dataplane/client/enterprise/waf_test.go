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
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

func TestNewWAFOperations(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	require.NotNil(t, waf)
	assert.Equal(t, c, waf.client)
}

// =============================================================================
// WAF Global Tests (v3.2+ only)
// =============================================================================

func TestWAFOperations_GetGlobal_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": jsonResponse(`{"enabled": true}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	global, err := waf.GetGlobal(context.Background(), "tx-123")

	require.NoError(t, err)
	require.NotNil(t, global)
}

func TestWAFOperations_GetGlobal_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetGlobal(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestWAFOperations_GetGlobal_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetGlobal(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestWAFOperations_GetGlobal_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetGlobal(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWAFGlobalRequiresV32))
}

func TestWAFOperations_GetGlobal_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetGlobal(context.Background(), "tx-123")

	require.Error(t, err)
	// Community edition fails before version check
}

func TestWAFOperations_CreateGlobal_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	global := &WafGlobal{}
	err := waf.CreateGlobal(context.Background(), "tx-123", global)

	require.NoError(t, err)
}

func TestWAFOperations_CreateGlobal_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	global := &WafGlobal{}
	err := waf.CreateGlobal(context.Background(), "tx-123", global)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWAFGlobalRequiresV32))
}

func TestWAFOperations_ReplaceGlobal_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{}`),
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	global := &WafGlobal{}
	err := waf.ReplaceGlobal(context.Background(), "tx-123", global)

	require.NoError(t, err)
}

func TestWAFOperations_DeleteGlobal_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	err := waf.DeleteGlobal(context.Background(), "tx-123")

	require.NoError(t, err)
}

// =============================================================================
// WAF Profile Tests (v3.2+ only)
// =============================================================================

func TestWAFOperations_GetAllProfiles_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles": jsonResponse(`[{"name": "default"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	profiles, err := waf.GetAllProfiles(context.Background(), "tx-123")

	require.NoError(t, err)
	assert.Len(t, profiles, 1)
}

func TestWAFOperations_GetAllProfiles_RequiresV32(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: "v3.1.0-ee1",
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetAllProfiles(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWAFProfilesRequiresV32))
}

func TestWAFOperations_GetProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles/default": jsonResponse(`{"name": "default"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	profile, err := waf.GetProfile(context.Background(), "tx-123", "default")

	require.NoError(t, err)
	require.NotNil(t, profile)
}

func TestWAFOperations_GetProfile_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetProfile(context.Background(), "tx-123", "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestWAFOperations_CreateProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	profile := &WafProfile{}
	err := waf.CreateProfile(context.Background(), "tx-123", profile)

	require.NoError(t, err)
}

func TestWAFOperations_ReplaceProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles/default": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "default"}`),
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	profile := &WafProfile{}
	err := waf.ReplaceProfile(context.Background(), "tx-123", "default", profile)

	require.NoError(t, err)
}

func TestWAFOperations_DeleteProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles/default": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "default"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	err := waf.DeleteProfile(context.Background(), "tx-123", "default")

	require.NoError(t, err)
}

// =============================================================================
// WAF Body Rules Backend Tests
// =============================================================================

func TestWAFOperations_GetAllBodyRulesBackend_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/api/waf_body_rules": jsonResponse(`[{"index": 0}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	rules, err := waf.GetAllBodyRulesBackend(context.Background(), "tx-123", "api")

	require.NoError(t, err)
	assert.Len(t, rules, 1)
}

func TestWAFOperations_GetAllBodyRulesBackend_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/api/waf_body_rules": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetAllBodyRulesBackend(context.Background(), "tx-123", "api")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestWAFOperations_GetAllBodyRulesBackend_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetAllBodyRulesBackend(context.Background(), "tx-123", "api")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestWAFOperations_CreateBodyRuleBackend_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/api/waf_body_rules/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	rule := &WafBodyRule{}
	err := waf.CreateBodyRuleBackend(context.Background(), "tx-123", "api", 0, rule)

	require.NoError(t, err)
}

func TestWAFOperations_DeleteBodyRuleBackend_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/backends/api/waf_body_rules/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	err := waf.DeleteBodyRuleBackend(context.Background(), "tx-123", "api", 0)

	require.NoError(t, err)
}

// =============================================================================
// WAF Body Rules Frontend Tests
// =============================================================================

func TestWAFOperations_GetAllBodyRulesFrontend_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/frontends/http/waf_body_rules": jsonResponse(`[{"index": 0}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	rules, err := waf.GetAllBodyRulesFrontend(context.Background(), "tx-123", "http")

	require.NoError(t, err)
	assert.Len(t, rules, 1)
}

func TestWAFOperations_CreateBodyRuleFrontend_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/frontends/http/waf_body_rules/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	rule := &WafBodyRule{}
	err := waf.CreateBodyRuleFrontend(context.Background(), "tx-123", "http", 0, rule)

	require.NoError(t, err)
}

func TestWAFOperations_DeleteBodyRuleFrontend_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/frontends/http/waf_body_rules/0": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"index": 0}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	err := waf.DeleteBodyRuleFrontend(context.Background(), "tx-123", "http", 0)

	require.NoError(t, err)
}

// =============================================================================
// WAF Ruleset Tests
// =============================================================================

func TestWAFOperations_GetAllRulesets_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets": jsonResponse(`[{"name": "owasp"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	rulesets, err := waf.GetAllRulesets(context.Background())

	require.NoError(t, err)
	assert.Len(t, rulesets, 1)
}

func TestWAFOperations_GetAllRulesets_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetAllRulesets(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestWAFOperations_GetAllRulesets_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetAllRulesets(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestWAFOperations_GetRuleset_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp": jsonResponse(`{"name": "owasp"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	ruleset, err := waf.GetRuleset(context.Background(), "owasp")

	require.NoError(t, err)
	require.NotNil(t, ruleset)
}

func TestWAFOperations_GetRuleset_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetRuleset(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestWAFOperations_CreateRuleset_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	content := bytes.NewBufferString("ruleset content")
	err := waf.CreateRuleset(context.Background(), content)

	require.NoError(t, err)
}

func TestWAFOperations_DeleteRuleset_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "owasp"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	err := waf.DeleteRuleset(context.Background(), "owasp")

	require.NoError(t, err)
}

// =============================================================================
// WAF Ruleset File Tests
// =============================================================================

func TestWAFOperations_GetAllRulesetFiles_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp/files": jsonResponse(`["rule1.conf", "rule2.conf"]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	files, err := waf.GetAllRulesetFiles(context.Background(), "owasp", "")

	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestWAFOperations_GetRulesetFile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp/files/rule1.conf": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("rule content"))
			},
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	content, err := waf.GetRulesetFile(context.Background(), "owasp", "rule1.conf", "")

	require.NoError(t, err)
	assert.Equal(t, []byte("rule content"), content)
}

func TestWAFOperations_GetRulesetFile_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp/files/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	_, err := waf.GetRulesetFile(context.Background(), "owasp", "nonexistent", "")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestWAFOperations_CreateRulesetFile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp/files": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	content := bytes.NewBufferString("rule content")
	err := waf.CreateRulesetFile(context.Background(), "owasp", "", content)

	require.NoError(t, err)
}

func TestWAFOperations_DeleteRulesetFile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/waf/rulesets/owasp/files/rule1.conf": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	waf := NewWAFOperations(c)

	err := waf.DeleteRulesetFile(context.Background(), "owasp", "rule1.conf", "")

	require.NoError(t, err)
}
