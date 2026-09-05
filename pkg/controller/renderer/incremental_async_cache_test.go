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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type incrementalCacheBuildObserverProbe struct {
	started        chan IncrementalCacheBuildIdentity
	completed      chan incrementalCacheBuildCompletion
	gate           <-chan struct{}
	panicStarted   bool
	panicCompleted bool
}

type incrementalCacheBuildCompletion struct {
	identity IncrementalCacheBuildIdentity
	err      error
}

func newIncrementalCacheBuildObserverProbe() *incrementalCacheBuildObserverProbe {
	return &incrementalCacheBuildObserverProbe{
		started:   make(chan IncrementalCacheBuildIdentity, 4),
		completed: make(chan incrementalCacheBuildCompletion, 4),
	}
}

func (o *incrementalCacheBuildObserverProbe) IncrementalCacheBuildStarted(
	ctx context.Context,
	identity IncrementalCacheBuildIdentity,
) {
	o.started <- identity
	if o.panicStarted {
		panic("cache observer start poison")
	}
	if o.gate != nil {
		select {
		case <-o.gate:
		case <-ctx.Done():
		}
	}
}

func (o *incrementalCacheBuildObserverProbe) IncrementalCacheBuildCompleted(
	identity IncrementalCacheBuildIdentity,
	err error,
) {
	o.completed <- incrementalCacheBuildCompletion{identity: identity, err: err}
	if o.panicCompleted {
		panic("cache observer completion poison")
	}
}

func TestColdCacheBuildObserverPairsExactLifecycle(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	gate := make(chan struct{})
	observer := newIncrementalCacheBuildObserverProbe()
	observer.gate = gate
	installIncrementalCacheBuildObserver(fixture, observer)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)

	result := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	started := receiveIncrementalCacheBuildStart(t, observer)
	require.NoError(t, started.ValidateAuthentication())
	generation, err := started.Generation()
	require.NoError(t, err)
	fixture.service.incremental.mu.Lock()
	assert.Equal(t, fixture.service.incremental.cachePendingGeneration, generation)
	fixture.service.incremental.mu.Unlock()
	select {
	case completion := <-observer.completed:
		t.Fatalf("cache build completed before release: %+v", completion)
	default:
	}
	assert.Equal(t, "a=first\nb=stable\n", result.HAProxyConfig)
	assert.Zero(t, fixture.service.incremental.graph.Generation())

	close(gate)
	waitForIncrementalCache(t, fixture.service)
	completion := receiveIncrementalCacheBuildCompletion(t, observer)
	require.NoError(t, completion.err)
	assert.True(t, started.Same(completion.identity))
	select {
	case duplicate := <-observer.completed:
		t.Fatalf("cache build completed more than once: %+v", duplicate)
	default:
	}
}

func TestColdCacheBuildObserverReportsSupersededAndSuccessExactlyOnce(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var blocked atomic.Bool
	observer := newIncrementalCacheBuildObserverProbe()
	installIncrementalCacheBuildObserver(fixture, observer)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		beforePrepare: func(context.Context, uint64) {
			if blocked.CompareAndSwap(false, true) {
				close(entered)
				<-gate
			}
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	first := receiveIncrementalCacheBuildStart(t, observer)
	waitForSignal(t, entered)
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlA}),
		[]string{"default", "a"},
	))
	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	close(gate)
	latest := receiveIncrementalCacheBuildStart(t, observer)
	waitForIncrementalCache(t, fixture.service)

	completions := []incrementalCacheBuildCompletion{
		receiveIncrementalCacheBuildCompletion(t, observer),
		receiveIncrementalCacheBuildCompletion(t, observer),
	}
	var firstErr, latestErr error
	for _, completion := range completions {
		switch {
		case first.Same(completion.identity):
			firstErr = completion.err
		case latest.Same(completion.identity):
			latestErr = completion.err
		default:
			t.Fatal("completion belongs to no started cache build")
		}
	}
	require.Error(t, firstErr)
	require.NoError(t, latestErr)
	firstGeneration, err := first.Generation()
	require.NoError(t, err)
	latestGeneration, err := latest.Generation()
	require.NoError(t, err)
	assert.Greater(t, latestGeneration, firstGeneration)
	select {
	case duplicate := <-observer.completed:
		t.Fatalf("cache build completed more than once: %+v", duplicate)
	default:
	}
}

func TestColdCacheBuildObserverPanicsAreIsolated(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	observer := newIncrementalCacheBuildObserverProbe()
	observer.panicStarted = true
	observer.panicCompleted = true
	installIncrementalCacheBuildObserver(fixture, observer)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	started := receiveIncrementalCacheBuildStart(t, observer)
	waitForIncrementalCache(t, fixture.service)
	completion := receiveIncrementalCacheBuildCompletion(t, observer)
	require.NoError(t, completion.err)
	assert.True(t, started.Same(completion.identity))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())
}

func installIncrementalCacheBuildObserver(
	fixture *incrementalHTTPTestFixture,
	observer IncrementalCacheBuildObserver,
) {
	service := fixture.service
	fixture.service = NewRenderService(&RenderServiceConfig{
		Engine:                        service.engine,
		Config:                        service.config,
		Logger:                        service.logger,
		Capabilities:                  service.capabilities,
		HAProxyPodStore:               service.haproxyPodStore,
		HTTPStoreComponent:            service.httpStoreComponent,
		CurrentAuxFilesProvider:       service.currentAuxFilesProvider,
		TypedResourceTypes:            service.typedResourceTypes,
		IncrementalCacheBuildObserver: observer,
	})
}

func receiveIncrementalCacheBuildStart(
	t *testing.T,
	observer *incrementalCacheBuildObserverProbe,
) IncrementalCacheBuildIdentity {
	t.Helper()
	select {
	case identity := <-observer.started:
		return identity
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cache build start")
		return IncrementalCacheBuildIdentity{}
	}
}

func receiveIncrementalCacheBuildCompletion(
	t *testing.T,
	observer *incrementalCacheBuildObserverProbe,
) incrementalCacheBuildCompletion {
	t.Helper()
	select {
	case completion := <-observer.completed:
		return completion
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cache build completion")
		return incrementalCacheBuildCompletion{}
	}
}

func TestColdCacheBuildPublishesAfterAuthoritativeResult(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		afterPrepare: func(context.Context, uint64) {
			close(entered)
			<-release
		},
	})

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	var publications atomic.Int32
	result.InputTransaction = stageRenderPublication(result.InputTransaction, func() {
		publications.Add(1)
	})
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "a=first\nb=stable\n", result.HAProxyConfig)
	assert.Equal(t, int32(1), publications.Load())
	waitForSignal(t, entered)
	assert.True(t, incrementalCachePending(fixture.service))
	assert.Zero(t, fixture.service.incremental.graph.Generation())
	fixture.service.planMu.Lock()
	assert.Same(t, result.CycleSnapshot, fixture.service.lastCycleSnapshot)
	fixture.service.planMu.Unlock()

	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCache(t, fixture.service)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())
	assert.Equal(t, int32(1), publications.Load())
}

func renderStaleColdCacheBuild(
	t *testing.T,
	fixture *incrementalHTTPTestFixture,
	entered chan struct{},
) (ready *incrementalCacheReadySignal, publications, aborts *atomic.Int32) {
	t.Helper()
	stale, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	staleTransaction, ok := stale.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	publications = &atomic.Int32{}
	aborts = &atomic.Int32{}
	staleTransaction.stagePublicationFinalizer(func() {
		require.True(t, staleTransaction.incremental.deferCachePublication(
			func() { publications.Add(1) },
			func() { aborts.Add(1) },
		))
	}, nil)
	require.NoError(t, staleTransaction.Commit(t.Context()))
	assert.Equal(t, "a=first\nb=stable\n", stale.HAProxyConfig)
	waitForSignal(t, entered)
	fixture.service.incremental.mu.Lock()
	ready = fixture.service.incremental.cacheReadySignal
	fixture.service.incremental.mu.Unlock()
	require.NotNil(t, ready)
	return ready, publications, aborts
}

func newColdCacheOracleService(t *testing.T, service *RenderService) *RenderService {
	t.Helper()
	oracle := NewRenderService(&RenderServiceConfig{
		Engine:                  service.engine,
		Config:                  service.config,
		Logger:                  service.logger,
		Capabilities:            service.capabilities,
		HAProxyPodStore:         service.haproxyPodStore,
		HTTPStoreComponent:      service.httpStoreComponent,
		CurrentAuxFilesProvider: service.currentAuxFilesProvider,
		TypedResourceTypes:      service.typedResourceTypes,
	})
	t.Cleanup(func() { require.NoError(t, oracle.RetireIncrementalCache()) })
	return oracle
}

func identityFreeIncrementalHTTPEffectTuples(
	t *testing.T,
	service *RenderService,
) []authenticatedIncrementalHTTPEffectTuple {
	t.Helper()
	effects := authenticatedIncrementalHTTPEffectTuples(t, service)
	for index := range effects {
		assert.NotZero(t, effects[index].inputID)
		effects[index].inputID = 0
	}
	return effects
}

func TestColdCacheBuildImmediateSuccessorMatchesColdOracleBeforeReadiness(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	entered := make(chan struct{})
	release := make(chan struct{})
	var blocked atomic.Bool
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		afterPrepare: func(context.Context, uint64) {
			if blocked.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		},
	})

	staleReady, staleCallbackPublications, staleCallbackAborts := renderStaleColdCacheBuild(t, fixture, entered)
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))

	successor, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- successor.InputTransaction.Commit(t.Context())
	}()
	select {
	case err := <-commitDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("successor render waited for the stale cache builder")
	}
	assert.Equal(t, "a=stable\nb=stable\n", successor.HAProxyConfig)
	assert.True(t, incrementalCachePending(fixture.service))
	assert.Zero(t, fixture.service.incremental.graph.Generation())
	fixture.service.incremental.mu.Lock()
	successorReady := fixture.service.incremental.cacheReadySignal
	fixture.service.incremental.mu.Unlock()
	require.NotNil(t, successorReady)
	assert.NotSame(t, staleReady, successorReady)
	select {
	case <-staleReady.done:
		assert.ErrorIs(t, staleReady.result(), errIncrementalCacheSuperseded)
	case <-time.After(2 * time.Second):
		t.Fatal("superseded cache readiness was not completed")
	}

	oracle := newColdCacheOracleService(t, fixture.service)
	oracleResult := renderAndCommitWithoutCacheWait(t, oracle, fixture.provider)
	waitForIncrementalCache(t, oracle)
	assertRenderResultObservablesEqual(t, oracleResult, successor)
	oracleHTTPEffects := identityFreeIncrementalHTTPEffectTuples(t, oracle)

	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCache(t, fixture.service)
	require.NoError(t, successorReady.result())
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())
	assertRenderResultObservablesEqual(t, oracleResult, successor)
	successorHTTPEffects := identityFreeIncrementalHTTPEffectTuples(t, fixture.service)
	assert.Equal(t, oracleHTTPEffects, successorHTTPEffects)
	assert.Zero(t, staleCallbackPublications.Load())
	assert.Equal(t, int32(1), staleCallbackAborts.Load())
	fixture.service.planMu.Lock()
	assert.Same(t, successor.CycleSnapshot, fixture.service.lastCycleSnapshot)
	fixture.service.planMu.Unlock()
}

func TestColdCacheBuildPanicPublishesNoPartialCache(t *testing.T) {
	for _, stage := range []string{"before prepare", "after prepare"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			primeIncrementalHTTPFixtures(fixture)
			bootstrapIncrementalHTTPOutputOnly(t, fixture)
			hooks := incrementalCacheBuilderHooks{}
			panicBuild := func(context.Context, uint64) { panic("poison cache builder") }
			if stage == "before prepare" {
				hooks.beforePrepare = panicBuild
			} else {
				hooks.afterPrepare = panicBuild
			}
			setIncrementalCacheBuilderHooks(t, fixture.service, hooks)
			result := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
			assert.Equal(t, "a=first\nb=stable\n", result.HAProxyConfig)
			ready := requireIncrementalCacheReadySignal(t, fixture.service)
			waitForIncrementalCacheSignal(t, ready)
			require.ErrorContains(t, ready.result(), "poison cache builder")
			assert.True(t, incrementalCachePending(fixture.service))
			assert.Zero(t, fixture.service.incremental.graph.Generation())

			setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{})
			require.NoError(t, fixture.provider.GetStore("routes").Update(
				incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
				[]string{"default", "a"},
			))
			retry := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
			assert.Equal(t, "a=stable\nb=stable\n", retry.HAProxyConfig)
			waitForIncrementalCache(t, fixture.service)
			assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())
			assert.Contains(t, fixture.httpComponent.GetStore().EvictUnused(), fixture.urlA)
		})
	}
}

func TestColdCacheSynchronousPreparationPanicAborts(t *testing.T) {
	for _, stage := range []string{"HTTP prepared", "lease ownership prepared"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			primeIncrementalHTTPFixtures(fixture)
			bootstrapIncrementalHTTPOutputOnly(t, fixture)
			base := fixture.service.incremental.snapshot
			var publications atomic.Int32
			panicPrepare := func(context.Context, uint64) { panic("poison synchronous preparation") }
			hooks := incrementalCacheBuilderHooks{}
			if stage == "HTTP prepared" {
				hooks.afterHTTPPrepare = panicPrepare
			} else {
				hooks.afterDependencyPrepare = panicPrepare
			}
			setIncrementalCacheBuilderHooks(t, fixture.service, hooks)

			result, err := fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
			require.NoError(t, err)
			result.InputTransaction = stageRenderPublication(result.InputTransaction, func() {
				publications.Add(1)
			})
			err = result.InputTransaction.Commit(t.Context())
			require.ErrorContains(t, err, "poison synchronous preparation")
			assert.Zero(t, publications.Load())
			assert.Same(t, base, fixture.service.incremental.snapshot)
			assert.False(t, incrementalCachePending(fixture.service))
			assert.Zero(t, fixture.service.incremental.graph.Generation())

			setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{})
			retry := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
			assert.Equal(t, "a=first\nb=stable\n", retry.HAProxyConfig)
			waitForIncrementalCache(t, fixture.service)
		})
	}
}

func TestColdCachePublicationPreflightPanicPublishesNothing(t *testing.T) {
	stages := []incrementalColdPublicationStage{
		incrementalColdPublicationHTTP,
		incrementalColdPublicationOwnership,
		incrementalColdPublicationState,
		incrementalColdPublicationOutput,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			primeIncrementalHTTPFixtures(fixture)
			bootstrapIncrementalHTTPOutputOnly(t, fixture)
			base := fixture.service.incremental.snapshot
			var publications atomic.Int32
			var aborts atomic.Int32
			setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
				beforeColdPublication: func(_ context.Context, _ uint64, current incrementalColdPublicationStage) {
					if current == stage {
						panic("poison cold publication preflight")
					}
				},
			})

			result, err := fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
			require.NoError(t, err)
			transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
			require.True(t, ok)
			transaction.stagePublicationFinalizer(
				func() { publications.Add(1) },
				func() { aborts.Add(1) },
			)
			err = transaction.Commit(t.Context())
			require.ErrorContains(t, err, "poison cold publication preflight")
			assert.Zero(t, publications.Load())
			assert.Equal(t, int32(1), aborts.Load())
			assert.Same(t, base, fixture.service.incremental.snapshot)
			assert.False(t, incrementalCachePending(fixture.service))
			assert.Zero(t, fixture.service.incremental.graph.Generation())
			fixture.service.incremental.mu.Lock()
			assert.NoError(t, fixture.service.incremental.cachePublicationErr)
			fixture.service.incremental.mu.Unlock()

			setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{})
			retry := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
			assert.Equal(t, "a=first\nb=stable\n", retry.HAProxyConfig)
			waitForIncrementalCache(t, fixture.service)
		})
	}
}

func TestColdCacheUnexpectedMutatorPanicPoisonsAndUnlocksState(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	state := fixture.service.incremental
	state.mu.Lock()
	base := state.snapshot
	state.mu.Unlock()
	state.cache.mu.Lock()
	generation := state.cache.desiredGeneration + 1
	state.cache.desiredGeneration = generation
	state.cache.mu.Unlock()
	session := &incrementalRenderSession{state: state}
	build := newIncrementalCacheBuild(
		t.Context(), &state.cache, session, generation, httpstore.ActiveLeaseToken{}, nil, nil,
	)
	var aborts atomic.Int32

	published, err := state.cache.publishCold(
		t.Context(),
		state,
		base,
		build,
		func() { panic("poison concrete cold publication") },
		func() { aborts.Add(1) },
	)

	assert.False(t, published)
	require.ErrorContains(t, err, "poison concrete cold publication")
	assert.Equal(t, int32(1), aborts.Load())
	require.True(t, state.mu.TryLock(), "renderer state mutex remained locked")
	state.mu.Unlock()
	require.True(t, state.cache.mu.TryLock(), "cache builder mutex remained locked")
	state.cache.mu.Unlock()
	_, renderErr := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, renderErr, "incremental render cache publication is poisoned")
	build.cancel(err)
	build.releasePublication()
}

func TestColdCacheRequiredPublicationPanicPublishesNothing(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	base := fixture.service.incremental.snapshot
	fixture.service.planMu.Lock()
	previousCycle := fixture.service.lastCycleSnapshot
	previousGeneration := fixture.service.publishedOutputGeneration
	fixture.service.planMu.Unlock()
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	var mutated atomic.Int32
	var completed atomic.Int32
	var completedAborts atomic.Int32
	transaction.stagePublicationFinalizer(func() {
		mutated.Store(1)
		panic("poison required publication")
	}, func() { mutated.Store(0) })
	transaction.stagePublicationFinalizer(
		func() { completed.Add(1) },
		func() { completedAborts.Add(1) },
	)

	require.NotPanics(t, func() {
		err = transaction.Commit(t.Context())
	})
	require.ErrorContains(t, err, "poison required publication")
	assert.Zero(t, mutated.Load())
	assert.Zero(t, completed.Load())
	assert.Equal(t, int32(1), completedAborts.Load())
	assert.False(t, incrementalCachePending(fixture.service))
	assert.Zero(t, fixture.service.incremental.graph.Generation())
	assert.Same(t, base, fixture.service.incremental.snapshot)
	fixture.service.incremental.mu.Lock()
	assert.NoError(t, fixture.service.incremental.cachePublicationErr)
	fixture.service.incremental.mu.Unlock()
	fixture.service.planMu.Lock()
	assert.Same(t, previousCycle, fixture.service.lastCycleSnapshot)
	assert.Equal(t, previousGeneration, fixture.service.publishedOutputGeneration)
	fixture.service.planMu.Unlock()

	retry := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	assert.Equal(t, result.HAProxyConfig, retry.HAProxyConfig)
	waitForIncrementalCache(t, fixture.service)
}

func TestWarmCacheRequiredPublicationPanicRollsBackCallerAndGraph(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	waitForIncrementalCache(t, fixture.service)
	base := fixture.service.incremental.snapshot
	generation := fixture.service.incremental.graph.Generation()
	fixture.service.planMu.Lock()
	previousCycle := fixture.service.lastCycleSnapshot
	previousOutputGeneration := fixture.service.publishedOutputGeneration
	fixture.service.planMu.Unlock()
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	var mutated atomic.Int32
	transaction.stagePublicationFinalizer(func() {
		mutated.Store(1)
		panic("poison warm required publication")
	}, func() { mutated.Store(0) })

	require.NotPanics(t, func() { err = transaction.Commit(t.Context()) })
	require.ErrorContains(t, err, "poison warm required publication")
	assert.Zero(t, mutated.Load())
	assert.Equal(t, generation, fixture.service.incremental.graph.Generation())
	assert.Same(t, base, fixture.service.incremental.snapshot)
	fixture.service.planMu.Lock()
	assert.Same(t, previousCycle, fixture.service.lastCycleSnapshot)
	assert.Equal(t, previousOutputGeneration, fixture.service.publishedOutputGeneration)
	fixture.service.planMu.Unlock()
	require.True(t, fixture.service.planMu.TryLock(), "required publication stranded the service lock")
	fixture.service.planMu.Unlock()

	retry := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	assert.Equal(t, "a=stable\nb=stable\n", retry.HAProxyConfig)
}

func TestColdCacheDeferredCallbackPanicDiscardsOptionalPublication(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	observer := newIncrementalCacheBuildObserverProbe()
	installIncrementalCacheBuildObserver(fixture, observer)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	session := transaction.incremental
	var deferred atomic.Bool
	var aborted atomic.Bool
	transaction.stagePublicationFinalizer(func() {
		deferred.Store(session.deferCachePublication(
			func() { panic("poison deferred cache callback") },
			func() { aborted.Store(true) },
		))
	}, nil)

	require.NoError(t, transaction.Commit(t.Context()))
	assert.True(t, deferred.Load())
	settleIncrementalCacheBuild(t, fixture.service)
	started := receiveIncrementalCacheBuildStart(t, observer)
	completion := receiveIncrementalCacheBuildCompletion(t, observer)
	assert.True(t, started.Same(completion.identity))
	require.NoError(t, completion.err)
	assert.True(t, aborted.Load())
	assert.False(t, incrementalCachePending(fixture.service))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())
	fixture.service.planMu.Lock()
	assert.Nil(t, fixture.service.exactCycleCandidate)
	fixture.service.planMu.Unlock()

	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	retry := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	assert.Equal(t, "a=stable\nb=stable\n", retry.HAProxyConfig)
	assert.False(t, incrementalCachePending(fixture.service))
}

func TestRetireIncrementalCacheCancelsAndJoinsBuilder(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	entered := make(chan struct{})
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		beforeRendererPublish: func(ctx context.Context, _ uint64) {
			close(entered)
			<-ctx.Done()
		},
	})
	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	waitForSignal(t, entered)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)

	done := make(chan error, 1)
	go func() { done <- fixture.service.RetireIncrementalCache() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("retiring incremental cache did not join the cache builder")
	}
	waitForIncrementalCacheSignal(t, ready)
	require.ErrorContains(t, ready.result(), "incremental cache build was superseded")
	fixture.service.incremental.cache.mu.Lock()
	assert.False(t, fixture.service.incremental.cache.running)
	fixture.service.incremental.cache.mu.Unlock()
	_, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	assert.ErrorContains(t, err, "incremental render cache was retired")
}

func TestColdCachePendingReadinessFailsClosed(t *testing.T) {
	tests := map[string]func(*incrementalRenderState){
		"nil signal": func(state *incrementalRenderState) {
			state.cacheReadySignal = nil
		},
		"forged signal": func(state *incrementalRenderState) {
			state.cacheReadySignal = &incrementalCacheReadySignal{
				authority:  state.cacheReadyAuthority,
				generation: state.cachePendingGeneration,
				done:       make(chan struct{}),
			}
		},
		"substituted signal": func(state *incrementalRenderState) {
			foreignState := &incrementalRenderState{}
			foreignAuthority := newIncrementalCacheReadyAuthority(foreignState)
			foreignState.cacheReadyAuthority = foreignAuthority
			state.cacheReadySignal = newIncrementalCacheReadySignal(
				foreignAuthority,
				state.cachePendingGeneration,
			)
		},
		"zero generation": func(state *incrementalRenderState) {
			state.cachePendingGeneration = 0
		},
		"stale generation": func(state *incrementalRenderState) {
			state.cachePendingGeneration++
		},
		"completed success": func(state *incrementalRenderState) {
			state.cacheReadySignal.complete(nil)
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			primeIncrementalHTTPFixtures(fixture)
			observer := newIncrementalCacheBuildObserverProbe()
			installIncrementalCacheBuildObserver(fixture, observer)
			bootstrapIncrementalHTTPOutputOnly(t, fixture)
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				fixture.service.incremental.cache.shutdown()
			})
			setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
				afterPrepare: func(context.Context, uint64) {
					close(entered)
					<-release
				},
			})
			_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
			waitForSignal(t, entered)

			state := fixture.service.incremental
			generation := state.graph.Generation()
			state.mu.Lock()
			base := state.snapshot
			poison(state)
			state.mu.Unlock()
			_, err := fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
			require.ErrorContains(t, err, "incremental cache readiness")
			releaseOnce.Do(func() { close(release) })
			completion := receiveIncrementalCacheBuildCompletion(t, observer)
			require.Error(t, completion.err)
			state.mu.Lock()
			assert.Same(t, base, state.snapshot)
			state.mu.Unlock()
			assert.Equal(t, generation, state.graph.Generation())
		})
	}
}

func TestColdCacheReadyStateRejectsLiveSignal(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	// Hold the builder so the pending signal is still there to read. Unheld it
	// races the assertion, and a loaded machine loses.
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		afterPrepare: func(context.Context, uint64) {
			close(entered)
			<-release
		},
	})
	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	waitForSignal(t, entered)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCache(t, fixture.service)

	state := fixture.service.incremental
	state.mu.Lock()
	state.cacheReadySignal = newIncrementalCacheReadySignal(
		state.cacheReadyAuthority,
		ready.generation+1,
	)
	state.mu.Unlock()
	_, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "incremental cache readiness retains a signal after completion")
}

func renderAndCommitWithoutCacheWait(
	t *testing.T,
	service *RenderService,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	return result
}

func primeIncrementalHTTPFixtures(fixture *incrementalHTTPTestFixture) {
	fixture.httpComponent.GetStore().LoadFixture(fixture.urlA, "first")
	fixture.httpComponent.GetStore().LoadFixture(fixture.urlB, "stable")
}

func bootstrapIncrementalHTTPOutputOnly(t *testing.T, fixture *incrementalHTTPTestFixture) {
	t.Helper()
	result := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	assert.Equal(t, "a=first\nb=stable\n", result.HAProxyConfig)
	fixture.service.incremental.mu.Lock()
	assert.False(t, fixture.service.incremental.cachePending)
	assert.Zero(t, fixture.service.incremental.cachePendingGeneration)
	assert.Nil(t, fixture.service.incremental.cacheReadySignal)
	fixture.service.incremental.mu.Unlock()
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

func setIncrementalCacheBuilderHooks(
	t *testing.T,
	service *RenderService,
	hooks incrementalCacheBuilderHooks,
) {
	t.Helper()
	service.incremental.cache.mu.Lock()
	service.incremental.cache.hooks = hooks
	service.incremental.cache.mu.Unlock()
}

func incrementalCachePending(service *RenderService) bool {
	service.incremental.mu.Lock()
	defer service.incremental.mu.Unlock()
	return service.incremental.cachePending
}

// settleIncrementalCacheBuild waits out an in-flight cache build, and returns
// immediately if one already finished.
//
// The builder clears cachePending and the signal when it completes, so a test
// that only wants to wait for it cannot require the signal to still be there:
// nothing holds the builder between Commit and the check, and on a loaded
// machine it wins that race. Where the live signal is the subject rather than
// the scaffolding, use requireIncrementalCacheReadySignal instead — an absent
// signal is exactly what those tests are asserting about.
func settleIncrementalCacheBuild(t *testing.T, service *RenderService) {
	t.Helper()
	service.incremental.mu.Lock()
	pending := service.incremental.cachePending
	ready := service.incremental.cacheReadySignal
	service.incremental.mu.Unlock()
	if !pending || ready == nil {
		return
	}
	waitForIncrementalCacheSignal(t, ready)
	require.NoError(t, ready.result())
}

func requireIncrementalCacheReadySignal(
	t *testing.T,
	service *RenderService,
) *incrementalCacheReadySignal {
	t.Helper()
	service.incremental.mu.Lock()
	defer service.incremental.mu.Unlock()
	require.True(t, service.incremental.cachePending)
	require.NotNil(t, service.incremental.cacheReadySignal)
	return service.incremental.cacheReadySignal
}

func waitForIncrementalCacheSignal(t *testing.T, ready *incrementalCacheReadySignal) {
	t.Helper()
	require.NotNil(t, ready)
	select {
	case <-ready.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for incremental cache readiness")
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cache builder")
	}
}
