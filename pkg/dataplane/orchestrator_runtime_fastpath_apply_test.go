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
