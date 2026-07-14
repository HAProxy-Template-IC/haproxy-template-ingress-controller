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

// TestHapticPathRewrite exercises both forms of the haptic-native
// haproxy-haptic.org/path-rewrite key end-to-end, verifying via echo-server's
// reflected path that the upstream sees the rewritten path.
//
//   - two-token form: a "<from> <to>" value emits
//     `http-request replace-path <from> <to>`, so the matched prefix is
//     stripped (or rewritten) before forwarding.
//   - bare form: a value with no space emits
//     `http-request replace-path (.*) <value>`, replacing the whole request
//     path with the given value.
func TestHapticPathRewrite(t *testing.T) {
	t.Parallel()

	// Two-token form: strip the /api/v1/ prefix via "<from> <to>".
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/path-rewrite two-token form",
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

	// Bare form: a value with no space replaces the whole path, so every
	// request lands on /backend at the upstream regardless of the request path.
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: haproxy-haptic.org/path-rewrite bare whole-path form",
		Host:        "ingress-haptic-rewrite-bare.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/path-rewrite": `/backend`,
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "/svc rewrites to /backend at the backend",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/svc").ExpectEchoPath(t, "/backend")
				},
			},
			{
				Name: "/deep/nested/path also rewrites to /backend at the backend",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/deep/nested/path").ExpectEchoPath(t, "/backend")
				},
			},
		},
	})
}
