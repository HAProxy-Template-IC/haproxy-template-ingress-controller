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
	"context"
	"log/slog"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestRenderServiceDynamicBindingsTrackAliasesAndPropsExactly(t *testing.T) {
	fixture := newDynamicBindingFixture(t, map[string]any{
		"alpha": map[string]any{"label": "A"},
	})
	fixture.add(t, "alpha", "same", "alpha-value")
	fixture.add(t, "beta", "same", "beta-value")

	assert.Equal(t, "alpha/A/same=alpha-value@reconcile\n", fixture.renderAndCommitCacheReady(t, fixture.provider))
	alphaQuery := fixture.query("alpha", "same")
	betaQuery := fixture.query("beta", "same")
	require.NotEqual(t, alphaQuery, betaQuery)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(alphaQuery).Executions)
	assert.Zero(t, fixture.service.incremental.graph.Counters(betaQuery).Executions)
	assert.Equal(t, map[string]int{"alpha/same": 1}, fixture.engine.executionCounts())

	fixture.config.TemplatingSettings.ExtraContext["unused"] = "changed"
	assert.Equal(t, "alpha/A/same=alpha-value@reconcile\n", fixture.renderAndCommitCacheReady(t, fixture.provider))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(alphaQuery).Executions)
	assert.Equal(t, map[string]int{"alpha/same": 1}, fixture.engine.executionCounts())

	fixture.setBindings(map[string]any{
		"alpha": map[string]any{"label": "A"},
		"beta":  map[string]any{"label": "B"},
	})
	assert.Equal(t,
		"alpha/A/same=alpha-value@reconcile\nbeta/B/same=beta-value@reconcile\n",
		fixture.renderAndCommitCacheReady(t, fixture.provider),
	)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(alphaQuery).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(betaQuery).Executions)
	assert.Equal(t, map[string]int{"alpha/same": 1, "beta/same": 1}, fixture.engine.executionCounts())

	fixture.setBindings(map[string]any{
		"alpha": map[string]any{"label": "changed"},
		"beta":  map[string]any{"label": "B"},
	})
	assert.Equal(t,
		"alpha/changed/same=alpha-value@reconcile\nbeta/B/same=beta-value@reconcile\n",
		fixture.renderAndCommitCacheReady(t, fixture.provider),
	)
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(alphaQuery).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(betaQuery).Executions)
	assert.Equal(t, map[string]int{"alpha/same": 2, "beta/same": 1}, fixture.engine.executionCounts())

	fixture.setBindings(map[string]any{
		"beta": map[string]any{"label": "B"},
	})
	assert.Equal(t,
		"beta/B/same=beta-value@reconcile\n",
		fixture.renderAndCommitCacheReady(t, fixture.provider),
	)
	_, alphaCached := fixture.service.incremental.graph.Value(alphaQuery)
	assert.False(t, alphaCached)
	assert.Zero(t, fixture.service.incremental.graph.Counters(alphaQuery))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(betaQuery).Executions)
	assert.Equal(t, map[string]int{"alpha/same": 2, "beta/same": 1}, fixture.engine.executionCounts())
}

func TestRenderServiceDynamicBindingsRejectInvalidPlannerResults(t *testing.T) {
	tests := map[string]struct {
		bindings any
		want     string
	}{
		"unknown alias": {
			bindings: map[string]any{"unknown": map[string]any{}},
			want:     `alias "unknown" is not a watched resource`,
		},
		"non-object output": {
			bindings: []any{"alpha"},
			want:     "output must be a JSON object",
		},
		"non-object props": {
			bindings: map[string]any{"alpha": []any{}},
			want:     `alias "alpha" props must be a JSON object`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newDynamicBindingFixture(t, test.bindings)
			fixture.add(t, "alpha", "item", "value")

			_, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
			require.ErrorContains(t, err, test.want)
			assert.Empty(t, fixture.engine.executionCounts())
		})
	}
}

func TestRenderServiceAdmissionInvalidatesOnlyExactDynamicSubject(t *testing.T) {
	fixture := newDynamicBindingFixture(t, map[string]any{
		"alpha": map[string]any{"label": "A"},
	})
	fixture.add(t, "alpha", "a", "base-a")
	fixture.add(t, "alpha", "b", "base-b")
	fixture.add(t, "beta", "a", "unrelated")

	baseOutput := "alpha/A/a=base-a@reconcile\nalpha/A/b=base-b@reconcile\n"
	assert.Equal(t, baseOutput, fixture.renderAndCommitCacheReady(t, fixture.provider))
	aQuery := fixture.query("alpha", "a")
	bQuery := fixture.query("alpha", "b")
	committedA := fixture.service.incremental.graph.Counters(aQuery)
	committedB := fixture.service.incremental.graph.Counters(bQuery)
	assert.Equal(t, map[string]int{"alpha/a": 1, "alpha/b": 1}, fixture.engine.executionCounts())

	admitted := dynamicBindingResource("a", "proposed")
	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"alpha": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: admitted}),
		}),
	)
	assert.Equal(t,
		"alpha/A/a=proposed@admission\nalpha/A/b=base-b@reconcile\n",
		fixture.renderAndCommit(
			t,
			admissionProvider,
			rendercontext.RenderModeAdmission,
			rendercontext.WithAdmissionSubject("alpha", "default", "a"),
		),
	)
	assert.Equal(t, map[string]int{"alpha/a": 2, "alpha/b": 1}, fixture.engine.executionCounts())
	assert.Equal(t, committedA, fixture.service.incremental.graph.Counters(aQuery))
	assert.Equal(t, committedB, fixture.service.incremental.graph.Counters(bQuery))

	unrelated := dynamicBindingResource("a", "proposed-unrelated")
	unrelatedProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"beta": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: unrelated}),
		}),
	)
	assert.Equal(t,
		baseOutput,
		fixture.renderAndCommit(
			t,
			unrelatedProvider,
			rendercontext.RenderModeAdmission,
			rendercontext.WithAdmissionSubject("beta", "default", "a"),
		),
	)
	assert.Equal(t, map[string]int{"alpha/a": 2, "alpha/b": 1}, fixture.engine.executionCounts())
	assert.Equal(t, committedA, fixture.service.incremental.graph.Counters(aQuery))
	assert.Equal(t, committedB, fixture.service.incremental.graph.Counters(bQuery))

	assert.Equal(t, baseOutput, fixture.renderAndCommitCacheReady(t, fixture.provider))
	assert.Equal(t, map[string]int{"alpha/a": 2, "alpha/b": 1}, fixture.engine.executionCounts())
	assert.Equal(t, committedA, fixture.service.incremental.graph.Counters(aQuery))
	assert.Equal(t, committedB, fixture.service.incremental.graph.Counters(bQuery))
}

type dynamicBindingFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	provider stores.StoreProvider
	stores   map[string]*k8sstore.MemoryStore
}

func newDynamicBindingFixture(t *testing.T, bindings any) *dynamicBindingFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]any{"bindings": bindings},
		},
		WatchedResources: map[string]config.WatchedResource{
			"alpha": {
				APIVersion: "example.test/v1",
				Resources:  "alphas",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
			"beta": {
				APIVersion: "example.test/v1",
				Resources:  "betas",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"dynamic": {
				Name: "dynamic",
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: `{{ toJSON(extraContext["bindings"]) }}`,
				},
				Template: `{{ source }}/{{ props | dig_string("", "label") }}/{{ item | dig_string("", "metadata", "name") }}={{ item | dig_string("", "spec", "value") }}@{{ renderMode }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "dynamic" }}{%%
if tostring(extraContext["mutateRoot"]) == "true" {
  extraContext["bindings"] = extraContext["rootBindings"]
}
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
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine,
		Config: cfg,
		Logger: slog.Default(),
	})
	resourceStores := map[string]*k8sstore.MemoryStore{
		"alpha": k8sstore.NewMemoryStore(2),
		"beta":  k8sstore.NewMemoryStore(2),
	}
	providerStores := make(map[string]stores.Store, len(resourceStores))
	for name, store := range resourceStores {
		providerStores[name] = store
	}
	return &dynamicBindingFixture{
		config:   cfg,
		service:  service,
		engine:   engine,
		provider: stores.NewRealStoreProvider(providerStores),
		stores:   resourceStores,
	}
}

func (f *dynamicBindingFixture) setBindings(bindings any) {
	f.config.TemplatingSettings.ExtraContext["bindings"] = bindings
}

func (f *dynamicBindingFixture) add(t *testing.T, source, name, value string) {
	t.Helper()
	require.NoError(t, f.stores[source].Add(
		dynamicBindingResource(name, value),
		[]string{"default", name},
	))
}

func (f *dynamicBindingFixture) query(source, name string) incremental.QueryKey {
	tempComponent31 := f.service.incremental.components["dynamic"]
	return componentQueryKey(&tempComponent31, source, "default", name)
}

func (f *dynamicBindingFixture) renderAndCommit(
	t *testing.T,
	provider stores.StoreProvider,
	mode rendercontext.RenderMode,
	opts ...rendercontext.Option,
) string {
	t.Helper()
	result, err := f.service.Render(t.Context(), provider, mode, opts...)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	return result.HAProxyConfig
}

func (f *dynamicBindingFixture) renderAndCommitCacheReady(
	t *testing.T,
	provider stores.StoreProvider,
) string {
	t.Helper()
	result := f.renderAndCommit(t, provider, rendercontext.RenderModeReconcile)
	waitForIncrementalCache(t, f.service)
	return result
}

func dynamicBindingResource(name, value string) map[string]any {
	const namespace = "default"
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Example",
		"metadata": map[string]any{
			"namespace": namespace,
			"name":      name,
		},
		"spec": map[string]any{"value": value},
	}
}

type dynamicBindingCountingEngine struct {
	templating.Engine
	executor        templating.IncrementalComponentExecutor
	batchExecutor   templating.IncrementalComponentBatchExecutor
	planner         templating.IncrementalBindingPlannerExecutor
	snapshotPlanner templating.IncrementalBindingSnapshotPlanner

	mu                sync.Mutex
	executions        map[string]int
	plannerExecutions int
}

func newDynamicBindingCountingEngine(tb testing.TB, engine templating.Engine) *dynamicBindingCountingEngine {
	tb.Helper()
	executor, ok := engine.(templating.IncrementalComponentExecutor)
	require.True(tb, ok)
	batchExecutor, ok := engine.(templating.IncrementalComponentBatchExecutor)
	require.True(tb, ok)
	planner, ok := engine.(templating.IncrementalBindingPlannerExecutor)
	require.True(tb, ok)
	snapshotPlanner, ok := engine.(templating.IncrementalBindingSnapshotPlanner)
	require.True(tb, ok)
	return &dynamicBindingCountingEngine{
		Engine:          engine,
		executor:        executor,
		batchExecutor:   batchExecutor,
		planner:         planner,
		snapshotPlanner: snapshotPlanner,
		executions:      map[string]int{},
	}
}

func (e *dynamicBindingCountingEngine) RenderIncrementalBindings(
	ctx context.Context,
	templateName string,
	extraContext map[string]any,
) ([]byte, error) {
	e.mu.Lock()
	e.plannerExecutions++
	e.mu.Unlock()
	return e.planner.RenderIncrementalBindings(ctx, templateName, extraContext)
}

func (e *dynamicBindingCountingEngine) SnapshotIncrementalBindingInputs(
	entryPoints []string,
	templateContext map[string]any,
) (*templating.IncrementalBindingInputSnapshot, error) {
	return e.snapshotPlanner.SnapshotIncrementalBindingInputs(entryPoints, templateContext)
}

func (e *dynamicBindingCountingEngine) MatchIncrementalBindingInputs(
	entryPoints []string,
	templateContext map[string]any,
	snapshot *templating.IncrementalBindingInputSnapshot,
) bool {
	return e.snapshotPlanner.MatchIncrementalBindingInputs(entryPoints, templateContext, snapshot)
}

func (e *dynamicBindingCountingEngine) RenderIncrementalBindingsSnapshot(
	ctx context.Context,
	templateName string,
	snapshot *templating.IncrementalBindingInputSnapshot,
) ([]byte, error) {
	e.mu.Lock()
	e.plannerExecutions++
	e.mu.Unlock()
	return e.snapshotPlanner.RenderIncrementalBindingsSnapshot(ctx, templateName, snapshot)
}

func (e *dynamicBindingCountingEngine) RenderIncrementalComponent(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	e.recordExecution(templateContext)
	return e.executor.RenderIncrementalComponent(ctx, templateName, templateContext)
}

func (e *dynamicBindingCountingEngine) RenderIncrementalComponents(
	ctx context.Context,
	templateName string,
	items []templating.IncrementalComponentBatchItem,
) ([]string, error) {
	for index := range items {
		e.recordExecution(items[index].TemplateContext)
	}
	return e.batchExecutor.RenderIncrementalComponents(ctx, templateName, items)
}

func (e *dynamicBindingCountingEngine) recordExecution(templateContext map[string]any) {
	source, _ := templateContext["source"].(string)
	item, _ := templateContext["item"].(map[string]any)
	metadata, _ := item["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.executions[source+"/"+name]++
}

func (e *dynamicBindingCountingEngine) executionCounts() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make(map[string]int, len(e.executions))
	for key, count := range e.executions {
		result[key] = count
	}
	return result
}

func (e *dynamicBindingCountingEngine) plannerExecutionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.plannerExecutions
}
