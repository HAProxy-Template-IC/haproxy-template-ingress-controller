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
	"strings"
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
	ingressPathCandidateComponent    = "features-900-ingress-path-candidates"
	ingressDefaultCandidateComponent = "features-905-ingress-default-backend-candidates"
	ingressExactPathComponent        = "pathitems-exact-500-ingress"
	ingressPrefixExactPathComponent  = "pathitems-pfxexact-500-ingress"
	ingressPrefixPathComponent       = "pathitems-prefix-500-ingress"
	ingressDefaultPathComponent      = "pathitems-default-501-ingress"
)

const ingressPathChartRoot = `{{- render "features-070-ingress-route-colocation" -}}
{{- render "features-900-ingress-path-candidates" -}}
{{- render "features-905-ingress-default-backend-candidates" -}}
# exact
{{- render "pathitems-exact-500-ingress" -}}
# prefix-exact
{{- render "pathitems-pfxexact-500-ingress" -}}
# prefix
{{- render "pathitems-prefix-500-ingress" -}}
# default
{{- render "pathitems-default-501-ingress" -}}
{%%
if tostring(extraContext | dig("failAfterPaths") | fallback(false)) == "true" {
  fail("forced failure after ingress paths")
}
%%}`

type ingressPathChartFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	services  *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestIngressPathChartSelectorsStayExactAndPromoteLoser(t *testing.T) {
	fixture := newIngressPathChartFixture(t)
	fixture.add(t, ingressPathResource("a", "2026-01-01T00:00:00Z", "example.test", "/same", "Exact", "svc-a", "", "v1"))
	fixture.add(t, ingressPathResource("b", "2026-01-01T00:00:01Z", "example.test", "/same", "Exact", "svc-b", "", "v1"))

	first := fixture.renderAndCommit(t)
	assertIngressPathWinner(t, first, "a", "svc-a")
	firstEvents := requireRenderEvents(t, first)
	require.Len(t, firstEvents, 1)
	assert.Equal(t, "b", firstEvents[0].Name)
	assert.Equal(t, "RouteConflict", firstEvents[0].Reason)
	fixture.assertAllExecutions(t, "a", 1)
	fixture.assertAllExecutions(t, "b", 1)

	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, firstEvents, requireRenderEvents(t, warm))
	fixture.assertAllExecutions(t, "a", 1)
	fixture.assertAllExecutions(t, "b", 1)

	fixture.update(t, ingressPathResource("b", "2026-01-01T00:00:01Z", "example.test", "/same", "Exact", "svc-b", "", "v2"))
	loserChanged := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, loserChanged.HAProxyConfig)
	assert.Equal(t, firstEvents, requireRenderEvents(t, loserChanged))
	fixture.assertAllExecutions(t, "a", 1)
	fixture.assertAllExecutions(t, "b", 2)

	fixture.delete(t, "a")
	promoted := fixture.renderAndCommit(t)
	assertIngressPathWinner(t, promoted, "b", "svc-b")
	assert.Empty(t, requireRenderEvents(t, promoted))
	assert.Equal(t, uint64(2), fixture.executions(ingressPathCandidateComponent, "b"))
	assert.Equal(t, uint64(3), fixture.executions(ingressExactPathComponent, "b"))
	assert.Equal(t, uint64(2), fixture.executions(ingressPrefixExactPathComponent, "b"))
	assert.Equal(t, uint64(2), fixture.executions(ingressPrefixPathComponent, "b"))
	assert.Equal(t, uint64(2), fixture.executions(ingressDefaultPathComponent, "b"))
	fixture.assertRetired(t, "a")
}

func TestIngressPathChartAbortAdmissionAndABADoNotPoisonSelectors(t *testing.T) {
	fixture := newIngressPathChartFixture(t)
	original := ingressPathResource("a", "2026-01-01T00:00:00Z", "example.test", "/same", "Exact", "svc-a", "", "v1")
	fixture.add(t, original)
	fixture.add(t, ingressPathResource("b", "2026-01-01T00:00:01Z", "example.test", "/same", "Exact", "svc-b", "", "v1"))
	baseline := fixture.renderAndCommit(t)
	baselineEvents := requireRenderEvents(t, baseline)
	assertIngressPathWinner(t, baseline, "a", "svc-a")
	baselineExecutions := fixture.engine.executionCounts()["ingresses/a"]

	fixture.update(t, ingressPathResource("a", "2026-01-01T00:00:02Z", "example.test", "/same", "Exact", "svc-a", "", "v2"))
	aborted, err := fixture.render(t)
	require.NoError(t, err)
	assertIngressPathWinner(t, aborted, "b", "svc-b")
	aborted.InputTransaction.Abort()
	fixture.assertCommittedOwner(t, "Exact|example.test/same", "default/a")
	assert.Equal(t, uint64(1), fixture.executions(ingressPathCandidateComponent, "a"))
	afterAbortExecutions := fixture.engine.executionCounts()["ingresses/a"]
	assert.Equal(t, baselineExecutions+7, afterAbortExecutions)

	fixture.update(t, original)
	recovered := fixture.renderAndCommit(t)
	assert.Equal(t, baseline.HAProxyConfig, recovered.HAProxyConfig)
	assert.Equal(t, baselineEvents, requireRenderEvents(t, recovered))
	fixture.assertCommittedOwner(t, "Exact|example.test/same", "default/a")
	assert.Equal(t, uint64(1), fixture.executions(ingressPathCandidateComponent, "a"))
	assert.Equal(t, afterAbortExecutions, fixture.engine.executionCounts()["ingresses/a"])

	invalid := ingressPathResource("a", "2026-01-01T00:00:00Z", "example.test", "/bad path", "Exact", "svc-a", "", "admission")
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: invalid}),
		}),
	)
	failed, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("ingresses", "default", "a"),
	)
	require.ErrorContains(t, err, "path containing whitespace")
	assert.Nil(t, failed)
	fixture.assertCommittedOwner(t, "Exact|example.test/same", "default/a")
	beforeWarm := fixture.engine.executionCounts()
	afterAdmission := fixture.renderAndCommit(t)
	assert.Equal(t, baseline.HAProxyConfig, afterAdmission.HAProxyConfig)
	assert.Equal(t, baselineEvents, requireRenderEvents(t, afterAdmission))
	assert.Equal(t, beforeWarm, fixture.engine.executionCounts())
}

func TestIngressDefaultBackendChartPromotesExactNewestWinner(t *testing.T) {
	fixture := newIngressPathChartFixture(t)
	older := ingressPathResource("older", "2026-01-01T00:00:00Z", "example.test", "/older", "Exact", "route-a", "default-a", "v1")
	newer := ingressPathResource("newer", "2026-01-01T00:00:01Z", "example.test", "/newer", "Exact", "route-b", "default-b", "v1")
	fixture.add(t, older)
	fixture.add(t, newer)

	first := fixture.renderAndCommit(t)
	assert.Contains(t, first.HAProxyConfig, "default_newer_svc_default-b_http")
	assert.NotContains(t, first.HAProxyConfig, "default_older_svc_default-a_http")
	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, uint64(1), fixture.executions(ingressDefaultPathComponent, "older"))
	assert.Equal(t, uint64(1), fixture.executions(ingressDefaultPathComponent, "newer"))

	fixture.delete(t, "newer")
	promoted := fixture.renderAndCommit(t)
	assert.Contains(t, promoted.HAProxyConfig, "default_older_svc_default-a_http")
	assert.NotContains(t, promoted.HAProxyConfig, "default_newer_svc_default-b_http")
	assert.Equal(t, uint64(2), fixture.executions(ingressDefaultPathComponent, "older"))
	assert.Equal(t, uint64(1), fixture.executions(ingressDefaultCandidateComponent, "older"))
}

func TestIngressPathChartWarmAndColdOutputsMatch(t *testing.T) {
	resources := []map[string]any{
		ingressPathResource("a", "2026-01-01T00:00:00Z", "a.example", "/a", "Exact", "svc-a", "default-a", "v1"),
		ingressPathResource("b", "2026-01-01T00:00:01Z", "b.example", "/b/", "Prefix", "svc-b", "", "v1"),
	}
	warmFixture := newIngressPathChartFixture(t)
	for _, resource := range resources {
		warmFixture.add(t, resource)
	}
	first := warmFixture.renderAndCommit(t)
	firstEvents := requireRenderEvents(t, first)
	warm := warmFixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	warmEvents := requireRenderEvents(t, warm)
	assert.Equal(t, firstEvents, warmEvents)

	coldFixture := newIngressPathChartFixture(t)
	for _, resource := range resources {
		coldFixture.add(t, resource)
	}
	cold := coldFixture.renderAndCommit(t)
	assert.Equal(t, warm.HAProxyConfig, cold.HAProxyConfig)
	assert.Equal(t, warmEvents, requireRenderEvents(t, cold))
}

func BenchmarkIngressPathChartIncrementalScaling(b *testing.B) {
	for _, resources := range []int{1, 128, 8192} {
		b.Run(fmt.Sprintf("no-change-%d", resources), func(b *testing.B) {
			fixture := benchmarkIngressPathChartFixture(b, resources)
			before := fixture.totalExecutions()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				fixture.renderAndCommit(b)
			}
			b.StopTimer()
			b.ReportMetric(float64(fixture.totalExecutions()-before)/float64(b.N), "component-executions/op")
		})
		b.Run(fmt.Sprintf("one-change-%d", resources), func(b *testing.B) {
			fixture := benchmarkIngressPathChartFixture(b, resources)
			before := fixture.totalExecutions()
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := range b.N {
				name := "ingress-00000000"
				fixture.update(b, ingressPathResource(name, "", "", "", "", "", "", fmt.Sprintf("v%d", iteration)))
				fixture.renderAndCommit(b)
			}
			b.StopTimer()
			b.ReportMetric(float64(fixture.totalExecutions()-before)/float64(b.N), "component-executions/op")
		})
	}
}

func newIngressPathChartFixture(tb testing.TB) *ingressPathChartFixture {
	tb.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"failAfterPaths": false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1", Resources: "ingresses",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"services": {
				APIVersion: "v1", Resources: "services",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: loadIngressPathChartSnippets(tb),
		HAProxyConfig:    config.HAProxyConfig{Template: ingressPathChartRoot},
	}
	types := ingressPathSchemaTypes(tb)
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(tb, err)
	engine := newDynamicBindingCountingEngine(tb, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	ingresses := k8sstore.NewMemoryStore(2)
	services := k8sstore.NewMemoryStore(2)
	return &ingressPathChartFixture{
		config: cfg, service: service, engine: engine, ingresses: ingresses, services: services,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{
			"ingresses": ingresses, "services": services,
		}),
	}
}

func ingressPathSchemaTypes(tb testing.TB) *typebootstrap.Result {
	tb.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(tb, ok)
	schemaRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "tests", "schemas")
	fetcher, err := schemafetcher.NewDirFetcher(schemaRoot)
	require.NoError(tb, err)
	result, err := typebootstrap.Bootstrap(tb.Context(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{
			{Name: "ingresses", GVK: schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}},
			{Name: "services", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Service"}},
		},
		Fetcher: fetcher,
		Logger:  slog.Default(),
	})
	require.NoError(tb, err)
	require.Empty(tb, result.Errors)
	require.Len(tb, result.Types, 2)
	return result
}

func loadIngressPathChartSnippets(tb testing.TB) map[string]config.TemplateSnippet {
	tb.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(tb, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	wanted := map[string]bool{
		"util-host-key": true, "util-webhook-reject-or-warn": true, "util-validate-config-value": true,
		"util-backend-name-ingress": true, "features-070-ingress-route-colocation": true,
		ingressPathCandidateComponent: true, ingressDefaultCandidateComponent: true,
		"util-path-map-entry-ingress": true, ingressExactPathComponent: true,
		ingressPrefixExactPathComponent: true, ingressPrefixPathComponent: true, ingressDefaultPathComponent: true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, relativePath := range []string{"base/library.yaml", "ingress/library.yaml"} {
		content, err := os.ReadFile(filepath.Join(chartRoot, relativePath))
		require.NoError(tb, err)
		var library ingressBackendChartLibrary
		require.NoError(tb, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source: chartSnippet.Incremental.Source, BindingsTemplate: chartSnippet.Incremental.BindingsTemplate,
					Group: chartSnippet.Incremental.Group, Consumes: chartSnippet.Incremental.Consumes,
					OptionalConsumes: chartSnippet.Incremental.OptionalConsumes,
					Effects:          chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(tb, result, len(wanted))
	return result
}

func ingressPathResource(
	name, timestamp, host, path, pathType, service, defaultService, revision string,
) map[string]any {
	rules := []any{}
	if host != "" || path != "" {
		rule := map[string]any{"host": host}
		if path != "" {
			rule["http"] = map[string]any{"paths": []any{map[string]any{
				"path": path, "pathType": pathType,
				"backend": map[string]any{"service": map[string]any{
					"name": service, "port": map[string]any{"name": "http"},
				}},
			}}}
		}
		rules = append(rules, rule)
	}
	spec := map[string]any{"rules": rules}
	if defaultService != "" {
		spec["defaultBackend"] = map[string]any{"service": map[string]any{
			"name": defaultService, "port": map[string]any{"name": "http"},
		}}
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
		"metadata": map[string]any{
			"namespace": "default", "name": name, "creationTimestamp": timestamp,
			"annotations": map[string]any{"test.haptic/revision": revision},
		},
		"spec": spec,
	}
}

func benchmarkIngressPathChartFixture(tb testing.TB, count int) *ingressPathChartFixture {
	tb.Helper()
	fixture := newIngressPathChartFixture(tb)
	for index := range count {
		name := fmt.Sprintf("ingress-%08d", index)
		fixture.add(tb, ingressPathResource(name, "", "", "", "", "", "", "initial"))
	}
	fixture.renderAndCommit(tb)
	return fixture
}

func (f *ingressPathChartFixture) add(tb testing.TB, resource map[string]any) {
	tb.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(tb, f.ingresses.Add(resource, []string{"default", name}))
}

func (f *ingressPathChartFixture) update(tb testing.TB, resource map[string]any) {
	tb.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(tb, f.ingresses.Update(resource, []string{"default", name}))
}

func (f *ingressPathChartFixture) delete(tb testing.TB, name string) {
	tb.Helper()
	require.NoError(tb, f.ingresses.Delete("default", name, []string{"default", name}))
}

func (f *ingressPathChartFixture) render(tb testing.TB) (*RenderResult, error) {
	tb.Helper()
	return f.service.Render(tb.Context(), f.provider, rendercontext.RenderModeReconcile)
}

func (f *ingressPathChartFixture) renderAndCommit(tb testing.TB) *RenderResult {
	tb.Helper()
	result, err := f.render(tb)
	require.NoError(tb, err)
	require.NoError(tb, result.InputTransaction.Commit(tb.Context()))
	waitForIncrementalCache(tb, f.service)
	return result
}

func (f *ingressPathChartFixture) executions(componentName, ingress string) uint64 {
	component := f.service.incremental.components[componentName]
	query := componentQueryKey(&component, "ingresses", "default", ingress)
	return f.service.incremental.graph.Counters(query).Executions
}

func (f *ingressPathChartFixture) assertAllExecutions(tb testing.TB, ingress string, want uint64) {
	tb.Helper()
	for _, component := range []string{
		"features-070-ingress-route-colocation", ingressPathCandidateComponent,
		ingressDefaultCandidateComponent, ingressExactPathComponent, ingressPrefixExactPathComponent,
		ingressPrefixPathComponent, ingressDefaultPathComponent,
	} {
		assert.Equal(tb, want, f.executions(component, ingress), component+"/"+ingress)
	}
}

func (f *ingressPathChartFixture) assertRetired(tb testing.TB, ingress string) {
	tb.Helper()
	for _, componentName := range []string{
		"features-070-ingress-route-colocation", ingressPathCandidateComponent,
		ingressDefaultCandidateComponent, ingressExactPathComponent, ingressPrefixExactPathComponent,
		ingressPrefixPathComponent, ingressDefaultPathComponent,
	} {
		component := f.service.incremental.components[componentName]
		query := componentQueryKey(&component, "ingresses", "default", ingress)
		_, found := f.service.incremental.graph.Value(query)
		assert.False(tb, found, componentName)
		assert.Zero(tb, f.service.incremental.graph.Counters(query), componentName)
	}
}

func (f *ingressPathChartFixture) assertCommittedOwner(tb testing.TB, key, owner string) {
	tb.Helper()
	input, err := incrementalSelectorInput(
		f.service.incremental.snapshot.groupIndexes["ingress-path-candidates"],
		"ingress-path-candidates", "owners", key,
	)
	require.NoError(tb, err)
	require.True(tb, input.Found)
	assert.Contains(tb, string(input.Value), `"owner":"`+owner+`"`)
}

func (f *ingressPathChartFixture) totalExecutions() int {
	total := 0
	for _, executions := range f.engine.executionCounts() {
		total += executions
	}
	return total
}

func assertIngressPathWinner(tb testing.TB, result *RenderResult, name, service string) {
	tb.Helper()
	assert.Contains(tb, result.HAProxyConfig, "# Ingress: default/"+name+" (1 paths)")
	assert.Contains(tb, result.HAProxyConfig, "example.test/same BACKEND:default_"+name+"_svc_"+service+"_http")
	other := "a"
	if name == "a" {
		other = "b"
	}
	assert.NotContains(tb, result.HAProxyConfig, "# Ingress: default/"+other+" (1 paths)")
	assert.False(tb, strings.Contains(result.HAProxyConfig, "BACKEND:default_"+other+"_svc_"))
}
