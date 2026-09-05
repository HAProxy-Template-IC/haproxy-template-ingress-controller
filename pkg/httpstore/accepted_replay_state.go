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
	"errors"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
)

type acceptedReplayStateEntry struct {
	snapshot ContentSnapshot
	proof    *AcceptedReplayProof
}

type acceptedReplayStateAuthentication struct {
	store     *HTTPStore
	source    SourceID
	entries   *iradix.Node[acceptedReplayStateEntry]
	count     int
	epoch     *ReplayEpoch
	watermark Revision
	replay    Revision
}

// AcceptedReplayState authenticates the accepted HTTP inputs published by one render.
type AcceptedReplayState struct {
	store     *HTTPStore
	source    SourceID
	entries   *iradix.Tree[acceptedReplayStateEntry]
	root      *iradix.Node[acceptedReplayStateEntry]
	count     int
	epoch     *ReplayEpoch
	watermark Revision
	replay    Revision
	auth      acceptedReplayStateAuthentication
	seal      *AcceptedReplayState
}

// CaptureAcceptedReplayState binds accepted snapshots to one selective replay cursor.
func (s *HTTPStore) CaptureAcceptedReplayState(
	snapshots []ContentSnapshot,
) (*AcceptedReplayState, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return nil, false
	}
	proofs := make([]*AcceptedReplayProof, len(snapshots))
	for index := range snapshots {
		proof, ok := s.captureAcceptedReplayProofLocked(&snapshots[index])
		if !ok {
			return nil, false
		}
		proofs[index] = proof
	}
	return newAcceptedReplayStateLocked(s, snapshots, proofs, s.captureReplayEpochLocked())
}

func newAcceptedReplayStateLocked(
	store *HTTPStore,
	snapshots []ContentSnapshot,
	proofs []*AcceptedReplayProof,
	epoch *ReplayEpoch,
) (*AcceptedReplayState, bool) {
	if store == nil || epoch == nil || epoch.ValidateAuthentication() != nil ||
		epoch.store != store || epoch.source != store.revisionSource ||
		epoch.revision != store.replayRevision || len(snapshots) != len(proofs) {
		return nil, false
	}
	txn := iradix.New[acceptedReplayStateEntry]().Txn()
	for index := range snapshots {
		current, ok := store.currentAcceptedReplaySnapshotLocked(&snapshots[index], proofs[index])
		if !ok {
			return nil, false
		}
		if _, replaced := txn.Insert([]byte(current.URL), acceptedReplayStateEntry{
			snapshot: current,
			proof:    proofs[index],
		}); replaced {
			return nil, false
		}
	}
	return sealAcceptedReplayState(
		store,
		txn.Commit(),
		epoch,
		store.semanticRevision,
		store.replayRevision,
	), true
}

func (s *HTTPStore) currentAcceptedReplaySnapshotLocked(
	snapshot *ContentSnapshot,
	proof *AcceptedReplayProof,
) (ContentSnapshot, bool) {
	if !snapshot.Found || !snapshot.Cacheable || snapshot.StoreSource != s.revisionSource ||
		proof == nil || proof.validate() != nil ||
		proof.store != s || proof.url != snapshot.URL || proof.descriptor != snapshot.Descriptor ||
		proof.token != snapshot.Token {
		return ContentSnapshot{}, false
	}
	entry := s.cache[snapshot.URL]
	if entry == nil || entry != proof.entry {
		return ContentSnapshot{}, false
	}
	current := s.acceptedSnapshotLocked(entry, s.semanticRevision)
	if !sameAcceptedReplaySnapshot(snapshot, &current) {
		return ContentSnapshot{}, false
	}
	return current, true
}

func sameAcceptedReplaySnapshot(observed, current *ContentSnapshot) bool {
	return observed.URL == current.URL && observed.Descriptor == current.Descriptor &&
		observed.Content == current.Content && observed.Found == current.Found &&
		observed.Cacheable == current.Cacheable && observed.Token == current.Token &&
		observed.StoreSource == current.StoreSource && observed.Observation == current.Observation &&
		observed.Watermark >= observed.Observation && observed.Watermark <= current.Watermark
}

func sealAcceptedReplayState(
	store *HTTPStore,
	entries *iradix.Tree[acceptedReplayStateEntry],
	epoch *ReplayEpoch,
	watermark Revision,
	replay Revision,
) *AcceptedReplayState {
	state := &AcceptedReplayState{
		store: store, source: store.revisionSource, entries: entries, root: entries.Root(),
		count: entries.Len(), epoch: epoch, watermark: watermark, replay: replay,
	}
	state.auth = acceptedReplayStateAuthentication{
		store: state.store, source: state.source, entries: state.root, count: state.count,
		epoch: state.epoch, watermark: state.watermark, replay: state.replay,
	}
	state.seal = state
	return state
}

// Source returns the store identity that minted the state.
func (s *AcceptedReplayState) Source() SourceID {
	if s == nil {
		return 0
	}
	return s.source
}

// Snapshots returns detached accepted inputs in URL order.
func (s *AcceptedReplayState) Snapshots() []ContentSnapshot {
	if s == nil || s.entries == nil {
		return nil
	}
	result := make([]ContentSnapshot, 0, s.count)
	s.entries.Root().Walk(func(_ []byte, entry acceptedReplayStateEntry) bool {
		snapshot := entry.snapshot
		snapshot.Watermark = s.watermark
		result = append(result, snapshot)
		return false
	})
	return result
}

// Proofs returns the authenticated proofs corresponding to Snapshots.
func (s *AcceptedReplayState) Proofs() []*AcceptedReplayProof {
	if s == nil || s.entries == nil {
		return nil
	}
	result := make([]*AcceptedReplayProof, 0, s.count)
	s.entries.Root().Walk(func(_ []byte, entry acceptedReplayStateEntry) bool {
		result = append(result, entry.proof)
		return false
	})
	return result
}

// Epoch returns the complete HTTP replay root captured with the publication.
func (s *AcceptedReplayState) Epoch() *ReplayEpoch {
	if s == nil {
		return nil
	}
	return s.epoch
}

// Watermark returns the accepted-content journal position at publication.
func (s *AcceptedReplayState) Watermark() Revision {
	if s == nil {
		return 0
	}
	return s.watermark
}

// ReplayWatermark returns the render-observable journal position at publication.
func (s *AcceptedReplayState) ReplayWatermark() Revision {
	if s == nil {
		return 0
	}
	return s.replay
}

// ValidateAuthentication verifies that the state retains its minted identity and immutable root.
func (s *AcceptedReplayState) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.store == nil || s.source == 0 || s.entries == nil ||
		s.root != s.entries.Root() || s.count != s.entries.Len() || s.epoch == nil ||
		s.store != s.auth.store || s.source != s.auth.source || s.root != s.auth.entries ||
		s.count != s.auth.count || s.epoch != s.auth.epoch || s.watermark != s.auth.watermark ||
		s.replay != s.auth.replay || s.epoch.ValidateAuthentication() != nil ||
		s.epoch.store != s.store || s.epoch.source != s.source || s.epoch.revision != s.replay {
		return errors.New("accepted HTTP replay state has invalid provenance")
	}
	return nil
}

// AdvanceAcceptedReplayState rebases a state across unrelated URL changes.
func (s *HTTPStore) AdvanceAcceptedReplayState(
	state *AcceptedReplayState,
) (*AcceptedReplayState, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return nil, false
	}
	return s.advanceAcceptedReplayStateLocked(state)
}

func (s *HTTPStore) advanceAcceptedReplayStateLocked(
	state *AcceptedReplayState,
) (*AcceptedReplayState, bool) {
	if state == nil || state.ValidateAuthentication() != nil || state.store != s ||
		state.source != s.revisionSource {
		return nil, false
	}
	if state.replay == s.replayRevision {
		return state, true
	}
	if state.count == 0 {
		return sealAcceptedReplayState(
			s, state.entries, s.captureReplayEpochLocked(), s.semanticRevision, s.replayRevision,
		), true
	}
	current, changes, complete := s.replayChangesSinceLocked(state.replay)
	if !complete || current != s.replayRevision {
		return nil, false
	}
	for index := range changes {
		if _, relevant := state.entries.Get([]byte(changes[index].URL)); relevant {
			return nil, false
		}
	}
	epoch := s.captureReplayEpochLocked()
	return sealAcceptedReplayState(
		s,
		state.entries,
		epoch,
		s.semanticRevision,
		s.replayRevision,
	), true
}
