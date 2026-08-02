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
//  0. Medium: GET + X-Version=v1 → default
//  1. Catch-all: / → default
//  2. Highest: GET + X-Version=v2 + X-Environment=prod + ?debug=true → v2
//  3. Low: GET → v2
//
// Effective routing should be:
//   - GET + (v2 + prod + debug=true) → v2 (rule 2 wins on most criteria)
//   - GET + X-Version=v1            → default (rule 0 — explicit v1 match)
//   - GET only                      → v2 (rule 3 — only one matching rule)
//   - POST or other                 → default (rule 1 catch-all)
func TestHTTPRoutePrecedence(t *testing.T) {
	t.Parallel()

	host := "httproute-precedence.localdev.me"
	var fwd GatewayForward

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
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

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

			// Gate on the controller deploying THIS route to every HAProxy pod
			// before asserting. The marker is route-gated (issue #71): the bare
			// namespace already enters spec.Content via the Gateway's
			// route-independent typed-access-smoke comment (rendered when the
			// Gateway is created, before this route), so it would pass off a
			// pre-route render and race the route's own throttled deploy. The
			// fragment "gtw_<ns>_echo-precedence_" appears only once this route's
			// backends render; <ns> is unique per test.
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, "echo-precedence")
			return ctx
		}).
		// All four assertions poll on (status==200 AND Echo backend identity).
		// Polling on status alone leaves a race window where one HTTPRoute
		// rule has landed (the request gets a 200 from whatever backend it
		// hit) but the rule under test hasn't, so the response identifies a
		// different backend than expected.
		Assess("highest-precedence rule wins (GET + 2 headers + query)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/?debug=true").
				WithHeader("X-Version", "v2").
				WithHeader("X-Environment", "prod").
				ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("medium rule (GET + X-Version=v1) wins over catch-all", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").
				WithHeader("X-Version", "v1").
				ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Assess("plain GET routes to v2 (rule 3, low specificity)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("POST falls all the way through to catch-all", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").WithMethod("POST").ExpectEchoEnvironment(t, "")
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
	var fwd GatewayForward

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
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

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

			// Gate on the controller deploying THIS route to every HAProxy pod
			// before asserting. The marker is route-gated (issue #71): the bare
			// namespace already enters spec.Content via the Gateway's
			// route-independent typed-access-smoke comment (rendered when the
			// Gateway is created, before this route), so it would pass off a
			// pre-route render and race the route's own throttled deploy. The
			// fragment "gtw_<ns>_echo-combined_" appears only once this route's
			// backends render; <ns> is unique per test.
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, "echo-combined")
			return ctx
		}).
		Assess("all matchers satisfied → v2", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api?token=secret123").
				WithMethod("POST").
				WithHeader("Content-Type", "application/json").
				ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("token regex mismatch → catch-all default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api?token=wrongtoken").
				WithMethod("POST").
				WithHeader("Content-Type", "application/json").
				ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Assess("wrong content-type → catch-all default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api?token=secret123").
				WithMethod("POST").
				WithHeader("Content-Type", "text/plain").
				ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Assess("wrong method → catch-all default", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/api?token=secret123").
				WithHeader("Content-Type", "application/json").
				ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
