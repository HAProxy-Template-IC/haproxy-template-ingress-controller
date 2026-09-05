// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalBindingPlanCacheTracksEveryPlannerInputExactly(t *testing.T) {
	state, engine := newBindingAmbientState(t)
	baselineContext := bindingAmbientContext()
	baselinePlan, baselineCache, exact, err := state.prepareBindingPlan(t.Context(), baselineContext)
	require.NoError(t, err)
	require.True(t, exact)
	require.NotNil(t, baselineCache)
	require.Equal(t, 1, engine.plannerExecutionCount())
	state.snapshot.bindingCache = baselineCache

	unchanged, cache, exact, err := state.prepareBindingPlan(t.Context(), bindingAmbientContext())
	require.NoError(t, err)
	require.True(t, exact)
	require.Same(t, baselineCache, cache)
	assert.True(t, sameIncrementalBindingPlans(baselinePlan, unchanged))
	assert.Equal(t, 1, engine.plannerExecutionCount())

	unused := bindingAmbientContext()
	unused["controller"] = map[string]any{"unavailable": make(chan struct{})}
	_, cache, _, err = state.prepareBindingPlan(t.Context(), unused)
	require.NoError(t, err)
	require.Same(t, baselineCache, cache)
	assert.Equal(t, 1, engine.plannerExecutionCount())

	tests := map[string]func(map[string]any){
		"extra context": func(value map[string]any) {
			value["extraContext"].(map[string]any)["value"] = "changed"
		},
		"capabilities": func(value map[string]any) {
			value["capabilities"].(map[string]any)["supports_crt_list"] = true
		},
		"current config": func(value map[string]any) {
			value["currentConfig"].(*renderplan.CurrentConfig).
				ServerIndex["backend"]["server"] = renderplan.ServerAddr{Address: "changed"}
		},
		"current files": func(value map[string]any) {
			(*value["currentFiles"].(*map[string]string))["state"] = "changed"
		},
		"paths": func(value map[string]any) {
			value["pathResolver"].(*templating.PathResolver).BaseDir = "/changed"
		},
		"runtime": func(value map[string]any) {
			value["runtimeEnvironment"].(*templating.RuntimeEnvironment).GOMAXPROCS = 8
		},
		"template snippets": func(value map[string]any) {
			value["templateSnippets"] = []string{"dynamic", "new"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			state.snapshot.bindingCache = baselineCache
			changedContext := bindingAmbientContext()
			mutate(changedContext)
			before := engine.plannerExecutionCount()
			changed, candidate, authenticated, err := state.prepareBindingPlan(t.Context(), changedContext)
			require.NoError(t, err)
			require.True(t, authenticated)
			require.NotNil(t, candidate)
			assert.Equal(t, before+1, engine.plannerExecutionCount())
			assert.False(t, sameIncrementalBindingPlans(baselinePlan, changed))
		})
	}
}

func TestIncrementalBindingPlanPrecomputesStaticBindings(t *testing.T) {
	cfg := &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"alpha": {APIVersion: "example.test/v1", Resources: "alphas"},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"static": {
				Name:        "static",
				Incremental: &config.IncrementalTemplate{Source: "alpha"},
			},
		},
	}
	state := newIncrementalRenderState(cfg, nil)
	require.NotNil(t, state)
	first, cache, exact, err := state.prepareBindingPlan(t.Context(), nil)
	require.NoError(t, err)
	require.True(t, exact)
	require.Nil(t, cache)
	second, _, exact, err := state.prepareBindingPlan(t.Context(), map[string]any{"unrelated": true})
	require.NoError(t, err)
	require.True(t, exact)
	assert.Same(t, first, second)
	require.Len(t, first.bindings, 1)
	assert.Equal(t, incrementalBinding{component: "static", source: "alpha", props: []byte("{}")}, first.bindings[0])
}

func TestIncrementalBindingPlanCacheTransactionsCannotPoisonCommittedPlan(t *testing.T) {
	bindingsA := map[string]any{"alpha": map[string]any{"label": "A"}}
	bindingsB := map[string]any{"alpha": map[string]any{"label": "B"}}
	bindingsC := map[string]any{"alpha": map[string]any{"label": "C"}}
	fixture := newDynamicBindingFixture(t, bindingsA)
	fixture.add(t, "alpha", "item", "value")

	requireBindingPlanCacheReuse(t, fixture)
	requireBindingPlanCacheSurvivesAbortedRenders(t, fixture, bindingsA, bindingsB)
	requireBindingPlanCacheSurvivesConcurrentCommits(t, fixture, bindingsB, bindingsC)
	requireBindingPlanCacheSurvivesRootMutation(t, fixture, bindingsA, bindingsB)
}

func requireBindingPlanCacheReuse(t *testing.T, fixture *dynamicBindingFixture) {
	t.Helper()
	assert.Equal(t, "alpha/A/item=value@reconcile\n", fixture.renderAndCommitCacheReady(
		t, fixture.provider,
	))
	assert.Equal(t, 1, fixture.engine.plannerExecutionCount())
	assert.Equal(t, "alpha/A/item=value@reconcile\n", fixture.renderAndCommitCacheReady(
		t, fixture.provider,
	))
	assert.Equal(t, 1, fixture.engine.plannerExecutionCount())
	assert.Equal(t, map[string]int{"alpha/item": 1}, fixture.engine.executionCounts())

	fixture.config.TemplatingSettings.ExtraContext["unused"] = "changed"
	assert.Equal(t, "alpha/A/item=value@reconcile\n", fixture.renderAndCommitCacheReady(
		t, fixture.provider,
	))
	assert.Equal(t, 2, fixture.engine.plannerExecutionCount())
	assert.Equal(t, map[string]int{"alpha/item": 1}, fixture.engine.executionCounts())
	delete(fixture.config.TemplatingSettings.ExtraContext, "unused")
	fixture.renderAndCommitCacheReady(t, fixture.provider)
	assert.Equal(t, 3, fixture.engine.plannerExecutionCount())
}

func requireBindingPlanCacheSurvivesAbortedRenders(
	t *testing.T,
	fixture *dynamicBindingFixture,
	bindingsA, bindingsB map[string]any,
) {
	t.Helper()
	fixture.config.TemplatingSettings.ExtraContext["failRoot"] = true
	_, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced root failure")
	failedCalls := fixture.engine.plannerExecutionCount()
	delete(fixture.config.TemplatingSettings.ExtraContext, "failRoot")
	fixture.renderAndCommitCacheReady(t, fixture.provider)
	assert.Equal(t, failedCalls, fixture.engine.plannerExecutionCount())

	fixture.setBindings(bindingsB)
	aborted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	abortedCalls := fixture.engine.plannerExecutionCount()
	aborted.InputTransaction.Abort()
	fixture.setBindings(bindingsA)
	fixture.renderAndCommitCacheReady(t, fixture.provider)
	assert.Equal(t, abortedCalls, fixture.engine.plannerExecutionCount())

	fixture.setBindings(bindingsB)
	admission, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeAdmission)
	require.NoError(t, err)
	require.NoError(t, admission.InputTransaction.Commit(t.Context()))
	admissionCalls := fixture.engine.plannerExecutionCount()
	fixture.setBindings(bindingsA)
	fixture.renderAndCommitCacheReady(t, fixture.provider)
	assert.Equal(t, admissionCalls, fixture.engine.plannerExecutionCount())
}

func requireBindingPlanCacheSurvivesConcurrentCommits(
	t *testing.T,
	fixture *dynamicBindingFixture,
	bindingsB, bindingsC map[string]any,
) {
	t.Helper()
	fixture.setBindings(bindingsB)
	pendingB, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	fixture.setBindings(bindingsC)
	pendingC, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, pendingC.InputTransaction.Commit(t.Context()))
	require.ErrorIs(t, pendingB.InputTransaction.Commit(t.Context()), errRenderOutputGenerationSuperseded)
	concurrentCalls := fixture.engine.plannerExecutionCount()
	fixture.setBindings(bindingsC)
	assert.Equal(t, "alpha/C/item=value@reconcile\n", fixture.renderAndCommitCacheReady(
		t, fixture.provider,
	))
	assert.Equal(t, concurrentCalls, fixture.engine.plannerExecutionCount())
}

func requireBindingPlanCacheSurvivesRootMutation(
	t *testing.T,
	fixture *dynamicBindingFixture,
	bindingsA, bindingsB map[string]any,
) {
	t.Helper()
	fixture.setBindings(bindingsA)
	fixture.config.TemplatingSettings.ExtraContext["mutateRoot"] = true
	fixture.config.TemplatingSettings.ExtraContext["rootBindings"] = bindingsB
	assert.Equal(t, "alpha/A/item=value@reconcile\n", fixture.renderAndCommitCacheReady(
		t, fixture.provider,
	))
	mutationCalls := fixture.engine.plannerExecutionCount()
	assert.Equal(t, bindingsA, fixture.config.TemplatingSettings.ExtraContext["bindings"])
	assert.Equal(t, "alpha/A/item=value@reconcile\n", fixture.renderAndCommitCacheReady(
		t, fixture.provider,
	))
	assert.Equal(t, mutationCalls, fixture.engine.plannerExecutionCount())
}

func TestRenderServiceAmbientServiceInputsInvalidateCommittedComponents(t *testing.T) {
	t.Run("introspected engine", func(t *testing.T) {
		testAmbientServiceInputInvalidation(t, func(engine templating.Engine) templating.Engine {
			return engine
		})
	})
	t.Run("opaque engine fails closed", func(t *testing.T) {
		testAmbientServiceInputInvalidation(t, func(engine templating.Engine) templating.Engine {
			return newDynamicBindingCountingEngine(t, engine)
		})
	})
}

func testAmbientServiceInputInvalidation(
	t *testing.T,
	wrapEngine func(templating.Engine) templating.Engine,
) {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"alpha": {
				APIVersion: "example.test/v1",
				Resources:  "alphas",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"dynamic": {
				Name: "dynamic",
				Incremental: &config.IncrementalTemplate{BindingsTemplate: `{%%
var crtList = "off"
if capabilities["supports_crt_list"] == true {
  crtList = "on"
}
%%}{{ toJSON(map[string]any{"alpha": map[string]any{
  "file": currentFiles["state"],
  "cap": crtList,
  "config": currentConfig.ServerIndex["be_app"]["srv1"].Address,
}}) }}`},
				Template: `{{ source }}/{{ item | dig_string("", "metadata", "name") }}=file:{{ props | dig_string("", "file") }} cap:{{ props | dig_string("", "cap") }} config:{{ props | dig_string("", "config") }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "dynamic" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := wrapEngine(baseEngine)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	service.SetAckedPlan(planWithServer("plan-1", "10.0.0.1"))
	alpha := k8sstore.NewMemoryStore(2)
	require.NoError(t, alpha.Add(
		dynamicBindingResource("item", "value"), []string{"default", "item"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"alpha": alpha})
	tempComponent6 := service.incremental.components["dynamic"]
	query := componentQueryKey(&tempComponent6, "alpha", "default", "item")
	render := func(files map[string]string) string {
		t.Helper()
		result, err := service.Render(
			t.Context(), provider, rendercontext.RenderModeReconcile,
			rendercontext.WithCurrentAuxFiles(files),
		)
		require.NoError(t, err)
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		waitForIncrementalCache(t, service)
		return result.HAProxyConfig
	}
	executions := func() uint64 { return service.incremental.graph.Counters(query).Executions }

	assert.Equal(t, "alpha/item=file:one cap:off config:10.0.0.1\n", render(map[string]string{"state": "one"}))
	assert.Equal(t, uint64(1), executions())
	assert.Equal(t, "alpha/item=file:one cap:off config:10.0.0.1\n", render(map[string]string{"state": "one"}))
	assert.Equal(t, uint64(1), executions())

	assert.Equal(t, "alpha/item=file:two cap:off config:10.0.0.1\n", render(map[string]string{"state": "two"}))
	assert.Equal(t, uint64(2), executions())
	assert.Equal(t, "alpha/item=file:two cap:off config:10.0.0.1\n", render(map[string]string{"state": "two"}))
	assert.Equal(t, uint64(2), executions())

	service.SetCapabilities(dataplane.Capabilities{SupportsCrtList: true})
	assert.Equal(t, "alpha/item=file:two cap:on config:10.0.0.1\n", render(map[string]string{"state": "two"}))
	assert.Equal(t, uint64(3), executions())
	assert.Equal(t, "alpha/item=file:two cap:on config:10.0.0.1\n", render(map[string]string{"state": "two"}))
	assert.Equal(t, uint64(3), executions())

	service.SetAckedPlan(planWithServer("plan-1-equivalent", "10.0.0.1"))
	assert.Equal(t, "alpha/item=file:two cap:on config:10.0.0.1\n", render(map[string]string{"state": "two"}))
	assert.Equal(t, uint64(3), executions(), "an equivalent ACKed plan must not rerun components")

	service.SetAckedPlan(planWithServer("plan-2", "10.0.0.2"))
	assert.Equal(t, "alpha/item=file:two cap:on config:10.0.0.2\n", render(map[string]string{"state": "two"}))
	assert.Equal(t, uint64(4), executions())
}

func BenchmarkIncrementalBindingPlanCacheHit(b *testing.B) {
	state, engine := newBindingAmbientState(b)
	baseContext := bindingAmbientContext()
	_, cache, exact, err := state.prepareBindingPlan(context.Background(), baseContext)
	require.NoError(b, err)
	require.True(b, exact)
	state.snapshot.bindingCache = cache
	var failures atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			plan, candidate, authenticated, err := state.prepareBindingPlan(context.Background(), baseContext)
			if err != nil || !authenticated || plan == nil || candidate != cache {
				failures.Add(1)
			}
		}
	})
	b.StopTimer()
	if failed := failures.Load(); failed != 0 {
		b.Fatalf("cache hit failures = %d", failed)
	}
	if calls := engine.plannerExecutionCount(); calls != 1 {
		b.Fatalf("planner executions = %d, want 1", calls)
	}
}

func newBindingAmbientState(tb testing.TB) (*incrementalRenderState, *dynamicBindingCountingEngine) {
	tb.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"alpha": {
				APIVersion: "example.test/v1",
				Resources:  "alphas",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"dynamic": {
				Name: "dynamic",
				Incremental: &config.IncrementalTemplate{BindingsTemplate: `{{ toJSON(map[string]any{
  "alpha": map[string]any{
    "extra": extraContext["value"],
    "capability": capabilities["supports_crt_list"],
    "config": currentConfig.ServerIndex["backend"]["server"].Address,
    "file": currentFiles["state"],
    "path": pathResolver.GetBaseDir(),
    "runtime": runtimeEnvironment.GOMAXPROCS,
    "snippets": templateSnippets,
  },
}) }}`},
				Template: `{{ source }}={{ props | toJSON() }}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "dynamic" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(tb, err)
	engine := newDynamicBindingCountingEngine(tb, baseEngine)
	state := newIncrementalRenderState(cfg, engine)
	require.NotNil(tb, state)
	require.NoError(tb, state.configErr)
	return state, engine
}

func bindingAmbientContext() map[string]any {
	files := map[string]string{"state": "base"}
	return map[string]any{
		"extraContext": map[string]any{"value": "base"},
		"capabilities": map[string]any{"supports_crt_list": false},
		"currentConfig": &renderplan.CurrentConfig{ServerIndex: map[string]map[string]renderplan.ServerAddr{
			"backend": {"server": {Address: "base"}},
		}},
		"currentFiles":       &files,
		"pathResolver":       &templating.PathResolver{BaseDir: "/base"},
		"runtimeEnvironment": &templating.RuntimeEnvironment{GOMAXPROCS: 4},
		"templateSnippets":   []string{"dynamic"},
	}
}
