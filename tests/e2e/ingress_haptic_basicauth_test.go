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

// TestHapticBasicAuth verifies that the haptic-annotations library's
// haproxy-haptic.org/auth-type=basic annotation gates the Ingress behind
// HTTP Basic auth, mirroring the haproxytech-library test
// (TestIngressBasicAuth) against haptic's canonical keys.
//
// Two checks:
//   - no credentials → 401
//   - admin:admin     → 200 (echo-server reached)
//
// The haptic fragment (50-auth-spoe.yaml) defaults auth-secret-type to
// "auth-file": the Secret carries a single `auth` data key holding an
// htpasswd file (one `username:hash` line each). The auth Secret is
// per-test (deleted with the namespace).
func TestHapticBasicAuth(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native HTTP Basic auth",
		Host:        "ingress-haptic-basicauth.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/auth-type":        "basic",
			"haproxy-haptic.org/auth-secret":      "echo-auth-secret",
			"haproxy-haptic.org/auth-realm":       "Echo-Server-Protected",
			"haproxy-haptic.org/auth-secret-type": "auth-file",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			// Pre-generated bcrypt hash for "admin" (admin/admin matches the
			// dev-env secret); regenerate with:
			//   htpasswd -nbB admin admin | cut -d: -f2
			adminBcrypt := "$2y$05$mN1WVk5Qnbg4QwdAdXbfz.8b3ceH6Q5KOVCKxR2IkNAfJgLi5pIKW"
			// auth-file format: one `username:hash` htpasswd line in the `auth` key.
			htpasswd := "admin:" + adminBcrypt + "\n"
			authSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "echo-auth-secret",
					Namespace: namespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{"auth": []byte(htpasswd)},
			}
			if err := client.Resources(namespace).Create(ctx, authSecret); err != nil {
				t.Fatalf("create auth secret: %v", err)
			}
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "returns 401 without credentials",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "returns 200 with admin:admin credentials",
				Check: func(t *testing.T, host string) {
					resp := httpclient.New(t).GET(host, "/").WithBasicAuth("admin", "admin").ExpectOK(t)
					if resp.Echo == nil {
						t.Fatalf("expected echo-server JSON after auth, got status=%d", resp.Status)
					}
				},
			},
		},
	})
}
