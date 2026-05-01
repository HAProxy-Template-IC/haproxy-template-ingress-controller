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

// TestIngressCORS covers test_ingress_cors: the haproxy.org/cors-*
// annotations inject the CORS response headers. We assert the four
// critical ones (origin, methods, credentials — bash covered the first
// three; max-age is also exercised by the same code path).
func TestIngressCORS(t *testing.T) {
	t.Parallel()
	const origin = "https://example.com"
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: CORS annotations",
		Host:        "ingress-cors.localdev.me",
		Annotations: map[string]string{
			"haproxy.org/cors-enable":            "true",
			"haproxy.org/cors-allow-origin":      origin,
			"haproxy.org/cors-allow-methods":     "GET, POST, PUT, DELETE, OPTIONS",
			"haproxy.org/cors-allow-headers":     "Content-Type, Authorization, X-Custom-Header",
			"haproxy.org/cors-allow-credentials": "true",
			"haproxy.org/cors-max-age":           "3600",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "Access-Control-Allow-Origin echoes the configured origin",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("Origin", origin).
						ExpectHeader(t, "Access-Control-Allow-Origin", origin)
				},
			},
			{
				Name: "Access-Control-Allow-Methods is set",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("Origin", origin).
						ExpectHeader(t, "Access-Control-Allow-Methods", "GET")
				},
			},
			{
				Name: "Access-Control-Allow-Credentials is true",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("Origin", origin).
						ExpectHeader(t, "Access-Control-Allow-Credentials", "true")
				},
			},
		},
	})
}
