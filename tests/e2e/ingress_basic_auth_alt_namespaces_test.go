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

// adminBcrypt is the bcrypt hash for password "admin", regenerated with:
//
//	htpasswd -nbB admin admin | cut -d: -f2
//
// Shared by the basic-auth Secret fixtures across the three annotation
// namespaces (haproxytech / haproxy-ingress / nginx) so each library's
// auth-secret code path can be exercised end-to-end.
const adminBcrypt = "$2y$05$mN1WVk5Qnbg4QwdAdXbfz.8b3ceH6Q5KOVCKxR2IkNAfJgLi5pIKW"

// createBasicAuthSecret creates the haproxy.org/haproxy-ingress.github.io
// flavoured auth secret: data keyed by username, value is the bcrypt hash.
// This is the format the haproxytech / haproxy-ingress libraries expect.
func createBasicAuthSecret(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "echo-auth-secret",
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"admin": []byte(adminBcrypt)},
	}
	if err := client.Resources(namespace).Create(ctx, authSecret); err != nil {
		t.Fatalf("create auth secret: %v", err)
	}
}

// createNginxAuthSecret creates the nginx.ingress.kubernetes.io flavoured
// auth secret: a single `auth` key containing htpasswd-style entries
// (one `username:bcrypt` per line). The chart's nginx-ingress library
// rejects the haproxy.org username-keyed shape because the format is
// different upstream. See ingresses.haproxytech.yaml vs nginx-ingress.yaml
// for the contrast.
func createNginxAuthSecret(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()
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
		t.Fatalf("create nginx auth secret: %v", err)
	}
}

// TestIngressBasicAuthHaproxyIngress covers the haproxy-ingress.github.io
// auth-type/auth-secret/auth-realm code path — the same behaviour as
// TestIngressBasicAuth but exercising the haproxy-ingress library's
// independent annotation parsing.
func TestIngressBasicAuthHaproxyIngress(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HTTP Basic auth via haproxy-ingress.github.io",
		Host:        "ingress-hi-basic-auth.localdev.me",
		Annotations: map[string]string{
			"haproxy-ingress.github.io/auth-type":   "basic",
			"haproxy-ingress.github.io/auth-secret": "echo-auth-secret",
			"haproxy-ingress.github.io/auth-realm":  "Echo-Server-Protected",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			createBasicAuthSecret(ctx, t, client, namespace)
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

// TestIngressBasicAuthNginx covers the nginx.ingress.kubernetes.io
// auth-type/auth-secret/auth-realm code path. Same fixture as
// TestIngressBasicAuth but exercising the nginx-ingress library's
// independent annotation parsing.
func TestIngressBasicAuthNginx(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HTTP Basic auth via nginx.ingress.kubernetes.io",
		Host:        "ingress-nginx-basic-auth.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/auth-type":   "basic",
			"nginx.ingress.kubernetes.io/auth-secret": "echo-auth-secret",
			"nginx.ingress.kubernetes.io/auth-realm":  "Echo-Server-Protected",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			// nginx.ingress's auth-secret expects htpasswd format under
			// the `auth` data key, not the haproxy.org username-keyed shape.
			createNginxAuthSecret(ctx, t, client, namespace)
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
