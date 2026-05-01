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

// TestIngressNginxPermanentRedirect covers
// nginx.ingress.kubernetes.io/permanent-redirect: a request to any path
// should return a redirect to the configured target. Distinct from
// haproxy.org/request-redirect (TestIngressRedirect) because this is the
// nginx-style annotation that the chart's nginx-ingress library wires
// independently — same rule shape, but goes through a different
// dedup/code path (which used to be subject to the cross-frontend
// first_seen bug).
func TestIngressNginxPermanentRedirect(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: nginx.ingress.kubernetes.io/permanent-redirect",
		Host:        "ingress-perm-redirect.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/permanent-redirect": "https://example.com/relocated",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "any request redirects to configured Location",
			Check: func(t *testing.T, host string) {
				httpclient.New(t).GET(host, "/some/path").ExpectRedirect(t, "https://example.com/relocated")
			},
		}},
	})
}
