package dataplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
)

// runtimeMapFake is a minimal Dataplane API fake for the pure-runtime map
// lane: it serves the runtime map entries endpoint from an in-memory store
// and counts force_reload config pushes. With applyEntryWrites=false it
// emulates the issue #48 failure mode — runtime map POST/DELETE are
// acknowledged (201/204) but never take effect, exactly what the CI
// artifacts show on the haproxytech 3.1 image under reload churn.
type runtimeMapFake struct {
	mu               sync.Mutex
	entries          map[string]string
	applyEntryWrites bool
	forceReloads     atomic.Int32
}

func (f *runtimeMapFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		isEntries := strings.Contains(r.URL.Path, "/runtime/maps/") && strings.HasSuffix(r.URL.Path, "/entries")
		switch {
		case isEntries && r.Method == http.MethodGet:
			f.listEntries(w)
		case isEntries && r.Method == http.MethodPost:
			f.addEntry(w, r)
		case strings.Contains(r.URL.Path, "/runtime/maps/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/configuration/raw") && r.Method == http.MethodPost:
			if r.URL.Query().Get("force_reload") == "true" {
				f.forceReloads.Add(1)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

func (f *runtimeMapFake) listEntries(w http.ResponseWriter) {
	f.mu.Lock()
	out := make([]map[string]string, 0, len(f.entries))
	for k, v := range f.entries {
		out = append(out, map[string]string{"key": k, "value": v})
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (f *runtimeMapFake) addEntry(w http.ResponseWriter, r *http.Request) {
	var e struct{ Key, Value *string }
	_ = json.NewDecoder(r.Body).Decode(&e)
	if f.applyEntryWrites && e.Key != nil {
		f.mu.Lock()
		f.entries[*e.Key] = deref(e.Value)
		f.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]string{"key": deref(e.Key), "value": deref(e.Value)}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// applyRuntimeOnlyForTest drives applyRuntimeOnly with one pending map
// content update against the fake.
func applyRuntimeOnlyForTest(t *testing.T, fake *runtimeMapFake) (*SyncResult, error) {
	t.Helper()
	orch, cleanup := createTestOrchestratorWithParser(t, fake.handler(), &mockConfigParser{})
	t.Cleanup(cleanup)

	mapUpdates := []auxiliaryfiles.MapFile{{
		Path:    "host.map",
		Content: "example.test:18666 example.test:18666\n",
	}}
	return orch.applyRuntimeOnly(
		context.Background(),
		"global\n  daemon\n",
		&comparator.ConfigDiff{},
		nil, // runtimeOps
		mapUpdates,
		nil, // certUpdates
		nil, // caUpdates
		&auxiliaryFileDiffs{},
		"", // actions
		1,  // version
		DefaultSyncOptions(),
		time.Now(),
	)
}

// TestApplyRuntimeOnly_MapVerifyMismatchFallsBackToReload pins the issue #48
// hardening: when a runtime map update is acknowledged by the Dataplane API
// but the read-back shows the live map did NOT converge (lost master-socket
// write), the pure-runtime lane must fall back to a force_reload push —
// which converges from the on-disk file the pre-config phase wrote — instead
// of reporting a runtime-only success that latches stale routing.
func TestApplyRuntimeOnly_MapVerifyMismatchFallsBackToReload(t *testing.T) {
	fake := &runtimeMapFake{entries: map[string]string{}, applyEntryWrites: false}

	res, err := applyRuntimeOnlyForTest(t, fake)

	require.NoError(t, err)
	assert.True(t, res.ReloadTriggered, "lost runtime map write must trigger the reload fallback")
	assert.Equal(t, SyncModeReload, res.SyncMode)
	assert.GreaterOrEqual(t, fake.forceReloads.Load(), int32(1), "a force_reload push must converge the worker from the on-disk file")
}

// TestApplyRuntimeOnly_MapVerifyConvergedStaysRuntime pins the good case: the
// runtime map write takes effect, the read-back matches desired, and the lane
// stays reload-free.
func TestApplyRuntimeOnly_MapVerifyConvergedStaysRuntime(t *testing.T) {
	fake := &runtimeMapFake{entries: map[string]string{}, applyEntryWrites: true}

	res, err := applyRuntimeOnlyForTest(t, fake)

	require.NoError(t, err)
	assert.False(t, res.ReloadTriggered, "converged runtime map apply must stay reload-free")
	assert.Equal(t, SyncModeRuntime, res.SyncMode)
	assert.Equal(t, int32(0), fake.forceReloads.Load(), "no force_reload push on the happy path")
}
