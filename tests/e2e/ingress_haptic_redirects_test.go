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
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticRedirects covers the haptic-native host redirect and app-root
// annotations under the haproxy-haptic.org/ prefix, adapting the nginx-ingress
// permanent/temporal redirect and the app-root vendor e2e tests:
//
//   - haproxy-haptic.org/permanent-redirect (+ -code): any path returns a 301
//     with the configured Location (registered into gf["redirectHosts"], base
//     emits from redirect-loc-<code>.map).
//   - haproxy-haptic.org/temporal-redirect (+ -code): same shape, default 302.
//   - haproxy-haptic.org/app-root: a request to "/" is redirected to the
//     configured sub-path (path="/"-gated redirect via app-root.map); other
//     paths reach the backend.
//
// Each annotation registers per host, and a catch-all host redirect would
// shadow the app-root "/"-gated redirect, so the three behaviours use distinct
// hosts under the shared "ingress-haptic-redirects" theme rather than one
// Ingress.
func TestHapticRedirects(t *testing.T) {
	t.Parallel()

	// Permanent redirect: default code is 301; set it explicitly to exercise
	// the -code key and pin the asserted status deterministically.
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/permanent-redirect",
		Host:        "ingress-haptic-redirects-permanent.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/permanent-redirect":      "https://example.com/relocated",
			"haproxy-haptic.org/permanent-redirect-code": "301",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "any request returns 301 to configured Location",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/some/path").ExpectStatus(t, 301)
				if got := resp.Header.Get("Location"); got != "https://example.com/relocated" {
					t.Fatalf("expected Location https://example.com/relocated, got %q", got)
				}
			},
		}},
	})

	// Temporal redirect: default code is 302; set it explicitly to exercise the
	// -code key and confirm the chart picks the temporary status per annotation.
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/temporal-redirect",
		Host:        "ingress-haptic-redirects-temporal.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/temporal-redirect":      "https://example.com/temp",
			"haproxy-haptic.org/temporal-redirect-code": "302",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "any request returns 302 to configured Location",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/some/path").ExpectStatus(t, 302)
				if got := resp.Header.Get("Location"); got != "https://example.com/temp" {
					t.Fatalf("expected Location https://example.com/temp, got %q", got)
				}
			},
		}},
	})

	// App-root: a request to "/" is redirected to the configured sub-path; any
	// other path (here the app-root target itself) reaches the backend.
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/app-root",
		Host:        "ingress-haptic-redirects-approot.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/app-root": "/welcome",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "GET / redirects to /welcome",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectRedirect(t, "/welcome")
				},
			},
			{
				Name: "GET /welcome reaches the backend (no redirect loop)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/welcome").ExpectOK(t)
				},
			},
		},
	})
}
