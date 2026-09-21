// Copyright 2026 Philipp Hossner
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

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestBundledGatewayPolicyActivationTransitions(t *testing.T) {
	cfg, setup, logger, cleanup := bundledChartSetup(t)
	t.Cleanup(cleanup)
	for _, kind := range []string{"HTTPRoute", "GRPCRoute"} {
		t.Run(kind, func(t *testing.T) {
			storeName := strings.ToLower(kind) + "s"
			fixtures := benchHTTPRouteScaleFixturesShaped(cfg, 1, benchRoutePlain)
			fixtures["httproutes"] = nil
			fixtures[storeName] = []any{gatewayPolicyActivationRoute(kind, nil, false)}
			credentials := cfg.ValidationTests["test-gateway-policy-authentication"].Fixtures
			fixtures["secrets"] = slices.Concat(fixtures["secrets"], credentials["secrets"])
			fixtures["routepolicies"] = credentials["routepolicies"]
			storeMap, err := createStoresForBenchmark(cfg, setup.Engine, fixtures)
			require.NoError(t, err)
			provider := stores.NewRealStoreProvider(storeMap)
			engine := newBundledActivationCountingEngine(t, setup.Engine)
			lifecycle := newIncrementalBenchmarkCacheLifecycle(nil)
			service := newBundledIncrementalBenchmarkService(cfg, setup, engine, logger, lifecycle)
			baseline, err := runIncrementalBenchmarkRenderCacheReady(t.Context(), service, provider, lifecycle)
			require.NoError(t, err)
			suffix := "http"
			if kind == "GRPCRoute" {
				suffix = "grpc"
			}
			for _, component := range []string{
				"gateway-policy-records-", "gateway-policy-errors-", "map-haptic-api-key-routes-861-gateway-",
			} {
				require.Zero(t, engine.componentCounts()[component+suffix], component)
			}

			for _, test := range []struct {
				name    string
				filter  map[string]any
				backend bool
				error   string
			}{
				{name: "valid policy", filter: gatewayPolicyActivationFilter("haproxy-haptic.org", "HAProxyRoutePolicy", "api")},
				{name: "missing policy", filter: gatewayPolicyActivationFilter("haproxy-haptic.org", "HAProxyRoutePolicy", "missing"), error: "is missing"},
				{name: "unnamed policy", filter: gatewayPolicyActivationFilter("haproxy-haptic.org", "HAProxyRoutePolicy", ""), error: "requires a name"},
				{name: "unsupported extension", filter: gatewayPolicyActivationFilter("example.com", "Unknown", "extension"), error: "is unsupported"},
				{name: "backend policy", filter: gatewayPolicyActivationFilter("haproxy-haptic.org", "HAProxyRoutePolicy", "api"), backend: true, error: "attached to a backend"},
			} {
				t.Run(test.name, func(t *testing.T) {
					require.NoError(t, storeMap[storeName].Update(
						gatewayPolicyActivationRoute(kind, test.filter, test.backend), []string{"default", "route-0"},
					))
					changed, err := runIncrementalBenchmarkRenderResult(service, provider)
					require.NoError(t, err)
					freshLifecycle := newIncrementalBenchmarkCacheLifecycle(nil)
					fresh := newBundledIncrementalBenchmarkService(cfg, setup, setup.Engine, logger, freshLifecycle)
					oracle, err := runIncrementalBenchmarkRenderCacheReady(t.Context(), fresh, provider, freshLifecycle)
					require.NoError(t, err)
					require.Equal(t, bundledRenderAcrossServices(t, oracle), bundledRenderAcrossServices(t, changed))
					assertGatewayPolicyActivationResult(t, changed, kind, test.error)
					if test.error != "" {
						_, err = fresh.Render(t.Context(), provider, rendercontext.RenderModeAdmission)
						require.ErrorContains(t, err, test.error)
					}
					require.NoError(t, storeMap[storeName].Update(
						gatewayPolicyActivationRoute(kind, nil, false), []string{"default", "route-0"},
					))
					removed, err := runIncrementalBenchmarkRenderResult(service, provider)
					require.NoError(t, err)
					require.Equal(t, bundledRenderAcrossServices(t, baseline), bundledRenderAcrossServices(t, removed))
				})
			}
		})
	}
}

func gatewayPolicyActivationRoute(kind string, filter map[string]any, backend bool) map[string]any {
	route := benchHTTPRouteContentShaped("route-0", "svc-0", benchRoutePlain)
	route["kind"] = kind
	rule := route["spec"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if kind == "GRPCRoute" {
		delete(rule, "matches")
	}
	if filter != nil {
		target := rule
		if backend {
			target = rule["backendRefs"].([]any)[0].(map[string]any)
		}
		target["filters"] = []any{filter}
	}
	return route
}

func gatewayPolicyActivationFilter(group, kind, name string) map[string]any {
	return map[string]any{
		"type":         "ExtensionRef",
		"extensionRef": map[string]any{"group": group, "kind": kind, "name": name},
	}
}

func assertGatewayPolicyActivationResult(t *testing.T, result *renderer.RenderResult, kind, expectedError string) {
	t.Helper()
	snapshot := bundledRenderBytes(t, result)
	identity := "h_default_route-0_0"
	if kind == "GRPCRoute" {
		identity = "g_default_route-0_0"
	}
	if expectedError == "" {
		require.Contains(t, snapshot.Files["map:haptic-api-key-routes.map"], "gateway/"+identity)
		return
	}
	require.Contains(t, snapshot.Files["map:gw-policy-errors.map"], identity)
	require.Contains(t, result.HAProxyConfig, "http-request deny deny_status 503")
}
