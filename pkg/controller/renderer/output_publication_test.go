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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestOutputPublicationSuccessfulCommitPublishesCompositeRoot(t *testing.T) {
	service := newOutputPublicationService(t)
	output := outputPublicationSnapshot(t, service, nil, "committed")
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	inner := &outputPublicationStagingTransaction{}

	transaction, err := service.stageOutputPublication(
		inner,
		rendercontext.RenderModeReconcile,
		generation,
		output,
	)
	require.NoError(t, err)
	require.Same(t, inner, transaction)
	storedPlan, storedOutput, publishedGeneration := outputPublicationState(service)
	assert.Nil(t, storedPlan)
	assert.Nil(t, storedOutput)
	assert.Zero(t, publishedGeneration)

	require.NoError(t, transaction.Commit(t.Context()))

	storedPlan, storedOutput, publishedGeneration = outputPublicationState(service)
	require.NotNil(t, storedPlan)
	assert.Same(t, output, storedOutput)
	assert.Equal(t, generation, publishedGeneration)
	require.NoError(t, service.outputAuthority.ValidateSnapshot(storedOutput))
	wantPlan := outputPublicationPlan(t, output)
	assert.NotSame(t, wantPlan, storedPlan)
	assert.True(t, renderplan.ExactlyEqual(wantPlan, storedPlan))
	wantArtifacts, err := output.ArtifactSnapshot()
	require.NoError(t, err)
	storedArtifacts, err := storedOutput.ArtifactSnapshot()
	require.NoError(t, err)
	assert.Same(t, wantArtifacts, storedArtifacts)
}

func TestOutputPublicationNilTransactionCommitsAuthenticatedRoot(t *testing.T) {
	service := newOutputPublicationService(t)
	output := outputPublicationSnapshot(t, service, nil, "nil-transaction")
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)

	transaction, err := service.stageOutputPublication(
		nil,
		rendercontext.RenderModeReconcile,
		generation,
		output,
	)
	require.NoError(t, err)
	require.NotNil(t, transaction)
	require.NoError(t, transaction.Commit(t.Context()))

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, output, storedOutput)
	assert.Equal(t, uint64(1), generation)
	assert.True(t, renderplan.ExactlyEqual(outputPublicationPlan(t, output), storedPlan))
}

func TestOutputPublicationAdmissionCommitNeverPublishesCompositeRoot(t *testing.T) {
	service := newOutputPublicationService(t)
	baselineOutput := outputPublicationSnapshot(t, service, nil, "baseline")
	candidateOutput := outputPublicationSnapshot(t, service, baselineOutput, "admission")
	baselinePlan := outputPublicationPlan(t, baselineOutput)
	service.rememberOwnedOutput(1, baselinePlan, baselineOutput)
	inner := &outputPublicationTestTransaction{}

	transaction, err := service.stageOutputPublication(
		inner,
		rendercontext.RenderModeAdmission,
		2,
		candidateOutput,
	)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit(t.Context()))

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, baselinePlan, storedPlan)
	assert.Same(t, baselineOutput, storedOutput)
	assert.Equal(t, uint64(1), generation)
	assert.Equal(t, int32(1), inner.commitCalls.Load())
}

func TestOutputPublicationFailuresPreserveCommittedCompositeRoot(t *testing.T) {
	rejected := errors.New("input commit rejected")
	tests := map[string]struct {
		run       func(*testing.T, RenderInputTransaction) error
		commitErr error
		wantErr   error
		wantAbort int32
	}{
		"abort": {
			run: func(t *testing.T, transaction RenderInputTransaction) error {
				t.Helper()
				transaction.Abort()
				return transaction.Commit(t.Context())
			},
			wantErr:   errPlanPublicationAborted,
			wantAbort: 1,
		},
		"inner failure": {
			run: func(t *testing.T, transaction RenderInputTransaction) error {
				t.Helper()
				return transaction.Commit(t.Context())
			},
			commitErr: rejected,
			wantErr:   rejected,
			wantAbort: 1,
		},
		"canceled commit": {
			run: func(t *testing.T, transaction RenderInputTransaction) error {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return transaction.Commit(ctx)
			},
			wantErr:   context.Canceled,
			wantAbort: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service := newOutputPublicationService(t)
			baselineOutput := outputPublicationSnapshot(t, service, nil, "baseline")
			candidateOutput := outputPublicationSnapshot(t, service, baselineOutput, name)
			baselinePlan := outputPublicationPlan(t, baselineOutput)
			service.rememberOwnedOutput(1, baselinePlan, baselineOutput)
			generation, err := service.reserveOutputGeneration()
			require.NoError(t, err)
			inner := &outputPublicationTestTransaction{commitErr: test.commitErr}
			transaction, err := service.stageOutputPublication(
				inner,
				rendercontext.RenderModeReconcile,
				generation,
				candidateOutput,
			)
			require.NoError(t, err)

			err = test.run(t, transaction)
			require.ErrorIs(t, err, test.wantErr)
			storedPlan, storedOutput, generation := outputPublicationState(service)
			assert.Same(t, baselinePlan, storedPlan)
			assert.Same(t, baselineOutput, storedOutput)
			assert.Equal(t, uint64(1), generation)
			assert.Equal(t, test.wantAbort, inner.abortCalls.Load())
		})
	}
}

func TestOutputPublicationLateOlderCommitCannotOverwriteNewerCompositeRoot(t *testing.T) {
	service := newOutputPublicationService(t)
	olderOutput := outputPublicationSnapshot(t, service, nil, "older")
	newerOutput := outputPublicationSnapshot(t, service, olderOutput, "newer")
	olderGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	newerGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	require.Greater(t, newerGeneration, olderGeneration)

	olderStarted := make(chan struct{})
	releaseOlder := make(chan struct{})
	olderInner := &outputPublicationTestTransaction{
		started: olderStarted,
		release: releaseOlder,
	}
	older, err := service.stageOutputPublication(
		olderInner,
		rendercontext.RenderModeReconcile,
		olderGeneration,
		olderOutput,
	)
	require.NoError(t, err)
	newer, err := service.stageOutputPublication(
		nil,
		rendercontext.RenderModeReconcile,
		newerGeneration,
		newerOutput,
	)
	require.NoError(t, err)

	olderResult := make(chan error, 1)
	go func() {
		olderResult <- older.Commit(t.Context())
	}()
	<-olderStarted
	require.NoError(t, newer.Commit(t.Context()))
	close(releaseOlder)
	require.ErrorIs(t, <-olderResult, errRenderOutputGenerationSuperseded)

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, newerOutput, storedOutput)
	assert.Equal(t, newerGeneration, generation)
	assert.True(t, renderplan.ExactlyEqual(outputPublicationPlan(t, newerOutput), storedPlan))
}

func TestOutputPublicationOlderMayCommitBeforeNewerCandidate(t *testing.T) {
	service := newOutputPublicationService(t)
	olderOutput := outputPublicationSnapshot(t, service, nil, "older")
	newerOutput := outputPublicationSnapshot(t, service, olderOutput, "newer")
	olderGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	newerGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	older, err := service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, olderGeneration, olderOutput,
	)
	require.NoError(t, err)
	newer, err := service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, newerGeneration, newerOutput,
	)
	require.NoError(t, err)

	require.NoError(t, older.Commit(t.Context()))
	require.NoError(t, newer.Commit(t.Context()))

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, newerOutput, storedOutput)
	assert.Equal(t, newerGeneration, generation)
	assert.True(t, renderplan.ExactlyEqual(outputPublicationPlan(t, newerOutput), storedPlan))
}

func TestEquivalentOutputPublicationsCommitInEitherOrderWithoutOverwrite(t *testing.T) {
	for _, newerFirst := range []bool{false, true} {
		name := "older first"
		if newerFirst {
			name = "newer first"
		}
		t.Run(name, func(t *testing.T) {
			service := newOutputPublicationService(t)
			olderOutput := outputPublicationSnapshot(t, service, nil, "same")
			newerOutput := outputPublicationSnapshot(t, service, nil, "same")
			equal, err := olderOutput.ExactEqual(newerOutput)
			require.NoError(t, err)
			require.True(t, equal)
			olderGeneration, err := service.reserveOutputGeneration()
			require.NoError(t, err)
			newerGeneration, err := service.reserveOutputGeneration()
			require.NoError(t, err)
			older, err := service.stageOutputPublication(
				nil, rendercontext.RenderModeReconcile, olderGeneration, olderOutput,
			)
			require.NoError(t, err)
			newer, err := service.stageOutputPublication(
				nil, rendercontext.RenderModeReconcile, newerGeneration, newerOutput,
			)
			require.NoError(t, err)

			if newerFirst {
				require.NoError(t, newer.Commit(t.Context()))
				require.NoError(t, older.Commit(t.Context()))
			} else {
				require.NoError(t, older.Commit(t.Context()))
				require.NoError(t, newer.Commit(t.Context()))
			}

			storedPlan, storedOutput, generation := outputPublicationState(service)
			assert.Same(t, newerOutput, storedOutput)
			assert.Equal(t, newerGeneration, generation)
			assert.True(t, renderplan.ExactlyEqual(outputPublicationPlan(t, newerOutput), storedPlan))
		})
	}
}

func TestEquivalentStaleOutputPublicationCancellationPreservesCause(t *testing.T) {
	service := newOutputPublicationService(t)
	olderOutput := outputPublicationSnapshot(t, service, nil, "same")
	newerOutput := outputPublicationSnapshot(t, service, nil, "same")
	olderGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	newerGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	older, err := service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, olderGeneration, olderOutput,
	)
	require.NoError(t, err)
	newer, err := service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, newerGeneration, newerOutput,
	)
	require.NoError(t, err)
	require.NoError(t, newer.Commit(t.Context()))

	canceled := errors.New("canceled equivalent candidate")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(canceled)
	require.ErrorIs(t, older.Commit(ctx), canceled)

	_, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, newerOutput, storedOutput)
	assert.Equal(t, newerGeneration, generation)
}

func TestAbortedNewerOutputReservationLeavesOlderCandidateUsable(t *testing.T) {
	service := newOutputPublicationService(t)
	olderOutput := outputPublicationSnapshot(t, service, nil, "older")
	newerOutput := outputPublicationSnapshot(t, service, olderOutput, "newer")
	olderGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	newerGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	older, err := service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, olderGeneration, olderOutput,
	)
	require.NoError(t, err)
	newer, err := service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, newerGeneration, newerOutput,
	)
	require.NoError(t, err)

	newer.Abort()
	require.NoError(t, older.Commit(t.Context()))

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, olderOutput, storedOutput)
	assert.Equal(t, olderGeneration, generation)
	assert.True(t, renderplan.ExactlyEqual(outputPublicationPlan(t, olderOutput), storedPlan))
}

func TestOutputPublicationGenerationZeroFailsClosed(t *testing.T) {
	service := newOutputPublicationService(t)
	baselineOutput := outputPublicationSnapshot(t, service, nil, "baseline")
	candidateOutput := outputPublicationSnapshot(t, service, baselineOutput, "zero")
	baselinePlan := outputPublicationPlan(t, baselineOutput)
	service.rememberOwnedOutput(4, baselinePlan, baselineOutput)
	inner := &outputPublicationTestTransaction{}

	transaction, err := service.stageOutputPublication(
		inner,
		rendercontext.RenderModeReconcile,
		0,
		candidateOutput,
	)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit(t.Context()))

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, baselinePlan, storedPlan)
	assert.Same(t, baselineOutput, storedOutput)
	assert.Equal(t, uint64(4), generation)
	assert.Equal(t, int32(1), inner.commitCalls.Load())
}

func TestOutputPublicationGenerationExhaustionCannotWrapAndPublish(t *testing.T) {
	service := newOutputPublicationService(t)
	service.nextOutputGeneration = ^uint64(0) - 1
	lastOutput := outputPublicationSnapshot(t, service, nil, "last")
	wrappedOutput := outputPublicationSnapshot(t, service, lastOutput, "wrapped")

	lastGeneration, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	assert.Equal(t, ^uint64(0), lastGeneration)
	transaction, err := service.stageOutputPublication(
		nil,
		rendercontext.RenderModeReconcile,
		lastGeneration,
		lastOutput,
	)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit(t.Context()))

	_, err = service.reserveOutputGeneration()
	require.ErrorContains(t, err, "generation is exhausted")
	_, err = service.reserveOutputGeneration()
	require.ErrorContains(t, err, "generation is exhausted")
	service.rememberOwnedOutput(1, outputPublicationPlan(t, wrappedOutput), wrappedOutput)

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, lastOutput, storedOutput)
	assert.Equal(t, ^uint64(0), generation)
	assert.True(t, renderplan.ExactlyEqual(outputPublicationPlan(t, lastOutput), storedPlan))
}

func TestOutputReservationRetainsAndReclaimsConcurrentAttempts(t *testing.T) {
	service := newOutputPublicationService(t)
	reservations := make([]*renderOutputReservation, 0, 16)
	for range 16 {
		generation, err := service.reserveOutputGeneration()
		require.NoError(t, err)
		reservation, err := service.outputReservation(generation)
		require.NoError(t, err)
		reservations = append(reservations, reservation)
	}
	service.planMu.Lock()
	assert.Len(t, service.outputReservations, len(reservations))
	service.planMu.Unlock()
	for _, reservation := range reservations {
		reservation.abortPublication(false)
	}
	service.planMu.Lock()
	assert.Empty(t, service.outputReservations)
	service.planMu.Unlock()
}

func TestOutputReservationRejectsCopiedAndForeignHandles(t *testing.T) {
	service := newOutputPublicationService(t)
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	reservation, err := service.outputReservation(generation)
	require.NoError(t, err)

	copied := *reservation
	transaction := &planPublicationTransaction{}
	require.ErrorContains(
		t, transaction.bindRenderOutputReservation(&copied), "invalid provenance",
	)

	foreign := newOutputPublicationService(t)
	require.ErrorContains(t, reservation.validate(foreign, generation), "invalid provenance")
	assert.Equal(t, uint32(renderOutputReservationReady), reservation.state.Load())
}

func TestOutputReservationRejectsCandidateBindingPoison(t *testing.T) {
	tests := map[string]func(
		*testing.T,
		*RenderService,
		*renderOutputReservation,
		*renderOutputCandidate,
	){
		"copied binding": func(
			t *testing.T,
			_ *RenderService,
			reservation *renderOutputReservation,
			_ *renderOutputCandidate,
		) {
			t.Helper()
			copied := *reservation.candidateBinding
			reservation.candidateBinding = &copied
		},
		"copied candidate": func(
			t *testing.T,
			_ *RenderService,
			reservation *renderOutputReservation,
			candidate *renderOutputCandidate,
		) {
			t.Helper()
			copied := *candidate
			reservation.candidateBinding.candidate = &copied
		},
		"substituted candidate": func(
			t *testing.T,
			service *RenderService,
			reservation *renderOutputReservation,
			_ *renderOutputCandidate,
		) {
			t.Helper()
			replacementOutput := outputPublicationSnapshot(t, service, nil, "replacement")
			reservation.candidateBinding.candidate = newRenderOutputCandidate(
				reservation, replacementOutput, nil,
			)
		},
		"same-authority replacement": func(
			t *testing.T,
			service *RenderService,
			reservation *renderOutputReservation,
			_ *renderOutputCandidate,
		) {
			t.Helper()
			replacementOutput := outputPublicationSnapshot(t, service, nil, "replacement")
			replacement := newRenderOutputCandidate(reservation, replacementOutput, nil)
			_, err := newRenderOutputCandidateBinding(reservation.candidateAuthority, replacement)
			require.ErrorContains(t, err, "already used")

			binding := &renderOutputCandidateBinding{
				authority:   reservation.candidateAuthority,
				reservation: reservation,
				candidate:   replacement,
			}
			binding.seal = binding
			binding.auth = renderOutputCandidateBindingAuthentication{
				owner:       binding,
				authority:   binding.authority,
				reservation: binding.reservation,
				candidate:   binding.candidate,
			}
			reservation.candidateBinding = binding
		},
		"foreign binding": func(
			t *testing.T,
			_ *RenderService,
			reservation *renderOutputReservation,
			_ *renderOutputCandidate,
		) {
			t.Helper()
			foreignService := newOutputPublicationService(t)
			foreignGeneration, err := foreignService.reserveOutputGeneration()
			require.NoError(t, err)
			foreignReservation, err := foreignService.outputReservation(foreignGeneration)
			require.NoError(t, err)
			foreignOutput := outputPublicationSnapshot(t, foreignService, nil, "foreign")
			foreignCandidate := newRenderOutputCandidate(foreignReservation, foreignOutput, nil)
			require.NoError(t, foreignReservation.bindCandidate(foreignCandidate))
			reservation.candidateBinding = foreignReservation.candidateBinding
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			service := newOutputPublicationService(t)
			generation, err := service.reserveOutputGeneration()
			require.NoError(t, err)
			reservation, err := service.outputReservation(generation)
			require.NoError(t, err)
			output := outputPublicationSnapshot(t, service, nil, "candidate")
			candidate := newRenderOutputCandidate(reservation, output, nil)
			require.NoError(t, reservation.bindCandidate(candidate))

			poison(t, service, reservation, candidate)
			err = reservation.beginPublication()
			require.ErrorContains(t, err, "invalid provenance")
			assert.Equal(t, uint32(renderOutputReservationAborted), reservation.state.Load())
			service.planMu.Lock()
			_, retained := service.outputReservations[generation]
			service.planMu.Unlock()
			assert.False(t, retained)
		})
	}
}

func TestOutputReservationRejectsCandidateAuthorityReplacement(t *testing.T) {
	service := newOutputPublicationService(t)
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	reservation, err := service.outputReservation(generation)
	require.NoError(t, err)

	forged := &renderOutputCandidateBindingAuthority{reservation: reservation}
	forged.seal = forged
	reservation.candidateAuthority = forged
	require.ErrorContains(t, reservation.validate(service, generation), "invalid provenance")

	reservation.abortPublication(false)
}

func TestOutputReservationFailedTransactionBindingRevokesCandidate(t *testing.T) {
	service := newOutputPublicationService(t)
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	reservation, err := service.outputReservation(generation)
	require.NoError(t, err)
	output := outputPublicationSnapshot(t, service, nil, "candidate")
	transaction := &planPublicationTransaction{inner: &outputPublicationPanickingAbortTransaction{}}

	staged, err := service.stageOutputPublication(
		transaction, rendercontext.RenderModeReconcile, generation, output,
	)
	require.ErrorIs(t, err, errRenderPublicationAtomicBoundary)
	assert.Nil(t, staged)
	service.planMu.Lock()
	_, retained := service.outputReservations[generation]
	service.planMu.Unlock()
	require.NotNil(t, reservation)
	assert.Equal(t, uint32(renderOutputReservationAborted), reservation.state.Load())
	assert.False(t, retained)
	assert.NotNil(t, reservation.candidateAuthority.binding.Load())

	_, err = service.stageOutputPublication(
		nil, rendercontext.RenderModeReconcile, generation, output,
	)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestOutputPublicationAckedPlanOutranksStagedFallback(t *testing.T) {
	tests := map[string]bool{
		"ACK before stage": true,
		"ACK after stage":  false,
	}
	for name, ackBeforeStage := range tests {
		t.Run(name, func(t *testing.T) {
			service := newOutputPublicationService(t)
			baselineOutput := outputPublicationSnapshot(t, service, nil, "baseline")
			candidateOutput := outputPublicationSnapshot(t, service, baselineOutput, "candidate")
			baselinePlan := outputPublicationPlan(t, baselineOutput)
			ackedPlan := planWithServer("acked", "10.0.0.10")
			service.rememberOwnedOutput(1, baselinePlan, baselineOutput)
			generation, err := service.reserveOutputGeneration()
			require.NoError(t, err)
			if ackBeforeStage {
				service.SetAckedPlan(ackedPlan)
			}
			transaction, err := service.stageOutputPublication(
				nil,
				rendercontext.RenderModeReconcile,
				generation,
				candidateOutput,
			)
			require.NoError(t, err)
			if !ackBeforeStage {
				service.SetAckedPlan(ackedPlan)
			}

			require.NoError(t, transaction.Commit(t.Context()))
			storedPlan, storedOutput, generation := outputPublicationState(service)
			assert.Same(t, baselinePlan, storedPlan)
			assert.Same(t, candidateOutput, storedOutput)
			assert.Equal(t, uint64(2), generation)
			assert.Equal(t, "10.0.0.10", serverAddress(t, service.currentConfig()))
		})
	}
}

func TestRenderServiceTerminalPublicationOwnsLockThroughRollback(t *testing.T) {
	service := newOutputPublicationService(t)
	baseline := outputPublicationSnapshot(t, service, nil, "baseline")
	candidate := outputPublicationSnapshot(t, service, baseline, "candidate")
	baselinePlan := outputPublicationPlan(t, baseline)
	service.rememberOwnedOutput(1, baselinePlan, baseline)
	transaction := &planPublicationTransaction{}
	entered := make(chan struct{})
	release := make(chan struct{})
	staged := service.stageRenderServicePublication(transaction, func() bool {
		service.publishedOutputGeneration = 2
		service.lastOutputSnapshot = candidate
		close(entered)
		<-release
		panic("terminal publication poison")
	})
	require.Same(t, transaction, staged)
	commitResult := make(chan error, 1)
	go func() { commitResult <- transaction.Commit(t.Context()) }()
	<-entered
	assert.False(t, service.planMu.TryLock(), "terminal publication released planMu before its outcome")

	acked := planWithServer("acked", "10.0.0.10")
	ackStarted := make(chan struct{})
	ackDone := make(chan struct{})
	go func() {
		close(ackStarted)
		service.SetAckedPlan(acked)
		close(ackDone)
	}()
	<-ackStarted
	select {
	case <-ackDone:
		t.Fatal("concurrent ACK bypassed terminal publication ownership")
	default:
	}
	close(release)
	err := <-commitResult
	require.ErrorContains(t, err, "terminal publication poison")
	<-ackDone

	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, baseline, storedOutput)
	assert.Equal(t, uint64(1), generation)
	assert.Same(t, baselinePlan, storedPlan)
	assert.Equal(t, "10.0.0.10", serverAddress(t, service.currentConfig()))
	require.True(t, service.planMu.TryLock(), "terminal rollback stranded planMu")
	service.planMu.Unlock()
}

func TestRenderServiceTerminalPublicationCompletionReleasesLock(t *testing.T) {
	service := newOutputPublicationService(t)
	output := outputPublicationSnapshot(t, service, nil, "committed")
	generation, err := service.reserveOutputGeneration()
	require.NoError(t, err)
	transaction, err := service.stageOutputPublication(
		nil,
		rendercontext.RenderModeReconcile,
		generation,
		output,
	)
	require.NoError(t, err)

	require.NoError(t, transaction.Commit(t.Context()))
	require.True(t, service.planMu.TryLock(), "terminal completion stranded planMu")
	assert.Same(t, output, service.lastOutputSnapshot)
	assert.Equal(t, uint64(1), service.publishedOutputGeneration)
	service.planMu.Unlock()
}

func TestRenderServiceTerminalPublicationRollsBackWhenInnerAbortPanics(t *testing.T) {
	service := newOutputPublicationService(t)
	baseline := outputPublicationSnapshot(t, service, nil, "baseline")
	candidate := outputPublicationSnapshot(t, service, baseline, "candidate")
	baselinePlan := outputPublicationPlan(t, baseline)
	service.rememberOwnedOutput(1, baselinePlan, baseline)
	commitErr := errors.New("input commit failed")
	abortErr := errors.New("input abort failed")
	inner := &outputPublicationPanickingAbortTransaction{
		commitErr: commitErr,
		abortErr:  abortErr,
	}
	transaction := service.stageRenderServicePublication(inner, func() bool {
		service.publishedOutputGeneration = 2
		service.lastOutputSnapshot = candidate
		return true
	})

	err := transaction.Commit(t.Context())
	require.ErrorIs(t, err, commitErr)
	require.ErrorIs(t, err, abortErr)
	storedPlan, storedOutput, generation := outputPublicationState(service)
	assert.Same(t, baselinePlan, storedPlan)
	assert.Same(t, baseline, storedOutput)
	assert.Equal(t, uint64(1), generation)
	require.True(t, service.planMu.TryLock(), "terminal rollback stranded planMu")
	service.planMu.Unlock()
}

func newOutputPublicationService(t *testing.T) *RenderService {
	t.Helper()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(t, err)
	return &RenderService{
		planAuthority:     planAuthority,
		artifactAuthority: artifactAuthority,
		outputAuthority:   outputAuthority,
	}
}

func outputPublicationSnapshot(
	t *testing.T,
	service *RenderService,
	previous *renderoutput.Snapshot,
	label string,
) *renderoutput.Snapshot {
	t.Helper()
	var previousArtifacts *renderartifact.Snapshot
	if previous != nil {
		var err error
		previousArtifacts, err = previous.ArtifactSnapshot()
		require.NoError(t, err)
	}
	descriptor := renderartifact.Descriptor{
		Family:      renderartifact.General,
		Name:        "output.txt",
		Path:        "files/output.txt",
		RuntimePath: "files/output.txt",
	}
	content := label + "\n"
	builder, err := renderartifact.NewBuilder(service.artifactAuthority, previousArtifacts)
	require.NoError(t, err)
	require.NoError(t, builder.Add(descriptor, renderartifact.NewLiteralContent(content)))
	artifacts, err := builder.Build()
	require.NoError(t, err)

	config := "global\n    # " + label + "\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind:       renderplan.SectionKindCore,
			Name:       "core#0",
			TextDigest: renderplan.DigestString(config),
			Length:     len(config),
			Text:       config,
			TextKnown:  true,
		}},
		Files: []renderplan.File{
			outputPublicationFile(renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config),
			outputPublicationFile(descriptor.RuntimePath, renderplan.FileKindGeneral, false, content),
		},
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(
		service.outputAuthority,
		config,
		plan,
		artifacts,
		previous,
	)
	require.NoError(t, err)
	require.NoError(t, service.outputAuthority.ValidateSnapshot(output))
	return output
}

func outputPublicationFile(path, kind string, reload bool, content string) renderplan.File {
	return renderplan.File{
		Path:           path,
		Kind:           kind,
		ReloadOnChange: reload,
		Digest:         renderplan.DigestString(content),
		Size:           int64(len(content)),
		Content:        content,
		ContentKnown:   true,
	}
}

func outputPublicationPlan(t *testing.T, output *renderoutput.Snapshot) *renderplan.Plan {
	t.Helper()
	planSnapshot, err := output.PlanSnapshot()
	require.NoError(t, err)
	plan, err := planSnapshot.LegacyCopy()
	require.NoError(t, err)
	return plan
}

func outputPublicationState(service *RenderService) (*renderplan.Plan, *renderoutput.Snapshot, uint64) {
	service.planMu.Lock()
	defer service.planMu.Unlock()
	return service.lastPlan, service.lastOutputSnapshot, service.publishedOutputGeneration
}

type outputPublicationTestTransaction struct {
	once         sync.Once
	publications stagedRenderPublications
	commitErr    error
	err          error
	started      chan struct{}
	release      <-chan struct{}
	commitCalls  atomic.Int32
	abortCalls   atomic.Int32
}

type outputPublicationPanickingAbortTransaction struct {
	commitErr error
	abortErr  error
}

func (*outputPublicationPanickingAbortTransaction) HasCandidates() bool { return false }

func (t *outputPublicationPanickingAbortTransaction) Commit(context.Context) error {
	return t.commitErr
}

func (t *outputPublicationPanickingAbortTransaction) Abort() {
	panic(t.abortErr)
}

func (*outputPublicationTestTransaction) HasCandidates() bool { return false }

func (t *outputPublicationTestTransaction) Commit(ctx context.Context) error {
	t.once.Do(func() {
		t.commitCalls.Add(1)
		if t.started != nil {
			close(t.started)
		}
		if t.release != nil {
			select {
			case <-t.release:
			case <-ctx.Done():
				t.err = context.Cause(ctx)
				t.abortCalls.Add(1)
				_ = t.publications.abortResult()
				return
			}
		}
		if cause := context.Cause(ctx); cause != nil {
			t.err = cause
			t.abortCalls.Add(1)
			_ = t.publications.abortResult()
			return
		}
		if t.err = t.publications.prepareTerminalResult(); t.err != nil {
			t.abortCalls.Add(1)
			return
		}
		if t.commitErr != nil {
			t.err = errors.Join(t.commitErr, t.publications.abortResult())
			t.abortCalls.Add(1)
			return
		}
		t.err = t.publications.completeResult()
	})
	return t.err
}

func (t *outputPublicationTestTransaction) Abort() {
	t.once.Do(func() {
		t.err = errPlanPublicationAborted
		t.abortCalls.Add(1)
		_ = t.publications.abortResult()
	})
}

func (t *outputPublicationTestTransaction) StagePublication(callback func()) {
	t.publications.stage(callback, nil)
}

func (t *outputPublicationTestTransaction) stagePublicationFinalizer(publish, abort func()) {
	t.publications.stage(publish, abort)
}

func (t *outputPublicationTestTransaction) stageOptionalPublication(publish func()) {
	t.publications.stageOptional(publish)
}

func (t *outputPublicationTestTransaction) bindRenderOutputReservation(
	reservation *renderOutputReservation,
) error {
	return t.publications.bindRenderOutputReservation(reservation)
}

type outputPublicationStagingTransaction struct {
	once         sync.Once
	publications stagedRenderPublications
	err          error
}

func (*outputPublicationStagingTransaction) HasCandidates() bool { return false }

func (t *outputPublicationStagingTransaction) StagePublication(callback func()) {
	t.publications.stage(callback, nil)
}

func (t *outputPublicationStagingTransaction) stagePublicationFinalizer(publish, abort func()) {
	t.publications.stage(publish, abort)
}

func (t *outputPublicationStagingTransaction) stageOptionalPublication(publish func()) {
	t.publications.stageOptional(publish)
}

func (t *outputPublicationStagingTransaction) bindRenderOutputReservation(
	reservation *renderOutputReservation,
) error {
	return t.publications.bindRenderOutputReservation(reservation)
}

func (t *outputPublicationStagingTransaction) Commit(ctx context.Context) error {
	t.once.Do(func() {
		if cause := context.Cause(ctx); cause != nil {
			t.err = cause
			_ = t.publications.abortResult()
			return
		}
		if t.err = t.publications.prepareTerminalResult(); t.err != nil {
			return
		}
		t.err = t.publications.completeResult()
	})
	return t.err
}

func (t *outputPublicationStagingTransaction) Abort() {
	t.once.Do(func() {
		t.err = errPlanPublicationAborted
		_ = t.publications.abortResult()
	})
}
