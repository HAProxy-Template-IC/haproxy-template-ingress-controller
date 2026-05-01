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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressAuthTLSSecretNginx mirrors TestIngressAuthTLSSecret but
// exercises the nginx.ingress.kubernetes.io/auth-tls-* code path in the
// nginx-ingress chart library. Same fixture shape (server cert + CA
// bundle + ingress with mTLS annotations), different annotation
// namespace. Covers nginx.ingress.kubernetes.io/auth-tls-secret,
// auth-tls-verify-client, auth-tls-error-page, and
// auth-tls-pass-certificate-to-upstream — all four annotations the
// nginx-ingress library renders into the chart's mTLS pipeline.
func TestIngressAuthTLSSecretNginx(t *testing.T) {
	t.Parallel()
	host := "ingress-nginx-auth-tls.localdev.me"

	bundle, err := generateMTLSBundle(host)
	if err != nil {
		t.Fatalf("generate mTLS bundle: %v", err)
	}

	feature := features.New("Ingress: client-mTLS via nginx auth-tls-secret").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			serverSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nginx-auth-tls-server-tls",
					Namespace: ns,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"tls.crt": bundle.ServerCertPEM,
					"tls.key": bundle.ServerKeyPEM,
				},
			}
			if err := client.Resources(ns).Create(ctx, serverSecret); err != nil {
				t.Fatalf("create server TLS secret: %v", err)
			}

			caSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nginx-auth-tls-client-ca",
					Namespace: ns,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"ca.crt": bundle.CACertPEM,
				},
			}
			if err := client.Resources(ns).Create(ctx, caSecret); err != nil {
				t.Fatalf("create client-CA secret: %v", err)
			}

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-nginx-auth-tls",
				Host:           host,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				TLSSecretName:  "nginx-auth-tls-server-tls",
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/auth-tls-secret":                       "nginx-auth-tls-client-ca",
					"nginx.ingress.kubernetes.io/auth-tls-verify-client":                "on",
					"nginx.ingress.kubernetes.io/auth-tls-error-page":                   "https://login.example.com/tls-error",
					"nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream": "false",
				},
			})
			return ctx
		}).
		Assess("valid client cert is admitted, request reaches backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).HTTPS(host, "/").
				WithClientCert(bundle.ClientCertPEM, bundle.ClientKeyPEM, bundle.CACertPEM).
				ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Assess("missing client cert is rejected at the TLS layer", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			_, err := httpclient.New(t).HTTPS(host, "/").Do(ctx)
			if err == nil {
				t.Fatalf("expected TLS handshake error without client cert, got success")
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
