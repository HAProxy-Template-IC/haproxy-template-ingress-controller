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

// TestIngressProxyProtocol covers test_ingress_proxy_protocol: the
// haproxy.org/send-proxy-protocol-v2 annotation makes HAProxy prepend
// PROXY protocol v2 headers to backend connections. We verify the route
// works (haproxy-demo-backend's port 8080 binds with `accept-proxy` —
// without the annotation it would reject the connection).
func TestIngressProxyProtocol(t *testing.T) {
	t.Parallel()
	host := "ingress-proxy-protocol.localdev.me"

	feature := features.New("Ingress: proxy-protocol annotation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echoBackend := NewEchoServerBackend(ctx, t, client, ns)
			demo := NewHAProxyDemoBackend(ctx, t, client, ns, echoBackend, host)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-proxy-protocol",
				Host:           host,
				Path:           "/",
				BackendService: demo.HTTPProxyProtocol.Service,
				BackendPort:    demo.HTTPProxyProtocol.Port,
				Annotations: map[string]string{
					"haproxy.org/send-proxy-protocol": "proxy-v2",
				},
			})
			return ctx
		}).
		Assess("PROXY-protocol-aware backend serves the request", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON via demo-backend, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// TestIngressBackendSSL covers test_ingress_backend_ssl: the
// haproxy.org/server-ssl annotation makes HAProxy use HTTPS to backend.
// Routes through haproxy-demo-backend's port 8443 (TLS-terminating).
func TestIngressBackendSSL(t *testing.T) {
	t.Parallel()
	host := "ingress-backend-ssl.localdev.me"

	feature := features.New("Ingress: server-ssl annotation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echoBackend := NewEchoServerBackend(ctx, t, client, ns)
			demo := NewHAProxyDemoBackend(ctx, t, client, ns, echoBackend, host)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-backend-ssl",
				Host:           host,
				Path:           "/",
				BackendService: demo.HTTPS.Service,
				BackendPort:    demo.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy.org/server-ssl": "true",
				},
			})
			return ctx
		}).
		Assess("HTTPS-to-backend route reaches the backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON via TLS-terminating demo-backend, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// TestIngressSSLPassthrough covers test_ingress_ssl_passthrough: the
// haproxy.org/ssl-passthrough annotation makes HAProxy route by SNI
// without terminating TLS. The TLS handshake happens at the backend
// (haproxy-demo-backend's HTTPS listener); the chart's frontend just
// forwards encrypted bytes.
func TestIngressSSLPassthrough(t *testing.T) {
	t.Parallel()
	host := "ingress-ssl-passthrough.localdev.me"

	feature := features.New("Ingress: ssl-passthrough annotation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echoBackend := NewEchoServerBackend(ctx, t, client, ns)
			demo := NewHAProxyDemoBackend(ctx, t, client, ns, echoBackend, host)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-ssl-passthrough",
				Host:           host,
				Path:           "/",
				BackendService: demo.HTTPS.Service,
				BackendPort:    demo.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy.org/ssl-passthrough": "true",
				},
			})
			return ctx
		}).
		Assess("HTTPS request passes through to the backend that terminates TLS", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON via passthrough, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
