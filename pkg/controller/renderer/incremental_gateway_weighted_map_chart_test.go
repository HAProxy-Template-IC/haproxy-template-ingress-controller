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

package renderer

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	gatewayHTTPWeightedMapComponent = "map-weighted-backend-510-gateway-http"
	gatewayGRPCWeightedMapComponent = "map-weighted-backend-520-gateway-grpc"
)

const gatewayWeightedMapRoot = `{{ render_glob "map-weighted-backend-*" }}`

type gatewayWeightedMapFixture struct {
	config          *config.Config
	service         *RenderService
	httpRoutes      *k8sstore.MemoryStore
	grpcRoutes      *k8sstore.MemoryStore
	services        *k8sstore.MemoryStore
	referenceGrants *k8sstore.MemoryStore
	provider        stores.StoreProvider
}

func TestGatewayWeightedMapExecutionScaling(t *testing.T) {
	for _, routeCount := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("routes=%d", routeCount), func(t *testing.T) {
			fixture := newGatewayWeightedMapFixture(t)
			fixture.addService(t, "echo")
			fixture.addService(t, "other")
			fixture.addService(t, "unrelated")
			for index := range routeCount {
				name := fmt.Sprintf("route-%06d", index)
				backends := []string{"echo", "echo"}
				if index == 0 {
					backends = []string{"echo", "other"}
				}
				require.NoError(t, fixture.httpRoutes.Add(
					gatewayWeightedMapRoute("HTTPRoute", name, backends...),
					[]string{"default", name},
				))
			}

			cold := fixture.renderAndCommitCacheReady(t)
			assert.Contains(t, cold, "0:default_route-000000_0 gtw_default_route-000000_echo_80")
			assert.Contains(t, cold, "1:default_route-000000_0 gtw_default_route-000000_other_80")
			last := fmt.Sprintf("route-%06d", routeCount-1)
			targetExecutions := fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", "route-000000")
			lastExecutions := fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", last)
			require.Equal(t, uint64(1), targetExecutions)
			require.Equal(t, uint64(1), lastExecutions)

			assert.Equal(t, cold, fixture.renderAndCommitCacheReady(t))
			assert.Equal(t, targetExecutions,
				fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", "route-000000"))
			assert.Equal(t, lastExecutions,
				fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", last))

			require.NoError(t, fixture.services.Update(
				gatewayWeightedMapService("unrelated", map[string]any{"changed": "true"}),
				[]string{"default", "unrelated"},
			))
			assert.Equal(t, cold, fixture.renderAndCommitCacheReady(t))
			assert.Equal(t, targetExecutions,
				fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", "route-000000"))

			changedRoute := gatewayWeightedMapRoute("HTTPRoute", "route-000000", "other", "echo", "echo")
			require.NoError(t, fixture.httpRoutes.Update(changedRoute, []string{"default", "route-000000"}))
			changed := fixture.renderAndCommitCacheReady(t)
			assert.Contains(t, changed, "2:default_route-000000_0 gtw_default_route-000000_echo_80")
			assert.Equal(t, targetExecutions+1,
				fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", "route-000000"))
			assert.Equal(t, lastExecutions,
				fixture.executions(gatewayHTTPWeightedMapComponent, "httproutes", last))

			oracle := newGatewayWeightedMapFixture(t)
			oracle.provider = fixture.provider
			assert.Equal(t, changed, oracle.renderAndCommitCacheReady(t))
		})
	}
}

func TestGatewayWeightedMapHTTPAndGRPCBindingsRetireIndependently(t *testing.T) {
	fixture := newGatewayWeightedMapFixture(t)
	fixture.addService(t, "echo")
	require.NoError(t, fixture.httpRoutes.Add(
		gatewayWeightedMapRoute("HTTPRoute", "same", "echo", "echo"),
		[]string{"default", "same"},
	))
	require.NoError(t, fixture.grpcRoutes.Add(
		gatewayWeightedMapRoute("GRPCRoute", "same", "echo", "echo"),
		[]string{"default", "same"},
	))

	both := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, both, "# HTTPRoute: default/same")
	assert.Contains(t, both, "# GRPCRoute: default/same")
	require.Equal(t, uint64(1), fixture.executions(
		gatewayHTTPWeightedMapComponent, "httproutes", "same"))
	require.Equal(t, uint64(1), fixture.executions(
		gatewayGRPCWeightedMapComponent, "grpcroutes", "same"))

	require.NoError(t, fixture.httpRoutes.Delete("default", "same", []string{"default", "same"}))
	grpcOnly := fixture.renderAndCommitCacheReady(t)
	assert.NotContains(t, grpcOnly, "# HTTPRoute: default/same")
	assert.Contains(t, grpcOnly, "# GRPCRoute: default/same")
	require.Equal(t, uint64(1), fixture.executions(
		gatewayGRPCWeightedMapComponent, "grpcroutes", "same"))

	require.NoError(t, fixture.httpRoutes.Add(
		gatewayWeightedMapRoute("HTTPRoute", "same", "echo", "echo"),
		[]string{"default", "same"},
	))
	assert.Equal(t, both, fixture.renderAndCommitCacheReady(t))
	require.Equal(t, uint64(1), fixture.executions(
		gatewayHTTPWeightedMapComponent, "httproutes", "same"))
}

func TestGatewayWeightedMapColdMatchesWholeStoreGenerator(t *testing.T) {
	current := newGatewayWeightedMapFixture(t)
	current.addService(t, "echo")
	current.addService(t, "other")
	require.NoError(t, current.httpRoutes.Add(
		gatewayWeightedMapRoute("HTTPRoute", "http", "echo", "other", "echo"),
		[]string{"default", "http"},
	))
	require.NoError(t, current.grpcRoutes.Add(
		gatewayWeightedMapRoute("GRPCRoute", "grpc", "other", "echo"),
		[]string{"default", "grpc"},
	))
	require.NoError(t, current.httpRoutes.Add(
		gatewayWeightedMapRoute("HTTPRoute", "invalid", "missing", "echo"),
		[]string{"default", "invalid"},
	))

	legacySnippets := loadGatewayWeightedMapSnippets(t)
	delete(legacySnippets, "map-weighted-backend-500-gateway")
	delete(legacySnippets, gatewayHTTPWeightedMapComponent)
	delete(legacySnippets, "map-weighted-backend-515-gateway-separator")
	delete(legacySnippets, gatewayGRPCWeightedMapComponent)
	legacySnippets["legacy-weighted-map"] = config.TemplateSnippet{
		Name: "legacy-weighted-map", Requires: []string{"httproutes", "grpcroutes", "referencegrants", "services"},
		Template: `{%- import "util-generate-httproute-weighted-backend-map-gateway" for GenerateHTTPRouteWeightedBackendMap -%}
{%- import "util-generate-grpcroute-weighted-backend-map-gateway" for GenerateGRPCRouteWeightedBackendMap -%}
# gateway/map-weighted-backend-gateway
{{ GenerateHTTPRouteWeightedBackendMap(resources.httproutes.List()) }}
{{ GenerateGRPCRouteWeightedBackendMap(resources.grpcroutes.List()) }}`,
	}
	legacy := newGatewayWeightedMapFixtureWithTemplates(t, legacySnippets, `{{ render "legacy-weighted-map" }}`)
	legacy.provider = current.provider

	assert.Equal(t, legacy.renderAndCommitCacheReady(t), current.renderAndCommitCacheReady(t))
}

func newGatewayWeightedMapFixture(t *testing.T) *gatewayWeightedMapFixture {
	t.Helper()
	return newGatewayWeightedMapFixtureWithTemplates(t, loadGatewayWeightedMapSnippets(t), gatewayWeightedMapRoot)
}

func newGatewayWeightedMapFixtureWithTemplates(
	t *testing.T,
	snippets map[string]config.TemplateSnippet,
	root string,
) *gatewayWeightedMapFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"httproutes":      {APIVersion: "gateway.networking.k8s.io/v1", Resources: "httproutes", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"grpcroutes":      {APIVersion: "gateway.networking.k8s.io/v1", Resources: "grpcroutes", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"services":        {APIVersion: "v1", Resources: "services", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"referencegrants": {APIVersion: "gateway.networking.k8s.io/v1", Resources: "referencegrants", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
		TemplateSnippets: snippets,
		HAProxyConfig:    config.HAProxyConfig{Template: root},
	}
	require.NoError(t, config.ValidateTemplateStructure(cfg))
	types := gatewayWeightedMapSchemaTypes(t)
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	fixture := &gatewayWeightedMapFixture{
		config: cfg, service: service,
		httpRoutes: k8sstore.NewMemoryStore(2), grpcRoutes: k8sstore.NewMemoryStore(2),
		services: k8sstore.NewMemoryStore(2), referenceGrants: k8sstore.NewMemoryStore(2),
	}
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"httproutes": fixture.httpRoutes, "grpcroutes": fixture.grpcRoutes,
		"services": fixture.services, "referencegrants": fixture.referenceGrants,
	})
	return fixture
}

func gatewayWeightedMapSchemaTypes(t *testing.T) *typebootstrap.Result {
	t.Helper()
	all := gatewayBackendSchemaTypes(t)
	result := &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	}
	for _, alias := range []string{"httproutes", "grpcroutes", "services", "referencegrants"} {
		result.Types[alias] = all.Types[alias]
		result.Kinds[alias] = all.Kinds[alias]
	}
	return result
}

func loadGatewayWeightedMapSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts", "gateway")
	wanted := map[string]bool{
		"util-backend-name-gateway": true, "util-reference-grant-permitted": true,
		"util-backend-ref-valid":                               true,
		"util-generate-httproute-weighted-backend-map-gateway": true,
		"util-generate-grpcroute-weighted-backend-map-gateway": true,
		"map-weighted-backend-500-gateway":                     true,
		"map-weighted-backend-515-gateway-separator":           true,
		gatewayHTTPWeightedMapComponent:                        true, gatewayGRPCWeightedMapComponent: true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, filename := range []string{"21-route-helpers.yaml", "42-maps-weighted.yaml"} {
		content, err := os.ReadFile(filepath.Join(chartRoot, filename))
		require.NoError(t, err)
		var library ingressBackendChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{
				Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires,
			}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source: chartSnippet.Incremental.Source, Group: chartSnippet.Incremental.Group,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func gatewayWeightedMapRoute(kind, name string, backends ...string) map[string]any {
	refs := make([]any, 0, len(backends))
	for _, backend := range backends {
		refs = append(refs, map[string]any{
			"group": "", "kind": "Service", "name": backend, "port": int64(80), "weight": int64(1),
		})
	}
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": kind,
		"metadata": map[string]any{"namespace": "default", "name": name},
		"spec":     map[string]any{"rules": []any{map[string]any{"backendRefs": refs}}},
	}
}

func gatewayWeightedMapService(name string, labels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"namespace": "default", "name": name, "labels": labels},
		"spec":     map[string]any{"ports": []any{map[string]any{"name": "http", "port": int64(80)}}},
	}
}

func (f *gatewayWeightedMapFixture) addService(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, f.services.Add(
		gatewayWeightedMapService(name, map[string]any{}), []string{"default", name},
	))
}

func (f *gatewayWeightedMapFixture) renderAndCommitCacheReady(t *testing.T) string {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result.HAProxyConfig
}

func (f *gatewayWeightedMapFixture) executions(
	componentName, source, name string,
) uint64 {
	component := f.service.incremental.components[componentName]
	query := componentQueryKey(&component, source, "default", name)
	return f.service.incremental.graph.Counters(query).Executions
}
