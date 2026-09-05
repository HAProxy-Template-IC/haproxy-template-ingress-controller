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
	"slices"
	"sync"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
)

type preparedCommitState uint8

const (
	preparedCommitReady preparedCommitState = iota
	preparedCommitSealed
	preparedCommitPublished
	preparedCommitCommitted
	preparedCommitReleased
)

// PreparedInitialCandidateCommit retains exact store authority until Release.
type PreparedInitialCandidateCommit struct {
	mu          sync.Mutex
	store       *HTTPStore
	authority   chan struct{}
	sources     []preparedSourcePlan
	candidates  []*InitialCandidate
	entries     []*CacheEntry
	commits     []CandidateCommit
	watermark   Revision
	active      *preparedActiveLeasePlan
	planned     *AcceptedReplayState
	snapshots   []ContentSnapshot
	publication *preparedHTTPStorePublication
	rollback    *preparedHTTPRollbackCheckpoint
	state       preparedCommitState
}

// PrepareAcceptedReplayState authenticates the exact post-publication accepted inputs.
func (c *PreparedInitialCandidateCommit) PrepareAcceptedReplayState(
	snapshots []ContentSnapshot,
) (*AcceptedReplayState, error) {
	if c == nil {
		return nil, errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != preparedCommitReady || c.store == nil || c.planned != nil {
		return nil, errors.New("accepted HTTP replay state cannot be prepared")
	}
	validated, err := c.validatePublishedReplaySnapshotsLocked(snapshots)
	if err != nil {
		return nil, err
	}
	state, err := c.preparePublishedReplayStateLocked(validated)
	if err != nil {
		return nil, err
	}
	c.planned = state
	c.snapshots = validated
	return state, nil
}

type preparedSourcePlan struct {
	source         *StagedSource
	generation     uint64
	publishedEntry *CacheEntry
}

// ValidatePublication verifies the retained terminal publication without changing store state.
func (c *PreparedInitialCandidateCommit) ValidatePublication() error {
	if c == nil {
		return errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if (c.state != preparedCommitReady && c.state != preparedCommitSealed) || c.store == nil {
		return errors.New("prepared HTTP publication is not ready")
	}
	if c.state == preparedCommitSealed {
		return c.publication.validate(c, c.store)
	}
	return c.validatePublicationLocked()
}

// SealPublication authenticates every terminal value while the store lock is retained.
func (c *PreparedInitialCandidateCommit) SealPublication() error {
	if c == nil {
		return errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitSealed {
		return nil
	}
	if c.state != preparedCommitReady || c.store == nil {
		return errors.New("prepared HTTP publication is not ready")
	}
	return c.sealPublicationLocked()
}

func (c *PreparedInitialCandidateCommit) sealPublicationLocked() error {
	if err := c.validatePublicationLocked(); err != nil {
		return err
	}
	publication, err := c.preparePublicationLocked()
	if err != nil {
		return err
	}
	c.publication = publication
	c.rollback = newPreparedHTTPRollbackCheckpoint(c.store, c.authority, publication)
	c.state = preparedCommitSealed
	return nil
}

func (c *PreparedInitialCandidateCommit) validatePublicationLocked() error {
	if c.store == nil || c.authority == nil || c.store.prepareAuthority != c.authority {
		return errors.New("prepared HTTP publication authority is invalid")
	}
	if err := c.store.validatePublicationBaseLocked(); err != nil {
		return err
	}
	sourceByURL, changedSources, err := c.validateSourcePlansLocked()
	if err != nil {
		return err
	}
	semanticChanges := changedSources + uint64(len(c.candidates))
	if err := c.store.validateInitialCandidatesLocked(c.candidates, sourceByURL); err != nil {
		return err
	}
	if semanticChanges > ^uint64(0)-uint64(c.store.semanticRevision) ||
		semanticChanges > ^uint64(0)-uint64(c.store.replayRevision) {
		return errors.New("prepared HTTP publication revision capacity is exhausted")
	}
	if c.watermark != c.store.semanticRevision+Revision(semanticChanges) ||
		len(c.commits) != len(c.candidates) || len(c.entries) != len(c.candidates) {
		return errors.New("prepared HTTP publication token plan is invalid")
	}
	if err := c.validateCommitPlansLocked(sourceByURL, changedSources); err != nil {
		return err
	}
	if err := c.store.validatePreparedActiveLeasePlanLocked(c.active); err != nil {
		return err
	}
	if err := c.validateReplayPlansLocked(semanticChanges); err != nil {
		return err
	}
	return c.store.validateActiveLeaseChangeCapacityLocked(c.sources, c.candidates)
}

func (c *PreparedInitialCandidateCommit) validateReplayPlansLocked(semanticChanges uint64) error {
	if c.active != nil && len(c.active.publishedReplay) != 0 {
		if _, err := c.validatePublishedReplaySnapshotsLocked(c.active.publishedReplay); err != nil {
			return err
		}
		if err := c.validatePreparedReplayStateLocked(
			c.active.replay,
			c.active.publishedReplay,
			semanticChanges,
		); err != nil {
			return err
		}
	}
	if c.planned != nil {
		if _, err := c.validatePublishedReplaySnapshotsLocked(c.snapshots); err != nil {
			return err
		}
		if err := c.validatePreparedReplayStateLocked(c.planned, c.snapshots, semanticChanges); err != nil {
			return err
		}
	}
	return nil
}

func (c *PreparedInitialCandidateCommit) validateSourcePlansLocked() (
	sourceByURL map[string]*StagedSource,
	changedSources uint64,
	err error,
) {
	sourceByURL = make(map[string]*StagedSource, len(c.sources))
	nextGeneration := c.store.nextSourceGeneration
	for index := range c.sources {
		plan := &c.sources[index]
		if err := c.store.validateStagedSourceLocked(plan.source); err != nil {
			return nil, 0, err
		}
		if _, exists := sourceByURL[plan.source.url]; exists {
			return nil, 0, fmt.Errorf("staged HTTP source for %s appears more than once", plan.source.url)
		}
		sourceByURL[plan.source.url] = plan.source
		if !plan.source.Changed() {
			if err := c.validateUnchangedSourcePlanLocked(plan); err != nil {
				return nil, 0, err
			}
			continue
		}
		if nextGeneration == ^uint64(0) {
			return nil, 0, errors.New("HTTP store source generation exhausted")
		}
		nextGeneration++
		changedSources++
		if err := validateChangedSourcePlan(plan, nextGeneration); err != nil {
			return nil, 0, err
		}
	}
	return sourceByURL, changedSources, nil
}

func validateChangedSourcePlan(plan *preparedSourcePlan, generation uint64) error {
	if plan.generation != generation {
		return errors.New("prepared HTTP source generation is invalid")
	}
	if !validPreparedPublishedSourceEntry(plan) {
		return errors.New("prepared HTTP source entry is invalid")
	}
	return nil
}

func (c *PreparedInitialCandidateCommit) validateUnchangedSourcePlanLocked(
	plan *preparedSourcePlan,
) error {
	if plan.generation != plan.source.baseGeneration {
		return errors.New("prepared HTTP source generation changed without a source transition")
	}
	if plan.publishedEntry == nil || plan.source.baseEntry == nil ||
		plan.publishedEntry == plan.source.baseEntry ||
		c.store.cache[plan.source.url] != plan.source.baseEntry ||
		!samePreparedCacheEntry(plan.publishedEntry, plan.source.baseEntry) {
		return errors.New("prepared HTTP source entry changed before publication")
	}
	return nil
}

func (c *PreparedInitialCandidateCommit) validateCommitPlansLocked(
	sourceByURL map[string]*StagedSource,
	changedSources uint64,
) error {
	for index := range c.commits {
		candidate := c.candidates[index]
		if c.entries[index] == nil {
			return errors.New("prepared HTTP publication entry plan is invalid")
		}
		if candidate.source != nil {
			planned := sourceByURL[candidate.url]
			if planned == nil {
				return errors.New("prepared HTTP publication entry has no source plan")
			}
			for sourceIndex := range c.sources {
				if c.sources[sourceIndex].source == planned &&
					c.sources[sourceIndex].publishedEntry != c.entries[index] {
					return errors.New("prepared HTTP publication entry does not match its source plan")
				}
			}
		} else if candidate.entry == nil || !samePreparedCacheEntry(c.entries[index], candidate.entry) {
			return errors.New("prepared HTTP publication entry changed before publication")
		}
		expected := CandidateCommit{
			Candidate: candidate.token,
			Accepted: SnapshotToken{
				source:     c.store.revisionSource,
				url:        candidate.url,
				descriptor: candidate.sourceDescriptor,
				kind:       SnapshotAccepted,
				revision: c.store.semanticRevision +
					Revision(changedSources+uint64(index)) + 1,
			},
		}
		if c.commits[index] != expected {
			return errors.New("prepared HTTP publication token plan is invalid")
		}
	}
	return nil
}

func samePreparedCacheEntry(left, right *CacheEntry) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftCopy := *left
	rightCopy := *right
	return samePreparedCacheEntrySource(&leftCopy, &rightCopy) &&
		samePreparedCacheEntryContent(&leftCopy, &rightCopy)
}

func samePreparedCacheEntrySource(left, right *CacheEntry) bool {
	return left.mutationRevision == right.mutationRevision &&
		left.replayRevision == right.replayRevision &&
		left.acceptedRevision == right.acceptedRevision &&
		left.sourceIdentity == right.sourceIdentity &&
		left.sourceDescriptor == right.sourceDescriptor &&
		left.sourceGeneration == right.sourceGeneration && left.fixture == right.fixture &&
		left.URL == right.URL && left.Options == right.Options &&
		sameAuthConfig(left.Auth, right.Auth)
}

func samePreparedCacheEntryContent(left, right *CacheEntry) bool {
	return left.AcceptedContent == right.AcceptedContent &&
		left.AcceptedChecksum == right.AcceptedChecksum && left.AcceptedTime.Equal(right.AcceptedTime) &&
		left.LastAccessTime.Equal(right.LastAccessTime) && left.PendingContent == right.PendingContent &&
		left.PendingChecksum == right.PendingChecksum && left.PendingRevision == right.PendingRevision &&
		left.HasPending == right.HasPending && left.ValidationState == right.ValidationState &&
		left.ValidationStartedAt.Equal(right.ValidationStartedAt) && left.ETag == right.ETag &&
		left.LastModified == right.LastModified
}

func sameAuthConfig(left, right *AuthConfig) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Type != right.Type || left.Username != right.Username || left.Password != right.Password ||
		left.Token != right.Token || len(left.Headers) != len(right.Headers) {
		return false
	}
	for name, value := range left.Headers {
		if right.Headers[name] != value {
			return false
		}
	}
	return true
}

func (c *PreparedInitialCandidateCommit) validatePreparedReplayStateLocked(
	state *AcceptedReplayState,
	snapshots []ContentSnapshot,
	semanticChanges uint64,
) error {
	futureReplay := c.store.replayRevision + Revision(semanticChanges)
	if state == nil || state.ValidateAuthentication() != nil || state.store != c.store ||
		state.watermark != c.watermark || state.replay != futureReplay ||
		state.count != len(snapshots) {
		return errors.New("prepared published HTTP replay state is invalid")
	}
	plannedEntries := make(map[string]*CacheEntry, len(c.sources))
	for index := range c.sources {
		plan := &c.sources[index]
		plannedEntries[plan.source.url] = plan.publishedEntry
	}
	candidates := make(map[SnapshotToken]*InitialCandidate, len(c.commits))
	for index := range c.commits {
		candidates[c.commits[index].Accepted] = c.candidates[index]
	}
	for index := range snapshots {
		if err := c.validatePreparedReplayEntryLocked(state, &snapshots[index], plannedEntries, candidates); err != nil {
			return err
		}
	}
	return nil
}

func (c *PreparedInitialCandidateCommit) validatePreparedReplayEntryLocked(
	state *AcceptedReplayState,
	snapshot *ContentSnapshot,
	plannedEntries map[string]*CacheEntry,
	candidates map[SnapshotToken]*InitialCandidate,
) error {
	entry, found := state.entries.Get([]byte(snapshot.URL))
	if !found || entry.snapshot != *snapshot || entry.proof == nil || entry.proof.validate() != nil {
		return errors.New("prepared published HTTP replay entry is invalid")
	}
	expectedEntry := plannedEntries[snapshot.URL]
	if expectedEntry == nil {
		expectedEntry = c.store.cache[snapshot.URL]
	}
	if candidate := candidates[snapshot.Token]; candidate != nil {
		for candidateIndex := range c.candidates {
			if c.candidates[candidateIndex] == candidate {
				expectedEntry = c.entries[candidateIndex]
				break
			}
		}
	}
	if expectedEntry == nil || entry.proof.entry != expectedEntry {
		return errors.New("prepared published HTTP replay proof has an invalid source")
	}
	return nil
}

func validPreparedPublishedSourceEntry(plan *preparedSourcePlan) bool {
	entry := plan.publishedEntry
	source := plan.source
	return entry != nil && source != nil && entry.URL == source.url &&
		entry.sourceIdentity == source.spec.descriptor.Identity() &&
		entry.sourceDescriptor == source.spec.descriptor && entry.sourceGeneration == plan.generation &&
		entry.Options == source.spec.options && entry.Auth == source.spec.auth &&
		pristinePublishedSourceEntry(entry)
}

func pristinePublishedSourceEntry(entry *CacheEntry) bool {
	return entry.mutationRevision == 0 && entry.replayRevision == 0 && entry.acceptedRevision == 0 &&
		!entry.fixture && entry.AcceptedContent == "" && entry.AcceptedChecksum == "" &&
		entry.AcceptedTime.IsZero() && entry.LastAccessTime.IsZero() && entry.PendingContent == "" &&
		entry.PendingChecksum == "" && entry.PendingRevision == 0 && !entry.HasPending &&
		entry.ValidationState == StateAccepted && entry.ValidationStartedAt.IsZero() &&
		entry.ETag == "" && entry.LastModified == ""
}

// PrepareInitialCandidatesAndVerifyObservations validates a commit without publishing it.
func (s *HTTPStore) PrepareInitialCandidatesAndVerifyObservations(
	ctx context.Context,
	candidates []*InitialCandidate,
	observations []ObservationToken,
) (*PreparedInitialCandidateCommit, error) {
	return s.prepareInitialCandidates(ctx, nil, candidates, nil, observations, observations, nil, nil, nil)
}

// PrepareStagedSourcesAndVerifyObservations validates source and content publication.
func (s *HTTPStore) PrepareStagedSourcesAndVerifyObservations(
	ctx context.Context,
	sources []*StagedSource,
	candidates []*InitialCandidate,
	observations []ObservationToken,
) (*PreparedInitialCandidateCommit, error) {
	return s.prepareInitialCandidates(ctx, sources, candidates, nil, observations, observations, nil, nil, nil)
}

// PrepareStagedSourcesAndVerifyObservationSets permits verification-only reads
// to be rebased while retained reads must survive the planned publication.
func (s *HTTPStore) PrepareStagedSourcesAndVerifyObservationSets(
	ctx context.Context,
	sources []*StagedSource,
	candidates []*InitialCandidate,
	verificationOnly []ObservationToken,
	retained []ObservationToken,
) (*PreparedInitialCandidateCommit, error) {
	observations := make([]ObservationToken, 0, len(verificationOnly)+len(retained))
	observations = append(observations, verificationOnly...)
	observations = append(observations, retained...)
	return s.prepareInitialCandidates(ctx, sources, candidates, nil, observations, retained, nil, nil, nil)
}

// PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases also prepares
// one render cache's exact lease transition and relevant-change acknowledgment.
func (s *HTTPStore) PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
	ctx context.Context,
	sources []*StagedSource,
	candidates []*InitialCandidate,
	verificationOnly []ObservationToken,
	retained []ObservationToken,
	active *ActiveLeaseCommit,
) (*PreparedInitialCandidateCommit, error) {
	return s.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayEpoch(
		ctx, sources, candidates, verificationOnly, retained, active, nil,
	)
}

// PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayState
// retains either a complete root fence or a selective accepted-input fence.
func (s *HTTPStore) PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayState(
	ctx context.Context,
	sources []*StagedSource,
	candidates []*InitialCandidate,
	verificationOnly []ObservationToken,
	retained []ObservationToken,
	active *ActiveLeaseCommit,
	replayEpoch *ReplayEpoch,
	replayState *AcceptedReplayState,
) (*PreparedInitialCandidateCommit, error) {
	observations := make([]ObservationToken, 0, len(verificationOnly)+len(retained))
	observations = append(observations, verificationOnly...)
	observations = append(observations, retained...)
	return s.prepareInitialCandidates(
		ctx, sources, candidates, nil, observations, retained, active, replayEpoch, replayState,
	)
}

// PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayEpoch
// also retains one complete HTTP render-root fence.
func (s *HTTPStore) PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayEpoch(
	ctx context.Context,
	sources []*StagedSource,
	candidates []*InitialCandidate,
	verificationOnly []ObservationToken,
	retained []ObservationToken,
	active *ActiveLeaseCommit,
	replay *ReplayEpoch,
) (*PreparedInitialCandidateCommit, error) {
	observations := make([]ObservationToken, 0, len(verificationOnly)+len(retained))
	observations = append(observations, verificationOnly...)
	observations = append(observations, retained...)
	return s.prepareInitialCandidates(ctx, sources, candidates, nil, observations, retained, active, replay, nil)
}

func (s *HTTPStore) prepareInitialCandidates(
	ctx context.Context,
	sources []*StagedSource,
	candidates []*InitialCandidate,
	accepted []SnapshotToken,
	observations []ObservationToken,
	retained []ObservationToken,
	active *ActiveLeaseCommit,
	replayEpoch *ReplayEpoch,
	replayState *AcceptedReplayState,
) (*PreparedInitialCandidateCommit, error) {
	if s == nil || s.publicationPoisoned.Load() {
		return nil, errHTTPStorePublicationPoisoned
	}
	authority := s.prepareAuthority
	if authority == nil {
		return nil, errHTTPStorePublicationPoisoned
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("preparing initial HTTP candidates: %w", context.Cause(ctx))
	case <-authority:
	}

	s.mu.Lock()
	commit, err := s.prepareInitialCandidatesLocked(ctx, authority, &initialCandidatePrepareInput{
		sources: sources, candidates: candidates, accepted: accepted,
		observations: observations, retained: retained,
		active: active, replayEpoch: replayEpoch, replayState: replayState,
	})
	if err != nil {
		s.mu.Unlock()
		returnPreparedHTTPAuthority(authority)
		return nil, err
	}
	return commit, nil
}

type initialCandidatePrepareInput struct {
	sources      []*StagedSource
	candidates   []*InitialCandidate
	accepted     []SnapshotToken
	observations []ObservationToken
	retained     []ObservationToken
	active       *ActiveLeaseCommit
	replayEpoch  *ReplayEpoch
	replayState  *AcceptedReplayState
}

func (s *HTTPStore) prepareInitialCandidatesLocked(
	ctx context.Context,
	authority chan struct{},
	input *initialCandidatePrepareInput,
) (*PreparedInitialCandidateCommit, error) {
	if err := s.publicationErrorLocked(); err != nil || s.prepareAuthority != authority {
		return nil, errHTTPStorePublicationPoisoned
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, fmt.Errorf("preparing initial HTTP candidates: %w", cause)
	}
	if err := s.validatePrepareReplayFencesLocked(input.replayEpoch, input.replayState); err != nil {
		return nil, err
	}
	advancedReplay := input.replayState
	if input.replayState != nil {
		var ok bool
		advancedReplay, ok = s.advanceAcceptedReplayStateLocked(input.replayState)
		if !ok {
			return nil, errors.New("accepted HTTP replay inputs changed while the render was running")
		}
	}
	if !s.verifySnapshotsLocked(input.accepted) || !s.verifyObservationsLocked(input.observations) {
		return nil, errors.New("accepted HTTP content changed while the render was running")
	}
	plans, sourceByURL, err := s.planStagedSourcesLocked(input.sources)
	if err != nil {
		return nil, err
	}
	if err := s.validateInitialCandidatesLocked(input.candidates, sourceByURL); err != nil {
		return nil, err
	}
	var activePlan *preparedActiveLeasePlan
	if input.active != nil {
		activePlan, err = s.planActiveLeaseCommitLocked(input.active)
		if err != nil {
			return nil, err
		}
	}
	if err := validateActiveLeasesSurvivePublication(activePlan, plans, input.candidates); err != nil {
		return nil, err
	}
	if !acceptedReplayStateSurvivesPublication(advancedReplay, plans, input.candidates) {
		return nil, errors.New("prepared HTTP publication invalidates accepted replay inputs")
	}
	changedSources := countChangedSources(plans)
	semanticChanges := changedSources + uint64(len(input.candidates))
	if semanticChanges > ^uint64(0)-uint64(s.semanticRevision) {
		return nil, errors.New("HTTP store semantic revision exhausted")
	}
	if changedSources > ^uint64(0)-s.nextSourceGeneration {
		return nil, errors.New("HTTP store source generation exhausted")
	}
	if !s.observationsSurvivePublicationLocked(input.retained, plans, input.candidates, semanticChanges) {
		return nil, errors.New("prepared HTTP publication invalidates a render observation")
	}
	s.materializePreparedSourceEntriesLocked(plans)
	commits, entries, err := s.buildCandidateCommitsLocked(input.candidates, plans, changedSources)
	if err != nil {
		return nil, err
	}
	return &PreparedInitialCandidateCommit{
		store:      s,
		authority:  authority,
		sources:    plans,
		candidates: append([]*InitialCandidate(nil), input.candidates...),
		entries:    entries,
		commits:    commits,
		watermark:  s.semanticRevision + Revision(semanticChanges),
		active:     activePlan,
	}, nil
}

func (s *HTTPStore) validatePrepareReplayFencesLocked(
	replayEpoch *ReplayEpoch,
	replayState *AcceptedReplayState,
) error {
	if replayEpoch != nil && replayState != nil {
		return errors.New("HTTP render commit has conflicting replay fences")
	}
	if replayEpoch != nil && !s.replayEpochCurrentLocked(replayEpoch) {
		return errors.New("HTTP render root changed while the render was running")
	}
	return nil
}

func countChangedSources(plans []preparedSourcePlan) uint64 {
	changed := uint64(0)
	for index := range plans {
		if plans[index].source.Changed() {
			changed++
		}
	}
	return changed
}

func (s *HTTPStore) materializePreparedSourceEntriesLocked(plans []preparedSourcePlan) {
	nextGeneration := s.nextSourceGeneration
	for index := range plans {
		plan := &plans[index]
		if plan.source.Changed() {
			nextGeneration++
			plan.generation = nextGeneration
			plan.publishedEntry = preparePublishedSourceEntry(plan.source, nextGeneration)
		} else {
			plan.generation = plan.source.baseGeneration
			entry := *s.cache[plan.source.url]
			plan.publishedEntry = &entry
		}
	}
}

func (s *HTTPStore) buildCandidateCommitsLocked(
	candidates []*InitialCandidate,
	plans []preparedSourcePlan,
	changedSources uint64,
) (commits []CandidateCommit, entries []*CacheEntry, err error) {
	commits = make([]CandidateCommit, len(candidates))
	entries = make([]*CacheEntry, len(candidates))
	planBySource := make(map[*StagedSource]*preparedSourcePlan, len(plans))
	for index := range plans {
		planBySource[plans[index].source] = &plans[index]
	}
	for index, candidate := range candidates {
		accepted := SnapshotToken{
			source:     s.revisionSource,
			url:        candidate.url,
			descriptor: candidate.sourceDescriptor,
			kind:       SnapshotAccepted,
			revision:   s.semanticRevision + Revision(changedSources+uint64(index)) + 1,
		}
		commits[index] = CandidateCommit{Candidate: candidate.token, Accepted: accepted}
		entry := candidate.entry
		if candidate.source != nil {
			plan := planBySource[candidate.source]
			if plan == nil {
				return nil, nil, errors.New("initial HTTP candidate has no prepared source entry")
			}
			entry = plan.publishedEntry
		} else {
			detached := *entry
			entry = &detached
		}
		entries[index] = entry
	}
	return commits, entries, nil
}

func preparePublishedSourceEntry(source *StagedSource, generation uint64) *CacheEntry {
	return &CacheEntry{
		URL:              source.url,
		ValidationState:  StateAccepted,
		Options:          source.spec.options,
		Auth:             source.spec.auth,
		sourceIdentity:   source.spec.descriptor.Identity(),
		sourceDescriptor: source.spec.descriptor,
		sourceGeneration: generation,
	}
}

func acceptedReplayStateSurvivesPublication(
	state *AcceptedReplayState,
	plans []preparedSourcePlan,
	candidates []*InitialCandidate,
) bool {
	if state == nil {
		return true
	}
	for index := range plans {
		if plans[index].source.Changed() {
			if _, found := state.entries.Get([]byte(plans[index].source.url)); found {
				return false
			}
		}
	}
	for _, candidate := range candidates {
		if _, found := state.entries.Get([]byte(candidate.url)); found {
			return false
		}
	}
	return true
}

func validateActiveLeasesSurvivePublication(
	active *preparedActiveLeasePlan,
	plans []preparedSourcePlan,
	candidates []*InitialCandidate,
) error {
	for index := range plans {
		source := plans[index].source
		if source.Changed() && activeLeasePlanReferencesTransition(
			active, source.url, source.baseDescriptor, source.spec.descriptor,
		) {
			return fmt.Errorf("leased HTTP source %s changes during cache publication", source.url)
		}
	}
	for _, candidate := range candidates {
		if activeLeasePlanReferencesTransition(
			active, candidate.url, candidate.sourceDescriptor, candidate.sourceDescriptor,
		) {
			return fmt.Errorf("leased HTTP content %s changes during cache publication", candidate.url)
		}
	}
	return nil
}

type plannedObservationChange struct {
	previous SourceDescriptor
	next     SourceDescriptor
}

func (s *HTTPStore) observationsSurvivePublicationLocked(
	observations []ObservationToken,
	plans []preparedSourcePlan,
	candidates []*InitialCandidate,
	semanticChanges uint64,
) bool {
	changesByURL := make(map[string][]plannedObservationChange, len(plans)+len(candidates))
	for index := range plans {
		source := plans[index].source
		if !source.Changed() {
			continue
		}
		changesByURL[source.url] = append(changesByURL[source.url], plannedObservationChange{
			previous: source.baseDescriptor,
			next:     source.spec.descriptor,
		})
	}
	for _, candidate := range candidates {
		changesByURL[candidate.url] = append(changesByURL[candidate.url], plannedObservationChange{
			previous: candidate.sourceDescriptor,
			next:     candidate.sourceDescriptor,
		})
	}
	for index := range observations {
		observation := &observations[index]
		changes := changesByURL[observation.url]
		if observation.found {
			if len(changes) != 0 {
				return false
			}
			continue
		}
		if !s.negativeObservationHistorySurvivesLocked(observation.watermark, semanticChanges) {
			return false
		}
		for _, change := range changes {
			if change.previous == observation.descriptor || change.next == observation.descriptor {
				return false
			}
		}
	}
	return true
}

func (s *HTTPStore) negativeObservationHistorySurvivesLocked(
	watermark Revision,
	semanticChanges uint64,
) bool {
	if semanticChanges == 0 {
		return true
	}
	if s.semanticJournalCapacity <= 0 {
		return false
	}
	capacity := uint64(s.semanticJournalCapacity)
	futureLength := uint64(len(s.semanticJournal)) + semanticChanges
	if futureLength > capacity {
		futureLength = capacity
	}
	if futureLength == 0 {
		return false
	}
	futureCurrent := s.semanticRevision + Revision(semanticChanges)
	futureOldest := futureCurrent - Revision(futureLength) + 1
	return watermark >= futureOldest-1
}

// Planned returns the exact tokens Publish will create.
func (c *PreparedInitialCandidateCommit) Planned() ([]CandidateCommit, Revision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitReleased {
		return nil, 0
	}
	return append([]CandidateCommit(nil), c.commits...), c.watermark
}

// PlannedActiveLeases returns the exact token and URL transitions Publish creates.
func (c *PreparedInitialCandidateCommit) PlannedActiveLeases() (
	ActiveLeaseToken,
	ActiveLeaseTransition,
	bool,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitReleased || c.active == nil {
		return ActiveLeaseToken{}, ActiveLeaseTransition{}, false
	}
	return c.active.token, ActiveLeaseTransition{
		Activated: slices.Clone(c.active.transition.Activated),
		Retired:   slices.Clone(c.active.transition.Retired),
	}, true
}

// PreparePublishedReplayActiveLeases binds the post-publication accepted inputs to a lease root.
func (c *PreparedInitialCandidateCommit) PreparePublishedReplayActiveLeases(
	active *ActiveLeaseCommit,
	snapshots []ContentSnapshot,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != preparedCommitReady || c.store == nil || c.active != nil {
		return errors.New("published HTTP replay lease cannot be prepared")
	}
	validated, err := c.validatePublishedReplaySnapshotsLocked(snapshots)
	if err != nil {
		return err
	}
	plan, err := c.store.planPublishedReplayActiveLeaseLocked(active, validated)
	if err != nil {
		return err
	}
	plan.replay, err = c.preparePublishedReplayStateLocked(validated)
	if err != nil {
		return err
	}
	c.active = plan
	return nil
}

func (c *PreparedInitialCandidateCommit) validatePublishedReplaySnapshotsLocked(
	snapshots []ContentSnapshot,
) ([]ContentSnapshot, error) {
	committed := make(map[SnapshotToken]*InitialCandidate, len(c.commits))
	for index := range c.commits {
		committed[c.commits[index].Accepted] = c.candidates[index]
	}
	seenCandidates := make(map[SnapshotToken]struct{}, len(committed))
	seenURLs := make(map[string]struct{}, len(snapshots))
	validated := slices.Clone(snapshots)
	for index := range validated {
		snapshot := &validated[index]
		if _, exists := seenURLs[snapshot.URL]; exists {
			return nil, fmt.Errorf("published HTTP replay lease duplicates source %s", snapshot.URL)
		}
		seenURLs[snapshot.URL] = struct{}{}
		if candidate := committed[snapshot.Token]; candidate != nil {
			expected := ContentSnapshot{
				URL: candidate.url, Descriptor: candidate.sourceDescriptor, Content: candidate.content,
				Found: true, Cacheable: true, Token: snapshot.Token,
				StoreSource: c.store.revisionSource, Observation: snapshot.Token.Revision(),
				Watermark: c.watermark,
			}
			if *snapshot != expected {
				return nil, fmt.Errorf("published HTTP replay lease has an invalid candidate snapshot for %s", snapshot.URL)
			}
			seenCandidates[snapshot.Token] = struct{}{}
			continue
		}
		if !c.store.verifySnapshotLocked(&snapshot.Token) {
			return nil, fmt.Errorf("published HTTP replay lease has a stale snapshot for %s", snapshot.URL)
		}
		entry := c.store.cache[snapshot.URL]
		if entry == nil {
			return nil, fmt.Errorf("published HTTP replay lease has no source %s", snapshot.URL)
		}
		expected := c.store.acceptedSnapshotLocked(entry, c.watermark)
		if *snapshot != expected {
			return nil, fmt.Errorf("published HTTP replay lease has an invalid snapshot for %s", snapshot.URL)
		}
	}
	if len(seenCandidates) != len(committed) {
		return nil, errors.New("published HTTP replay lease omits a committed candidate")
	}
	return validated, nil
}

func (c *PreparedInitialCandidateCommit) preparePublishedReplayStateLocked(
	snapshots []ContentSnapshot,
) (*AcceptedReplayState, error) {
	changedSources := uint64(0)
	for index := range c.sources {
		plan := &c.sources[index]
		if plan.source.Changed() {
			changedSources++
		}
	}
	type plannedCandidate struct {
		candidate *InitialCandidate
		entry     *CacheEntry
	}
	committed := make(map[SnapshotToken]plannedCandidate, len(c.commits))
	for index := range c.commits {
		candidate := c.candidates[index]
		entry := c.entries[index]
		if entry == nil {
			return nil, fmt.Errorf("published HTTP replay lease has no planned source %s", candidate.url)
		}
		committed[c.commits[index].Accepted] = plannedCandidate{candidate: candidate, entry: entry}
	}
	plannedEntries := make(map[string]*CacheEntry, len(c.sources))
	for index := range c.sources {
		plan := &c.sources[index]
		plannedEntries[plan.source.url] = plan.publishedEntry
	}
	txn := iradix.New[acceptedReplayStateEntry]().Txn()
	for index := range snapshots {
		snapshot := snapshots[index]
		var proof *AcceptedReplayProof
		if candidate, planned := committed[snapshot.Token]; planned {
			if snapshot.URL != candidate.candidate.url {
				return nil, errors.New("published HTTP replay candidate has an invalid source")
			}
			proof = sealAcceptedReplayProof(c.store, candidate.entry, &snapshot)
		} else if entry := plannedEntries[snapshot.URL]; entry != nil {
			proof = sealAcceptedReplayProof(c.store, entry, &snapshot)
		} else {
			var ok bool
			proof, ok = c.store.captureAcceptedReplayProofLocked(&snapshot)
			if !ok {
				return nil, fmt.Errorf("published HTTP replay lease cannot authenticate source %s", snapshot.URL)
			}
		}
		if _, replaced := txn.Insert([]byte(snapshot.URL), acceptedReplayStateEntry{
			snapshot: snapshot,
			proof:    proof,
		}); replaced {
			return nil, fmt.Errorf("published HTTP replay lease duplicates source %s", snapshot.URL)
		}
	}
	semanticChanges := changedSources + uint64(len(c.candidates))
	replay := c.store.replayRevision + Revision(semanticChanges)
	return sealAcceptedReplayState(
		c.store,
		txn.Commit(),
		sealReplayEpoch(c.store, replay),
		c.watermark,
		replay,
	), nil
}

// Publish accepts the prepared candidates without a fallible step.
func (c *PreparedInitialCandidateCommit) Publish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitReady {
		if err := c.sealPublicationLocked(); err != nil {
			panic(fmt.Sprintf("prepared HTTP publication failed authentication: %v", err))
		}
	}
	if c.state == preparedCommitSealed {
		c.publishSealedLocked()
	}
}

// PublishSealed completes a publication returned successfully from SealPublication.
func (c *PreparedInitialCandidateCommit) PublishSealed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitPublished {
		return
	}
	if c.state != preparedCommitSealed {
		panic("prepared HTTP store publication is not sealed")
	}
	c.publishSealedLocked()
}

func (c *PreparedInitialCandidateCommit) publishSealedLocked() {
	if err := c.publication.validate(c, c.store); err != nil {
		panic(fmt.Sprintf("prepared HTTP replacement state failed authentication: %v", err))
	}
	c.publication.publish()
	c.state = preparedCommitPublished
}

// ValidatePublishedPublication authenticates tentative live state without releasing authority.
func (c *PreparedInitialCandidateCommit) ValidatePublishedPublication() error {
	if c == nil {
		return errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validatePublishedPublicationLocked()
}

func (c *PreparedInitialCandidateCommit) validatePublishedPublicationLocked() error {
	if c.state != preparedCommitPublished || c.store == nil || c.publication == nil {
		return errors.New("prepared HTTP publication is not published")
	}
	if err := c.rollback.validate(c.store, c.authority); err != nil {
		return err
	}
	return c.publication.validatePublished(c, c.store)
}

// CommitPublishedPublication records a rollback-capable publication decision.
func (c *PreparedInitialCandidateCommit) CommitPublishedPublication() error {
	if c == nil {
		return errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitCommitted {
		return nil
	}
	if err := c.validatePublishedPublicationLocked(); err != nil {
		return err
	}
	c.state = preparedCommitCommitted
	return nil
}

// ReleaseCommittedPublication exposes committed state and releases retained authority.
func (c *PreparedInitialCandidateCommit) ReleaseCommittedPublication() {
	if c == nil {
		panic("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitReleased {
		return
	}
	if c.state != preparedCommitCommitted {
		panic("prepared HTTP publication is not committed")
	}
	c.releaseCommittedLocked()
}

// PlannedSourceState returns one source from the authenticated sealed replacement.
func (c *PreparedInitialCandidateCommit) PlannedSourceState(url string) (SourceState, bool, error) {
	if c == nil {
		return SourceState{}, false, errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != preparedCommitSealed || c.publication == nil {
		return SourceState{}, false, errors.New("prepared HTTP publication is not sealed")
	}
	if err := c.publication.validate(c, c.store); err != nil {
		return SourceState{}, false, err
	}
	entry, found := c.publication.cache.entries[url]
	if !found {
		return SourceState{}, false, nil
	}
	return sourceState(entry), true, nil
}

// PlannedHasActiveLease reports whether the sealed replacement leases one URL.
func (c *PreparedInitialCandidateCommit) PlannedHasActiveLease(url string) (bool, error) {
	if c == nil {
		return false, errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != preparedCommitSealed || c.publication == nil {
		return false, errors.New("prepared HTTP publication is not sealed")
	}
	if err := c.publication.validate(c, c.store); err != nil {
		return false, err
	}
	return len(c.publication.active.urls[url]) != 0, nil
}

// PlannedPendingOverlay freezes pending inputs from the authenticated sealed replacement.
func (c *PreparedInitialCandidateCommit) PlannedPendingOverlay() (*HTTPOverlay, error) {
	if c == nil {
		return nil, errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != preparedCommitSealed || c.publication == nil {
		return nil, errors.New("prepared HTTP publication is not sealed")
	}
	if err := c.publication.validate(c, c.store); err != nil {
		return nil, err
	}
	return newHTTPOverlayFromState(
		c.publication.cache.entries,
		c.store.revisionSource,
		c.publication.semanticRevision,
		true,
	), nil
}

// PlannedActiveReplayState returns the authenticated selective replay state before publication.
func (c *PreparedInitialCandidateCommit) PlannedActiveReplayState() (
	*AcceptedReplayState,
	bool,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if (c.state != preparedCommitReady && c.state != preparedCommitSealed) || c.store == nil ||
		c.active == nil || c.active.replay == nil || c.active.replay.ValidateAuthentication() != nil ||
		c.active.replay.store != c.store {
		return nil, false
	}
	return c.active.replay, true
}

// Release makes the published store state visible and releases commit authority.
func (c *PreparedInitialCandidateCommit) Release() {
	err := c.ReleasePublication()
	if err != nil {
		panic(fmt.Sprintf("prepared HTTP publication failed final authentication: %v", err))
	}
}

// ReleasePublication makes the published store state visible and reports authentication failure.
func (c *PreparedInitialCandidateCommit) ReleasePublication() error {
	if c == nil {
		return errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == preparedCommitReleased {
		return nil
	}
	if c.state == preparedCommitPublished {
		if err := c.validatePublishedPublicationLocked(); err != nil {
			return errors.Join(err, c.abortLocked())
		}
		c.state = preparedCommitCommitted
	}
	if c.state == preparedCommitCommitted {
		c.releaseCommittedLocked()
		return nil
	}
	return c.abortLocked()
}

func (c *PreparedInitialCandidateCommit) releaseCommittedLocked() {
	c.state = preparedCommitReleased
	authority := c.authority
	c.store.mu.Unlock()
	returnPreparedHTTPAuthority(authority)
}

func (c *PreparedInitialCandidateCommit) abortLocked() error {
	if c.state == preparedCommitReleased {
		return nil
	}
	var publicationErr error
	rollbackFailed := false
	if c.state == preparedCommitPublished || c.state == preparedCommitCommitted {
		if c.publication == nil {
			publicationErr = errors.New("published HTTP replacement state is missing")
		} else {
			publicationErr = c.publication.validatePublished(c, c.store)
		}
		rollbackErr := c.rollback.restore(c.store, c.authority)
		publicationErr = errors.Join(publicationErr, rollbackErr)
		rollbackFailed = rollbackErr != nil
	}
	c.state = preparedCommitReleased
	authority := c.authority
	if !rollbackFailed && authority != nil && cap(authority) == 1 {
		if c.store.prepareAuthority != authority {
			c.store.prepareAuthority = authority
		}
	} else if rollbackFailed {
		c.store.quarantinePublicationLocked()
	}
	c.store.mu.Unlock()
	if !rollbackFailed {
		returnPreparedHTTPAuthority(authority)
	}
	return publicationErr
}

func returnPreparedHTTPAuthority(authority chan struct{}) {
	defer func() { _ = recover() }()
	if authority == nil || cap(authority) != 1 {
		return
	}
	select {
	case authority <- struct{}{}:
	default:
	}
}

// Abort discards an unpublished commit and releases commit authority.
func (c *PreparedInitialCandidateCommit) Abort() {
	err := c.AbortPublication()
	if err != nil {
		panic(fmt.Sprintf("prepared HTTP publication failed rollback authentication: %v", err))
	}
}

// AbortPublication rolls back a tentative publication and reports authentication failure.
func (c *PreparedInitialCandidateCommit) AbortPublication() error {
	if c == nil {
		return errors.New("prepared HTTP publication is missing")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.abortLocked()
}
