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

// TestHapticProxyProtocol is the haptic-native counterpart of
// TestIngressProxyProtocol. The haproxy-haptic.org/proxy-protocol annotation
// (value proxy-v2 -> send-proxy-v2, per the backend-directives fragment) makes
// HAProxy prepend PROXY protocol v2 headers to backend connections. We verify
// the route works: haproxy-demo-backend's port 8080 binds with `accept-proxy`,
// so without the PROXY header prefix it would reject the connection. A 200 with
// the echo-server JSON body proves the backend received and accepted the PROXY
// header.
func TestHapticProxyProtocol(t *testing.T) {
	t.Parallel()
	host := "ingress-haptic-proxyproto.localdev.me"

	feature := features.New("Ingress: haproxy-haptic.org/proxy-protocol annotation").
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
				Name:           "echo-haptic-proxyproto",
				Host:           host,
				Path:           "/",
				BackendService: demo.HTTPProxyProtocol.Service,
				BackendPort:    demo.HTTPProxyProtocol.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/proxy-protocol": "proxy-v2",
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
