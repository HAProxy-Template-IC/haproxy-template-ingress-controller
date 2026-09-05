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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalStatePreparationFailsBeforeInputPublication(t *testing.T) {
	inputKey := incremental.NewInputKey("input")
	queryKey := incremental.NewQueryKey("query")
	graph, err := incremental.New(incremental.Definition{
		Key: queryKey,
		Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
			value, _, readErr := reader.Input(inputKey)
			return value, readErr
		},
	})
	require.NoError(t, err)
	graphSession, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, graphSession.ApplyInputs(incremental.Input{
		Key:      inputKey,
		Revision: incremental.NewRevision("r1"),
		Found:    true,
		Value:    []byte("candidate"),
	}))
	_, err = graphSession.Evaluate(t.Context(), queryKey)
	require.NoError(t, err)

	state := newHTTPRegistryTestState()
	base := newIncrementalStateSnapshot()
	state.snapshot = base
	runtime := &incrementalRenderSession{
		state:         state,
		base:          base,
		cursors:       map[string]incrementalStoreCursor{},
		bindings:      base.bindings.Txn(),
		members:       base.members.Txn(),
		activeGroups:  base.activeGroups.instances.Txn(),
		retired:       base.retired.Txn(),
		results:       base.results.Txn(),
		derived:       base.derived.Txn(),
		httpEffects:   base.httpEffects.Txn(),
		groupIndexes:  map[string]*incrementalGroupIndex{},
		groupReady:    map[string]bool{},
		groupChanged:  map[string]bool{},
		requested:     map[string]bool{},
		httpKnown:     map[httpInputIdentity]httpInputSpec{},
		httpRetained:  map[uint64]struct{}{},
		httpRefDeltas: map[uint64]httpRefDelta{1: {removed: 1}},
	}
	runtime.resetCatalog(base.catalog)
	published := false

	err = graphSession.CommitWithPreparedPublisher(t.Context(),
		func(context.Context, []incremental.InputRevision) (bool, error) { return true, nil },
		func(retired []incremental.InputKey) (incremental.CommitPublication, error) {
			prepared, prepareErr := runtime.prepareStateCommit(retired, httpstore.ActiveLeaseToken{})
			if prepareErr != nil {
				return incremental.CommitPublication{}, prepareErr
			}
			return incremental.CommitPublication{
				Publish:  func() { published = true },
				Complete: prepared.Publish,
				Abort:    prepared.Abort,
			}, nil
		})

	require.ErrorContains(t, err, "reference count is inconsistent")
	assert.False(t, published)
	assert.Zero(t, graph.Generation())
	assert.Same(t, base, state.snapshot)
	require.NoError(t, runtime.finishHTTPInputs(false, nil))
}

func TestWarmGraphMutationPanicPublishesNoHTTPOrPostProcessState(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	base := fixture.service.incremental.snapshot
	generation := fixture.service.incremental.graph.Generation()
	baseToken := base.httpCursor.token
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlA))
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))
	descriptor, err := httpstore.DescribeSource(httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	acceptedA := fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlA, descriptor)
	acceptedB := fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlB, descriptor)
	require.True(t, acceptedA.Found)
	require.True(t, acceptedB.Found)
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	runtime := transaction.incremental
	require.NotNil(t, runtime.graphSession)
	postProcess := &postProcessTestPublication{}
	require.Same(t, transaction, newPostProcessPublicationTransaction(transaction, postProcess))
	httpPublication := &incrementalHTTPPublication{
		session: runtime, transaction: transaction.http,
	}
	releaseFences, err := runtime.acquireStoreCommitFences(t.Context())
	require.NoError(t, err)
	err = runtime.graphSession.CommitWithPreparedPublisher(
		t.Context(),
		func(ctx context.Context, inputs []incremental.InputRevision) (bool, error) {
			return runtime.verifyGraphPublication(ctx, inputs, httpPublication)
		},
		func(retired []incremental.InputKey) (incremental.CommitPublication, error) {
			prepared, prepareErr := runtime.prepareGraphPublication(
				retired,
				httpPublication,
				incrementalCommitPublications{prepare: func() {
					if publicationErr := transaction.publications.prepareTerminalResult(); publicationErr != nil {
						panic(requiredRenderPublicationPanic{err: publicationErr})
					}
				}},
			)
			if prepareErr != nil {
				return incremental.CommitPublication{}, prepareErr
			}
			publication := prepared.core
			publication.Complete = func() { panic("graph mutate then panic") }
			return publication, nil
		},
	)
	releaseFences()
	httpPublication.finish()
	runtime.abort()
	_ = transaction.publications.abortResult()

	require.ErrorContains(t, err, "graph mutate then panic")
	assert.Equal(t, generation, fixture.service.incremental.graph.Generation())
	assert.Same(t, base, fixture.service.incremental.snapshot)
	assert.Equal(t, baseToken, fixture.service.incremental.snapshot.httpCursor.token)
	assert.Equal(t, acceptedA, fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlA, descriptor))
	assert.Equal(t, acceptedB, fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlB, descriptor))
	assert.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlA))
	assert.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))
	assert.Zero(t, postProcess.publishes.Load())
	assert.EqualValues(t, 1, postProcess.aborts.Load())

	retry := renderAndCommitIncrementalCacheReady(t, fixture.service, fixture.provider)
	assert.Equal(t, "a=stable\nb=stable\n", retry)
}

func TestWarmGraphVisibilityWaitsForTerminalStatePublication(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	base := fixture.service.incremental.snapshot
	baseGeneration := fixture.service.incremental.graph.Generation()
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	runtime := transaction.incremental
	httpPublication := &incrementalHTTPPublication{session: runtime, transaction: transaction.http}
	releaseFences, err := runtime.acquireStoreCommitFences(t.Context())
	require.NoError(t, err)
	defer releaseFences()
	var prepared *preparedIncrementalGraphPublication
	err = runtime.graphSession.CommitWithPreparedPublisher(
		t.Context(),
		func(ctx context.Context, inputs []incremental.InputRevision) (bool, error) {
			return runtime.verifyGraphPublication(ctx, inputs, httpPublication)
		},
		func(retired []incremental.InputKey) (incremental.CommitPublication, error) {
			var prepareErr error
			prepared, prepareErr = runtime.prepareGraphPublication(
				retired, httpPublication, incrementalCommitPublications{},
			)
			if prepareErr != nil {
				return incremental.CommitPublication{}, prepareErr
			}
			return prepared.core, nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	defer prepared.releaseStateLock()
	assert.Equal(t, baseGeneration+1, fixture.service.incremental.graph.Generation())

	started := make(chan struct{})
	observed := make(chan *incrementalStateSnapshot, 1)
	go func() {
		close(started)
		runtime.state.mu.Lock()
		observed <- runtime.state.snapshot
		runtime.state.mu.Unlock()
	}()
	<-started
	select {
	case <-observed:
		t.Fatal("incremental state became visible before terminal publication")
	default:
	}

	prepared.releaseStateLock()
	httpPublication.finish()
	prepared.state.Release()
	assert.Same(t, prepared.state.snapshot, <-observed)
	assert.NotSame(t, base, fixture.service.incremental.snapshot)
}

func TestPublicationCallbacksRunExactlyOnceAfterSuccessfulCommit(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	var calls atomic.Int32
	var aborts atomic.Int32
	transaction.stagePublicationFinalizer(
		func() { calls.Add(1) },
		func() { aborts.Add(1) },
	)

	require.NoError(t, transaction.Commit(t.Context()))
	require.NoError(t, transaction.Commit(t.Context()))
	transaction.Abort()
	assert.Equal(t, int32(1), calls.Load())
	assert.Zero(t, aborts.Load())
	transaction.stagePublicationFinalizer(
		func() { calls.Add(1) },
		func() { aborts.Add(1) },
	)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, int32(1), aborts.Load())
}

func TestPublicationCallbacksDoNotRunAfterRejectedCommit(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	var calls atomic.Int32
	var aborts atomic.Int32
	transaction.stagePublicationFinalizer(
		func() { calls.Add(1) },
		func() { aborts.Add(1) },
	)
	routes := fixture.provider.GetStore("routes")
	require.NoError(t, routes.Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))

	require.Error(t, transaction.Commit(t.Context()))
	transaction.Abort()
	assert.Zero(t, calls.Load())
	assert.Equal(t, int32(1), aborts.Load())
	transaction.stagePublicationFinalizer(
		func() { calls.Add(1) },
		func() { aborts.Add(1) },
	)
	assert.Zero(t, calls.Load())
	assert.Equal(t, int32(2), aborts.Load())
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

func TestCombinedInputTransactionCommitReportsPriorAbort(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	var published atomic.Bool
	var aborted atomic.Bool
	transaction.stagePublicationFinalizer(
		func() { published.Store(true) },
		func() { aborted.Store(true) },
	)

	transaction.Abort()

	require.ErrorIs(t, transaction.Commit(t.Context()), errCombinedInputTransactionAborted)
	assert.False(t, published.Load())
	assert.True(t, aborted.Load())
}

func TestCombinedInputTransactionPreparationFailureAbortsEveryCandidate(t *testing.T) {
	input := &postProcessTestInputTransaction{}
	transaction, ok := newCombinedRenderInputTransaction(input, nil, nil).(*combinedRenderInputTransaction)
	require.True(t, ok)
	publication := &postProcessTestPublication{}
	joined := newPostProcessPublicationTransaction(transaction, publication)
	require.Same(t, transaction, joined)

	err := joined.Commit(t.Context())

	require.ErrorContains(t, err, "no atomic preparation protocol")
	assert.Zero(t, input.commits.Load())
	assert.EqualValues(t, 1, input.aborts.Load())
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
}

func TestCombinedInputTransactionCancellationAbortsEveryCandidate(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	publication := &postProcessTestPublication{}
	joined := newPostProcessPublicationTransaction(transaction, publication)
	require.Same(t, transaction, joined)
	ctx, cancel := context.WithCancelCause(t.Context())
	wantErr := errors.New("commit canceled")
	cancel(wantErr)

	err = joined.Commit(ctx)

	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, joined.Commit(t.Context()), wantErr)
	joined.Abort()
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

func TestCombinedInputTransactionCleanupFailurePrecedesEveryPublication(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	require.NotNil(t, transaction.incremental)
	transaction.incremental.loggerContext.logger = nil
	transaction.incremental.cachePublicationEnabled = false
	transaction.incremental.httpMu.Lock()
	transaction.incremental.httpRetained[999] = struct{}{}
	transaction.incremental.httpMu.Unlock()
	publication := &postProcessTestPublication{}
	joined := newPostProcessPublicationTransaction(transaction, publication)
	require.Same(t, transaction, joined)

	err = joined.Commit(t.Context())

	require.ErrorContains(t, err, "no in-flight reference")
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
	assert.Zero(t, fixture.service.incremental.graph.Generation())
}

func TestPostProcessPublicationSharesCombinedInputCommitOutcome(t *testing.T) {
	tests := map[string]struct {
		mutate bool
	}{
		"success": {},
		"revision conflict": {
			mutate: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
			require.NoError(t, err)
			transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
			require.True(t, ok)
			publication := &postProcessTestPublication{}
			joined := newPostProcessPublicationTransaction(transaction, publication)
			require.Same(t, transaction, joined)
			if test.mutate {
				routes := fixture.provider.GetStore("routes")
				require.NoError(t, routes.Update(
					incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
					[]string{"default", "a"},
				))
			}

			err = joined.Commit(t.Context())
			if test.mutate {
				require.Error(t, err)
				assert.Zero(t, publication.publishes.Load())
				assert.EqualValues(t, 1, publication.aborts.Load())
				return
			}
			require.NoError(t, err)
			assert.EqualValues(t, 1, publication.publishes.Load())
			assert.Zero(t, publication.aborts.Load())
		})
	}
}

func TestCombinedInputTransactionConcurrentAbortNeverReportsSuccess(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))

	for range 100 {
		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
		require.True(t, ok)
		var published atomic.Bool
		var aborted atomic.Bool
		transaction.stagePublicationFinalizer(
			func() { published.Store(true) },
			func() { aborted.Store(true) },
		)
		start := make(chan struct{})
		commitResult := make(chan error, 1)
		var completed sync.WaitGroup
		completed.Add(2)
		go func() {
			defer completed.Done()
			<-start
			commitResult <- transaction.Commit(t.Context())
		}()
		go func() {
			defer completed.Done()
			<-start
			transaction.Abort()
		}()
		close(start)
		completed.Wait()

		commitErr := <-commitResult
		if published.Load() {
			require.NoError(t, commitErr)
			assert.False(t, aborted.Load())
			continue
		}
		require.ErrorIs(t, commitErr, errCombinedInputTransactionAborted)
		assert.True(t, aborted.Load())
	}
}
