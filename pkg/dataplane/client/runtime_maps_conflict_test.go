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

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplaceRuntimeMap_AddConflictConvergesWithSet pins the runtime lane
// against a key that already exists when the add runs (#138).
//
// The delta is computed from an earlier `show map`, so a concurrent reload can
// load the on-disk file and make a "new" key present. Returning the 409 made
// the orchestrator fall back to a full reload, which resets rate-limit
// stick-table state — the very thing the runtime lane exists to avoid.
func TestReplaceRuntimeMap_AddConflictConvergesWithSet(t *testing.T) {
	var (
		mu       sync.Mutex
		setCalls []string
		addCalls []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)

		// `show map` — the live map is EMPTY, so the delta classes every
		// desired key as a pure add.
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/runtime/maps/"):
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `[]`)

		// `add map` — the key is in fact already present (a reload loaded it).
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/runtime/maps/"):
			mu.Lock()
			addCalls = append(addCalls, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			fmt.Fprintln(w, `{"message":"entry already exists"}`)

		// `set map` — the convergence path under test.
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/runtime/maps/"):
			mu.Lock()
			setCalls = append(setCalls, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{}`)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
		PodName:  "haproxy-0",
	})
	require.NoError(t, err)

	err = c.ReplaceRuntimeMap(context.Background(), "pod-names.map", "10.42.1.209 backend-7c9f\n")

	require.NoError(t, err,
		"a pre-existing key must converge, not surface an error that forces a full reload")

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, addCalls, 1, "the add is still attempted first")
	assert.Len(t, setCalls, 1, "the 409 must be converged with set map")
}
