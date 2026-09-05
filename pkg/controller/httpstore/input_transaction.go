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
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

type transactionState uint8

const (
	transactionOpen transactionState = iota
	transactionPrepared
	transactionCommitted
	transactionAborted
)

var errInputTransactionAborted = errors.New("render input transaction was aborted")

// InputTransaction owns authoritative HTTP candidates for one render.
type InputTransaction struct {
	component          *Component
	fetchGroup         singleflight.Group
	mu                 sync.Mutex
	state              transactionState
	sources            map[string]*purehttpstore.StagedSource
	results            map[string]*inputFetchResult
	candidates         map[string]*purehttpstore.InitialCandidate
	committedSnapshots []purehttpstore.ContentSnapshot
	committedReplay    *purehttpstore.AcceptedReplayState
	cacheable          bool
	prepared           *PreparedInputCommit
	retrySeed          *InputRetrySeed
	replayEpoch        *purehttpstore.ReplayEpoch
	replayState        *purehttpstore.AcceptedReplayState
}

type inputFetchResult struct {
	snapshot purehttpstore.ContentSnapshot
	err      error
}

func newInputTransaction(component *Component, seeds ...*InputRetrySeed) *InputTransaction {
	transaction := &InputTransaction{
		component:  component,
		sources:    make(map[string]*purehttpstore.StagedSource),
		results:    make(map[string]*inputFetchResult),
		candidates: make(map[string]*purehttpstore.InitialCandidate),
	}
	if len(seeds) > 0 {
		transaction.retrySeed = seeds[0]
	}
	return transaction
}

func (t *InputTransaction) fetch(
	ctx context.Context,
	url string,
	opts purehttpstore.FetchOptions,
	auth *purehttpstore.AuthConfig,
) (purehttpstore.ContentSnapshot, error) {
	descriptor, err := purehttpstore.DescribeSource(opts, auth)
	if err != nil {
		return purehttpstore.ContentSnapshot{}, err
	}
	if result, adopted, adoptErr := t.adoptRetryInput(url, descriptor); adopted || adoptErr != nil {
		if adoptErr != nil {
			return purehttpstore.ContentSnapshot{}, adoptErr
		}
		return result.snapshot, result.err
	}
	source, err := t.component.stageSource(url, opts, auth)
	if err != nil {
		return purehttpstore.ContentSnapshot{}, err
	}
	source, err = t.enrollSource(source)
	if err != nil {
		return purehttpstore.ContentSnapshot{}, err
	}
	if result, resultErr := t.cachedResult(url); result != nil || resultErr != nil {
		if resultErr != nil {
			return purehttpstore.ContentSnapshot{}, resultErr
		}
		if !t.component.store.VerifyStagedSource(source) {
			return purehttpstore.ContentSnapshot{}, fmt.Errorf("HTTP source %s changed within one render", url)
		}
		return result.snapshot, result.err
	}

	value, err, _ := t.fetchGroup.Do(url, func() (any, error) {
		return t.fetchAndRecord(ctx, source)
	})
	if err != nil {
		return purehttpstore.ContentSnapshot{}, err
	}
	result, ok := value.(*inputFetchResult)
	if !ok {
		return purehttpstore.ContentSnapshot{}, errors.New("HTTP candidate fetch returned an invalid result")
	}
	return result.snapshot, result.err
}

func (t *InputTransaction) fetchAndRecord(
	ctx context.Context,
	source *purehttpstore.StagedSource,
) (*inputFetchResult, error) {
	url := source.URL()
	if result, err := t.cachedResult(url); result != nil || err != nil {
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	snapshot, candidate, fetchErr := t.component.store.PrepareStagedSnapshot(ctx, source)
	if snapshot.URL == "" {
		snapshot.URL = url
		snapshot.Descriptor = source.Descriptor()
	}
	result := &inputFetchResult{snapshot: snapshot, err: fetchErr}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return nil, errors.New("render input transaction is no longer open")
	}
	if previous, exists := t.results[url]; exists {
		return previous, nil
	}
	if candidate != nil {
		if previous, exists := t.candidates[url]; exists && previous != candidate {
			return nil, fmt.Errorf("HTTP source %s changed within one render", url)
		}
		t.candidates[url] = candidate
	}
	t.results[url] = result
	return result, nil
}

func (t *InputTransaction) replay(
	snapshot *purehttpstore.ContentSnapshot,
	source *purehttpstore.StagedSource,
) error {
	if snapshot == nil || !snapshot.Cacheable || snapshot.Token.Kind() != purehttpstore.SnapshotAccepted ||
		snapshot.URL != snapshot.Token.URL() || snapshot.Descriptor != snapshot.Token.SourceDescriptor() {
		return errors.New("HTTP replay requires an exact accepted snapshot")
	}
	if source == nil || source.URL() != snapshot.URL || source.Descriptor() != snapshot.Descriptor {
		return errors.New("HTTP replay source does not match its snapshot")
	}
	if _, err := t.enrollSource(source); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return errors.New("render input transaction is no longer open")
	}
	if previous, exists := t.results[snapshot.URL]; exists {
		if previous.err != nil || !sameObservedHTTPSnapshot(&previous.snapshot, snapshot) {
			return fmt.Errorf("HTTP source %s changed within one render", snapshot.URL)
		}
		return nil
	}
	t.results[snapshot.URL] = &inputFetchResult{snapshot: *snapshot}
	return nil
}

func (t *InputTransaction) requireAcceptedReplayState(
	state *purehttpstore.AcceptedReplayState,
) error {
	if state == nil || state.ValidateAuthentication() != nil ||
		state.Source() != t.component.RevisionSource() {
		return errors.New("accepted HTTP replay state has invalid provenance")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return errors.New("render input transaction is no longer open")
	}
	if t.replayEpoch != nil {
		return errors.New("render input transaction has conflicting HTTP replay fences")
	}
	if t.replayState != nil && t.replayState != state {
		return errors.New("render input transaction has conflicting accepted HTTP replay states")
	}
	t.replayState = state
	return nil
}

func (t *InputTransaction) observedSnapshot(
	expected *purehttpstore.ContentSnapshot,
) (purehttpstore.ContentSnapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return purehttpstore.ContentSnapshot{}, false
	}
	result := t.results[expected.URL]
	if result == nil || result.err != nil || !sameObservedHTTPSnapshot(&result.snapshot, expected) {
		return purehttpstore.ContentSnapshot{}, false
	}
	return result.snapshot, true
}

func (t *InputTransaction) enrollSource(
	source *purehttpstore.StagedSource,
) (*purehttpstore.StagedSource, error) {
	t.mu.Lock()
	if t.state != transactionOpen {
		t.mu.Unlock()
		return nil, errors.New("render input transaction is no longer open")
	}
	selected := source
	if previous, exists := t.sources[source.URL()]; exists {
		if previous.Descriptor() != source.Descriptor() {
			t.mu.Unlock()
			return nil, fmt.Errorf("HTTP source %s changed within one render", source.URL())
		}
		selected = previous
	} else {
		t.sources[source.URL()] = source
	}
	t.mu.Unlock()
	if !t.component.store.VerifyStagedSource(selected) {
		return nil, fmt.Errorf("HTTP source %s changed within one render", source.URL())
	}
	return selected, nil
}

func (t *InputTransaction) cachedResult(key string) (*inputFetchResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return nil, errors.New("render input transaction is no longer open")
	}
	return t.results[key], nil
}

// HasCandidates reports whether this render accepted HTTP content no previous
// render had. Only such a render needs the synchronous check before its commit:
// content already in the store was checked when it was first accepted.
func (t *InputTransaction) HasCandidates() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.candidates) > 0
}

func (t *InputTransaction) ProvisionalURLs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	urls := make([]string, 0, len(t.results))
	seen := make(map[string]struct{}, len(t.results))
	for _, result := range t.results {
		snapshot := &result.snapshot
		accepted := snapshot.Cacheable && snapshot.Token.Valid() &&
			snapshot.Token.Kind() == purehttpstore.SnapshotAccepted
		if accepted || snapshot.URL == "" {
			continue
		}
		if _, exists := seen[snapshot.URL]; exists {
			continue
		}
		seen[snapshot.URL] = struct{}{}
		urls = append(urls, snapshot.URL)
	}
	slices.Sort(urls)
	return urls
}

// Cacheable reports whether every HTTP read produced an exact content version.
func (t *InputTransaction) Cacheable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.state {
	case transactionCommitted:
		return t.cacheable
	case transactionAborted:
		return false
	}
	return t.cacheableLocked()
}

func (t *InputTransaction) cacheableLocked() bool {
	for _, result := range t.results {
		if result.err != nil || !result.snapshot.Cacheable {
			return false
		}
	}
	return true
}

// Snapshots returns the exact versions read by this render.
func (t *InputTransaction) Snapshots() []purehttpstore.ContentSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == transactionCommitted {
		return slices.Clone(t.committedSnapshots)
	}
	return t.snapshotsLocked()
}

func (t *InputTransaction) committedAcceptedReplayState() (*purehttpstore.AcceptedReplayState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionCommitted {
		return nil, false
	}
	if t.committedReplay == nil || t.committedReplay.ValidateAuthentication() != nil {
		return nil, true
	}
	return t.committedReplay, true
}

func (t *InputTransaction) snapshotsLocked() []purehttpstore.ContentSnapshot {
	snapshots := make([]purehttpstore.ContentSnapshot, 0, len(t.results))
	for _, result := range t.results {
		snapshots = append(snapshots, result.snapshot)
	}
	slices.SortFunc(snapshots, func(a, b purehttpstore.ContentSnapshot) int {
		return strings.Compare(a.URL, b.URL)
	})
	return snapshots
}

// PrepareCommit validates every input and retains publication authority.
func (t *InputTransaction) PrepareCommit(ctx context.Context) (*PreparedInputCommit, error) {
	return t.PrepareCommitWithObservations(ctx, nil)
}

// PrepareCommitWithObservations also validates exact inputs held by a render graph.
func (t *InputTransaction) PrepareCommitWithObservations(
	ctx context.Context,
	additional []purehttpstore.ObservationToken,
) (*PreparedInputCommit, error) {
	return t.PrepareCommitWithObservationsAndActiveLeases(ctx, additional, nil)
}

// PrepareCommitWithObservationsAndActiveLeases also prepares one render
// cache's exact persistent HTTP dependency transition.
func (t *InputTransaction) PrepareCommitWithObservationsAndActiveLeases(
	ctx context.Context,
	additional []purehttpstore.ObservationToken,
	active *purehttpstore.ActiveLeaseCommit,
) (*PreparedInputCommit, error) {
	return t.prepareCommitWithObservationsAndActiveLeases(ctx, additional, active, true)
}

// PrepareCommitPreservingRefreshers publishes inputs without changing timer ownership.
func (t *InputTransaction) PrepareCommitPreservingRefreshers(
	ctx context.Context,
	additional []purehttpstore.ObservationToken,
) (*PreparedInputCommit, error) {
	return t.prepareCommitWithObservationsAndActiveLeases(ctx, additional, nil, false)
}

func (t *InputTransaction) prepareCommitWithObservationsAndActiveLeases(
	ctx context.Context,
	additional []purehttpstore.ObservationToken,
	active *purehttpstore.ActiveLeaseCommit,
	refreshers bool,
) (*PreparedInputCommit, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	prepared, finished, err := t.preparedStateLocked()
	if finished {
		return prepared, err
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, fmt.Errorf("preparing render inputs: %w", cause)
	}
	if len(t.sources) == 0 && len(t.results) == 0 && len(additional) == 0 && active == nil &&
		t.replayEpoch == nil && t.replayState == nil {
		plan := newPreparedInputPublicationPlan(nil, nil, nil, true)
		prepared := &PreparedInputCommit{transaction: t, plan: plan, cacheable: true}
		t.state = transactionPrepared
		t.prepared = prepared
		return prepared, nil
	}

	preparedActive, publishedActive, publishedReplay, err := splitPublishedReplayCommit(active)
	if err != nil {
		return nil, err
	}
	sources, candidates, observations, err := t.commitInputsLocked()
	if err != nil {
		return nil, err
	}
	componentCommit, err := t.component.prepareStagedSourcesAndVerifyObservations(
		ctx,
		sources,
		candidates,
		observations,
		additional,
		preparedActive,
		refreshers,
		t.replayEpoch,
		t.replayState,
	)
	if err != nil {
		return nil, err
	}
	commits, watermark := componentCommit.Planned()
	acceptedByCandidate, err := t.acceptancePlanLocked(commits)
	if err != nil {
		componentCommit.Abort()
		return nil, err
	}
	committedSnapshots, cacheable := t.committedStatePlanLocked(acceptedByCandidate, watermark)
	if err := bindPublishedReplayLeases(
		componentCommit, publishedActive, publishedReplay, acceptedByCandidate, watermark,
	); err != nil {
		componentCommit.Abort()
		return nil, err
	}
	var committedReplay *purehttpstore.AcceptedReplayState
	if cacheable {
		committedReplay, err = planCommittedReplayState(componentCommit, committedSnapshots)
		if err != nil {
			componentCommit.Abort()
			return nil, err
		}
	}
	activeToken, activeTransition, hasActive := componentCommit.PlannedActiveLeases()
	inputPlan, err := componentCommit.bindInputPlan(committedReplay, committedSnapshots, cacheable)
	if err != nil {
		componentCommit.Abort()
		return nil, err
	}
	prepared = &PreparedInputCommit{
		transaction:        t,
		component:          componentCommit,
		plan:               inputPlan,
		committedSnapshots: slices.Clone(committedSnapshots),
		committedReplay:    committedReplay,
		cacheable:          cacheable,
		active:             activeToken,
		transition:         activeTransition,
		hasActive:          hasActive,
	}
	t.state = transactionPrepared
	t.prepared = prepared
	return prepared, nil
}

func splitPublishedReplayCommit(active *purehttpstore.ActiveLeaseCommit) (
	preparedActive *purehttpstore.ActiveLeaseCommit,
	publishedActive *purehttpstore.ActiveLeaseCommit,
	publishedReplay []purehttpstore.ContentSnapshot,
	err error,
) {
	if active == nil || len(active.PublishedReplay) == 0 {
		return active, nil, nil, nil
	}
	if active.Replay != nil {
		return nil, nil, nil, errors.New("published HTTP replay lease cannot include a captured replay state")
	}
	cloned := *active
	cloned.PublishedReplay = nil
	return nil, &cloned, slices.Clone(active.PublishedReplay), nil
}

func bindPublishedReplayLeases(
	componentCommit *preparedCandidateCommit,
	publishedActive *purehttpstore.ActiveLeaseCommit,
	publishedReplay []purehttpstore.ContentSnapshot,
	acceptedByCandidate map[purehttpstore.SnapshotToken]purehttpstore.SnapshotToken,
	watermark purehttpstore.Revision,
) error {
	if len(publishedReplay) == 0 {
		return nil
	}
	for index := range publishedReplay {
		if accepted, found := acceptedByCandidate[publishedReplay[index].Token]; found {
			publishedReplay[index].Token = accepted
			publishedReplay[index].Observation = accepted.Revision()
		}
		publishedReplay[index].Watermark = watermark
	}
	return componentCommit.preparePublishedReplayActiveLeases(publishedActive, publishedReplay)
}

func planCommittedReplayState(
	componentCommit *preparedCandidateCommit,
	committedSnapshots []purehttpstore.ContentSnapshot,
) (*purehttpstore.AcceptedReplayState, error) {
	if planned, ok := componentCommit.plannedActiveReplayState(); ok {
		return planned, nil
	}
	return componentCommit.prepareAcceptedReplayState(committedSnapshots)
}

func (t *InputTransaction) preparedStateLocked() (*PreparedInputCommit, bool, error) {
	switch t.state {
	case transactionCommitted:
		if t.prepared != nil {
			return t.prepared, true, nil
		}
		return &PreparedInputCommit{transaction: t, state: preparedInputPublished}, true, nil
	case transactionPrepared:
		return nil, true, errors.New("render input transaction is already prepared")
	case transactionAborted:
		return nil, true, errInputTransactionAborted
	default:
		return nil, false, nil
	}
}

func (t *InputTransaction) committedStatePlanLocked(
	accepted map[purehttpstore.SnapshotToken]purehttpstore.SnapshotToken,
	watermark purehttpstore.Revision,
) ([]purehttpstore.ContentSnapshot, bool) {
	snapshots := make([]purehttpstore.ContentSnapshot, 0, len(t.results))
	cacheable := true
	for _, result := range t.results {
		snapshot := result.snapshot
		if acceptedToken, found := accepted[snapshot.Token]; found {
			snapshot.Token = acceptedToken
			snapshot.Observation = acceptedToken.Revision()
		}
		snapshot.Watermark = watermark
		if result.err != nil || !snapshot.Cacheable {
			cacheable = false
		}
		snapshots = append(snapshots, snapshot)
	}
	slices.SortFunc(snapshots, func(a, b purehttpstore.ContentSnapshot) int {
		return strings.Compare(a.URL, b.URL)
	})
	return snapshots, cacheable
}

func (t *InputTransaction) commitInputsLocked() (
	[]*purehttpstore.StagedSource,
	[]*purehttpstore.InitialCandidate,
	[]purehttpstore.ObservationToken,
	error,
) {
	sourceURLs := make([]string, 0, len(t.sources))
	for url := range t.sources {
		sourceURLs = append(sourceURLs, url)
	}
	slices.Sort(sourceURLs)
	sources := make([]*purehttpstore.StagedSource, 0, len(sourceURLs))
	for _, url := range sourceURLs {
		source := t.sources[url]
		if source == nil {
			return nil, nil, nil, errors.New("staged HTTP source is missing from the prepared commit")
		}
		sources = append(sources, source)
	}
	urls := make([]string, 0, len(t.candidates))
	for url := range t.candidates {
		urls = append(urls, url)
	}
	slices.Sort(urls)
	candidates := make([]*purehttpstore.InitialCandidate, 0, len(urls))
	candidateTokens := make(map[purehttpstore.SnapshotToken]struct{}, len(urls))
	for _, url := range urls {
		candidate := t.candidates[url]
		if candidate == nil {
			return nil, nil, nil, errors.New("validated HTTP candidate is missing from the prepared commit")
		}
		candidates = append(candidates, candidate)
		candidateTokens[candidate.SnapshotToken()] = struct{}{}
	}
	observations := make([]purehttpstore.ObservationToken, 0, len(t.results))
	for _, result := range t.results {
		observation := result.snapshot.ObservationToken()
		if observation.Valid() {
			observations = append(observations, observation)
		}
		if result.snapshot.Token.Kind() == purehttpstore.SnapshotInitialCandidate {
			if _, exists := candidateTokens[result.snapshot.Token]; !exists {
				return nil, nil, nil, errors.New("validated HTTP candidate is missing from the prepared commit")
			}
		}
	}
	return sources, candidates, observations, nil
}

func (t *InputTransaction) acceptancePlanLocked(
	commits []purehttpstore.CandidateCommit,
) (map[purehttpstore.SnapshotToken]purehttpstore.SnapshotToken, error) {
	accepted := make(map[purehttpstore.SnapshotToken]purehttpstore.SnapshotToken, len(commits))
	for index := range commits {
		accepted[commits[index].Candidate] = commits[index].Accepted
	}
	for _, result := range t.results {
		if result.snapshot.Token.Kind() == purehttpstore.SnapshotInitialCandidate {
			if _, exists := accepted[result.snapshot.Token]; !exists {
				return nil, errors.New("validated HTTP candidate has no planned accepted snapshot")
			}
		}
	}
	return accepted, nil
}

// Commit atomically accepts every candidate used by the validated render.
func (t *InputTransaction) Commit(ctx context.Context) error {
	prepared, err := t.PrepareCommit(ctx)
	if err != nil {
		return err
	}
	defer prepared.Release()
	if cause := context.Cause(ctx); cause != nil {
		return fmt.Errorf("committing render inputs: %w", cause)
	}
	if err := prepared.SealPublication(); err != nil {
		return err
	}
	prepared.PublishSealed()
	return nil
}

// Abort discards render-local candidates.
func (t *InputTransaction) Abort() {
	t.mu.Lock()
	if t.prepared != nil {
		prepared := t.prepared
		t.mu.Unlock()
		prepared.Abort()
		return
	}
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return
	}
	t.state = transactionAborted
	t.sources = nil
	t.results = nil
	t.candidates = nil
	t.retrySeed = nil
	t.replayEpoch = nil
	t.replayState = nil
	t.committedSnapshots = nil
	t.committedReplay = nil
}
