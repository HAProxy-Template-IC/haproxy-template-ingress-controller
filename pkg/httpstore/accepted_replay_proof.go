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

import "errors"

type acceptedReplayProofAuthentication struct {
	store      *HTTPStore
	entry      *CacheEntry
	source     SourceID
	url        string
	descriptor SourceDescriptor
	generation uint64
	replay     uint64
	token      SnapshotToken
}

// AcceptedReplayProof authenticates one accepted value and its source state.
type AcceptedReplayProof struct {
	store      *HTTPStore
	entry      *CacheEntry
	source     SourceID
	url        string
	descriptor SourceDescriptor
	generation uint64
	replay     uint64
	token      SnapshotToken
	auth       acceptedReplayProofAuthentication
	seal       *AcceptedReplayProof
}

// URL returns the observed source URL.
func (p *AcceptedReplayProof) URL() string {
	if p == nil {
		return ""
	}
	return p.url
}

// Descriptor returns the exact observed source declaration.
func (p *AcceptedReplayProof) Descriptor() SourceDescriptor {
	if p == nil {
		return SourceDescriptor{}
	}
	return p.descriptor
}

// ValidateAuthentication verifies that the proof retains its minted identity.
func (p *AcceptedReplayProof) ValidateAuthentication() error {
	return p.validate()
}

// CaptureAcceptedReplayProof binds an accepted snapshot to current refresh state.
func (s *HTTPStore) CaptureAcceptedReplayProof(snapshot *ContentSnapshot) (*AcceptedReplayProof, bool) {
	if s == nil || snapshot == nil || !snapshot.Found || !snapshot.Cacheable {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil || snapshot.StoreSource != s.revisionSource {
		return nil, false
	}
	return s.captureAcceptedReplayProofLocked(snapshot)
}

func (s *HTTPStore) captureAcceptedReplayProofLocked(
	snapshot *ContentSnapshot,
) (*AcceptedReplayProof, bool) {
	if !s.verifySnapshotLocked(&snapshot.Token) {
		return nil, false
	}
	entry := s.cache[snapshot.URL]
	if entry == nil || entry.HasPending || entry.ValidationState == StateValidating {
		return nil, false
	}
	return sealAcceptedReplayProof(s, entry, snapshot), true
}

func sealAcceptedReplayProof(
	store *HTTPStore,
	entry *CacheEntry,
	snapshot *ContentSnapshot,
) *AcceptedReplayProof {
	proof := &AcceptedReplayProof{
		store: store, entry: entry, source: store.revisionSource, url: snapshot.URL,
		descriptor: snapshot.Descriptor, generation: entry.sourceGeneration,
		replay: entry.replayRevision, token: snapshot.Token,
	}
	proof.auth = acceptedReplayProofAuthentication{
		store: proof.store, entry: proof.entry, source: proof.source, url: proof.url,
		descriptor: proof.descriptor, generation: proof.generation,
		replay: proof.replay, token: proof.token,
	}
	proof.seal = proof
	return proof
}

// StageAcceptedReplayProof pins a proof only while every source field remains exact.
func (s *HTTPStore) StageAcceptedReplayProof(
	proof *AcceptedReplayProof,
) (ContentSnapshot, *StagedSource, bool) {
	if proof == nil || proof.validate() != nil || proof.store != s {
		return ContentSnapshot{}, nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return ContentSnapshot{}, nil, false
	}
	entry := s.cache[proof.url]
	if entry == nil || entry != proof.entry || entry.sourceDescriptor != proof.descriptor ||
		entry.sourceGeneration != proof.generation || entry.replayRevision != proof.replay ||
		entry.HasPending || entry.ValidationState == StateValidating ||
		!s.verifySnapshotLocked(&proof.token) {
		return ContentSnapshot{}, nil, false
	}
	var spec sourceSpec
	if entry.fixture && proof.descriptor == (SourceDescriptor{}) {
		spec = sourceSpec{options: entry.Options, auth: entry.Auth}
	} else {
		var err error
		spec, err = normalizeSource(entry.Options, entry.Auth)
		if err != nil || spec.descriptor != proof.descriptor {
			return ContentSnapshot{}, nil, false
		}
	}
	return s.acceptedSnapshotLocked(entry, s.semanticRevision), s.stageSourceLocked(proof.url, &spec), true
}

func (p *AcceptedReplayProof) validate() error {
	if p == nil || p.seal != p || p.store == nil || p.entry == nil || p.source == 0 || p.url == "" ||
		!p.token.Valid() || p.token.Source() != p.source || p.token.URL() != p.url ||
		p.token.SourceDescriptor() != p.descriptor || p.token.Kind() != SnapshotAccepted ||
		p.store != p.auth.store || p.entry != p.auth.entry || p.source != p.auth.source ||
		p.url != p.auth.url || p.descriptor != p.auth.descriptor || p.generation != p.auth.generation ||
		p.replay != p.auth.replay || p.token != p.auth.token {
		return errors.New("accepted HTTP replay proof has invalid provenance")
	}
	return nil
}
