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

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
)

func TestNewGitOperations(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	require.NotNil(t, git)
	assert.Equal(t, c, git.client)
}

// =============================================================================
// Git Settings Tests
// =============================================================================

func TestGitOperations_GetSettings_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/settings": jsonResponse(`{"enabled": true, "remote_url": "git@example.com:repo.git"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	settings, err := git.GetSettings(context.Background())

	require.NoError(t, err)
	require.NotNil(t, settings)
}

func TestGitOperations_GetSettings_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/settings": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetSettings(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestGitOperations_GetSettings_InvalidJSON(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/settings": jsonResponse(`{invalid`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetSettings(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestGitOperations_GetSettings_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetSettings(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestGitOperations_ReplaceSettings_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/settings": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{}`),
				http.MethodPut: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	settings := &GitSettings{}
	err := git.ReplaceSettings(context.Background(), settings)

	require.NoError(t, err)
}

func TestGitOperations_ReplaceSettings_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/settings": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPut: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	settings := &GitSettings{}
	err := git.ReplaceSettings(context.Background(), settings)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestGitOperations_ReplaceSettings_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	settings := &GitSettings{}
	err := git.ReplaceSettings(context.Background(), settings)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

// =============================================================================
// Git Actions Tests
// =============================================================================

func TestGitOperations_GetAllActions_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions": jsonResponse(`[{"id": "pull", "name": "Pull"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	actions, err := git.GetAllActions(context.Background())

	require.NoError(t, err)
	assert.Len(t, actions, 1)
}

func TestGitOperations_GetAllActions_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetAllActions(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestGitOperations_GetAllActions_InvalidJSON(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions": jsonResponse(`not-array`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetAllActions(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestGitOperations_GetAllActions_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetAllActions(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestGitOperations_GetAction_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions/pull": jsonResponse(`{"id": "pull", "name": "Pull"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	action, err := git.GetAction(context.Background(), "pull")

	require.NoError(t, err)
	require.NotNil(t, action)
}

func TestGitOperations_GetAction_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetAction(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestGitOperations_GetAction_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions/pull": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetAction(context.Background(), "pull")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestGitOperations_GetAction_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	_, err := git.GetAction(context.Background(), "pull")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestGitOperations_ExecuteAction_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: jsonResponse(`{"id": "pull", "status": "completed"}`),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	action := &GitAction{}
	result, err := git.ExecuteAction(context.Background(), action)

	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestGitOperations_ExecuteAction_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/git/actions": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	action := &GitAction{}
	_, err := git.ExecuteAction(context.Background(), action)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestGitOperations_ExecuteAction_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	git := NewGitOperations(c)

	action := &GitAction{}
	_, err := git.ExecuteAction(context.Background(), action)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}
