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
)

func TestNewBotManagementOperations(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	require.NotNil(t, bot)
	assert.Equal(t, c, bot.client)
}

func TestBotManagementOperations_GetAllProfiles_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": jsonResponse(`[{"name": "default"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	profiles, err := bot.GetAllProfiles(context.Background(), "tx-123")

	require.NoError(t, err)
	assert.Len(t, profiles, 1)
}

func TestBotManagementOperations_GetAllProfiles_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetAllProfiles(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestBotManagementOperations_GetAllProfiles_InvalidJSON(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": jsonResponse(`not-an-array`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetAllProfiles(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestBotManagementOperations_GetAllProfiles_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetAllProfiles(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_GetProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles/default": jsonResponse(`{"name": "default"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	profile, err := bot.GetProfile(context.Background(), "tx-123", "default")

	require.NoError(t, err)
	require.NotNil(t, profile)
}

func TestBotManagementOperations_GetProfile_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetProfile(context.Background(), "tx-123", "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestBotManagementOperations_GetProfile_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetProfile(context.Background(), "tx-123", "default")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_CreateProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	profile := &BotmgmtProfile{}
	err := bot.CreateProfile(context.Background(), "tx-123", profile)

	require.NoError(t, err)
}

func TestBotManagementOperations_CreateProfile_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	profile := &BotmgmtProfile{}
	err := bot.CreateProfile(context.Background(), "tx-123", profile)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestBotManagementOperations_CreateProfile_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	profile := &BotmgmtProfile{}
	err := bot.CreateProfile(context.Background(), "tx-123", profile)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_DeleteProfile_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles/default": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "default"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	err := bot.DeleteProfile(context.Background(), "tx-123", "default")

	require.NoError(t, err)
}

func TestBotManagementOperations_DeleteProfile_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles/default": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	err := bot.DeleteProfile(context.Background(), "tx-123", "default")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestBotManagementOperations_DeleteProfile_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	err := bot.DeleteProfile(context.Background(), "tx-123", "default")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_GetAllCaptchas_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": jsonResponse(`[{"name": "recaptcha"}]`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	captchas, err := bot.GetAllCaptchas(context.Background(), "tx-123")

	require.NoError(t, err)
	assert.Len(t, captchas, 1)
}

func TestBotManagementOperations_GetAllCaptchas_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": errorResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetAllCaptchas(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestBotManagementOperations_GetAllCaptchas_InvalidJSON(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": jsonResponse(`not-valid`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetAllCaptchas(context.Background(), "tx-123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestBotManagementOperations_GetAllCaptchas_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetAllCaptchas(context.Background(), "tx-123")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_GetCaptcha_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas/recaptcha": jsonResponse(`{"name": "recaptcha"}`),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	captcha, err := bot.GetCaptcha(context.Background(), "tx-123", "recaptcha")

	require.NoError(t, err)
	require.NotNil(t, captcha)
}

func TestBotManagementOperations_GetCaptcha_NotFound(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas/nonexistent": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetCaptcha(context.Background(), "tx-123", "nonexistent")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestBotManagementOperations_GetCaptcha_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	_, err := bot.GetCaptcha(context.Background(), "tx-123", "recaptcha")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_CreateCaptcha_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`[]`),
				http.MethodPost: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	captcha := &Captcha{}
	err := bot.CreateCaptcha(context.Background(), "tx-123", captcha)

	require.NoError(t, err)
}

func TestBotManagementOperations_CreateCaptcha_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodPost: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	captcha := &Captcha{}
	err := bot.CreateCaptcha(context.Background(), "tx-123", captcha)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestBotManagementOperations_CreateCaptcha_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	captcha := &Captcha{}
	err := bot.CreateCaptcha(context.Background(), "tx-123", captcha)

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}

func TestBotManagementOperations_DeleteCaptcha_Success(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas/recaptcha": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodGet: jsonResponse(`{"name": "recaptcha"}`),
				http.MethodDelete: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				},
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	err := bot.DeleteCaptcha(context.Background(), "tx-123", "recaptcha")

	require.NoError(t, err)
}

func TestBotManagementOperations_DeleteCaptcha_ServerError(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas/recaptcha": methodAwareHandler(map[string]http.HandlerFunc{
				http.MethodDelete: errorResponse(http.StatusBadRequest),
			}),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	err := bot.DeleteCaptcha(context.Background(), "tx-123", "recaptcha")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
}

func TestBotManagementOperations_DeleteCaptcha_CommunityEdition(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	bot := NewBotManagementOperations(c)

	err := bot.DeleteCaptcha(context.Background(), "tx-123", "recaptcha")

	require.Error(t, err)
	assert.True(t, errors.Is(err, client.ErrEnterpriseRequired))
}
