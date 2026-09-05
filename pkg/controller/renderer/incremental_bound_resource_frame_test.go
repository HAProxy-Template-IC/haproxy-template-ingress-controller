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
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncrementalVectorBoundResourceFrameUsesOneInflightGate(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	view := &incrementalVectorResourceView{execution: execution, index: -1}
	view.seal = view
	require.NoError(t, execution.Begin(0))

	invocationCtx, release, err := view.BeginBoundStoreInvocation(
		execution.items[0].ctx,
		execution,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), execution.inflight.Load())
	invocation, _ := invocationCtx.Value(
		incrementalVectorStoreInvocationContextKey{},
	).(*incrementalVectorStoreInvocation)
	require.NotNil(t, invocation)
	assert.True(t, invocation.active.Load())

	release()
	release()
	assert.Zero(t, execution.inflight.Load())
	assert.False(t, invocation.active.Load())
	require.NoError(t, execution.End(0, "stable"))
	require.NoError(t, execution.finish())
}

func TestIncrementalVectorBoundResourceFrameRejectsCrossItemContext(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 2)
	view := &incrementalVectorResourceView{execution: execution, index: -1}
	view.seal = view
	require.NoError(t, execution.Begin(0))

	_, _, err := view.BeginBoundStoreInvocation(execution.items[1].ctx, execution)
	require.ErrorContains(t, err, "used inactive incremental component vector item 1")
	require.Error(t, execution.End(0, ""))
	require.Error(t, execution.finish())
}

func TestIncrementalVectorBoundResourceFrameRejectsCrossExecutionLease(t *testing.T) {
	executionA := testIncrementalVectorExecution(t, 1)
	executionB := testIncrementalVectorExecution(t, 1)
	viewA := &incrementalVectorResourceView{execution: executionA, index: -1}
	viewA.seal = viewA
	require.NoError(t, executionA.Begin(0))
	require.NoError(t, executionB.Begin(0))

	_, _, err := viewA.BeginBoundStoreInvocation(executionA.items[0].ctx, executionB)
	require.ErrorContains(t, err, "no matching execution lease")
	require.Error(t, executionA.End(0, ""))
	require.Error(t, executionA.finish())
	require.NoError(t, executionB.End(0, "stable"))
	require.NoError(t, executionB.finish())
}

func TestIncrementalVectorBoundResourceFrameDrainsBeforeRevocation(t *testing.T) {
	execution := testIncrementalVectorExecution(t, 1)
	view := &incrementalVectorResourceView{execution: execution, index: -1}
	view.seal = view
	require.NoError(t, execution.Begin(0))
	_, release, err := view.BeginBoundStoreInvocation(execution.items[0].ctx, execution)
	require.NoError(t, err)

	ended := make(chan error, 1)
	go func() { ended <- execution.End(0, "stable") }()
	writerWaiting := false
	for range 100_000 {
		if !execution.callGate.TryRLock() {
			writerWaiting = true
			break
		}
		execution.callGate.RUnlock()
		runtime.Gosched()
	}
	require.True(t, writerWaiting, "vector revocation never reached the bound resource gate")
	select {
	case <-ended:
		t.Fatal("vector revocation completed before the bound resource frame drained")
	default:
	}
	release()
	require.NoError(t, <-ended)
	require.NoError(t, execution.finish())
}

func TestIncrementalBatchBoundResourceFrameRejectsCrossGeneration(t *testing.T) {
	session := &incrementalRenderSession{}
	batch, authority := prepareBatchCapabilityGenerationBatch(t, session)
	preparedA := prepareBatchCapabilityGenerationItem(t, session, batch, authority, "A")
	preparedB := prepareBatchCapabilityGenerationItem(t, session, batch, authority, "B")
	require.NoError(t, preparedA.activate())

	_, _, err := batch.view.BeginBoundStoreInvocation(preparedB.ctx, preparedA.lease)
	require.ErrorContains(t, err, "outside incremental component generation")
	preparedA.deactivate()
	require.Error(t, preparedA.lease.publicationError())
	assert.Error(t, session.resourceErrors.Err())
}

func TestIncrementalBatchBoundResourceFrameRejectsForeignAuthority(t *testing.T) {
	sessionA := &incrementalRenderSession{}
	batchA, _ := prepareBatchCapabilityGenerationBatch(t, sessionA)
	sessionB := &incrementalRenderSession{}
	batchB, authorityB := prepareBatchCapabilityGenerationBatch(t, sessionB)
	preparedB := prepareBatchCapabilityGenerationItem(t, sessionB, batchB, authorityB, "B")

	_, _, err := batchA.view.BeginBoundStoreInvocation(preparedB.ctx, preparedB.lease)
	require.ErrorContains(t, err, "no matching bound capability lease")
	assert.Error(t, sessionA.resourceErrors.Err())
	require.Error(t, preparedB.lease.err())
}

func TestIncrementalBatchBoundResourceFrameDrainsBeforeRevocation(t *testing.T) {
	session := &incrementalRenderSession{}
	batch, authority := prepareBatchCapabilityGenerationBatch(t, session)
	prepared := prepareBatchCapabilityGenerationItem(t, session, batch, authority, "A")
	require.NoError(t, prepared.activate())
	invocationCtx, release, err := batch.view.BeginBoundStoreInvocation(prepared.ctx, prepared.lease)
	require.NoError(t, err)
	assert.Same(t, prepared.lease, invocationCtx.Value(incrementalCapabilityInvocationContextKey{}))

	revoked := make(chan struct{})
	go func() {
		prepared.deactivate()
		close(revoked)
	}()
	for prepared.lease.state.Load() != incrementalCapabilityLeaseRevoking {
		runtime.Gosched()
	}
	select {
	case <-revoked:
		t.Fatal("batch revocation completed before the bound resource frame drained")
	default:
	}
	release()
	<-revoked
	require.NoError(t, prepared.lease.publicationError())
}

func TestIncrementalBatchBoundResourceFrameRejectsRevokedLease(t *testing.T) {
	session := &incrementalRenderSession{}
	batch, authority := prepareBatchCapabilityGenerationBatch(t, session)
	prepared := prepareBatchCapabilityGenerationItem(t, session, batch, authority, "A")
	require.NoError(t, prepared.activate())
	prepared.deactivate()

	_, _, err := batch.view.BeginBoundStoreInvocation(prepared.ctx, prepared.lease)
	require.ErrorContains(t, err, "outside incremental component generation")
	require.Error(t, prepared.lease.publicationError())
	assert.True(t, errors.Is(prepared.lease.publicationError(), err))
}
