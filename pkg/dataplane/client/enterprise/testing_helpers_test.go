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

package enterprise

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
)

// Test helper constants for mock server configuration.
const (
	// testEnterpriseAPIVersion reports as HAProxy Enterprise v3.2.
	testEnterpriseAPIVersion = "v3.2.6-ee1"
	// testCommunityAPIVersion reports as HAProxy Community (no -ee suffix).
	testCommunityAPIVersion = "v3.2.6 87ad0bcf"
	testUsername            = "admin"
	testPassword            = "password"
)

// mockServerConfig configures a mock Dataplane API server for tests.
type mockServerConfig struct {
	// apiVersion is returned by the /v3/info endpoint.
	// Defaults to testEnterpriseAPIVersion if empty.
	apiVersion string

	// handlers maps URL paths to handler functions.
	handlers map[string]http.HandlerFunc
}

// newMockEnterpriseServer creates a test server that mimics the Enterprise Dataplane API.
// It always provides a /v3/info endpoint returning the configured API version.
func newMockEnterpriseServer(t *testing.T, cfg mockServerConfig) *httptest.Server {
	t.Helper()

	apiVersion := cfg.apiVersion
	if apiVersion == "" {
		apiVersion = testEnterpriseAPIVersion
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle /v3/info specially - always required for client initialization
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
		if !hasPrefix(path, "/v3") {
			if handler, ok := cfg.handlers["/v3"+path]; ok {
				handler(w, r)
				return
			}
		}

		// Default: 404 - log the path for debugging
		t.Logf("Mock server: no handler for path: %s", path)
		w.WriteHeader(http.StatusNotFound)
	}))
}

// hasPrefix checks if s has the given prefix.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// newTestClient creates an enterprise client connected to a test server.
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

// jsonResponse creates a handler that returns JSON with http.StatusOK.
func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, body)
	}
}

// errorResponse creates a handler that returns an error status.
func errorResponse(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}
}

// methodAwareHandler creates a handler that responds differently based on HTTP method.
func methodAwareHandler(handlers map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handler, ok := handlers[r.Method]; ok {
			handler(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
