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
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalVectorExecutionCompletesEachItemOnce(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)

	require.NoError(t, execution.Begin(0))
	release, err := execution.BeginIncrementalExecution(execution.items[0].ctx, "test")
	require.NoError(t, err)
	release()
	require.NoError(t, execution.End(0, "a"))
	require.NoError(t, execution.Begin(1))
	require.NoError(t, execution.End(1, "b"))
	require.NoError(t, execution.finish())
}

func TestIncrementalVectorLifecycleViolationIsTerminal(t *testing.T) {
	tests := []struct {
		name    string
		violate func(*testing.T, *incrementalVectorExecution) error
	}{
		{
			name: "end before begin",
			violate: func(_ *testing.T, execution *incrementalVectorExecution) error {
				return execution.End(0, "")
			},
		},
		{
			name: "overlapping begin",
			violate: func(t *testing.T, execution *incrementalVectorExecution) error {
				t.Helper()
				require.NoError(t, execution.Begin(0))
				return execution.Begin(1)
			},
		},
		{
			name: "wrong end",
			violate: func(t *testing.T, execution *incrementalVectorExecution) error {
				t.Helper()
				require.NoError(t, execution.Begin(0))
				return execution.End(1, "")
			},
		},
		{
			name: "duplicate begin",
			violate: func(t *testing.T, execution *incrementalVectorExecution) error {
				t.Helper()
				require.NoError(t, execution.Begin(0))
				require.NoError(t, execution.End(0, ""))
				return execution.Begin(0)
			},
		},
		{
			name: "incomplete finalization",
			violate: func(t *testing.T, execution *incrementalVectorExecution) error {
				t.Helper()
				require.NoError(t, execution.Begin(0))
				require.NoError(t, execution.End(0, ""))
				return execution.finish()
			},
		},
		{
			name: "finalization while active",
			violate: func(t *testing.T, execution *incrementalVectorExecution) error {
				t.Helper()
				require.NoError(t, execution.Begin(0))
				return execution.finish()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := testIncrementalVectorExecution(t, 2)
			require.Error(t, test.violate(t, execution))
			require.Error(t, execution.Begin(1))
			require.Error(t, execution.finish())
			execution.Abort(int(execution.active.Load()), errors.New("test cleanup"))
		})
	}
}

func TestIncrementalVectorExecutionDrainsActiveCapability(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)

	require.NoError(t, execution.Begin(0))
	release, err := execution.BeginIncrementalExecution(execution.items[0].ctx, "retained")
	require.NoError(t, err)
	ended := make(chan error, 1)
	go func() { ended <- execution.End(0, "") }()
	release()
	require.NoError(t, <-ended)
	require.NoError(t, execution.Begin(1))
	require.NoError(t, execution.End(1, ""))
	require.NoError(t, execution.finish())
}

func TestIncrementalVectorStoreInvocationDrainsBeforeEnd(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	view := &incrementalVectorResourceView{execution: execution, index: 0}
	view.seal = view

	require.NoError(t, execution.Begin(0))
	_, release, err := view.BeginStoreInvocation(execution.items[0].ctx)
	require.NoError(t, err)
	ended := make(chan error, 1)
	go func() { ended <- execution.End(0, "") }()

	writerWaiting := false
	for range 100_000 {
		if !execution.callGate.TryRLock() {
			writerWaiting = true
			break
		}
		execution.callGate.RUnlock()
		runtime.Gosched()
	}
	require.True(t, writerWaiting, "End never reached the revocation gate")
	select {
	case <-ended:
		t.Fatal("End returned before the store invocation drained")
	default:
	}
	release()
	require.NoError(t, <-ended)
	require.NoError(t, execution.finish())
}

func TestIncrementalVectorExecutionAbortIsTerminal(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)

	require.NoError(t, execution.Begin(0))
	execution.Abort(0, errors.New("failed"))
	require.Error(t, execution.End(0, ""))
	require.Error(t, execution.Begin(0))
	require.Error(t, execution.finish())
}

func TestIncrementalVectorRecorderKeepsItemEffectsSeparate(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)
	first := &incrementalVectorRecorder{execution: execution, index: 0}
	second := &incrementalVectorRecorder{execution: execution, index: 1}

	require.NoError(t, execution.Begin(0))
	first.Unique("cell", "a", "first")
	require.NoError(t, execution.End(0, ""))
	require.NoError(t, execution.Begin(1))
	second.Unique("cell", "b", "second")
	require.NoError(t, execution.End(1, ""))

	assert.Equal(t, []incrementalContribution{{Cell: "cell", Key: "a", Value: "first"}},
		execution.items[0].recorder.unique)
	assert.Equal(t, []incrementalContribution{{Cell: "cell", Key: "b", Value: "second"}},
		execution.items[1].recorder.unique)
}

func TestIncrementalVectorExecutionRejectsForeignContext(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	require.NoError(t, execution.Begin(0))
	_, err := execution.BeginIncrementalExecution(context.Background(), "foreign")
	require.Error(t, err)
	require.Error(t, execution.End(0, ""))
	require.Error(t, execution.finish())
}

func TestIncrementalVectorCapabilitiesRejectNilContext(t *testing.T) {
	var nilContext context.Context
	tests := map[string]func(*incrementalVectorExecution) error{
		"lease": func(execution *incrementalVectorExecution) error {
			_, err := execution.items[0].lease.BeginIncrementalExecution(nilContext, "test")
			return err
		},
		"native preflight": func(execution *incrementalVectorExecution) error {
			return execution.items[0].lease.BeforeIncrementalNativeCall(nilContext)
		},
		"store invocation": func(execution *incrementalVectorExecution) error {
			view := &incrementalVectorResourceView{execution: execution, index: 0}
			view.seal = view
			_, _, err := view.BeginStoreInvocation(nilContext)
			return err
		},
		"store read": func(execution *incrementalVectorExecution) error {
			view := &incrementalVectorResourceView{execution: execution, index: 0}
			view.seal = view
			_, err := view.ListContext(nilContext, "routes", nil)
			return err
		},
	}
	for name, invoke := range tests {
		t.Run(name, func(t *testing.T) {
			execution := testIncrementalVectorExecution(t, 1)
			require.NoError(t, execution.Begin(0))
			require.Error(t, invoke(execution))
			require.Error(t, execution.End(0, ""))
			require.Error(t, execution.finish())
		})
	}
}

func TestIncrementalVectorCapabilityCannotCrossItem(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)
	first := &incrementalVectorRecorder{execution: execution, index: 0}

	require.NoError(t, execution.Begin(0))
	require.NoError(t, execution.End(0, ""))
	require.NoError(t, execution.Begin(1))
	first.Publish("cell", "key", "poison")
	require.Error(t, execution.End(1, ""))
	require.Error(t, execution.finish())
	assert.Empty(t, execution.items[0].recorder.published)
	assert.Empty(t, execution.items[1].recorder.published)
}

func TestIncrementalVectorExecutionLeaseAuthenticatesActiveItemContext(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)

	require.NoError(t, execution.Begin(0))
	require.NoError(t, execution.ValidateIncrementalResourceInvocation(execution.items[0].ctx))
	require.NoError(t, execution.End(0, ""))
	require.NoError(t, execution.Begin(1))
	require.NoError(t, execution.ValidateIncrementalResourceInvocation(execution.items[1].ctx))
	require.NoError(t, execution.End(1, ""))
	require.NoError(t, execution.finish())
}

func TestIncrementalVectorExecutionLeaseRejectsStaleItemContext(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)

	require.NoError(t, execution.Begin(0))
	require.NoError(t, execution.End(0, ""))
	require.NoError(t, execution.Begin(1))
	require.Error(t, execution.ValidateIncrementalResourceInvocation(execution.items[0].ctx))
	require.Error(t, execution.End(1, ""))
	require.Error(t, execution.finish())
}

func TestIncrementalVectorExecutionFinalizesOnce(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	require.NoError(t, execution.Begin(0))
	require.NoError(t, execution.End(0, "first"))
	require.NoError(t, execution.finish())
	require.ErrorContains(t, execution.finish(), "already finalized")
}

func TestIncrementalVectorExecutionHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	execution := testIncrementalVectorExecutionContext(t, ctx, 1)
	require.NoError(t, execution.Begin(0))
	cancel()
	require.ErrorIs(t, execution.End(0, ""), context.Canceled)
	require.Error(t, execution.finish())
}

func TestIncrementalVectorAbortDrainsActiveCapability(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	require.NoError(t, execution.Begin(0))
	release, err := execution.current(0)
	require.NoError(t, err)
	aborted := make(chan struct{})
	go func() {
		execution.Abort(0, errors.New("abort"))
		close(aborted)
	}()
	writerWaiting := false
	for range 100_000 {
		if !execution.callGate.TryRLock() {
			writerWaiting = true
			break
		}
		execution.callGate.RUnlock()
		runtime.Gosched()
	}
	require.True(t, writerWaiting, "Abort never reached the revocation gate")
	select {
	case <-aborted:
		t.Fatal("Abort returned before the active capability drained")
	default:
	}
	release()
	<-aborted
	require.Error(t, execution.finish())
}

func TestFinishIncrementalVectorRenderPublishesIndependentTypedResults(t *testing.T) {
	session := &incrementalRenderSession{
		freshResults: map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted: map[incremental.QueryKey][]incrementalHTTPEffect{},
	}
	execution := testIncrementalVectorExecution(t, 2)
	execution.session = session
	for index, key := range []string{"a", "b"} {
		execution.items[index].prepared = &preparedIncrementalComponent{
			queryKey:  incremental.NewQueryKey(key),
			component: execution.component,
		}
		execution.items[index].http = &incrementalHTTPFetcher{
			effects: map[uint64]incrementalHTTPEffect{},
		}
		require.NoError(t, execution.Begin(index))
		require.NoError(t, execution.End(index, []string{"first", "second"}[index]))
	}
	vector := &preparedIncrementalVectorRender{execution: execution}
	encoded, err := session.finishComponentVectorRender(vector)
	require.NoError(t, err)
	assert.JSONEq(t, `{"text":"first"}`, encoded[0])
	assert.JSONEq(t, `{"text":"second"}`, encoded[1])
}

func TestFinishIncrementalVectorRenderIsAtomic(t *testing.T) {
	session := &incrementalRenderSession{
		freshResults: map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted: map[incremental.QueryKey][]incrementalHTTPEffect{},
	}
	execution := testIncrementalVectorExecution(t, 2)
	execution.session = session
	for index, key := range []string{"a", "b"} {
		execution.items[index].prepared = &preparedIncrementalComponent{
			queryKey:  incremental.NewQueryKey(key),
			component: execution.component,
		}
		execution.items[index].http = &incrementalHTTPFetcher{
			effects: map[uint64]incrementalHTTPEffect{},
		}
		require.NoError(t, execution.Begin(index))
		require.NoError(t, execution.End(index, []string{"first", "second"}[index]))
	}
	execution.items[1].recorder.err = errors.New("invalid second result")
	vector := &preparedIncrementalVectorRender{execution: execution}
	_, err := session.finishComponentVectorRender(vector)
	require.ErrorContains(t, err, "invalid second result")
	assert.Empty(t, session.freshResults)
	assert.Empty(t, session.httpExecuted)
}

func TestIncrementalVectorExecutionAuthenticatesSourceTransactionSelector(t *testing.T) {
	t.Run("same execution", func(t *testing.T) {
		execution := testIncrementalVectorExecution(t, 1)
		require.NoError(t, execution.ValidateIncrementalSourceTransactionSelector(execution))
	})

	t.Run("foreign execution", func(t *testing.T) {
		execution := testIncrementalVectorExecution(t, 1)
		foreign := testIncrementalVectorExecution(t, 1)
		require.ErrorContains(
			t,
			execution.ValidateIncrementalSourceTransactionSelector(foreign),
			"different authority",
		)
		assert.True(t, execution.failed.Load())
	})

	t.Run("finalized", func(t *testing.T) {
		execution := testIncrementalVectorExecution(t, 1)
		require.NoError(t, execution.Begin(0))
		require.NoError(t, execution.End(0, "complete"))
		require.NoError(t, execution.finish())
		require.ErrorContains(
			t,
			execution.ValidateIncrementalSourceTransactionSelector(execution),
			"terminal",
		)
	})

	t.Run("aborted", func(t *testing.T) {
		execution := testIncrementalVectorExecution(t, 1)
		execution.Abort(-1, errors.New("aborted"))
		require.ErrorContains(
			t,
			execution.ValidateIncrementalSourceTransactionSelector(execution),
			"terminal",
		)
	})

	t.Run("zero authority", func(t *testing.T) {
		var execution *incrementalVectorExecution
		require.ErrorContains(
			t,
			execution.ValidateIncrementalSourceTransactionSelector(execution),
			"invalid provenance",
		)
	})
}

func testIncrementalVectorExecution(t *testing.T, count int) *incrementalVectorExecution {
	t.Helper()
	return testIncrementalVectorExecutionContext(t, t.Context(), count)
}

func testIncrementalVectorExecutionContext(
	t *testing.T,
	ctx context.Context,
	count int,
) *incrementalVectorExecution {
	t.Helper()
	return testIncrementalVectorExecutionFixture(ctx, count)
}

func testIncrementalVectorExecutionFixture(
	ctx context.Context,
	count int,
) *incrementalVectorExecution {
	execution := &incrementalVectorExecution{
		session:   &incrementalRenderSession{},
		component: &incrementalComponent{name: "component", entryPoint: "entry"},
		items:     make([]incrementalVectorItemState, count),
	}
	execution.active.Store(-1)
	execution.seal = execution
	execution.ctx = ctx
	for index := range execution.items {
		state := &execution.items[index]
		state.token = &incrementalVectorItemToken{execution: execution, index: index}
		state.token.seal = state.token
		state.lease = &incrementalVectorItemLease{token: state.token}
		state.lease.seal = state.lease
		state.ctx = context.WithValue(
			templating.WithIncrementalExecutionLease(
				templating.WithImmutableResourceInputs(ctx),
				state.lease,
			),
			incrementalVectorExecutionContextKey{},
			state.token,
		)
	}
	return execution
}
