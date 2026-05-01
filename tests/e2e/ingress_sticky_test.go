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
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressStickySession covers test_ingress_sticky: the
// haproxy.org/cookie-persistence annotation makes HAProxy emit a
// SERVERID cookie that pins clients to a backend. We don't verify the
// stickiness behavior (would need to detect different backends across
// requests), only that the cookie is set.
//
// This test deliberately omits t.Parallel(): cookie-persistence
// interacts with HAProxy stick-tables that other tests in the same
// suite could mutate. Keeping it serial avoids subtle timing flakes.
func TestIngressStickySession(t *testing.T) {
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: cookie-persistence sets SERVERID cookie",
		Host:        "ingress-sticky.localdev.me",
		Annotations: map[string]string{
			"haproxy.org/cookie-persistence": "SERVERID",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "response carries Set-Cookie: SERVERID=…",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				cookies := resp.Header.Values("Set-Cookie")
				for _, c := range cookies {
					if strings.HasPrefix(strings.ToUpper(c), "SERVERID=") {
						return
					}
				}
				t.Fatalf("expected Set-Cookie SERVERID=…; got headers: %v", cookies)
			},
		}},
	})
}
