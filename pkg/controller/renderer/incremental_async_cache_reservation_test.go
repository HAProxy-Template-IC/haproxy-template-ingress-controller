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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

func TestIncrementalCacheOutputReservationRejectsPoison(t *testing.T) {
	tests := map[string]func(
		*incrementalRenderState,
		*RenderService,
		*renderOutputReservation,
	) *renderOutputReservation{
		"copied": func(
			_ *incrementalRenderState,
			_ *RenderService,
			reservation *renderOutputReservation,
		) *renderOutputReservation {
			copied := *reservation
			return &copied
		},
		"foreign": func(
			_ *incrementalRenderState,
			_ *RenderService,
			_ *renderOutputReservation,
		) *renderOutputReservation {
			foreignState := &incrementalRenderState{}
			_, foreign := committedCacheOutputReservation(foreignState, 1)
			return foreign
		},
		"aborted": func(
			_ *incrementalRenderState,
			_ *RenderService,
			reservation *renderOutputReservation,
		) *renderOutputReservation {
			reservation.state.Store(uint32(renderOutputReservationAborted))
			return reservation
		},
		"stale": func(
			_ *incrementalRenderState,
			service *RenderService,
			reservation *renderOutputReservation,
		) *renderOutputReservation {
			service.planMu.Lock()
			service.nextOutputGeneration++
			newer := newRenderOutputReservation(service, service.nextOutputGeneration)
			newer.state.Store(uint32(renderOutputReservationCommitted))
			service.publishedOutputGeneration = newer.generation
			service.committedOutputReservation.Store(newer)
			service.planMu.Unlock()
			return reservation
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			state := &incrementalRenderState{}
			service, reservation := committedCacheOutputReservation(state, 1)
			candidate := poison(state, service, reservation)
			build := &incrementalCacheBuild{
				session:           &incrementalRenderSession{state: state},
				generation:        1,
				reservationSource: func() (*renderOutputReservation, error) { return candidate, nil },
			}

			_, err := build.resolveOutputReservation()
			require.Error(t, err)
		})
	}
}

func TestIncrementalCacheOutputReservationGetterRequiresTerminalSuccess(t *testing.T) {
	state := &incrementalRenderState{}
	_, reservation := committedCacheOutputReservation(state, 1)
	publications := &stagedRenderPublications{reservation: reservation}

	for _, publicationState := range []renderPublicationState{
		renderPublicationsOpen,
		renderPublicationsFinalizing,
		renderPublicationsCommitted,
		renderPublicationsFailed,
	} {
		publications.state = publicationState
		_, err := publications.committedOutputReservation()
		require.Error(t, err)
	}

	publications.state = renderPublicationsSucceeded
	got, err := publications.committedOutputReservation()
	require.NoError(t, err)
	assert.Same(t, reservation, got)
}

func TestColdCacheReservedSuccessorDoesNotCancelBeforeStart(t *testing.T) {
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
		beforeOutputReservation: func(context.Context, uint64) {
			close(entered)
			<-release
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	waitForSignal(t, entered)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	reserveDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.reserveOutputGeneration()
		reserveDone <- err
	}()
	select {
	case err := <-reserveDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("successor reservation waited for cache preparation")
	}
	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCacheSignal(t, ready)
	require.NoError(t, ready.result())
	started := receiveIncrementalCacheBuildStart(t, observer)
	completed := receiveIncrementalCacheBuildCompletion(t, observer)
	assert.True(t, started.Same(completed.identity))
	require.NoError(t, completed.err)
	assert.NotZero(t, fixture.service.incremental.graph.Generation())
}

func TestColdCacheReservedSuccessorDoesNotRejectTerminalPublication(t *testing.T) {
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
		beforeRendererPublish: func(context.Context, uint64) {
			close(entered)
			<-release
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	started := receiveIncrementalCacheBuildStart(t, observer)
	waitForSignal(t, entered)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	reserveDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.reserveOutputGeneration()
		reserveDone <- err
	}()
	select {
	case err := <-reserveDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("successor reservation waited for cache construction")
	}
	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCacheSignal(t, ready)
	require.NoError(t, ready.result())
	completion := receiveIncrementalCacheBuildCompletion(t, observer)
	assert.True(t, started.Same(completion.identity))
	require.NoError(t, completion.err)
	assert.NotZero(t, fixture.service.incremental.graph.Generation())
}

func TestColdCacheCommittedSuccessorCancelsBeforeStart(t *testing.T) {
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
		beforeOutputReservation: func(context.Context, uint64) {
			close(entered)
			<-release
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	waitForSignal(t, entered)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	commitCurrentCycleAsSuccessor(t, fixture.service)
	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCacheSignal(t, ready)
	require.Error(t, ready.result())
	select {
	case started := <-observer.started:
		t.Fatalf("stale cache build started: %+v", started)
	default:
	}
	select {
	case completed := <-observer.completed:
		t.Fatalf("cache observer completed a build that never started: %+v", completed)
	default:
	}
}

func TestColdCacheCommittedSuccessorRejectsTerminalPublication(t *testing.T) {
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
		beforeRendererPublish: func(context.Context, uint64) {
			close(entered)
			<-release
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	started := receiveIncrementalCacheBuildStart(t, observer)
	waitForSignal(t, entered)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	commitCurrentCycleAsSuccessor(t, fixture.service)
	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCacheSignal(t, ready)
	require.Error(t, ready.result())
	completion := receiveIncrementalCacheBuildCompletion(t, observer)
	assert.True(t, started.Same(completion.identity))
	require.Error(t, completion.err)
}

func committedCacheOutputReservation(
	state *incrementalRenderState,
	generation uint64,
) (*RenderService, *renderOutputReservation) {
	service := &RenderService{
		incremental:               state,
		nextOutputGeneration:      generation,
		publishedOutputGeneration: generation,
	}
	reservation := newRenderOutputReservation(service, generation)
	reservation.state.Store(uint32(renderOutputReservationCommitted))
	service.committedOutputReservation.Store(reservation)
	return service, reservation
}

func commitCurrentCycleAsSuccessor(t *testing.T, service *RenderService) {
	t.Helper()
	service.planMu.Lock()
	cycle := service.lastCycleSnapshot
	identity := service.lastPlanIdentity
	service.planMu.Unlock()
	require.NotNil(t, cycle)
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	transaction, err := service.stageCyclePublication(
		nil,
		rendercontext.RenderModeReconcile,
		generation,
		identity,
		cycle,
		nil,
	)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit(t.Context()))
}
