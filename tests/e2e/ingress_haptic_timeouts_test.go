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

// TestHapticTimeouts covers the haproxy-haptic.org/timeout-* backend timeout
// family. Every key maps directly to a `timeout <x>` line in the generated
// backend (see charts/haptic/charts/haptic-annotations/20-backend-directives.yaml):
//
//	timeout-connect      -> timeout connect
//	timeout-server       -> timeout server
//	timeout-queue        -> timeout queue
//	timeout-tunnel       -> timeout tunnel
//	timeout-http-request -> timeout http-request
//	timeout-keep-alive   -> timeout http-keep-alive
//	timeout-check        -> timeout check
//
// Applying the full stack on one Ingress must produce a valid render that
// still serves traffic, mirroring the vendor smoke-test assertion.
func TestHapticTimeouts(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/timeout-* family",
		Host:        "ingress-haptic-timeouts.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/timeout-connect":      "5s",
			"haproxy-haptic.org/timeout-server":       "30s",
			"haproxy-haptic.org/timeout-queue":        "15s",
			"haproxy-haptic.org/timeout-tunnel":       "1h",
			"haproxy-haptic.org/timeout-http-request": "5s",
			"haproxy-haptic.org/timeout-keep-alive":   "1m",
			"haproxy-haptic.org/timeout-check":        "5s",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "ingress with full timeout stack still serves traffic",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON")
				}
			},
		}},
	})
}
