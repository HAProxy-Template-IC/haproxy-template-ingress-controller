// Copyright 2025 Philipp Hossner
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
	"errors"
	"fmt"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

type renderOutputReservationState uint32

const (
	renderOutputReservationReady renderOutputReservationState = iota
	renderOutputReservationPublishing
	renderOutputReservationCommitted
	renderOutputReservationAborted
)

type renderOutputCandidateAuthentication struct {
	owner       *renderOutputCandidate
	reservation *renderOutputReservation
	output      *renderoutput.Snapshot
	cycle       *rendercycle.Snapshot
}

type renderOutputCandidateBindingAuthority struct {
	seal        *renderOutputCandidateBindingAuthority
	reservation *renderOutputReservation
	binding     atomic.Pointer[renderOutputCandidateBinding]
}

type renderOutputCandidateBindingAuthentication struct {
	owner       *renderOutputCandidateBinding
	authority   *renderOutputCandidateBindingAuthority
	reservation *renderOutputReservation
	candidate   *renderOutputCandidate
}

type renderOutputCandidateBinding struct {
	seal        *renderOutputCandidateBinding
	authority   *renderOutputCandidateBindingAuthority
	reservation *renderOutputReservation
	candidate   *renderOutputCandidate
	auth        renderOutputCandidateBindingAuthentication
}

type renderOutputCandidate struct {
	seal        *renderOutputCandidate
	reservation *renderOutputReservation
	output      *renderoutput.Snapshot
	cycle       *rendercycle.Snapshot
	auth        renderOutputCandidateAuthentication
}

type renderOutputReservationAuthentication struct {
	owner              *renderOutputReservation
	service            *RenderService
	generation         uint64
	candidateAuthority *renderOutputCandidateBindingAuthority
}

type renderOutputReservation struct {
	seal               *renderOutputReservation
	service            *RenderService
	generation         uint64
	state              atomic.Uint32
	candidateAuthority *renderOutputCandidateBindingAuthority
	candidateBinding   *renderOutputCandidateBinding
	auth               renderOutputReservationAuthentication
}

func newRenderOutputReservation(service *RenderService, generation uint64) *renderOutputReservation {
	reservation := &renderOutputReservation{service: service, generation: generation}
	reservation.seal = reservation
	reservation.candidateAuthority = &renderOutputCandidateBindingAuthority{reservation: reservation}
	reservation.candidateAuthority.seal = reservation.candidateAuthority
	reservation.auth = renderOutputReservationAuthentication{
		owner:              reservation,
		service:            service,
		generation:         generation,
		candidateAuthority: reservation.candidateAuthority,
	}
	reservation.state.Store(uint32(renderOutputReservationReady))
	return reservation
}

func (r *renderOutputReservation) validate(service *RenderService, generation uint64) error {
	if r == nil || r.seal != r || r.auth.owner != r || r.service == nil ||
		r.auth.service != r.service || r.service != service || r.generation == 0 ||
		r.auth.generation != r.generation || r.generation != generation ||
		r.candidateAuthority == nil || r.auth.candidateAuthority != r.candidateAuthority ||
		r.candidateAuthority.seal != r.candidateAuthority ||
		r.candidateAuthority.reservation != r {
		return errors.New("render output reservation has invalid provenance")
	}
	return nil
}

func newRenderOutputCandidateBinding(
	authority *renderOutputCandidateBindingAuthority,
	candidate *renderOutputCandidate,
) (*renderOutputCandidateBinding, error) {
	if authority == nil || authority.seal != authority || authority.reservation == nil {
		return nil, errors.New("render output candidate binding authority has invalid provenance")
	}
	binding := &renderOutputCandidateBinding{
		authority:   authority,
		reservation: authority.reservation,
		candidate:   candidate,
	}
	binding.seal = binding
	binding.auth = renderOutputCandidateBindingAuthentication{
		owner:       binding,
		authority:   authority,
		reservation: binding.reservation,
		candidate:   candidate,
	}
	if !authority.binding.CompareAndSwap(nil, binding) {
		return nil, errors.New("render output candidate binding authority was already used")
	}
	return binding, nil
}

func (b *renderOutputCandidateBinding) validate(
	reservation *renderOutputReservation,
) (*renderOutputCandidate, error) {
	if b == nil || b.seal != b || b.auth.owner != b || b.authority == nil ||
		b.auth.authority != b.authority || b.reservation == nil ||
		b.auth.reservation != b.reservation || b.reservation != reservation ||
		b.authority != reservation.candidateAuthority ||
		b.authority.seal != b.authority || b.authority.reservation != reservation ||
		b.authority.binding.Load() != b ||
		b.candidate == nil || b.auth.candidate != b.candidate {
		return nil, errors.New("render output candidate binding has invalid provenance")
	}
	if err := b.candidate.validate(); err != nil {
		return nil, err
	}
	return b.candidate, nil
}

func (r *renderOutputReservation) boundCandidate() (*renderOutputCandidate, error) {
	if err := r.validate(r.service, r.generation); err != nil {
		return nil, err
	}
	if r.candidateBinding == nil || r.candidateAuthority.binding.Load() != r.candidateBinding {
		return nil, errors.New("render output candidate binding has invalid provenance")
	}
	return r.candidateBinding.validate(r)
}

func newRenderOutputCandidate(
	reservation *renderOutputReservation,
	output *renderoutput.Snapshot,
	cycle *rendercycle.Snapshot,
) *renderOutputCandidate {
	candidate := &renderOutputCandidate{
		reservation: reservation,
		output:      output,
		cycle:       cycle,
	}
	candidate.seal = candidate
	candidate.auth = renderOutputCandidateAuthentication{
		owner:       candidate,
		reservation: reservation,
		output:      output,
		cycle:       cycle,
	}
	return candidate
}

func (c *renderOutputCandidate) validate() error {
	if c == nil || c.seal != c || c.auth.owner != c || c.reservation == nil ||
		c.auth.reservation != c.reservation || c.auth.output != c.output || c.auth.cycle != c.cycle ||
		(c.output == nil) == (c.cycle == nil) {
		return errors.New("render output candidate has invalid provenance")
	}
	if err := c.reservation.validate(c.reservation.service, c.reservation.generation); err != nil {
		return err
	}
	if c.cycle != nil {
		return c.reservation.service.cycleAuthority.ValidateSnapshot(c.cycle)
	}
	return c.reservation.service.outputAuthority.ValidateSnapshot(c.output)
}

func (c *renderOutputCandidate) exactEqualCurrentLocked() (bool, error) {
	if err := c.validate(); err != nil {
		return false, err
	}
	service := c.reservation.service
	if c.cycle != nil {
		if service.lastCycleSnapshot == nil {
			return false, nil
		}
		return c.cycle.ExactEqual(service.lastCycleSnapshot)
	}
	if service.lastCycleSnapshot != nil || service.lastOutputSnapshot == nil {
		return false, nil
	}
	return c.output.ExactEqual(service.lastOutputSnapshot)
}

func (r *renderOutputReservation) bindCandidate(candidate *renderOutputCandidate) error {
	if err := candidate.validate(); err != nil {
		return err
	}
	if candidate.reservation != r {
		return errors.New("render output candidate belongs to another reservation")
	}
	service := r.service
	service.planMu.Lock()
	defer service.planMu.Unlock()
	if service.outputReservations[r.generation] != r ||
		renderOutputReservationState(r.state.Load()) != renderOutputReservationReady {
		return errors.New("render output reservation is no longer ready")
	}
	if r.candidateBinding != nil {
		return errors.New("render output reservation has conflicting candidates")
	}
	binding, err := newRenderOutputCandidateBinding(r.candidateAuthority, candidate)
	if err != nil {
		return err
	}
	r.candidateBinding = binding
	return nil
}

func (r *renderOutputReservation) revokeCandidateBinding() {
	if r == nil || r.service == nil {
		return
	}
	r.service.planMu.Lock()
	defer r.service.planMu.Unlock()
	if r.service.outputReservations[r.generation] == r && r.state.CompareAndSwap(
		uint32(renderOutputReservationReady), uint32(renderOutputReservationAborted),
	) {
		delete(r.service.outputReservations, r.generation)
	}
}

func (r *renderOutputReservation) beginPublication() error {
	if r == nil {
		return errors.New("render output reservation is unavailable")
	}
	if err := r.validate(r.service, r.generation); err != nil {
		return err
	}
	service := r.service
	service.planMu.Lock()
	if service.outputReservations[r.generation] != r ||
		renderOutputReservationState(r.state.Load()) != renderOutputReservationReady {
		service.planMu.Unlock()
		return errRenderOutputGenerationSuperseded
	}
	if _, err := r.boundCandidate(); err != nil {
		delete(service.outputReservations, r.generation)
		r.state.Store(uint32(renderOutputReservationAborted))
		service.planMu.Unlock()
		return err
	}
	if r.generation <= service.publishedOutputGeneration {
		delete(service.outputReservations, r.generation)
		r.state.Store(uint32(renderOutputReservationAborted))
		service.planMu.Unlock()
		return errRenderOutputGenerationSuperseded
	}
	if !r.state.CompareAndSwap(
		uint32(renderOutputReservationReady), uint32(renderOutputReservationPublishing),
	) {
		service.planMu.Unlock()
		return errors.New("render output reservation is not publishable")
	}
	return nil
}

func (r *renderOutputReservation) resolveStaleCandidateBeforeCommit() (bool, error) {
	if r == nil {
		return true, errors.New("render output reservation is unavailable")
	}
	if err := r.validate(r.service, r.generation); err != nil {
		return true, err
	}
	service := r.service
	service.planMu.Lock()
	defer service.planMu.Unlock()
	if service.outputReservations[r.generation] != r ||
		renderOutputReservationState(r.state.Load()) != renderOutputReservationReady {
		return true, errRenderOutputGenerationSuperseded
	}
	if service.publishedOutputGeneration == 0 {
		return false, nil
	}
	if r.generation > service.publishedOutputGeneration {
		return false, nil
	}
	committed := service.committedOutputReservation.Load()
	if err := committed.validate(service, service.publishedOutputGeneration); err != nil ||
		renderOutputReservationState(committed.state.Load()) != renderOutputReservationCommitted {
		return true, errors.New("committed render output reservation is invalid")
	}
	if _, retained := service.outputReservations[committed.generation]; retained {
		return true, errors.New("committed render output reservation remains publishable")
	}
	candidate, err := r.boundCandidate()
	if err != nil {
		delete(service.outputReservations, r.generation)
		r.state.Store(uint32(renderOutputReservationAborted))
		return true, err
	}
	equal, err := candidate.exactEqualCurrentLocked()
	if err != nil {
		delete(service.outputReservations, r.generation)
		r.state.Store(uint32(renderOutputReservationAborted))
		return true, err
	}
	if equal {
		delete(service.outputReservations, r.generation)
		r.state.Store(uint32(renderOutputReservationAborted))
		return true, nil
	}
	delete(service.outputReservations, r.generation)
	r.state.Store(uint32(renderOutputReservationAborted))
	return true, errRenderOutputGenerationSuperseded
}

func (r *renderOutputReservation) validateCompletion() error {
	if r == nil {
		return errors.New("render output reservation is unavailable")
	}
	if err := r.validate(r.service, r.generation); err != nil {
		return err
	}
	service := r.service
	if service.outputReservations[r.generation] != r {
		return errors.New("render output reservation changed during publication")
	}
	if renderOutputReservationState(r.state.Load()) != renderOutputReservationPublishing ||
		service.publishedOutputGeneration != r.generation {
		return errors.New("render output reservation changed during publication")
	}
	return nil
}

func (r *renderOutputReservation) commitPublication() error {
	if err := r.validateCompletion(); err != nil {
		return err
	}
	if !r.state.CompareAndSwap(
		uint32(renderOutputReservationPublishing), uint32(renderOutputReservationCommitted),
	) {
		return errors.New("render output reservation changed during commit")
	}
	delete(r.service.outputReservations, r.generation)
	r.service.committedOutputReservation.Store(r)
	return nil
}

func (r *renderOutputReservation) releasePublication() {
	if r == nil || r.service == nil {
		return
	}
	r.service.planMu.Unlock()
}

func (r *renderOutputReservation) abortPublication(active bool) {
	if r == nil || r.service == nil {
		return
	}
	if active {
		r.state.Store(uint32(renderOutputReservationAborted))
		if r.service.outputReservations[r.generation] == r {
			delete(r.service.outputReservations, r.generation)
		}
		r.service.planMu.Unlock()
		return
	}
	r.service.planMu.Lock()
	if r.state.CompareAndSwap(
		uint32(renderOutputReservationReady), uint32(renderOutputReservationAborted),
	) && r.service.outputReservations[r.generation] == r {
		delete(r.service.outputReservations, r.generation)
	}
	r.service.planMu.Unlock()
}

func (r *renderOutputReservation) validateCommittedCacheBuild(
	state *incrementalRenderState,
	generation uint64,
) error {
	if r == nil {
		return errors.New("render output reservation is unavailable")
	}
	if err := r.validate(r.service, generation); err != nil {
		return err
	}
	service := r.service
	service.planMu.Lock()
	defer service.planMu.Unlock()
	return r.validateCommittedCacheBuildLocked(state, generation)
}

func (r *renderOutputReservation) beginCommittedCachePublication(
	state *incrementalRenderState,
	generation uint64,
) error {
	if r == nil {
		return errors.New("render output reservation is unavailable")
	}
	if err := r.validate(r.service, generation); err != nil {
		return err
	}
	service := r.service
	service.planMu.Lock()
	if err := r.validateCommittedCacheBuildLocked(state, generation); err != nil {
		service.planMu.Unlock()
		return err
	}
	return nil
}

func (r *renderOutputReservation) validateCommittedCacheBuildLocked(
	state *incrementalRenderState,
	generation uint64,
) error {
	service := r.service
	if state == nil || service.incremental != state || service.committedOutputReservation.Load() != r ||
		service.publishedOutputGeneration != generation || service.nextOutputGeneration < generation ||
		renderOutputReservationState(r.state.Load()) != renderOutputReservationCommitted {
		return errors.New("incremental cache output reservation is not the current committed output")
	}
	if _, retained := service.outputReservations[generation]; retained {
		return errors.New("committed render output reservation remains publishable")
	}
	return nil
}

func (r *renderOutputReservation) endCommittedCachePublication() {
	if r == nil || r.service == nil {
		return
	}
	r.service.planMu.Unlock()
}

// SetAckedPlan records the plan the fleet confirmed it is running. Pods that
// disagree resolve to the newest ACK, which is what this call carries.
func (s *RenderService) SetAckedPlan(plan *renderplan.Plan) {
	if plan == nil || s.skipCurrentConfigProjection {
		return
	}
	owned := plan.Clone()
	s.planMu.Lock()
	defer s.planMu.Unlock()
	previous := s.ackedCurrentConfigRoot
	if previous == nil {
		previous = s.lastCurrentConfigRoot
	}
	s.ackedPlan = owned
	s.ackedCurrentConfigRoot = currentConfigRootForPlan(owned, previous)
}

// buildPlan turns the render into its plan.
func (s *RenderService) buildPlan(
	registry *rendercontext.PlanRegistry,
	config string,
	aux *dataplane.AuxiliaryFiles,
	cacheSession *rendercontext.RenderCacheSession,
) (*renderplan.Plan, *rendercontext.RenderPlanIdentity, error) {
	plan, identity, err := registry.PlanWithCacheIdentity(config, aux, cacheSession)
	if err != nil {
		return nil, nil, fmt.Errorf("building the render plan: %w", err)
	}
	return plan, identity, nil
}

// rememberPlan keeps the newest committed reconcile plan until a pod ACKs one.
func (s *RenderService) rememberPlan(mode rendercontext.RenderMode, plan *renderplan.Plan) {
	if mode != rendercontext.RenderModeReconcile || plan == nil || s.skipCurrentConfigProjection {
		return
	}
	owned := plan.Clone()
	s.rememberOwnedPlan(owned)
}

func (s *RenderService) rememberOwnedPlan(plan *renderplan.Plan) {
	if plan == nil || s.skipCurrentConfigProjection {
		return
	}
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if s.ackedPlan != nil {
		return
	}
	s.lastPlan = plan
	s.lastCurrentConfigRoot = currentConfigRootForPlan(plan, s.lastCurrentConfigRoot)
}

func (s *RenderService) reserveOutputGeneration() (uint64, error) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	return s.reserveOutputGenerationLocked()
}

func (s *RenderService) reserveOutputGenerationLocked() (uint64, error) {
	if s.outputGenerationExhausted {
		return 0, fmt.Errorf("render output generation is exhausted")
	}
	if s.nextOutputGeneration == ^uint64(0) {
		s.outputGenerationExhausted = true
		return 0, fmt.Errorf("render output generation is exhausted")
	}
	s.nextOutputGeneration++
	reservation := newRenderOutputReservation(s, s.nextOutputGeneration)
	if s.outputReservations == nil {
		s.outputReservations = make(map[uint64]*renderOutputReservation)
	}
	s.outputReservations[reservation.generation] = reservation
	return s.nextOutputGeneration, nil
}

func (s *RenderService) outputReservation(generation uint64) (*renderOutputReservation, error) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	reservation := s.outputReservations[generation]
	if err := reservation.validate(s, generation); err != nil {
		return nil, err
	}
	if renderOutputReservationState(reservation.state.Load()) != renderOutputReservationReady {
		return nil, errors.New("render output reservation is no longer ready")
	}
	return reservation, nil
}

func (s *RenderService) rememberOwnedOutput(
	generation uint64,
	plan *renderplan.Plan,
	output *renderoutput.Snapshot,
) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if generation == 0 || generation <= s.publishedOutputGeneration {
		return
	}
	if generation > s.nextOutputGeneration {
		s.nextOutputGeneration = generation
	}
	reservation := newRenderOutputReservation(s, generation)
	reservation.state.Store(uint32(renderOutputReservationCommitted))
	s.committedOutputReservation.Store(reservation)
	s.publishedOutputGeneration = generation
	if s.ackedPlan == nil && plan != nil {
		s.lastPlan = plan
		s.lastCurrentConfigRoot = currentConfigRootForPlan(plan, s.lastCurrentConfigRoot)
	}
	s.lastOutputSnapshot = output
	s.lastCycleSnapshot = nil
	s.lastPlanIdentity = nil
	s.lastRenderCache = nil
}

func (s *RenderService) previousCycleState() (
	*rendercycle.Snapshot,
	*rendercontext.RenderPlanIdentity,
	*exactCycleCurrentConfigRoot,
) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	return s.lastCycleSnapshot, s.lastPlanIdentity, s.lastCurrentConfigRoot
}

// currentConfig is what templates read as `currentConfig`: the servers of the
// plan the fleet ACKed, or of the last committed reconcile render until one does.
func (s *RenderService) currentConfig() *renderplan.CurrentConfig {
	s.planMu.Lock()
	plan := s.ackedPlan
	if plan == nil {
		plan = s.lastPlan
	}
	root := s.lastCurrentConfigRoot
	s.planMu.Unlock()
	if plan != nil {
		current := plan.CurrentConfig()
		return &current
	}
	if root == nil {
		return nil
	}
	current, err := root.materialize()
	if err != nil {
		return nil
	}
	return &current
}
