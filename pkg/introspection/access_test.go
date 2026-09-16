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

package introspection

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDebugAccessUsesRequestPath(t *testing.T) {
	server := NewServer("127.0.0.1:0", NewRegistry())
	for _, pattern := range []string{"/debug", "GET /debug/custom", "example.com/debug/host"} {
		server.RegisterHandler(pattern, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("diagnostic"))
		})
	}
	server.Setup()

	for _, path := range []string{"/debug", "/debug/custom", "/debug/host", "/debug/vars", "/debug/pprof/", "/%64ebug/custom"} {
		for _, remote := range []string{"192.0.2.1:1234", "127.0.0.1:1234", "[::1]:1234"} {
			t.Run(path+"/"+remote, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, "http://example.com"+path, http.NoBody)
				request.RemoteAddr = remote
				request.Header.Set("X-Forwarded-For", "127.0.0.1")
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, request)
				want := http.StatusOK
				if remote == "192.0.2.1:1234" {
					want = http.StatusForbidden
				}
				assert.Equal(t, want, response.Code, response.Body.String())
			})
		}
	}
	for _, path := range []string{"/health", "/healthz"} {
		request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		request.RemoteAddr = "192.0.2.1:1234"
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		assert.Equal(t, http.StatusOK, response.Code)
	}
}
