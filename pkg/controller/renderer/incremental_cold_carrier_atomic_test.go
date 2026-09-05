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
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const coldCarrierFinalizationPoison = "injected carrier finalization poison"

type coldCarrierProbeEngine struct {
	*coldVectorProbeEngine
	carrier templating.IncrementalComponentVectorCarrierWavesRenderer

	mu      sync.Mutex
	attempt *coldCarrierProbeAttempt
}

// coldCarrierProbeLifecycle holds every worker at its wave seal until all of them
// have drained, so the poison lands on an otherwise complete multi-shard wave.
type coldCarrierProbeLifecycle struct {
	waves   *incrementalColdCarrierWavesLifecycle
	attempt *coldCarrierProbeAttempt
	call    *coldCarrierProbeCall
	ctx     context.Context
}

type coldCarrierProbeAttempt struct {
	expected int
	poison   bool
	ready    chan struct{}
	once     sync.Once

	mu       sync.Mutex
	calls    []*coldCarrierProbeCall
	rendered int
	failure  error
}

type coldCarrierProbeCall struct {
	index     int
	lanes     map[string]int
	lifecycle *incrementalColdCarrierLifecycle
	rendered  bool
	returned  bool
	poisoned  bool
}

func TestColdComponentCarrierMultiShardFinalizationFailureCannotPoisonRetry(t *testing.T) {
	const (
		itemCount    = incrementalColdVectorItemsPerShard * 2
		expectedRuns = 2
	)
	service, engine, provider := newColdCarrierService(t, itemCount)
	initialSnapshot := service.incremental.snapshot

	failedAttempt := engine.beginAttempt(expectedRuns, true)
	failed, err := renderColdCarrierService(t, service, provider)
	require.ErrorContains(t, err, coldCarrierFinalizationPoison)
	assert.Nil(t, failed)
	assertColdCarrierAttemptDrained(t, failedAttempt, itemCount, true)
	assertColdCarrierCommittedCacheEmpty(t, service, initialSnapshot)

	retryAttempt := engine.beginAttempt(expectedRuns, false)
	retried, err := renderColdCarrierService(t, service, provider)
	require.NoError(t, err)
	require.NotNil(t, retried)
	assert.Equal(t, coldCarrierExpectedOutput(itemCount), retried.HAProxyConfig)
	assertColdCarrierAttemptDrained(t, retryAttempt, itemCount, false)
	assertColdCarrierCommittedCacheEmpty(t, service, initialSnapshot)
	assert.Empty(t, engine.vectorEntryPoints())
	assert.Empty(t, engine.batchEntryPoints())

	require.NoError(t, retried.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, uint64(1), service.incremental.graph.Generation())
	assert.NotSame(t, initialSnapshot, service.incremental.snapshot)
	assert.Equal(t, itemCount*2, service.incremental.snapshot.results.Len())
	require.Contains(t, service.incremental.snapshot.groupIndexes, "routes")
	assert.Equal(t, itemCount*2, service.incremental.snapshot.groupIndexes["routes"].instances.Len())
}

func newColdCarrierProbeEngine(tb testing.TB, base templating.Engine) *coldCarrierProbeEngine {
	tb.Helper()
	carrier, ok := base.(templating.IncrementalComponentVectorCarrierWavesRenderer)
	require.True(tb, ok)
	_, available := carrier.IncrementalComponentVectorCarrierEligibility()
	require.True(tb, available)
	return &coldCarrierProbeEngine{
		coldVectorProbeEngine: newColdVectorProbeEngine(tb, base),
		carrier:               carrier,
	}
}

func (e *coldCarrierProbeEngine) IncrementalComponentVectorCarrierEligibility() (
	templating.IncrementalComponentVectorCarrierEligibility,
	bool,
) {
	return e.carrier.IncrementalComponentVectorCarrierEligibility()
}

func (e *coldCarrierProbeEngine) RenderIncrementalComponentVectorCarrierWaves(
	ctx context.Context,
	input templating.IncrementalComponentVectorCarrierWavesInput,
) error {
	e.mu.Lock()
	attempt := e.attempt
	e.mu.Unlock()
	if attempt == nil {
		return errors.New("cold carrier probe has no active attempt")
	}
	waves, ok := input.Lifecycle.(*incrementalColdCarrierWavesLifecycle)
	if !ok {
		return errors.New("cold carrier probe received an unexpected lifecycle")
	}
	call := attempt.register(input)
	defer attempt.markReturned(call)
	input.Lifecycle = &coldCarrierProbeLifecycle{
		waves: waves, attempt: attempt, call: call, ctx: ctx,
	}
	return e.carrier.RenderIncrementalComponentVectorCarrierWaves(ctx, input)
}

func (l *coldCarrierProbeLifecycle) LoadWave(
	ctx context.Context,
	wave int,
) ([]templating.IncrementalComponentVectorCarrierLane, error) {
	return l.waves.LoadWave(ctx, wave)
}

func (l *coldCarrierProbeLifecycle) SealWave(wave int) error {
	l.waves.mu.Lock()
	inner := l.waves.inner
	l.waves.mu.Unlock()
	if inner == nil {
		return l.waves.SealWave(wave)
	}
	err := inner.validateComplete()
	l.attempt.markRendered(l.call, inner, err)
	if err != nil {
		return l.waves.SealWave(wave)
	}
	select {
	case <-l.attempt.ready:
	case <-l.ctx.Done():
		return l.ctx.Err()
	}
	if l.attempt.renderFailure() == nil && l.attempt.poison && l.call.index == 0 {
		if poisonErr := poisonColdCarrierFinalization(inner); poisonErr != nil {
			return poisonErr
		}
		l.attempt.markPoisoned(l.call)
	}
	return l.waves.SealWave(wave)
}

func (l *coldCarrierProbeLifecycle) Begin(index int) error {
	return l.waves.Begin(index)
}

func (l *coldCarrierProbeLifecycle) End(index int, output string) error {
	return l.waves.End(index, output)
}

func (l *coldCarrierProbeLifecycle) Abort(activeIndex int, cause error) {
	l.waves.Abort(activeIndex, cause)
}

func (e *coldCarrierProbeEngine) beginAttempt(expected int, poison bool) *coldCarrierProbeAttempt {
	attempt := &coldCarrierProbeAttempt{
		expected: expected,
		poison:   poison,
		ready:    make(chan struct{}),
	}
	e.mu.Lock()
	e.attempt = attempt
	e.mu.Unlock()
	return attempt
}

func (a *coldCarrierProbeAttempt) register(
	input templating.IncrementalComponentVectorCarrierWavesInput,
) *coldCarrierProbeCall {
	lanes := map[string]int{}
	for _, wave := range input.Waves {
		for _, lane := range wave.Lanes {
			lanes[lane.TemplateName] += lane.Count
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	call := &coldCarrierProbeCall{index: len(a.calls), lanes: lanes}
	a.calls = append(a.calls, call)
	return call
}

func (a *coldCarrierProbeAttempt) markRendered(
	call *coldCarrierProbeCall,
	lifecycle *incrementalColdCarrierLifecycle,
	err error,
) {
	a.mu.Lock()
	call.lifecycle = lifecycle
	call.rendered = err == nil
	if err == nil {
		a.rendered++
	} else if a.failure == nil {
		a.failure = err
	}
	ready := a.failure != nil || a.rendered >= a.expected
	a.mu.Unlock()
	if ready {
		a.once.Do(func() { close(a.ready) })
	}
}

func (a *coldCarrierProbeAttempt) markReturned(call *coldCarrierProbeCall) {
	a.mu.Lock()
	call.returned = true
	a.mu.Unlock()
}

func (a *coldCarrierProbeAttempt) markPoisoned(call *coldCarrierProbeCall) {
	a.mu.Lock()
	call.poisoned = true
	a.mu.Unlock()
}

func (a *coldCarrierProbeAttempt) renderFailure() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failure
}

func poisonColdCarrierFinalization(lifecycle *incrementalColdCarrierLifecycle) error {
	if lifecycle == nil {
		return errors.New("cold carrier finalization poison has no lifecycle")
	}
	lifecycle.mu.Lock()
	if !lifecycle.validLocked() || lifecycle.aborted || lifecycle.terminal != nil || lifecycle.active >= 0 {
		lifecycle.mu.Unlock()
		return errors.New("cold carrier finalization poison found an undrained lifecycle")
	}
	segments := append([]incrementalColdCarrierSegment(nil), lifecycle.segments...)
	lifecycle.mu.Unlock()
	for segmentIndex := range segments {
		execution := segments[segmentIndex].execution
		if execution == nil {
			return errors.New("cold carrier finalization poison found an empty lane")
		}
		execution.mu.RLock()
		if execution.active.Load() >= 0 || execution.inflight.Load() != 0 || execution.aborted {
			execution.mu.RUnlock()
			return errors.New("cold carrier finalization poison found an undrained lane")
		}
		for itemIndex := range execution.items {
			item := &execution.items[itemIndex]
			if !item.completed || !item.outputSet || item.beginCount != 1 {
				execution.mu.RUnlock()
				return errors.New("cold carrier finalization poison found an incomplete lane")
			}
		}
		execution.mu.RUnlock()
	}
	target := segments[len(segments)-1].execution
	target.mu.Lock()
	target.items[len(target.items)-1].recorder.err = errors.New(coldCarrierFinalizationPoison)
	target.mu.Unlock()
	return nil
}

func assertColdCarrierAttemptDrained(
	t *testing.T,
	attempt *coldCarrierProbeAttempt,
	itemCount int,
	wantPoison bool,
) {
	t.Helper()
	attempt.mu.Lock()
	calls := append([]*coldCarrierProbeCall(nil), attempt.calls...)
	rendered := attempt.rendered
	failure := attempt.failure
	attempt.mu.Unlock()
	require.Len(t, calls, attempt.expected)
	assert.Equal(t, attempt.expected, rendered)
	assert.NoError(t, failure)
	totals := map[string]int{}
	poisoned := 0
	for _, call := range calls {
		assert.True(t, call.rendered, "carrier shard %d rendered", call.index)
		assert.True(t, call.returned, "carrier shard %d returned", call.index)
		if call.poisoned {
			poisoned++
		}
		require.Len(t, call.lanes, 2)
		assert.Equal(t, itemCount/attempt.expected,
			call.lanes[helpers.IncrementalEntryPointName("100-first")])
		assert.Equal(t, itemCount/attempt.expected,
			call.lanes[helpers.IncrementalEntryPointName("200-second")])
		for name, count := range call.lanes {
			totals[name] += count
		}
		require.NotNil(t, call.lifecycle)
		call.lifecycle.mu.Lock()
		aborted := call.lifecycle.aborted
		active := call.lifecycle.active
		segments := append([]incrementalColdCarrierSegment(nil), call.lifecycle.segments...)
		call.lifecycle.mu.Unlock()
		assert.Equal(t, -1, active, "carrier shard %d active item", call.index)
		if aborted {
			assert.Error(t, call.lifecycle.validateComplete(), "carrier shard %d abort", call.index)
		} else {
			assert.NoError(t, call.lifecycle.validateComplete(), "carrier shard %d drain", call.index)
		}
		for segmentIndex := range segments {
			execution := segments[segmentIndex].execution
			require.NotNil(t, execution)
			execution.mu.RLock()
			assert.Equal(t, int64(-1), execution.active.Load())
			assert.Zero(t, execution.inflight.Load())
			for itemIndex := range execution.items {
				assert.True(t, execution.items[itemIndex].completed)
				assert.True(t, execution.items[itemIndex].outputSet)
				assert.Equal(t, uint8(1), execution.items[itemIndex].beginCount)
			}
			execution.mu.RUnlock()
		}
	}
	assert.Equal(t, itemCount, totals[helpers.IncrementalEntryPointName("100-first")])
	assert.Equal(t, itemCount, totals[helpers.IncrementalEntryPointName("200-second")])
	if wantPoison {
		assert.Equal(t, 1, poisoned)
	} else {
		assert.Zero(t, poisoned)
	}
}

func assertColdCarrierCommittedCacheEmpty(
	t *testing.T,
	service *RenderService,
	initial *incrementalStateSnapshot,
) {
	t.Helper()
	assert.Zero(t, service.incremental.graph.Generation())
	assert.Same(t, initial, service.incremental.snapshot)
	snapshot := service.incremental.snapshot
	assert.Empty(t, snapshot.cursors)
	assert.Zero(t, snapshot.bindings.Len())
	assert.Zero(t, snapshot.members.Len())
	assert.Zero(t, snapshot.activeGroups.instances.Len())
	assert.Zero(t, snapshot.retired.Len())
	assert.Zero(t, snapshot.results.Len())
	assert.Zero(t, snapshot.derived.Len())
	assert.Zero(t, snapshot.httpEffects.Len())
	assert.Zero(t, snapshot.catalog.Len())
	require.Len(t, snapshot.groupIndexes, 1)
	for _, index := range snapshot.groupIndexes {
		empty, err := index.authenticatedStructurallyEmpty()
		require.NoError(t, err)
		assert.True(t, empty)
	}
	assert.Empty(t, snapshot.groupReady)
	require.NotNil(t, snapshot.preparedPlan)
	require.NoError(t, snapshot.preparedPlan.validateAuthentication(snapshot.results.Root()))
	assert.Zero(t, snapshot.preparedPlan.instances.Len())
	assert.Zero(t, snapshot.preparedPlan.calls.Len())
	assert.Zero(t, snapshot.preparedPlan.backendCandidates.Len())
	assert.Zero(t, snapshot.preparedPlan.profileCandidates.Len())
	assert.Zero(t, snapshot.preparedPlan.profileVariants.Len())
	assert.Zero(t, snapshot.preparedPlan.standaloneProfiles.Len())
	assert.Zero(t, snapshot.preparedPlan.conditions.Len())
	assert.Zero(t, snapshot.preparedPlan.requirements.Len())
	assert.Zero(t, snapshot.preparedPlan.missingProfiles.Len())
	assert.Zero(t, snapshot.preparedPlan.conflictingProfiles.Len())
	assert.Zero(t, snapshot.preparedPlan.outputs.Len())
	assert.Nil(t, snapshot.bindingCache)
}

func newColdCarrierService(
	tb testing.TB,
	itemCount int,
) (*RenderService, *coldCarrierProbeEngine, stores.StoreProvider) {
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
	base, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(tb, err)
	engine := newColdCarrierProbeEngine(tb, base)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	store := k8sstore.NewMemoryStore(2)
	for index := range itemCount {
		name := fmt.Sprintf("route-%03d", index)
		require.NoError(tb, store.Add(
			incrementalTestResource("default", name, nil),
			[]string{"default", name},
		))
	}
	return service, engine, stores.NewRealStoreProvider(map[string]stores.Store{"routes": store})
}

func renderColdCarrierService(
	t *testing.T,
	service *RenderService,
	provider stores.StoreProvider,
) (*RenderResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	return service.Render(ctx, provider, rendercontext.RenderModeReconcile)
}

func coldCarrierExpectedOutput(itemCount int) string {
	var output strings.Builder
	for _, prefix := range []string{"first", "second"} {
		for index := range itemCount {
			fmt.Fprintf(&output, "%s/route-%03d\n", prefix, index)
		}
	}
	return output.String()
}
