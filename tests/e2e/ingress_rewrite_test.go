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

// TestIngressPathRewrite covers test_ingress_rewrite: the
// haproxy.org/path-rewrite annotation strips a path prefix before
// forwarding to the backend. echo-server includes the (rewritten) path
// in its JSON response, so we verify by asserting on Echo.Path.
func TestIngressPathRewrite(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: path-rewrite annotation",
		Host:        "ingress-rewrite.localdev.me",
		Annotations: map[string]string{
			// Strip /api/v1/ prefix.
			"haproxy.org/path-rewrite": `^/api/v1/(.*) /\1`,
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "/api/v1/test rewrites to /test at the backend",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/api/v1/test").ExpectEchoPath(t, "/test")
				},
			},
			{
				Name: "/api/v1/users rewrites to /users at the backend",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/api/v1/users").ExpectEchoPath(t, "/users")
				},
			},
		},
	})
}
