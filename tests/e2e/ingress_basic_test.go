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

// TestIngressBasic is the foundational smoke test: an Ingress with no
// annotations, plain HTTP on the chart's NodePort, hitting echo-server.
// If this passes across the HAProxy version matrix, the e2e suite's
// fixture wiring is sound and richer behavioural tests can layer on
// top.
func TestIngressBasic(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: basic routing",
		Host:        "ingress-basic.localdev.me",
		Assess: []SimpleIngressAssertion{{
			Name: "host returns 200 from echo-server",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON body, got %d bytes: %s", len(resp.Body), string(resp.Body))
				}
			},
		}},
	})
}
