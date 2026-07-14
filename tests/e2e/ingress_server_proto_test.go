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

// TestIngressServerProtoH2 covers haproxy.org/server-proto: setting it to
// "h2" makes HAProxy speak HTTP/2 to the backend. The fixture is
// haproxy-demo-backend's TLS frontend, which now advertises ALPN
// "h2,http/1.1" — so HAProxy negotiates HTTP/2 against it via TLS.
//
// Combined with server-ssl=true so the backend connection is over TLS
// (HAProxy needs the TLS layer to negotiate ALPN).
func TestIngressServerProtoH2(t *testing.T) {
	RequireVendorLibrary(t, "haproxytech")
	t.Parallel()
	host := "ingress-server-proto-h2.localdev.me"

	feature := features.New("Ingress: server-proto h2 (HTTP/2 to backend)").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echo := NewEchoServerBackend(ctx, t, client, ns)
			demo := NewHAProxyDemoBackend(ctx, t, client, ns, echo, host)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-server-proto-h2",
				Host:           host,
				Path:           "/",
				BackendService: demo.HTTPS.Service,
				BackendPort:    demo.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy.org/server-ssl":   "true",
					"haproxy.org/server-proto": "h2",
				},
			})
			return ctx
		}).
		Assess("HAProxy speaks h2 to TLS-terminating backend → 200 from echo via demo",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON via h2 to backend, got %d bytes", len(resp.Body))
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
