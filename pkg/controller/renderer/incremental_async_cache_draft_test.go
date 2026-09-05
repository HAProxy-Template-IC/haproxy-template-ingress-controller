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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type incrementalColdCacheDraftInstallation struct {
	build *incrementalCacheBuild
	draft *incrementalColdCacheDraft
	err   error
}

type incrementalColdCacheDraftIdentityPoisoner struct{}

type incrementalColdCacheDraftTestAction struct {
	seal *incrementalColdCacheDraftTestAction
	run  func(context.Context, *incrementalRenderSession) error
}

type incrementalColdCacheDraftTestMaterializer struct {
	seal          *incrementalColdCacheDraftTestMaterializer
	identity      *incrementalColdCacheDraftMaterializerIdentity
	action        *incrementalColdCacheDraftTestAction
	authenticated *incrementalColdCacheDraftTestAction
}

func newIncrementalColdCacheDraftTestMaterializer(
	run func(context.Context, *incrementalRenderSession) error,
) *incrementalColdCacheDraftTestMaterializer {
	identity := &incrementalColdCacheDraftMaterializerIdentity{}
	identity.seal = identity
	action := &incrementalColdCacheDraftTestAction{run: run}
	action.seal = action
	materializer := &incrementalColdCacheDraftTestMaterializer{
		identity: identity, action: action, authenticated: action,
	}
	materializer.seal = materializer
	return materializer
}

func (m *incrementalColdCacheDraftTestMaterializer) incrementalColdCacheDraftMaterializerIdentity() *incrementalColdCacheDraftMaterializerIdentity {
	return m.identity
}

func (m *incrementalColdCacheDraftTestMaterializer) validateIncrementalColdCacheDraftMaterializer() error {
	if m == nil || m.seal != m || m.identity == nil || m.identity.seal != m.identity ||
		m.action == nil || m.action != m.authenticated || m.action.seal != m.action || m.action.run == nil {
		return errors.New("incremental cold cache test materializer has invalid provenance")
	}
	return nil
}

func (m *incrementalColdCacheDraftTestMaterializer) materializeIncrementalColdCacheDraft(
	ctx context.Context,
	session *incrementalRenderSession,
) error {
	return m.action.run(ctx, session)
}

func (incrementalColdCacheDraftIdentityPoisoner) IncrementalCacheBuildStarted(
	_ context.Context,
	identity IncrementalCacheBuildIdentity,
) {
	identity.identity.seal = &incrementalCacheBuildIdentity{}
}

func (incrementalColdCacheDraftIdentityPoisoner) IncrementalCacheBuildCompleted(
	IncrementalCacheBuildIdentity,
	error,
) {
}

func TestColdCacheDraftMaterializesBeforeGraphPreparation(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	installed := make(chan incrementalColdCacheDraftInstallation, 1)
	var materialized atomic.Bool
	var preparedAfterMaterialization atomic.Bool
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		beforeOutputReservation: installIncrementalColdCacheDraft(fixture.service.incremental, installed, func(
			_ context.Context,
			session *incrementalRenderSession,
		) error {
			materialized.Store(session != nil)
			return nil
		}),
		beforePrepare: func(context.Context, uint64) {
			preparedAfterMaterialization.Store(materialized.Load())
		},
	})

	result := renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	assert.Equal(t, "a=first\nb=stable\n", result.HAProxyConfig)
	installation := receiveIncrementalColdCacheDraftInstallation(t, installed)
	require.NoError(t, installation.err)
	waitForIncrementalCache(t, fixture.service)
	assert.True(t, materialized.Load())
	assert.True(t, preparedAfterMaterialization.Load())
	assert.Equal(t, incrementalColdCacheDraftMaterialized, coldCacheDraftLifecycle(installation.draft))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())

	err := installation.build.materializeColdCacheDraft()
	require.Error(t, err)
	assert.Equal(t, incrementalColdCacheDraftMaterialized, coldCacheDraftLifecycle(installation.draft))
}

func TestColdCacheDraftRejectsPoison(t *testing.T) {
	tests := map[string]func(*incrementalColdCacheDraft){
		"forged seal": func(draft *incrementalColdCacheDraft) {
			draft.seal = &incrementalColdCacheDraft{}
		},
		"forged authentication": func(draft *incrementalColdCacheDraft) {
			draft.authentication = &incrementalColdCacheDraftAuthentication{}
		},
		"changed renderer state": func(draft *incrementalColdCacheDraft) {
			draft.state = &incrementalRenderState{graph: draft.state.graph}
		},
		"changed graph session": func(draft *incrementalColdCacheDraft) {
			draft.graphSession = nil
		},
		"changed output generation": func(draft *incrementalColdCacheDraft) {
			draft.outputGeneration++
		},
		"changed owner": func(draft *incrementalColdCacheDraft) {
			draft.owner = &incrementalCacheBuild{}
		},
		"changed lifecycle": func(draft *incrementalColdCacheDraft) {
			draft.lifecycle = incrementalColdCacheDraftTransferred
		},
		"changed callback": func(draft *incrementalColdCacheDraft) {
			materializer := draft.materializer.(*incrementalColdCacheDraftTestMaterializer)
			action := &incrementalColdCacheDraftTestAction{
				run: func(context.Context, *incrementalRenderSession) error { return nil },
			}
			action.seal = action
			materializer.action = action
		},
		"forged materializer": func(draft *incrementalColdCacheDraft) {
			draft.materializer = newIncrementalColdCacheDraftTestMaterializer(
				func(context.Context, *incrementalRenderSession) error { return nil },
			)
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			_, session, build := newIncrementalColdCacheDraftProtocolFixture(t)
			draft, err := newIncrementalColdCacheDraftForTest(
				session,
				build.generation,
				func(context.Context, *incrementalRenderSession) error { return nil },
			)
			require.NoError(t, err)
			require.NoError(t, draft.sealDraft())
			poison(draft)

			err = build.transferColdCacheDraft(draft)
			require.ErrorContains(t, err, "incremental cold cache draft")
			assert.Nil(t, build.draft)
			assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(draft))
		})
	}
}

func TestColdCacheDraftRejectsPoisonedBuildIdentity(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	installIncrementalCacheBuildObserver(fixture, incrementalColdCacheDraftIdentityPoisoner{})
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	installed := make(chan incrementalColdCacheDraftInstallation, 1)
	var materializations atomic.Int32
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		beforeOutputReservation: installIncrementalColdCacheDraft(
			fixture.service.incremental,
			installed,
			func(context.Context, *incrementalRenderSession) error {
				materializations.Add(1)
				return nil
			},
		),
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	installation := receiveIncrementalColdCacheDraftInstallation(t, installed)
	require.NoError(t, installation.err)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	waitForIncrementalCacheSignal(t, ready)

	require.ErrorContains(t, ready.result(), "build identity")
	assert.Zero(t, materializations.Load())
	assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(installation.draft))
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

func TestColdCacheDraftRejectsPartialForeignAndStaleTransfers(t *testing.T) {
	t.Run("partial", func(t *testing.T) {
		_, session, build := newIncrementalColdCacheDraftProtocolFixture(t)
		draft, err := newIncrementalColdCacheDraftForTest(
			session,
			build.generation,
			func(context.Context, *incrementalRenderSession) error { return nil },
		)
		require.NoError(t, err)
		require.Error(t, build.transferColdCacheDraft(draft))
		assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(draft))
	})

	t.Run("foreign session", func(t *testing.T) {
		state, _, build := newIncrementalColdCacheDraftProtocolFixture(t)
		graphSession, err := state.graph.BeginColdReset()
		require.NoError(t, err)
		t.Cleanup(graphSession.Abort)
		foreign := &incrementalRenderSession{state: state, graphSession: graphSession}
		draft, err := newIncrementalColdCacheDraftForTest(
			foreign,
			build.generation,
			func(context.Context, *incrementalRenderSession) error { return nil },
		)
		require.NoError(t, err)
		require.NoError(t, draft.sealDraft())
		require.ErrorContains(t, build.transferColdCacheDraft(draft), "another build")
	})

	t.Run("output generation", func(t *testing.T) {
		_, session, build := newIncrementalColdCacheDraftProtocolFixture(t)
		draft, err := newIncrementalColdCacheDraftForTest(
			session,
			build.generation+1,
			func(context.Context, *incrementalRenderSession) error { return nil },
		)
		require.NoError(t, err)
		require.NoError(t, draft.sealDraft())
		require.ErrorContains(t, build.transferColdCacheDraft(draft), "another build")
	})

	t.Run("graph generation", func(t *testing.T) {
		state, session, build := newIncrementalColdCacheDraftProtocolFixture(t)
		draft, err := newIncrementalColdCacheDraftForTest(
			session,
			build.generation,
			func(context.Context, *incrementalRenderSession) error { return nil },
		)
		require.NoError(t, err)
		require.NoError(t, draft.sealDraft())
		successor, err := state.graph.BeginColdReset()
		require.NoError(t, err)
		require.NoError(t, successor.Commit(t.Context(), acceptIncrementalDraftRevisions))

		require.ErrorContains(t, build.transferColdCacheDraft(draft), "stale graph")
	})
}

func TestColdCacheDraftConcurrentTransferHasOneOwner(t *testing.T) {
	_, session, build := newIncrementalColdCacheDraftProtocolFixture(t)
	draft, err := newIncrementalColdCacheDraftForTest(
		session,
		build.generation,
		func(context.Context, *incrementalRenderSession) error { return nil },
	)
	require.NoError(t, err)
	require.NoError(t, draft.sealDraft())
	const contenders = 32
	results := make(chan error, contenders)
	var group sync.WaitGroup
	group.Add(contenders)
	for range contenders {
		go func() {
			defer group.Done()
			results <- build.transferColdCacheDraft(draft)
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for transferErr := range results {
		if transferErr == nil {
			successes++
			continue
		}
		assert.ErrorContains(t, transferErr, "transfer is closed")
	}
	assert.Equal(t, 1, successes)
	assert.Same(t, build, draft.owner)
	assert.Equal(t, incrementalColdCacheDraftTransferred, coldCacheDraftLifecycle(draft))
}

func TestColdCacheDraftRejectsTransferredOwnershipPoison(t *testing.T) {
	tests := map[string]func(*incrementalColdCacheDraft){
		"owner": func(draft *incrementalColdCacheDraft) {
			draft.owner = &incrementalCacheBuild{}
		},
		"lifecycle": func(draft *incrementalColdCacheDraft) {
			draft.lifecycle = incrementalColdCacheDraftSealed
		},
		"transfer authentication": func(draft *incrementalColdCacheDraft) {
			draft.transfer = &incrementalColdCacheDraftTransferAuthentication{}
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			_, session, build := newIncrementalColdCacheDraftProtocolFixture(t)
			draft, err := newIncrementalColdCacheDraftForTest(
				session,
				build.generation,
				func(context.Context, *incrementalRenderSession) error { return nil },
			)
			require.NoError(t, err)
			require.NoError(t, draft.sealDraft())
			require.NoError(t, build.transferColdCacheDraft(draft))
			poison(draft)

			draft.mu.Lock()
			err = draft.validateLocked(incrementalColdCacheDraftTransferred)
			draft.mu.Unlock()
			require.ErrorContains(t, err, "incremental cold cache draft")
			draft.revoke()
			assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(draft))
		})
	}
}

func TestColdCacheDraftFailurePublishesNoPartialCache(t *testing.T) {
	tests := map[string]func(context.Context, *incrementalRenderSession) error{
		"error": func(context.Context, *incrementalRenderSession) error {
			return errors.New("partial draft")
		},
		"panic": func(context.Context, *incrementalRenderSession) error {
			panic("poison draft")
		},
	}
	for name, materialize := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			primeIncrementalHTTPFixtures(fixture)
			bootstrapIncrementalHTTPOutputOnly(t, fixture)
			installed := make(chan incrementalColdCacheDraftInstallation, 1)
			setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
				beforeOutputReservation: installIncrementalColdCacheDraft(
					fixture.service.incremental, installed, materialize,
				),
			})

			_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
			installation := receiveIncrementalColdCacheDraftInstallation(t, installed)
			require.NoError(t, installation.err)
			ready := requireIncrementalCacheReadySignal(t, fixture.service)
			waitForIncrementalCacheSignal(t, ready)
			require.Error(t, ready.result())
			assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(installation.draft))
			assert.Zero(t, fixture.service.incremental.graph.Generation())
		})
	}
}

func TestColdCacheDraftSupersededBeforeMaterializationIsRevoked(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	installed := make(chan incrementalColdCacheDraftInstallation, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var materializations atomic.Int32
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		beforeOutputReservation: func(ctx context.Context, generation uint64) {
			installIncrementalColdCacheDraft(
				fixture.service.incremental,
				installed,
				func(context.Context, *incrementalRenderSession) error {
					materializations.Add(1)
					return nil
				},
			)(ctx, generation)
			<-release
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	installation := receiveIncrementalColdCacheDraftInstallation(t, installed)
	require.NoError(t, installation.err)
	ready := requireIncrementalCacheReadySignal(t, fixture.service)
	commitCurrentCycleAsSuccessor(t, fixture.service)
	releaseOnce.Do(func() { close(release) })
	waitForIncrementalCacheSignal(t, ready)

	require.Error(t, ready.result())
	assert.Zero(t, materializations.Load())
	assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(installation.draft))
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

func TestColdCacheDraftCancellationCannotPublishOlderGeneration(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	primeIncrementalHTTPFixtures(fixture)
	bootstrapIncrementalHTTPOutputOnly(t, fixture)
	installed := make(chan incrementalColdCacheDraftInstallation, 2)
	firstEntered := make(chan struct{})
	var installations atomic.Uint32
	setIncrementalCacheBuilderHooks(t, fixture.service, incrementalCacheBuilderHooks{
		beforeOutputReservation: func(ctx context.Context, generation uint64) {
			index := installations.Add(1)
			materialize := func(ctx context.Context, _ *incrementalRenderSession) error {
				if index != 1 {
					return nil
				}
				close(firstEntered)
				<-ctx.Done()
				return nil
			}
			installIncrementalColdCacheDraft(
				fixture.service.incremental, installed, materialize,
			)(ctx, generation)
		},
	})

	_ = renderAndCommitWithoutCacheWait(t, fixture.service, fixture.provider)
	first := receiveIncrementalColdCacheDraftInstallation(t, installed)
	require.NoError(t, first.err)
	waitForSignal(t, firstEntered)
	firstReady := requireIncrementalCacheReadySignal(t, fixture.service)
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))

	successor, err := fixture.service.Render(
		t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
	)
	require.NoError(t, err)
	require.NoError(t, successor.InputTransaction.Commit(t.Context()))
	second := receiveIncrementalColdCacheDraftInstallation(t, installed)
	require.NoError(t, second.err)
	waitForIncrementalCacheSignal(t, firstReady)
	require.ErrorIs(t, firstReady.result(), errIncrementalCacheSuperseded)
	waitForIncrementalCache(t, fixture.service)

	assert.Equal(t, incrementalColdCacheDraftRevoked, coldCacheDraftLifecycle(first.draft))
	assert.Equal(t, incrementalColdCacheDraftMaterialized, coldCacheDraftLifecycle(second.draft))
	assert.Greater(t, second.build.generation, first.build.generation)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Generation())
	assert.Equal(t, "a=stable\nb=stable\n", successor.HAProxyConfig)
}

func installIncrementalColdCacheDraft(
	state *incrementalRenderState,
	installed chan<- incrementalColdCacheDraftInstallation,
	materialize func(context.Context, *incrementalRenderSession) error,
) func(context.Context, uint64) {
	return func(_ context.Context, generation uint64) {
		state.cache.mu.Lock()
		build := state.cache.current
		state.cache.mu.Unlock()
		installation := incrementalColdCacheDraftInstallation{build: build}
		if build == nil || build.generation != generation {
			installation.err = errors.New("incremental cold cache build is unavailable")
			installed <- installation
			return
		}
		installation.draft, installation.err = newIncrementalColdCacheDraftForTest(
			build.session, generation, materialize,
		)
		if installation.err == nil {
			installation.err = installation.draft.sealDraft()
		}
		if installation.err == nil {
			installation.err = build.transferColdCacheDraft(installation.draft)
		}
		installed <- installation
	}
}

func receiveIncrementalColdCacheDraftInstallation(
	t *testing.T,
	installed <-chan incrementalColdCacheDraftInstallation,
) incrementalColdCacheDraftInstallation {
	t.Helper()
	select {
	case installation := <-installed:
		return installation
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for incremental cold cache draft installation")
		return incrementalColdCacheDraftInstallation{}
	}
}

func newIncrementalColdCacheDraftForTest(
	session *incrementalRenderSession,
	outputGeneration uint64,
	run func(context.Context, *incrementalRenderSession) error,
) (*incrementalColdCacheDraft, error) {
	return newIncrementalColdCacheDraft(
		session,
		outputGeneration,
		newIncrementalColdCacheDraftTestMaterializer(run),
	)
}

func newIncrementalColdCacheDraftProtocolFixture(
	t *testing.T,
) (*incrementalRenderState, *incrementalRenderSession, *incrementalCacheBuild) {
	t.Helper()
	graph, err := incremental.New()
	require.NoError(t, err)
	graphSession, err := graph.BeginColdReset()
	require.NoError(t, err)
	t.Cleanup(graphSession.Abort)
	state := &incrementalRenderState{graph: graph}
	session := &incrementalRenderSession{state: state, graphSession: graphSession}
	build := &incrementalCacheBuild{
		builder: &state.cache, session: session, generation: 1, draftAccepting: true,
	}
	return state, session, build
}

func acceptIncrementalDraftRevisions(context.Context, []incremental.InputRevision) (bool, error) {
	return true, nil
}

func coldCacheDraftLifecycle(draft *incrementalColdCacheDraft) incrementalColdCacheDraftState {
	if draft == nil {
		return 0
	}
	draft.mu.Lock()
	defer draft.mu.Unlock()
	return draft.lifecycle
}
