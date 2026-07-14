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
	"bytes"
	"net/http"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticAllowedMethods verifies allowed-methods (64-gateway-security.yaml):
// a disallowed HTTP method is denied 405; an allowed one reaches the upstream.
func TestHapticAllowedMethods(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native allowed-methods gating",
		Host:        "ingress-haptic-methods.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/allowed-methods": "GET,HEAD",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "GET is allowed (200)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusOK)
				},
			},
			{
				Name: "POST is denied (405)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").WithMethod("POST").ExpectStatus(t, http.StatusMethodNotAllowed)
				},
			},
		},
	})
}

// TestHapticRequireHeaders verifies require-headers (64-gateway-security.yaml):
// a request missing a required header is denied 400; supplying it admits it.
func TestHapticRequireHeaders(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native require-headers gating",
		Host:        "ingress-haptic-requirehdr.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/require-headers": "X-Tenant-Id",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "missing required header is denied (400)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusBadRequest)
				},
			},
			{
				Name: "supplying the header admits the request (200)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").WithHeader("X-Tenant-Id", "acme").ExpectStatus(t, http.StatusOK)
				},
			},
		},
	})
}

// TestHapticMockResponse verifies mock-response (64-gateway-security.yaml):
// the route returns the canned body without reaching the upstream.
func TestHapticMockResponse(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native mock-response",
		Host:        "ingress-haptic-mock.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/mock-response":              `{"stub":true}`,
			"haproxy-haptic.org/mock-response-content-type": "application/json",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "returns the canned 200 body",
				Check: func(t *testing.T, host string) {
					resp := httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusOK)
					if !bytes.Contains(resp.Body, []byte(`{"stub":true}`)) {
						t.Fatalf("expected mock body, got: %s", string(resp.Body))
					}
					if resp.Echo != nil {
						t.Fatalf("mock-response should short-circuit the upstream, but got echo-server output")
					}
				},
			},
		},
	})
}

// TestHapticRequestTermination verifies the fixed-response annotation
// (64-gateway-security.yaml): every request on the route gets the fixed status.
func TestHapticRequestTermination(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native fixed-response",
		Host:        "ingress-haptic-termination.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/fixed-response":      "true",
			"haproxy-haptic.org/fixed-response-code": "503",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "returns the fixed 503",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusServiceUnavailable)
				},
			},
		},
	})
}
