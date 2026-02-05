package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// mockConfigParser implements ConfigParser for testing.
type mockConfigParser struct {
	parseCalled atomic.Int32
	parseFunc   func(config string) (*parserconfig.StructuredConfig, error)
}

func (m *mockConfigParser) ParseFromString(config string) (*parserconfig.StructuredConfig, error) {
	m.parseCalled.Add(1)
	if m.parseFunc != nil {
		return m.parseFunc(config)
	}
	return &parserconfig.StructuredConfig{}, nil
}

// createTestOrchestratorWithParser creates an orchestrator backed by a mock HTTP server.
func createTestOrchestratorWithParser(t *testing.T, handler http.HandlerFunc, p ConfigParser) (orch *orchestrator, cleanup func()) {
	t.Helper()

	server := httptest.NewServer(handler)

	c, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	return &orchestrator{
		client:     c,
		parser:     p,
		comparator: comparator.New(),
		logger:     slog.Default(),
	}, server.Close
}

// v3InfoResponse writes the standard /v3/info response for client initialization.
// Returns true if the request was handled.
func v3InfoResponse(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/v3/info" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		return true
	}
	return false
}

func TestFetchCurrentConfig_CacheHit(t *testing.T) {
	var rawConfigCalls atomic.Int32

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "42")
		case "/services/haproxy/configuration/raw":
			rawConfigCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "should-not-be-fetched")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	cachedConfig := &parserconfig.StructuredConfig{}
	opts := &SyncOptions{
		CachedCurrentConfig: cachedConfig,
		CachedConfigVersion: 42,
	}

	configStr, preParsedCurrent, preCachedVersion, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Empty(t, configStr, "config string should be empty on cache hit")
	assert.Same(t, cachedConfig, preParsedCurrent, "should return the cached config pointer")
	assert.Equal(t, int64(42), preCachedVersion)
	assert.Equal(t, int32(0), rawConfigCalls.Load(), "GetRawConfiguration should not be called on cache hit")
}

func TestFetchCurrentConfig_CacheMiss(t *testing.T) {
	var rawConfigCalls atomic.Int32

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "43") // Different from cached version 42
		case "/services/haproxy/configuration/raw":
			rawConfigCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "global\n  daemon\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	cachedConfig := &parserconfig.StructuredConfig{}
	opts := &SyncOptions{
		CachedCurrentConfig: cachedConfig,
		CachedConfigVersion: 42,
	}

	configStr, preParsedCurrent, preCachedVersion, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr, "should return raw config on cache miss")
	assert.Nil(t, preParsedCurrent, "preParsedCurrent should be nil on cache miss")
	assert.Equal(t, int64(43), preCachedVersion, "should report the actual pod version")
	assert.Equal(t, int32(1), rawConfigCalls.Load(), "GetRawConfiguration should be called on cache miss")
}

func TestFetchCurrentConfig_GetVersionFailure(t *testing.T) {
	var rawConfigCalls atomic.Int32

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusInternalServerError)
		case "/services/haproxy/configuration/raw":
			rawConfigCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "global\n  daemon\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	opts := &SyncOptions{
		CachedCurrentConfig: &parserconfig.StructuredConfig{},
		CachedConfigVersion: 42,
	}

	configStr, preParsedCurrent, preCachedVersion, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr, "should fall back to raw config fetch")
	assert.Nil(t, preParsedCurrent, "preParsedCurrent should be nil on version check failure")
	assert.Equal(t, int64(-1), preCachedVersion, "preCachedVersion should remain -1 on failure")
	assert.Equal(t, int32(1), rawConfigCalls.Load(), "GetRawConfiguration should be called as fallback")
}

func TestFetchCurrentConfig_NoCachedConfig(t *testing.T) {
	var versionCalls, rawConfigCalls atomic.Int32

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			versionCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "42")
		case "/services/haproxy/configuration/raw":
			rawConfigCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "global\n  daemon\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	opts := &SyncOptions{
		// No CachedCurrentConfig — version check should be skipped entirely
	}

	configStr, preParsedCurrent, preCachedVersion, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr)
	assert.Nil(t, preParsedCurrent)
	assert.Equal(t, int64(-1), preCachedVersion, "preCachedVersion should be -1 when no cache used")
	assert.Equal(t, int32(0), versionCalls.Load(), "GetVersion should not be called when no cached config")
	assert.Equal(t, int32(1), rawConfigCalls.Load(), "GetRawConfiguration should be called directly")
}

func TestParseAndCompareConfigs_UsesPreParsedCurrent(t *testing.T) {
	mockParser := &mockConfigParser{
		parseFunc: func(config string) (*parserconfig.StructuredConfig, error) {
			return &parserconfig.StructuredConfig{}, nil
		},
	}

	orch := &orchestrator{
		parser:     mockParser,
		comparator: comparator.New(),
		logger:     slog.Default(),
	}

	preParsedCurrent := &parserconfig.StructuredConfig{}
	preParsedDesired := &parserconfig.StructuredConfig{}

	diff, err := orch.parseAndCompareConfigs("unused-current", "unused-desired", preParsedDesired, preParsedCurrent)

	require.NoError(t, err)
	require.NotNil(t, diff)
	assert.Equal(t, int32(0), mockParser.parseCalled.Load(),
		"parser should not be called when both configs are pre-parsed")
}

func TestParseAndCompareConfigs_ParsesDesiredWhenOnlyCurrentPreParsed(t *testing.T) {
	mockParser := &mockConfigParser{
		parseFunc: func(config string) (*parserconfig.StructuredConfig, error) {
			return &parserconfig.StructuredConfig{}, nil
		},
	}

	orch := &orchestrator{
		parser:     mockParser,
		comparator: comparator.New(),
		logger:     slog.Default(),
	}

	preParsedCurrent := &parserconfig.StructuredConfig{}

	diff, err := orch.parseAndCompareConfigs("unused-current", "desired-config", nil, preParsedCurrent)

	require.NoError(t, err)
	require.NotNil(t, diff)
	assert.Equal(t, int32(1), mockParser.parseCalled.Load(),
		"parser should be called once for desired config only")
}
