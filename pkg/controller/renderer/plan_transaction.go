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
	"fmt"
	"slices"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

var errPlanPublicationAborted = errors.New("render plan publication was aborted")
var errRenderOutputGenerationSuperseded = fmt.Errorf(
	"render output generation was superseded: %w", incremental.ErrRevisionConflict,
)
var errRenderPublicationAtomicBoundary = errors.New(
	"render input transaction has no atomic publication protocol",
)

type renderPublicationFinalizerStager interface {
	stagePublicationFinalizer(publish, abort func())
}

type renderOptionalPublicationStager interface {
	stageOptionalPublication(func())
}

type renderOutputReservationBinder interface {
	bindRenderOutputReservation(*renderOutputReservation) error
}

type renderPublicationFinalizer struct {
	publish func()
	abort   func()
}

type renderServicePublicationState struct {
	lastPlan                   *renderplan.Plan
	lastCurrentConfigRoot      *exactCycleCurrentConfigRoot
	lastCycleSnapshot          *rendercycle.Snapshot
	lastOutputSnapshot         *renderoutput.Snapshot
	lastPlanIdentity           *rendercontext.RenderPlanIdentity
	lastRenderCache            *rendercontext.PreparedRenderCachePublication
	publishedOutputGeneration  uint64
	committedOutputReservation *renderOutputReservation
}

func (f renderPublicationFinalizer) finish(succeeded bool) {
	if succeeded {
		if f.publish != nil {
			f.publish()
		}
		return
	}
	if f.abort != nil {
		f.abort()
	}
}

type renderPublicationState uint8

const (
	renderPublicationsOpen renderPublicationState = iota
	renderPublicationsPreparing
	renderPublicationsSealed
	renderPublicationsPrepared
	renderPublicationsFinalizing
	renderPublicationsCommitted
	renderPublicationsSucceeded
	renderPublicationsFailed
)

type renderPublicationPhase uint8

const (
	renderPublicationReversible renderPublicationPhase = iota
	renderPublicationIrreversible
)

type stagedRenderPublications struct {
	mu                 sync.Mutex
	pending            []renderPublicationFinalizer
	irreversible       []renderPublicationFinalizer
	optional           []func()
	applied            []renderPublicationFinalizer
	late               []renderPublicationFinalizer
	lateIrreversible   []renderPublicationFinalizer
	rejectIrreversible bool
	reservation        *renderOutputReservation
	reservationActive  bool
	terminalValidated  bool
	state              renderPublicationState
	err                error
	done               chan struct{}
}

type renderPublicationAbortJournal struct {
	applied          []renderPublicationFinalizer
	remaining        []renderPublicationFinalizer
	pending          []renderPublicationFinalizer
	irreversible     []renderPublicationFinalizer
	late             []renderPublicationFinalizer
	lateIrreversible []renderPublicationFinalizer
}

func (j *renderPublicationAbortJournal) abort() error {
	return errors.Join(
		abortRequiredRenderPublications(j.lateIrreversible),
		abortRequiredRenderPublications(j.late),
		abortRequiredRenderPublications(j.irreversible),
		abortRequiredRenderPublications(j.pending),
		abortRequiredRenderPublications(j.remaining),
		abortRequiredRenderPublications(j.applied),
	)
}

func (s *stagedRenderPublications) bindRenderOutputReservation(
	reservation *renderOutputReservation,
) error {
	if reservation == nil {
		return errors.New("render output reservation is unavailable")
	}
	if err := reservation.validate(reservation.service, reservation.generation); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != renderPublicationsOpen {
		return errors.New("render output reservation was bound after publication started")
	}
	if s.reservation != nil && s.reservation != reservation {
		return errors.New("render transaction contains conflicting output reservations")
	}
	s.reservation = reservation
	return nil
}

func (s *stagedRenderPublications) resolveStaleCandidateBeforeCommit() (bool, error) {
	s.mu.Lock()
	reservation := s.reservation
	s.mu.Unlock()
	if reservation == nil {
		return false, nil
	}
	return reservation.resolveStaleCandidateBeforeCommit()
}

func (s *stagedRenderPublications) committedOutputReservation() (*renderOutputReservation, error) {
	if s == nil {
		return nil, errors.New("render publications are unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != renderPublicationsSucceeded {
		return nil, errors.New("render output reservation is not committed")
	}
	reservation := s.reservation
	if reservation == nil {
		return nil, errors.New("render output reservation is unavailable")
	}
	if err := reservation.validate(reservation.service, reservation.generation); err != nil {
		return nil, err
	}
	if err := reservation.validateCommittedCacheBuild(
		reservation.service.incremental,
		reservation.generation,
	); err != nil {
		return nil, err
	}
	return reservation, nil
}

func (s *stagedRenderPublications) stage(publish, abort func()) {
	s.stageRequired(
		renderPublicationFinalizer{publish: publish, abort: abort},
		renderPublicationReversible,
	)
}

func (s *stagedRenderPublications) stageRequired(
	finalizer renderPublicationFinalizer,
	phase renderPublicationPhase,
) {
	if finalizer.publish == nil && finalizer.abort == nil {
		return
	}
	for {
		s.mu.Lock()
		switch s.state {
		case renderPublicationsOpen, renderPublicationsPreparing:
			switch phase {
			case renderPublicationIrreversible:
				s.irreversible = append(s.irreversible, finalizer)
			default:
				s.pending = append(s.pending, finalizer)
			}
			s.mu.Unlock()
			return
		case renderPublicationsSealed, renderPublicationsPrepared:
			if phase == renderPublicationIrreversible {
				s.lateIrreversible = append(s.lateIrreversible, finalizer)
			} else {
				s.late = append(s.late, finalizer)
			}
			s.mu.Unlock()
			return
		case renderPublicationsFinalizing, renderPublicationsCommitted:
			s.mu.Unlock()
			finalizer.finish(false)
			return
		case renderPublicationsSucceeded:
			s.mu.Unlock()
			finalizer.finish(false)
			return
		default:
			s.mu.Unlock()
			finalizer.finish(false)
			return
		}
	}
}

func (s *stagedRenderPublications) stageOptional(publish func()) {
	if publish == nil {
		return
	}
	s.mu.Lock()
	switch s.state {
	case renderPublicationsOpen, renderPublicationsPreparing,
		renderPublicationsSealed, renderPublicationsPrepared:
		s.optional = append(s.optional, publish)
		s.mu.Unlock()
		return
	default:
		s.mu.Unlock()
		return
	}
}

func (s *stagedRenderPublications) prepareTerminalResult() error {
	return s.prepareTerminalResultWithPolicy(false)
}

func (s *stagedRenderPublications) prepareTerminalResultRejectingIrreversible() error {
	return s.prepareTerminalResultWithPolicy(true)
}

func (s *stagedRenderPublications) prepareTerminalResultWithPolicy(rejectIrreversible bool) error {
	if err := s.prepareResult(rejectIrreversible); err != nil {
		return err
	}
	return s.sealResult()
}

func (s *stagedRenderPublications) sealResult() error {
	for {
		s.mu.Lock()
		switch s.state {
		case renderPublicationsFinalizing, renderPublicationsCommitted, renderPublicationsSucceeded:
			s.mu.Unlock()
			return nil
		case renderPublicationsFailed:
			err := s.err
			s.mu.Unlock()
			return err
		case renderPublicationsPrepared:
			late := s.late
			s.late = nil
			lateIrreversible := s.lateIrreversible
			s.lateIrreversible = nil
			if s.rejectIrreversible && len(lateIrreversible) != 0 {
				s.mu.Unlock()
				remaining := slices.Concat(late, lateIrreversible)
				return s.failSealing(remaining, errIrreversiblePublicationAtomicBoundary)
			}
			if len(late) == 0 && len(lateIrreversible) == 0 {
				s.done = make(chan struct{})
				s.state = renderPublicationsFinalizing
				s.mu.Unlock()
				return nil
			}
			s.mu.Unlock()

			late = append(late, lateIrreversible...)
			remaining, err := s.publishRequiredRenderPublications(late)
			if err != nil {
				return s.failSealing(remaining, err)
			}
		default:
			s.mu.Unlock()
			return errors.New("required render publication was not prepared")
		}
	}
}

func (s *stagedRenderPublications) failSealing(
	remaining []renderPublicationFinalizer,
	publicationErr error,
) error {
	s.mu.Lock()
	journal := renderPublicationAbortJournal{
		applied:          s.applied,
		remaining:        remaining,
		pending:          s.pending,
		irreversible:     s.irreversible,
		late:             s.late,
		lateIrreversible: s.lateIrreversible,
	}
	s.pending = nil
	s.irreversible = nil
	s.late = nil
	s.lateIrreversible = nil
	s.optional = nil
	s.applied = nil
	s.state = renderPublicationsFailed
	s.err = publicationErr
	reservation := s.reservation
	reservationActive := s.reservationActive
	s.reservationActive = false
	s.mu.Unlock()
	abortErr := journal.abort()
	reservation.abortPublication(reservationActive)
	return errors.Join(publicationErr, abortErr)
}

func (s *stagedRenderPublications) prepareResult(rejectIrreversible bool) error {
	s.mu.Lock()
	switch s.state {
	case renderPublicationsPrepared, renderPublicationsFinalizing,
		renderPublicationsCommitted, renderPublicationsSucceeded:
		if rejectIrreversible && !s.rejectIrreversible {
			s.mu.Unlock()
			return errIrreversiblePublicationAtomicBoundary
		}
		s.mu.Unlock()
		return nil
	case renderPublicationsFailed:
		err := s.err
		s.mu.Unlock()
		return err
	case renderPublicationsPreparing, renderPublicationsSealed:
		s.mu.Unlock()
		return errors.New("required render publication is already being prepared")
	default:
		s.rejectIrreversible = rejectIrreversible
		s.state = renderPublicationsPreparing
		reservation := s.reservation
		s.mu.Unlock()
		if reservation != nil {
			if err := reservation.beginPublication(); err != nil {
				return s.failPreparation(nil, err)
			}
			s.mu.Lock()
			s.reservationActive = true
			s.mu.Unlock()
		}
	}

	for {
		s.mu.Lock()
		if s.rejectIrreversible && len(s.irreversible) != 0 {
			s.mu.Unlock()
			return s.failPreparation(nil, errIrreversiblePublicationAtomicBoundary)
		}
		pending := s.pending
		s.pending = nil
		if len(pending) == 0 {
			irreversible := s.irreversible
			s.irreversible = nil
			s.state = renderPublicationsSealed
			s.mu.Unlock()
			return s.publishSealed(irreversible)
		}
		s.mu.Unlock()
		remaining, err := s.publishRequiredRenderPublications(pending)
		if err != nil {
			return s.failPreparation(remaining, err)
		}
	}
}

func (s *stagedRenderPublications) publishSealed(
	irreversible []renderPublicationFinalizer,
) error {
	remaining, err := s.publishRequiredRenderPublications(irreversible)
	if err != nil {
		return s.failPreparation(remaining, err)
	}
	s.mu.Lock()
	s.state = renderPublicationsPrepared
	s.mu.Unlock()
	return nil
}

func (s *stagedRenderPublications) failPreparation(
	remaining []renderPublicationFinalizer,
	publicationErr error,
) error {
	s.mu.Lock()
	journal := renderPublicationAbortJournal{
		applied:          s.applied,
		remaining:        remaining,
		pending:          s.pending,
		irreversible:     s.irreversible,
		late:             s.late,
		lateIrreversible: s.lateIrreversible,
	}
	s.pending = nil
	s.irreversible = nil
	s.late = nil
	s.lateIrreversible = nil
	s.optional = nil
	s.applied = nil
	s.state = renderPublicationsFailed
	s.err = publicationErr
	reservation := s.reservation
	reservationActive := s.reservationActive
	s.reservationActive = false
	s.mu.Unlock()
	abortErr := journal.abort()
	reservation.abortPublication(reservationActive)
	return errors.Join(publicationErr, abortErr)
}

func (s *stagedRenderPublications) validateTerminalResult() error {
	s.mu.Lock()
	switch s.state {
	case renderPublicationsCommitted, renderPublicationsSucceeded:
		s.mu.Unlock()
		return nil
	case renderPublicationsFailed:
		err := s.err
		s.mu.Unlock()
		return err
	case renderPublicationsFinalizing:
		reservation := s.reservation
		if reservation != nil {
			if err := reservation.validateCompletion(); err != nil {
				return s.failTerminalResultLocked(err)
			}
		}
		s.terminalValidated = true
		s.mu.Unlock()
		return nil
	default:
		s.mu.Unlock()
		return errors.New("required render publication was not prepared")
	}
}

func (s *stagedRenderPublications) commitTerminalResult() error {
	if err := s.validateTerminalResult(); err != nil {
		return err
	}
	s.mu.Lock()
	switch s.state {
	case renderPublicationsCommitted, renderPublicationsSucceeded:
		s.mu.Unlock()
		return nil
	case renderPublicationsFailed:
		err := s.err
		s.mu.Unlock()
		return err
	case renderPublicationsFinalizing:
		if !s.terminalValidated {
			s.mu.Unlock()
			return errors.New("required render publication was not validated")
		}
		if s.reservation != nil {
			if err := s.reservation.commitPublication(); err != nil {
				return s.failTerminalResultLocked(err)
			}
		}
		s.state = renderPublicationsCommitted
		s.mu.Unlock()
		return nil
	default:
		s.mu.Unlock()
		return errors.New("required render publication was not prepared")
	}
}

func (s *stagedRenderPublications) releaseTerminalResult() error {
	s.mu.Lock()
	switch s.state {
	case renderPublicationsSucceeded:
		s.mu.Unlock()
		return nil
	case renderPublicationsFailed:
		err := s.err
		s.mu.Unlock()
		return err
	case renderPublicationsCommitted:
		optional := s.optional
		reservation := s.reservation
		reservationActive := s.reservationActive
		s.reservationActive = false
		s.applied = nil
		s.optional = nil
		s.state = renderPublicationsSucceeded
		done := s.done
		s.done = nil
		s.mu.Unlock()
		if reservation != nil && reservationActive {
			reservation.releasePublication()
		}
		if done != nil {
			close(done)
		}
		for _, publish := range optional {
			_ = callOptionalRenderPublication(publish)
		}
		return nil
	default:
		s.mu.Unlock()
		return errors.New("required render publication was not committed")
	}
}

func (s *stagedRenderPublications) failTerminalResultLocked(publicationErr error) error {
	applied := s.applied
	s.pending = nil
	s.irreversible = nil
	s.late = nil
	s.lateIrreversible = nil
	s.optional = nil
	s.applied = nil
	s.state = renderPublicationsFailed
	s.err = publicationErr
	reservation := s.reservation
	reservationActive := s.reservationActive
	s.reservationActive = false
	done := s.done
	s.done = nil
	s.mu.Unlock()
	abortErr := abortRequiredRenderPublications(applied)
	reservation.abortPublication(reservationActive)
	if done != nil {
		close(done)
	}
	return errors.Join(publicationErr, abortErr)
}

func (s *stagedRenderPublications) completeResult() error {
	if err := s.commitTerminalResult(); err != nil {
		return err
	}
	return s.releaseTerminalResult()
}

func (s *stagedRenderPublications) finishResult(succeeded bool) error {
	if !succeeded {
		return s.abortResult()
	}
	if err := s.prepareTerminalResult(); err != nil {
		return err
	}
	return s.completeResult()
}

func (s *stagedRenderPublications) finish(succeeded bool) {
	if err := s.finishResult(succeeded); err != nil {
		panic(err)
	}
}

func (s *stagedRenderPublications) abortResult() error {
	s.mu.Lock()
	switch s.state {
	case renderPublicationsSucceeded:
		s.mu.Unlock()
		return nil
	case renderPublicationsFailed:
		err := s.err
		s.mu.Unlock()
		return err
	case renderPublicationsPreparing, renderPublicationsSealed:
		s.mu.Unlock()
		return errors.New("required render publication is being prepared")
	}
	journal := renderPublicationAbortJournal{
		applied:          s.applied,
		pending:          s.pending,
		irreversible:     s.irreversible,
		late:             s.late,
		lateIrreversible: s.lateIrreversible,
	}
	s.pending = nil
	s.irreversible = nil
	s.late = nil
	s.lateIrreversible = nil
	s.optional = nil
	s.applied = nil
	s.state = renderPublicationsFailed
	s.err = errPlanPublicationAborted
	reservation := s.reservation
	reservationActive := s.reservationActive
	s.reservationActive = false
	done := s.done
	s.done = nil
	s.mu.Unlock()
	if done != nil {
		close(done)
	}
	abortErr := journal.abort()
	reservation.abortPublication(reservationActive)
	return abortErr
}

func callOptionalRenderPublication(publish func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("optional render publication panicked: %v", recovered)
		}
	}()
	publish()
	return nil
}

func (s *stagedRenderPublications) publishRequiredRenderPublications(
	pending []renderPublicationFinalizer,
) ([]renderPublicationFinalizer, error) {
	for index := range pending {
		finalizer := pending[index]
		s.mu.Lock()
		s.applied = append(s.applied, finalizer)
		s.mu.Unlock()
		if err := callRequiredRenderPublication("publish", finalizer.publish); err != nil {
			return pending[index+1:], err
		}
	}
	return nil, nil
}

func abortRequiredRenderPublications(candidates []renderPublicationFinalizer) error {
	var result error
	for index := len(candidates) - 1; index >= 0; index-- {
		result = errors.Join(result, callRequiredRenderPublication("abort", candidates[index].abort))
	}
	return result
}

func callRequiredRenderPublication(phase string, callback func()) (err error) {
	if callback == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = fmt.Errorf("required render publication %s panicked: %w", phase, recoveredErr)
				return
			}
			err = fmt.Errorf("required render publication %s panicked: %v", phase, recovered)
		}
	}()
	callback()
	return nil
}

func abortRenderInputTransaction(transaction RenderInputTransaction) (err error) {
	if transaction == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = fmt.Errorf("aborting render input transaction panicked: %w", recoveredErr)
				return
			}
			err = fmt.Errorf("aborting render input transaction panicked: %v", recovered)
		}
	}()
	transaction.Abort()
	return nil
}

func (s *RenderService) stagePlanPublication(
	transaction RenderInputTransaction,
	plan *renderplan.Plan,
) RenderInputTransaction {
	owned := s.ownFallbackPlan(plan)
	if owned == nil {
		return stageRenderPublication(transaction, nil)
	}
	s.planMu.Lock()
	previousRoot := s.lastCurrentConfigRoot
	s.planMu.Unlock()
	ownedRoot := currentConfigRootForPlan(owned, previousRoot)
	return s.stageRenderServicePublication(transaction, func() bool {
		if s.ackedPlan != nil {
			return false
		}
		s.lastPlan = owned
		s.lastCurrentConfigRoot = ownedRoot
		return true
	})
}

func (s *RenderService) stageOutputPublication(
	transaction RenderInputTransaction,
	mode rendercontext.RenderMode,
	generation uint64,
	output *renderoutput.Snapshot,
) (RenderInputTransaction, error) {
	if err := s.outputAuthority.ValidateSnapshot(output); err != nil {
		return nil, err
	}
	if mode != rendercontext.RenderModeReconcile {
		return stageRenderPublication(transaction, nil), nil
	}
	if generation == 0 {
		return stageRenderPublication(transaction, nil), nil
	}
	var ownedPlan *renderplan.Plan
	if s.fallbackOutputPlanNeeded(output) {
		var err error
		ownedPlan, err = s.ownFallbackOutputPlan(output)
		if err != nil {
			return nil, err
		}
	}
	var ownedRoot *exactCycleCurrentConfigRoot
	if ownedPlan != nil {
		s.planMu.Lock()
		previousRoot := s.lastCurrentConfigRoot
		s.planMu.Unlock()
		ownedRoot = currentConfigRootForPlan(ownedPlan, previousRoot)
		if err := ownedRoot.validate(); err != nil {
			return nil, fmt.Errorf("authenticating currentConfig projection: %w", err)
		}
	}
	return s.stageReservedRenderServicePublication(transaction, generation, output, nil, func() {
		s.publishedOutputGeneration = generation
		if s.ackedPlan == nil && ownedPlan != nil {
			s.lastPlan = ownedPlan
			s.lastCurrentConfigRoot = ownedRoot
		}
		s.lastOutputSnapshot = output
		s.lastCycleSnapshot = nil
		s.lastPlanIdentity = nil
		s.lastRenderCache = nil
	})
}

func (s *RenderService) stageCyclePublication(
	transaction RenderInputTransaction,
	mode rendercontext.RenderMode,
	generation uint64,
	identity *rendercontext.RenderPlanIdentity,
	cycle *rendercycle.Snapshot,
	cache *rendercontext.PreparedRenderCachePublication,
) (RenderInputTransaction, error) {
	return s.stageCyclePublicationWithPlan(
		transaction, mode, generation, identity, nil, nil, cycle, cache,
	)
}

func (s *RenderService) stageCyclePublicationWithPlan(
	transaction RenderInputTransaction,
	mode rendercontext.RenderMode,
	generation uint64,
	identity *rendercontext.RenderPlanIdentity,
	plan *renderplan.Plan,
	currentConfigRoot *exactCycleCurrentConfigRoot,
	cycle *rendercycle.Snapshot,
	cache *rendercontext.PreparedRenderCachePublication,
) (RenderInputTransaction, error) {
	if err := s.cycleAuthority.ValidateSnapshot(cycle); err != nil {
		return nil, err
	}
	output, err := cycle.OutputSnapshot()
	if err != nil {
		return nil, err
	}
	if mode != rendercontext.RenderModeReconcile || generation == 0 {
		return stageRenderPublication(transaction, nil), nil
	}
	if err := s.validateCyclePublicationInputs(identity, cache, generation); err != nil {
		return nil, err
	}
	if currentConfigRoot == nil && !s.skipCurrentConfigProjection {
		s.planMu.Lock()
		if s.lastOutputSnapshot == output {
			currentConfigRoot = s.lastCurrentConfigRoot
		}
		s.planMu.Unlock()
	}
	if currentConfigRoot == nil && !s.skipCurrentConfigProjection {
		return nil, errors.New("currentConfig root is unavailable for rendered output")
	}
	if currentConfigRoot != nil {
		if err := currentConfigRoot.validate(); err != nil {
			return nil, err
		}
	}
	var ownedPlan *renderplan.Plan
	if currentConfigRoot == nil {
		ownedPlan = s.ownSuppliedFallbackOutputPlan(output, plan)
	}
	return s.stageReservedRenderServicePublication(transaction, generation, nil, cycle, func() {
		s.publishCycleStateLocked(
			generation, ownedPlan, output, currentConfigRoot, cycle, identity, cache,
		)
	})
}

func (s *RenderService) validateCyclePublicationInputs(
	identity *rendercontext.RenderPlanIdentity,
	cache *rendercontext.PreparedRenderCachePublication,
	generation uint64,
) error {
	if identity != nil {
		if err := identity.ValidateAuthentication(); err != nil {
			return err
		}
	}
	if cache != nil {
		if err := s.mainDocumentCache.ValidatePublication(cache, generation); err != nil {
			return err
		}
	}
	return nil
}

func (s *RenderService) publishCycleStateLocked(
	generation uint64,
	ownedPlan *renderplan.Plan,
	output *renderoutput.Snapshot,
	currentConfigRoot *exactCycleCurrentConfigRoot,
	cycle *rendercycle.Snapshot,
	identity *rendercontext.RenderPlanIdentity,
	cache *rendercontext.PreparedRenderCachePublication,
) {
	s.publishedOutputGeneration = generation
	if s.ackedPlan == nil {
		switch {
		case ownedPlan != nil:
			s.lastPlan = ownedPlan
		case s.lastOutputSnapshot != output:
			s.lastPlan = nil
		}
	}
	s.lastCurrentConfigRoot = currentConfigRoot
	s.lastCycleSnapshot = cycle
	s.lastOutputSnapshot = output
	s.lastPlanIdentity = identity
	s.lastRenderCache = cache
}

func (s *RenderService) stageReservedRenderServicePublication(
	transaction RenderInputTransaction,
	generation uint64,
	output *renderoutput.Snapshot,
	cycle *rendercycle.Snapshot,
	publishLocked func(),
) (RenderInputTransaction, error) {
	if transaction == nil {
		transaction = &planPublicationTransaction{}
	}
	if err := validateAtomicRenderPublicationBoundary(transaction); err != nil {
		return nil, err
	}
	reservation, err := s.outputReservation(generation)
	if err != nil {
		return nil, err
	}
	candidate := newRenderOutputCandidate(reservation, output, cycle)
	if err := reservation.bindCandidate(candidate); err != nil {
		return nil, err
	}
	binder := transaction.(renderOutputReservationBinder)
	if err := binder.bindRenderOutputReservation(reservation); err != nil {
		reservation.revokeCandidateBinding()
		return nil, err
	}
	var before renderServicePublicationState
	published := false
	publish := func() {
		if reservation.service != s || s.outputReservations[generation] != reservation ||
			renderOutputReservationState(reservation.state.Load()) != renderOutputReservationPublishing ||
			generation <= s.publishedOutputGeneration {
			panic(requiredRenderPublicationPanic{err: errRenderOutputGenerationSuperseded})
		}
		before = s.renderServicePublicationStateLocked()
		published = true
		publishLocked()
	}
	abort := func() {
		if !published {
			return
		}
		s.restoreRenderServicePublicationStateLocked(before)
		published = false
	}
	required := transaction.(renderPublicationFinalizerStager)
	required.stagePublicationFinalizer(publish, abort)
	return transaction, nil
}

func (s *RenderService) stageRenderServicePublication(
	transaction RenderInputTransaction,
	publishLocked func() bool,
) RenderInputTransaction {
	return s.stageRenderServicePublicationChecked(transaction, func() (bool, error) {
		return publishLocked(), nil
	})
}

func (s *RenderService) stageRenderServicePublicationChecked(
	transaction RenderInputTransaction,
	publishLocked func() (bool, error),
) RenderInputTransaction {
	var before renderServicePublicationState
	locked := false
	publish := func() {
		s.planMu.Lock()
		locked = true
		before = s.renderServicePublicationStateLocked()
		published, err := publishLocked()
		if err != nil {
			s.restoreRenderServicePublicationStateLocked(before)
			locked = false
			s.planMu.Unlock()
			panic(requiredRenderPublicationPanic{err: err})
		}
		if !published {
			locked = false
			s.planMu.Unlock()
			return
		}
	}
	complete := func() {
		if !locked {
			return
		}
		locked = false
		s.planMu.Unlock()
	}
	abort := func() {
		if !locked {
			return
		}
		s.restoreRenderServicePublicationStateLocked(before)
		locked = false
		s.planMu.Unlock()
	}
	transaction = stageRenderPublicationFinalizer(transaction, publish, abort)
	return stageOptionalRenderPublication(transaction, complete)
}

func validateAtomicRenderPublicationBoundary(transaction RenderInputTransaction) error {
	if transaction == nil {
		return nil
	}
	_, hasRequired := transaction.(renderPublicationFinalizerStager)
	_, hasCompletion := transaction.(renderOptionalPublicationStager)
	_, hasReservation := transaction.(renderOutputReservationBinder)
	if !hasRequired || !hasCompletion || !hasReservation {
		return fmt.Errorf("%w: %T", errRenderPublicationAtomicBoundary, transaction)
	}
	return nil
}

func (s *RenderService) renderServicePublicationStateLocked() renderServicePublicationState {
	return renderServicePublicationState{
		lastPlan:                   s.lastPlan,
		lastCurrentConfigRoot:      s.lastCurrentConfigRoot,
		lastCycleSnapshot:          s.lastCycleSnapshot,
		lastOutputSnapshot:         s.lastOutputSnapshot,
		lastPlanIdentity:           s.lastPlanIdentity,
		lastRenderCache:            s.lastRenderCache,
		publishedOutputGeneration:  s.publishedOutputGeneration,
		committedOutputReservation: s.committedOutputReservation.Load(),
	}
}

func (s *RenderService) restoreRenderServicePublicationStateLocked(state renderServicePublicationState) {
	s.lastPlan = state.lastPlan
	s.lastCurrentConfigRoot = state.lastCurrentConfigRoot
	s.lastCycleSnapshot = state.lastCycleSnapshot
	s.lastOutputSnapshot = state.lastOutputSnapshot
	s.lastPlanIdentity = state.lastPlanIdentity
	s.lastRenderCache = state.lastRenderCache
	s.publishedOutputGeneration = state.publishedOutputGeneration
	s.committedOutputReservation.Store(state.committedOutputReservation)
}

func (s *RenderService) ownSuppliedFallbackOutputPlan(
	output *renderoutput.Snapshot,
	plan *renderplan.Plan,
) *renderplan.Plan {
	if plan == nil || !s.fallbackOutputPlanNeeded(output) {
		return nil
	}
	return plan.Clone()
}

func (s *RenderService) fallbackOutputPlanNeeded(output *renderoutput.Snapshot) bool {
	if s.skipCurrentConfigProjection {
		return false
	}
	s.planMu.Lock()
	hasACK := s.ackedPlan != nil
	reusesPublishedOutput := s.lastPlan != nil && s.lastOutputSnapshot == output
	s.planMu.Unlock()
	return !hasACK && !reusesPublishedOutput
}

func (s *RenderService) ownFallbackOutputPlan(output *renderoutput.Snapshot) (*renderplan.Plan, error) {
	planSnapshot, err := output.PlanSnapshot()
	if err != nil {
		return nil, err
	}
	return planSnapshot.LegacyCopy()
}

func stageRenderPublication(transaction RenderInputTransaction, publish func()) RenderInputTransaction {
	return stageRenderPublicationFinalizer(transaction, publish, nil)
}

func stageRenderPublicationFinalizer(
	transaction RenderInputTransaction,
	publish,
	abort func(),
) RenderInputTransaction {
	if staged, ok := transaction.(renderPublicationFinalizerStager); ok {
		staged.stagePublicationFinalizer(publish, abort)
		return transaction
	}
	staged := &planPublicationTransaction{inner: transaction}
	staged.stagePublicationFinalizer(publish, abort)
	return staged
}

func stageOptionalRenderPublication(
	transaction RenderInputTransaction,
	publish func(),
) RenderInputTransaction {
	if staged, ok := transaction.(renderOptionalPublicationStager); ok {
		staged.stageOptionalPublication(publish)
		return transaction
	}
	staged := &planPublicationTransaction{inner: transaction}
	staged.stageOptionalPublication(publish)
	return staged
}

func (s *RenderService) ownFallbackPlan(plan *renderplan.Plan) *renderplan.Plan {
	if plan == nil || s.skipCurrentConfigProjection {
		return nil
	}
	s.planMu.Lock()
	hasACK := s.ackedPlan != nil
	s.planMu.Unlock()
	if hasACK {
		return nil
	}
	return plan.Clone()
}

type planPublicationTransaction struct {
	once         sync.Once
	inner        RenderInputTransaction
	publications stagedRenderPublications
	err          error
}

func (t *planPublicationTransaction) HasCandidates() bool {
	return t.inner != nil && t.inner.HasCandidates()
}

func (t *planPublicationTransaction) StagePublication(callback func()) {
	t.stagePublicationFinalizer(callback, nil)
}

func (t *planPublicationTransaction) stagePublicationFinalizer(publish, abort func()) {
	t.publications.stage(publish, abort)
}

func (t *planPublicationTransaction) stageOptionalPublication(publish func()) {
	t.publications.stageOptional(publish)
}

func (t *planPublicationTransaction) bindRenderOutputReservation(
	reservation *renderOutputReservation,
) error {
	if t.inner != nil {
		return fmt.Errorf("%w: %T", errRenderPublicationAtomicBoundary, t.inner)
	}
	return t.publications.bindRenderOutputReservation(reservation)
}

func (t *planPublicationTransaction) Commit(ctx context.Context) error {
	t.once.Do(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicErr := fmt.Errorf("render plan transaction panicked: %v", recovered)
				t.err = errors.Join(
					panicErr,
					abortRenderInputTransaction(t.inner),
					t.publications.abortResult(),
				)
			}
		}()
		if cause := context.Cause(ctx); cause != nil {
			t.err = errors.Join(
				cause,
				abortRenderInputTransaction(t.inner),
				t.publications.abortResult(),
			)
			return
		}
		handled, err := t.publications.resolveStaleCandidateBeforeCommit()
		if handled {
			t.err = errors.Join(
				err,
				abortRenderInputTransaction(t.inner),
				t.publications.abortResult(),
			)
			return
		}
		if t.inner != nil {
			t.err = t.publications.prepareTerminalResultRejectingIrreversible()
		} else {
			t.err = t.publications.prepareTerminalResult()
		}
		if t.err != nil {
			t.err = errors.Join(t.err, abortRenderInputTransaction(t.inner))
			return
		}
		if t.inner != nil {
			t.err = t.inner.Commit(ctx)
			if t.err != nil {
				t.err = errors.Join(
					t.err,
					abortRenderInputTransaction(t.inner),
					t.publications.abortResult(),
				)
				return
			}
		}
		t.err = t.publications.completeResult()
	})
	return t.err
}

func (t *planPublicationTransaction) Abort() {
	t.once.Do(func() {
		t.err = errors.Join(
			errPlanPublicationAborted,
			abortRenderInputTransaction(t.inner),
			t.publications.abortResult(),
		)
	})
}
