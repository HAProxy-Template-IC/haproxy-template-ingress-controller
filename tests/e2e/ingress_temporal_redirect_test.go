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

// TestIngressNginxTemporalRedirect covers
// nginx.ingress.kubernetes.io/temporal-redirect: distinct from
// permanent-redirect because it uses 302 (Found) instead of 301
// (Moved Permanently). Same chart code path, different rendered code,
// so it gets its own assertion to confirm the chart picks the right
// status code per annotation.
func TestIngressNginxTemporalRedirect(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: nginx.ingress.kubernetes.io/temporal-redirect",
		Host:        "ingress-temporal-redirect.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/temporal-redirect": "https://example.com/temp",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "any request returns redirect to configured location",
			Check: func(t *testing.T, host string) {
				httpclient.New(t).GET(host, "/some/path").ExpectRedirect(t, "https://example.com/temp")
			},
		}},
	})
}

// TestIngressHaproxyRedirectTo covers haproxy-ingress.github.io/redirect-to
// with a custom redirect code (302). Verifies the haproxy-ingress
// library's redirect annotation wires correctly through the (post-bug)
// frontend-filters flow.
func TestIngressHaproxyRedirectTo(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-ingress.github.io/redirect-to",
		Host:        "ingress-haproxy-redirect-to.localdev.me",
		Annotations: map[string]string{
			"haproxy-ingress.github.io/redirect-to":      "https://example.com/relocated",
			"haproxy-ingress.github.io/redirect-to-code": "302",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "request redirects to configured Location with code 302",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectStatus(t, 302)
				if got := resp.Header.Get("Location"); got != "https://example.com/relocated" {
					t.Fatalf("expected Location https://example.com/relocated, got %q", got)
				}
			},
		}},
	})
}
