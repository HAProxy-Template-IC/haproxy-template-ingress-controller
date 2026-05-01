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

// TestHTTPRouteBasic covers the bash test_httproute_basic: a Gateway
// with an HTTPRoute attached, routing all traffic for one host to the
// echo-server backend. Mirrors TestIngressBasic but via Gateway API
// resources.
//
// First HTTPRoute test in the suite — if it passes, the Gateway API CRDs
// are installed correctly, the chart's gateway library is wired, and the
// per-test Gateway/HTTPRoute fixtures work. Subsequent HTTPRoute tests
// reuse the same Setup pattern.
func TestHTTPRouteBasic(t *testing.T) {
	t.Parallel()

	host := "httproute-basic.localdev.me"

	feature := features.New("HTTPRoute: basic routing").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "test-gateway")
			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-basic",
				GatewayName: "test-gateway",
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
		Assess(host+" returns 200 from echo-server", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
