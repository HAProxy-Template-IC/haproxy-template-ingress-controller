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

// TestIngressHaproxyIngressTimeouts covers the haproxy-ingress.github.io
// timeout-* family + backend-check-interval. The haproxy-ingress library
// re-implements (rather than aliases) the haproxytech/* timeout
// annotations, so the rendered HAProxy directives go through their own
// snippet code path. Smoke-test that applying them all on one Ingress
// produces a valid render that still serves traffic.
//
// Closes the gap on the last few haproxy-ingress.github.io annotations
// that didn't have any test coverage (chart validationTests included).
func TestIngressHaproxyIngressTimeouts(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-ingress.github.io timeout-* family",
		Host:        "ingress-hi-timeouts.localdev.me",
		Annotations: map[string]string{
			"haproxy-ingress.github.io/timeout-connect":      "5s",
			"haproxy-ingress.github.io/timeout-http-request": "5s",
			"haproxy-ingress.github.io/timeout-keep-alive":   "1m",
			"haproxy-ingress.github.io/timeout-queue":        "15s",
			"haproxy-ingress.github.io/timeout-server":       "30s",
			"haproxy-ingress.github.io/timeout-tunnel":       "1h",
			// backend-check-interval lives here too — it's a backend-side
			// directive that pairs with the timeout family.
			"haproxy-ingress.github.io/backend-check-interval": "20s",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "ingress with full timeout/check stack still serves traffic",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON")
				}
			},
		}},
	})
}
