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

// TestHapticPathRewrite adapts the vendor path-rewrite e2e tests
// (TestIngressPathRewrite / ingress_rewrite_test.go) to the native
// haproxy-haptic.org/* annotation library. It exercises the two haptic
// canonical path-rewrite keys end-to-end, verifying via echo-server's
// reflected path that the upstream sees the rewritten path.
//
//   - haproxy-haptic.org/path-rewrite (haproxytech form): a "<from> <to>"
//     value emits `http-request replace-path <from> <to>`, so the matched
//     prefix is stripped before forwarding.
//   - haproxy-haptic.org/rewrite-target (haproxy-ingress / nginx form): a
//     value carrying a `$N` backreference is translated to HAProxy's `\N`
//     and emitted as backend-scoped `http-request replace-path (.*) <value>`.
func TestHapticPathRewrite(t *testing.T) {
	t.Parallel()

	// path-rewrite: strip the /api/v1/ prefix via the "<from> <to>" form.
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/path-rewrite annotation",
		Host:        "ingress-haptic-rewrite.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/path-rewrite": `^/api/v1/(.*) /\1`,
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

	// rewrite-target: prepend /backend using the nginx-style $1 capture,
	// which translates to HAProxy's \1 backreference over the whole path.
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/rewrite-target annotation",
		Host:        "ingress-haptic-rewrite-target.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/rewrite-target": `/backend$1`,
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "/svc prepends /backend at the backend",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/svc").ExpectEchoPath(t, "/backend/svc")
				},
			},
		},
	})
}
