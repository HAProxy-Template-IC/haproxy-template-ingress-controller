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
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestFreshColdStartRejectsZeroGenerationSnapshotState(t *testing.T) {
	service, _, _ := newColdVectorService(t)
	snapshot := service.incremental.snapshot
	poisoned, _, _ := snapshot.bindings.Insert([]byte("existing"), `{}`)
	snapshot.bindings = poisoned
	session := &incrementalRenderSession{state: service.incremental, base: snapshot}

	cold, err := session.selectFreshColdStart()
	require.ErrorContains(t, err, "zero-generation snapshot is not empty")
	assert.False(t, cold)
}

func TestFreshColdStartDoesNotReplaceExistingGeneration(t *testing.T) {
	service, _, _ := newColdVectorService(t)
	key := incremental.NewQueryKey("existing")
	graph, err := incremental.New(incremental.Definition{
		Key: key,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return []byte("value"), nil
		},
	})
	require.NoError(t, err)
	graphSession, err := graph.Begin()
	require.NoError(t, err)
	_, err = graphSession.Evaluate(t.Context(), key)
	require.NoError(t, err)
	require.NoError(t, graphSession.Commit(t.Context(), func(
		context.Context,
		[]incremental.InputRevision,
	) (bool, error) {
		return true, nil
	}))
	service.incremental.graph = graph
	session := &incrementalRenderSession{state: service.incremental, base: service.incremental.snapshot}

	cold, err := session.selectFreshColdStart()
	require.NoError(t, err)
	assert.False(t, cold)
}

func TestColdComponentVectorExecutesEachEntryPointOnce(t *testing.T) {
	service, engine, provider := newColdVectorService(t)

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "first/a\nfirst/b\nsecond/a\nsecond/b\n", result.HAProxyConfig)
	assert.Equal(t, []int{2}, engine.vectorCounts(helpers.IncrementalEntryPointName("100-first")))
	assert.Equal(t, []int{2}, engine.vectorCounts(helpers.IncrementalEntryPointName("200-second")))
	assert.Equal(t, 1, engine.bindCount(helpers.IncrementalEntryPointName("100-first")))
	assert.Equal(t, 1, engine.bindCount(helpers.IncrementalEntryPointName("200-second")))
	assert.Empty(t, engine.batchEntryPoints())
}

func TestColdComponentVectorPreflightsEveryEntryPointBeforeFallback(t *testing.T) {
	service, engine, provider := newColdVectorService(t)
	engine.setIneligible(helpers.IncrementalEntryPointName("100-first"))

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "first/a\nfirst/b\nsecond/a\nsecond/b\n", result.HAProxyConfig)
	assert.ElementsMatch(t, []string{
		helpers.IncrementalEntryPointName("100-first"),
		helpers.IncrementalEntryPointName("200-second"),
	}, engine.eligibilityEntryPoints())
	assert.Empty(t, engine.vectorEntryPoints())
	assert.ElementsMatch(t, []string{
		helpers.IncrementalEntryPointName("100-first"),
		helpers.IncrementalEntryPointName("200-second"),
	}, engine.batchEntryPoints())
}

func TestColdComponentVectorFinalizationFailureCannotPoisonRetry(t *testing.T) {
	service, engine, provider := newColdVectorService(t)
	second := helpers.IncrementalEntryPointName("200-second")
	engine.setPoison(second)

	_, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "injected vector result poison")
	assert.Zero(t, service.incremental.graph.Generation())
	assert.Equal(t, []int{2}, engine.vectorCounts(helpers.IncrementalEntryPointName("100-first")))
	assert.Equal(t, []int{2}, engine.vectorCounts(second))

	engine.setPoison("")
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "first/a\nfirst/b\nsecond/a\nsecond/b\n", result.HAProxyConfig)
	assert.Equal(t, []int{2, 2}, engine.vectorCounts(helpers.IncrementalEntryPointName("100-first")))
	assert.Equal(t, []int{2, 2}, engine.vectorCounts(second))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, uint64(1), service.incremental.graph.Generation())
}

func TestShardIncrementalColdVectorGroupsPreservesEveryItem(t *testing.T) {
	prepared := make([]*preparedIncrementalComponent, 3000)
	indexes := make([]int, len(prepared))
	for index := range indexes {
		indexes[index] = index
	}
	groups := shardIncrementalColdVectorGroupsWithLimit([]incrementalColdVectorGroup{{
		entryPoint: "routes",
		indexes:    indexes,
		prepared:   prepared,
	}}, 16)

	require.Len(t, groups, 16)
	var actual []int
	for index := range groups {
		assert.Equal(t, "routes", groups[index].entryPoint)
		assert.LessOrEqual(t, len(groups[index].prepared), 188)
		assert.Len(t, groups[index].prepared, len(groups[index].indexes))
		actual = append(actual, groups[index].indexes...)
	}
	assert.Equal(t, indexes, actual)
}

type coldVectorProbeEngine struct {
	templating.Engine
	component templating.IncrementalComponentExecutor
	batch     templating.IncrementalComponentBatchExecutor
	planner   templating.IncrementalBindingPlannerExecutor
	binder    templating.IncrementalResourceBinder
	vector    templating.IncrementalComponentVectorRenderer

	mu               sync.Mutex
	eligibilityCalls []string
	vectorCalls      map[string][]int
	batchCalls       []string
	bindCalls        map[string]int
	ineligible       map[string]struct{}
	poison           string
}

func newColdVectorProbeEngine(tb testing.TB, engine templating.Engine) *coldVectorProbeEngine {
	tb.Helper()
	component, ok := engine.(templating.IncrementalComponentExecutor)
	require.True(tb, ok)
	batch, ok := engine.(templating.IncrementalComponentBatchExecutor)
	require.True(tb, ok)
	planner, ok := engine.(templating.IncrementalBindingPlannerExecutor)
	require.True(tb, ok)
	binder, ok := engine.(templating.IncrementalResourceBinder)
	require.True(tb, ok)
	vector, ok := engine.(templating.IncrementalComponentVectorRenderer)
	require.True(tb, ok)
	return &coldVectorProbeEngine{
		Engine: engine, component: component, batch: batch, planner: planner, binder: binder, vector: vector,
		vectorCalls: map[string][]int{}, bindCalls: map[string]int{}, ineligible: map[string]struct{}{},
	}
}

func (e *coldVectorProbeEngine) RenderIncrementalComponent(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	return e.component.RenderIncrementalComponent(ctx, templateName, templateContext)
}

func (e *coldVectorProbeEngine) RenderIncrementalComponents(
	ctx context.Context,
	templateName string,
	items []templating.IncrementalComponentBatchItem,
) ([]string, error) {
	e.mu.Lock()
	e.batchCalls = append(e.batchCalls, templateName)
	e.mu.Unlock()
	return e.batch.RenderIncrementalComponents(ctx, templateName, items)
}

func (e *coldVectorProbeEngine) RenderIncrementalBindings(
	ctx context.Context,
	templateName string,
	extraContext map[string]any,
) ([]byte, error) {
	return e.planner.RenderIncrementalBindings(ctx, templateName, extraContext)
}

func (e *coldVectorProbeEngine) BindIncrementalResources(
	templateName string,
	resources any,
	lease templating.IncrementalResourceInvocationLease,
) (any, error) {
	e.mu.Lock()
	e.bindCalls[templateName]++
	e.mu.Unlock()
	return e.binder.BindIncrementalResources(templateName, resources, lease)
}

func (e *coldVectorProbeEngine) IncrementalComponentVectorEligibility(
	templateName string,
) (templating.IncrementalComponentVectorEligibility, bool) {
	e.mu.Lock()
	e.eligibilityCalls = append(e.eligibilityCalls, templateName)
	_, ineligible := e.ineligible[templateName]
	e.mu.Unlock()
	if ineligible {
		return templating.IncrementalComponentVectorEligibility{}, false
	}
	return e.vector.IncrementalComponentVectorEligibility(templateName)
}

func (e *coldVectorProbeEngine) RenderIncrementalComponentVector(
	ctx context.Context,
	templateName string,
	input templating.IncrementalComponentVectorInput,
) error {
	e.mu.Lock()
	e.vectorCalls[templateName] = append(e.vectorCalls[templateName], input.Count)
	poison := e.poison == templateName
	e.mu.Unlock()
	expectedBindings := incrementalColdVectorBindings()
	if len(input.SharedContext) != 0 || !slices.Equal(sortedMapKeys(input.Bindings), expectedBindings[:]) {
		return errors.New("invalid cold vector partition")
	}
	if err := e.vector.RenderIncrementalComponentVector(ctx, templateName, input); err != nil {
		return err
	}
	if poison {
		execution, ok := input.Lifecycle.(*incrementalVectorExecution)
		if !ok || len(execution.items) == 0 {
			return errors.New("cold vector execution is unavailable")
		}
		execution.items[len(execution.items)-1].recorder.err = errors.New("injected vector result poison")
	}
	return nil
}

func (e *coldVectorProbeEngine) setIneligible(entryPoint string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ineligible[entryPoint] = struct{}{}
}

func (e *coldVectorProbeEngine) setPoison(entryPoint string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.poison = entryPoint
}

func (e *coldVectorProbeEngine) vectorCounts(entryPoint string) []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.vectorCalls[entryPoint])
}

func (e *coldVectorProbeEngine) eligibilityEntryPoints() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.eligibilityCalls)
}

func (e *coldVectorProbeEngine) vectorEntryPoints() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]string, 0, len(e.vectorCalls))
	for entryPoint := range e.vectorCalls {
		result = append(result, entryPoint)
	}
	slices.Sort(result)
	return result
}

func (e *coldVectorProbeEngine) batchEntryPoints() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.batchCalls)
}

func (e *coldVectorProbeEngine) bindCount(entryPoint string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bindCalls[entryPoint]
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func newColdVectorService(
	tb testing.TB,
) (*RenderService, *coldVectorProbeEngine, stores.StoreProvider) {
	tb.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-first": {
				Name: "100-first", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes", Group: "routes"},
				Template: `first/{{ item | dig_string("", "metadata", "name") }}
`,
			},
			"200-second": {
				Name: "200-second", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes", Group: "routes"},
				Template: `second/{{ item | dig_string("", "metadata", "name") }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "100-first" }}{{ render "200-second" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg,
		nil,
		nil,
		declarations,
		helpers.EngineOptions{},
	)
	require.NoError(tb, err)
	engine := newColdVectorProbeEngine(tb, baseEngine)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	store := k8sstore.NewMemoryStore(2)
	for _, name := range []string{"a", "b"} {
		require.NoError(tb, store.Add(
			incrementalTestResource("default", name, nil),
			[]string{"default", name},
		))
	}
	return service, engine, stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
}
