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

package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	CommunityAPIVersion  = "v3.2.6 87ad0bcf"
	EnterpriseAPIVersion = "v3.2.6-ee1"
)

// MockServerConfig configures a mock Dataplane API server for tests.
type MockServerConfig struct {
	// APIVersion is returned by the /v3/info endpoint.
	// Defaults to CommunityAPIVersion or EnterpriseAPIVersion depending on server type.
	APIVersion string

	// Handlers maps URL paths to handler functions.
	Handlers map[string]http.HandlerFunc
}

// NewMockServer creates a test server that mimics the Dataplane API.
// It always provides a /v3/info endpoint returning the configured API version.
func NewMockServer(t *testing.T, cfg MockServerConfig) *httptest.Server {
	t.Helper()

	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = CommunityAPIVersion
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"api":{"version":"%s"}}`, apiVersion)
			return
		}

		if handler, ok := cfg.Handlers[r.URL.Path]; ok {
			handler(w, r)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}

// NewMockEnterpriseServer creates a test server that mimics the Enterprise Dataplane API.
// It provides the same /v3/info endpoint but also tries to match handlers with and without
// the /v3 prefix, which mirrors how the enterprise API routes requests.
func NewMockEnterpriseServer(t *testing.T, cfg MockServerConfig) *httptest.Server {
	t.Helper()

	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = EnterpriseAPIVersion
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"api":{"version":"%s"}}`, apiVersion)
			return
		}

		path := r.URL.Path
		if handler, ok := cfg.Handlers[path]; ok {
			handler(w, r)
			return
		}

		if !strings.HasPrefix(path, "/v3") {
			if handler, ok := cfg.Handlers["/v3"+path]; ok {
				handler(w, r)
				return
			}
		}

		t.Logf("Mock server: no handler for path: %s", path)
		w.WriteHeader(http.StatusNotFound)
	}))
}
