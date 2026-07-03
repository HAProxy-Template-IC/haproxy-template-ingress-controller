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

// TestHTTPRoutePaths covers test_httproute_paths: a single HTTPRoute with
// three rules — Exact match for /exact, PathPrefix for /api, and PathPrefix
// for / (catch-all). All three rules go to the same backend; we verify
// each rule type matches and routes through.
func TestHTTPRoutePaths(t *testing.T) {
	t.Parallel()
	host := "httproute-paths.localdev.me"
	var fwd GatewayForward

	feature := features.New("HTTPRoute: path matching variants").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "test-gateway")
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-paths",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{
					{PathType: "Exact", Path: "/exact",
						BackendRefs: []HTTPRouteBackendRef{{Service: backend.Service, Port: backend.Port}}},
					{PathType: "PathPrefix", Path: "/api",
						BackendRefs: []HTTPRouteBackendRef{{Service: backend.Service, Port: backend.Port}}},
					{PathType: "PathPrefix", Path: "/",
						BackendRefs: []HTTPRouteBackendRef{{Service: backend.Service, Port: backend.Port}}},
				},
			})
			return ctx
		}).
		Assess("Exact /exact matches", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/exact").ExpectOK(t)
			return ctx
		}).
		Assess("PathPrefix /api matches", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api/test").ExpectOK(t)
			return ctx
		}).
		Assess("Catch-all / matches", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectOK(t)
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// TestHTTPRouteMethods covers test_httproute_methods: routing by HTTP
// method. The chart's gateway library appears NOT to fall through to a
// less-specific rule when a more-specific rule matches the path but not
// the method (a partial match becomes a hard non-match, returning 404
// on the same path with a different method). The dev-env demo works
// around this by declaring an EXPLICIT rule per method and a final
// catch-all; we mirror that pattern here.
//
// If the chart's behaviour later aligns with upstream Gateway API
// semantics (rule with method=GET should *not* block POST from
// matching a later rule with no method constraint), the second-rule
// "POST /api → default" assertion can drop the explicit POST rule.
func TestHTTPRouteMethods(t *testing.T) {
	t.Parallel()
	host := "httproute-methods.localdev.me"
	var fwd GatewayForward

	feature := features.New("HTTPRoute: method matching").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			defaultBackend := NewEchoServerBackend(ctx, t, client, ns)
			v2Backend := NewEchoServerV2Backend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "test-gateway")
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-methods",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{
					{PathType: "PathPrefix", Path: "/api", Method: "POST",
						BackendRefs: []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
					{PathType: "PathPrefix", Path: "/api", Method: "GET",
						BackendRefs: []HTTPRouteBackendRef{{Service: v2Backend.Service, Port: v2Backend.Port}}},
					{PathType: "PathPrefix", Path: "/",
						BackendRefs: []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
				},
			})
			return ctx
		}).
		Assess("GET /api routes to v2 backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api").ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("POST /api routes to default backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api").WithMethod("POST").ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// TestHTTPRouteHeaders covers test_httproute_headers: header-based
// routing. Requests with X-Api-Version: v2 go to v2 backend; without
// that header, they fall through to the catch-all default.
func TestHTTPRouteHeaders(t *testing.T) {
	t.Parallel()
	host := "httproute-headers.localdev.me"
	var fwd GatewayForward

	feature := features.New("HTTPRoute: header matching").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			defaultBackend := NewEchoServerBackend(ctx, t, client, ns)
			v2Backend := NewEchoServerV2Backend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "test-gateway")
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-headers",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{
					{PathType: "PathPrefix", Path: "/api",
						HeaderMatches: []HTTPRouteHeaderMatch{{Name: "X-Api-Version", Value: "v2"}},
						BackendRefs:   []HTTPRouteBackendRef{{Service: v2Backend.Service, Port: v2Backend.Port}}},
					{PathType: "PathPrefix", Path: "/",
						BackendRefs: []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
				},
			})
			return ctx
		}).
		Assess("Request with X-Api-Version: v2 routes to v2 backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api").
				WithHeader("X-Api-Version", "v2").ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("Request without header falls through to default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// TestHTTPRouteQuery covers test_httproute_query: routing by query param.
func TestHTTPRouteQuery(t *testing.T) {
	t.Parallel()
	host := "httproute-query.localdev.me"
	var fwd GatewayForward

	feature := features.New("HTTPRoute: query parameter matching").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			defaultBackend := NewEchoServerBackend(ctx, t, client, ns)
			v2Backend := NewEchoServerV2Backend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "test-gateway")
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-query",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{
					{PathType: "PathPrefix", Path: "/api",
						QueryMatches: []HTTPRouteQueryMatch{{Name: "version", Value: "beta"}},
						BackendRefs:  []HTTPRouteBackendRef{{Service: v2Backend.Service, Port: v2Backend.Port}}},
					{PathType: "PathPrefix", Path: "/",
						BackendRefs: []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
				},
			})
			return ctx
		}).
		Assess("Request with ?version=beta routes to v2 backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api?version=beta").ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("Request without query falls through to default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
