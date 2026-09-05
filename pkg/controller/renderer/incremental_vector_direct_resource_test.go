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
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type directVectorResourceFixture struct {
	execution *incrementalVectorExecution
	view      *incrementalVectorResourceView
	reader    *batchCapabilityTrackingReader
	ctx       context.Context
}

type directVectorReflectKeySource []reflect.Value

type directVectorPanicReader struct{}

var nilDirectVectorResourceContext context.Context

func (s directVectorReflectKeySource) Len() int { return len(s) }

func (directVectorReflectKeySource) Value(int) any {
	panic("boxed lookup-key access")
}

func (s directVectorReflectKeySource) ReflectValue(index int) reflect.Value {
	return s[index]
}

func (*directVectorPanicReader) Input(
	incremental.InputKey,
) (value []byte, found bool, err error) {
	panic("direct resource read panic")
}

func (*directVectorPanicReader) ExactInput(incremental.InputKey) (incremental.Input, error) {
	panic("direct resource read panic")
}

func (*directVectorPanicReader) Query(context.Context, incremental.QueryKey) ([]byte, error) {
	panic("direct resource read panic")
}

func newDirectVectorResourceFixture(t *testing.T) *directVectorResourceFixture {
	t.Helper()
	execution := testIncrementalVectorExecution(t, 1)
	execution.session.resourceProofs = map[incremental.InputKey]incremental.Input{}
	reader := newBatchCapabilityTrackingReader("direct")
	execution.items[0].prepared = &preparedIncrementalComponent{reader: reader}
	execution.items[0].derived = rendercontext.NewDerivedResourceView()
	view := &incrementalVectorResourceView{execution: execution, index: -1}
	view.seal = view
	require.NoError(t, execution.Begin(0))
	return &directVectorResourceFixture{
		execution: execution,
		view:      view,
		reader:    reader,
		ctx:       execution.items[0].ctx,
	}
}

func (f *directVectorResourceFixture) begin(
	t *testing.T,
) rendercontext.DirectBoundStoreInvocation {
	t.Helper()
	invocation, err := f.view.BeginDirectBoundStoreInvocation(f.ctx, f.execution)
	require.NoError(t, err)
	return invocation
}

func TestIncrementalVectorDirectBoundResourceRecordsExactRead(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	invocation := fixture.begin(t)

	items, err := fixture.view.ListDirectBound(
		fixture.ctx, invocation, "routes", nil,
	)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "direct", items[0].(map[string]any)["generation"])
	assert.Equal(t, int64(1), fixture.reader.exactReads.Load())
	proof, observed := fixture.execution.session.resourceProofs[fixture.reader.input.Key]
	require.True(t, observed)
	assert.Equal(t, fixture.reader.input.Revision, proof.Revision)
	assert.Equal(t, fixture.reader.input.Found, proof.Found)
	assert.Equal(t, fixture.reader.input.Value, proof.Value)

	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	require.NoError(t, fixture.execution.End(0, "stable"))
	require.NoError(t, fixture.execution.finish())
}

func TestIncrementalVectorDirectBoundResourceNormalizesReflectionValues(t *testing.T) {
	type namedString string
	keys := directVectorReflectKeySource{
		reflect.ValueOf(any(namedString("default"))),
		reflect.ValueOf(any(int64(42))),
		reflect.Value{},
	}

	canonical, err := (&incrementalVectorResourceView{}).NormalizeLookupKeySource("routes", keys)
	require.NoError(t, err)
	assert.Equal(t, []string{"default", "42", ""}, canonical)
}

func TestIncrementalVectorDirectBoundResourceRejectsRetainedCall(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	invocation := fixture.begin(t)
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))

	_, err := fixture.view.ListDirectBound(fixture.ctx, invocation, "routes", nil)
	require.ErrorContains(t, err, "stale invocation")
	assert.Zero(t, fixture.reader.exactReads.Load())
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceRejectsReadReplay(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	invocation := fixture.begin(t)
	copyOfInvocation := invocation
	_, err := fixture.view.ListDirectBound(fixture.ctx, invocation, "routes", nil)
	require.NoError(t, err)

	_, err = fixture.view.ListDirectBound(fixture.ctx, copyOfInvocation, "routes", nil)
	require.ErrorContains(t, err, "already consumed")
	assert.Equal(t, int64(1), fixture.reader.exactReads.Load())
	assert.Equal(t, int64(1), fixture.execution.inflight.Load())
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceRejectsConcurrentDuplicateRead(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	reader := newBatchCapabilityBlockingReader("direct")
	fixture.execution.items[0].prepared.reader = reader
	invocation := fixture.begin(t)
	readDone := make(chan error, 1)
	go func() {
		_, err := fixture.view.ListDirectBound(fixture.ctx, invocation, "routes", nil)
		readDone <- err
	}()
	<-reader.started

	_, err := fixture.view.ListDirectBound(fixture.ctx, invocation, "routes", nil)
	require.ErrorContains(t, err, "already consumed")
	assert.Equal(t, int64(1), fixture.execution.inflight.Load())
	close(reader.release)
	require.NoError(t, <-readDone)
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceEndRejectsActiveRead(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	reader := newBatchCapabilityBlockingReader("direct")
	fixture.execution.items[0].prepared.reader = reader
	invocation := fixture.begin(t)
	readDone := make(chan error, 1)
	go func() {
		_, err := fixture.view.ListDirectBound(fixture.ctx, invocation, "routes", nil)
		readDone <- err
	}()
	<-reader.started

	err := fixture.view.EndDirectBoundStoreInvocation(invocation)
	require.ErrorContains(t, err, "still reading")
	assert.Equal(t, int64(1), fixture.execution.inflight.Load())
	assert.Equal(
		t,
		directInvocationState(invocation.Generation(), incrementalVectorDirectInvocationReading),
		fixture.execution.directInvocations[invocation.Slot()].Load(),
	)
	close(reader.release)
	require.NoError(t, <-readDone)
	assert.Equal(
		t,
		directInvocationState(invocation.Generation(), incrementalVectorDirectInvocationDone),
		fixture.execution.directInvocations[invocation.Slot()].Load(),
	)
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceReadPanicCompletesInvocation(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	fixture.execution.items[0].prepared.reader = &directVectorPanicReader{}
	invocation := fixture.begin(t)

	assert.PanicsWithValue(t, "direct resource read panic", func() {
		_, _ = fixture.view.ListDirectBound(fixture.ctx, invocation, "routes", nil)
	})
	assert.Equal(
		t,
		directInvocationState(invocation.Generation(), incrementalVectorDirectInvocationDone),
		fixture.execution.directInvocations[invocation.Slot()].Load(),
	)
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	assert.Zero(t, fixture.execution.inflight.Load())
	require.NoError(t, fixture.execution.End(0, "stable"))
}

func TestIncrementalVectorDirectBoundResourceZeroReadEnd(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	invocation := fixture.begin(t)

	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	assert.Zero(t, fixture.reader.exactReads.Load())
	assert.Zero(t, fixture.execution.inflight.Load())
	require.NoError(t, fixture.execution.End(0, "stable"))
	require.NoError(t, fixture.execution.finish())
}

func TestIncrementalVectorDirectBoundResourceRejectsDoubleEnd(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	invocation := fixture.begin(t)
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	assert.Zero(t, fixture.execution.inflight.Load())

	err := fixture.view.EndDirectBoundStoreInvocation(invocation)
	require.ErrorContains(t, err, "stale invocation")
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceRejectsForeignInvocation(t *testing.T) {
	fixtureA := newDirectVectorResourceFixture(t)
	fixtureB := newDirectVectorResourceFixture(t)
	invocationA := fixtureA.begin(t)
	invocationB := fixtureB.begin(t)

	err := fixtureA.view.EndDirectBoundStoreInvocation(invocationB)
	require.ErrorContains(t, err, "foreign incremental component vector item")
	assert.Equal(t, int64(1), fixtureA.execution.inflight.Load())
	assert.Equal(t, int64(1), fixtureB.execution.inflight.Load())

	require.NoError(t, fixtureA.view.EndDirectBoundStoreInvocation(invocationA))
	require.NoError(t, fixtureB.view.EndDirectBoundStoreInvocation(invocationB))
	assert.Zero(t, fixtureA.execution.inflight.Load())
	assert.Zero(t, fixtureB.execution.inflight.Load())
	require.Error(t, fixtureA.execution.End(0, "poisoned"))
	require.NoError(t, fixtureB.execution.End(0, "stable"))
}

func TestIncrementalVectorDirectBoundResourceRejectsABAReplay(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	first := fixture.begin(t)
	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(first))
	second := fixture.begin(t)
	require.Equal(t, first.Slot(), second.Slot())
	require.NotEqual(t, first.Generation(), second.Generation())

	_, err := fixture.view.ListDirectBound(fixture.ctx, first, "routes", nil)
	require.ErrorContains(t, err, "stale invocation")
	err = fixture.view.EndDirectBoundStoreInvocation(first)
	require.Error(t, err)
	assert.Equal(t, int64(1), fixture.execution.inflight.Load())
	assert.Equal(
		t,
		directInvocationState(second.Generation(), incrementalVectorDirectInvocationOpen),
		fixture.execution.directInvocations[second.Slot()].Load(),
	)

	require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(second))
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceExhaustionDoesNotRetain(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	invocations := make([]rendercontext.DirectBoundStoreInvocation, len(fixture.execution.directInvocations))
	for index := range invocations {
		invocations[index] = fixture.begin(t)
	}
	assert.Equal(t, int64(len(invocations)), fixture.execution.inflight.Load())

	const attempts = 16
	start := make(chan struct{})
	errorsByAttempt := make(chan error, attempts)
	var callers sync.WaitGroup
	callers.Add(attempts)
	for range attempts {
		go func() {
			defer callers.Done()
			<-start
			_, err := fixture.view.BeginDirectBoundStoreInvocation(fixture.ctx, fixture.execution)
			errorsByAttempt <- err
		}()
	}
	close(start)
	callers.Wait()
	close(errorsByAttempt)
	for err := range errorsByAttempt {
		require.Error(t, err)
	}
	assert.Equal(t, int64(len(invocations)), fixture.execution.inflight.Load())
	for slot := range fixture.execution.directInvocations {
		assert.Equal(
			t,
			directInvocationState(
				invocations[slot].Generation(), incrementalVectorDirectInvocationOpen,
			),
			fixture.execution.directInvocations[slot].Load(),
		)
	}

	for _, invocation := range invocations {
		require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
	}
	assert.Zero(t, fixture.execution.inflight.Load())
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceRejectsGenerationWrap(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	fixture.execution.directInvocationSequence.Store(incrementalVectorDirectInvocationMaxGeneration)

	_, err := fixture.view.BeginDirectBoundStoreInvocation(fixture.ctx, fixture.execution)
	require.ErrorContains(t, err, "exhausted its invocation generations")
	assert.Zero(t, fixture.execution.inflight.Load())
	for slot := range fixture.execution.directInvocations {
		assert.Zero(t, fixture.execution.directInvocations[slot].Load())
	}
	require.Error(t, fixture.execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceRejectsInvalidBegin(t *testing.T) {
	tests := []struct {
		name  string
		begin func(*directVectorResourceFixture) error
	}{
		{
			name: "nil context",
			begin: func(fixture *directVectorResourceFixture) error {
				_, err := fixture.view.BeginDirectBoundStoreInvocation(
					nilDirectVectorResourceContext, fixture.execution,
				)
				return err
			},
		},
		{
			name: "foreign execution lease",
			begin: func(fixture *directVectorResourceFixture) error {
				foreign := testIncrementalVectorExecution(t, 1)
				_, err := fixture.view.BeginDirectBoundStoreInvocation(fixture.ctx, foreign)
				return err
			},
		},
		{
			name: "foreign item context",
			begin: func(fixture *directVectorResourceFixture) error {
				foreign := testIncrementalVectorExecution(t, 1)
				_, err := fixture.view.BeginDirectBoundStoreInvocation(
					foreign.items[0].ctx, fixture.execution,
				)
				return err
			},
		},
		{
			name: "canceled context",
			begin: func(fixture *directVectorResourceFixture) error {
				ctx, cancel := context.WithCancel(fixture.ctx)
				cancel()
				_, err := fixture.view.BeginDirectBoundStoreInvocation(ctx, fixture.execution)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDirectVectorResourceFixture(t)
			require.Error(t, test.begin(fixture))
			assert.Zero(t, fixture.execution.inflight.Load())
			for slot := range fixture.execution.directInvocations {
				assert.Zero(t, fixture.execution.directInvocations[slot].Load())
			}
			require.Error(t, fixture.execution.End(0, "poisoned"))
		})
	}
}

func TestIncrementalVectorDirectBoundResourceRejectsStaleItem(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)
	view := &incrementalVectorResourceView{execution: execution, index: -1}
	view.seal = view
	require.NoError(t, execution.Begin(0))

	_, err := view.BeginDirectBoundStoreInvocation(execution.items[1].ctx, execution)
	require.ErrorContains(t, err, "inactive incremental component vector item 1")
	assert.Zero(t, execution.inflight.Load())
	for slot := range execution.directInvocations {
		assert.Zero(t, execution.directInvocations[slot].Load())
	}
	require.Error(t, execution.End(0, "poisoned"))
}

func TestIncrementalVectorDirectBoundResourceDrainsBeforeTerminalTransition(t *testing.T) {
	for _, test := range []struct {
		name       string
		transition func(*incrementalVectorExecution) <-chan error
	}{
		{
			name: "end",
			transition: func(execution *incrementalVectorExecution) <-chan error {
				done := make(chan error, 1)
				go func() {
					done <- execution.End(0, "stable")
				}()
				return done
			},
		},
		{
			name: "abort",
			transition: func(execution *incrementalVectorExecution) <-chan error {
				done := make(chan error, 1)
				go func() {
					execution.Abort(0, errors.New("abort"))
					done <- nil
				}()
				return done
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDirectVectorResourceFixture(t)
			invocation := fixture.begin(t)
			done := test.transition(fixture.execution)
			waitForDirectResourceWriter(t, fixture.execution)
			select {
			case <-done:
				t.Fatal("terminal transition completed before the direct resource call drained")
			default:
			}
			require.NoError(t, fixture.view.EndDirectBoundStoreInvocation(invocation))
			require.NoError(t, <-done)
			assert.Zero(t, fixture.execution.inflight.Load())
		})
	}
}

func TestIncrementalVectorDirectBoundResourceConcurrentCalls(t *testing.T) {
	fixture := newDirectVectorResourceFixture(t)
	const callers = 32
	start := make(chan struct{})
	errorsByCaller := make(chan error, callers)
	var calls sync.WaitGroup
	calls.Add(callers)
	for range callers {
		go func() {
			defer calls.Done()
			<-start
			invocation, err := fixture.view.BeginDirectBoundStoreInvocation(
				fixture.ctx, fixture.execution,
			)
			if err != nil {
				errorsByCaller <- err
				return
			}
			_, readErr := fixture.view.ListDirectBound(
				fixture.ctx, invocation, "routes", nil,
			)
			endErr := fixture.view.EndDirectBoundStoreInvocation(invocation)
			errorsByCaller <- errors.Join(readErr, endErr)
		}()
	}
	close(start)
	calls.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		require.NoError(t, err)
	}
	assert.Equal(t, int64(callers), fixture.reader.exactReads.Load())
	assert.Zero(t, fixture.execution.inflight.Load())
	require.NoError(t, fixture.execution.End(0, "stable"))
	require.NoError(t, fixture.execution.finish())
}

func waitForDirectResourceWriter(t *testing.T, execution *incrementalVectorExecution) {
	t.Helper()
	for range 100_000 {
		if !execution.callGate.TryRLock() {
			return
		}
		execution.callGate.RUnlock()
		runtime.Gosched()
	}
	t.Fatal("terminal transition never reached the direct resource call gate")
}
