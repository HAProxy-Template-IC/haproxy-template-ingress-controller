package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
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

// After a runtime-bypass apply (skip_version push) the pod's config has no
// `# _version` header and GetVersion reads it as 1 — for ANY body. A cached
// entry at version 1 must therefore never satisfy the cache check: the cached
// body and the pod's actual body can differ while both read as 1, and a false
// hit would let the comparator no-op against a stale baseline (permanent
// drift that even drift-prevention deploys can't correct).
func TestFetchCurrentConfig_HeaderlessSentinelForcesFetch(t *testing.T) {
	var rawConfigCalls atomic.Int32

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "1") // headerless sentinel
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
		CachedConfigVersion: 1, // equal to the pod's sentinel reading
	}

	configStr, preParsedCurrent, preCachedVersion, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr, "must fetch the actual config despite version equality at 1")
	assert.Nil(t, preParsedCurrent, "must not serve the cached config for the headerless sentinel")
	assert.Equal(t, int64(1), preCachedVersion)
	assert.Equal(t, int32(1), rawConfigCalls.Load(), "GetRawConfiguration must be called")
}

// The no-changes path must report real versions (>1) as PostSyncVersion so
// the deployer's version cache keeps working. (The headerless sentinel never
// reaches the no-changes result anymore: sync() force-reloads instead of
// trusting an empty diff against a headerless config — see
// TestSync_HeaderlessNoDiff_ForcesReload.)
func TestSync_NoChanges_ReportsRealPostSyncVersion(t *testing.T) {
	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "7")
		case "/services/haproxy/configuration/raw":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "# _version=7\nglobal\n  daemon\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	opts := &SyncOptions{
		// Cached config present but at a non-matching version so the
		// sync takes the full-fetch path and reaches the no-changes
		// branch with preCachedVersion = the pod's reading.
		CachedCurrentConfig: &parserconfig.StructuredConfig{},
		CachedConfigVersion: 99,
		// Matching checksums skip the auxiliary-file comparison, so
		// the equal config diff lands in the no-changes branch.
		ContentChecksum:      "abc",
		LastDeployedChecksum: "abc",
	}

	result, err := orch.sync(context.Background(), "global\n  daemon\n", opts, nil)

	require.NoError(t, err)
	require.Equal(t, SyncModeNoChanges, result.SyncMode)
	assert.Equal(t, int64(7), result.PostSyncVersion)
}

// A headerless on-disk config (no `# _version=N` header — the last write was a
// skip_version push) must never satisfy sync() as "no changes": the dataplane
// writes skip_version bodies to disk without a reload (even when their runtime
// actions FAIL), so the running worker may never have loaded what the fetch
// returns. Trusting the empty diff parks that content indefinitely while the
// deploy reports success (CI job 15180387459: new TCP listeners sat on disk for
// 90s, the Gateway was reported Programmed, and every connection was refused).
// sync() must instead push the desired config WITH a reload — which also
// re-stamps the version header, so the next sync is trustworthy again.
func TestSync_HeaderlessNoDiff_ForcesReload(t *testing.T) {
	tests := []struct {
		name string
		// opts per case; nil CachedCurrentConfig exercises the header-scan
		// detection path (no GetVersion pre-check).
		useVersionCheck bool
		rawBody         string
		wantReload      bool
	}{
		{
			name:            "headerless via GetVersion sentinel forces reload",
			useVersionCheck: true,
			rawBody:         "global\n  daemon\n",
			wantReload:      true,
		},
		{
			name:            "headerless via header scan (no version pre-check) forces reload",
			useVersionCheck: false,
			rawBody:         "global\n  daemon\n",
			wantReload:      true,
		},
		{
			name:            "versioned header via header scan stays no-changes",
			useVersionCheck: false,
			rawBody:         "# _md5hash=abc\n# _version=3\nglobal\n  daemon\n",
			wantReload:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &configPostRecorder{}
			orch, cleanup := createTestOrchestratorWithParser(t, parkedConfigHandler(rec, tt.rawBody), &mockConfigParser{})
			defer cleanup()

			opts := &SyncOptions{
				// Matching checksums skip the auxiliary-file comparison, so
				// the equal config diff reaches the no-changes decision point.
				ContentChecksum:      "abc",
				LastDeployedChecksum: "abc",
			}
			if tt.useVersionCheck {
				// Non-matching cached version: the sentinel forces the full
				// fetch and preCachedVersion carries the pod's reading (1).
				opts.CachedCurrentConfig = &parserconfig.StructuredConfig{}
				opts.CachedConfigVersion = 99
			}

			result, err := orch.sync(context.Background(), "global\n  daemon\n", opts, nil)
			require.NoError(t, err)

			if tt.wantReload {
				assert.Equal(t, SyncModeReload, result.SyncMode,
					"an empty diff against a headerless config must not be trusted")
				assert.True(t, result.ReloadTriggered)
				assert.Equal(t, int32(1), rec.posts.Load(), "the desired config must be pushed")
				assert.Contains(t, rec.lastQuery(), "force_reload=true", "the push must reload to activate parked content")
			} else {
				assert.Equal(t, SyncModeNoChanges, result.SyncMode)
				assert.Equal(t, int32(0), rec.posts.Load(), "a versioned header keeps the no-changes path push-free")
			}
		})
	}
}

// configPostRecorder counts raw-config POSTs and records the last query string.
type configPostRecorder struct {
	posts atomic.Int32
	mu    sync.Mutex
	query string
}

func (r *configPostRecorder) record(req *http.Request) {
	r.posts.Add(1)
	r.mu.Lock()
	r.query = req.URL.RawQuery
	r.mu.Unlock()
}

func (r *configPostRecorder) lastQuery() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.query
}

// parkedConfigHandler simulates a pod whose GetVersion reads the headerless
// sentinel and whose on-disk config is rawBody; raw-config POSTs are recorded
// on rec and answered with a synchronous 200 (reload already finished).
func parkedConfigHandler(rec *configPostRecorder, rawBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "1") // headerless sentinel
		case "/services/haproxy/configuration/raw":
			if r.Method == http.MethodPost {
				rec.record(r)
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, rawBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}
