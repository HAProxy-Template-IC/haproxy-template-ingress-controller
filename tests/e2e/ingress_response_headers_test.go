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

// TestIngressResponseHeaders covers test_ingress_headers_response: the
// haproxy.org/response-set-header annotation injects security headers
// into the response. Verifies the rendered HAProxy config produces the
// configured headers on the wire.
func TestIngressResponseHeaders(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: response-set-header annotation",
		Host:        "ingress-headers-response.localdev.me",
		Annotations: map[string]string{
			"haproxy.org/response-set-header": "Strict-Transport-Security max-age=31536000\nX-Custom-Response custom-resp-value\nX-Frame-Options DENY",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "Strict-Transport-Security header is set",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectHeader(t, "Strict-Transport-Security", "max-age=31536000")
				},
			},
			{
				Name: "X-Frame-Options header is DENY",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectHeader(t, "X-Frame-Options", "DENY")
				},
			},
			{
				Name: "X-Custom-Response header passes through",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectHeader(t, "X-Custom-Response", "custom-resp-value")
				},
			},
		},
	})
}
