package executors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

type mockServerConfig struct {
	apiVersion string
	handlers   map[string]http.HandlerFunc
}

func newMockServer(t *testing.T, cfg mockServerConfig) *httptest.Server {
	t.Helper()
	return testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: cfg.apiVersion,
		Handlers:   cfg.handlers,
	})
}

func newTestClient(t *testing.T, server *httptest.Server) *client.DataplaneClient {
	t.Helper()
	return testutil.NewTestClient(t, server)
}

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

func TestBotMgmtProfileCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": testutil.StatusResponse(http.StatusCreated),
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
			"/v3/services/haproxy/configuration/botmgmt_profiles/test": testutil.StatusResponse(http.StatusOK),
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
			"/v3/services/haproxy/configuration/botmgmt_profiles/test": testutil.StatusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := BotMgmtProfileDelete()

	err := executor(context.Background(), c, "tx-123", nil, "test")

	require.NoError(t, err)
}

func TestCaptchaCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/captchas": testutil.StatusResponse(http.StatusCreated),
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
			"/v3/services/haproxy/configuration/captchas/recaptcha": testutil.StatusResponse(http.StatusOK),
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
			"/v3/services/haproxy/configuration/captchas/recaptcha": testutil.StatusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := CaptchaDelete()

	err := executor(context.Background(), c, "tx-123", nil, "recaptcha")

	require.NoError(t, err)
}

func TestWAFProfileCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_profiles": testutil.StatusResponse(http.StatusCreated),
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
			"/v3/services/haproxy/configuration/waf_profiles/waf-default": testutil.StatusResponse(http.StatusOK),
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
			"/v3/services/haproxy/configuration/waf_profiles/waf-default": testutil.StatusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFProfileDelete()

	err := executor(context.Background(), c, "tx-123", nil, "waf-default")

	require.NoError(t, err)
}

func TestWAFGlobalCreate_Success(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/waf_global": testutil.StatusResponse(http.StatusCreated),
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
			"/v3/services/haproxy/configuration/waf_global": testutil.StatusResponse(http.StatusOK),
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
			"/v3/services/haproxy/configuration/waf_global": testutil.StatusResponse(http.StatusNoContent),
		},
	})
	defer server.Close()

	c := newTestClient(t, server)
	executor := WAFGlobalDelete()

	err := executor(context.Background(), c, "tx-123", nil)

	require.NoError(t, err)
}

func TestBotMgmtProfileCreate_ServerError(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/v3/services/haproxy/configuration/botmgmt_profiles": testutil.StatusResponse(http.StatusInternalServerError),
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
			"/v3/services/haproxy/configuration/captchas": testutil.StatusResponse(http.StatusBadRequest),
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
			"/v3/services/haproxy/configuration/waf_profiles": testutil.StatusResponse(http.StatusConflict),
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
			"/v3/services/haproxy/configuration/waf_global": testutil.StatusResponse(http.StatusUnprocessableEntity),
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
