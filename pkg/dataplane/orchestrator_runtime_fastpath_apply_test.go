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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

const (
	endpointTestConfigBase = `global

defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s

backend api
  default-server check
  server SRV_1 %s enabled
`
)

// TestSyncRuntimeFast_RawPushNoFetch pins the runtime-raw lane: SyncRuntimeFast
// applies the shared render diff's runtime actions via a single
// skip_reload+skip_version raw push carrying the DESIRED config body — and does
// NOT fetch each pod's config first (the fetch only re-derived the same changed
// servers). The body is the desired render (entered only when the diff is purely
// runtime-eligible, so pushing it skip_reload hides nothing from a reload).
func TestSyncRuntimeFast_RawPushNoFetch(t *testing.T) {
	var gets, posts atomic.Int32
	var mu sync.Mutex
	var gotActions, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"api":{"version":"v3.2.13 abcdef0"}}`)
		case strings.HasSuffix(r.URL.Path, "/configuration/raw") && r.Method == http.MethodGet:
			gets.Add(1) // a per-pod fetch — must NOT happen
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/configuration/raw") && r.Method == http.MethodPost:
			posts.Add(1)
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			gotActions = r.Header.Get("X-Runtime-Actions")
			gotBody = string(b)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	c, err := NewClient(context.Background(), &Endpoint{URL: server.URL, Username: "admin", Password: "pass"})
	require.NoError(t, err)
	defer c.Close()

	p, err := parser.New()
	require.NoError(t, err)
	prev, err := p.ParseFromString(buildEndpointTestConfig("10.0.0.1:8080"))
	require.NoError(t, err)
	currentRaw := buildEndpointTestConfig("10.0.0.2:8080")
	current, err := p.ParseFromString(currentRaw)
	require.NoError(t, err)

	updates, err := ComputeRuntimeServerUpdates(prev, current)
	require.NoError(t, err)
	res, err := c.SyncRuntimeFast(context.Background(), updates, currentRaw, DefaultSyncOptions())
	require.NoError(t, err)
	assert.Len(t, res.AppliedOperations, 1)
	assert.False(t, res.ReloadTriggered, "the runtime-raw lane never reloads")
	assert.Equal(t, SyncModeRuntime, res.SyncMode)

	assert.Equal(t, int32(0), gets.Load(), "no per-pod fetch — desiredConfig is the body")
	assert.GreaterOrEqual(t, posts.Load(), int32(1), "the runtime change is pushed")
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, gotActions, "SetServerAddr", "shared render diff's set-server action carried in the header")
	assert.Contains(t, gotBody, "10.0.0.2", "the pushed body is the desired render")
}

func buildEndpointTestConfig(addrPort string) string {
	return fmt.Sprintf(endpointTestConfigBase, addrPort)
}

// TestSyncRuntimeFast_RestampVersionHeader pins the header re-stamp: a
// skip_version push leaves the pod's config headerless, and sync() refuses to
// trust an empty diff against a headerless config (it forces a reload — see
// TestSync_HeaderlessNoDiff_ForcesReload). To keep the pure runtime-raw lane
// reload-free across later structural syncs, an AUTHORITATIVE apply
// (opts.RestampVersionHeader, set only by the deployer's runtime-raw lane
// dispatch) follows the successful skip_version push with ONE versioned
// skip_reload push of the same body — restoring the `# _version` header
// without a reload. Partial fast-track applies (flag unset) must NOT re-stamp:
// they can race an in-flight structural reload, and a stamped header would let
// the next sync trust an empty diff over a lost `set server` update.
func TestSyncRuntimeFast_RestampVersionHeader(t *testing.T) {
	type recordedPost struct {
		query   string
		actions string
	}

	run := func(t *testing.T, restamp bool, restampStatus int) []recordedPost {
		t.Helper()
		var mu sync.Mutex
		var recorded []recordedPost
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v3/info":
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"api":{"version":"v3.2.13 abcdef0"}}`)
			case strings.HasSuffix(r.URL.Path, "/configuration/raw") && r.Method == http.MethodPost:
				mu.Lock()
				recorded = append(recorded, recordedPost{
					query:   r.URL.RawQuery,
					actions: r.Header.Get("X-Runtime-Actions"),
				})
				// The re-stamp is the VERSIONED push (no skip_version param);
				// the initial apply carries skip_version=true.
				isRestamp := !strings.Contains(r.URL.RawQuery, "skip_version=true")
				mu.Unlock()
				if isRestamp && restampStatus != 0 {
					w.WriteHeader(restampStatus)
					return
				}
				w.WriteHeader(http.StatusCreated)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		c, err := NewClient(context.Background(), &Endpoint{URL: server.URL, Username: "admin", Password: "pass"})
		require.NoError(t, err)
		defer c.Close()

		p, err := parser.New()
		require.NoError(t, err)
		prev, err := p.ParseFromString(buildEndpointTestConfig("10.0.0.1:8080"))
		require.NoError(t, err)
		currentRaw := buildEndpointTestConfig("10.0.0.2:8080")
		current, err := p.ParseFromString(currentRaw)
		require.NoError(t, err)
		updates, err := ComputeRuntimeServerUpdates(prev, current)
		require.NoError(t, err)

		opts := DefaultSyncOptions()
		opts.RestampVersionHeader = restamp
		res, err := c.SyncRuntimeFast(context.Background(), updates, currentRaw, opts)
		require.NoError(t, err, "the apply must succeed regardless of re-stamp outcome")
		require.Equal(t, SyncModeRuntime, res.SyncMode)

		mu.Lock()
		defer mu.Unlock()
		return append([]recordedPost(nil), recorded...)
	}

	t.Run("authoritative apply re-stamps with a versioned skip_reload push", func(t *testing.T) {
		posts := run(t, true, 0)
		require.Len(t, posts, 2)
		assert.Contains(t, posts[0].query, "skip_version=true")
		assert.NotEmpty(t, posts[0].actions, "first push carries the runtime actions")
		assert.Contains(t, posts[1].query, "version=1", "re-stamp uses the headerless sentinel as the expected version")
		assert.Contains(t, posts[1].query, "skip_reload=true", "re-stamp must not reload")
		assert.NotContains(t, posts[1].query, "skip_version=true", "re-stamp is a VERSIONED push — that is what writes the header")
		assert.Empty(t, posts[1].actions, "re-stamp carries no runtime actions")
	})

	t.Run("partial apply (flag unset) pushes exactly once and stays headerless", func(t *testing.T) {
		posts := run(t, false, 0)
		require.Len(t, posts, 1)
		assert.Contains(t, posts[0].query, "skip_version=true")
	})

	t.Run("re-stamp failure is best-effort and does not fail the apply", func(t *testing.T) {
		posts := run(t, true, http.StatusConflict) // concurrent versioned writer
		require.Len(t, posts, 2, "the re-stamp was attempted")
	})
}
