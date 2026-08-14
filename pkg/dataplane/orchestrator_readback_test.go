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

package dataplane

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

const readBackTestBase = `global

defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s

backend api
  default-server check
  server SRV_1 10.0.0.1:8080 enabled
`

const readBackTestExtraBackend = `
backend api2
  default-server check
  server SRV_1 10.9.9.9:8080 enabled
`

// readBackHandler simulates one pod: GetVersion returns version, the FIRST
// raw-config GET returns currentBody (the pre-deploy fetch), every later GET
// returns readBackBody (what the post-reload read-back sees), and raw-config
// POSTs are recorded on rec and answered with a synchronous 200 (reload
// already finished).
func readBackHandler(rec *configPostRecorder, version, currentBody string, readBackBody *atomic.Value) http.HandlerFunc {
	var gets atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, version)
		case "/services/haproxy/configuration/raw":
			if r.Method == http.MethodPost {
				rec.record(r)
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			if gets.Add(1) == 1 {
				fmt.Fprint(w, currentBody)
				return
			}
			fmt.Fprint(w, readBackBody.Load().(string))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// newReadBackOrchestrator builds an orchestrator with the REAL parser against
// the readBackHandler pod.
func newReadBackOrchestrator(t *testing.T, rec *configPostRecorder, version, currentBody string, readBackBody *atomic.Value) (orch *orchestrator, cleanup func()) {
	t.Helper()
	p, err := parser.New()
	require.NoError(t, err)
	return createTestOrchestratorWithParser(t, readBackHandler(rec, version, currentBody, readBackBody), p)
}

// TestSync_HeaderlessRuntimeOnlyDelta_ForcesReload closes issue #84 mode B's
// second half: a HEADERLESS on-disk config means the last write was an
// unverified skip_version push, which the dataplane writes to disk even when
// its runtime actions FAIL — so the file can carry structural content no
// worker ever loaded. The empty-diff branch already force-reloads
// (TestSync_NoDiff_TrustedOnlyWithActivationProof); this pins the runtime/aux-delta
// branch, which previously applied reload-free, stamped the version header
// over the parked content, and reported success while routes 404ed for the
// full 30s until an unrelated reload. A headerless config with ANY delta must
// take the reload path. The versioned control case proves the runtime path
// stays reload-free when the header vouches for the on-disk state.
func TestSync_HeaderlessRuntimeOnlyDelta_ForcesReload(t *testing.T) {
	desired := strings.Replace(readBackTestBase, "10.0.0.1:8080", "10.0.0.2:8080", 1)

	tests := []struct {
		name        string
		version     string // GetVersion reading: "1" = headerless sentinel
		currentBody string
		wantReload  bool
	}{
		{
			name:        "headerless on-disk config forces the reload path",
			version:     "1",
			currentBody: readBackTestBase, // no # _version header
			wantReload:  true,
		},
		{
			name:        "versioned on-disk config keeps the runtime path reload-free",
			version:     "5",
			currentBody: "# _md5hash=abc\n# _version=5\n" + readBackTestBase,
			wantReload:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &configPostRecorder{}
			var readBack atomic.Value
			// The read-back sees the desired body the deploy just pushed
			// (headerless — the handler echoes bodies without a header).
			readBack.Store(desired)
			orch, cleanup := newReadBackOrchestrator(t, rec, tt.version, tt.currentBody, &readBack)
			defer cleanup()

			result, err := orch.sync(context.Background(), desired, DefaultSyncOptions(), DefaultAuxiliaryFiles())
			require.NoError(t, err)
			require.True(t, result.Success)

			if tt.wantReload {
				assert.Equal(t, SyncModeReload, result.SyncMode,
					"a runtime-only delta against a headerless config must not be applied reload-free")
				assert.True(t, result.ReloadTriggered)
				assert.Contains(t, rec.lastQuery(), "force_reload=true",
					"the final push must reload to activate potentially parked skip_version content")
			} else {
				assert.Equal(t, SyncModeRuntime, result.SyncMode,
					"a versioned on-disk config keeps the pure-runtime path (no reload storm)")
				assert.False(t, result.ReloadTriggered)
				assert.NotContains(t, rec.lastQuery(), "force_reload=true")
			}
			if result.SyncMode != SyncModeReload {
				assert.False(t, result.PostSyncConfigMatchesDesired,
					"only a post-reload comparator can prove graph equivalence")
			}
		})
	}
}

// TestApplyWithReload_ReadBackVerdicts pins the post-reload read-back (issue
// #84 mode A): a structural deploy's synchronous 2xx only proves the
// dataplane processed the push — a concurrent skip_version writer can clobber
// the file between the write and the master's re-exec read. After the reload,
// the orchestrator reads the disk back:
//   - byte-identical (modulo the version header) → success;
//   - byte-divergent but only in runtime-eligible server fields → success (a
//     concurrent runtime bypass legitimately patched pod addresses);
//   - STRUCTURALLY divergent → a retryable post_reload_divergence error, so
//     the fast deploy retry redeploys instead of reporting untruthful success.
func TestApplyWithReload_ReadBackVerdicts(t *testing.T) {
	// The deploy pushes desired = base + a NEW backend (structural change).
	desired := readBackTestBase + readBackTestExtraBackend

	tests := []struct {
		name               string
		readBackBody       string
		wantErr            bool
		wantDivergence     bool
		wantMatchesDesired bool
	}{
		{
			name:               "read-back identical to the pushed body succeeds",
			readBackBody:       "# _md5hash=abc\n# _version=6\n" + desired,
			wantMatchesDesired: true,
		},
		{
			name:               "byte-different comparator-equivalent read-back succeeds",
			readBackBody:       "# retained dataplane comment\n" + desired,
			wantMatchesDesired: true,
		},
		{
			name: "runtime-only divergence (concurrent bypass patched an address) succeeds",
			// Headerless (the bypass pushes skip_version) and one server
			// address differs — exactly what a concurrent partial apply writes.
			readBackBody: strings.Replace(desired, "10.0.0.1:8080", "10.0.0.99:8080", 1),
		},
		{
			name: "structural divergence (stale body clobbered the new backend) fails retryably",
			// The clobber restored the pre-deploy body: the new backend the
			// reload was supposed to activate is gone from disk.
			readBackBody:   readBackTestBase,
			wantErr:        true,
			wantDivergence: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &configPostRecorder{}
			var readBack atomic.Value
			readBack.Store(tt.readBackBody)
			orch, cleanup := newReadBackOrchestrator(t, rec, "5", "# _md5hash=abc\n# _version=5\n"+readBackTestBase, &readBack)
			defer cleanup()

			result, err := orch.sync(context.Background(), desired, DefaultSyncOptions(), DefaultAuxiliaryFiles())

			require.Positive(t, rec.posts.Load(), "the structural change must be pushed")
			assert.Contains(t, rec.lastQuery(), "force_reload=true")

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantDivergence, IsPostReloadDivergence(err))
				require.NotNil(t, result, "the failed result still describes the attempted deploy")
				assert.False(t, result.Success)
				assert.True(t, result.ReloadTriggered)
				assert.False(t, result.PostSyncConfigMatchesDesired)
				return
			}
			require.NoError(t, err)
			assert.True(t, result.Success)
			assert.Equal(t, SyncModeReload, result.SyncMode)
			require.NotNil(t, result.PostSyncParsedConfig,
				"the read-back parse feeds the caller's post-sync cache without a second fetch")
			assert.Equal(t, tt.wantMatchesDesired, result.PostSyncConfigMatchesDesired)
		})
	}
}

func TestApplyWithReload_ByteIdentityDoesNotClaimComparatorEquivalence(t *testing.T) {
	desired := readBackTestBase + readBackTestExtraBackend
	p, err := parser.New()
	require.NoError(t, err)
	nonEquivalentDesired, err := p.ParseFromString(desired + strings.Replace(readBackTestExtraBackend, "api2", "api3", 1))
	require.NoError(t, err)

	rec := &configPostRecorder{}
	var readBack atomic.Value
	readBack.Store("# _md5hash=abc\n# _version=6\n" + desired)
	orch, cleanup := newReadBackOrchestrator(t, rec, "5", "# _md5hash=abc\n# _version=5\n"+readBackTestBase, &readBack)
	defer cleanup()

	opts := DefaultSyncOptions()
	opts.PreParsedConfig = nonEquivalentDesired
	result, err := orch.sync(context.Background(), desired, opts, DefaultAuxiliaryFiles())

	require.NoError(t, err)
	require.True(t, result.Success)
	require.NotNil(t, result.PostSyncParsedConfig)
	assert.False(t, result.PostSyncConfigMatchesDesired,
		"matching raw bytes do not prove the supplied parsed desired graph is equivalent")
}

// TestApplyWithReload_ReadBackFetchFailure pins the unknown-state verdict:
// when the post-reload read-back cannot fetch the on-disk config at all, the
// deploy must NOT report success (the pod's state is unverified) — it returns
// a retryable post_reload_readback error, and the fast retry re-syncs.
func TestApplyWithReload_ReadBackFetchFailure(t *testing.T) {
	var gets atomic.Int32
	var mu sync.Mutex
	var lastQuery string
	p, err := parser.New()
	require.NoError(t, err)

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "5")
		case "/services/haproxy/configuration/raw":
			if r.Method == http.MethodPost {
				mu.Lock()
				lastQuery = r.URL.RawQuery
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
				return
			}
			if gets.Add(1) == 1 {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "# _md5hash=abc\n# _version=5\n"+readBackTestBase)
				return
			}
			// Every read-back attempt fails.
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, p)
	defer cleanup()

	result, err := orch.sync(context.Background(), readBackTestBase+readBackTestExtraBackend, DefaultSyncOptions(), DefaultAuxiliaryFiles())

	require.Error(t, err)
	assert.False(t, IsPostReloadDivergence(err), "a fetch failure is unknown state, not a confirmed divergence")
	require.NotNil(t, result)
	assert.False(t, result.Success)
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, lastQuery, "force_reload=true")
}

// TestApplyWithReload_ReadBackTransient5xxRecovers pins the no-amplification
// contract: the dataplane API can briefly 5xx while HAProxy re-execs right
// after a verified reload. A TRANSIENT 5xx on the read-back must be retried
// (not treated as clobber/unknown-state), so the already-successful reload is
// NOT re-triggered by a purely observational read hiccup. One 500 followed by
// a matching body ⇒ the deploy succeeds with exactly one force_reload.
func TestApplyWithReload_ReadBackTransient5xxRecovers(t *testing.T) {
	var gets atomic.Int32
	var mu sync.Mutex
	var reloadPosts int
	p, err := parser.New()
	require.NoError(t, err)

	pushed := readBackTestBase + readBackTestExtraBackend

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch r.URL.Path {
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "5")
		case "/services/haproxy/configuration/raw":
			if r.Method == http.MethodPost {
				mu.Lock()
				if strings.Contains(r.URL.RawQuery, "force_reload=true") {
					reloadPosts++
				}
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
				return
			}
			switch gets.Add(1) {
			case 1:
				// Pre-deploy diff fetch: current == base (no api2 backend).
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, readBackTestBase)
			case 2:
				// First read-back attempt: transient re-exec 5xx.
				w.WriteHeader(http.StatusInternalServerError)
			default:
				// Retry succeeds and matches the pushed body.
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "# _md5hash=abc\n# _version=6\n"+pushed)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, p)
	defer cleanup()

	result, err := orch.sync(context.Background(), pushed, DefaultSyncOptions(), DefaultAuxiliaryFiles())

	require.NoError(t, err, "a transient read-back 5xx that recovers must not fail the deploy")
	require.NotNil(t, result)
	assert.True(t, result.Success)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, reloadPosts, "the verified reload must not be re-triggered by a transient read-back hiccup")
}
