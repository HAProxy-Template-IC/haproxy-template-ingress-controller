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

// TestHTTPRoutePrecedence covers test_httproute_precedence: an HTTPRoute
// with rules declared out-of-order to verify the chart sorts by match
// specificity per the Gateway API spec. Rule order in declaration:
//
//	0. Medium: GET + X-Version=v1 → default
//	1. Catch-all: / → default
//	2. Highest: GET + X-Version=v2 + X-Environment=prod + ?debug=true → v2
//	3. Low: GET → v2
//
// Effective routing should be:
//   - GET + (v2 + prod + debug=true) → v2 (rule 2 wins on most criteria)
//   - GET + X-Version=v1            → default (rule 0 — explicit v1 match)
//   - GET only                      → v2 (rule 3 — only one matching rule)
//   - POST or other                 → default (rule 1 catch-all)
func TestHTTPRoutePrecedence(t *testing.T) {
	t.Parallel()

	host := "httproute-precedence.localdev.me"

	feature := features.New("HTTPRoute: match precedence").
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

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-precedence",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{
					// Rule 0: medium specific
					{PathType: "PathPrefix", Path: "/", Method: "GET",
						HeaderMatches: []HTTPRouteHeaderMatch{{Name: "X-Version", Value: "v1"}},
						BackendRefs:   []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
					// Rule 1: catch-all
					{PathType: "PathPrefix", Path: "/",
						BackendRefs: []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
					// Rule 2: most specific — should sort to top
					{PathType: "PathPrefix", Path: "/", Method: "GET",
						HeaderMatches: []HTTPRouteHeaderMatch{
							{Name: "X-Version", Value: "v2"},
							{Name: "X-Environment", Value: "prod"},
						},
						QueryMatches: []HTTPRouteQueryMatch{{Name: "debug", Value: "true"}},
						BackendRefs:  []HTTPRouteBackendRef{{Service: v2Backend.Service, Port: v2Backend.Port}}},
					// Rule 3: low
					{PathType: "PathPrefix", Path: "/", Method: "GET",
						BackendRefs: []HTTPRouteBackendRef{{Service: v2Backend.Service, Port: v2Backend.Port}}},
				},
			})
			return ctx
		}).
		// All four assertions poll on (status==200 AND Echo backend identity).
		// Polling on status alone leaves a race window where one HTTPRoute
		// rule has landed (the request gets a 200 from whatever backend it
		// hit) but the rule under test hasn't, so the response identifies a
		// different backend than expected.
		Assess("highest-precedence rule wins (GET + 2 headers + query)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/?debug=true").
				WithHeader("X-Version", "v2").
				WithHeader("X-Environment", "prod").
				ExpectMatching(t, "rule 2 (most specific) routes to v2 backend",
					func(resp *httpclient.Response) bool {
						return resp.Status == 200 && resp.Echo != nil && resp.Echo.Environment == "v2"
					})
			return ctx
		}).
		Assess("medium rule (GET + X-Version=v1) wins over catch-all", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").
				WithHeader("X-Version", "v1").
				ExpectMatching(t, "rule 0 (explicit v1 header) routes to default backend",
					func(resp *httpclient.Response) bool {
						return resp.Status == 200 && resp.Echo != nil && resp.Echo.Environment != "v2"
					})
			return ctx
		}).
		Assess("plain GET routes to v2 (rule 3, low specificity)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").
				ExpectMatching(t, "rule 3 (GET catch-all) routes to v2 backend",
					func(resp *httpclient.Response) bool {
						return resp.Status == 200 && resp.Echo != nil && resp.Echo.Environment == "v2"
					})
			return ctx
		}).
		Assess("POST falls all the way through to catch-all", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").WithMethod("POST").
				ExpectMatching(t, "rule 1 (catch-all) routes POST to default backend",
					func(resp *httpclient.Response) bool {
						return resp.Status == 200 && resp.Echo != nil && resp.Echo.Environment != "v2"
					})
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// TestHTTPRouteCombined covers test_httproute_combined: a single rule
// with all four matcher kinds (path + method + header + queryParam regex).
// Verifies the chart can compose them via AND.
func TestHTTPRouteCombined(t *testing.T) {
	t.Parallel()

	host := "httproute-combined.localdev.me"

	feature := features.New("HTTPRoute: combined matchers (path + method + header + query regex)").
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

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-combined",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{
					{PathType: "PathPrefix", Path: "/api", Method: "POST",
						HeaderMatches: []HTTPRouteHeaderMatch{{Name: "Content-Type", Value: "application/json"}},
						QueryMatches: []HTTPRouteQueryMatch{{
							Name: "token", Type: "RegularExpression", Value: "^secret[0-9]+$"}},
						BackendRefs: []HTTPRouteBackendRef{{Service: v2Backend.Service, Port: v2Backend.Port}}},
					{PathType: "PathPrefix", Path: "/",
						BackendRefs: []HTTPRouteBackendRef{{Service: defaultBackend.Service, Port: defaultBackend.Port}}},
				},
			})
			return ctx
		}).
		Assess("all matchers satisfied → v2", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/api?token=secret123").
				WithMethod("POST").
				WithHeader("Content-Type", "application/json").
				ExpectOK(t)
			if resp.Echo == nil || resp.Echo.Environment != "v2" {
				t.Fatalf("expected v2 backend with all matchers satisfied")
			}
			return ctx
		}).
		Assess("token regex mismatch → catch-all default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/api?token=wrongtoken").
				WithMethod("POST").
				WithHeader("Content-Type", "application/json").
				ExpectOK(t)
			if resp.Echo == nil || resp.Echo.Environment == "v2" {
				t.Fatalf("expected default backend (token regex didn't match)")
			}
			return ctx
		}).
		Assess("wrong content-type → catch-all default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/api?token=secret123").
				WithMethod("POST").
				WithHeader("Content-Type", "text/plain").
				ExpectOK(t)
			if resp.Echo == nil || resp.Echo.Environment == "v2" {
				t.Fatalf("expected default backend (Content-Type didn't match)")
			}
			return ctx
		}).
		Assess("wrong method → catch-all default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(host, "/api?token=secret123").
				WithHeader("Content-Type", "application/json").
				ExpectOK(t)
			if resp.Echo == nil || resp.Echo.Environment == "v2" {
				t.Fatalf("expected default backend (GET instead of POST)")
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

