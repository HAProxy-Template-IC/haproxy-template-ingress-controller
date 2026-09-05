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
)

type postProcessTestInputTransaction struct {
	commitErr error
	commits   atomic.Int32
	aborts    atomic.Int32
}

func (*postProcessTestInputTransaction) HasCandidates() bool {
	return false
}

func (t *postProcessTestInputTransaction) Commit(context.Context) error {
	t.commits.Add(1)
	return t.commitErr
}

func (t *postProcessTestInputTransaction) Abort() {
	t.aborts.Add(1)
}

type postProcessTestPublication struct {
	publishes atomic.Int32
	aborts    atomic.Int32
}

func (p *postProcessTestPublication) Publish() {
	p.publishes.Add(1)
}

func (p *postProcessTestPublication) Abort() {
	p.aborts.Add(1)
}

func TestPostProcessPublicationFollowsSuccessfulInputCommit(t *testing.T) {
	inner := &postProcessTestInputTransaction{}
	publication := &postProcessTestPublication{}
	transaction := newPostProcessPublicationTransaction(inner, publication)

	require.NoError(t, transaction.Commit(t.Context()))
	require.NoError(t, transaction.Commit(t.Context()))
	transaction.Abort()
	assert.EqualValues(t, 1, inner.commits.Load())
	assert.Zero(t, inner.aborts.Load())
	assert.EqualValues(t, 1, publication.publishes.Load())
	assert.Zero(t, publication.aborts.Load())
}

func TestPostProcessPublicationAbortsWhenInputCommitFails(t *testing.T) {
	wantErr := errors.New("input commit failed")
	inner := &postProcessTestInputTransaction{commitErr: wantErr}
	publication := &postProcessTestPublication{}
	transaction := newPostProcessPublicationTransaction(inner, publication)

	require.ErrorIs(t, transaction.Commit(t.Context()), wantErr)
	assert.EqualValues(t, 1, inner.commits.Load())
	assert.EqualValues(t, 1, inner.aborts.Load())
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
}

func TestPostProcessPublicationCancellationAbortsEverything(t *testing.T) {
	inner := &postProcessTestInputTransaction{}
	publication := &postProcessTestPublication{}
	transaction := newPostProcessPublicationTransaction(inner, publication)
	ctx, cancel := context.WithCancelCause(t.Context())
	wantErr := errors.New("commit canceled")
	cancel(wantErr)

	require.ErrorIs(t, transaction.Commit(ctx), wantErr)
	assert.Zero(t, inner.commits.Load())
	assert.EqualValues(t, 1, inner.aborts.Load())
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
}

func TestPostProcessPublicationAbortIsSticky(t *testing.T) {
	inner := &postProcessTestInputTransaction{}
	publication := &postProcessTestPublication{}
	transaction := newPostProcessPublicationTransaction(inner, publication)

	transaction.Abort()
	require.ErrorIs(t, transaction.Commit(t.Context()), errPostProcessPublicationAborted)
	assert.Zero(t, inner.commits.Load())
	assert.EqualValues(t, 1, inner.aborts.Load())
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
}

func TestPostProcessPublicationCommitsWithoutOtherInputs(t *testing.T) {
	publication := &postProcessTestPublication{}
	transaction := newPostProcessPublicationTransaction(nil, publication)

	require.NoError(t, transaction.Commit(t.Context()))
	assert.EqualValues(t, 1, publication.publishes.Load())
	assert.Zero(t, publication.aborts.Load())
}

func TestPostProcessPublicationJoinsExistingPublicationBoundary(t *testing.T) {
	transaction := &planPublicationTransaction{}
	var mainPublishes atomic.Int32
	var mainAborts atomic.Int32
	transaction.stagePublicationFinalizer(
		incrementPostProcessCounter(&mainPublishes),
		incrementPostProcessCounter(&mainAborts),
	)
	publication := &postProcessTestPublication{}

	joined := newPostProcessPublicationTransaction(transaction, publication)

	require.Same(t, transaction, joined)
	require.NoError(t, joined.Commit(t.Context()))
	assert.EqualValues(t, 1, mainPublishes.Load())
	assert.Zero(t, mainAborts.Load())
	assert.EqualValues(t, 1, publication.publishes.Load())
	assert.Zero(t, publication.aborts.Load())
}

func TestPostProcessPublicationExistingBoundaryFailureAbortsEveryCandidate(t *testing.T) {
	wantErr := errors.New("input commit failed")
	inner := &postProcessTestInputTransaction{commitErr: wantErr}
	transaction := &planPublicationTransaction{inner: inner}
	var mainPublishes atomic.Int32
	var mainAborts atomic.Int32
	transaction.stagePublicationFinalizer(
		incrementPostProcessCounter(&mainPublishes),
		incrementPostProcessCounter(&mainAborts),
	)
	publication := &postProcessTestPublication{}
	joined := newPostProcessPublicationTransaction(transaction, publication)

	require.Same(t, transaction, joined)
	require.ErrorIs(t, joined.Commit(t.Context()), wantErr)
	require.ErrorIs(t, joined.Commit(t.Context()), wantErr)
	joined.Abort()
	assert.EqualValues(t, 1, inner.commits.Load())
	assert.EqualValues(t, 1, inner.aborts.Load())
	assert.EqualValues(t, 1, mainPublishes.Load())
	assert.EqualValues(t, 1, mainAborts.Load())
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
}

func TestPostProcessPublicationWaitsForEveryReversiblePublication(t *testing.T) {
	transaction := &planPublicationTransaction{}
	publication := &postProcessTestPublication{}
	joined := newPostProcessPublicationTransaction(transaction, publication)
	require.Same(t, transaction, joined)
	var state atomic.Int32
	transaction.stagePublicationFinalizer(func() {
		state.Store(1)
		panic("mutate then panic")
	}, func() { state.Store(0) })

	err := joined.Commit(t.Context())

	require.ErrorContains(t, err, "mutate then panic")
	assert.Zero(t, state.Load())
	assert.Zero(t, publication.publishes.Load())
	assert.EqualValues(t, 1, publication.aborts.Load())
}

func TestPostProcessPublicationLateStagingIsRejected(t *testing.T) {
	tests := map[string]struct {
		finish      func(*planPublicationTransaction) error
		wantPublish int32
		wantAbort   int32
	}{
		"success": {
			finish: func(transaction *planPublicationTransaction) error {
				return transaction.Commit(t.Context())
			},
			wantAbort: 1,
		},
		"abort": {
			finish: func(transaction *planPublicationTransaction) error {
				transaction.Abort()
				return transaction.Commit(t.Context())
			},
			wantAbort: 1,
		},
		"inner failure": {
			finish: func(transaction *planPublicationTransaction) error {
				transaction.inner = &postProcessTestInputTransaction{commitErr: errors.New("failed")}
				return transaction.Commit(t.Context())
			},
			wantAbort: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			transaction := &planPublicationTransaction{}
			_ = test.finish(transaction)
			publication := &postProcessTestPublication{}

			joined := newPostProcessPublicationTransaction(transaction, publication)

			require.Same(t, transaction, joined)
			assert.Equal(t, test.wantPublish, publication.publishes.Load())
			assert.Equal(t, test.wantAbort, publication.aborts.Load())
		})
	}
}

func TestStagedRenderPublicationsConcurrentStageHasExactOutcome(t *testing.T) {
	for _, succeeded := range []bool{false, true} {
		for range 1000 {
			var publications stagedRenderPublications
			var publishes atomic.Int32
			var aborts atomic.Int32
			start := make(chan struct{})
			var completed sync.WaitGroup
			completed.Add(2)
			go func() {
				defer completed.Done()
				<-start
				publications.stage(incrementPostProcessCounter(&publishes), incrementPostProcessCounter(&aborts))
			}()
			go func() {
				defer completed.Done()
				<-start
				publications.finish(succeeded)
			}()
			close(start)
			completed.Wait()

			if succeeded {
				assert.EqualValues(t, 1, publishes.Load()+aborts.Load())
			} else {
				assert.Zero(t, publishes.Load())
				assert.EqualValues(t, 1, aborts.Load())
			}
		}
	}
}

func TestStagedRenderPublicationsDrainReentrantFinalizers(t *testing.T) {
	var publications stagedRenderPublications
	var first atomic.Int32
	var second atomic.Int32
	publications.stage(nil, func() {
		first.Add(1)
		publications.stage(nil, incrementPostProcessCounter(&second))
	})

	publications.finish(false)

	assert.EqualValues(t, 1, first.Load())
	assert.EqualValues(t, 1, second.Load())
}

func TestStagedRenderPublicationsPanicAbortsEveryCandidate(t *testing.T) {
	var publications stagedRenderPublications
	var completed atomic.Int32
	var mutated atomic.Int32
	var aborts atomic.Int32
	publications.stage(func() {
		mutated.Store(1)
		panic("publication failed")
	}, func() {
		mutated.Store(0)
		aborts.Add(1)
	})
	publications.stage(incrementPostProcessCounter(&completed), incrementPostProcessCounter(&aborts))

	assert.Panics(t, func() {
		publications.finish(true)
	})
	assert.Zero(t, mutated.Load())
	assert.Zero(t, completed.Load())
	assert.EqualValues(t, 2, aborts.Load())
	publications.stage(incrementPostProcessCounter(&completed), nil)
	assert.Zero(t, completed.Load())
}

func incrementPostProcessCounter(counter *atomic.Int32) func() {
	return func() {
		counter.Add(1)
	}
}
