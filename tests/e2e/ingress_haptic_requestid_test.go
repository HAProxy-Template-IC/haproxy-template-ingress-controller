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

// TestHapticRequestID verifies request correlation IDs (74-request-id.yaml):
// with request-id enabled, HAProxy generates a unique id and forwards it to the
// upstream in the configured header.
func TestHapticRequestID(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native request correlation ID",
		Host:        "ingress-haptic-requestid.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/request-id":        "true",
			"haproxy-haptic.org/request-id-header": "X-Request-ID",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "upstream receives a non-empty X-Request-ID",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectMatching(t,
						"upstream received a non-empty X-Request-ID",
						func(resp *httpclient.Response) bool {
							return resp.Echo != nil && resp.Echo.Headers["x-request-id"] != ""
						})
				},
			},
		},
	})
}
