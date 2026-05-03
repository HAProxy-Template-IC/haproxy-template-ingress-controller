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

// TestIngressRedirect verifies that the haproxy.org/request-redirect
// annotation produces a 302 Location response without ever reaching the
// backend.
func TestIngressRedirect(t *testing.T) {
	t.Parallel()
	const target = "https://echo.localdev.me"
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: request-redirect annotation",
		Host:        "ingress-redirect.localdev.me",
		Annotations: map[string]string{
			"haproxy.org/request-redirect":      target,
			"haproxy.org/request-redirect-code": "302",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "returns 302 with Location header",
			Check: func(t *testing.T, host string) {
				httpclient.New(t).GET(host, "/").ExpectRedirect(t, target)
			},
		}},
	})
}
