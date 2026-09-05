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
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	ingressBackendComponent       = "backenditems-500-ingress"
	nginxDefaultBackendComponent  = "backenditems-510-nginx-ingress-default-backend"
	hapticDefaultBackendComponent = "backenditems-850-haptic-default-backend"
)

const ingressBackendChartRoot = `# degraded-before={{ shared.Get("degradedBackendRef:default/missing/http") != nil }}
{{ planRegistry.ProfileGroup() }}
{{ render "backends-500-ingress" }}
{{ render "backends-510-nginx-ingress-default-backend" }}
{{ render "backends-850-haptic-default-backend" }}
# degraded-after={{ shared.Get("degradedBackendRef:default/missing/http") != nil }}
{%- if tostring(extraContext | dig("failAfterBackends") | fallback(false)) == "true" -%}
{{ fail("forced failure after ingress backends") }}
{%- end -%}`

const ingressBackendTestDirective = `{%- if ingress != nil -%}
  {%- var annotations = ingress.Metadata.Annotations -%}
  {%- var filename = annotations["test.haptic/file"] -%}
  {%- if filename != "" -%}
    {%- var content = annotations["test.haptic/content"] -%}
    {%- var secretName = annotations["test.haptic/secret"] -%}
    {%- if secretName != "" -%}
      {%- var secret = resources.secrets.GetSingle(ingress.Metadata.Namespace, secretName) -%}
      {%- if secret != nil -%}{%- content = secret.StringData["value"] -%}{%- end -%}
    {%- end -%}
    {%- var _, registerErr = RegisterBackendFile("map", filename, content) -%}
    {%- if registerErr != nil -%}{{ fail(tostring(registerErr)) }}{%- end -%}
  {%- end -%}
  {%- var eventMessage = annotations["test.haptic/event"] -%}
  {%- if eventMessage != "" -%}{{- recordEvent(ingress, "TestBackend", eventMessage) -}}{%- end -%}
{%- end -%}`

type ingressBackendChartLibrary struct {
	TemplateSnippets map[string]ingressBackendChartSnippet `yaml:"templateSnippets"`
}

type ingressBackendChartSnippet struct {
	Template    string                          `yaml:"template"`
	Requires    []string                        `yaml:"requires"`
	Incremental *ingressBackendChartIncremental `yaml:"incremental"`
}

type ingressBackendChartIncremental struct {
	Source            string                     `yaml:"source"`
	BindingsTemplate  string                     `yaml:"bindingsTemplate"`
	WhenAnyPathExists []string                   `yaml:"whenAnyPathExists"`
	Root              string                     `yaml:"root"`
	Group             string                     `yaml:"group"`
	Consumes          []string                   `yaml:"consumes"`
	OptionalConsumes  []string                   `yaml:"optionalConsumes"`
	Effects           []config.IncrementalEffect `yaml:"effects"`
}

type ingressBackendChartFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	services  *k8sstore.MemoryStore
	endpoints *k8sstore.MemoryStore
	secrets   *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestIngressBackendChartCachesExactDependenciesAndEffects(t *testing.T) {
	fixture := newIngressBackendChartFixture(t)
	fixture.addService(t, sslPassthroughService("echo", "http", 80))
	fixture.addService(t, sslPassthroughService("other", "http", 81))
	fixture.addEndpoint(t, sslPassthroughEndpoint("echo", "http", 8080, "10.0.0.1"))
	fixture.addEndpoint(t, sslPassthroughEndpoint("other", "http", 8181, "10.0.1.1"))
	fixture.addSecret(t, ingressBackendSecretResource("echo-secret", "alpha"))
	fixture.addIngress(t, ingressBackendIngressResource("a", "echo", map[string]string{
		"test.haptic/file": "a.map", "test.haptic/secret": "echo-secret", "test.haptic/event": "alpha event",
	}))
	fixture.addIngress(t, ingressBackendIngressResource("b", "other", nil))
	first := assertIngressBackendColdAndWarm(t, fixture)
	assertIngressBackendUnrelatedResourcesStayIdle(t, fixture, first)
	assertIngressBackendExactDependencyFanout(t, fixture)
	assertIngressBackendEffectRemovalAndDelete(t, fixture)
}

func assertIngressBackendColdAndWarm(t *testing.T, fixture *ingressBackendChartFixture) *RenderResult {
	t.Helper()
	first := fixture.renderAndCommit(t)
	assert.Contains(t, first.HAProxyConfig, "backend default_a_svc_echo_http")
	assert.Contains(t, first.HAProxyConfig, "10.0.0.1:8080")
	assert.Contains(t, first.HAProxyConfig, "backend default_b_svc_other_http")
	assert.Contains(t, first.HAProxyConfig, "10.0.1.1:8181")
	assert.Equal(t, "alpha", ingressBackendMapContent(t, first, "a.map"))
	firstEvents := requireRenderEvents(t, first)
	require.Len(t, firstEvents, 1)
	assert.Equal(t, "alpha event", firstEvents[0].Message)
	fixture.assertExecutionsForIngress(t, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)

	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, "alpha", ingressBackendMapContent(t, warm, "a.map"))
	assert.Equal(t, firstEvents, requireRenderEvents(t, warm))
	fixture.assertExecutionsForIngress(t, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)
	return first
}

func assertIngressBackendUnrelatedResourcesStayIdle(
	t *testing.T,
	fixture *ingressBackendChartFixture,
	first *RenderResult,
) {
	t.Helper()
	fixture.addService(t, sslPassthroughService("unrelated", "http", 82))
	fixture.addEndpoint(t, sslPassthroughEndpoint("unrelated", "http", 8282, "10.0.2.1"))
	fixture.addSecret(t, ingressBackendSecretResource("unrelated", "ignored"))
	unrelated := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, unrelated.HAProxyConfig)
	fixture.assertExecutionsForIngress(t, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)
}

func assertIngressBackendExactDependencyFanout(t *testing.T, fixture *ingressBackendChartFixture) {
	t.Helper()
	fixture.updateEndpoint(t, sslPassthroughEndpoint("echo", "http", 8080, "10.0.0.9"))
	endpointChanged := fixture.renderAndCommit(t)
	assert.Contains(t, endpointChanged.HAProxyConfig, "10.0.0.9:8080")
	assert.NotContains(t, endpointChanged.HAProxyConfig, "10.0.0.1:8080")
	fixture.assertExecutions(t, ingressBackendComponent, "a", 2)
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "a", 1)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)

	fixture.updateService(t, sslPassthroughService("echo", "http", 90))
	serviceChanged := fixture.renderAndCommit(t)
	assert.Contains(t, serviceChanged.HAProxyConfig, "Service echo:90")
	fixture.assertExecutions(t, ingressBackendComponent, "a", 3)
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "a", 1)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)

	fixture.updateSecret(t, ingressBackendSecretResource("echo-secret", "beta"))
	secretChanged := fixture.renderAndCommit(t)
	assert.Equal(t, "beta", ingressBackendMapContent(t, secretChanged, "a.map"))
	fixture.assertExecutions(t, ingressBackendComponent, "a", 4)
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "a", 1)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)
}

func assertIngressBackendEffectRemovalAndDelete(t *testing.T, fixture *ingressBackendChartFixture) {
	t.Helper()
	fixture.updateIngress(t, ingressBackendIngressResource("a", "echo", map[string]string{
		"test.haptic/event": "updated event",
	}))
	removedFile := fixture.renderAndCommit(t)
	assert.Empty(t, ingressBackendMapContentIfPresent(t, removedFile, "a.map"))
	removedEvents := requireRenderEvents(t, removedFile)
	require.Len(t, removedEvents, 1)
	assert.Equal(t, "updated event", removedEvents[0].Message)
	fixture.assertExecutions(t, ingressBackendComponent, "a", 5)
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "a", 2)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "a", 0)

	fixture.deleteIngress(t, "a")
	deleted := fixture.renderAndCommit(t)
	assert.NotContains(t, deleted.HAProxyConfig, "backend default_a_svc_echo_http")
	assert.Empty(t, requireRenderEvents(t, deleted))
	fixture.assertExecutionsForIngress(t, "b", 0)
}

func TestIngressAnnotationDefaultBackendsInvalidateOnlyExactReferences(t *testing.T) {
	fixture := newIngressBackendChartFixture(t)
	for _, service := range []string{"route-a", "route-b", "nginx-error", "haptic-error"} {
		fixture.addService(t, sslPassthroughService(service, "http", 80))
		fixture.addEndpoint(t, sslPassthroughEndpoint(service, "http", 8080, "10.0.0.1"))
	}
	fixture.addIngress(t, ingressBackendIngressResource("nginx", "route-a", map[string]string{
		"nginx.ingress.kubernetes.io/default-backend": "nginx-error",
	}))
	fixture.addIngress(t, ingressBackendIngressResource("haptic", "route-b", map[string]string{
		"haproxy-haptic.org/default-backend": "haptic-error",
	}))

	first := fixture.renderAndCommit(t)
	assert.Contains(t, first.HAProxyConfig, "backend default_nginx_default-backend_nginx-error")
	assert.Contains(t, first.HAProxyConfig, "backend default_haptic_default-backend_haptic-error")
	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	fixture.assertExecutionsForIngress(t, "nginx", 0)
	fixture.assertExecutionsForIngress(t, "haptic", 1)

	fixture.updateEndpoint(t, sslPassthroughEndpoint("nginx-error", "http", 8080, "10.0.0.8"))
	nginxChanged := fixture.renderAndCommit(t)
	assert.Contains(t, nginxChanged.HAProxyConfig, "10.0.0.8:8080")
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "nginx", 2)
	fixture.assertExecutions(t, ingressBackendComponent, "nginx", 1)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "nginx", 0)
	fixture.assertExecutionsForIngress(t, "haptic", 1)

	fixture.updateService(t, sslPassthroughService("haptic-error", "http", 81))
	fixture.renderAndCommit(t)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "haptic", 2)
	fixture.assertExecutions(t, ingressBackendComponent, "haptic", 1)
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "haptic", 1)
}

func TestIngressBackendChartFileConflictsAndAbortsDoNotPoisonCache(t *testing.T) {
	fixture := newIngressBackendChartFixture(t)
	fixture.addService(t, sslPassthroughService("echo", "http", 80))
	fixture.addEndpoint(t, sslPassthroughEndpoint("echo", "http", 8080, "10.0.0.1"))
	fixture.addSecret(t, ingressBackendSecretResource("a-secret", "same"))
	fixture.addSecret(t, ingressBackendSecretResource("b-secret", "same"))
	fixture.addIngress(t, ingressBackendIngressResource("a", "echo", map[string]string{
		"test.haptic/file": "shared.map", "test.haptic/secret": "a-secret",
	}))
	fixture.addIngress(t, ingressBackendIngressResource("b", "echo", map[string]string{
		"test.haptic/file": "shared.map", "test.haptic/secret": "b-secret",
	}))

	baseline := fixture.renderAndCommit(t)
	assert.Equal(t, "same", ingressBackendMapContent(t, baseline, "shared.map"))
	fixture.updateSecret(t, ingressBackendSecretResource("b-secret", "conflict"))
	failed, err := fixture.render(t)
	require.ErrorContains(t, err, "shared.map")
	assert.Nil(t, failed)
	assert.Equal(t, uint64(1), fixture.executions(ingressBackendComponent, "b"))

	fixture.updateSecret(t, ingressBackendSecretResource("b-secret", "same"))
	recovered := fixture.renderAndCommit(t)
	assert.Equal(t, "same", ingressBackendMapContent(t, recovered, "shared.map"))
	assert.Equal(t, uint64(1), fixture.executions(ingressBackendComponent, "b"))

	fixture.updateIngress(t, ingressBackendIngressResource("b", "echo", map[string]string{
		"test.haptic/file": "b.map", "test.haptic/secret": "b-secret",
	}))
	fixture.renderAndCommit(t)

	fixture.updateSecret(t, ingressBackendSecretResource("a-secret", "after-abort"))
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = true
	failed, err = fixture.render(t)
	require.ErrorContains(t, err, "forced failure after ingress backends")
	assert.Nil(t, failed)
	beforeRetry := fixture.executions(ingressBackendComponent, "a")
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = false
	retried := fixture.renderAndCommit(t)
	assert.Equal(t, "after-abort", ingressBackendMapContent(t, retried, "shared.map"))
	assert.Equal(t, beforeRetry+1, fixture.executions(ingressBackendComponent, "a"))
}

func TestIngressBackendChartAdmissionAndConcurrentRendersStayIsolated(t *testing.T) {
	fixture := newIngressBackendChartFixture(t)
	fixture.addService(t, sslPassthroughService("echo", "http", 80))
	fixture.addService(t, sslPassthroughService("other", "http", 80))
	fixture.addEndpoint(t, sslPassthroughEndpoint("echo", "http", 8080, "10.0.0.1"))
	fixture.addEndpoint(t, sslPassthroughEndpoint("other", "http", 8080, "10.0.0.2"))
	fixture.addIngress(t, ingressBackendIngressResource("subject", "echo", nil))
	fixture.addIngress(t, ingressBackendIngressResource("stable", "echo", nil))
	baseline := fixture.renderAndCommit(t)
	baselineCounts := fixture.engine.executionCounts()

	results := make([]*RenderResult, 2)
	errors := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errors[index] = fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
		}()
	}
	wait.Wait()
	for index := range results {
		require.NoError(t, errors[index])
		assert.Equal(t, baseline.HAProxyConfig, results[index].HAProxyConfig)
		require.NoError(t, results[index].InputTransaction.Commit(t.Context()))
	}
	assert.Equal(t, baselineCounts, fixture.engine.executionCounts())

	proposed := ingressBackendIngressResource("subject", "other", nil)
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = true
	failed, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("ingresses", "default", "subject"),
	)
	require.ErrorContains(t, err, "forced failure after ingress backends")
	assert.Nil(t, failed)
	afterAdmissionCounts := fixture.engine.executionCounts()
	assert.Equal(t, baselineCounts["ingresses/stable"], afterAdmissionCounts["ingresses/stable"])
	assert.Equal(t, baselineCounts["ingresses/subject"]+2, afterAdmissionCounts["ingresses/subject"])

	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = false
	afterAdmission := fixture.renderAndCommit(t)
	assert.Equal(t, baseline.HAProxyConfig, afterAdmission.HAProxyConfig)
	assert.NotContains(t, afterAdmission.HAProxyConfig, "default_subject_svc_other_http")
	assert.Equal(t, afterAdmissionCounts, fixture.engine.executionCounts())

	fixture.updateIngress(t, proposed)
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = true
	failed, err = fixture.render(t)
	require.ErrorContains(t, err, "forced failure after ingress backends")
	assert.Nil(t, failed)
	beforeRetry := fixture.engine.executionCounts()
	fixture.config.TemplatingSettings.ExtraContext["failAfterBackends"] = false
	retried := fixture.renderAndCommit(t)
	assert.Contains(t, retried.HAProxyConfig, "default_subject_svc_other_http")
	assert.NotContains(t, retried.HAProxyConfig, "default_subject_svc_echo_http")
	assert.Equal(t, beforeRetry["ingresses/subject"]+2, fixture.engine.executionCounts()["ingresses/subject"])
	assert.Equal(t, beforeRetry["ingresses/stable"], fixture.engine.executionCounts()["ingresses/stable"])
}

func TestIngressBackendChartReplaysAndClearsDegradedMarker(t *testing.T) {
	fixture := newIngressBackendChartFixture(t)
	fixture.addIngress(t, ingressBackendIngressResource("subject", "missing", nil))

	first := fixture.renderAndCommit(t)
	assert.Contains(t, first.HAProxyConfig, "# degraded-after=true")
	assert.Contains(t, first.HAProxyConfig, "backend default_subject_svc_missing_http")
	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	fixture.assertExecutions(t, ingressBackendComponent, "subject", 1)

	fixture.addService(t, sslPassthroughService("missing", "http", 80))
	resolved := fixture.renderAndCommit(t)
	assert.Contains(t, resolved.HAProxyConfig, "# degraded-after=false")
	fixture.assertExecutions(t, ingressBackendComponent, "subject", 2)
	fixture.assertExecutions(t, nginxDefaultBackendComponent, "subject", 1)
	fixture.assertExecutions(t, hapticDefaultBackendComponent, "subject", 0)
}

func newIngressBackendChartFixture(t *testing.T) *ingressBackendChartFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"failAfterBackends": false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"services":  {APIVersion: "v1", Resources: "services", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"endpoints": {APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices", IndexBy: []string{"metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"}},
			"secrets":   {APIVersion: "v1", Resources: "secrets", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
		TemplateSnippets: loadIngressBackendChartSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: ingressBackendChartRoot},
	}
	types := ingressBackendSchemaTypes(t)
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	ingresses := k8sstore.NewMemoryStore(2)
	services := k8sstore.NewMemoryStore(2)
	endpoints := k8sstore.NewMemoryStore(2)
	secrets := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": ingresses, "services": services, "endpoints": endpoints, "secrets": secrets,
	})
	return &ingressBackendChartFixture{
		config: cfg, service: service, engine: engine, ingresses: ingresses, services: services,
		endpoints: endpoints, secrets: secrets, provider: provider,
	}
}

func ingressBackendSchemaTypes(t *testing.T) *typebootstrap.Result {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	schemaRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "tests", "schemas")
	fetcher, err := schemafetcher.NewDirFetcher(schemaRoot)
	require.NoError(t, err)
	result, err := typebootstrap.Bootstrap(t.Context(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{
			{Name: "ingresses", GVK: schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}},
			{Name: "services", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Service"}},
			{Name: "endpoints", GVK: schema.GroupVersionKind{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice"}},
			{Name: "secrets", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Secret"}},
		},
		Fetcher: fetcher,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Types, 4)
	return result
}

func loadIngressBackendChartSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	files := []string{
		"base/library.yaml", "kubernetes-backends/library.yaml", "ingress/library.yaml",
		"nginx-ingress/17-default-backend.yaml",
		"haptic-annotations/40-features.yaml",
	}
	wanted := map[string]bool{
		"util-service-port-resolution": true, "util-backend": true,
		"util-backend-servers-helpers": true, "util-backend-servers-result": true,
		"util-backend-name-ingress": true, "util-generate-backends-ingress": true,
		"util-generate-annotation-default-backend": true, "util-ingress-backend-bindings": true,
		"util-replay-ingress-backend-effects": true, "backends-500-ingress": true, ingressBackendComponent: true,
		"util-nginx-ingress-default-backend": true, "backends-510-nginx-ingress-default-backend": true,
		nginxDefaultBackendComponent: true, "util-haptic-default-backend": true,
		"backends-850-haptic-default-backend": true, hapticDefaultBackendComponent: true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted)+1)
	for _, relativePath := range files {
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
					Root:              chartSnippet.Incremental.Root,
					Group:             chartSnippet.Incremental.Group, Consumes: chartSnippet.Incremental.Consumes,
					OptionalConsumes: chartSnippet.Incremental.OptionalConsumes,
					Effects:          chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	result["backend-directives-900-test-effects"] = config.TemplateSnippet{
		Name: "backend-directives-900-test-effects", Template: ingressBackendTestDirective,
	}
	return result
}

func ingressBackendIngressResource(name, service string, annotations map[string]string) map[string]any {
	annotationValues := map[string]any{}
	for key, value := range annotations {
		annotationValues[key] = value
	}
	backend := map[string]any{"service": map[string]any{
		"name": service, "port": map[string]any{"name": "http"},
	}}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
		"metadata": map[string]any{"namespace": "default", "name": name, "annotations": annotationValues},
		"spec": map[string]any{"rules": []any{
			map[string]any{"http": map[string]any{"paths": []any{map[string]any{"backend": backend}}}},
		}},
	}
}

func ingressBackendSecretResource(name, value string) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]any{"namespace": "default", "name": name},
		"stringData": map[string]any{"value": value},
	}
}

func (f *ingressBackendChartFixture) addIngress(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(resource, []string{"default", name}))
}

func (f *ingressBackendChartFixture) updateIngress(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Update(resource, []string{"default", name}))
}

func (f *ingressBackendChartFixture) deleteIngress(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, f.ingresses.Delete("default", name, []string{"default", name}))
}

func (f *ingressBackendChartFixture) addService(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.services.Add(resource, []string{"default", name}))
}

func (f *ingressBackendChartFixture) updateService(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.services.Update(resource, []string{"default", name}))
}

func (f *ingressBackendChartFixture) addEndpoint(t *testing.T, resource map[string]any) {
	t.Helper()
	metadata := resource["metadata"].(map[string]any)
	service := metadata["labels"].(map[string]any)["kubernetes.io/service-name"].(string)
	require.NoError(t, f.endpoints.Add(resource, []string{"default", service}))
}

func (f *ingressBackendChartFixture) updateEndpoint(t *testing.T, resource map[string]any) {
	t.Helper()
	metadata := resource["metadata"].(map[string]any)
	service := metadata["labels"].(map[string]any)["kubernetes.io/service-name"].(string)
	require.NoError(t, f.endpoints.Update(resource, []string{"default", service}))
}

func (f *ingressBackendChartFixture) addSecret(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.secrets.Add(resource, []string{"default", name}))
}

func (f *ingressBackendChartFixture) updateSecret(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.secrets.Update(resource, []string{"default", name}))
}

func (f *ingressBackendChartFixture) render(t *testing.T) (*RenderResult, error) {
	t.Helper()
	return f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
}

func (f *ingressBackendChartFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.render(t)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *ingressBackendChartFixture) executions(componentName, ingress string) uint64 {
	component := f.service.incremental.components[componentName]
	query := componentQueryKey(&component, "ingresses", "default", ingress)
	return f.service.incremental.graph.Counters(query).Executions
}

func (f *ingressBackendChartFixture) assertExecutions(t *testing.T, componentName, ingress string, want uint64) {
	t.Helper()
	assert.Equal(t, want, f.executions(componentName, ingress), componentName+"/"+ingress)
}

func (f *ingressBackendChartFixture) assertExecutionsForIngress(
	t *testing.T,
	ingress string,
	hapticDefaultBackendWant uint64,
) {
	t.Helper()
	f.assertExecutions(t, ingressBackendComponent, ingress, 1)
	f.assertExecutions(t, nginxDefaultBackendComponent, ingress, 1)
	f.assertExecutions(t, hapticDefaultBackendComponent, ingress, hapticDefaultBackendWant)
}

func ingressBackendMapContent(t *testing.T, result *RenderResult, name string) string {
	t.Helper()
	content := ingressBackendMapContentIfPresent(t, result, name)
	require.NotEmpty(t, content)
	return content
}

func ingressBackendMapContentIfPresent(t *testing.T, result *RenderResult, name string) string {
	t.Helper()
	for _, file := range requireAuxiliaryFiles(t, result).MapFiles {
		if strings.HasSuffix(file.Path, "/"+name) || file.Path == name {
			return file.Content
		}
	}
	return ""
}
