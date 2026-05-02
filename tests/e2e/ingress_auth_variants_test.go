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
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// authServerBase returns the in-cluster URL of the shared auth-server
// fixture (deployed once into SharedFixturesNamespace by TestMain). The
// SPOA hub fetches it as plain HTTP so cross-namespace is fine.
//
// Format mirrors what TestIngressExternalAuth's smoke test uses
// (svc:80) — a previous attempt at .svc.cluster.local without port
// produced a 401 Basic-auth challenge from HAProxy, suggesting the
// chart's nginx-ingress library doesn't accept that variant uniformly.
func authServerBase() string {
	return "http://auth-server." + SharedFixturesNamespace + ".svc:80"
}

// TestIngressAuthMethod covers test_ingress_auth_method: the
// nginx.ingress.kubernetes.io/auth-method annotation forces the SPOA
// hub's auth subrequest to go out as POST. auth-server's /allow-post
// returns 200 only on POST; the chart must thread the method through
// or the auth subrequest fails (default GET → 405) and the request
// gets a 401.
//
// A 200 from the protected ingress is the proof: chart correctly
// overrode the default GET to POST.
func TestIngressAuthMethod(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: auth-method (POST override)",
		Host:        "ingress-auth-method.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/auth-url":    authServerBase() + "/allow-post",
			"nginx.ingress.kubernetes.io/auth-method": "POST",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "chart overrides default GET to POST → auth-server 200 → backend reached",
			Check: func(t *testing.T, host string) {
				httpclient.New(t).GET(host, "/").ExpectOK(t)
			},
		}},
	})
}

// TestIngressAuthHeadersSucceed covers test_ingress_auth_headers_succeed:
// auth-server's /allow returns X-Auth-User: alice; the
// nginx.ingress.kubernetes.io/auth-response-headers annotation tells the
// chart to forward that header to the upstream backend on auth success.
// echo-server's JSON includes the request headers, so we can read it back.
func TestIngressAuthHeadersSucceed(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: auth-response-headers forwarded to backend on success",
		Host:        "ingress-auth-headers-succeed.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/auth-url":              authServerBase() + "/allow",
			"nginx.ingress.kubernetes.io/auth-response-headers": "X-Auth-User",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "X-Auth-User: alice reaches the backend",
			Check: func(t *testing.T, host string) {
				// Poll on the echo'd header (not just status). The 200 from the
				// auth-allowed path syncs quickly, but the companion
				// `http-request set-header X-Auth-User var(...)` rule that
				// forwards the header to the backend can land a reload cycle
				// later. ExpectEchoHeader waits for the full pipeline.
				httpclient.New(t).GET(host, "/").ExpectEchoHeader(t, "X-Auth-User", "alice")
			},
		}},
	})
}

// TestIngressAuthHeadersFail covers test_ingress_auth_headers_fail:
// auth-server's /deny-with-fail-headers returns 401 plus
// WWW-Authenticate and X-Error-Reason headers; the
// haproxy-ingress.github.io/auth-headers-fail annotation tells the chart
// to forward those headers to the *client* (not the backend) on the
// deny path. Verifies the fail-path header machinery — reaches the
// client through the http-after-response set-header rules the chart
// emits when the auth plugin's `allowed=false`.
func TestIngressAuthHeadersFail(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: auth-headers-fail forwarded to client on 401",
		Host:        "ingress-auth-headers-fail.localdev.me",
		Annotations: map[string]string{
			"haproxy-ingress.github.io/auth-url":          authServerBase() + "/deny-with-fail-headers",
			"haproxy-ingress.github.io/auth-headers-fail": "WWW-Authenticate, X-Error-Reason",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "WWW-Authenticate and X-Error-Reason are on the 401 response",
			Check: func(t *testing.T, host string) {
				// Poll on the conjunction of all three signals. The
				// `http-request deny deny_status 401` rule and the
				// companion `http-after-response set-header` rules can land
				// in different reload cycles, so polling on any single
				// signal leaves a race window where one rule is live but the
				// others are not (e.g. set-header rule landed → response
				// carries WWW-Authenticate, but deny rule hasn't → status
				// is still 200 from the proxied backend).
				_ = httpclient.New(t).GET(host, "/").ExpectMatching(t,
					"auth deny pipeline live (401 + WWW-Authenticate + X-Error-Reason)",
					func(resp *httpclient.Response) bool {
						return resp.Status == http.StatusUnauthorized &&
							strings.Contains(resp.Header.Get("WWW-Authenticate"), `Bearer realm="api"`) &&
							resp.Header.Get("X-Error-Reason") == "token-expired"
					})
			},
		}},
	})
}
