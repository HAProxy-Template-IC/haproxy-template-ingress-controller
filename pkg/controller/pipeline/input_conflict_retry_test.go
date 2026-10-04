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

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
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

// The HTTP store's acceptance raises no watch event, so a reconcile that lost
// the race to a sibling render accepting the same content has no next trigger
// (#199: the follower warmer beat the new leader's first render).
func TestAReconcileThatLosesTheHTTPAcceptanceRaceIsRetried(t *testing.T) {
	attempts := 0
	want := &PipelineResult{HAProxyConfig: "settled"}

	result, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			if attempts == 1 {
				return nil, nil, fmt.Errorf("committing validated render inputs: HTTP source http://x %w",
					httpstore.ErrInputsMoved)
			}
			return want, &validation.ValidationResult{Valid: true}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Same(t, want, result)
}

// A cold first render whose graph cache lost to a concurrent cold render
// (the warmer's, on the replica that just became leader) failed with
// ErrCommitConflict and, with no trigger behind it, the new leader never
// published a config: e2e setup timed out on main (#207).
func TestAReconcileThatLosesTheColdGraphRaceIsRetried(t *testing.T) {
	attempts := 0
	want := &PipelineResult{HAProxyConfig: "settled"}

	result, _, err := settleInputConflicts(
		t.Context(), nil, rendercontext.RenderModeReconcile,
		func() (*PipelineResult, *validation.ValidationResult, error) {
			attempts++
			if attempts == 1 {
				return nil, nil, fmt.Errorf("committing validated render inputs: %w",
					incremental.ErrCommitConflict)
			}
			return want, &validation.ValidationResult{Valid: true}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Same(t, want, result)
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

// releasingInputTransaction drops what it holds when Commit runs, as the
// render's combined transaction does, so only a decision taken before the
// commit still sees the candidates.
type releasingInputTransaction struct {
	candidates bool
	commitErr  error
}

func (f *releasingInputTransaction) HasCandidates() bool { return f.candidates }
func (f *releasingInputTransaction) Abort()              {}
func (f *releasingInputTransaction) Commit(context.Context) error {
	f.candidates = false
	return f.commitErr
}

// A render that read new external content and lost the commit race must not
// be deployed: its acceptance was checked against inputs that moved. The guard
// asked the transaction after Commit had released it, so it never fired.
func TestALostCommitRaceWithCandidatesFailsTheRender(t *testing.T) {
	pipeline := &Pipeline{}
	lost := &releasingInputTransaction{candidates: true, commitErr: incremental.ErrRevisionConflict}

	pipelineErr := pipeline.commitInputs(t.Context(), lost, nil)

	require.NotNil(t, pipelineErr, "a candidate render that lost the commit race was kept for deployment")
	require.ErrorIs(t, pipelineErr.Cause, incremental.ErrRevisionConflict)
	require.ErrorIs(t, pipelineErr.Cause, errCandidateAcceptanceLost)
	assert.True(t, inputsMovedUnderTheRender(pipelineErr.Cause, rendercontext.RenderModeReconcile),
		"the reconcile must re-render instead of giving up")
}

// A reconcile whose new content lost the race deploys a render without that
// content right away. Failing the reconcile instead left the fleet without the
// rollout's endpoint change for 1.8 s while a churning cluster kept beating
// each acceptance attempt (#278).
func TestALostCandidateRaceRendersAgainWithoutTheCandidates(t *testing.T) {
	var withheld []bool
	want := &PipelineResult{HAProxyConfig: "without the page"}

	result, _, err := renderWithholdingLostCandidates(t.Context(), rendercontext.RenderModeReconcile,
		func(ctx context.Context) (*PipelineResult, *validation.ValidationResult, error) {
			withheld = append(withheld, controllerhttpstore.CandidatesWithheld(ctx))
			if len(withheld) == 1 {
				return nil, nil, fmt.Errorf("%w: %w", errCandidateAcceptanceLost, incremental.ErrRevisionConflict)
			}
			return want, nil, nil
		})

	require.NoError(t, err)
	assert.Same(t, want, result)
	assert.Equal(t, []bool{false, true}, withheld)
}

// A critical source cannot render without its content, so the lost race goes
// back to the settle loop, which retries the acceptance.
func TestACriticalWithheldSourceKeepsSettlingTheRace(t *testing.T) {
	lost := fmt.Errorf("%w: %w", errCandidateAcceptanceLost, incremental.ErrRevisionConflict)
	calls := 0
	_, _, err := renderWithholdingLostCandidates(t.Context(), rendercontext.RenderModeReconcile,
		func(context.Context) (*PipelineResult, *validation.ValidationResult, error) {
			calls++
			if calls == 1 {
				return nil, nil, lost
			}
			return nil, nil, fmt.Errorf("rendering: %w", controllerhttpstore.ErrCandidateWithheld)
		})

	require.ErrorIs(t, err, errCandidateAcceptanceLost)
	assert.True(t, inputsMovedUnderTheRender(err, rendercontext.RenderModeReconcile))
	assert.Equal(t, 2, calls)
}

// Admission answers for the object under review and deploys nothing, so it
// keeps settling the race instead of judging a render without the content.
func TestAdmissionDoesNotWithholdLostCandidates(t *testing.T) {
	calls := 0
	_, _, err := renderWithholdingLostCandidates(t.Context(), rendercontext.RenderModeAdmission,
		func(context.Context) (*PipelineResult, *validation.ValidationResult, error) {
			calls++
			return nil, nil, fmt.Errorf("%w: %w", errCandidateAcceptanceLost, incremental.ErrRevisionConflict)
		})

	require.ErrorIs(t, err, errCandidateAcceptanceLost)
	assert.Equal(t, 1, calls)
}

// Without candidates the same lost race keeps the render.
func TestALostCommitRaceWithoutCandidatesKeepsTheRender(t *testing.T) {
	pipeline := &Pipeline{}
	lost := &releasingInputTransaction{commitErr: incremental.ErrRevisionConflict}

	require.Nil(t, pipeline.commitInputs(t.Context(), lost, nil))
}

// Losing the cache is not losing the render. Failing here starved the fleet:
// under a burst, conflicts arrive faster than renders finish and every
// reconcile fails, measured at 21 in a row and 176s without a successful
// render while the cluster waited for routes created minutes earlier.
func TestAConflictWithNothingExternalToAcceptKeepsTheRender(t *testing.T) {
	err := fmt.Errorf("committing validated render inputs: %w", incremental.ErrRevisionConflict)

	assert.True(t, commitConflictLeavesOutputUsable(err, false))
}

// A cold render whose graph cache lost to a concurrent cold render is in the
// same position: its output describes inputs that still hold, only the cache
// went to the other session. With candidates the content was not accepted, so
// the render must not be deployed; the renderer accepts it without the cache
// before it ever reports this conflict.
func TestAColdCacheRaceWithNothingExternalToAcceptKeepsTheRender(t *testing.T) {
	err := fmt.Errorf("committing validated render inputs: %w", incremental.ErrCommitConflict)

	assert.True(t, commitConflictLeavesOutputUsable(err, false))
	assert.False(t, commitConflictLeavesOutputUsable(err, true))
}

// Content fetched while another render accepted the same source was checked
// against a store state that has since moved.
func TestAnHTTPInputsMovedRaceWithCandidatesFails(t *testing.T) {
	err := fmt.Errorf("preparing render inputs: %w", httpstore.ErrInputsMoved)

	assert.True(t, commitConflictLeavesOutputUsable(err, false))
	assert.False(t, commitConflictLeavesOutputUsable(err, true))
}

// A render accepting external content must still fail: the commit decides the
// store's accepted version of something fetched over the network, and the
// render gate cannot undo that acceptance afterwards.
func TestAConflictWhileAcceptingExternalContentStillFails(t *testing.T) {
	err := fmt.Errorf("committing validated render inputs: %w", incremental.ErrRevisionConflict)

	assert.False(t, commitConflictLeavesOutputUsable(err, true))
}

// Only the input race is forgiven. Any other commit failure is a real failure.
func TestANonConflictCommitFailureIsNeverForgiven(t *testing.T) {
	err := errors.New("the store rejected the write")

	assert.False(t, commitConflictLeavesOutputUsable(err, false))
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
