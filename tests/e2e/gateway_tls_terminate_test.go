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

// TestGatewayTLSTerminate covers test_gateway_tls_terminate: a Gateway
// with an HTTPS listener in TLS Terminate mode, an HTTPRoute attached
// for the same host, and HTTPS request via SNI. Mirror of TestIngressTLS
// but via Gateway API.
func TestGatewayTLSTerminate(t *testing.T) {
	t.Parallel()
	host := "gateway-tls-terminate.localdev.me"

	feature := features.New("Gateway: HTTPS listener with TLS Terminate mode").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewTLSSecret(ctx, t, client, ns, "gateway-tls-cert", []string{host})
			NewHTTPSGateway(ctx, t, ns, "tls-gateway", "gateway-tls-cert")
			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-gateway-tls",
				GatewayName: "tls-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{{
					PathType: "PathPrefix",
					Path:     "/",
					BackendRefs: []HTTPRouteBackendRef{{
						Service: backend.Service,
						Port:    backend.Port,
					}},
				}},
			})
			return ctx
		}).
		Assess(host+" returns 200 over HTTPS through the gateway", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
