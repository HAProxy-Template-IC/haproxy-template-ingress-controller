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

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// A conflict means a watched input moved while the render was reading it. The
// admission webhook denied operators' creates on it before this retry existed,
// because the reconcile path recovers on its next trigger and admission has no
// next trigger.
func TestARenderThatLosesTheInputRaceIsRetried(t *testing.T) {
	attempts := 0
	want := &PipelineResult{HAProxyConfig: "settled"}

	result, validationResult, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeAdmission,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			if attempts == 1 {
				return nil, nil, fmt.Errorf("committing validated render inputs: %w",
					incremental.ErrRevisionConflict)
			}
			return want, &validation.ValidationResult{Valid: true}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "the second render should have been attempted")
	assert.Same(t, want, result)
	assert.True(t, validationResult.Valid)
}

// The bound has to hold: a cluster changing continuously must not spin here
// instead of making progress, and the caller still learns why.
func TestAPersistentInputRaceStopsAtTheAttemptLimit(t *testing.T) {
	attempts := 0

	_, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			return nil, nil, fmt.Errorf("committing validated render inputs: %w",
				incremental.ErrRevisionConflict)
		})

	require.Error(t, err)
	assert.ErrorIs(t, err, incremental.ErrRevisionConflict)
	assert.Equal(t, renderInputConflictAttempts, attempts)
}

// Only the input race is retried. Re-rendering an invalid configuration would
// spend two more renders to reach the same verdict.
func TestAnOrdinaryRenderFailureIsNotRetried(t *testing.T) {
	attempts := 0
	sentinel := errors.New("template does not compile")

	_, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			return nil, nil, sentinel
		})

	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, attempts)
}

// A cancelled context stops the retry: the caller has already gone away, and a
// leader that just lost its lease must not keep rendering.
func TestACancelledRenderIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	attempts := 0

	_, _, err := settleInputConflicts(
		ctx, nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			return nil, nil, fmt.Errorf("committing validated render inputs: %w",
				incremental.ErrRevisionConflict)
		})

	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	assert.Equal(t, 1, attempts)
}

// fakeInputTransaction stands in for the render's input transaction so the
// commit-conflict decision can be exercised without a graph to race.
type fakeInputTransaction struct{ candidates, httpState bool }

func (f fakeInputTransaction) HasCandidates() bool        { return f.candidates }
func (f fakeInputTransaction) CarriesHTTPState() bool     { return f.httpState }
func (fakeInputTransaction) Commit(context.Context) error { return nil }
func (fakeInputTransaction) Abort()                       {}

// Losing the cache is not losing the render. Failing here starved the fleet:
// under a burst, conflicts arrive faster than renders finish and every
// reconcile fails, measured at 21 in a row and 176s without a successful
// render while the cluster waited for routes created minutes earlier.
func TestAConflictWithNothingExternalToAcceptKeepsTheRender(t *testing.T) {
	err := fmt.Errorf("committing validated render inputs: %w", incremental.ErrRevisionConflict)

	assert.True(t, commitConflictLeavesOutputUsable(err, fakeInputTransaction{candidates: false}))
}

// A render accepting external content must still fail: the commit decides the
// store's accepted version of something fetched over the network, and the
// render gate cannot undo that acceptance afterwards.
func TestAConflictWhileAcceptingExternalContentStillFails(t *testing.T) {
	err := fmt.Errorf("committing validated render inputs: %w", incremental.ErrRevisionConflict)

	assert.False(t, commitConflictLeavesOutputUsable(err, fakeInputTransaction{candidates: true}))
}

// Only the input race is forgiven. Any other commit failure is a real failure.
func TestANonConflictCommitFailureIsNeverForgiven(t *testing.T) {
	err := errors.New("the store rejected the write")

	assert.False(t, commitConflictLeavesOutputUsable(err, fakeInputTransaction{candidates: false}))
}

// Lease accounting is a commit too. Skipping it leaves the HTTP store counting
// references this render released, and a later render's removals then exceed
// what the store believes exists — 60 such rejections in one e2e run.
func TestAConflictCarryingLeaseAccountingStillFails(t *testing.T) {
	err := fmt.Errorf("committing validated render inputs: %w", incremental.ErrRevisionConflict)

	assert.False(t, commitConflictLeavesOutputUsable(err,
		fakeInputTransaction{candidates: false, httpState: true}))
}

// Counting attempts is the wrong bound for admission: three of them fire inside
// 25ms, faster than the commit they lose to, so an operator's update was denied
// for a race inside the controller. Admission paces its retries instead, and
// must outlive a conflict that a fourth read would settle.
func TestAdmissionOutlastsAConflictPastTheReconcileAttemptLimit(t *testing.T) {
	attempts := 0
	want := &PipelineResult{HAProxyConfig: "settled"}

	result, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeAdmission,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			if attempts <= renderInputConflictAttempts+1 {
				return nil, nil, fmt.Errorf("starting incremental render: %w",
					incremental.ErrRevisionConflict)
			}
			return want, &validation.ValidationResult{Valid: true}, nil
		})

	require.NoError(t, err)
	assert.Same(t, want, result)
	assert.Greater(t, attempts, renderInputConflictAttempts,
		"admission must not stop at the reconcile attempt limit")
}

// The pacing is still bounded: a cluster that never settles has to get an
// answer rather than hold the webhook open until the apiserver times it out.
func TestAdmissionStopsPacingWithinItsBudget(t *testing.T) {
	attempts := 0
	started := time.Now()

	_, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeAdmission,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			return nil, nil, fmt.Errorf("starting incremental render: %w",
				incremental.ErrRevisionConflict)
		})

	require.Error(t, err)
	assert.ErrorIs(t, err, incremental.ErrRevisionConflict)
	assert.Less(t, time.Since(started), admissionInputConflictBudget+time.Second,
		"the retry budget must bound how long the webhook waits")
}

// A request that is nearly out of time spends what is left on answering, not on
// another wait it cannot afford.
func TestAdmissionKeepsTheRequestDeadlineForTheAnswer(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(admissionInputConflictReserve/2))
	defer cancel()
	attempts := 0

	_, _, err := settleInputConflicts(
		ctx, nil, rendercontext.RenderModeAdmission,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			return nil, nil, fmt.Errorf("starting incremental render: %w",
				incremental.ErrRevisionConflict)
		})

	require.Error(t, err)
	assert.Equal(t, 1, attempts, "no budget was left to wait for another read")
}

// A store noticing mid-read that its snapshot moved says the same thing a
// commit conflict says, and reaches the pipeline as a different error. It
// denied the object under review instead of re-rendering: one namespace's
// Secret rotating rejected an unrelated Ingress in another, with the operator's
// own object named in the refusal.
func TestARenderWhoseSnapshotMovedIsRetried(t *testing.T) {
	attempts := 0
	want := &PipelineResult{HAProxyConfig: "settled"}

	result, validationResult, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeAdmission,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			if attempts == 1 {
				return nil, nil, fmt.Errorf(
					"rendering template 'ingress-tls-certificate-publications': resource a/b no "+
						"longer matches its pinned snapshot: its informer generation changed "+
						"before the API read: %w", stores.ErrSnapshotChanged)
			}
			return want, &validation.ValidationResult{Valid: true}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "the second render should have been attempted")
	assert.Same(t, want, result)
	assert.True(t, validationResult.Valid)
}

// A reconcile is re-triggered by the very change that moved the snapshot, so
// re-reading it inline buys nothing and delays the deploy behind up to
// renderInputConflictAttempts slow renders — long enough on a contended node
// for a rolling restart to lose its last server before the new one lands.
func TestAReconcileWhoseSnapshotMovedIsNotRetried(t *testing.T) {
	attempts := 0
	snapshotMoved := fmt.Errorf("resource a/b no longer matches its pinned snapshot: %w",
		stores.ErrSnapshotChanged)

	_, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			return nil, nil, snapshotMoved
		})

	require.ErrorIs(t, err, stores.ErrSnapshotChanged)
	assert.Equal(t, 1, attempts, "the reconcile should not have re-rendered")
}

// A commit conflict is still settled inline on a reconcile: unlike a moved
// snapshot it means this render lost a race it can win by reading the newer
// revision, and nothing else is guaranteed to trigger another attempt.
func TestAReconcileWhoseCommitConflictedIsRetried(t *testing.T) {
	attempts := 0
	want := &PipelineResult{HAProxyConfig: "settled"}

	result, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			if attempts == 1 {
				return nil, nil, fmt.Errorf("commit: %w", incremental.ErrRevisionConflict)
			}
			return want, &validation.ValidationResult{Valid: true}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Same(t, want, result)
}
