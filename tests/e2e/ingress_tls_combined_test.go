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

// TestIngressTLSCombined covers test_ingress_tls_combined: TLS termination
// stacked with security-header annotations. Verifies that HSTS,
// X-Frame-Options, and X-Content-Type-Options all reach the client over
// the HTTPS path.
func TestIngressTLSCombined(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description:   "Ingress: TLS termination + security headers",
		Host:          "ingress-tls-combined.localdev.me",
		TLSSecretName: "ingress-tls-combined-cert",
		Annotations: map[string]string{
			"haproxy.org/ssl-redirect":      "true",
			"haproxy.org/ssl-redirect-code": "301",
			"haproxy.org/response-set-header": "Strict-Transport-Security max-age=31536000; includeSubDomains\n" +
				"X-Frame-Options DENY\n" +
				"X-Content-Type-Options nosniff",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "HSTS header present on HTTPS response",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).HTTPS(host, "/").ExpectHeader(t, "Strict-Transport-Security", "max-age=31536000")
				},
			},
			{
				Name: "X-Frame-Options DENY",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).HTTPS(host, "/").ExpectHeader(t, "X-Frame-Options", "DENY")
				},
			},
			{
				Name: "X-Content-Type-Options nosniff",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).HTTPS(host, "/").ExpectHeader(t, "X-Content-Type-Options", "nosniff")
				},
			},
		},
	})
}
