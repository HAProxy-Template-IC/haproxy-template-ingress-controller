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
	"math"
	"sync/atomic"
	"time"
)

const defaultSemanticJournalCapacity = 4096
const defaultReplayJournalCapacity = 4096

// SourceID identifies one HTTPStore for its lifetime. Zero is unsupported.
type SourceID uint64

// Revision is a monotonic semantic revision within one HTTPStore.
type Revision uint64

// SnapshotKind distinguishes shared accepted content from render-local versions.
type SnapshotKind uint8

const (
	SnapshotAccepted SnapshotKind = iota + 1
	SnapshotPending
	SnapshotInitialCandidate
)

// SnapshotToken identifies the exact bytes and source returned by one store read.
type SnapshotToken struct {
	source     SourceID
	url        string
	descriptor SourceDescriptor
	kind       SnapshotKind
	revision   Revision
}

// Valid reports whether the token identifies a cacheable snapshot.
func (t SnapshotToken) Valid() bool {
	return t.source != 0 && t.url != "" && t.kind != 0 && t.revision != 0
}

// Source returns the store instance that minted this token.
func (t SnapshotToken) Source() SourceID {
	return t.source
}

// URL returns the exact requested URL.
func (t SnapshotToken) URL() string {
	return t.url
}

// SourceIdentity returns the opaque effective options and authentication identity.
func (t SnapshotToken) SourceIdentity() string {
	return t.descriptor.Identity()
}

// SourceDescriptor returns the exact opaque fetch declaration.
func (t SnapshotToken) SourceDescriptor() SourceDescriptor {
	return t.descriptor
}

// Kind returns which content lifecycle supplied the bytes.
func (t SnapshotToken) Kind() SnapshotKind {
	return t.kind
}

// Revision returns the content version within the store.
func (t SnapshotToken) Revision() Revision {
	return t.revision
}

// ContentSnapshot binds template-observable bytes to their exact store version.
type ContentSnapshot struct {
	URL         string
	Descriptor  SourceDescriptor
	Content     string
	Found       bool
	Cacheable   bool
	Token       SnapshotToken
	StoreSource SourceID
	Observation Revision
	Watermark   Revision
}

// ObservationToken identifies one exact present or negative content read.
type ObservationToken struct {
	source     SourceID
	url        string
	descriptor SourceDescriptor
	revision   Revision
	watermark  Revision
	found      bool
	accepted   SnapshotToken
}

// Valid reports whether the token can be verified by its source store.
func (t *ObservationToken) Valid() bool {
	if t == nil {
		return false
	}
	if t.source == 0 || t.url == "" {
		return false
	}
	if !t.found {
		return !t.accepted.Valid()
	}
	return t.accepted.Valid() && t.accepted.source == t.source &&
		t.accepted.url == t.url && t.accepted.descriptor == t.descriptor &&
		t.accepted.kind == SnapshotAccepted && t.accepted.revision == t.revision
}

// Revision returns the exact observable content revision.
func (t *ObservationToken) Revision() Revision {
	if t == nil {
		return 0
	}
	return t.revision
}

// Watermark returns the journal position captured with the read.
func (t *ObservationToken) Watermark() Revision {
	if t == nil {
		return 0
	}
	return t.watermark
}

// Found reports whether accepted content existed for the declaration.
func (t *ObservationToken) Found() bool {
	if t == nil {
		return false
	}
	return t.found
}

// ObservationToken returns the exact verification token for this snapshot.
func (s *ContentSnapshot) ObservationToken() ObservationToken {
	if s == nil {
		return ObservationToken{}
	}
	observation := ObservationToken{
		source:     s.StoreSource,
		url:        s.URL,
		descriptor: s.Descriptor,
		revision:   s.Observation,
		watermark:  s.Watermark,
		found:      s.Found,
	}
	if s.Found && s.Token.Kind() == SnapshotAccepted {
		observation.accepted = s.Token
	}
	return observation
}

// MarshalJSON rejects lossy serialization of opaque descriptors and tokens.
func (ContentSnapshot) MarshalJSON() ([]byte, error) {
	return nil, errors.New("HTTP content snapshots require explicit dependency encoding")
}

// SemanticChange identifies an accepted-content or source-authority transition.
type SemanticChange struct {
	Revision               Revision
	URL                    string
	PreviousSourceIdentity string
	SourceIdentity         string
	PreviousDescriptor     SourceDescriptor
	Descriptor             SourceDescriptor
	Removed                bool
	authentication         uint64
}

// ReplayChange identifies one URL whose render-observable state changed.
type ReplayChange struct {
	Revision       Revision
	URL            string
	authentication uint64
}

// CandidateCommit maps render-local candidate bytes to the accepted version
// created by the same atomic commit.
type CandidateCommit struct {
	Candidate SnapshotToken
	Accepted  SnapshotToken
}

var nextHTTPStoreSource atomic.Uint64

func allocateHTTPStoreSource() SourceID {
	for {
		current := nextHTTPStoreSource.Load()
		if current == ^uint64(0) {
			panic("HTTP store source identity exhausted")
		}
		if nextHTTPStoreSource.CompareAndSwap(current, current+1) {
			return SourceID(current + 1)
		}
	}
}

func (s *HTTPStore) nextSemanticRevisionLocked() Revision {
	if s.semanticRevision == Revision(^uint64(0)) {
		panic("HTTP store semantic revision exhausted")
	}
	s.semanticRevision++
	return s.semanticRevision
}

func (s *HTTPStore) recordReplayChangeLocked(url string) {
	if url == "" {
		panic("HTTP replay change has no URL")
	}
	if s.replayRevision == Revision(^uint64(0)) {
		panic("HTTP store replay revision exhausted")
	}
	s.replayRevision++
	change := ReplayChange{Revision: s.replayRevision, URL: url}
	change.authentication = authenticateReplayChange(s.revisionSource, &change)
	if s.replayJournalCapacity == 0 {
		return
	}
	if len(s.replayJournal) < s.replayJournalCapacity {
		s.replayJournal = append(s.replayJournal, change)
		return
	}
	s.replayJournal[s.replayJournalStart] = change
	s.replayJournalStart = (s.replayJournalStart + 1) % s.replayJournalCapacity
}

func (s *HTTPStore) recordSemanticChangeLocked(
	url string,
	previousSource, source SourceDescriptor,
	removed bool,
) Revision {
	s.recordReplayChangeLocked(url)
	revision := s.nextSemanticRevisionLocked()
	s.recordActiveLeaseChangeLocked(url, previousSource, source)
	change := SemanticChange{
		Revision:               revision,
		URL:                    url,
		PreviousSourceIdentity: previousSource.Identity(),
		SourceIdentity:         source.Identity(),
		PreviousDescriptor:     previousSource,
		Descriptor:             source,
		Removed:                removed,
	}
	change.authentication = authenticateSemanticChange(s.revisionSource, &change)
	if s.semanticJournalCapacity == 0 {
		return revision
	}
	if len(s.semanticJournal) < s.semanticJournalCapacity {
		s.semanticJournal = append(s.semanticJournal, change)
		return revision
	}
	s.semanticJournal[s.semanticJournalStart] = change
	s.semanticJournalStart = (s.semanticJournalStart + 1) % s.semanticJournalCapacity
	return revision
}

// RevisionSource returns the stable identity of this HTTPStore instance.
func (s *HTTPStore) RevisionSource() SourceID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return 0
	}
	return s.revisionSource
}

// Watermark returns the current accepted-content semantic revision.
func (s *HTTPStore) Watermark() Revision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return 0
	}
	return s.semanticRevision
}

// ReplayWatermark returns the render-relevant source, content, and pending epoch.
func (s *HTTPStore) ReplayWatermark() Revision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return 0
	}
	return s.replayRevision
}

// ChangesSince returns the bounded accepted-content change journal.
func (s *HTTPStore) ChangesSince(revision Revision) (
	current Revision,
	changes []SemanticChange,
	complete bool,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return 0, nil, false
	}
	return s.changesSinceLocked(revision)
}

func (s *HTTPStore) replayChangesSinceLocked(revision Revision) (
	current Revision,
	changes []ReplayChange,
	complete bool,
) {
	current = s.replayRevision
	if revision > current {
		return current, nil, false
	}
	if revision == current {
		return current, nil, true
	}
	if len(s.replayJournal) == 0 {
		return current, nil, false
	}
	oldest := s.replayJournal[s.replayJournalStart].Revision
	if oldest == 0 || revision < oldest-1 {
		return current, nil, false
	}
	span := uint64(revision + 1 - oldest)
	if span > math.MaxInt {
		return current, nil, false
	}
	start := int(span)
	if start >= len(s.replayJournal) {
		return current, nil, false
	}
	changes = make([]ReplayChange, 0, len(s.replayJournal)-start)
	expected := revision + 1
	for offset := start; offset < len(s.replayJournal); offset++ {
		change := s.replayJournal[(s.replayJournalStart+offset)%len(s.replayJournal)]
		if change.Revision != expected || change.URL == "" ||
			change.authentication != authenticateReplayChange(s.revisionSource, &change) {
			return current, nil, false
		}
		changes = append(changes, change)
		expected++
	}
	return current, changes, true
}

func (s *HTTPStore) changesSinceLocked(revision Revision) (
	current Revision,
	changes []SemanticChange,
	complete bool,
) {
	current = s.semanticRevision
	if revision > current {
		return current, nil, false
	}
	if revision == current {
		return current, nil, true
	}
	if len(s.semanticJournal) == 0 {
		return current, nil, false
	}
	oldest := s.semanticJournal[s.semanticJournalStart].Revision
	if oldest == 0 || revision < oldest-1 {
		return current, nil, false
	}

	span := uint64(revision + 1 - oldest)
	if span > math.MaxInt {
		return current, nil, false
	}
	start := int(span)
	if start >= len(s.semanticJournal) {
		return current, nil, false
	}
	changes = make([]SemanticChange, 0, len(s.semanticJournal)-start)
	expected := revision + 1
	for offset := start; offset < len(s.semanticJournal); offset++ {
		change := s.semanticJournal[(s.semanticJournalStart+offset)%len(s.semanticJournal)]
		if change.Revision != expected ||
			change.authentication != authenticateSemanticChange(s.revisionSource, &change) {
			return current, nil, false
		}
		changes = append(changes, change)
		expected++
	}
	return current, changes, true
}

// AcceptedSnapshot returns accepted bytes and their exact source version atomically.
func (s *HTTPStore) AcceptedSnapshot(url string, source SourceDescriptor) ContentSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publicationErrorLocked() != nil {
		return ContentSnapshot{URL: url, Descriptor: source}
	}

	snapshot := ContentSnapshot{
		URL:         url,
		Descriptor:  source,
		StoreSource: s.revisionSource,
		Watermark:   s.semanticRevision,
	}
	entry, exists := s.cache[url]
	if !exists || entry.sourceDescriptor != source || entry.AcceptedChecksum == "" {
		return snapshot
	}
	entry.LastAccessTime = time.Now()
	return s.acceptedSnapshotLocked(entry, s.semanticRevision)
}

func (s *HTTPStore) acceptedSnapshotLocked(entry *CacheEntry, watermark Revision) ContentSnapshot {
	return ContentSnapshot{
		URL:         entry.URL,
		Descriptor:  entry.sourceDescriptor,
		Content:     entry.AcceptedContent,
		Found:       true,
		Cacheable:   entry.acceptedRevision != 0,
		Token:       s.acceptedTokenLocked(entry),
		StoreSource: s.revisionSource,
		Observation: entry.acceptedRevision,
		Watermark:   watermark,
	}
}

// PinAcceptedSnapshot returns and touches the exact accepted version identified by token.
func (s *HTTPStore) PinAcceptedSnapshot(token SnapshotToken) (ContentSnapshot, SourceState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publicationErrorLocked() != nil {
		return ContentSnapshot{}, SourceState{}, false
	}

	if token.source != s.revisionSource || token.kind != SnapshotAccepted || token.revision == 0 {
		return ContentSnapshot{}, SourceState{}, false
	}
	entry, exists := s.cache[token.url]
	if !exists || entry.sourceDescriptor != token.descriptor || entry.AcceptedChecksum == "" ||
		entry.acceptedRevision != token.revision {
		return ContentSnapshot{}, SourceState{}, false
	}
	entry.LastAccessTime = time.Now()
	return ContentSnapshot{
		URL:         token.url,
		Descriptor:  token.descriptor,
		Content:     entry.AcceptedContent,
		Found:       true,
		Cacheable:   true,
		Token:       token,
		StoreSource: s.revisionSource,
		Observation: token.revision,
		Watermark:   s.semanticRevision,
	}, sourceState(entry), true
}

func (s *HTTPStore) acceptedTokenLocked(entry *CacheEntry) SnapshotToken {
	return SnapshotToken{
		source:     s.revisionSource,
		url:        entry.URL,
		descriptor: entry.sourceDescriptor,
		kind:       SnapshotAccepted,
		revision:   entry.acceptedRevision,
	}
}

func (s *HTTPStore) candidateTokenLocked(candidate *InitialCandidate) SnapshotToken {
	return SnapshotToken{
		source:     s.revisionSource,
		url:        candidate.url,
		descriptor: candidate.sourceDescriptor,
		kind:       SnapshotInitialCandidate,
		revision:   candidate.candidateRevision,
	}
}

// VerifySnapshots atomically verifies that every accepted token is still current.
func (s *HTTPStore) VerifySnapshots(tokens []SnapshotToken) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return false
	}
	return s.verifySnapshotsLocked(tokens)
}

// VerifyObservations atomically verifies exact present and negative reads.
func (s *HTTPStore) VerifyObservations(tokens []ObservationToken) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return false
	}
	return s.verifyObservationsLocked(tokens)
}

func (s *HTTPStore) verifyObservationsLocked(tokens []ObservationToken) bool {
	for index := range tokens {
		if !s.verifyObservationLocked(&tokens[index]) {
			return false
		}
	}
	return true
}

func (s *HTTPStore) verifyObservationLocked(token *ObservationToken) bool {
	if !token.Valid() || token.source != s.revisionSource || token.watermark > s.semanticRevision {
		return false
	}
	if token.found {
		return s.verifySnapshotLocked(&token.accepted)
	}
	entry, exists := s.cache[token.url]
	if exists && entry.sourceDescriptor == token.descriptor && entry.AcceptedChecksum != "" {
		return false
	}
	_, changes, complete := s.changesSinceLocked(token.watermark)
	if !complete {
		return false
	}
	for index := range changes {
		change := &changes[index]
		if change.URL == token.url &&
			(change.PreviousDescriptor == token.descriptor || change.Descriptor == token.descriptor) {
			return false
		}
	}
	return true
}

func (s *HTTPStore) verifySnapshotsLocked(tokens []SnapshotToken) bool {
	for index := range tokens {
		if !s.verifySnapshotLocked(&tokens[index]) {
			return false
		}
	}
	return true
}

func (s *HTTPStore) verifySnapshotLocked(token *SnapshotToken) bool {
	if token == nil || token.source != s.revisionSource || token.kind != SnapshotAccepted || token.revision == 0 {
		return false
	}
	entry, exists := s.cache[token.url]
	return exists && entry.sourceDescriptor == token.descriptor &&
		entry.AcceptedChecksum != "" && entry.acceptedRevision == token.revision
}

// CommitInitialCandidatesAndVerify verifies all pre-existing reads and accepts
// every validated initial candidate in one store transaction.
func (s *HTTPStore) CommitInitialCandidatesAndVerify(
	ctx context.Context,
	candidates []*InitialCandidate,
	accepted []SnapshotToken,
) ([]CandidateCommit, Revision, error) {
	return s.commitInitialCandidatesAndVerify(ctx, candidates, accepted, nil)
}

// CommitInitialCandidatesAndVerifyObservations verifies exact present and
// negative reads before accepting validated initial candidates.
func (s *HTTPStore) CommitInitialCandidatesAndVerifyObservations(
	ctx context.Context,
	candidates []*InitialCandidate,
	observations []ObservationToken,
) ([]CandidateCommit, Revision, error) {
	return s.commitInitialCandidatesAndVerify(ctx, candidates, nil, observations)
}

func (s *HTTPStore) commitInitialCandidatesAndVerify(
	ctx context.Context,
	candidates []*InitialCandidate,
	accepted []SnapshotToken,
	observations []ObservationToken,
) ([]CandidateCommit, Revision, error) {
	prepared, err := s.prepareInitialCandidates(
		ctx, nil, candidates, accepted, observations, observations, nil, nil, nil,
	)
	if err != nil {
		return nil, 0, err
	}
	defer prepared.Abort()
	commits, watermark := prepared.Planned()
	if cause := context.Cause(ctx); cause != nil {
		prepared.Abort()
		return nil, 0, fmt.Errorf("committing initial HTTP candidates: %w", cause)
	}
	prepared.Publish()
	prepared.Release()
	return commits, watermark, nil
}

func (s *HTTPStore) validateInitialCandidatesLocked(
	candidates []*InitialCandidate,
	sources map[string]*StagedSource,
) error {
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.store != s || candidate.candidateRevision == 0 ||
			candidate.candidateRevision > s.nextCandidateRevision {
			return errors.New("initial HTTP candidate does not belong to this store")
		}
		if _, exists := seen[candidate.url]; exists {
			return fmt.Errorf("initial HTTP candidate for %s appears more than once", candidate.url)
		}
		seen[candidate.url] = struct{}{}
		if s.candidateTokenLocked(candidate) != candidate.token {
			return fmt.Errorf("HTTP source %s has an invalid initial candidate token", candidate.url)
		}
		if candidate.source != nil {
			if sources[candidate.url] != candidate.source || candidate.sourceDescriptor != candidate.source.Descriptor() {
				return fmt.Errorf("HTTP source %s is missing its staged declaration", candidate.url)
			}
			if candidate.source.Changed() {
				continue
			}
		}
		entry, exists := s.cache[candidate.url]
		if !exists || entry != candidate.entry || entry.sourceDescriptor != candidate.sourceDescriptor ||
			entry.sourceGeneration != candidate.sourceGeneration ||
			entry.mutationRevision != candidate.mutationRevision || entry.AcceptedChecksum != "" || entry.HasPending {
			return fmt.Errorf("HTTP source %s changed before its validated content could be accepted", candidate.url)
		}
	}
	return nil
}
