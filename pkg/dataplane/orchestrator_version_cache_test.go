package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func (m *mockConfigParser) ParseFromStringFor(_, config string) (*parserconfig.StructuredConfig, error) {
	return m.ParseFromString(config)
}

func (m *mockConfigParser) ParseFromStringUncachedFor(_, config string) (*parserconfig.StructuredConfig, error) {
	return m.ParseFromString(config)
}

func (m *mockConfigParser) ParseFromStringUncached(config string) (*parserconfig.StructuredConfig, error) {
	return m.ParseFromString(config)
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
		CachedCurrentConfig:         cachedConfig,
		CachedConfigVersion:         42,
		CachedCurrentConfigChecksum: "cached-proof",
		LastActivatedConfigChecksum: "cached-proof",
	}

	configStr, preParsedCurrent, preCachedVersion, currentChecksum, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Empty(t, configStr, "config string should be empty on cache hit")
	assert.Same(t, cachedConfig, preParsedCurrent, "should return the cached config pointer")
	assert.Equal(t, int64(42), preCachedVersion)
	assert.Equal(t, "cached-proof", currentChecksum)
	assert.Equal(t, int32(0), rawConfigCalls.Load(), "GetRawConfiguration should not be called on cache hit")
}

func TestFetchCurrentConfig_UnprovenCacheForcesRawFetch(t *testing.T) {
	tests := []struct {
		name     string
		checksum string
		proof    string
	}{
		{name: "missing current checksum", proof: "proof"},
		{name: "missing activation proof", checksum: "current"},
		{name: "mismatched activation proof", checksum: "current", proof: "other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var versionCalls atomic.Int32
			var rawCalls atomic.Int32
			const raw = "global\n  daemon\n"
			orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
				if v3InfoResponse(w, r) {
					return
				}
				switch r.URL.Path {
				case "/services/haproxy/configuration/version":
					versionCalls.Add(1)
					fmt.Fprint(w, "42")
				case "/services/haproxy/configuration/raw":
					rawCalls.Add(1)
					fmt.Fprint(w, raw)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}, &mockConfigParser{})
			defer cleanup()

			configStr, parsed, version, checksum, err := orch.fetchCurrentConfig(context.Background(), &SyncOptions{
				CachedCurrentConfig:         &parserconfig.StructuredConfig{},
				CachedConfigVersion:         42,
				CachedCurrentConfigChecksum: test.checksum,
				LastActivatedConfigChecksum: test.proof,
			})

			require.NoError(t, err)
			assert.Equal(t, raw, configStr)
			assert.Nil(t, parsed)
			assert.Equal(t, int64(-1), version)
			assert.Equal(t, activationChecksum(raw), checksum)
			assert.Zero(t, versionCalls.Load())
			assert.Equal(t, int32(1), rawCalls.Load())
		})
	}
}

func TestFetchCurrentConfig_InvalidCachedVersionForcesRawFetch(t *testing.T) {
	for _, cachedVersion := range []int64{-1, 0, headerlessConfigVersion} {
		t.Run(fmt.Sprintf("version_%d", cachedVersion), func(t *testing.T) {
			var versionCalls atomic.Int32
			var rawCalls atomic.Int32
			const raw = "global\n  daemon\n"
			orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
				if v3InfoResponse(w, r) {
					return
				}
				switch r.URL.Path {
				case "/services/haproxy/configuration/version":
					versionCalls.Add(1)
					fmt.Fprint(w, cachedVersion)
				case "/services/haproxy/configuration/raw":
					rawCalls.Add(1)
					fmt.Fprint(w, raw)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}, &mockConfigParser{})
			defer cleanup()

			configStr, parsed, version, checksum, err := orch.fetchCurrentConfig(context.Background(), &SyncOptions{
				CachedCurrentConfig:         &parserconfig.StructuredConfig{},
				CachedConfigVersion:         cachedVersion,
				CachedCurrentConfigChecksum: "proof",
				LastActivatedConfigChecksum: "proof",
			})

			require.NoError(t, err)
			assert.Equal(t, raw, configStr)
			assert.Nil(t, parsed)
			assert.Equal(t, int64(-1), version)
			assert.Equal(t, activationChecksum(raw), checksum)
			assert.Zero(t, versionCalls.Load())
			assert.Equal(t, int32(1), rawCalls.Load())
		})
	}
}

func TestSync_ProvenCacheHitNeedsNoRawFetchOrPush(t *testing.T) {
	var rawCalls atomic.Int32
	var posts atomic.Int32
	parsed := &parserconfig.StructuredConfig{}
	const proof = "paired-proof"
	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			fmt.Fprint(w, "42")
		case "/services/haproxy/configuration/raw":
			if r.Method == http.MethodPost {
				posts.Add(1)
			} else {
				rawCalls.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	result, err := orch.sync(context.Background(), "unused", &SyncOptions{
		CachedCurrentConfig:         parsed,
		CachedConfigVersion:         42,
		CachedCurrentConfigChecksum: proof,
		LastActivatedConfigChecksum: proof,
		PreParsedConfig:             parsed,
		ContentChecksum:             "content",
		LastDeployedChecksum:        "content",
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, SyncModeNoChanges, result.SyncMode)
	assert.Equal(t, int64(42), result.PostSyncVersion)
	assert.Equal(t, proof, result.ActivatedConfigChecksum)
	assert.Zero(t, rawCalls.Load())
	assert.Zero(t, posts.Load())
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
		CachedCurrentConfig:         cachedConfig,
		CachedConfigVersion:         42,
		CachedCurrentConfigChecksum: "cached-proof",
		LastActivatedConfigChecksum: "cached-proof",
	}

	configStr, preParsedCurrent, preCachedVersion, currentChecksum, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr, "should return raw config on cache miss")
	assert.Nil(t, preParsedCurrent, "preParsedCurrent should be nil on cache miss")
	assert.Equal(t, int64(43), preCachedVersion, "should report the actual pod version")
	assert.Equal(t, activationChecksum(configStr), currentChecksum)
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
		CachedCurrentConfig:         &parserconfig.StructuredConfig{},
		CachedConfigVersion:         42,
		CachedCurrentConfigChecksum: "cached-proof",
		LastActivatedConfigChecksum: "cached-proof",
	}

	configStr, preParsedCurrent, preCachedVersion, currentChecksum, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr, "should fall back to raw config fetch")
	assert.Nil(t, preParsedCurrent, "preParsedCurrent should be nil on version check failure")
	assert.Equal(t, int64(-1), preCachedVersion, "preCachedVersion should remain -1 on failure")
	assert.Equal(t, activationChecksum(configStr), currentChecksum)
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

	configStr, preParsedCurrent, preCachedVersion, currentChecksum, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr)
	assert.Nil(t, preParsedCurrent)
	assert.Equal(t, int64(-1), preCachedVersion, "preCachedVersion should be -1 when no cache used")
	assert.Equal(t, activationChecksum(configStr), currentChecksum)
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
	var versionCalls atomic.Int32

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			versionCalls.Add(1)
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
		CachedCurrentConfig:         cachedConfig,
		CachedConfigVersion:         1, // equal to the pod's sentinel reading
		CachedCurrentConfigChecksum: "cached-proof",
		LastActivatedConfigChecksum: "cached-proof",
	}

	configStr, preParsedCurrent, preCachedVersion, currentChecksum, err := orch.fetchCurrentConfig(context.Background(), opts)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", configStr, "must fetch the actual config despite version equality at 1")
	assert.Nil(t, preParsedCurrent, "must not serve the cached config for the headerless sentinel")
	assert.Equal(t, int64(-1), preCachedVersion)
	assert.Equal(t, activationChecksum(configStr), currentChecksum)
	assert.Zero(t, versionCalls.Load(), "an invalid cached version must bypass the cache check")
	assert.Equal(t, int32(1), rawConfigCalls.Load(), "GetRawConfiguration must be called")
}

// The no-changes path must report real versions (>1) as PostSyncVersion so
// the deployer's version cache keeps working. (Reaching that path at all now
// requires an activation proof matching what is on disk — see
// TestSync_NoDiff_TrustedOnlyWithActivationProof.)
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
		// Matching checksums skip the auxiliary-file comparison, so
		// the equal config diff lands in the no-changes branch.
		ContentChecksum:      "abc",
		LastDeployedChecksum: "abc",
		// Steady state: this pod was already proven to be running exactly what
		// is on disk, which is what makes the empty diff trustworthy.
		LastActivatedConfigChecksum: activationChecksum("# _version=7\nglobal\n  daemon\n"),
	}

	result, err := orch.sync(context.Background(), "global\n  daemon\n", opts, nil)

	require.NoError(t, err)
	require.Equal(t, SyncModeNoChanges, result.SyncMode)
	assert.Equal(t, int64(7), result.PostSyncVersion)
	assert.Equal(t, opts.LastActivatedConfigChecksum, result.ActivatedConfigChecksum,
		"a no-op sync must carry the proof forward, or the next sync reloads against a config it just verified")
}

// An empty diff is trustworthy ONLY against a config this endpoint was proven
// to be running. "Disk == desired" is not that proof: a skip_version push writes
// the body verbatim with no reload, and the dataplane writes it even when the
// runtime actions that accompany it FAIL — so structural content can sit parked
// on disk that no worker ever loaded while the diff reads empty, the deploy
// reports success, and routes stay dead (CI job 15180387459: TCP listeners
// parked 90s, Gateway reported Programmed, every connection refused).
//
// The guard used to key on the `# _version=N` header, which is a proxy for the
// question and answers it wrong in both directions. The versioned-but-unproven
// case below is the hole that proxy left open (#112 item 2): content parked by a
// VERSIONED skip_reload push whose follow-up force_reload failed carries a
// header and would have been trusted.
func TestSync_NoDiff_TrustedOnlyWithActivationProof(t *testing.T) {
	tests := []struct {
		name string
		// opts per case; nil CachedCurrentConfig exercises the header-scan
		// detection path (no GetVersion pre-check).
		useVersionCheck bool
		rawBody         string
		// proven marks the on-disk body as previously activated.
		proven     bool
		wantReload bool
	}{
		{
			name:            "headerless and unproven forces reload",
			useVersionCheck: true,
			rawBody:         "global\n  daemon\n",
			wantReload:      true,
		},
		{
			name:            "headerless via header scan and unproven forces reload",
			useVersionCheck: false,
			rawBody:         "global\n  daemon\n",
			wantReload:      true,
		},
		{
			// The hole the header proxy left open: a versioned skip_reload push
			// whose follow-up force_reload failed leaves parked content that
			// carries a header. A header is not a proof of activation.
			name:            "versioned but unproven forces reload",
			useVersionCheck: false,
			rawBody:         "# _md5hash=abc\n# _version=3\nglobal\n  daemon\n",
			wantReload:      true,
		},
		{
			name:            "versioned and proven stays no-changes",
			useVersionCheck: false,
			rawBody:         "# _md5hash=abc\n# _version=3\nglobal\n  daemon\n",
			proven:          true,
			wantReload:      false,
		},
		{
			// A runtime apply activates a headerless body. The header is
			// irrelevant; the proof is what counts, so this must stay
			// reload-free or every bypass would cost a reload.
			name:            "headerless but proven stays no-changes",
			useVersionCheck: false,
			rawBody:         "global\n  daemon\n",
			proven:          true,
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
			if tt.proven {
				opts.LastActivatedConfigChecksum = activationChecksum(tt.rawBody)
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
					"an empty diff against a config whose activation was never proven must not be trusted")
				assert.True(t, result.ReloadTriggered)
				assert.Equal(t, int32(1), rec.posts.Load(), "the desired config must be pushed")
				assert.Contains(t, rec.lastQuery(), "force_reload=true", "the push must reload to activate parked content")
			} else {
				assert.Equal(t, SyncModeNoChanges, result.SyncMode)
				assert.Equal(t, int32(0), rec.posts.Load(), "a proven config keeps the no-changes path push-free")
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

// Every SUCCESSFUL sync path must return an activation proof.
//
// This is a whole-class guard, not a per-path one, because the failure mode is
// silent and inverted: the deployer writes back whatever it receives, so a path
// that merely FORGETS to set the proof does not lose an optimisation — it
// actively CLEARS the stored one, and the next sync reloads against a config
// that same sync just activated. Reload per sync, forever.
//
// applyRuntimeOnly shipped with exactly that gap (caught in review on !1490) and
// syncRuntimeRawPush's no-actions early return had the same one. Reflection over
// the success paths is cheaper than remembering.
func TestSyncResult_EverySuccessPathCarriesAnActivationProof(t *testing.T) {
	// Documented inventory of the success-producing paths and what each proves.
	// A new success path must be added here deliberately, with a reason.
	paths := []struct {
		name  string
		proof string
		why   string
	}{
		{"applyWithReload", "read-back checksum", "reload verified + read back"},
		{"applyRuntimeOnly", "activationChecksum(desiredConfig)", "versioned skip_reload push, worker took the actions"},
		{"syncRuntimeRawPush", "activationChecksum(body)", "skip_version push, worker took the actions"},
		{"syncRuntimeRawPush/no-actions", "carried from opts", "nothing pushed, prior proof still holds"},
		{"sync/no-diff", "carried from opts", "reached only BECAUSE the proof matched"},
	}
	for _, p := range paths {
		require.NotEmpty(t, p.proof, "%s must record or carry a proof (%s)", p.name, p.why)
	}

	// The real assertion: the source has no success path that omits it.
	src, err := os.ReadFile("orchestrator.go")
	require.NoError(t, err)
	fast, err := os.ReadFile("orchestrator_runtime_fastpath.go")
	require.NoError(t, err)

	for file, content := range map[string]string{
		"orchestrator.go":                  string(src),
		"orchestrator_runtime_fastpath.go": string(fast),
	} {
		for _, block := range strings.Split(content, "return &SyncResult{")[1:] {
			head := block
			if i := strings.Index(block, "}, nil"); i >= 0 {
				head = block[:i]
			}
			if !strings.Contains(head, "Success:           true") {
				continue
			}
			assert.Contains(t, head, "ActivatedConfigChecksum",
				"a Success:true SyncResult in %s omits ActivatedConfigChecksum — that CLEARS "+
					"the stored proof and forces a reload on the next sync", file)
		}
	}
}
