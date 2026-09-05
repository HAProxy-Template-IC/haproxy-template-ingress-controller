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
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

const defaultBackendPublicationComponent = "kubernetes-default-backend-publication"
const varnishBackendPublicationComponent = "haptic-varnish-backend-publication"

const defaultBackendPublicationRoot = `{%- import "util-service-port-resolution" for ResolveServicePort -%}
{%- import "util-backend-servers-result" for BackendServersResult -%}
{{- render "kubernetes-default-backend-publication" -}}
{%%
var backend = extraContext["defaultBackendService"].(map[string]any)
var namespace = tostring(backend["namespace"])
var name = tostring(backend["name"])
var resolved = split(ResolveServicePort(namespace, name, backend["port"]), " ")
var port = toint(resolved[0])
var portName = resolved[1]
var legacyResult = BackendServersResult(name, 0, port, nil, portName, "default_backend", namespace)
var legacy = map[string]any{"port": port, "portName": portName, "servers": legacyResult["servers"]}
var incremental = legacy
var publications = incremental_values("kubernetes-default-backend", "backends")
if len(publications) > 0 { incremental = publications[0].(map[string]any) }
%%}
{{ "I\n" }}{{ toJSON(incremental) }}{{ "\nL\n" }}{{ toJSON(legacy) }}
{%- if tostring(extraContext | dig("failAfterBackends") | fallback(false)) == "true" -%}
{{- fail("forced failure after Kubernetes backend publications") -}}
{%- end -%}`

const varnishBackendPublicationRoot = `{%- import "util-backend-servers-result" for BackendServersResult -%}
{{- render "haptic-varnish-backend-publication" -}}
{%%
var namespace = extraContext | dig("cache", "varnish", "namespace") | fallback("default") | tostring()
var name = extraContext | dig("cache", "varnish", "serviceName") | fallback("haptic-varnish-cache") | tostring()
var port = toint(extraContext | dig("cache", "varnish", "servicePort") | fallback("6081") | tostring())
var legacyResult = BackendServersResult(name, 0, port, map[string]any{"flags": []any{}}, "", "varnish_cache_shards", namespace)
var legacy = legacyResult["servers"].([]any)
var incremental = []any{}
var publications = incremental_values("haptic-varnish-backend", "backends")
if len(publications) > 0 { incremental = publications[0].([]any) }
%%}
{{ "I\n" }}{{ toJSON(incremental) }}{{ "\nL\n" }}{{ toJSON(legacy) }}
{%- if tostring(extraContext | dig("failAfterBackends") | fallback(false)) == "true" -%}
{{- fail("forced failure after Kubernetes backend publications") -}}
{%- end -%}`

type kubernetesBackendPublicationFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	services  *k8sstore.MemoryStore
	endpoints *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

type kubernetesBackendUnavailableVersions struct{}

func (kubernetesBackendUnavailableVersions) IsServed(_, _ string) bool {
	return false
}

func TestDefaultBackendPublicationTracksExactServiceAndEndpointDependencies(t *testing.T) {
	fixture := newKubernetesBackendPublicationFixture(t, defaultBackendPublicationComponent,
		defaultBackendPublicationRoot, map[string]any{
			"defaultBackendService": map[string]any{
				"namespace": "default", "name": "echo", "port": map[string]any{"name": "http"},
			},
		})
	assertKubernetesBackendPublicationLifecycle(t, fixture, "default_backend")
}

func TestVarnishBackendPublicationTracksExactServiceAndEndpointDependencies(t *testing.T) {
	fixture := newKubernetesBackendPublicationFixture(t, varnishBackendPublicationComponent,
		varnishBackendPublicationRoot, map[string]any{
			"cache": map[string]any{"varnish": map[string]any{
				"enabled": "true", "namespace": "default", "serviceName": "echo", "servicePort": "80",
			}},
		})
	assertKubernetesBackendPublicationLifecycle(t, fixture, "varnish_cache_shards")
}

func TestKubernetesBackendPublicationConfigPropsSelectExactTarget(t *testing.T) {
	tests := map[string]struct {
		component    string
		root         string
		extraContext map[string]any
		selectOther  func(map[string]any)
	}{
		"default backend": {
			component: defaultBackendPublicationComponent,
			root:      defaultBackendPublicationRoot,
			extraContext: map[string]any{"defaultBackendService": map[string]any{
				"namespace": "default", "name": "echo", "port": map[string]any{"name": "http"},
			}},
			selectOther: func(extraContext map[string]any) {
				extraContext["defaultBackendService"].(map[string]any)["name"] = "other"
			},
		},
		"varnish backend": {
			component: varnishBackendPublicationComponent,
			root:      varnishBackendPublicationRoot,
			extraContext: map[string]any{"cache": map[string]any{"varnish": map[string]any{
				"enabled": "true", "namespace": "default", "serviceName": "echo", "servicePort": "80",
			}}},
			selectOther: func(extraContext map[string]any) {
				extraContext["cache"].(map[string]any)["varnish"].(map[string]any)["serviceName"] = "other"
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newKubernetesBackendPublicationFixture(t, test.component, test.root, test.extraContext)
			fixture.addService(t, backendPublicationService("echo", 80))
			fixture.addEndpoint(t, backendPublicationEndpoint("echo", "echo-slice", "10.0.0.1"))
			fixture.addService(t, backendPublicationService("other", 80))
			fixture.addEndpoint(t, backendPublicationEndpoint("other", "other-slice", "10.0.1.1"))

			baseline := fixture.renderAndCommit(t)
			fixture.requireDifferential(t, baseline)
			assert.Contains(t, baseline.HAProxyConfig, "10.0.0.1")
			baselineCounts := fixture.engine.executionCounts()

			fixture.config.TemplatingSettings.ExtraContext["unused"] = "changed"
			assert.Equal(t, baseline.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
			assert.Equal(t, baselineCounts, fixture.engine.executionCounts())

			test.selectOther(fixture.config.TemplatingSettings.ExtraContext)
			selected := fixture.renderAndCommit(t)
			fixture.requireDifferential(t, selected)
			assert.Contains(t, selected.HAProxyConfig, "10.0.1.1")
			assert.NotContains(t, selected.HAProxyConfig, "10.0.0.1")
			selectedCounts := fixture.engine.executionCounts()
			assert.NotEqual(t, baselineCounts, selectedCounts)
			assert.Equal(t, selected.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
			assert.Equal(t, selectedCounts, fixture.engine.executionCounts())
		})
	}
}

func TestMigratedChartRootsHaveNoAmbientBackendOrTrustedCatalogReads(t *testing.T) {
	tests := map[string]struct {
		path      string
		snippet   string
		forbidden []string
		required  []string
	}{
		"default backend": {
			path: "kubernetes-backends/library.yaml", snippet: "default-backend-100-kubernetes",
			forbidden: []string{"ResolveServicePort(", "BackendServers("},
			required:  []string{"endpoints"},
		},
		"varnish backend": {
			path: "haptic-annotations/70-caching.yaml", snippet: "backends-870-haptic-varnish-cache",
			forbidden: []string{"BackendServers("},
			required:  []string{"endpoints"},
		},
		"TCPRoute backend": {
			path: "gateway/90-tcproute.yaml", snippet: "backends-502-gateway-tcproute",
			forbidden: []string{"BackendServers(", "BackendServersResult("},
		},
		"TCPRoute frontend": {
			path: "gateway/90-tcproute.yaml", snippet: "frontends-700-gateway-tcp-listener",
			required: []string{"endpoints"},
		},
		"trusted WAF catalogs": {
			path: "haptic-annotations/83-waf-policies.yaml", snippet: "util-waf-haptic-coraza-scan",
			forbidden: []string{"resources.configmaps.GetSingle("},
		},
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(chartRoot, test.path))
			require.NoError(t, err)
			var library ingressBackendChartLibrary
			require.NoError(t, yaml.Unmarshal(content, &library))
			snippet, found := library.TemplateSnippets[test.snippet]
			require.True(t, found)
			for _, forbidden := range test.forbidden {
				assert.NotContains(t, snippet.Template, forbidden)
			}
			for _, required := range test.required {
				assert.Contains(t, snippet.Requires, required)
			}
		})
	}
}

func TestTCPRouteBackendAndCallersStripTogetherWithoutEndpoints(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts",
		"gateway", "90-tcproute.yaml")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var library ingressBackendChartLibrary
	require.NoError(t, yaml.Unmarshal(content, &library))

	names := []string{
		"gateway-tcproute-claims-100-route",
		"backends-502-gateway-tcproute",
		"frontends-700-gateway-tcp-listener",
	}
	snippets := make(map[string]config.TemplateSnippet, len(names))
	for _, name := range names {
		chartSnippet, found := library.TemplateSnippets[name]
		require.True(t, found)
		snippets[name] = config.TemplateSnippet{
			Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires,
		}
	}
	cfg := &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"endpoints": {
				APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices", Optional: true,
			},
		},
		TemplateSnippets: snippets,
	}
	effective, resolution, err := config.ResolveEffective(cfg, kubernetesBackendUnavailableVersions{}, nil)
	require.NoError(t, err)
	assert.Empty(t, effective.TemplateSnippets)
	assert.ElementsMatch(t, names, resolution.StrippedSnippets)
}

func assertKubernetesBackendPublicationLifecycle(
	t *testing.T,
	fixture *kubernetesBackendPublicationFixture,
	backendName string,
) {
	t.Helper()
	fixture.addService(t, backendPublicationService("echo", 80))
	fixture.addEndpoint(t, backendPublicationEndpoint("echo", "echo-slice", "10.0.0.1"))
	fixture.addEndpoint(t, backendPublicationEndpoint("echo", "echo-slice-b", "10.0.0.4"))
	fixture.addService(t, backendPublicationService("unrelated", 81))
	fixture.addEndpoint(t, backendPublicationEndpoint("unrelated", "unrelated-slice", "10.0.1.1"))

	cold := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, cold)
	assert.Contains(t, cold.HAProxyConfig, "10.0.0.1")
	assert.Contains(t, cold.HAProxyConfig, "10.0.0.4")
	assert.Contains(t, cold.HAProxyConfig, backendName)
	coldCounts := fixture.engine.executionCounts()
	assert.Equal(t, 1, coldCounts["services/echo"])
	assert.Equal(t, 1, coldCounts["endpoints/echo-slice"])
	assert.Equal(t, 1, coldCounts["endpoints/echo-slice-b"])

	warm := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, warm)
	assert.Equal(t, cold.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, coldCounts, fixture.engine.executionCounts())

	fixture.updateEndpoint(t, backendPublicationEndpoint("unrelated", "unrelated-slice", "10.0.1.2"))
	unrelated := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, unrelated)
	assert.Equal(t, cold.HAProxyConfig, unrelated.HAProxyConfig)
	assert.Equal(t, 1, fixture.engine.executionCounts()["services/echo"])
	assert.Equal(t, 1, fixture.engine.executionCounts()["endpoints/echo-slice"])
	assert.Equal(t, 1, fixture.engine.executionCounts()["endpoints/echo-slice-b"])
	assert.Equal(t, 2, fixture.engine.executionCounts()["endpoints/unrelated-slice"])

	fixture.updateEndpoint(t, backendPublicationEndpoint("echo", "echo-slice", "10.0.0.2"))
	changed := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, changed)
	assert.Contains(t, changed.HAProxyConfig, "10.0.0.2")
	assert.NotContains(t, changed.HAProxyConfig, "10.0.0.1")
	assert.Equal(t, 2, fixture.engine.executionCounts()["services/echo"])
	assert.Equal(t, 2, fixture.engine.executionCounts()["endpoints/echo-slice"])
	assert.Equal(t, 2, fixture.engine.executionCounts()["endpoints/echo-slice-b"])

	assertKubernetesBackendPublicationRecovery(t, fixture)
}

func assertKubernetesBackendPublicationRecovery(
	t *testing.T,
	fixture *kubernetesBackendPublicationFixture,
) {
	t.Helper()
	committedSnapshot := fixture.service.incremental.snapshot
	fixture.updateEndpoint(t, backendPublicationEndpoint("echo", "echo-slice", "10.0.0.3"))
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after Kubernetes backend publications")
	assert.Nil(t, failed)
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = false
	retried := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, retried)
	assert.Contains(t, retried.HAProxyConfig, "10.0.0.3")

	fixture.deleteEndpoint(t, "echo-slice", "echo")
	deleted := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, deleted)
	assert.NotContains(t, deleted.HAProxyConfig, "10.0.0.3")

	fixture.addEndpoint(t, backendPublicationEndpoint("echo", "echo-slice", "10.0.0.3"))
	recreated := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, recreated)
	assert.Equal(t, retried.HAProxyConfig, recreated.HAProxyConfig)
}

func newKubernetesBackendPublicationFixture(
	t *testing.T,
	component string,
	root string,
	extraContext map[string]any,
) *kubernetesBackendPublicationFixture {
	t.Helper()
	extraContext["failAfterBackends"] = false
	cfg := &config.Config{
		Dataplane:          testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: extraContext},
		WatchedResources: map[string]config.WatchedResource{
			"services": {
				APIVersion: "v1", Resources: "services",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"endpoints": {
				APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices",
				IndexBy: []string{"metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"},
			},
		},
		TemplateSnippets: loadKubernetesBackendPublicationSnippets(t, component),
		HAProxyConfig:    config.HAProxyConfig{Template: root},
	}
	require.NoError(t, config.ValidateTemplateStructure(cfg))
	types := &typebootstrap.Result{
		Types: map[string]reflect.Type{
			"services": reflect.TypeOf(backendServersService{}), "endpoints": reflect.TypeOf(backendServersEndpointSlice{}),
		},
		Kinds:  map[string]string{"services": "Service", "endpoints": "EndpointSlice"},
		Errors: map[string]error{},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	servicesStore := k8sstore.NewMemoryStore(2)
	endpointsStore := k8sstore.NewMemoryStore(2)
	return &kubernetesBackendPublicationFixture{
		config: cfg, service: service, engine: engine, services: servicesStore, endpoints: endpointsStore,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{
			"services": servicesStore, "endpoints": endpointsStore,
		}),
	}
}

func loadKubernetesBackendPublicationSnippets(
	t *testing.T,
	component string,
) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	wanted := map[string]bool{
		"util-backend-servers-helpers": true,
		"util-service-port-resolution": true,
		"util-backend-servers-result":  true,
		component:                      true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, relativePath := range []string{
		"base/library.yaml", "kubernetes-backends/library.yaml", "haptic-annotations/70-caching.yaml",
	} {
		content, err := os.ReadFile(filepath.Join(chartRoot, relativePath))
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
					Source: chartSnippet.Incremental.Source, BindingsTemplate: chartSnippet.Incremental.BindingsTemplate,
					WhenAnyPathExists: chartSnippet.Incremental.WhenAnyPathExists,
					Root:              chartSnippet.Incremental.Root, Group: chartSnippet.Incremental.Group,
					Consumes: chartSnippet.Incremental.Consumes, OptionalConsumes: chartSnippet.Incremental.OptionalConsumes,
					Effects: chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func backendPublicationService(name string, port int64) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"namespace": "default", "name": name},
		"spec":     map[string]any{"ports": []any{map[string]any{"name": "http", "port": port}}},
	}
}

func backendPublicationEndpoint(serviceName, name, address string) map[string]any {
	return map[string]any{
		"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice",
		"metadata": map[string]any{
			"namespace": "default", "name": name,
			"labels": map[string]any{"kubernetes.io/service-name": serviceName},
		},
		"ports": []any{map[string]any{"name": "http", "port": int64(8080)}},
		"endpoints": []any{map[string]any{
			"addresses": []any{address}, "targetRef": map[string]any{"name": serviceName + "-pod"},
		}},
	}
}

func (f *kubernetesBackendPublicationFixture) addService(t *testing.T, resource map[string]any) {
	t.Helper()
	metadata := resource["metadata"].(map[string]any)
	require.NoError(t, f.services.Add(resource, []string{"default", metadata["name"].(string)}))
}

func (f *kubernetesBackendPublicationFixture) addEndpoint(t *testing.T, resource map[string]any) {
	t.Helper()
	metadata := resource["metadata"].(map[string]any)
	serviceName := metadata["labels"].(map[string]any)["kubernetes.io/service-name"].(string)
	require.NoError(t, f.endpoints.Add(resource, []string{"default", serviceName}))
}

func (f *kubernetesBackendPublicationFixture) updateEndpoint(t *testing.T, resource map[string]any) {
	t.Helper()
	metadata := resource["metadata"].(map[string]any)
	serviceName := metadata["labels"].(map[string]any)["kubernetes.io/service-name"].(string)
	require.NoError(t, f.endpoints.Update(resource, []string{"default", serviceName}))
}

func (f *kubernetesBackendPublicationFixture) deleteEndpoint(t *testing.T, name, serviceName string) {
	t.Helper()
	require.NoError(t, f.endpoints.Delete("default", name, []string{"default", serviceName}))
}

func (f *kubernetesBackendPublicationFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *kubernetesBackendPublicationFixture) requireDifferential(t *testing.T, result *RenderResult) {
	t.Helper()
	trimmed := strings.TrimSpace(result.HAProxyConfig)
	require.True(t, strings.HasPrefix(trimmed, "I\n"), trimmed)
	parts := strings.Split(strings.TrimPrefix(trimmed, "I\n"), "\nL\n")
	require.Len(t, parts, 2, trimmed)
	assert.JSONEq(t, strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0]))
}
