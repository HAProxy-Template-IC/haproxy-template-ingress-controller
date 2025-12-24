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

func TestNewLoggingOperations(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	require.NotNil(t, logging)
	assert.Equal(t, c, logging.client)
}

// =============================================================================
// Get Log Config Tests
// =============================================================================

func TestLoggingOperations_GetLogConfig_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/logs/config": jsonResponse(`{"enabled": true}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	config, err := logging.GetLogConfig(context.Background())

	require.NoError(t, err)
	require.NotNil(t, config)
}

func TestLoggingOperations_GetLogConfig_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/logs/config": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	_, err := logging.GetLogConfig(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestLoggingOperations_GetLogConfig_InvalidJSON(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/logs/config": jsonResponse(`{not-valid-json`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	_, err := logging.GetLogConfig(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestLoggingOperations_GetLogConfig_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	_, err := logging.GetLogConfig(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

// =============================================================================
// Replace Log Config Tests
// =============================================================================

func TestLoggingOperations_ReplaceLogConfig_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/logs/config": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{}`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	config := &LogConfiguration{}
	err := logging.ReplaceLogConfig(context.Background(), config)

	require.NoError(t, err)
}

func TestLoggingOperations_ReplaceLogConfig_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/logs/config": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	config := &LogConfiguration{}
	err := logging.ReplaceLogConfig(context.Background(), config)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestLoggingOperations_ReplaceLogConfig_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	logging := NewLoggingOperations(c)

	config := &LogConfiguration{}
	err := logging.ReplaceLogConfig(context.Background(), config)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}
