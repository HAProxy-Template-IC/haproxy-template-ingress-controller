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
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalSourceTransactionTestMode int32

const (
	incrementalSourceTransactionTestNormal incrementalSourceTransactionTestMode = iota
	incrementalSourceTransactionTestFailSecond
	incrementalSourceTransactionTestPanicSecond
	incrementalSourceTransactionTestCancelSecond
	incrementalSourceTransactionTestForgeContext
	incrementalSourceTransactionTestReuseContext
	incrementalSourceTransactionTestCaptureContext
	incrementalSourceTransactionTestInjectStaleContext
	incrementalSourceTransactionTestForgeForeignSelector
	incrementalSourceTransactionTestCaptureSelector
	incrementalSourceTransactionTestInjectStaleSelector
)

var errIncrementalSourceTransactionTestFailure = errors.New("source transaction injected failure")

type incrementalSourceTransactionProbeEngine struct {
	*templating.ScriggoEngine

	enabled        bool
	mode           atomic.Int32
	sourceCalls    atomic.Int32
	fallbackCalls  atomic.Int32
	waveCalls      atomic.Int32
	vectorCalls    atomic.Int32
	batchCalls     atomic.Int32
	componentCalls atomic.Int32
	poisonChild    atomic.Int32

	mu            sync.Mutex
	cancel        context.CancelCauseFunc
	staleContext  context.Context
	staleSelector templating.IncrementalSourceTransactionChildSelector
	vectorNames   []string
}

type incrementalSourceTransactionProbeLifecycle struct {
	templating.IncrementalComponentSourceTransactionLifecycle
	engine *incrementalSourceTransactionProbeEngine
}

type incrementalSourceTransactionTestSelector struct {
	child int
}

func (s *incrementalSourceTransactionTestSelector) ActiveIncrementalSourceTransactionChild() (int, error) {
	return s.child, nil
}

func (e *incrementalSourceTransactionProbeEngine) IncrementalComponentSourceTransactionsEligibility() bool {
	return e.enabled && e.ScriggoEngine.IncrementalComponentSourceTransactionsEligibility()
}

func (e *incrementalSourceTransactionProbeEngine) RenderIncrementalComponentSourceTransactions(
	ctx context.Context,
	input templating.IncrementalComponentSourceTransactionsInput,
) error {
	e.sourceCalls.Add(1)
	input.Lifecycle = &incrementalSourceTransactionProbeLifecycle{
		IncrementalComponentSourceTransactionLifecycle: input.Lifecycle,
		engine: e,
	}
	return e.ScriggoEngine.RenderIncrementalComponentSourceTransactions(ctx, input)
}

func (e *incrementalSourceTransactionProbeEngine) BindIncrementalSourceTransactionResources(
	templateNames []string,
	resources any,
	lease templating.IncrementalResourceInvocationLease,
	selector templating.IncrementalSourceTransactionChildSelector,
) (any, error) {
	switch incrementalSourceTransactionTestMode(e.mode.Load()) {
	case incrementalSourceTransactionTestForgeForeignSelector:
		selector = &incrementalVectorExecution{}
	case incrementalSourceTransactionTestCaptureSelector:
		e.mu.Lock()
		e.staleSelector = selector
		e.mu.Unlock()
	case incrementalSourceTransactionTestInjectStaleSelector:
		e.mu.Lock()
		selector = e.staleSelector
		e.mu.Unlock()
	default:
		if child := int(e.poisonChild.Load()); child >= 0 {
			selector = &incrementalSourceTransactionTestSelector{child: child}
		}
	}
	return e.ScriggoEngine.BindIncrementalSourceTransactionResources(
		templateNames, resources, lease, selector,
	)
}

func (e *incrementalSourceTransactionProbeEngine) RenderIncrementalComponentVectorCarrierWaves(
	ctx context.Context,
	input templating.IncrementalComponentVectorCarrierWavesInput,
) error {
	e.fallbackCalls.Add(1)
	e.waveCalls.Add(1)
	return e.ScriggoEngine.RenderIncrementalComponentVectorCarrierWaves(ctx, input)
}

func (e *incrementalSourceTransactionProbeEngine) RenderIncrementalComponentVector(
	ctx context.Context,
	templateName string,
	input templating.IncrementalComponentVectorInput,
) error {
	e.fallbackCalls.Add(1)
	e.vectorCalls.Add(1)
	e.mu.Lock()
	e.vectorNames = append(e.vectorNames, templateName)
	e.mu.Unlock()
	return e.ScriggoEngine.RenderIncrementalComponentVector(ctx, templateName, input)
}

func (e *incrementalSourceTransactionProbeEngine) RenderIncrementalComponents(
	ctx context.Context,
	templateName string,
	items []templating.IncrementalComponentBatchItem,
) ([]string, error) {
	e.fallbackCalls.Add(1)
	e.batchCalls.Add(1)
	return e.ScriggoEngine.RenderIncrementalComponents(ctx, templateName, items)
}

func (e *incrementalSourceTransactionProbeEngine) RenderIncrementalComponent(
	ctx context.Context,
	templateName string,
	templateContext map[string]any,
) (string, error) {
	e.fallbackCalls.Add(1)
	e.componentCalls.Add(1)
	return e.ScriggoEngine.RenderIncrementalComponent(ctx, templateName, templateContext)
}

func (e *incrementalSourceTransactionProbeEngine) vectorCallNames() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.vectorNames...)
}

func (l *incrementalSourceTransactionProbeLifecycle) LoadSourceTransactionWave(
	ctx context.Context,
	wave int,
) (templating.IncrementalComponentSourceTransactionBatch, error) {
	batch, err := l.IncrementalComponentSourceTransactionLifecycle.LoadSourceTransactionWave(ctx, wave)
	if err != nil {
		return batch, err
	}
	switch incrementalSourceTransactionTestMode(l.engine.mode.Load()) {
	case incrementalSourceTransactionTestForgeContext:
		if len(batch.ChildContexts) > 0 {
			batch.ChildContexts[0] = context.Background()
		}
	case incrementalSourceTransactionTestReuseContext:
		if len(batch.ChildContexts) > 1 {
			batch.ChildContexts[1] = batch.ChildContexts[0]
		}
	case incrementalSourceTransactionTestCaptureContext:
		if len(batch.ChildContexts) > 0 {
			l.engine.mu.Lock()
			l.engine.staleContext = batch.ChildContexts[0]
			l.engine.mu.Unlock()
		}
	case incrementalSourceTransactionTestInjectStaleContext:
		l.engine.mu.Lock()
		stale := l.engine.staleContext
		l.engine.mu.Unlock()
		if len(batch.ChildContexts) > 0 && stale != nil {
			batch.ChildContexts[0] = stale
		}
	}
	return batch, nil
}

func (l *incrementalSourceTransactionProbeLifecycle) Begin(index int) error {
	if index == 1 {
		switch incrementalSourceTransactionTestMode(l.engine.mode.Load()) {
		case incrementalSourceTransactionTestFailSecond:
			return errIncrementalSourceTransactionTestFailure
		case incrementalSourceTransactionTestPanicSecond:
			panic("source transaction injected panic")
		case incrementalSourceTransactionTestCancelSecond:
			l.engine.mu.Lock()
			cancel := l.engine.cancel
			l.engine.mu.Unlock()
			if cancel != nil {
				cancel(context.Canceled)
			}
			return context.Canceled
		}
	}
	return l.IncrementalComponentSourceTransactionLifecycle.Begin(index)
}

func TestIncrementalSourceTransactionsDistinctPropsAndSharedWinnerMatchDisabledControl(t *testing.T) {
	cfg := incrementalSourceTransactionSharedConfig()
	provider := incrementalSourceTransactionTestProvider(t)
	candidate, candidateEngine := newIncrementalSourceTransactionTestService(t, cfg, true)
	control, controlEngine := newIncrementalSourceTransactionTestService(t, cfg, false)

	candidateResult := renderIncrementalSourceTransactionTestResult(t, candidate, provider)
	controlResult := renderIncrementalSourceTransactionTestResult(t, control, provider)
	assertIncrementalSourceTransactionObservablesEqual(t, controlResult, candidateResult)
	assert.Contains(t, candidateResult.HAProxyConfig, "route=left")
	assert.Positive(t, candidateEngine.sourceCalls.Load())
	assert.Zero(t, candidateEngine.fallbackCalls.Load(),
		"waves=%d vectors=%d batches=%d components=%d vectorNames=%v",
		candidateEngine.waveCalls.Load(),
		candidateEngine.vectorCalls.Load(), candidateEngine.batchCalls.Load(),
		candidateEngine.componentCalls.Load(), candidateEngine.vectorCallNames())
	assert.Zero(t, controlEngine.sourceCalls.Load())
	assert.Positive(t, controlEngine.fallbackCalls.Load())

	for _, service := range []*RenderService{candidate, control} {
		for _, componentName := range []string{"100-left", "110-right", "200-consumer"} {
			component := service.incremental.components[componentName]
			query := componentQueryKey(&component, "routes", "default", "route")
			assert.Equal(t, uint64(1), service.incremental.graph.Counters(query).Executions, componentName)
		}
	}
	for _, componentName := range []string{"100-left", "110-right", "200-consumer"} {
		candidateComponent := candidate.incremental.components[componentName]
		controlComponent := control.incremental.components[componentName]
		candidateQuery := componentQueryKey(&candidateComponent, "routes", "default", "route")
		controlQuery := componentQueryKey(&controlComponent, "routes", "default", "route")
		candidateRoot := requireGraphExactValue(t, candidate.incremental.graph, candidateQuery)
		controlRoot := requireGraphExactValue(t, control.incremental.graph, controlQuery)
		equal, err := candidateRoot.ExactEqual(controlRoot)
		require.NoError(t, err)
		assert.True(t, equal, componentName)
	}

	sourceCalls := candidateEngine.sourceCalls.Load()
	warm := renderIncrementalSourceTransactionTestResult(t, candidate, provider)
	assertIncrementalSourceTransactionObservablesEqual(t, candidateResult, warm)
	assert.Equal(t, sourceCalls, candidateEngine.sourceCalls.Load())
}

func TestIncrementalSourceTransactionsAbortPartialArenaWithoutFallback(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode incrementalSourceTransactionTestMode
		want string
	}{
		{name: "failure", mode: incrementalSourceTransactionTestFailSecond, want: errIncrementalSourceTransactionTestFailure.Error()},
		{name: "panic", mode: incrementalSourceTransactionTestPanicSecond, want: "source transaction injected panic"},
		{name: "cancellation", mode: incrementalSourceTransactionTestCancelSecond, want: context.Canceled.Error()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := incrementalSourceTransactionPoisonConfig()
			provider := incrementalSourceTransactionTestProvider(t)
			service, engine := newIncrementalSourceTransactionTestService(t, cfg, true)
			engine.mode.Store(int32(testCase.mode))
			ctx := t.Context()
			if testCase.mode == incrementalSourceTransactionTestCancelSecond {
				var cancel context.CancelCauseFunc
				ctx, cancel = context.WithCancelCause(ctx)
				engine.mu.Lock()
				engine.cancel = cancel
				engine.mu.Unlock()
			}
			generation := service.incremental.graph.Generation()
			snapshot := service.incremental.snapshot

			result, err := service.Render(ctx, provider, rendercontext.RenderModeReconcile)
			require.ErrorContains(t, err, testCase.want)
			assert.Nil(t, result)
			assert.Equal(t, generation, service.incremental.graph.Generation())
			assert.Same(t, snapshot, service.incremental.snapshot)
			assert.Positive(t, engine.sourceCalls.Load())
			assert.Zero(t, engine.fallbackCalls.Load())

			engine.mode.Store(int32(incrementalSourceTransactionTestNormal))
			engine.mu.Lock()
			engine.cancel = nil
			engine.mu.Unlock()
			retry := renderIncrementalSourceTransactionTestResult(t, service, provider)
			assert.Contains(t, retry.HAProxyConfig, "first/route")
			assert.Contains(t, retry.HAProxyConfig, "second/route")
			assert.Zero(t, engine.fallbackCalls.Load())
		})
	}
}

func TestIncrementalSourceTransactionsRejectForgedAndStaleChildAuthorities(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode incrementalSourceTransactionTestMode
		want string
	}{
		{name: "foreign context", mode: incrementalSourceTransactionTestForgeContext, want: "crossed an incremental component vector boundary"},
		{name: "reused context", mode: incrementalSourceTransactionTestReuseContext, want: "context"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, engine := newIncrementalSourceTransactionTestService(
				t, incrementalSourceTransactionPoisonConfig(), true,
			)
			engine.mode.Store(int32(testCase.mode))
			generation := service.incremental.graph.Generation()
			snapshot := service.incremental.snapshot
			result, err := service.Render(
				t.Context(), incrementalSourceTransactionTestProvider(t), rendercontext.RenderModeReconcile,
			)
			require.ErrorContains(t, err, testCase.want)
			assert.Nil(t, result)
			assert.Equal(t, generation, service.incremental.graph.Generation())
			assert.Same(t, snapshot, service.incremental.snapshot)
			assert.Zero(t, engine.fallbackCalls.Load())
		})
	}

	t.Run("stale generation", func(t *testing.T) {
		cfg := incrementalSourceTransactionPoisonConfig()
		provider := incrementalSourceTransactionTestProvider(t)
		first, engine := newIncrementalSourceTransactionTestService(t, cfg, true)
		engine.mode.Store(int32(incrementalSourceTransactionTestCaptureContext))
		_ = renderIncrementalSourceTransactionTestResult(t, first, provider)
		engine.mu.Lock()
		require.NotNil(t, engine.staleContext)
		engine.mu.Unlock()

		second := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
		engine.mode.Store(int32(incrementalSourceTransactionTestInjectStaleContext))
		result, err := second.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
		require.ErrorContains(t, err, "context")
		assert.Nil(t, result)
		assert.Zero(t, second.incremental.graph.Generation())
		assert.Zero(t, engine.fallbackCalls.Load())
	})
}

func TestIncrementalSourceTransactionsRejectForgedChildSelector(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		mode        incrementalSourceTransactionTestMode
		poisonChild int32
	}{
		{name: "same batch allowed child", poisonChild: 0},
		{name: "foreign execution", mode: incrementalSourceTransactionTestForgeForeignSelector, poisonChild: -1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, engine := newIncrementalSourceTransactionTestService(
				t, incrementalSourceTransactionPoisonConfig(), true,
			)
			engine.mode.Store(int32(testCase.mode))
			engine.poisonChild.Store(testCase.poisonChild)
			generation := service.incremental.graph.Generation()
			snapshot := service.incremental.snapshot

			result, err := service.Render(
				t.Context(), incrementalSourceTransactionTestProvider(t), rendercontext.RenderModeReconcile,
			)
			require.ErrorContains(t, err, "selector has different authority")
			assert.Nil(t, result)
			assert.Equal(t, generation, service.incremental.graph.Generation())
			assert.Same(t, snapshot, service.incremental.snapshot)
			assert.Positive(t, engine.sourceCalls.Load())
			assert.Zero(t, engine.fallbackCalls.Load())
		})
	}

	t.Run("stale generation", func(t *testing.T) {
		cfg := incrementalSourceTransactionPoisonConfig()
		provider := incrementalSourceTransactionTestProvider(t)
		first, engine := newIncrementalSourceTransactionTestService(t, cfg, true)
		engine.mode.Store(int32(incrementalSourceTransactionTestCaptureSelector))
		_ = renderIncrementalSourceTransactionTestResult(t, first, provider)
		engine.mu.Lock()
		require.NotNil(t, engine.staleSelector)
		engine.mu.Unlock()

		second := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
		engine.mode.Store(int32(incrementalSourceTransactionTestInjectStaleSelector))
		result, err := second.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
		require.ErrorContains(t, err, "selector has different authority")
		assert.Nil(t, result)
		assert.Zero(t, second.incremental.graph.Generation())
		assert.Zero(t, engine.fallbackCalls.Load())
	})
}

func TestIncrementalSourceTransactionsHTTPObservationsMatchDisabledControl(t *testing.T) {
	var body atomic.Value
	body.Store("first")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(server.Close)
	cfg := incrementalSourceTransactionHTTPConfig(server.URL)
	provider := incrementalSourceTransactionTestProvider(t)
	candidate, candidateEngine, candidateHTTP := newIncrementalSourceTransactionHTTPTestService(t, cfg, true)
	control, controlEngine, controlHTTP := newIncrementalSourceTransactionHTTPTestService(t, cfg, false)

	candidateOutputOnly := renderIncrementalSourceTransactionTestResult(t, candidate, provider)
	controlOutputOnly := renderIncrementalSourceTransactionTestResult(t, control, provider)
	assertRenderResultObservablesEqual(t, controlOutputOnly, candidateOutputOnly)
	assert.Contains(t, candidateOutputOnly.HAProxyConfig, "route=first")
	assert.Equal(t, int32(2), requests.Load())

	candidateCold := renderIncrementalSourceTransactionTestResult(t, candidate, provider)
	controlCold := renderIncrementalSourceTransactionTestResult(t, control, provider)
	assertRenderResultObservablesEqual(t, candidateOutputOnly, candidateCold)
	assertRenderResultObservablesEqual(t, controlOutputOnly, controlCold)
	assertRenderResultObservablesEqual(t, controlCold, candidateCold)
	assert.Contains(t, candidateCold.HAProxyConfig, "route=first")
	coldEffects := authenticatedIncrementalHTTPEffectTuples(t, candidate)
	require.Len(t, coldEffects, 1)
	assert.Equal(t, coldEffects, authenticatedIncrementalHTTPEffectTuples(t, control))
	assert.Equal(t, int32(2), requests.Load())
	assert.Positive(t, candidateEngine.sourceCalls.Load())
	assert.Zero(t, candidateEngine.fallbackCalls.Load())
	assert.Zero(t, controlEngine.sourceCalls.Load())
	assert.Positive(t, controlEngine.fallbackCalls.Load())

	pair := &sourceTransactionHTTPPair{
		candidate: candidate, control: control,
		candidateEngine: candidateEngine, controlEngine: controlEngine,
		candidateHTTP: candidateHTTP, controlHTTP: controlHTTP,
		provider: provider, requests: &requests,
	}
	pair.requireWarmReuse(t, candidateCold, controlCold, coldEffects, 2)

	body.Store("second")
	promoteIncrementalHTTPBody(t, candidateHTTP, server.URL)
	promoteIncrementalHTTPBody(t, controlHTTP, server.URL)
	candidateChanged, controlChanged, changedEffects := pair.requireChangedBody(t, coldEffects)
	pair.requireWarmReuse(t, candidateChanged, controlChanged, changedEffects, 4)
}

type sourceTransactionHTTPPair struct {
	candidate       *RenderService
	control         *RenderService
	candidateEngine *incrementalSourceTransactionProbeEngine
	controlEngine   *incrementalSourceTransactionProbeEngine
	candidateHTTP   *controllerhttpstore.Component
	controlHTTP     *controllerhttpstore.Component
	provider        stores.StoreProvider
	requests        *atomic.Int32
}

func (p *sourceTransactionHTTPPair) requireWarmReuse(
	t *testing.T,
	candidatePrevious, controlPrevious *RenderResult,
	wantEffects []authenticatedIncrementalHTTPEffectTuple,
	wantRequests int32,
) {
	t.Helper()
	candidateSourceCalls := p.candidateEngine.sourceCalls.Load()
	controlFallbackCalls := p.controlEngine.fallbackCalls.Load()
	candidateWarm := renderIncrementalSourceTransactionTestResult(t, p.candidate, p.provider)
	controlWarm := renderIncrementalSourceTransactionTestResult(t, p.control, p.provider)
	assertRenderResultObservablesEqual(t, candidatePrevious, candidateWarm)
	assertRenderResultObservablesEqual(t, controlPrevious, controlWarm)
	assertRenderResultObservablesEqual(t, controlWarm, candidateWarm)
	assert.Equal(t, wantEffects, authenticatedIncrementalHTTPEffectTuples(t, p.candidate))
	assert.Equal(t, wantEffects, authenticatedIncrementalHTTPEffectTuples(t, p.control))
	assert.Equal(t, candidateSourceCalls, p.candidateEngine.sourceCalls.Load())
	assert.Equal(t, controlFallbackCalls, p.controlEngine.fallbackCalls.Load())
	assert.Equal(t, wantRequests, p.requests.Load())
}

func (p *sourceTransactionHTTPPair) requireChangedBody(
	t *testing.T,
	coldEffects []authenticatedIncrementalHTTPEffectTuple,
) (candidateChanged, controlChanged *RenderResult, changedEffects []authenticatedIncrementalHTTPEffectTuple) {
	t.Helper()
	candidateChanged = renderIncrementalSourceTransactionTestResult(t, p.candidate, p.provider)
	controlChanged = renderIncrementalSourceTransactionTestResult(t, p.control, p.provider)
	assertRenderResultObservablesEqual(t, controlChanged, candidateChanged)
	assert.Contains(t, candidateChanged.HAProxyConfig, "route=second")
	changedEffects = authenticatedIncrementalHTTPEffectTuples(t, p.candidate)
	require.Len(t, changedEffects, 1)
	assert.Equal(t, changedEffects, authenticatedIncrementalHTTPEffectTuples(t, p.control))
	assert.NotEqual(t, coldEffects, changedEffects)
	assert.Equal(t, int32(4), p.requests.Load())
	for _, service := range []*RenderService{p.candidate, p.control} {
		component := service.incremental.components["routes"]
		query := componentQueryKey(&component, "routes", "default", "route")
		assert.Equal(t, uint64(2), service.incremental.graph.Counters(query).Executions)
	}
	return candidateChanged, controlChanged, changedEffects
}

func TestIncrementalSourceTransactionsAllOutputAndEffectClassesMatchDisabledControl(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("served"))
	}))
	t.Cleanup(server.Close)

	effects := newSourceTransactionEffectsCase(t, server.URL, &requests)
	candidateCold, controlCold, coldArtifacts, coldDerived := effects.requireColdPhase(t)
	effects.requireWarmPhase(t, candidateCold, controlCold, &coldArtifacts)

	routes := effects.provider.GetStore("routes")
	require.NotNil(t, routes)
	changedArtifacts, changedExecutions := effects.requireChangedPhase(t, routes, &coldArtifacts)
	deletedExecutions := effects.requireDeletedPhase(t, routes, &changedArtifacts, changedExecutions)
	recreated := effects.requireRecreatedPhase(t, routes, &coldArtifacts, deletedExecutions, coldDerived)
	effects.requireUnchangedPhase(t, &recreated)
}

type sourceTransactionEffectsCase struct {
	cfg             *config.Config
	provider        stores.StoreProvider
	candidate       *RenderService
	control         *RenderService
	candidateEngine *incrementalSourceTransactionProbeEngine
	controlEngine   *incrementalSourceTransactionProbeEngine
	candidateHTTP   *controllerhttpstore.Component
	transitionTime  func(context.Context) (string, error)
	componentNames  []string
	requests        *atomic.Int32
}

type sourceTransactionRecreatedPhase struct {
	candidate  *RenderResult
	control    *RenderResult
	artifacts  incrementalSourceTransactionArtifactFamilies
	executions map[string]uint64
}

func newSourceTransactionEffectsCase(
	t *testing.T,
	serverURL string,
	requests *atomic.Int32,
) *sourceTransactionEffectsCase {
	t.Helper()
	cfg := incrementalSourceTransactionEffectsConfig(serverURL)
	provider := incrementalSourceTransactionEffectsProvider(t)
	candidate, candidateEngine, candidateHTTP := newIncrementalSourceTransactionHTTPTestService(t, cfg, true)
	control, controlEngine, _ := newIncrementalSourceTransactionHTTPTestService(t, cfg, false)
	transitionTime := func(context.Context) (string, error) {
		return "2026-08-27T12:00:00Z", nil
	}
	candidate.incremental.transitionNow = transitionTime
	control.incremental.transitionNow = transitionTime
	return &sourceTransactionEffectsCase{
		cfg: cfg, provider: provider,
		candidate: candidate, control: control,
		candidateEngine: candidateEngine, controlEngine: controlEngine,
		candidateHTTP: candidateHTTP, transitionTime: transitionTime,
		componentNames: []string{"200-backends", "300-governance", "400-status", "500-http"},
		requests:       requests,
	}
}

func (c *sourceTransactionEffectsCase) render(t *testing.T) (candidate, control *RenderResult) {
	t.Helper()
	return renderIncrementalSourceTransactionTestResult(t, c.candidate, c.provider),
		renderIncrementalSourceTransactionTestResult(t, c.control, c.provider)
}

func (c *sourceTransactionEffectsCase) executionCounts() map[string]uint64 {
	return incrementalSourceTransactionExecutionCounts(c.candidate, c.componentNames, []string{"a", "b"})
}

func (c *sourceTransactionEffectsCase) requireColdPhase(t *testing.T) (
	candidateCold *RenderResult,
	controlCold *RenderResult,
	coldArtifacts incrementalSourceTransactionArtifactFamilies,
	coldDerived []incrementalDerivedResource,
) {
	t.Helper()
	candidateOutputOnly, controlOutputOnly := c.render(t)
	assertIncrementalSourceTransactionObservablesEqual(t, controlOutputOnly, candidateOutputOnly)
	outputOnlyArtifacts := incrementalSourceTransactionArtifactFamilyBytes(t, candidateOutputOnly)
	assert.Equal(t, outputOnlyArtifacts, incrementalSourceTransactionArtifactFamilyBytes(t, controlOutputOnly))

	candidateCold, controlCold = c.render(t)
	coldArtifacts = assertIncrementalSourceTransactionEffectPhase(
		t, c.candidate, c.control, candidateCold, controlCold, c.componentNames, []string{"a", "b"},
	)
	assertIncrementalSourceTransactionObservablesEqual(t, candidateOutputOnly, candidateCold)
	assertIncrementalSourceTransactionObservablesEqual(t, controlOutputOnly, controlCold)
	assert.Equal(t, outputOnlyArtifacts, coldArtifacts)
	coldDerived = authenticatedIncrementalDerivedResources(t, c.candidate)
	c.assertColdObservables(t, candidateCold)
	c.assertColdExecutionCounts(t)
	return candidateCold, controlCold, coldArtifacts, coldDerived
}

func (c *sourceTransactionEffectsCase) assertColdObservables(t *testing.T, candidateCold *RenderResult) {
	t.Helper()
	assert.Contains(t, candidateCold.HAProxyConfig, "# main=alpha")
	assert.Contains(t, candidateCold.HAProxyConfig, "a/alpha=served")
	assert.NotEmpty(t, requireRenderPlan(t, candidateCold).Backends)
	assert.NotEmpty(t, materializedStatusPatches(t, candidateCold))
	assert.NotEmpty(t, requireRenderEvents(t, candidateCold))
	assert.NotEmpty(t, requireRenderedResources(t, candidateCold))
	assert.Positive(t, c.candidate.incremental.snapshot.derived.Len())
	assert.Positive(t, c.candidateEngine.sourceCalls.Load())
	assert.Equal(t, int32(2), c.candidateEngine.fallbackCalls.Load())
	assert.Equal(t, int32(2), c.candidateEngine.vectorCalls.Load())
	assert.Equal(t, []string{
		helpers.IncrementalEntryPointName("300-governance"),
		helpers.IncrementalEntryPointName("300-governance"),
	}, c.candidateEngine.vectorCallNames())
	assert.Zero(t, c.candidateEngine.waveCalls.Load())
	assert.Zero(t, c.candidateEngine.batchCalls.Load())
	assert.Zero(t, c.candidateEngine.componentCalls.Load())
	assert.Zero(t, c.controlEngine.sourceCalls.Load())
	assert.Positive(t, c.controlEngine.fallbackCalls.Load())
	assert.Equal(t, int32(2), c.requests.Load())
}

func (c *sourceTransactionEffectsCase) assertColdExecutionCounts(t *testing.T) {
	t.Helper()
	for _, service := range []*RenderService{c.candidate, c.control} {
		for _, componentName := range c.componentNames {
			component := service.incremental.components[componentName]
			for _, name := range []string{"a", "b"} {
				query := componentQueryKey(&component, "routes", "default", name)
				assert.Equal(t, uint64(1), service.incremental.graph.Counters(query).Executions, componentName+"/"+name)
			}
		}
	}
}

func (c *sourceTransactionEffectsCase) requireWarmPhase(
	t *testing.T,
	candidateCold, controlCold *RenderResult,
	coldArtifacts *incrementalSourceTransactionArtifactFamilies,
) {
	t.Helper()
	candidateSourceCalls := c.candidateEngine.sourceCalls.Load()
	candidateFallbackCalls := c.candidateEngine.fallbackCalls.Load()
	controlFallbackCalls := c.controlEngine.fallbackCalls.Load()
	candidateWarm, controlWarm := c.render(t)
	warmArtifacts := assertIncrementalSourceTransactionEffectPhase(
		t, c.candidate, c.control, candidateWarm, controlWarm, c.componentNames, []string{"a", "b"},
	)
	assertIncrementalSourceTransactionObservablesEqual(t, candidateCold, candidateWarm)
	assertIncrementalSourceTransactionObservablesEqual(t, controlCold, controlWarm)
	assert.Equal(t, *coldArtifacts, warmArtifacts)
	assert.Equal(t, candidateSourceCalls, c.candidateEngine.sourceCalls.Load())
	assert.Equal(t, candidateFallbackCalls, c.candidateEngine.fallbackCalls.Load())
	assert.Equal(t, controlFallbackCalls, c.controlEngine.fallbackCalls.Load())
}

func (c *sourceTransactionEffectsCase) requireChangedPhase(
	t *testing.T,
	routes stores.Store,
	coldArtifacts *incrementalSourceTransactionArtifactFamilies,
) (changedArtifacts incrementalSourceTransactionArtifactFamilies, changedExecutions map[string]uint64) {
	t.Helper()
	beforeChange := c.executionCounts()
	require.NoError(t, routes.Update(statusPatchResource("a", "alpha-2"), []string{"default", "a"}))
	candidateChanged, controlChanged := c.render(t)
	changedArtifacts = assertIncrementalSourceTransactionEffectPhase(
		t, c.candidate, c.control, candidateChanged, controlChanged, c.componentNames, []string{"a", "b"},
	)
	assertIncrementalSourceTransactionColdOracle(
		t, c.cfg, c.provider, c.candidateHTTP, c.transitionTime, c.candidate, candidateChanged, []string{"a", "b"},
	)
	assert.Contains(t, candidateChanged.HAProxyConfig, "# main=alpha-2")
	assertIncrementalSourceTransactionArtifactFamiliesChanged(t, coldArtifacts, &changedArtifacts)
	changedExecutions = c.executionCounts()
	assertIncrementalSourceTransactionChangedLocality(t, beforeChange, changedExecutions, c.componentNames)
	return changedArtifacts, changedExecutions
}

func (c *sourceTransactionEffectsCase) requireDeletedPhase(
	t *testing.T,
	routes stores.Store,
	changedArtifacts *incrementalSourceTransactionArtifactFamilies,
	changedExecutions map[string]uint64,
) map[string]uint64 {
	t.Helper()
	require.NoError(t, routes.Delete("default", "a", []string{"default", "a"}))
	candidateDeleted, controlDeleted := c.render(t)
	deletedArtifacts := assertIncrementalSourceTransactionEffectPhase(
		t, c.candidate, c.control, candidateDeleted, controlDeleted, c.componentNames, []string{"b"},
	)
	assertIncrementalSourceTransactionColdOracle(
		t, c.cfg, c.provider, c.candidateHTTP, c.transitionTime, c.candidate, candidateDeleted, []string{"b"},
	)
	assert.Contains(t, candidateDeleted.HAProxyConfig, "# main=beta")
	assertIncrementalSourceTransactionArtifactFamiliesChanged(t, changedArtifacts, &deletedArtifacts)
	deletedExecutions := c.executionCounts()
	for _, componentName := range c.componentNames {
		if componentName == "300-governance" {
			assert.Equal(t, changedExecutions[componentName+"/a"]+1, deletedExecutions[componentName+"/a"], componentName+"/a")
		} else {
			assert.Zero(t, deletedExecutions[componentName+"/a"], componentName+"/a")
		}
		assert.Equal(t, changedExecutions[componentName+"/b"], deletedExecutions[componentName+"/b"], componentName+"/b")
	}
	return deletedExecutions
}

func (c *sourceTransactionEffectsCase) requireRecreatedPhase(
	t *testing.T,
	routes stores.Store,
	coldArtifacts *incrementalSourceTransactionArtifactFamilies,
	deletedExecutions map[string]uint64,
	coldDerived []incrementalDerivedResource,
) sourceTransactionRecreatedPhase {
	t.Helper()
	recreated := statusPatchResource("a", "alpha")
	recreated["metadata"].(map[string]any)["uid"] = "uid-a-recreated"
	require.NoError(t, routes.Add(recreated, []string{"default", "a"}))
	candidateRecreated, controlRecreated := c.render(t)
	recreatedArtifacts := assertIncrementalSourceTransactionEffectPhase(
		t, c.candidate, c.control, candidateRecreated, controlRecreated, c.componentNames, []string{"a", "b"},
	)
	assertIncrementalSourceTransactionColdOracle(
		t, c.cfg, c.provider, c.candidateHTTP, c.transitionTime, c.candidate, candidateRecreated, []string{"a", "b"},
	)
	assert.Contains(t, candidateRecreated.HAProxyConfig, "# main=alpha")
	assert.Equal(t, *coldArtifacts, recreatedArtifacts)
	recreatedExecutions := c.executionCounts()
	for _, componentName := range c.componentNames {
		if componentName == "300-governance" {
			assert.Equal(t, deletedExecutions[componentName+"/a"]+1, recreatedExecutions[componentName+"/a"], componentName+"/a")
		} else {
			assert.Equal(t, uint64(1), recreatedExecutions[componentName+"/a"], componentName+"/a")
		}
		assert.Equal(t, deletedExecutions[componentName+"/b"], recreatedExecutions[componentName+"/b"], componentName+"/b")
	}
	assert.NotEqual(
		t,
		coldDerived,
		authenticatedIncrementalDerivedResources(t, c.candidate),
		"recreated UID must not reuse the deleted generation's derivation",
	)
	recreatedPatches := materializedStatusPatches(t, candidateRecreated)
	recreatedUID := ""
	for patchIndex := range recreatedPatches {
		if patch := &recreatedPatches[patchIndex]; patch.Name == "a" {
			recreatedUID = patch.UID
		}
	}
	assert.Equal(t, "uid-a-recreated", recreatedUID)
	return sourceTransactionRecreatedPhase{
		candidate:  candidateRecreated,
		control:    controlRecreated,
		artifacts:  recreatedArtifacts,
		executions: recreatedExecutions,
	}
}

func (c *sourceTransactionEffectsCase) requireUnchangedPhase(
	t *testing.T,
	recreated *sourceTransactionRecreatedPhase,
) {
	t.Helper()
	candidateSourceCalls := c.candidateEngine.sourceCalls.Load()
	candidateFallbackCalls := c.candidateEngine.fallbackCalls.Load()
	controlFallbackCalls := c.controlEngine.fallbackCalls.Load()
	candidateUnchanged, controlUnchanged := c.render(t)
	unchangedArtifacts := assertIncrementalSourceTransactionEffectPhase(
		t, c.candidate, c.control, candidateUnchanged, controlUnchanged, c.componentNames, []string{"a", "b"},
	)
	assertIncrementalSourceTransactionObservablesEqual(t, recreated.candidate, candidateUnchanged)
	assertIncrementalSourceTransactionObservablesEqual(t, recreated.control, controlUnchanged)
	assert.Equal(t, recreated.artifacts, unchangedArtifacts)
	assert.Equal(t, recreated.executions, c.executionCounts())
	assert.Equal(t, candidateSourceCalls, c.candidateEngine.sourceCalls.Load())
	assert.Equal(t, candidateFallbackCalls, c.candidateEngine.fallbackCalls.Load())
	assert.Equal(t, controlFallbackCalls, c.controlEngine.fallbackCalls.Load())
	assert.Equal(t, int32(2), c.requests.Load())
}

func newIncrementalSourceTransactionTestService(
	tb testing.TB,
	cfg *config.Config,
	enabled bool,
) (*RenderService, *incrementalSourceTransactionProbeEngine) {
	tb.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	base, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(tb, err)
	scriggo, ok := base.(*templating.ScriggoEngine)
	require.True(tb, ok)
	engine := &incrementalSourceTransactionProbeEngine{ScriggoEngine: scriggo, enabled: enabled}
	engine.poisonChild.Store(-1)
	return NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()}), engine
}

func newIncrementalSourceTransactionHTTPTestService(
	tb testing.TB,
	cfg *config.Config,
	enabled bool,
) (*RenderService, *incrementalSourceTransactionProbeEngine, *controllerhttpstore.Component) {
	tb.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	httpComponent := controllerhttpstore.New(bus, logger, -time.Hour)
	service, engine := newIncrementalSourceTransactionTestServiceWithHTTPComponent(
		tb, cfg, enabled, logger, httpComponent,
	)
	return service, engine, httpComponent
}

func newIncrementalSourceTransactionTestServiceWithHTTPComponent(
	tb testing.TB,
	cfg *config.Config,
	enabled bool,
	logger *slog.Logger,
	httpComponent *controllerhttpstore.Component,
) (*RenderService, *incrementalSourceTransactionProbeEngine) {
	tb.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	base, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(tb, err)
	scriggo, ok := base.(*templating.ScriggoEngine)
	require.True(tb, ok)
	engine := &incrementalSourceTransactionProbeEngine{ScriggoEngine: scriggo, enabled: enabled}
	engine.poisonChild.Store(-1)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger, HTTPStoreComponent: httpComponent,
	})
	return service, engine
}

func incrementalSourceTransactionTestProvider(tb testing.TB) stores.StoreProvider {
	tb.Helper()
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(tb, routes.Add(
		map[string]any{
			"apiVersion": "widgets.example.test/v1", "kind": "WidgetRoute",
			"metadata": map[string]any{
				"namespace": "default", "name": "route", "uid": "uid-route", "resourceVersion": "rv-route",
			},
			"spec": map[string]any{"value": "value"},
		},
		[]string{"default", "route"},
	))
	return stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
}

func incrementalSourceTransactionSharedConfig() *config.Config {
	producer := func(rank, value string) config.TemplateSnippet {
		return config.TemplateSnippet{
			Requires: []string{"routes"},
			Incremental: &config.IncrementalTemplate{
				BindingsTemplate: `{{ toJSON(map[string]any{"routes": map[string]any{"rank": "` + rank + `", "value": "` + value + `"}}) }}`,
				Group:            "producers", Effects: []config.IncrementalEffect{config.IncrementalEffectPublishValue},
			},
			Template: `{%%
var name = item | dig_string("", "metadata", "name")
show shared.PublishRanked("values", name, props | dig_string("", "rank"), props | dig_string("", "value"))
%%}`,
		}
	}
	left := producer("1", "left")
	left.Name = "100-left"
	right := producer("2", "right")
	right.Name = "110-right"
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "widgets.example.test/v1", Resources: "widgetroutes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-left":  left,
			"110-right": right,
			"200-consumer": {
				Name: "200-consumer", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "consumers", Consumes: []string{"producers"},
				},
				Template: `{%%
var name = item | dig_string("", "metadata", "name")
var selected, found = shared.Select("producers", "values", name)
if !found { fail("shared winner is missing") }
show name + "=" + tostring(selected) + "\n"
%%}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{
			Template: `{{ render "100-left" }}{{ render "110-right" }}{{ render "200-consumer" }}`,
		},
	}
}

func incrementalSourceTransactionPoisonConfig() *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "widgets.example.test/v1", Resources: "widgetroutes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-first": {
				Name: "100-first", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes", Group: "routes"},
				Template: `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
show "first/" + (current | dig_string("", "metadata", "name")) + "\n"
%%}`,
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
}

func incrementalSourceTransactionHTTPConfig(url string) *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "widgets.example.test/v1", Resources: "widgetroutes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name: "routes", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{{ item | dig_string("", "metadata", "name") }}={{ http.Fetch("` + url + `", map[string]any{"critical": true}) }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
}

func incrementalSourceTransactionEffectsConfig(url string) *config.Config {
	cfg := publishedValueServiceConfig()
	cfg.TemplateSnippets["300-governance"] = config.TemplateSnippet{
		Name: "300-governance", Requires: []string{"routes"},
		Incremental: &config.IncrementalTemplate{
			Source: "routes", Group: "governance",
			Effects: []config.IncrementalEffect{
				config.IncrementalEffectDeriveResource,
				config.IncrementalEffectRecordEvent,
			},
		},
		Template: `{%%
var current = deriveResource(source, item, "metadata.annotations.governed", "yes")
recordEvent(current, "Governed", "derived source")
%%}`,
	}
	cfg.TemplateSnippets["400-status"] = config.TemplateSnippet{
		Name: "400-status", Requires: []string{"routes"},
		Incremental: &config.IncrementalTemplate{
			Source: "routes", Group: "status",
			Effects: []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
		},
		Template: `{%%
statusPatch(item, map[string]any{
  "rendered": map[string]any{"value": item | dig_string("", "spec", "value")},
})
%%}`,
	}
	cfg.TemplateSnippets["500-http"] = config.TemplateSnippet{
		Name: "500-http", Requires: []string{"routes"},
		Incremental: &config.IncrementalTemplate{Source: "routes", Group: "http-effects"},
		Template: `{{ item | dig_string("", "metadata", "name") }}/{{ item | dig_string("", "spec", "value") }}={{ http.Fetch("` + url + `", map[string]any{"critical": true}) }}
`,
	}
	cfg.Files = map[string]config.GeneralFile{
		"published.txt": {Template: `{% var values = incremental_values("published-plans", "hosts") %}general={{ values[0] | dig_string("", "nested", "value") }}
`},
	}
	cfg.SSLCertificates = map[string]config.SSLCertificate{
		"published.pem": {Template: `{% var values = incremental_values("published-plans", "hosts") %}certificate={{ values[0] | dig_string("", "nested", "value") }}
`},
	}
	cfg.HAProxyConfig.Template += `{{ render "300-governance" }}{{ render "400-status" }}{{ render "500-http" }}{%%
var artifactValues = incremental_values("published-plans", "hosts")
var artifactValue = artifactValues[0] | dig_string("", "nested", "value")
var _, caErr = fileRegistry.Register("ca-file", "published-ca.pem", "ca=" + artifactValue + "\n")
if caErr != nil { fail(tostring(caErr)) }
var certificatePath, certificatePathErr = pathResolver.GetPath("published.pem", "cert")
if certificatePathErr != nil { fail(tostring(certificatePathErr)) }
var _, crtListErr = fileRegistry.Register(
  "crt-list", "published.list", tostring(certificatePath) + " " + artifactValue + ".example.test\n",
)
if crtListErr != nil { fail(tostring(crtListErr)) }
%%}`
	return cfg
}

func incrementalSourceTransactionEffectsProvider(tb testing.TB) stores.StoreProvider {
	tb.Helper()
	routes := k8sstore.NewMemoryStore(2)
	for _, resource := range []map[string]any{
		statusPatchResource("a", "alpha"),
		statusPatchResource("b", "beta"),
	} {
		metadata := resource["metadata"].(map[string]any)
		require.NoError(tb, routes.Add(resource, []string{
			metadata["namespace"].(string), metadata["name"].(string),
		}))
	}
	return stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes,
		"claims": k8sstore.NewMemoryStore(2),
		"others": k8sstore.NewMemoryStore(2),
	})
}

type incrementalSourceTransactionArtifactFamilies struct {
	maps         []string
	general      []string
	certificates []string
	cas          []string
	crtLists     []string
}

type incrementalSourceTransactionBackendEffect struct {
	component string
	source    string
	namespace string
	name      string
	digest    string
	encoded   string
}

func assertIncrementalSourceTransactionEffectPhase(
	t *testing.T,
	candidate, control *RenderService,
	candidateResult, controlResult *RenderResult,
	componentNames, backendNames []string,
) incrementalSourceTransactionArtifactFamilies {
	t.Helper()
	assertIncrementalSourceTransactionObservablesEqual(t, controlResult, candidateResult)
	candidateHTTP := authenticatedIncrementalHTTPEffectTuples(t, candidate)
	controlHTTP := authenticatedIncrementalHTTPEffectTuples(t, control)
	require.NotEmpty(t, candidateHTTP)
	assert.Equal(t, controlHTTP, candidateHTTP)
	candidateBackend := authenticatedIncrementalSourceTransactionBackendEffects(t, candidate, backendNames)
	controlBackend := authenticatedIncrementalSourceTransactionBackendEffects(t, control, backendNames)
	require.NotEmpty(t, candidateBackend)
	assert.Equal(t, controlBackend, candidateBackend)
	candidateDerived := authenticatedIncrementalDerivedResources(t, candidate)
	controlDerived := authenticatedIncrementalDerivedResources(t, control)
	require.NotEmpty(t, candidateDerived)
	assert.Equal(t, controlDerived, candidateDerived)
	assert.Equal(
		t,
		incrementalSourceTransactionExecutionCounts(control, componentNames, []string{"a", "b"}),
		incrementalSourceTransactionExecutionCounts(candidate, componentNames, []string{"a", "b"}),
	)
	candidateArtifacts := incrementalSourceTransactionArtifactFamilyBytes(t, candidateResult)
	controlArtifacts := incrementalSourceTransactionArtifactFamilyBytes(t, controlResult)
	assert.Equal(t, controlArtifacts, candidateArtifacts)
	return candidateArtifacts
}

func assertIncrementalSourceTransactionColdOracle(
	t *testing.T,
	cfg *config.Config,
	provider stores.StoreProvider,
	httpComponent *controllerhttpstore.Component,
	transitionTime func(context.Context) (string, error),
	warmService *RenderService,
	warmResult *RenderResult,
	backendNames []string,
) {
	t.Helper()
	oracle, _ := newIncrementalSourceTransactionTestServiceWithHTTPComponent(
		t, cfg, true, warmService.logger, httpComponent,
	)
	oracle.incremental.transitionNow = transitionTime
	defer func() {
		require.NoError(t, oracle.RetireIncrementalCache())
	}()
	coldResult := renderIncrementalSourceTransactionTestResult(t, oracle, provider)
	assertIncrementalSourceTransactionObservablesEqual(t, warmResult, coldResult)
	assert.Equal(
		t,
		incrementalDifferentialEffectSnapshot(t, warmService),
		incrementalDifferentialEffectSnapshot(t, oracle),
	)
	assert.Equal(
		t,
		canonicalIncrementalHTTPEffectTuples(t, warmService),
		canonicalIncrementalHTTPEffectTuples(t, oracle),
	)
	assert.Equal(
		t,
		authenticatedIncrementalSourceTransactionBackendEffects(t, warmService, backendNames),
		authenticatedIncrementalSourceTransactionBackendEffects(t, oracle, backendNames),
	)
	assert.Equal(
		t,
		authenticatedIncrementalDerivedResources(t, warmService),
		authenticatedIncrementalDerivedResources(t, oracle),
	)
	assert.Equal(
		t,
		incrementalSourceTransactionArtifactFamilyBytes(t, warmResult),
		incrementalSourceTransactionArtifactFamilyBytes(t, coldResult),
	)
}

func incrementalSourceTransactionArtifactFamilyBytes(
	t *testing.T,
	result *RenderResult,
) incrementalSourceTransactionArtifactFamilies {
	t.Helper()
	files := requireAuxiliaryFiles(t, result)
	families := incrementalSourceTransactionArtifactFamilies{}
	for _, file := range files.MapFiles {
		families.maps = append(families.maps, file.Content)
	}
	for _, file := range files.GeneralFiles {
		if file.IsCaFile {
			families.cas = append(families.cas, file.Content)
		} else {
			families.general = append(families.general, file.Content)
		}
	}
	for _, file := range files.SSLCertificates {
		families.certificates = append(families.certificates, file.Content)
	}
	for _, file := range files.SSLCaFiles {
		families.cas = append(families.cas, file.Content)
	}
	for _, file := range files.CRTListFiles {
		families.crtLists = append(families.crtLists, file.Content)
	}
	require.NotEmpty(t, families.maps)
	require.NotEmpty(t, families.general)
	require.NotEmpty(t, families.certificates)
	require.NotEmpty(t, families.cas)
	require.NotEmpty(t, families.crtLists)
	return families
}

func assertIncrementalSourceTransactionArtifactFamiliesChanged(
	t *testing.T,
	before, after *incrementalSourceTransactionArtifactFamilies,
) {
	t.Helper()
	assert.NotEqual(t, before.maps, after.maps, "map bytes")
	assert.NotEqual(t, before.general, after.general, "general-file bytes")
	assert.NotEqual(t, before.certificates, after.certificates, "certificate bytes")
	assert.NotEqual(t, before.cas, after.cas, "CA bytes")
	assert.NotEqual(t, before.crtLists, after.crtLists, "CRT-list bytes")
}

func authenticatedIncrementalSourceTransactionBackendEffects(
	t *testing.T,
	service *RenderService,
	names []string,
) []incrementalSourceTransactionBackendEffect {
	t.Helper()
	service.incremental.mu.Lock()
	snapshot := service.incremental.snapshot
	service.incremental.mu.Unlock()
	require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))
	component := service.incremental.components["200-backends"]
	index := snapshot.groupIndexes[component.group]
	require.NotNil(t, index)
	require.NoError(t, index.validateAuthentication())
	effects := make([]incrementalSourceTransactionBackendEffect, 0, len(names))
	for _, name := range names {
		query := componentQueryKey(&component, "routes", "default", name)
		root, found := snapshot.results.Root().Get(resultKey(&component, "routes", "default", name))
		require.True(t, found, name)
		require.NoError(t, root.ValidateAuthentication())
		require.NoError(t, service.incremental.graph.ValidateCommittedExactValue(query, root))
		graphRoot := requireGraphExactValue(t, service.incremental.graph, query)
		same, err := root.SameRoot(graphRoot)
		require.NoError(t, err)
		require.True(t, same, name)
		encoded, err := root.String()
		require.NoError(t, err)
		identity := incrementalGroupInstanceID{
			component: component.name, source: "routes", namespace: "default", name: name,
		}
		indexed, found := index.instances.Root().Get(incrementalGroupInstanceKey(identity))
		require.True(t, found, name)
		require.Equal(t, encoded, indexed.encodedResult)
		result, err := decodeExactComponentResult(root)
		require.NoError(t, err)
		require.NoError(t, validateIncrementalInstanceResult(&result))
		require.NotEmpty(t, result.BackendPlan)
		require.NotEmpty(t, result.BackendPlanOutput)
		require.NotEmpty(t, result.BackendPlanDigest)
		effects = append(effects, incrementalSourceTransactionBackendEffect{
			component: component.name,
			source:    "routes",
			namespace: "default",
			name:      name,
			digest:    result.BackendPlanDigest,
			encoded:   encoded,
		})
	}
	return effects
}

func authenticatedIncrementalDerivedResources(
	t *testing.T,
	service *RenderService,
) []incrementalDerivedResource {
	t.Helper()
	service.incremental.mu.Lock()
	snapshot := service.incremental.snapshot
	service.incremental.mu.Unlock()
	require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))
	resources := make([]incrementalDerivedResource, 0, snapshot.derived.Len())
	snapshot.derived.Root().Walk(func(_ []byte, resource incrementalDerivedResource) bool {
		resources = append(resources, resource)
		return false
	})
	return resources
}

func incrementalSourceTransactionExecutionCounts(
	service *RenderService,
	componentNames, names []string,
) map[string]uint64 {
	counts := make(map[string]uint64, len(componentNames)*len(names))
	for _, componentName := range componentNames {
		component := service.incremental.components[componentName]
		for _, name := range names {
			query := componentQueryKey(&component, "routes", "default", name)
			counts[componentName+"/"+name] = service.incremental.graph.Counters(query).Executions
		}
	}
	return counts
}

func assertIncrementalSourceTransactionChangedLocality(
	t *testing.T,
	before, after map[string]uint64,
	componentNames []string,
) {
	t.Helper()
	for _, componentName := range componentNames {
		assert.Equal(t, before[componentName+"/a"]+1, after[componentName+"/a"], componentName+"/a")
		assert.Equal(t, before[componentName+"/b"], after[componentName+"/b"], componentName+"/b")
	}
}

func renderIncrementalSourceTransactionTestResult(
	t *testing.T,
	service *RenderService,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	return result
}

func assertIncrementalSourceTransactionObservablesEqual(t *testing.T, want, got *RenderResult) {
	t.Helper()
	assert.Equal(t, want.HAProxyConfig, got.HAProxyConfig)
	assert.Equal(t, want.ContentChecksum, got.ContentChecksum)
	assert.Equal(t, requireAuxiliaryFiles(t, want), requireAuxiliaryFiles(t, got))
	assert.Equal(t, requireRenderPlan(t, want), requireRenderPlan(t, got))
	assert.Equal(t, want.PlanID, got.PlanID)
	assert.Equal(t, materializedStatusPatches(t, want), materializedStatusPatches(t, got))
	assert.Equal(t, requireRenderEvents(t, want), requireRenderEvents(t, got))
	assert.Equal(t, requireRenderedResources(t, want), requireRenderedResources(t, got))
	assert.Equal(t, want.AuxFileCount, got.AuxFileCount)
}
