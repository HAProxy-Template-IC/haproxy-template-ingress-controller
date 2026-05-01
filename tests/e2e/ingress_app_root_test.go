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

// TestIngressAppRoot covers haproxy-ingress.github.io/app-root and the
// equivalent nginx.ingress.kubernetes.io/app-root: a request to "/"
// should be redirected to the configured root path. The chart wires this
// to an `http-request redirect` rule that's gated on the request path
// being exactly "/".
func TestIngressAppRoot(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: app-root redirects / to configured root",
		Host:        "ingress-app-root.localdev.me",
		Annotations: map[string]string{
			"haproxy-ingress.github.io/app-root": "/welcome",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "GET / redirects to /welcome",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectRedirect(t, "/welcome")
				},
			},
			{
				Name: "GET /welcome reaches the backend (no redirect loop)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/welcome").ExpectOK(t)
				},
			},
		},
	})
}
