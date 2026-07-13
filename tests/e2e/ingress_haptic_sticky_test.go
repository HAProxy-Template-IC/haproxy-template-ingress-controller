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

// TestHapticSessionAffinity is the haptic-native counterpart of
// TestIngressStickySession (ingress_sticky_test.go). Where the vendor test
// drives haproxy.org/cookie-persistence, this drives the best-of-breed
// haproxy-haptic.org/affinity keys: affinity=cookie plus session-cookie-name.
// The 26-rewrite-affinity fragment renders a `cookie <name> insert indirect
// nocache dynamic` directive on the backend, so HAProxy inserts a per-backend
// affinity cookie under the requested name.
//
// Unlike the vendor test we assert both halves of the contract: the response
// sets the named cookie, and presenting that cookie back keeps routing
// consistent (the request reaches the same backend). The shared echo-server
// runs a single replica, so the backend identity (echo host / pod hostname)
// is a stable, deterministic consistency signal.
func TestHapticSessionAffinity(t *testing.T) {
	t.Parallel()

	const cookieName = "HAPTICSTICKY"

	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/affinity=cookie sets and honors a session cookie",
		Host:        "ingress-haptic-sticky.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/affinity":            "cookie",
			"haproxy-haptic.org/session-cookie-name": cookieName,
		},
		Assess: []SimpleIngressAssertion{{
			Name: "sets " + cookieName + " cookie and presenting it keeps routing consistent",
			Check: func(t *testing.T, host string) {
				// cookieValue returns the value of the named Set-Cookie
				// header, or "" if the response carries no such cookie.
				cookieValue := func(resp *httpclient.Response) string {
					for _, c := range resp.Header.Values("Set-Cookie") {
						if !strings.HasPrefix(c, cookieName+"=") {
							continue
						}
						v := strings.TrimPrefix(c, cookieName+"=")
						if i := strings.IndexByte(v, ';'); i >= 0 {
							v = v[:i]
						}
						return v
					}
					return ""
				}

				// First request carries no cookie: the backend answers 200
				// and HAProxy inserts the affinity cookie under cookieName.
				first := httpclient.New(t).GET(host, "/").ExpectMatching(t,
					"200 with Set-Cookie: "+cookieName+"=…",
					func(resp *httpclient.Response) bool {
						return resp.Status == http.StatusOK &&
							resp.Echo != nil && cookieValue(resp) != ""
					})

				value := cookieValue(first)
				backend := first.Echo.Host

				// Presenting the cookie keeps routing consistent: the request
				// still lands 200 on the same backend HAProxy pinned it to.
				httpclient.New(t).GET(host, "/").
					WithHeader("Cookie", cookieName+"="+value).
					ExpectMatching(t,
						"presented "+cookieName+" cookie routes to the same backend",
						func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusOK &&
								resp.Echo != nil && resp.Echo.Host == backend
						})
			},
		}},
	})
}
