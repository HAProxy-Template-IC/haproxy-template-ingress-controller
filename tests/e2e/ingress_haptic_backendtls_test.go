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

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticBackendTLS covers the HAPTIC-native backend-TLS annotation family
// (haproxy-haptic.org/backend-*) end-to-end. It is the haptic-prefixed
// counterpart of TestIngressBackendMTLS (haproxy.org/server-*) and
// TestIngressBackendSSL / TestIngressServerProtoH2 (haproxy.org/server-ssl,
// server-proto): all prove HAProxy speaks TLS to the upstream, verifying and
// presenting certificates.
//
// The fixture is NewHAProxyMTLSBackend — a TLS-terminating upstream configured
// with `verify required` against a private CA. Reaching it therefore forces the
// full backend-TLS leg to succeed:
//   - haproxy-haptic.org/backend-protocol: https  → TLS to the upstream (h1-ssl)
//   - haproxy-haptic.org/backend-verify:   on     → verify the upstream cert
//     (fail-closed without a CA)
//   - haproxy-haptic.org/backend-ca-secret        → ca.crt used to verify it
//   - haproxy-haptic.org/backend-crt-secret       → client cert+key presented
//     to the upstream (mTLS)
//   - haproxy-haptic.org/backend-sni:      host   → forward Host as SNI
//     (sni req.hdr(host))
//
// If any one of these mis-wires, the upstream's `verify required` rejects the
// connection and the request fails — a 200 with echo JSON proves all five
// annotations combined to establish a verified mTLS connection to the backend.
//
// The mTLS fixture's TLS frontend advertises no `alpn h2`, so this test drives
// the h1-ssl (`https`) protocol; the h2/grpcs variants of backend-protocol
// share the identical render path (both add `proto h2` on top of the same TLS
// flags) and are covered by the chart's render-time validationTests.
func TestHapticBackendTLS(t *testing.T) {
	t.Parallel()
	host := "ingress-haptic-backendtls.localdev.me"

	feature := features.New("Ingress: haptic backend-TLS (backend-protocol + verify + ca/crt-secret + sni)").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echo := NewEchoServerBackend(ctx, t, client, ns)
			mtls := NewHAProxyMTLSBackend(ctx, t, client, ns, echo, host)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-haptic-backendtls",
				Host:           host,
				Path:           "/",
				BackendService: mtls.HTTPS.Service,
				BackendPort:    mtls.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/backend-protocol":   "https",
					"haproxy-haptic.org/backend-verify":     "on",
					"haproxy-haptic.org/backend-ca-secret":  mtls.CASecretName,
					"haproxy-haptic.org/backend-crt-secret": mtls.ClientCertSecretName,
					"haproxy-haptic.org/backend-sni":        "host",
				},
			})
			return ctx
		}).
		Assess("haptic backend-* annotations establish a verified mTLS connection to the upstream → 200 from echo",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON via verified backend TLS, got %d bytes", len(resp.Body))
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
