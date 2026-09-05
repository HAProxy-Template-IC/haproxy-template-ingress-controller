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
	"fmt"
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
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type coldCarrierWaveProbeEngine struct {
	*coldVectorProbeEngine
	carrier templating.IncrementalComponentVectorCarrierWavesRenderer

	mu         sync.Mutex
	poisonCall int
	calls      [][]string
	waveRuns   int
}

// coldCarrierWaveProbeLifecycle injects the finalization poison after a wave has
// drained but before it is sealed, which is where the wave's results are built.
type coldCarrierWaveProbeLifecycle struct {
	waves  *incrementalColdCarrierWavesLifecycle
	engine *coldCarrierWaveProbeEngine

	mu       sync.Mutex
	poisoned map[int]bool
}

func newColdCarrierWaveProbeEngine(tb testing.TB, base templating.Engine) *coldCarrierWaveProbeEngine {
	tb.Helper()
	carrier, ok := base.(templating.IncrementalComponentVectorCarrierWavesRenderer)
	require.True(tb, ok)
	_, available := carrier.IncrementalComponentVectorCarrierEligibility()
	require.True(tb, available)
	return &coldCarrierWaveProbeEngine{
		coldVectorProbeEngine: newColdVectorProbeEngine(tb, base),
		carrier:               carrier,
		poisonCall:            -1,
	}
}

func (e *coldCarrierWaveProbeEngine) IncrementalComponentVectorCarrierEligibility() (
	templating.IncrementalComponentVectorCarrierEligibility,
	bool,
) {
	return e.carrier.IncrementalComponentVectorCarrierEligibility()
}

func (e *coldCarrierWaveProbeEngine) RenderIncrementalComponentVectorCarrierWaves(
	ctx context.Context,
	input templating.IncrementalComponentVectorCarrierWavesInput,
) error {
	e.mu.Lock()
	e.waveRuns++
	e.mu.Unlock()
	waves, ok := input.Lifecycle.(*incrementalColdCarrierWavesLifecycle)
	if !ok {
		return assert.AnError
	}
	input.Lifecycle = &coldCarrierWaveProbeLifecycle{
		waves:    waves,
		engine:   e,
		poisoned: map[int]bool{},
	}
	return e.carrier.RenderIncrementalComponentVectorCarrierWaves(ctx, input)
}

func (l *coldCarrierWaveProbeLifecycle) LoadWave(
	ctx context.Context,
	wave int,
) ([]templating.IncrementalComponentVectorCarrierLane, error) {
	lanes, err := l.waves.LoadWave(ctx, wave)
	if err != nil || len(lanes) == 0 {
		return lanes, err
	}
	names := make([]string, len(lanes))
	for index := range lanes {
		names[index] = lanes[index].TemplateName
	}
	slices.Sort(names)
	l.engine.mu.Lock()
	callIndex := len(l.engine.calls)
	l.engine.calls = append(l.engine.calls, names)
	poison := callIndex == l.engine.poisonCall
	l.engine.mu.Unlock()
	if poison {
		l.mu.Lock()
		l.poisoned[wave] = true
		l.mu.Unlock()
	}
	return lanes, nil
}

func (l *coldCarrierWaveProbeLifecycle) SealWave(wave int) error {
	l.mu.Lock()
	poison := l.poisoned[wave]
	l.mu.Unlock()
	if poison {
		l.waves.mu.Lock()
		inner := l.waves.inner
		l.waves.mu.Unlock()
		if err := poisonColdCarrierFinalization(inner); err != nil {
			return err
		}
	}
	return l.waves.SealWave(wave)
}

func (l *coldCarrierWaveProbeLifecycle) Begin(index int) error {
	return l.waves.Begin(index)
}

func (l *coldCarrierWaveProbeLifecycle) End(index int, output string) error {
	return l.waves.End(index, output)
}

func (l *coldCarrierWaveProbeLifecycle) Abort(activeIndex int, cause error) {
	l.waves.Abort(activeIndex, cause)
}

func (e *coldCarrierWaveProbeEngine) reset(poisonCall int) {
	e.mu.Lock()
	e.poisonCall = poisonCall
	e.calls = nil
	e.waveRuns = 0
	e.mu.Unlock()
}

func (e *coldCarrierWaveProbeEngine) renderedCalls() [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([][]string, len(e.calls))
	for index := range e.calls {
		result[index] = slices.Clone(e.calls[index])
	}
	return result
}

func (e *coldCarrierWaveProbeEngine) renderedWaveRuns() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.waveRuns
}

func TestColdCarrierGraphLaterWaveFailurePublishesNothingAndRetryReexecutesEveryWave(t *testing.T) {
	service, engine, provider, _ := newColdCarrierWaveService(t)
	initialSnapshot := service.incremental.snapshot
	producer := helpers.IncrementalEntryPointName("100-policies")
	consumer := helpers.IncrementalEntryPointName("200-routes")

	engine.reset(1)
	failed, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, coldCarrierFinalizationPoison)
	assert.Nil(t, failed)
	assert.Equal(t, [][]string{{producer}, {consumer}}, engine.renderedCalls())
	assert.Equal(t, 1, engine.renderedWaveRuns())
	assertColdCarrierGraphUnpublished(t, service, initialSnapshot)

	engine.reset(-1)
	retried, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, retried)
	assert.Equal(t, "route=value\n", retried.HAProxyConfig)
	assert.Equal(t, [][]string{{producer}, {consumer}}, engine.renderedCalls())
	assert.Equal(t, 1, engine.renderedWaveRuns())
	assertColdCarrierGraphUnpublished(t, service, initialSnapshot)

	require.NoError(t, retried.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, uint64(1), service.incremental.graph.Generation())
	assert.NotSame(t, initialSnapshot, service.incremental.snapshot)
	assert.Equal(t, 2, service.incremental.snapshot.results.Len())
	assert.True(t, service.incremental.snapshot.groupReady["policies"])
	assert.True(t, service.incremental.snapshot.groupReady["routes"])
}

func TestColdCarrierGraphWaveRootsInvalidateWarmConsumersExactly(t *testing.T) {
	service, engine, provider, policies := newColdCarrierWaveService(t)
	engine.reset(-1)
	cold, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, cold)
	assert.Equal(t, "route=value\n", cold.HAProxyConfig)
	require.NoError(t, cold.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, 1, engine.renderedWaveRuns())

	engine.reset(-1)
	warm, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, warm)
	assert.Equal(t, cold.HAProxyConfig, warm.HAProxyConfig)
	require.NoError(t, warm.InputTransaction.Commit(t.Context()))
	assert.Zero(t, engine.renderedWaveRuns())

	require.NoError(t, policies.Update(
		incrementalSelectorResource("policy", map[string]any{
			"target": "service", "rank": "1", "value": "changed",
		}),
		[]string{"default", "policy"},
	))
	changed, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, changed)
	assert.Equal(t, "route=changed\n", changed.HAProxyConfig)
	require.NoError(t, changed.InputTransaction.Commit(t.Context()))
	assert.Zero(t, engine.renderedWaveRuns())
}

func TestColdCarrierGraphRunsOnePersistentRendererPerWorkerAcrossEveryWave(t *testing.T) {
	const itemCount = 200
	cfg := incrementalSelectorServiceConfig(false)
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	base, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newColdCarrierWaveProbeEngine(t, base)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	policies := k8sstore.NewMemoryStore(2)
	routes := k8sstore.NewMemoryStore(2)
	for itemIndex := range itemCount {
		policy := fmt.Sprintf("policy-%03d", itemIndex)
		route := fmt.Sprintf("route-%03d", itemIndex)
		target := fmt.Sprintf("service-%03d", itemIndex)
		require.NoError(t, policies.Add(
			incrementalSelectorResource(policy, map[string]any{
				"target": target, "rank": "1", "value": fmt.Sprintf("value-%03d", itemIndex),
			}),
			[]string{"default", policy},
		))
		require.NoError(t, routes.Add(
			incrementalSelectorResource(route, map[string]any{"target": target}),
			[]string{"default", route},
		))
	}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"policies": policies, "routes": routes})
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Contains(t, result.HAProxyConfig, "route-000=value-000\n")
	assert.Contains(t, result.HAProxyConfig, "route-199=value-199\n")
	assert.Equal(t, 2, engine.renderedWaveRuns())
	calls := engine.renderedCalls()
	require.Len(t, calls, 4)
	producer := helpers.IncrementalEntryPointName("100-policies")
	consumer := helpers.IncrementalEntryPointName("200-routes")
	assert.Equal(t, [][]string{{producer}, {producer}, {consumer}, {consumer}}, calls)
}

func TestColdCarrierLaneFinalizationContainsPanic(t *testing.T) {
	session := &incrementalRenderSession{}
	component := &incrementalComponent{name: "component"}
	execution := &incrementalVectorExecution{
		session:   session,
		component: component,
		ctx:       t.Context(),
		items: []incrementalVectorItemState{{
			completed:  true,
			outputSet:  true,
			beginCount: 1,
		}},
	}
	execution.active.Store(-1)
	execution.seal = execution
	_, err := finalizeIncrementalColdCarrierLane(session, &preparedIncrementalVectorRender{execution: execution})
	require.ErrorContains(t, err, "incremental cold carrier lane finalization panic")
}

func newColdCarrierWaveService(
	tb testing.TB,
) (service *RenderService, engine *coldCarrierWaveProbeEngine, provider stores.StoreProvider, policies *k8sstore.MemoryStore) {
	tb.Helper()
	cfg := incrementalSelectorServiceConfig(false)
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	base, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(tb, err)
	engine = newColdCarrierWaveProbeEngine(tb, base)
	service = NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	policies = k8sstore.NewMemoryStore(2)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(tb, policies.Add(
		incrementalSelectorResource("policy", map[string]any{
			"target": "service", "rank": "1", "value": "value",
		}),
		[]string{"default", "policy"},
	))
	require.NoError(tb, routes.Add(
		incrementalSelectorResource("route", map[string]any{"target": "service"}),
		[]string{"default", "route"},
	))
	provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"policies": policies,
		"routes":   routes,
	})
	return service, engine, provider, policies
}

func assertColdCarrierGraphUnpublished(
	t *testing.T,
	service *RenderService,
	initialSnapshot *incrementalStateSnapshot,
) {
	t.Helper()
	assert.Zero(t, service.incremental.graph.Generation())
	assert.Same(t, initialSnapshot, service.incremental.snapshot)
	assert.Zero(t, initialSnapshot.results.Len())
	assert.Empty(t, initialSnapshot.groupReady)
	for _, index := range initialSnapshot.groupIndexes {
		empty, err := index.authenticatedStructurallyEmpty()
		require.NoError(t, err)
		assert.True(t, empty)
	}
}

func TestColdCarrierGraphScheduleRejectsAmbiguousOwnership(t *testing.T) {
	left := incremental.NewQueryKey("left")
	right := incremental.NewQueryKey("right")
	plan := &incrementalCarrierPlan{logicalQueries: 2}

	tests := []struct {
		name        string
		groupOrder  []string
		keysByGroup map[string][]incremental.QueryKey
		want        string
	}{
		{
			name:        "omitted",
			groupOrder:  []string{"left", "right"},
			keysByGroup: map[string][]incremental.QueryKey{"left": {left}},
			want:        "omitted a query",
		},
		{
			name:       "multiple groups",
			groupOrder: []string{"left", "right"},
			keysByGroup: map[string][]incremental.QueryKey{
				"left": {left, right}, "right": {right},
			},
			want: "belongs to multiple groups",
		},
		{
			name:        "unknown",
			groupOrder:  []string{"left", "right"},
			keysByGroup: map[string][]incremental.QueryKey{"left": {left}, "right": {incremental.NewQueryKey("other")}},
			want:        "unknown query",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newIncrementalColdCarrierGraphSchedule(
				plan,
				test.groupOrder,
				[]incremental.QueryKey{right, left},
				test.keysByGroup,
			)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestColdCarrierGraphStageSelectionRejectsDuplicatePlanMember(t *testing.T) {
	component := incrementalCarrierTestComponent(
		"component", "group", "routes", nil, nil, false, false, false, false,
	)
	key := componentQueryKey(&component, "routes", "default", "route")
	lane := incrementalCarrierLane{batchIndex: 0, queryKey: key, component: &component}
	plan := &incrementalCarrierPlan{stages: []incrementalCarrierStage{{
		carriers: []incrementalCarrier{
			{source: "routes", namespace: "default", name: "route", lanes: []incrementalCarrierLane{lane}},
			{source: "routes", namespace: "default", name: "route", lanes: []incrementalCarrierLane{lane}},
		},
	}}}

	_, _, err := selectIncrementalColdCarrierGraphStage(plan, []string{"group"})
	require.ErrorContains(t, err, "repeats batch item 0")
}

func TestColdCarrierGraphStageOrderCanonicalizesInterleavedGroups(t *testing.T) {
	keys := []incremental.QueryKey{
		incremental.NewQueryKey("a"),
		incremental.NewQueryKey("b"),
		incremental.NewQueryKey("c"),
		incremental.NewQueryKey("d"),
	}
	schedule := &incrementalColdCarrierGraphSchedule{
		keys: keys,
		keysByGroup: map[string][]incremental.QueryKey{
			"left":  {keys[0], keys[2]},
			"right": {keys[1], keys[3]},
		},
		queryIndexes: map[incremental.QueryKey]int{
			keys[0]: 0,
			keys[1]: 1,
			keys[2]: 2,
			keys[3]: 3,
		},
	}
	schedule.seal = schedule
	canonical := &incrementalColdCarrierStageResult{
		indexes: []int{0, 1, 2, 3},
		results: []incremental.ExactResult{
			{Key: keys[0]}, {Key: keys[1]}, {Key: keys[2]}, {Key: keys[3]},
		},
	}
	require.NoError(t, validateIncrementalColdCarrierGraphStageOrder(schedule, canonical))

	interleavedByGroup := &incrementalColdCarrierStageResult{
		indexes: []int{0, 2, 1, 3},
		results: []incremental.ExactResult{
			{Key: keys[0]}, {Key: keys[2]}, {Key: keys[1]}, {Key: keys[3]},
		},
	}
	require.ErrorContains(t,
		validateIncrementalColdCarrierGraphStageOrder(schedule, interleavedByGroup),
		"not in global batch order",
	)
}
