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

package httpstore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

type componentPreparedState uint8

const (
	componentPreparedReady componentPreparedState = iota
	componentPreparedSealed
	componentPreparedPublished
	componentPreparedCommitted
	componentPreparedReleased
)

type preparedCandidateCommit struct {
	mu          sync.Mutex
	component   *Component
	authority   chan struct{}
	store       *purehttpstore.PreparedInitialCandidateCommit
	sourceURLs  []string
	changed     []string
	commits     []purehttpstore.CandidateCommit
	watermark   purehttpstore.Revision
	active      purehttpstore.ActiveLeaseToken
	transition  purehttpstore.ActiveLeaseTransition
	hasActive   bool
	refreshers  bool
	replay      *purehttpstore.AcceptedReplayState
	inputPlan   *preparedInputPublicationPlan
	releasePlan *preparedComponentReleasePlan
	nextRequest *events.ProposalValidationRequestedEvent
	required    bool
	state       componentPreparedState
}

type preparedURLRefreshState struct {
	url    string
	state  purehttpstore.SourceState
	exists bool
}

type preparedComponentReleasePlanAuthentication struct {
	owner                   *preparedCandidateCommit
	component               *Component
	authority               chan struct{}
	refreshers              bool
	stopBefore              []string
	reconcile               []preparedURLRefreshState
	stopAfter               []string
	pendingBefore           *validationBatch
	pendingBeforeEntries    []validationBatchEntry
	queuedBefore            string
	pendingAfter            *validationBatch
	pendingAfterEntries     []validationBatchEntry
	queuedAfter             string
	request                 *events.ProposalValidationRequestedEvent
	requestID               string
	requestOverlay          *purehttpstore.HTTPOverlay
	requestSource           string
	requestSourceContext    string
	stopped                 bool
	currentRefreshers       map[string]*time.Timer
	currentManaged          map[string]bool
	currentPending          map[string]bool
	currentImmediate        map[string]bool
	currentGeneration       map[string]uint64
	currentSourceGeneration map[string]uint64
}

type preparedComponentReleasePlan struct {
	owner         *preparedCandidateCommit
	component     *Component
	authority     chan struct{}
	refreshers    bool
	stopBefore    []string
	reconcile     []preparedURLRefreshState
	stopAfter     []string
	pendingBefore *validationBatch
	queuedBefore  string
	pendingAfter  *validationBatch
	queuedAfter   string
	request       *events.ProposalValidationRequestedEvent
	stopped       bool
	auth          preparedComponentReleasePlanAuthentication
	seal          *preparedComponentReleasePlan
}

type preparedInputPublicationPlanAuthentication struct {
	owner     *preparedCandidateCommit
	replay    *purehttpstore.AcceptedReplayState
	snapshots []purehttpstore.ContentSnapshot
	cacheable bool
}

type preparedInputPublicationPlan struct {
	owner     *preparedCandidateCommit
	replay    *purehttpstore.AcceptedReplayState
	snapshots []purehttpstore.ContentSnapshot
	cacheable bool
	auth      preparedInputPublicationPlanAuthentication
	seal      *preparedInputPublicationPlan
}

func newPreparedInputPublicationPlan(
	owner *preparedCandidateCommit,
	replay *purehttpstore.AcceptedReplayState,
	snapshots []purehttpstore.ContentSnapshot,
	cacheable bool,
) *preparedInputPublicationPlan {
	plan := &preparedInputPublicationPlan{
		owner: owner, replay: replay, snapshots: slices.Clone(snapshots), cacheable: cacheable,
	}
	plan.auth = preparedInputPublicationPlanAuthentication{
		owner: owner, replay: replay, snapshots: slices.Clone(plan.snapshots), cacheable: cacheable,
	}
	plan.seal = plan
	return plan
}

func (p *preparedInputPublicationPlan) validate(owner *preparedCandidateCommit) error {
	if p == nil || p.seal != p || p.owner != owner || p.owner != p.auth.owner ||
		p.replay != p.auth.replay || p.cacheable != p.auth.cacheable ||
		!slices.Equal(p.snapshots, p.auth.snapshots) || !p.cacheable && p.replay != nil ||
		p.cacheable && p.replay == nil && (p.owner != nil || len(p.snapshots) != 0) {
		return errors.New("prepared HTTP input plan is invalid")
	}
	if p.replay != nil && p.replay.ValidateAuthentication() != nil {
		return errors.New("prepared HTTP input replay state is invalid")
	}
	return nil
}

func (c *Component) PrepareInitialCandidatesAndVerifyObservations(
	ctx context.Context,
	candidates []*purehttpstore.InitialCandidate,
	observations []purehttpstore.ObservationToken,
) (*preparedCandidateCommit, error) {
	return c.prepareStagedSourcesAndVerifyObservations(
		ctx, nil, candidates, nil, observations, nil, true, nil, nil,
	)
}

func (c *Component) prepareStagedSourcesAndVerifyObservations(
	ctx context.Context,
	sources []*purehttpstore.StagedSource,
	candidates []*purehttpstore.InitialCandidate,
	verificationOnly []purehttpstore.ObservationToken,
	retained []purehttpstore.ObservationToken,
	active *purehttpstore.ActiveLeaseCommit,
	refreshers bool,
	replayEpoch *purehttpstore.ReplayEpoch,
	replayState *purehttpstore.AcceptedReplayState,
) (*preparedCandidateCommit, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("preparing validated render inputs: %w", context.Cause(ctx))
	case <-c.prepareAuthority:
	}

	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		c.prepareAuthority <- struct{}{}
		return nil, errors.New("HTTP store stopped before validated render inputs could be accepted")
	}
	prepared, err := c.store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayState(
		ctx,
		append([]*purehttpstore.StagedSource(nil), sources...),
		append([]*purehttpstore.InitialCandidate(nil), candidates...),
		append([]purehttpstore.ObservationToken(nil), verificationOnly...),
		append([]purehttpstore.ObservationToken(nil), retained...),
		active,
		replayEpoch,
		replayState,
	)
	if err != nil {
		c.mu.Unlock()
		c.prepareAuthority <- struct{}{}
		return nil, err
	}
	commits, watermark := prepared.Planned()
	activeToken, transition, hasActive := prepared.PlannedActiveLeases()
	allURLs := make(map[string]struct{}, len(sources)+len(candidates))
	changedURLs := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		allURLs[source.URL()] = struct{}{}
		if source.Changed() {
			changedURLs[source.URL()] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		allURLs[candidate.URL()] = struct{}{}
	}
	return &preparedCandidateCommit{
		component:  c,
		authority:  c.prepareAuthority,
		store:      prepared,
		sourceURLs: sortedSet(allURLs),
		changed:    sortedSet(changedURLs),
		commits:    commits,
		watermark:  watermark,
		active:     activeToken,
		transition: transition,
		hasActive:  hasActive,
		refreshers: refreshers,
	}, nil
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func (c *preparedCandidateCommit) prepareReleasePlanLocked() (*preparedComponentReleasePlan, error) {
	component := c.component
	if component == nil || component.store == nil || component.eventBus == nil ||
		component.prepareAuthority != c.authority || component.refreshers == nil ||
		component.refreshManaged == nil || component.refreshPending == nil ||
		component.refreshImmediate == nil || component.refreshGeneration == nil ||
		component.refreshSourceGeneration == nil {
		return nil, errors.New("prepared HTTP component release state is invalid")
	}
	plan := &preparedComponentReleasePlan{
		owner: c, component: component, authority: c.authority, refreshers: c.refreshers,
		pendingBefore: component.pendingValidation, queuedBefore: component.queuedValidationSource,
		pendingAfter: component.pendingValidation, queuedAfter: component.queuedValidationSource,
		stopped: component.stopped,
	}
	if err := c.planRefresherTransitionsLocked(plan); err != nil {
		return nil, err
	}
	if err := c.planValidationRetirementLocked(plan); err != nil {
		return nil, err
	}
	plan.auth = preparedComponentReleasePlanAuthentication{
		owner: c, component: component, authority: plan.authority, refreshers: plan.refreshers,
		stopBefore: slices.Clone(plan.stopBefore), reconcile: slices.Clone(plan.reconcile),
		stopAfter: slices.Clone(plan.stopAfter), pendingBefore: plan.pendingBefore,
		pendingBeforeEntries: cloneValidationBatchEntries(plan.pendingBefore), queuedBefore: plan.queuedBefore,
		pendingAfter: plan.pendingAfter, pendingAfterEntries: cloneValidationBatchEntries(plan.pendingAfter),
		queuedAfter: plan.queuedAfter, request: plan.request,
		stopped:                 component.stopped,
		currentRefreshers:       maps.Clone(component.refreshers),
		currentManaged:          maps.Clone(component.refreshManaged),
		currentPending:          maps.Clone(component.refreshPending),
		currentImmediate:        maps.Clone(component.refreshImmediate),
		currentGeneration:       maps.Clone(component.refreshGeneration),
		currentSourceGeneration: maps.Clone(component.refreshSourceGeneration),
	}
	if plan.request != nil {
		overlay, ok := plan.request.HTTPOverlay.(*purehttpstore.HTTPOverlay)
		if !ok {
			return nil, errors.New("prepared HTTP validation request has an invalid overlay")
		}
		plan.auth.requestID = plan.request.ID
		plan.auth.requestOverlay = overlay
		plan.auth.requestSource = plan.request.Source
		plan.auth.requestSourceContext = plan.request.SourceContext
	}
	plan.seal = plan
	if err := plan.validate(c); err != nil {
		return nil, err
	}
	return plan, nil
}

func (c *preparedCandidateCommit) planRefresherTransitionsLocked(plan *preparedComponentReleasePlan) error {
	if !c.refreshers {
		return nil
	}
	plan.stopBefore = slices.Clone(c.changed)
	for _, url := range append(slices.Clone(c.sourceURLs), c.transition.Activated...) {
		state, exists, err := c.store.PlannedSourceState(url)
		if err != nil {
			return err
		}
		plan.reconcile = append(plan.reconcile, preparedURLRefreshState{
			url: url, state: state, exists: exists,
		})
	}
	sourceURLs := make(map[string]struct{}, len(c.sourceURLs))
	for _, url := range c.sourceURLs {
		sourceURLs[url] = struct{}{}
	}
	for _, url := range c.transition.Retired {
		if _, used := sourceURLs[url]; used {
			continue
		}
		active, err := c.store.PlannedHasActiveLease(url)
		if err != nil {
			return err
		}
		if !active {
			plan.stopAfter = append(plan.stopAfter, url)
		}
	}
	return nil
}

func (c *preparedCandidateCommit) planValidationRetirementLocked(plan *preparedComponentReleasePlan) error {
	retiredValidation := false
	for _, url := range c.changed {
		if plan.queuedAfter == url {
			plan.queuedAfter = ""
		}
		if validationBatchContains(plan.pendingAfter, url) {
			plan.pendingAfter = nil
			plan.queuedAfter = ""
			retiredValidation = true
		}
	}
	if retiredValidation {
		overlay, err := c.store.PlannedPendingOverlay()
		if err != nil {
			return err
		}
		plan.request, plan.pendingAfter = prepareValidationRequest(overlay, "")
	}
	return nil
}

func validationBatchContains(batch *validationBatch, url string) bool {
	if batch == nil {
		return false
	}
	for _, entry := range batch.entries {
		if entry.url == url {
			return true
		}
	}
	return false
}

func cloneValidationBatchEntries(batch *validationBatch) []validationBatchEntry {
	if batch == nil {
		return nil
	}
	return slices.Clone(batch.entries)
}

func samePreparedValidationBatch(
	batch *validationBatch,
	expected *validationBatch,
	entries []validationBatchEntry,
) bool {
	return batch == expected && (batch == nil ||
		batch.requestID == expected.requestID && slices.Equal(batch.entries, entries))
}

func (p *preparedComponentReleasePlan) validate(owner *preparedCandidateCommit) error {
	if !p.sealedIdentityIntact(owner) || !p.componentWiringIntact() || !p.componentStateUnchanged() {
		return errors.New("prepared HTTP component release plan failed authentication")
	}
	for _, url := range p.stopBefore {
		if url == "" {
			return errors.New("prepared HTTP component release plan has an invalid URL")
		}
	}
	for _, action := range p.reconcile {
		if action.url == "" {
			return errors.New("prepared HTTP component release plan has an invalid URL")
		}
	}
	for _, url := range p.stopAfter {
		if url == "" {
			return errors.New("prepared HTTP component release plan has an invalid URL")
		}
	}
	return p.validateRequest()
}

func (p *preparedComponentReleasePlan) sealedIdentityIntact(owner *preparedCandidateCommit) bool {
	return p != nil && p.seal == p && p.owner == owner && p.component != nil && p.authority != nil &&
		p.owner == p.auth.owner && p.component == p.auth.component && p.authority == p.auth.authority &&
		p.refreshers == p.auth.refreshers && slices.Equal(p.stopBefore, p.auth.stopBefore) &&
		slices.Equal(p.reconcile, p.auth.reconcile) && slices.Equal(p.stopAfter, p.auth.stopAfter) &&
		p.pendingBefore == p.auth.pendingBefore && p.queuedBefore == p.auth.queuedBefore &&
		p.pendingAfter == p.auth.pendingAfter && p.queuedAfter == p.auth.queuedAfter &&
		p.request == p.auth.request
}

func (p *preparedComponentReleasePlan) componentWiringIntact() bool {
	return p.component.prepareAuthority == p.authority &&
		cap(p.authority) == 1 && len(p.authority) == 0 && p.component.refreshers != nil &&
		p.component.refreshManaged != nil && p.component.refreshPending != nil &&
		p.component.refreshImmediate != nil && p.component.refreshGeneration != nil &&
		p.component.refreshSourceGeneration != nil && p.component.eventBus != nil
}

func (p *preparedComponentReleasePlan) componentStateUnchanged() bool {
	return p.stopped == p.auth.stopped && p.component.stopped == p.auth.stopped &&
		maps.Equal(p.component.refreshers, p.auth.currentRefreshers) &&
		maps.Equal(p.component.refreshManaged, p.auth.currentManaged) &&
		maps.Equal(p.component.refreshPending, p.auth.currentPending) &&
		maps.Equal(p.component.refreshImmediate, p.auth.currentImmediate) &&
		maps.Equal(p.component.refreshGeneration, p.auth.currentGeneration) &&
		maps.Equal(p.component.refreshSourceGeneration, p.auth.currentSourceGeneration) &&
		p.component.pendingValidation == p.pendingBefore &&
		p.component.queuedValidationSource == p.queuedBefore &&
		samePreparedValidationBatch(p.pendingBefore, p.auth.pendingBefore, p.auth.pendingBeforeEntries) &&
		samePreparedValidationBatch(p.pendingAfter, p.auth.pendingAfter, p.auth.pendingAfterEntries)
}

func (p *preparedComponentReleasePlan) validateRequest() error {
	if p.request == nil {
		if p.pendingAfter != nil && p.pendingAfter != p.pendingBefore {
			return errors.New("prepared HTTP validation batch has no request")
		}
		return nil
	}
	overlay, ok := p.request.HTTPOverlay.(*purehttpstore.HTTPOverlay)
	if !ok || overlay != p.auth.requestOverlay || p.request.ID != p.auth.requestID ||
		p.request.Source != p.auth.requestSource ||
		p.request.SourceContext != p.auth.requestSourceContext || len(p.request.Overlays) != 0 ||
		p.pendingAfter == nil || p.pendingAfter.requestID != p.request.ID {
		return errors.New("prepared HTTP validation request failed authentication")
	}
	return nil
}

func (p *preparedComponentReleasePlan) applyRequired() *events.ProposalValidationRequestedEvent {
	component := p.component
	component.pendingValidation = p.pendingAfter
	component.queuedValidationSource = p.queuedAfter
	return p.request
}

func (c *preparedCandidateCommit) Planned() ([]purehttpstore.CandidateCommit, purehttpstore.Revision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == componentPreparedReleased {
		return nil, 0
	}
	return append([]purehttpstore.CandidateCommit(nil), c.commits...), c.watermark
}

func (c *preparedCandidateCommit) validatePublication() error {
	if c == nil {
		return errors.New("prepared HTTP component publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != componentPreparedReady && c.state != componentPreparedSealed ||
		c.component == nil || c.store == nil || c.authority == nil ||
		c.component.prepareAuthority != c.authority ||
		cap(c.component.prepareAuthority) != 1 || len(c.component.prepareAuthority) != 0 {
		return errors.New("prepared HTTP component publication is not ready")
	}
	if c.replay != nil && c.replay.ValidateAuthentication() != nil {
		return errors.New("prepared HTTP component replay state is invalid")
	}
	if c.state == componentPreparedSealed {
		if err := c.releasePlan.validate(c); err != nil {
			return err
		}
	}
	return c.store.ValidatePublication()
}

func (c *preparedCandidateCommit) sealPublication() error {
	if c == nil {
		return errors.New("prepared HTTP component publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sealPublicationLocked()
}

func (c *preparedCandidateCommit) sealPublicationLocked() error {
	if c.state == componentPreparedSealed {
		return nil
	}
	if c.state != componentPreparedReady || c.component == nil || c.store == nil || c.authority == nil ||
		c.component.prepareAuthority != c.authority || cap(c.component.prepareAuthority) != 1 ||
		len(c.component.prepareAuthority) != 0 {
		return errors.New("prepared HTTP component publication is not ready")
	}
	if c.replay != nil && c.replay.ValidateAuthentication() != nil {
		return errors.New("prepared HTTP component replay state is invalid")
	}
	if err := c.store.SealPublication(); err != nil {
		return err
	}
	releasePlan, err := c.prepareReleasePlanLocked()
	if err != nil {
		return err
	}
	c.releasePlan = releasePlan
	c.state = componentPreparedSealed
	return nil
}

func (c *preparedCandidateCommit) PlannedActiveLeases() (
	purehttpstore.ActiveLeaseToken,
	purehttpstore.ActiveLeaseTransition,
	bool,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == componentPreparedReleased || !c.hasActive {
		return purehttpstore.ActiveLeaseToken{}, purehttpstore.ActiveLeaseTransition{}, false
	}
	return c.active, purehttpstore.ActiveLeaseTransition{
		Activated: slices.Clone(c.transition.Activated),
		Retired:   slices.Clone(c.transition.Retired),
	}, true
}

func (c *preparedCandidateCommit) preparePublishedReplayActiveLeases(
	active *purehttpstore.ActiveLeaseCommit,
	snapshots []purehttpstore.ContentSnapshot,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != componentPreparedReady || c.store == nil || c.hasActive {
		return errors.New("published HTTP replay lease cannot be prepared")
	}
	if err := c.store.PreparePublishedReplayActiveLeases(active, snapshots); err != nil {
		return err
	}
	token, transition, ok := c.store.PlannedActiveLeases()
	if !ok {
		return errors.New("published HTTP replay lease has no prepared transition")
	}
	c.active = token
	c.transition = transition
	c.hasActive = true
	return nil
}

func (c *preparedCandidateCommit) prepareAcceptedReplayState(
	snapshots []purehttpstore.ContentSnapshot,
) (*purehttpstore.AcceptedReplayState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != componentPreparedReady || c.store == nil || c.replay != nil {
		return nil, errors.New("accepted HTTP component replay state cannot be prepared")
	}
	replay, err := c.store.PrepareAcceptedReplayState(snapshots)
	if err != nil {
		return nil, err
	}
	c.replay = replay
	return replay, nil
}

func (c *preparedCandidateCommit) bindInputPlan(
	replay *purehttpstore.AcceptedReplayState,
	snapshots []purehttpstore.ContentSnapshot,
	cacheable bool,
) (*preparedInputPublicationPlan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != componentPreparedReady || c.store == nil || c.inputPlan != nil {
		return nil, errors.New("prepared HTTP input plan cannot be bound")
	}
	if cacheable {
		expected := c.replay
		if expected == nil {
			expected, _ = c.store.PlannedActiveReplayState()
		}
		if replay == nil || replay != expected || replay.ValidateAuthentication() != nil {
			return nil, errors.New("prepared HTTP input replay state does not match its component commit")
		}
	} else if replay != nil {
		return nil, errors.New("non-cacheable HTTP input plan has a replay state")
	}
	c.inputPlan = newPreparedInputPublicationPlan(c, replay, snapshots, cacheable)
	return c.inputPlan, nil
}

func (c *preparedCandidateCommit) validateInputPlan(
	plan *preparedInputPublicationPlan,
	replay *purehttpstore.AcceptedReplayState,
	snapshots []purehttpstore.ContentSnapshot,
	cacheable bool,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inputPlan == nil || c.inputPlan != plan {
		return errors.New("prepared HTTP input plan does not belong to its component commit")
	}
	if err := plan.validate(c); err != nil {
		return err
	}
	if plan.replay != replay || plan.cacheable != cacheable || !slices.Equal(plan.snapshots, snapshots) {
		return errors.New("prepared HTTP input publication does not match its component plan")
	}
	return nil
}

func (c *preparedCandidateCommit) Publish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == componentPreparedReady {
		if err := c.sealPublicationLocked(); err != nil {
			panic(fmt.Sprintf("prepared HTTP component publication failed authentication: %v", err))
		}
	}
	if c.state == componentPreparedSealed {
		c.publishSealedLocked()
	}
}

func (c *preparedCandidateCommit) publishSealed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == componentPreparedPublished {
		return
	}
	if c.state != componentPreparedSealed {
		panic("prepared HTTP component publication is not sealed")
	}
	c.publishSealedLocked()
}

func (c *preparedCandidateCommit) publishSealedLocked() {
	if err := c.releasePlan.validate(c); err != nil {
		panic(fmt.Sprintf("prepared HTTP component release plan failed authentication: %v", err))
	}
	c.store.PublishSealed()
	c.state = componentPreparedPublished
}

func (c *preparedCandidateCommit) validatePublishedPublication() error {
	if c == nil {
		return errors.New("prepared HTTP component publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validatePublishedPublicationLocked()
}

func (c *preparedCandidateCommit) validatePublishedPublicationLocked() error {
	if c.state != componentPreparedPublished || c.component == nil || c.store == nil ||
		c.authority == nil || c.component.prepareAuthority != c.authority ||
		cap(c.authority) != 1 || len(c.authority) != 0 {
		return errors.New("prepared HTTP component publication is not published")
	}
	if c.replay != nil && c.replay.ValidateAuthentication() != nil {
		return errors.New("prepared HTTP component replay state is invalid")
	}
	if err := c.releasePlan.validate(c); err != nil {
		return err
	}
	return c.store.ValidatePublishedPublication()
}

func (c *preparedCandidateCommit) commitPublishedPublication() error {
	if c == nil {
		return errors.New("prepared HTTP component publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commitPublishedPublicationLocked()
}

func (c *preparedCandidateCommit) commitPublishedPublicationLocked() error {
	if c.state == componentPreparedCommitted {
		return nil
	}
	if err := c.validatePublishedPublicationLocked(); err != nil {
		return err
	}
	if err := c.store.CommitPublishedPublication(); err != nil {
		return err
	}
	c.nextRequest = c.releasePlan.applyRequired()
	c.required = true
	c.state = componentPreparedCommitted
	return nil
}

func (c *preparedCandidateCommit) releaseCommittedPublication() {
	if c == nil {
		panic("prepared HTTP component publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == componentPreparedReleased {
		return
	}
	if c.state != componentPreparedCommitted {
		panic("prepared HTTP component publication is not committed")
	}
	c.releaseCommittedPublicationLocked()
}

func (c *preparedCandidateCommit) releaseCommittedPublicationLocked() {
	c.store.ReleaseCommittedPublication()
	c.state = componentPreparedReleased
	component := c.component
	authority := c.authority
	plan := c.releasePlan
	request := c.nextRequest
	applyPreparedOptionalRefreshers(plan)
	component.mu.Unlock()
	returnPreparedComponentAuthority(authority)
	if !publishPreparedValidationRequest(component, request) && request != nil {
		component.mu.Lock()
		if component.pendingValidation != nil && component.pendingValidation.requestID == request.ID {
			batch := component.pendingValidation
			component.pendingValidation = nil
			component.queuedValidationSource = ""
			component.mu.Unlock()
			for _, entry := range batch.entries {
				component.store.DiscardPendingVersion(entry.url, entry.checksum, entry.revision)
			}
			return
		}
		component.mu.Unlock()
	}
}

func (c *preparedCandidateCommit) plannedActiveReplayState() (
	*purehttpstore.AcceptedReplayState,
	bool,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != componentPreparedReady && c.state != componentPreparedSealed || c.store == nil {
		return nil, false
	}
	return c.store.PlannedActiveReplayState()
}

func (c *preparedCandidateCommit) Release() {
	if err := c.finish(true); err != nil {
		panic("prepared HTTP component publication failed final authentication: " + err.Error())
	}
}

func (c *preparedCandidateCommit) finish(commit bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == componentPreparedReleased {
		return nil
	}
	if commit && c.state == componentPreparedPublished {
		if err := c.commitPublishedPublicationLocked(); err != nil {
			return errors.Join(err, c.abortPublicationLocked())
		}
	}
	if commit && c.state == componentPreparedCommitted {
		c.releaseCommittedPublicationLocked()
		return nil
	}
	return c.abortPublicationLocked()
}

func (c *preparedCandidateCommit) abortPublicationLocked() error {
	if c.required {
		c.component.pendingValidation = c.releasePlan.pendingBefore
		c.component.queuedValidationSource = c.releasePlan.queuedBefore
		c.nextRequest = nil
		c.required = false
	}
	err := c.store.AbortPublication()
	c.state = componentPreparedReleased
	component := c.component
	authority := c.authority
	if authority != nil && cap(authority) == 1 {
		if component.prepareAuthority != authority {
			component.prepareAuthority = authority
		}
	}
	component.mu.Unlock()
	returnPreparedComponentAuthority(authority)
	return err
}

func returnPreparedComponentAuthority(authority chan struct{}) {
	defer func() { _ = recover() }()
	if authority == nil || cap(authority) != 1 {
		return
	}
	select {
	case authority <- struct{}{}:
	default:
	}
}

func publishPreparedValidationRequest(
	component *Component,
	request *events.ProposalValidationRequestedEvent,
) (published bool) {
	if request == nil {
		return true
	}
	defer func() { _ = recover() }()
	component.publishValidationRequest(request)
	return true
}

func applyPreparedOptionalRefreshers(plan *preparedComponentReleasePlan) {
	if plan == nil || !plan.refreshers {
		return
	}
	for _, url := range plan.stopBefore {
		callPreparedOptionalRefresher(func() { plan.component.stopRefresherLocked(url) })
	}
	for _, action := range plan.reconcile {
		callPreparedOptionalRefresher(func() {
			plan.component.reconcilePreparedURLLocked(action.url, action.state, action.exists)
		})
	}
	for _, url := range plan.stopAfter {
		callPreparedOptionalRefresher(func() { plan.component.stopRefresherLocked(url) })
	}
}

func callPreparedOptionalRefresher(action func()) {
	defer func() { _ = recover() }()
	action()
}

func (c *preparedCandidateCommit) Abort() {
	if err := c.finish(false); err != nil {
		panic("prepared HTTP component publication failed rollback authentication: " + err.Error())
	}
}
