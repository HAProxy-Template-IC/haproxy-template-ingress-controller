package executors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haptic/pkg/dataplane/client"
	v32ee "haptic/pkg/generated/dataplaneapi/v32ee"
)

// Test helper constants.
const (
	testEnterpriseAPIVersion = "v3.2.6-ee1"
	testCommunityAPIVersion  = "v3.2.6 87ad0bcf"
	testUsername             = "admin"
	testPassword             = "password"
)

// =============================================================================
// Test Helper Functions
// =============================================================================

// mockServerConfig configures a mock Dataplane API server for tests.
type mockServerConfig struct {
	apiVersion string
	handlers   map[string]http.HandlerFunc
}

// newMockServer creates a test server that mimics the Dataplane API.
func newMockServer(t *testing.T, cfg mockServerConfig) *httptest.Server {
	t.Helper()

	apiVersion := cfg.apiVersion
	if apiVersion == "" {
		apiVersion = testEnterpriseAPIVersion
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle /v3/info - always required for client initialization
		if r.URL.Path == "/v3/info" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"api":{"version":"%s"}}`, apiVersion)
			return
		}

		// Check for custom handler - try with and without /v3 prefix
		path := r.URL.Path
		if handler, ok := cfg.handlers[path]; ok {
			handler(w, r)
			return
		}

		// Try adding /v3 prefix if not present
		if len(path) < 3 || path[:3] != "/v3" {
			if handler, ok := cfg.handlers["/v3"+path]; ok {
				handler(w, r)
				return
			}
		}

		// Default: 404
		w.WriteHeader(http.StatusNotFound)
	}))
}

// newTestClient creates a client connected to a test server.
func newTestClient(t *testing.T, server *httptest.Server) *client.DataplaneClient {
	t.Helper()

	c, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: testUsername,
		Password: testPassword,
	})
	require.NoError(t, err, "failed to create test client")
	return c
}

// statusResponse creates a handler that returns a specific status code.
func statusResponse(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}
}

// =============================================================================
// Tests for convertModel helper
// =============================================================================

func TestConvertModel_SameType(t *testing.T) {
	scoreVersion := 2
	src := &v32ee.BotmgmtProfile{
		Name:         "test-profile",
		ScoreVersion: &scoreVersion,
	}

	var dst v32ee.BotmgmtProfile
	err := convertModel(src, &dst)

	require.NoError(t, err)
	assert.Equal(t, "test-profile", dst.Name)
	assert.Equal(t, 2, *dst.ScoreVersion)
}

func TestConvertModel_NilSource(t *testing.T) {
	var dst v32ee.BotmgmtProfile
	err := convertModel(nil, &dst)

	// JSON marshal of nil returns "null", which unmarshal handles
	require.NoError(t, err)
}

func TestConvertModel_WafGlobal(t *testing.T) {
	cache := 1000
	bodyLimit := 5000
	src := &v32ee.WafGlobal{
		AnalyzerCache: &cache,
		BodyLimit:     &bodyLimit,
	}

	var dst v32ee.WafGlobal
	err := convertModel(src, &dst)

	require.NoError(t, err)
	assert.Equal(t, 1000, *dst.AnalyzerCache)
	assert.Equal(t, 5000, *dst.BodyLimit)
}

// =============================================================================
// Tests for Bot Management Profile Executors
// =============================================================================

func TestBotMgmtProfileCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": statusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BotMgmtProfileCreate()

	profile := &v32ee.BotmgmtProfile{Name: "test"}
	err := executor(context.Background(), c, "tx-123", profile, "test")

	require.NoError(t, err)
}

func TestBotMgmtProfileUpdate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles/test": statusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BotMgmtProfileUpdate()

	profile := &v32ee.BotmgmtProfile{Name: "test"}
	err := executor(context.Background(), c, "tx-123", profile, "test")

	require.NoError(t, err)
}

func TestBotMgmtProfileDelete_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles/test": statusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BotMgmtProfileDelete()

	err := executor(context.Background(), c, "tx-123", nil, "test")

	require.NoError(t, err)
}

// =============================================================================
// Tests for Captcha Executors
// =============================================================================

func TestCaptchaCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": statusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := CaptchaCreate()

	captcha := &v32ee.Captcha{Name: "recaptcha"}
	err := executor(context.Background(), c, "tx-123", captcha, "recaptcha")

	require.NoError(t, err)
}

func TestCaptchaUpdate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas/recaptcha": statusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := CaptchaUpdate()

	captcha := &v32ee.Captcha{Name: "recaptcha"}
	err := executor(context.Background(), c, "tx-123", captcha, "recaptcha")

	require.NoError(t, err)
}

func TestCaptchaDelete_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas/recaptcha": statusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := CaptchaDelete()

	err := executor(context.Background(), c, "tx-123", nil, "recaptcha")

	require.NoError(t, err)
}

// =============================================================================
// Tests for WAF Profile Executors (v3.2+ only)
// =============================================================================

func TestWAFProfileCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles": statusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFProfileCreate()

	profile := &v32ee.WafProfile{Name: "waf-default"}
	err := executor(context.Background(), c, "tx-123", profile, "waf-default")

	require.NoError(t, err)
}

func TestWAFProfileUpdate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles/waf-default": statusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFProfileUpdate()

	profile := &v32ee.WafProfile{Name: "waf-default"}
	err := executor(context.Background(), c, "tx-123", profile, "waf-default")

	require.NoError(t, err)
}

func TestWAFProfileDelete_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles/waf-default": statusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFProfileDelete()

	err := executor(context.Background(), c, "tx-123", nil, "waf-default")

	require.NoError(t, err)
}

// =============================================================================
// Tests for WAF Global Executors (v3.2+ only, singleton)
// =============================================================================

func TestWAFGlobalCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": statusResponse(http.StatusCreated),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFGlobalCreate()

	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}
	err := executor(context.Background(), c, "tx-123", wafGlobal)

	require.NoError(t, err)
}

func TestWAFGlobalUpdate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": statusResponse(http.StatusOK),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFGlobalUpdate()

	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}
	err := executor(context.Background(), c, "tx-123", wafGlobal)

	require.NoError(t, err)
}

func TestWAFGlobalDelete_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": statusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFGlobalDelete()

	err := executor(context.Background(), c, "tx-123", nil)

	require.NoError(t, err)
}

// =============================================================================
// Tests for Server Error Handling
// =============================================================================

func TestBotMgmtProfileCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": statusResponse(http.StatusInternalServerError),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BotMgmtProfileCreate()

	profile := &v32ee.BotmgmtProfile{Name: "test"}
	err := executor(context.Background(), c, "tx-123", profile, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestCaptchaCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": statusResponse(http.StatusBadRequest),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := CaptchaCreate()

	captcha := &v32ee.Captcha{Name: "recaptcha"}
	err := executor(context.Background(), c, "tx-123", captcha, "recaptcha")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestWAFProfileCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles": statusResponse(http.StatusConflict),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFProfileCreate()

	profile := &v32ee.WafProfile{Name: "waf-default"}
	err := executor(context.Background(), c, "tx-123", profile, "waf-default")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "409")
}

func TestWAFGlobalUpdate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": statusResponse(http.StatusUnprocessableEntity),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFGlobalUpdate()

	cache := 1000
	wafGlobal := &v32ee.WafGlobal{AnalyzerCache: &cache}
	err := executor(context.Background(), c, "tx-123", wafGlobal)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
}
