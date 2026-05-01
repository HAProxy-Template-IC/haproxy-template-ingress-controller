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

// TestIngressNginxCanary covers nginx.ingress.kubernetes.io/canary*:
// applying canary annotations to an Ingress should not break HAProxy
// for the route — the chart accepts the annotations and continues to
// serve traffic. The canary semantics themselves (header-based traffic
// splitting between primary/canary backends) need a second canary
// Ingress + a primary Ingress sharing the same host to fully verify;
// that's a follow-up. This smoke check protects against the rendered
// config becoming invalid when canary annotations are present.
func TestIngressNginxCanary(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: nginx.ingress.kubernetes.io/canary annotations",
		Host:        "ingress-canary.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/canary":                 "true",
			"nginx.ingress.kubernetes.io/canary-by-header":       "X-Canary",
			"nginx.ingress.kubernetes.io/canary-by-header-value": "true",
			"nginx.ingress.kubernetes.io/canary-by-cookie":       "canary_cookie",
			"nginx.ingress.kubernetes.io/canary-weight":          "50",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "canary-annotated ingress still serves the backend",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON")
				}
			},
		}},
	})
}
