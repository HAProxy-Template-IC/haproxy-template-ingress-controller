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
	"context"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressCombined covers test_ingress_combined: an Ingress that
// stacks several annotations at once (auth + allowlist + rate-limit +
// response-set-header + load-balance + cookie-persistence + timeout)
// is the most realistic chart-stress test. We verify two things:
//   - request without auth → 401 (auth gate triggers)
//   - request with auth → 200 + X-Frame-Options: DENY (response-set-header
//     applies on the authed path, not just the rejected one)
//
// This is the "do many features compose without trampling each other"
// canary; if any single annotation breaks the chain the test fails clearly.
func TestIngressCombined(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: combined annotations",
		Host:        "ingress-combined.localdev.me",
		Annotations: map[string]string{
			"haproxy.org/auth-type":           "basic-auth",
			"haproxy.org/auth-secret":         "echo-auth-secret",
			"haproxy.org/allowlist":           "10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16",
			"haproxy.org/rate-limit-requests": "100",
			"haproxy.org/rate-limit-period":   "1m",
			"haproxy.org/response-set-header": "X-Frame-Options DENY\nX-Content-Type-Options nosniff",
			"haproxy.org/load-balance":        "leastconn",
			"haproxy.org/cookie-persistence":  "SERVERID",
			"haproxy.org/timeout-server":      "30s",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			adminBcrypt := "$2y$05$mN1WVk5Qnbg4QwdAdXbfz.8b3ceH6Q5KOVCKxR2IkNAfJgLi5pIKW"
			authSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "echo-auth-secret", Namespace: namespace},
				Type:       corev1.SecretTypeOpaque,
				Data:       map[string][]byte{"admin": []byte(adminBcrypt)},
			}
			if err := client.Resources(namespace).Create(ctx, authSecret); err != nil {
				t.Fatalf("create auth secret: %v", err)
			}
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "returns 401 without auth (auth gate triggers)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "returns 200 with auth, security headers stack",
				Check: func(t *testing.T, host string) {
					resp := httpclient.New(t).GET(host, "/").
						WithBasicAuth("admin", "admin").ExpectOK(t)
					if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
						t.Fatalf("expected X-Frame-Options: DENY, got %q", got)
					}
					if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
						t.Fatalf("expected X-Content-Type-Options: nosniff, got %q", got)
					}
				},
			},
		},
	})
}
