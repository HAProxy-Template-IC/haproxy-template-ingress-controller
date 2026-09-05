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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const gatewayTCPRouteClaimComponent = "gateway-tcproute-claims-100-route"

const gatewayTCPRouteClaimRoot = `{{ render "gateway-tcproute-claims-100-route" -}}
{%- for _, claim := range incremental_values("gateway-tcproute-claims", "ports") %}
{%- var value = claim.(map[string]any) %}
{{ tostring(value["port"]) }}={{ tostring(value["backendName"]) }}={{ toJSON(value["servers"]) }}
{%- end %}
{%- if extraContext | dig("fail") | fallback(false) %}{{ fail("forced TCPRoute claim failure") }}{%- end -%}`

type gatewayTCPRouteClaimFixture struct {
	config          *config.Config
	service         *RenderService
	engine          *dynamicBindingCountingEngine
	gateways        *k8sstore.MemoryStore
	tcpRoutes       *k8sstore.MemoryStore
	services        *k8sstore.MemoryStore
	endpoints       *k8sstore.MemoryStore
	namespaces      *k8sstore.MemoryStore
	referenceGrants *k8sstore.MemoryStore
	provider        stores.StoreProvider
}

func TestGatewayTCPRouteClaimsExecuteOnlyChangedRouteAcrossScale(t *testing.T) {
	for _, routeCount := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("routes=%d", routeCount), func(t *testing.T) {
			fixture := newGatewayTCPRouteClaimFixture(t)
			fixture.addGateway(t)
			fixture.addService(t)
			fixture.addEndpoint(t, "echo", "echo-slice", "10.0.0.1")
			for index := range routeCount {
				fixture.addRoute(t, gatewayTCPRouteClaim(index))
			}

			cold := fixture.renderAndCommitCacheReady(t)
			assert.Contains(t, cold, "9100=gtw_tcp_default_route-000000_0")
			coldCounts := fixture.engine.executionCounts()
			require.Len(t, coldCounts, routeCount)

			assert.Equal(t, cold, fixture.renderAndCommitCacheReady(t))
			assert.Equal(t, coldCounts, fixture.engine.executionCounts())

			changed := gatewayTCPRouteClaim(routeCount - 1)
			changed["metadata"].(map[string]any)["labels"] = map[string]any{"changed": "true"}
			fixture.updateRoute(t, changed)
			assert.Equal(t, cold, fixture.renderAndCommitCacheReady(t))
			counts := fixture.engine.executionCounts()
			assert.Equal(t, coldCounts[fmt.Sprintf("tcproutes/route-%06d", routeCount-1)]+1,
				counts[fmt.Sprintf("tcproutes/route-%06d", routeCount-1)])
			assert.Equal(t, coldCounts["tcproutes/route-000001"], counts["tcproutes/route-000001"])
		})
	}
}

func TestGatewayTCPRouteClaimPromotesCachedLoserAndSurvivesAbort(t *testing.T) {
	fixture := newGatewayTCPRouteClaimFixture(t)
	fixture.addGateway(t)
	fixture.addService(t)
	fixture.addEndpoint(t, "echo", "echo-slice", "10.0.0.1")
	fixture.addRoute(t, gatewayTCPRouteClaim(0))
	fixture.addRoute(t, gatewayTCPRouteClaim(1))

	baseline := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, baseline, "9100=gtw_tcp_default_route-000000_0")
	loserExecutions := fixture.engine.executionCounts()["tcproutes/route-000001"]

	require.NoError(t, fixture.tcpRoutes.Delete(
		"default", "route-000000", []string{"default", "route-000000"},
	))
	fixture.config.TemplatingSettings.ExtraContext["fail"] = true
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced TCPRoute claim failure")
	assert.Nil(t, result)
	assert.Equal(t, loserExecutions, fixture.engine.executionCounts()["tcproutes/route-000001"])

	fixture.config.TemplatingSettings.ExtraContext["fail"] = false
	promoted := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, promoted, "9100=gtw_tcp_default_route-000001_0")
	assert.NotContains(t, promoted, "route-000000")
	assert.Equal(t, loserExecutions, fixture.engine.executionCounts()["tcproutes/route-000001"])

	oracle := newGatewayTCPRouteClaimFixture(t)
	oracle.provider = fixture.provider
	assert.Equal(t, promoted, oracle.renderAndCommitCacheReady(t))
}

func TestGatewayTCPRouteClaimsTrackExactEndpointDependenciesAndAbort(t *testing.T) {
	fixture := newGatewayTCPRouteClaimFixture(t)
	fixture.addGateway(t)
	fixture.addService(t)
	fixture.addEndpoint(t, "echo", "echo-slice", "10.0.0.1")
	fixture.addRoute(t, gatewayTCPRouteClaim(0))
	fixture.addRoute(t, gatewayTCPRouteClaim(1))

	baseline := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, baseline, "10.0.0.1")
	baselineCounts := fixture.engine.executionCounts()

	fixture.addEndpoint(t, "other", "other-slice", "10.0.1.1")
	assert.Equal(t, baseline, fixture.renderAndCommitCacheReady(t))
	assert.Equal(t, baselineCounts, fixture.engine.executionCounts())

	fixture.updateEndpoint(t, "echo", "echo-slice", "10.0.0.2")
	changed := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, changed, "10.0.0.2")
	assert.NotContains(t, changed, "10.0.0.1")
	changedCounts := fixture.engine.executionCounts()
	assert.Equal(t, baselineCounts["tcproutes/route-000000"]+1, changedCounts["tcproutes/route-000000"])
	assert.Equal(t, baselineCounts["tcproutes/route-000001"]+1, changedCounts["tcproutes/route-000001"])

	committedSnapshot := fixture.service.incremental.snapshot
	fixture.updateEndpoint(t, "echo", "echo-slice", "10.0.0.3")
	fixture.config.TemplatingSettings.ExtraContext["fail"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced TCPRoute claim failure")
	assert.Nil(t, failed)
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["fail"] = false
	retried := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, retried, "10.0.0.3")

	require.NoError(t, fixture.endpoints.Delete("default", "echo-slice", []string{"default", "echo"}))
	deleted := fixture.renderAndCommitCacheReady(t)
	assert.NotContains(t, deleted, "10.0.0.3")
	fixture.addEndpoint(t, "echo", "echo-slice", "10.0.0.3")
	assert.Equal(t, retried, fixture.renderAndCommitCacheReady(t))
}

func newGatewayTCPRouteClaimFixture(t *testing.T) *gatewayTCPRouteClaimFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"gateways":        {APIVersion: "gateway.networking.k8s.io/v1", Resources: "gateways", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"tcproutes":       {APIVersion: "gateway.networking.k8s.io/v1", Resources: "tcproutes", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"services":        {APIVersion: "v1", Resources: "services", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"endpoints":       {APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices", IndexBy: []string{"metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"}},
			"namespaces":      {APIVersion: "v1", Resources: "namespaces", IndexBy: []string{"metadata.name"}},
			"referencegrants": {APIVersion: "gateway.networking.k8s.io/v1", Resources: "referencegrants", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
		TemplateSnippets: loadGatewayTCPRouteClaimSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: gatewayTCPRouteClaimRoot},
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]any{"fail": false},
		},
	}
	require.NoError(t, config.ValidateTemplateStructure(cfg))
	types := gatewayTCPRouteClaimSchemaTypes(t)
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	fixture := &gatewayTCPRouteClaimFixture{
		config: cfg, service: service, engine: engine,
		gateways: k8sstore.NewMemoryStore(2), tcpRoutes: k8sstore.NewMemoryStore(2),
		services: k8sstore.NewMemoryStore(2), endpoints: k8sstore.NewMemoryStore(2),
		namespaces:      k8sstore.NewMemoryStore(1),
		referenceGrants: k8sstore.NewMemoryStore(2),
	}
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"gateways": fixture.gateways, "tcproutes": fixture.tcpRoutes, "services": fixture.services,
		"endpoints":  fixture.endpoints,
		"namespaces": fixture.namespaces, "referencegrants": fixture.referenceGrants,
	})
	return fixture
}

func loadGatewayTCPRouteClaimSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	wanted := map[string]bool{
		"util-resource-helpers": true, "util-reference-grant-permitted": true,
		"util-backend-servers-helpers": true, "util-backend-servers-result": true,
		"util-publish-gateway-tcproute-claims": true, gatewayTCPRouteClaimComponent: true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, path := range []string{
		filepath.Join(chartRoot, "base", "library.yaml"),
		filepath.Join(chartRoot, "kubernetes-backends", "library.yaml"),
		filepath.Join(chartRoot, "gateway", "21-route-helpers.yaml"),
		filepath.Join(chartRoot, "gateway", "90-tcproute.yaml"),
	} {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		var library ingressBackendChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source: chartSnippet.Incremental.Source, Group: chartSnippet.Incremental.Group,
					Effects: chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func gatewayTCPRouteClaimSchemaTypes(t *testing.T) *typebootstrap.Result {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	schemaRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "tests", "schemas")
	fetcher, err := schemafetcher.NewDirFetcher(schemaRoot)
	require.NoError(t, err)
	result, err := typebootstrap.Bootstrap(t.Context(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{
			{Name: "gateways", GVK: schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}},
			{Name: "tcproutes", GVK: schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "TCPRoute"}},
			{Name: "services", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Service"}},
			{Name: "endpoints", GVK: schema.GroupVersionKind{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice"}},
			{Name: "namespaces", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}},
			{Name: "referencegrants", GVK: schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "ReferenceGrant"}},
		},
		Fetcher: fetcher,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Types, 6)
	return result
}

func gatewayTCPRouteClaim(index int) map[string]any {
	const serviceName = "echo"
	name := fmt.Sprintf("route-%06d", index)
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "TCPRoute",
		"metadata": map[string]any{
			"namespace": "default", "name": name,
			"creationTimestamp": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).
				Add(time.Duration(index) * time.Second).Format(time.RFC3339),
		},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "gateway"}},
			"rules": []any{map[string]any{"backendRefs": []any{map[string]any{
				"name": serviceName, "port": int64(8080), "weight": int64(1),
			}}}},
		},
	}
}

func (f *gatewayTCPRouteClaimFixture) addGateway(t *testing.T) {
	t.Helper()
	require.NoError(t, f.gateways.Add(map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway",
		"metadata": map[string]any{"namespace": "default", "name": "gateway"},
		"spec": map[string]any{
			"gatewayClassName": "haptic",
			"listeners": []any{map[string]any{
				"name": "tcp", "protocol": "TCP", "port": int64(9100),
				"allowedRoutes": map[string]any{"namespaces": map[string]any{"from": "Same"}},
			}},
		},
	}, []string{"default", "gateway"}))
}

func (f *gatewayTCPRouteClaimFixture) addService(t *testing.T) {
	t.Helper()
	require.NoError(t, f.services.Add(map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"namespace": "default", "name": "echo"},
		"spec":     map[string]any{"ports": []any{map[string]any{"name": "tcp", "port": int64(8080)}}},
	}, []string{"default", "echo"}))
}

func (f *gatewayTCPRouteClaimFixture) addRoute(t *testing.T, route map[string]any) {
	t.Helper()
	name := route["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.tcpRoutes.Add(route, []string{"default", name}))
}

func (f *gatewayTCPRouteClaimFixture) addEndpoint(
	t *testing.T,
	serviceName string,
	name string,
	address string,
) {
	t.Helper()
	require.NoError(t, f.endpoints.Add(gatewayTCPRouteEndpoint(serviceName, name, address),
		[]string{"default", serviceName}))
}

func (f *gatewayTCPRouteClaimFixture) updateEndpoint(
	t *testing.T,
	serviceName string,
	name string,
	address string,
) {
	t.Helper()
	require.NoError(t, f.endpoints.Update(gatewayTCPRouteEndpoint(serviceName, name, address),
		[]string{"default", serviceName}))
}

func gatewayTCPRouteEndpoint(serviceName, name, address string) map[string]any {
	return map[string]any{
		"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice",
		"metadata": map[string]any{
			"namespace": "default", "name": name,
			"labels": map[string]any{"kubernetes.io/service-name": serviceName},
		},
		"ports": []any{map[string]any{"name": "tcp", "port": int64(8080)}},
		"endpoints": []any{map[string]any{
			"addresses": []any{address}, "targetRef": map[string]any{"name": serviceName + "-pod"},
		}},
	}
}

func (f *gatewayTCPRouteClaimFixture) updateRoute(t *testing.T, route map[string]any) {
	t.Helper()
	name := route["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.tcpRoutes.Update(route, []string{"default", name}))
}

func (f *gatewayTCPRouteClaimFixture) renderAndCommitCacheReady(t *testing.T) string {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result.HAProxyConfig
}
