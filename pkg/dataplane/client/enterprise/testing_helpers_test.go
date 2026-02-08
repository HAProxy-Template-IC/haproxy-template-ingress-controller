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
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

const (
	testEnterpriseAPIVersion = testutil.EnterpriseAPIVersion
	testCommunityAPIVersion  = testutil.CommunityAPIVersion
)

type mockServerConfig struct {
	apiVersion string
	handlers   map[string]http.HandlerFunc
}

func newMockEnterpriseServer(t *testing.T, cfg mockServerConfig) *httptest.Server {
	t.Helper()
	return testutil.NewMockEnterpriseServer(t, testutil.MockServerConfig{
		APIVersion: cfg.apiVersion,
		Handlers:   cfg.handlers,
	})
}

func newTestClient(t *testing.T, server *httptest.Server) *client.DataplaneClient {
	t.Helper()
	return testutil.NewTestClient(t, server)
}

func jsonResponse(body string) http.HandlerFunc {
	return testutil.JSONResponse(body)
}

func errorResponse(status int) http.HandlerFunc {
	return testutil.ErrorResponse(status)
}

func methodAwareHandler(handlers map[string]http.HandlerFunc) http.HandlerFunc {
	return testutil.MethodAwareHandler(handlers)
}
