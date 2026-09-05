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
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const environmentIdentityBindings = `{{ toJSON(map[string]any{
  "routes": map[string]any{
    "crtList": capabilities["supports_crt_list"],
  },
}) }}`

const environmentIdentityProgramA = `{%%
var capability = tostring(props | dig("crtList") | fallback(false))
recordEvent(item, "EnvironmentIdentity", "program-a/crt-list=" + capability)
show "program-a/crt-list=" + capability + "\n"
%%}`

const environmentIdentityProgramB = `{%%
var capability = tostring(props | dig("crtList") | fallback(false))
recordEvent(item, "EnvironmentIdentity", "program-b/crt-list=" + capability)
show "program-b/crt-list=" + capability + "\n"
%%}`

func TestRenderServiceCapabilityIdentityInvalidatesComponentsAtomically(t *testing.T) {
	fixture := newEnvironmentIdentityFixture(t, environmentIdentityProgramA)
	fixture.addRoute(t)
	query := fixture.query()

	baseline := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, baseline, "program-a/crt-list=false\n")
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, map[string]int{"routes/route": 1}, fixture.engine.executionCounts())

	warm := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, warm, "program-a/crt-list=false\n")
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, map[string]int{"routes/route": 1}, fixture.engine.executionCounts())

	fixture.service.SetCapabilities(dataplane.Capabilities{SupportsHTTP2: true})
	unrelated := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, unrelated, "program-a/crt-list=false\n")
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, map[string]int{"routes/route": 1}, fixture.engine.executionCounts())

	committedSnapshot := fixture.service.incremental.snapshot
	committedCounters := fixture.service.incremental.graph.Counters(query)
	fixture.service.SetCapabilities(dataplane.Capabilities{
		SupportsCrtList: true,
		SupportsHTTP2:   true,
	})
	fixture.config.TemplatingSettings.ExtraContext["failRoot"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced root failure")
	assert.Nil(t, failed)
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, committedCounters, fixture.service.incremental.graph.Counters(query))
	assert.Equal(t, map[string]int{"routes/route": 2}, fixture.engine.executionCounts())

	fixture.service.SetCapabilities(dataplane.Capabilities{SupportsHTTP2: true})
	fixture.config.TemplatingSettings.ExtraContext["failRoot"] = false
	recovered := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, recovered, "program-a/crt-list=false\n")
	assert.Equal(t, committedCounters, fixture.service.incremental.graph.Counters(query))
	assert.Equal(t, map[string]int{"routes/route": 2}, fixture.engine.executionCounts())

	fixture.service.SetCapabilities(dataplane.Capabilities{
		SupportsCrtList: true,
		SupportsHTTP2:   true,
	})
	changed := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, changed, "program-a/crt-list=true\n")
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, map[string]int{"routes/route": 3}, fixture.engine.executionCounts())

	changedWarm := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, changedWarm, "program-a/crt-list=true\n")
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, map[string]int{"routes/route": 3}, fixture.engine.executionCounts())

	fixture.service.SetCapabilities(dataplane.Capabilities{SupportsHTTP2: true})
	aba := fixture.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, aba, "program-a/crt-list=false\n")
	assert.Equal(t, uint64(3), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, map[string]int{"routes/route": 4}, fixture.engine.executionCounts())
}

func TestRenderServiceProgramReloadDoesNotReuseOldComponentResults(t *testing.T) {
	first := newEnvironmentIdentityFixture(t, environmentIdentityProgramA)
	first.addRoute(t)
	firstResult := first.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, firstResult, "program-a/crt-list=false\n")
	assert.Equal(t, map[string]int{"routes/route": 1}, first.engine.executionCounts())
	require.NoError(t, first.service.RetireIncrementalCache())

	second := newEnvironmentIdentityFixture(t, environmentIdentityProgramB)
	second.provider = first.provider
	secondResult := second.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, secondResult, "program-b/crt-list=false\n")
	assert.Equal(t, map[string]int{"routes/route": 1}, second.engine.executionCounts())

	secondWarm := second.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, secondWarm, "program-b/crt-list=false\n")
	assert.Equal(t, map[string]int{"routes/route": 1}, second.engine.executionCounts())
}

func TestRenderServiceProgramReloadRejectsStaleIncrementalCache(t *testing.T) {
	first := newEnvironmentIdentityFixture(t, environmentIdentityProgramA)
	first.addRoute(t)
	firstResult := first.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, firstResult, "program-a/crt-list=false\n")
	firstSnapshot := first.service.incremental.snapshot
	firstGeneration := first.service.incremental.graph.Generation()
	firstPlan := first.service.lastPlan
	firstOutput := first.service.lastOutputSnapshot
	firstCycle := first.service.lastCycleSnapshot

	second := newEnvironmentIdentityFixture(t, environmentIdentityProgramB)
	second.provider = first.provider
	second.service.incremental.graph = first.service.incremental.graph
	second.service.incremental.snapshot = first.service.incremental.snapshot
	assertEnvironmentProgramRenderFailsClosed(t, second, "incremental environment identity")

	assert.Same(t, firstSnapshot, first.service.incremental.snapshot)
	assert.Equal(t, firstGeneration, first.service.incremental.graph.Generation())
	assert.Same(t, firstPlan, first.service.lastPlan)
	assert.Same(t, firstOutput, first.service.lastOutputSnapshot)
	assert.Same(t, firstCycle, first.service.lastCycleSnapshot)
	assert.Equal(t, map[string]int{"routes/route": 1}, first.engine.executionCounts())

	fresh := newEnvironmentIdentityFixture(t, environmentIdentityProgramB)
	fresh.provider = first.provider
	freshResult := fresh.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, freshResult, "program-b/crt-list=false\n")
	assert.Equal(t, map[string]int{"routes/route": 1}, fresh.engine.executionCounts())
	freshWarm := fresh.renderAndCommitCacheReady(t)
	assertEnvironmentIdentityResult(t, freshWarm, "program-b/crt-list=false\n")
	assert.Equal(t, map[string]int{"routes/route": 1}, fresh.engine.executionCounts())
}

func assertEnvironmentProgramRenderFailsClosed(
	t *testing.T,
	fixture *environmentIdentityFixture,
	wantError string,
) {
	t.Helper()
	snapshot := fixture.service.incremental.snapshot
	generation := fixture.service.incremental.graph.Generation()
	plan := fixture.service.lastPlan
	output := fixture.service.lastOutputSnapshot
	cycle := fixture.service.lastCycleSnapshot
	planIdentity := fixture.service.lastPlanIdentity
	renderCache := fixture.service.lastRenderCache
	exactCycle := fixture.service.exactCycleCandidate
	outputGeneration := fixture.service.publishedOutputGeneration
	executions := fixture.engine.executionCounts()

	result, err := fixture.service.Render(
		t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
	)
	require.ErrorContains(t, err, wantError)
	assert.Nil(t, result)
	assert.Same(t, snapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, generation, fixture.service.incremental.graph.Generation())
	assert.Same(t, plan, fixture.service.lastPlan)
	assert.Same(t, output, fixture.service.lastOutputSnapshot)
	assert.Same(t, cycle, fixture.service.lastCycleSnapshot)
	assert.Same(t, planIdentity, fixture.service.lastPlanIdentity)
	assert.Same(t, renderCache, fixture.service.lastRenderCache)
	assert.Same(t, exactCycle, fixture.service.exactCycleCandidate)
	assert.Equal(t, outputGeneration, fixture.service.publishedOutputGeneration)
	assert.Equal(t, executions, fixture.engine.executionCounts())
}

func TestRenderServiceSchemaTypeDeclarationChangeStartsColdForArbitraryCRD(t *testing.T) {
	withMarker := newSchemaEnvironmentFixture(t, true)
	withMarker.addWidget(t)
	resourceRevision, resourceBytes := schemaEnvironmentWidgetObservation(t, withMarker.widgets)
	withMarkerCold := withMarker.renderAndCommitCacheReady(t)
	require.Contains(t, withMarkerCold.HAProxyConfig, `"marker":"kept"`)
	assert.Equal(t, map[string]int{"widgets/widget": 1}, withMarker.engine.executionCounts())

	withMarkerWarm := withMarker.renderAndCommitCacheReady(t)
	assertSchemaEnvironmentObservableEqual(t, withMarkerCold, withMarkerWarm)
	assert.Equal(t, map[string]int{"widgets/widget": 1}, withMarker.engine.executionCounts())

	withoutMarker := newSchemaEnvironmentFixture(t, false)
	withoutMarker.provider = withMarker.provider
	withoutMarkerCold := withoutMarker.renderAndCommitCacheReady(t)
	require.NotContains(t, withoutMarkerCold.HAProxyConfig, `"marker":"kept"`)
	assert.NotEqual(t, withMarkerCold.HAProxyConfig, withoutMarkerCold.HAProxyConfig)
	assert.Equal(t, map[string]int{"widgets/widget": 1}, withoutMarker.engine.executionCounts())

	withoutMarkerWarm := withoutMarker.renderAndCommitCacheReady(t)
	assertSchemaEnvironmentObservableEqual(t, withoutMarkerCold, withoutMarkerWarm)
	assert.Equal(t, map[string]int{"widgets/widget": 1}, withoutMarker.engine.executionCounts())
	afterRevision, afterBytes := schemaEnvironmentWidgetObservation(t, withMarker.widgets)
	assert.Equal(t, resourceRevision, afterRevision)
	assert.Equal(t, resourceBytes, afterBytes)
	assert.NotEqual(t, requireRenderEvents(t, withMarkerCold), requireRenderEvents(t, withoutMarkerCold))
}

func TestRenderServiceSchemaTypeDeclarationRejectsStaleIncrementalCache(t *testing.T) {
	withMarker := newSchemaEnvironmentFixture(t, true)
	withMarker.addWidget(t)
	withMarkerResult := withMarker.renderAndCommitCacheReady(t)
	require.Contains(t, withMarkerResult.HAProxyConfig, `"marker":"kept"`)

	withoutMarker := newSchemaEnvironmentFixture(t, false)
	withoutMarker.provider = withMarker.provider
	withoutMarkerResult := withoutMarker.renderAndCommitCacheReady(t)
	require.NotContains(t, withoutMarkerResult.HAProxyConfig, `"marker":"kept"`)

	withoutMarker.service.incremental.graph = withMarker.service.incremental.graph
	withoutMarker.service.incremental.snapshot = withMarker.service.incremental.snapshot
	assertSchemaEnvironmentRenderFailsClosed(t, withoutMarker, "incremental environment identity")
}

func TestRenderServiceSchemaTypeDeclarationRejectsForgedIdentity(t *testing.T) {
	tests := map[string]func(*incrementalRenderState){
		"nil": func(state *incrementalRenderState) {
			state.environment = nil
		},
		"zero": func(state *incrementalRenderState) {
			state.environment = &incrementalEnvironmentAuthority{}
		},
		"copy": func(state *incrementalRenderState) {
			copied := *state.environment
			state.environment = &copied
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newSchemaEnvironmentFixture(t, true)
			fixture.addWidget(t)
			fixture.renderAndCommitCacheReady(t)
			poison(fixture.service.incremental)
			assertSchemaEnvironmentRenderFailsClosed(t, fixture, "incremental environment identity")
		})
	}
}

func TestRenderServiceSchemaTypeDeclarationRejectsStaleIdentity(t *testing.T) {
	withMarker := newSchemaEnvironmentFixture(t, true)
	withMarker.addWidget(t)
	withMarker.renderAndCommitCacheReady(t)

	withoutMarker := newSchemaEnvironmentFixture(t, false)
	withoutMarker.provider = withMarker.provider
	withoutMarker.renderAndCommitCacheReady(t)
	withoutMarker.service.incremental.environment.declarations =
		withMarker.service.incremental.environment.declarations
	assertSchemaEnvironmentRenderFailsClosed(
		t, withoutMarker, "incremental environment type declaration identity",
	)
}

func TestRenderServiceSchemaTypeDeclarationChangeRevokesCacheAcrossABA(t *testing.T) {
	fixture := newSchemaEnvironmentFixture(t, true)
	fixture.addWidget(t)
	fixture.renderAndCommitCacheReady(t)
	original := fixture.service.typedResourceTypes["widgets"]
	changed := schemaEnvironmentTypes(t, false).Types["widgets"]
	require.NotEqual(t, original, changed)

	fixture.service.typedResourceTypes["widgets"] = changed
	assertSchemaEnvironmentRenderFailsClosed(t, fixture, "type declarations changed")

	fixture.service.typedResourceTypes["widgets"] = original
	assertSchemaEnvironmentRenderFailsClosed(t, fixture, "type declarations changed")
}

func assertSchemaEnvironmentRenderFailsClosed(
	t *testing.T,
	fixture *schemaEnvironmentFixture,
	wantError string,
) {
	t.Helper()
	snapshot := fixture.service.incremental.snapshot
	generation := fixture.service.incremental.graph.Generation()
	plan := fixture.service.lastPlan
	executions := fixture.engine.executionCounts()
	result, err := fixture.service.Render(
		t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
	)

	require.ErrorContains(t, err, wantError)
	assert.Nil(t, result)
	assert.Same(t, snapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, generation, fixture.service.incremental.graph.Generation())
	assert.Same(t, plan, fixture.service.lastPlan)
	assert.Equal(t, executions, fixture.engine.executionCounts())
}

const schemaEnvironmentComponent = `{%%
var typed = resources.widgets.GetSingle("default", "widget")
var encoded = toJSON(typed)
recordEvent(item, "SchemaIdentity", encoded)
show encoded + "\n"
%%}`

type schemaEnvironmentFixture struct {
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	widgets  *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newSchemaEnvironmentFixture(t *testing.T, includeMarker bool) *schemaEnvironmentFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"widgets": {
				APIVersion: "widgets.example.test/v1",
				Resources:  "widgets",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"schema-environment": {
				Name:     "schema-environment",
				Requires: []string{"widgets"},
				Incremental: &config.IncrementalTemplate{
					Source:  "widgets",
					Effects: []config.IncrementalEffect{config.IncrementalEffectRecordEvent},
				},
				Template: schemaEnvironmentComponent,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "schema-environment" }}`},
	}
	types := schemaEnvironmentTypes(t, includeMarker)
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), TypedResourceTypes: types.Types,
	})
	widgets := k8sstore.NewMemoryStore(2)
	return &schemaEnvironmentFixture{
		service:  service,
		engine:   engine,
		widgets:  widgets,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"widgets": widgets}),
	}
}

func schemaEnvironmentTypes(t *testing.T, includeMarker bool) *typebootstrap.Result {
	t.Helper()
	properties := map[string]spec.Schema{
		"stable": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
	}
	if includeMarker {
		properties["marker"] = spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}}
	}
	gvk := schema.GroupVersionKind{Group: "widgets.example.test", Version: "v1", Kind: "Widget"}
	resourceSchema := &spec.Schema{SchemaProps: spec.SchemaProps{
		Type: spec.StringOrArray{"object"},
		Properties: map[string]spec.Schema{
			"apiVersion": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
			"kind":       {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
			"metadata":   {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"object"}}},
			"spec": {SchemaProps: spec.SchemaProps{
				Type:       spec.StringOrArray{"object"},
				Properties: properties,
			}},
		},
	}}
	result, err := typebootstrap.Bootstrap(t.Context(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{{Name: "widgets", GVK: gvk}},
		Fetcher:   schemafetcher.NewMapFetcher(map[schema.GroupVersionKind]*spec.Schema{gvk: resourceSchema}),
		Logger:    slog.Default(),
	})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Contains(t, result.Types, "widgets")
	return result
}

func (f *schemaEnvironmentFixture) addWidget(t *testing.T) {
	t.Helper()
	require.NoError(t, f.widgets.Add(map[string]any{
		"apiVersion": "widgets.example.test/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      "widget",
			"uid":       "widget-uid",
		},
		"spec": map[string]any{"stable": "same", "marker": "kept"},
	}, []string{"default", "widget"}))
}

func schemaEnvironmentWidgetObservation(
	t *testing.T,
	widgets *k8sstore.MemoryStore,
) (revision stores.Revision, encoded []byte) {
	t.Helper()
	snapshot, err := widgets.Pin()
	require.NoError(t, err)
	resource, found, err := snapshot.GetIdentity("default", "widget")
	require.NoError(t, err)
	require.True(t, found)
	encoded, err = json.Marshal(resource)
	require.NoError(t, err)
	return snapshot.IdentityRevision("default", "widget"), encoded
}

func (f *schemaEnvironmentFixture) renderAndCommitCacheReady(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func assertSchemaEnvironmentObservableEqual(t *testing.T, want, got *RenderResult) {
	t.Helper()
	assert.Equal(t, want.HAProxyConfig, got.HAProxyConfig)
	assert.Equal(t, requireAuxiliaryFiles(t, want), requireAuxiliaryFiles(t, got))
	assert.Equal(t, requireRenderPlan(t, want), requireRenderPlan(t, got))
	assert.Equal(t, want.PlanID, got.PlanID)
	assert.Equal(t, materializedStatusPatches(t, want), materializedStatusPatches(t, got))
	assert.Equal(t, requireRenderEvents(t, want), requireRenderEvents(t, got))
	assert.Equal(t, requireRenderedResources(t, want), requireRenderedResources(t, got))
}

type environmentIdentityFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	routes   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newEnvironmentIdentityFixture(
	t *testing.T,
	program string,
) *environmentIdentityFixture {
	t.Helper()
	var capabilities dataplane.Capabilities
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]any{"failRoot": false},
		},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"environment": {
				Name: "environment",
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: environmentIdentityBindings,
					Effects:          []config.IncrementalEffect{config.IncrementalEffectRecordEvent},
				},
				Template: program,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "environment" }}{%%
if tostring(extraContext["failRoot"]) == "true" {
  fail("forced root failure")
}
%%}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: capabilities,
	})
	routes := k8sstore.NewMemoryStore(2)
	return &environmentIdentityFixture{
		config:   cfg,
		service:  service,
		engine:   engine,
		routes:   routes,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	}
}

func (f *environmentIdentityFixture) addRoute(t *testing.T) {
	t.Helper()
	require.NoError(t, f.routes.Add(
		dynamicBindingResource("route", "value"),
		[]string{"default", "route"},
	))
}

func (f *environmentIdentityFixture) query() incremental.QueryKey {
	tempComponent32 := f.service.incremental.components["environment"]
	return componentQueryKey(&tempComponent32, "routes", "default", "route")
}

func (f *environmentIdentityFixture) renderAndCommitCacheReady(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func assertEnvironmentIdentityResult(t *testing.T, result *RenderResult, expected string) {
	t.Helper()
	assert.Equal(t, expected, result.HAProxyConfig)
	events := requireRenderEvents(t, result)
	assert.Equal(t, []templating.RenderedEvent{{
		Namespace:  "default",
		Name:       "route",
		APIVersion: "example.test/v1",
		Kind:       "Example",
		Type:       templating.EventTypeWarning,
		Reason:     "EnvironmentIdentity",
		Message:    expected[:len(expected)-1],
	}}, events)
}
