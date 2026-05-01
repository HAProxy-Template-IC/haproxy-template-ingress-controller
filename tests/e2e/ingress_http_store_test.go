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

//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressHTTPStoreDemo covers test_ingress_http_store_demo: the chart
// uses an HTTP store template (configured in dev-values.yaml's
// `header-blocklist.map`) that fetches a denylist from the
// blocklist-server fixture and applies it as a frontend-level deny rule
// on the X-Custom-Header. The denylist is global (not per-Ingress), so
// any host on the chart is gated.
//
// Default blocklist content (from scripts/dev-env-assets/blocklist-server.yaml):
//   - bad-value
//   - evil-header
//   - blocked-user-agent
func TestIngressHTTPStoreDemo(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HTTP-store-driven header denylist",
		Host:        "ingress-http-store.localdev.me",
		Assess: []SimpleIngressAssertion{
			{
				Name: "no X-Custom-Header → 200",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectOK(t)
				},
			},
			{
				Name: "normal X-Custom-Header → 200",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("X-Custom-Header", "normal-value").ExpectOK(t)
				},
			},
			{
				Name: "blocklisted X-Custom-Header (bad-value) → 403",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("X-Custom-Header", "bad-value").
						ExpectStatus(t, http.StatusForbidden)
				},
			},
			{
				Name: "blocklisted X-Custom-Header (evil-header) → 403",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("X-Custom-Header", "evil-header").
						ExpectStatus(t, http.StatusForbidden)
				},
			},
		},
	})
}
