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

// TestIngressBackendMTLS covers the chart's backend-side TLS-with-mTLS
// annotation triple — `haproxy.org/server-ssl` (turn on TLS to backend),
// `haproxy.org/server-ca` (CA secret used to verify the backend's cert),
// and `haproxy.org/server-crt` (client cert+key the chart presents to the
// backend). The fixture is a HAProxy backend with `verify required`
// configured against the same CA, so the request only succeeds if all
// three annotations wire correctly.
//
// This is the only integration-level signal we have for the
// haproxy.org/server-{ca,crt} pair; without it those annotations only
// reach the chart's render-time validationTests.
func TestIngressBackendMTLS(t *testing.T) {
	RequireVendorLibrary(t, "haproxytech")
	t.Parallel()
	host := "ingress-backend-mtls.localdev.me"

	feature := features.New("Ingress: backend mTLS via server-ssl + server-ca + server-crt").
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
				Name:           "echo-backend-mtls",
				Host:           host,
				Path:           "/",
				BackendService: mtls.HTTPS.Service,
				BackendPort:    mtls.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy.org/server-ssl": "true",
					"haproxy.org/server-ca":  mtls.CASecretName,
					"haproxy.org/server-crt": mtls.ClientCertSecretName,
				},
			})
			return ctx
		}).
		Assess("HAProxy presents client cert + verifies backend cert against CA → 200 from backend",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON via mTLS-verifying backend, got %d bytes", len(resp.Body))
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

// TestIngressBackendMTLSHaproxyIngress covers the same backend-mTLS
// flow as TestIngressBackendMTLS but via the haproxy-ingress.github.io/*
// annotation prefix. The chart's haproxy-ingress library wires the
// `secure-backends` family through a separate code path from
// haproxy.org/server-* — this test ensures that path actually presents
// the client cert and verifies the server cert.
func TestIngressBackendMTLSHaproxyIngress(t *testing.T) {
	RequireVendorLibrary(t, "haproxyIngress")
	t.Parallel()
	host := "ingress-hi-backend-mtls.localdev.me"

	feature := features.New("Ingress: backend mTLS via haproxy-ingress.github.io/secure-* family").
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
				Name:           "echo-hi-backend-mtls",
				Host:           host,
				Path:           "/",
				BackendService: mtls.HTTPS.Service,
				BackendPort:    mtls.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy-ingress.github.io/secure-backends":         "true",
					"haproxy-ingress.github.io/secure-verify-ca-secret": mtls.CASecretName,
					"haproxy-ingress.github.io/secure-crt-secret":       mtls.ClientCertSecretName,
					"haproxy-ingress.github.io/secure-sni":              host,
					"haproxy-ingress.github.io/secure-verify-hostname":  host,
				},
			})
			return ctx
		}).
		Assess("haproxy-ingress secure-* annotations wire mTLS through to the backend",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON via secure-* mTLS path, got %d bytes", len(resp.Body))
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
