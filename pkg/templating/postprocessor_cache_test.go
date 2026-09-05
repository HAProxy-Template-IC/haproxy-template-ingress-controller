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

package templating

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostProcessCacheUsesExactInputAndChainIdentity(t *testing.T) {
	cache := newPostProcessCache()
	firstChain := newPostProcessCacheIdentity()
	secondChain := newPostProcessCacheIdentity()
	tx := cache.begin()
	var calls atomic.Int32

	process := func(identity *postProcessCacheIdentity, input, output string) string {
		t.Helper()
		value, err := tx.process(context.Background(), identity, input, func(context.Context) (string, error) {
			calls.Add(1)
			return output, nil
		})
		require.NoError(t, err)
		return value
	}

	assert.Equal(t, "first-a", process(firstChain, "route-a", "first-a"))
	assert.Equal(t, "first-a", process(firstChain, "route-a", "poison"))
	assert.Equal(t, "first-b", process(firstChain, "route-b", "first-b"))
	assert.Equal(t, "second-a", process(secondChain, "route-a", "second-a"))
	assert.EqualValues(t, 3, calls.Load())

	publication := stagePostProcessCacheForTest(t, tx)
	assert.Empty(t, cache.active.Load().entries)
	require.True(t, publication.publish())
	require.Len(t, cache.active.Load().entries, 3)

	hit := cache.begin()
	assertPostProcessCacheHit(t, hit, firstChain, "route-a", "first-a")
	assertPostProcessCacheHit(t, hit, firstChain, "route-b", "first-b")
	assertPostProcessCacheHit(t, hit, secondChain, "route-a", "second-a")
}

func TestPostProcessCacheKeepsEveryStringByteInTheKey(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	inputs := []string{"", "route-a", "route-b", "route\x00a", "route\x00b", "röute-a", "röute-b"}
	for index, input := range inputs {
		want := fmt.Sprintf("value-%d", index)
		value, err := tx.process(context.Background(), identity, input, constantPostProcess(want))
		require.NoError(t, err)
		assert.Equal(t, want, value)
	}
	commitPostProcessCacheForTest(t, tx)
	require.Len(t, cache.active.Load().entries, len(inputs))

	hit := cache.begin()
	for index, input := range inputs {
		assertPostProcessCacheHit(t, hit, identity, input, fmt.Sprintf("value-%d", index))
	}
}

func TestPostProcessCacheAbortedGenerationCannotPoisonActive(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()

	tx := cache.begin()
	value, err := tx.process(context.Background(), identity, "stable", constantPostProcess("good"))
	require.NoError(t, err)
	assert.Equal(t, "good", value)
	commitPostProcessCacheForTest(t, tx)

	poisoned := cache.begin()
	value, err = poisoned.process(context.Background(), identity, "new", constantPostProcess("poison"))
	require.NoError(t, err)
	assert.Equal(t, "poison", value)
	publication := stagePostProcessCacheForTest(t, poisoned)
	require.True(t, publication.abort())
	assert.False(t, publication.publish())

	active := cache.active.Load()
	require.Len(t, active.entries, 1)
	assert.Equal(t, "good", active.entries[postProcessCacheKey{identity: identity, input: "stable"}])
	_, exists := active.entries[postProcessCacheKey{identity: identity, input: "new"}]
	assert.False(t, exists)

	recovery := cache.begin()
	value, err = recovery.process(context.Background(), identity, "new", constantPostProcess("correct"))
	require.NoError(t, err)
	assert.Equal(t, "correct", value)
}

func TestPostProcessCacheFailureIsNeverPublished(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	wantErr := errors.New("processor failed")

	_, err := tx.process(context.Background(), identity, "input", func(context.Context) (string, error) {
		return "partial", wantErr
	})
	require.ErrorIs(t, err, wantErr)
	_, err = tx.stage(context.Background())
	require.ErrorIs(t, err, wantErr)
	tx.abort()
	assert.Empty(t, cache.active.Load().entries)

	recovery := cache.begin()
	value, err := recovery.process(context.Background(), identity, "input", constantPostProcess("complete"))
	require.NoError(t, err)
	assert.Equal(t, "complete", value)
}

func TestPostProcessCachePanicCannotLeaveAReusablePartialResult(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	waiterDone := make(chan error, 1)
	started := make(chan struct{})
	release := make(chan struct{})

	producerDone := make(chan any, 1)
	go func() {
		defer func() {
			producerDone <- recover()
		}()
		_, _ = tx.process(context.Background(), identity, "input", func(context.Context) (string, error) {
			close(started)
			<-release
			panic("processor panic")
		})
	}()
	<-started
	go func() {
		_, err := tx.process(context.Background(), identity, "input", constantPostProcess("wrong"))
		waiterDone <- err
	}()
	close(release)
	assert.Equal(t, "processor panic", <-producerDone)
	require.ErrorIs(t, <-waiterDone, errPostProcessCacheComputePanicked)
	_, err := tx.stage(context.Background())
	require.ErrorIs(t, err, errPostProcessCacheComputePanicked)
	assert.Empty(t, cache.active.Load().entries)

	recovery := cache.begin()
	value, err := recovery.process(context.Background(), identity, "input", constantPostProcess("complete"))
	require.NoError(t, err)
	assert.Equal(t, "complete", value)
}

func TestPostProcessCacheCanceledHitFailsTransaction(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	seed := cache.begin()
	_, err := seed.process(context.Background(), identity, "input", constantPostProcess("cached"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, seed)

	ctx, cancel := context.WithCancelCause(context.Background())
	wantErr := errors.New("render canceled")
	cancel(wantErr)
	tx := cache.begin()
	var computed atomic.Bool
	_, err = tx.process(ctx, identity, "input", func(context.Context) (string, error) {
		computed.Store(true)
		return "wrong", nil
	})
	require.ErrorIs(t, err, wantErr)
	assert.False(t, computed.Load())
	_, err = tx.stage(context.Background())
	require.ErrorIs(t, err, wantErr)
	require.Len(t, cache.active.Load().entries, 1)
}

func TestPostProcessCacheCancellationDetectedAfterHitLookup(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	seed := cache.begin()
	_, err := seed.process(context.Background(), identity, "input", constantPostProcess("cached"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, seed)

	wantErr := errors.New("canceled after lookup")
	ctx := &postProcessSecondCauseContext{err: wantErr}
	tx := cache.begin()
	_, err = tx.process(ctx, identity, "input", constantPostProcess("wrong"))
	require.ErrorIs(t, err, wantErr)
	_, err = tx.stage(context.Background())
	require.ErrorIs(t, err, wantErr)
}

func TestPostProcessCacheCancellationDuringMissRejectsPartialOutput(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	ctx, cancel := context.WithCancelCause(context.Background())
	wantErr := errors.New("deadline won")
	tx := cache.begin()

	_, err := tx.process(ctx, identity, "input", func(context.Context) (string, error) {
		cancel(wantErr)
		return "partial", nil
	})
	require.ErrorIs(t, err, wantErr)
	_, err = tx.stage(context.Background())
	require.ErrorIs(t, err, wantErr)
	assert.Empty(t, cache.active.Load().entries)
}

func TestPostProcessCacheCancellationBeforeStageLeavesActiveGeneration(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	seed := cache.begin()
	_, err := seed.process(context.Background(), identity, "old", constantPostProcess("old-value"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, seed)

	tx := cache.begin()
	_, err = tx.process(context.Background(), identity, "new", constantPostProcess("new-value"))
	require.NoError(t, err)
	ctx, cancel := context.WithCancelCause(context.Background())
	wantErr := errors.New("commit canceled")
	cancel(wantErr)
	_, err = tx.stage(ctx)
	require.ErrorIs(t, err, wantErr)
	tx.abort()

	active := cache.active.Load()
	require.Len(t, active.entries, 1)
	assert.Equal(t, "old-value", active.entries[postProcessCacheKey{identity: identity, input: "old"}])
}

func TestPostProcessCacheCanceledWaiterFailsSharedTransaction(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	started := make(chan struct{})
	release := make(chan struct{})
	producerDone := make(chan error, 1)

	go func() {
		_, err := tx.process(context.Background(), identity, "input", func(context.Context) (string, error) {
			close(started)
			<-release
			return "complete", nil
		})
		producerDone <- err
	}()
	<-started

	waiterCtx, cancel := context.WithCancelCause(context.Background())
	wantErr := errors.New("waiter canceled")
	waiterDone := make(chan error, 1)
	go func() {
		_, err := tx.process(waiterCtx, identity, "input", constantPostProcess("wrong"))
		waiterDone <- err
	}()
	cancel(wantErr)
	require.ErrorIs(t, <-waiterDone, wantErr)
	close(release)
	require.ErrorIs(t, <-producerDone, wantErr)
	_, err := tx.stage(context.Background())
	require.ErrorIs(t, err, wantErr)
}

func TestPostProcessCacheWaiterReceivesCompletedValue(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	started := make(chan struct{})
	release := make(chan struct{})
	producerDone := make(chan error, 1)

	go func() {
		_, err := tx.process(context.Background(), identity, "input", func(context.Context) (string, error) {
			close(started)
			<-release
			return "complete", nil
		})
		producerDone <- err
	}()
	<-started
	waiting := make(chan struct{})
	waiterCtx := &postProcessWaitSignalContext{Context: context.Background(), waiting: waiting}
	waiterValue := make(chan string, 1)
	waiterErr := make(chan error, 1)
	go func() {
		value, err := tx.process(waiterCtx, identity, "input", constantPostProcess("wrong"))
		waiterValue <- value
		waiterErr <- err
	}()
	<-waiting
	close(release)
	require.NoError(t, <-producerDone)
	require.NoError(t, <-waiterErr)
	assert.Equal(t, "complete", <-waiterValue)
}

func TestPostProcessCacheStageRejectsInFlightComputation(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		_, err := tx.process(context.Background(), identity, "input", func(context.Context) (string, error) {
			close(started)
			<-release
			return "complete", nil
		})
		done <- err
	}()
	<-started
	_, err := tx.stage(context.Background())
	require.ErrorIs(t, err, errPostProcessCacheTransactionInFlight)
	close(release)
	require.NoError(t, <-done)
	commitPostProcessCacheForTest(t, tx)
}

func TestPostProcessCacheAbortDuringComputationUnblocksWaiters(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	started := make(chan struct{})
	release := make(chan struct{})
	producerDone := make(chan error, 1)

	go func() {
		_, err := tx.process(context.Background(), identity, "input", func(context.Context) (string, error) {
			close(started)
			<-release
			return "discarded", nil
		})
		producerDone <- err
	}()
	<-started
	waiting := make(chan struct{})
	waiterCtx := &postProcessWaitSignalContext{Context: context.Background(), waiting: waiting}
	waiterDone := make(chan error, 1)
	go func() {
		_, err := tx.process(waiterCtx, identity, "input", constantPostProcess("wrong"))
		waiterDone <- err
	}()
	<-waiting
	tx.abort()
	close(release)
	require.ErrorIs(t, <-producerDone, errPostProcessCacheTransactionClosed)
	require.ErrorIs(t, <-waiterDone, errPostProcessCacheTransactionClosed)
	assert.Empty(t, cache.active.Load().entries)
}

func TestPostProcessCacheStageChecksCancellationBeforeSealing(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	_, err := tx.process(context.Background(), identity, "input", constantPostProcess("value"))
	require.NoError(t, err)
	wantErr := errors.New("canceled while sealing")
	_, err = tx.stage(&postProcessSecondCauseContext{err: wantErr})
	require.ErrorIs(t, err, wantErr)
	tx.abort()
	assert.Empty(t, cache.active.Load().entries)
}

func TestPostProcessCacheRetainsOnlyLatestCommittedGeneration(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	first := cache.begin()
	_, err := first.process(context.Background(), identity, "old", constantPostProcess("old-value"))
	require.NoError(t, err)
	_, err = first.process(context.Background(), identity, "keep", constantPostProcess("keep-value"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, first)
	require.Len(t, cache.active.Load().entries, 2)

	next := cache.begin()
	assertPostProcessCacheHit(t, next, identity, "keep", "keep-value")
	commitPostProcessCacheForTest(t, next)

	active := cache.active.Load()
	require.Len(t, active.entries, 1)
	assert.Equal(t, "keep-value", active.entries[postProcessCacheKey{identity: identity, input: "keep"}])
	_, exists := active.entries[postProcessCacheKey{identity: identity, input: "old"}]
	assert.False(t, exists)

	empty := cache.begin()
	commitPostProcessCacheForTest(t, empty)
	assert.Empty(t, cache.active.Load().entries)
}

func TestPostProcessCacheCoalescesConcurrentMisses(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var calls atomic.Int32

	const goroutines = 100
	results := make(chan string, goroutines)
	errorsSeen := make(chan error, goroutines)
	for range goroutines {
		go func() {
			value, err := tx.process(context.Background(), identity, "same", func(context.Context) (string, error) {
				calls.Add(1)
				startOnce.Do(func() { close(started) })
				<-release
				return "result", nil
			})
			results <- value
			errorsSeen <- err
		}()
	}
	<-started
	close(release)
	for range goroutines {
		require.NoError(t, <-errorsSeen)
		assert.Equal(t, "result", <-results)
	}
	assert.EqualValues(t, 1, calls.Load())
	commitPostProcessCacheForTest(t, tx)
	require.Len(t, cache.active.Load().entries, 1)
}

func TestPostProcessCacheBatchReusesHitsAndComputesUniqueMisses(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	seed := cache.begin()
	_, err := seed.process(context.Background(), identity, "cached", constantPostProcess("cached-value"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, seed)

	tx := cache.begin()
	var misses []string
	values, err := tx.processBatch(
		context.Background(),
		identity,
		[]string{"cached", "new-a", "new-a", "new-b", "cached"},
		func(_ context.Context, inputs []string) ([]string, error) {
			misses = append(misses, inputs...)
			return []string{"value-a", "value-b"}, nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"new-a", "new-b"}, misses)
	assert.Equal(t, []string{"cached-value", "value-a", "value-a", "value-b", "cached-value"}, values)
	commitPostProcessCacheForTest(t, tx)

	hit := cache.begin()
	values, err = hit.processBatch(
		context.Background(),
		identity,
		[]string{"new-b", "cached", "new-a"},
		func(context.Context, []string) ([]string, error) {
			return nil, errors.New("unexpected cache miss")
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"value-b", "cached-value", "value-a"}, values)
}

func TestPostProcessCacheBatchMapsMissFailureToOriginalInput(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	seed := cache.begin()
	_, err := seed.process(context.Background(), identity, "cached", constantPostProcess("cached-value"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, seed)

	tx := cache.begin()
	_, err = tx.processBatch(
		context.Background(),
		identity,
		[]string{"cached", "first-miss", "first-miss", "failing-miss"},
		func(context.Context, []string) ([]string, error) {
			return nil, &PostProcessBatchError{Index: 1, Err: errors.New("failed")}
		},
	)
	var batchErr *PostProcessBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 3, batchErr.Index)
	_, err = tx.stage(context.Background())
	require.Error(t, err)
	assert.Len(t, cache.active.Load().entries, 1)
}

func TestPostProcessCacheBatchPanicFailsEveryMiss(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()

	assert.PanicsWithValue(t, "batch panic", func() {
		_, _ = tx.processBatch(
			context.Background(),
			identity,
			[]string{"first", "second"},
			func(context.Context, []string) ([]string, error) {
				panic("batch panic")
			},
		)
	})
	_, err := tx.stage(context.Background())
	require.ErrorIs(t, err, errPostProcessCacheComputePanicked)
	assert.Empty(t, cache.active.Load().entries)
}

func TestPostProcessCacheBatchCoalescesWithConcurrentSingleMiss(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	started := make(chan struct{})
	release := make(chan struct{})
	producerDone := make(chan error, 1)

	go func() {
		_, err := tx.process(context.Background(), identity, "shared", func(context.Context) (string, error) {
			close(started)
			<-release
			return "shared-value", nil
		})
		producerDone <- err
	}()
	<-started
	batchDone := make(chan struct {
		values []string
		err    error
	}, 1)
	go func() {
		values, err := tx.processBatch(
			context.Background(),
			identity,
			[]string{"shared", "owned"},
			func(_ context.Context, inputs []string) ([]string, error) {
				assert.Equal(t, []string{"owned"}, inputs)
				return []string{"owned-value"}, nil
			},
		)
		batchDone <- struct {
			values []string
			err    error
		}{values: values, err: err}
	}()
	close(release)
	require.NoError(t, <-producerDone)
	result := <-batchDone
	require.NoError(t, result.err)
	assert.Equal(t, []string{"shared-value", "owned-value"}, result.values)
	commitPostProcessCacheForTest(t, tx)
}

func TestPostProcessCacheConcurrentPublicationsRetainBothCommittedGenerations(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	first := cache.begin()
	second := cache.begin()
	_, err := first.process(context.Background(), identity, "first", constantPostProcess("first-value"))
	require.NoError(t, err)
	_, err = second.process(context.Background(), identity, "second", constantPostProcess("second-value"))
	require.NoError(t, err)
	firstPublication := stagePostProcessCacheForTest(t, first)
	secondPublication := stagePostProcessCacheForTest(t, second)

	ready := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for _, publication := range []*postProcessCachePublication{firstPublication, secondPublication} {
		go func() {
			defer wait.Done()
			<-ready
			publication.publish()
		}()
	}
	close(ready)
	wait.Wait()

	active := cache.active.Load()
	require.Len(t, active.entries, 2)
	firstValue, hasFirst := active.entries[postProcessCacheKey{identity: identity, input: "first"}]
	secondValue, hasSecond := active.entries[postProcessCacheKey{identity: identity, input: "second"}]
	require.True(t, hasFirst)
	require.True(t, hasSecond)
	assert.Equal(t, "first-value", firstValue)
	assert.Equal(t, "second-value", secondValue)
}

func TestPostProcessCacheConcurrentPublicationMergeIsPrunedByNextGeneration(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	first := cache.begin()
	second := cache.begin()
	_, err := first.process(context.Background(), identity, "first", constantPostProcess("first-value"))
	require.NoError(t, err)
	_, err = second.process(context.Background(), identity, "second", constantPostProcess("second-value"))
	require.NoError(t, err)
	firstPublication := stagePostProcessCacheForTest(t, first)
	secondPublication := stagePostProcessCacheForTest(t, second)
	require.True(t, firstPublication.publish())
	require.True(t, secondPublication.publish())
	require.Len(t, cache.active.Load().entries, 2)

	next := cache.begin()
	assertPostProcessCacheHit(t, next, identity, "second", "second-value")
	commitPostProcessCacheForTest(t, next)

	active := cache.active.Load()
	require.Len(t, active.entries, 1)
	assert.Equal(t, "second-value", active.entries[postProcessCacheKey{identity: identity, input: "second"}])
}

func TestPostProcessCachePublicationIsSingleUse(t *testing.T) {
	t.Run("publish wins", func(t *testing.T) {
		cache := newPostProcessCache()
		tx := cache.begin()
		_, err := tx.process(context.Background(), newPostProcessCacheIdentity(), "input", constantPostProcess("value"))
		require.NoError(t, err)
		publication := stagePostProcessCacheForTest(t, tx)
		assert.True(t, publication.publish())
		assert.False(t, publication.publish())
		assert.False(t, publication.abort())
		require.Len(t, cache.active.Load().entries, 1)
	})

	t.Run("abort wins", func(t *testing.T) {
		cache := newPostProcessCache()
		tx := cache.begin()
		_, err := tx.process(context.Background(), newPostProcessCacheIdentity(), "input", constantPostProcess("value"))
		require.NoError(t, err)
		publication := stagePostProcessCacheForTest(t, tx)
		assert.True(t, publication.abort())
		assert.False(t, publication.abort())
		assert.False(t, publication.publish())
		assert.Empty(t, cache.active.Load().entries)
	})
}

func TestPostProcessCacheCallerMutationCannotChangeCachedString(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	tx := cache.begin()
	value, err := tx.process(context.Background(), identity, "input", constantPostProcess("immutable"))
	require.NoError(t, err)
	commitPostProcessCacheForTest(t, tx)

	mutable := []byte(value)
	mutable[0] = 'X'
	hit := cache.begin()
	assertPostProcessCacheHit(t, hit, identity, "input", "immutable")
}

func TestPostProcessCacheRejectsInvalidCallsAndClosedTransactions(t *testing.T) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()

	missingIdentity := cache.begin()
	_, err := missingIdentity.process(context.Background(), nil, "input", constantPostProcess("value"))
	require.ErrorIs(t, err, errPostProcessCacheIdentityMissing)
	_, err = missingIdentity.stage(context.Background())
	require.ErrorIs(t, err, errPostProcessCacheIdentityMissing)

	missingCompute := cache.begin()
	_, err = missingCompute.process(context.Background(), identity, "input", nil)
	require.ErrorIs(t, err, errPostProcessCacheComputeMissing)
	_, err = missingCompute.stage(context.Background())
	require.ErrorIs(t, err, errPostProcessCacheComputeMissing)

	aborted := cache.begin()
	aborted.abort()
	_, err = aborted.process(context.Background(), identity, "input", constantPostProcess("value"))
	require.ErrorIs(t, err, errPostProcessCacheTransactionClosed)

	staged := cache.begin()
	_, err = staged.process(context.Background(), identity, "input", constantPostProcess("value"))
	require.NoError(t, err)
	publication := stagePostProcessCacheForTest(t, staged)
	_, err = staged.process(context.Background(), identity, "other", constantPostProcess("value"))
	require.ErrorIs(t, err, errPostProcessCacheTransactionClosed)
	publication.abort()
	staged.abort()
}

type postProcessSecondCauseContext struct {
	calls atomic.Int32
	err   error
}

func (*postProcessSecondCauseContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (*postProcessSecondCauseContext) Done() <-chan struct{} {
	return nil
}

func (c *postProcessSecondCauseContext) Err() error {
	if c.calls.Add(1) >= 2 {
		return c.err
	}
	return nil
}

func (*postProcessSecondCauseContext) Value(any) any {
	return nil
}

type postProcessWaitSignalContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *postProcessWaitSignalContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func constantPostProcess(output string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		return output, nil
	}
}

func assertPostProcessCacheHit(
	t *testing.T,
	tx *postProcessCacheTransaction,
	identity *postProcessCacheIdentity,
	input string,
	want string,
) {
	t.Helper()
	value, err := tx.process(context.Background(), identity, input, func(context.Context) (string, error) {
		return "", fmt.Errorf("cache miss for %q", input)
	})
	require.NoError(t, err)
	assert.Equal(t, want, value)
}

func stagePostProcessCacheForTest(
	t *testing.T,
	tx *postProcessCacheTransaction,
) *postProcessCachePublication {
	t.Helper()
	publication, err := tx.stage(context.Background())
	require.NoError(t, err)
	return publication
}

func commitPostProcessCacheForTest(t *testing.T, tx *postProcessCacheTransaction) {
	t.Helper()
	publication := stagePostProcessCacheForTest(t, tx)
	require.True(t, publication.publish())
}

func BenchmarkPostProcessCacheHit(b *testing.B) {
	cache := newPostProcessCache()
	identity := newPostProcessCacheIdentity()
	seed := cache.begin()
	_, err := seed.process(context.Background(), identity, "section", constantPostProcess("processed"))
	if err != nil {
		b.Fatal(err)
	}
	publication, err := seed.stage(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if !publication.publish() {
		b.Fatal("publishing seed generation")
	}
	tx := cache.begin()
	ctx := context.Background()
	miss := func(context.Context) (string, error) {
		return "", errors.New("unexpected miss")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		value, processErr := tx.process(ctx, identity, "section", miss)
		if processErr != nil || value != "processed" {
			b.Fatalf("cache hit = %q, %v", value, processErr)
		}
	}
}
