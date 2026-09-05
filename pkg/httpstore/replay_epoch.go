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

// ReplayEpoch binds the complete render-relevant state of one HTTP store.
type ReplayEpoch struct {
	store    *HTTPStore
	source   SourceID
	revision Revision
	auth     struct {
		store    *HTTPStore
		source   SourceID
		revision Revision
	}
	seal *ReplayEpoch
}

// CaptureReplayEpoch snapshots the global render-relevant HTTP epoch.
func (s *HTTPStore) CaptureReplayEpoch() *ReplayEpoch {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return nil
	}
	return s.captureReplayEpochLocked()
}

func (s *HTTPStore) captureReplayEpochLocked() *ReplayEpoch {
	return sealReplayEpoch(s, s.replayRevision)
}

func sealReplayEpoch(store *HTTPStore, revision Revision) *ReplayEpoch {
	epoch := &ReplayEpoch{store: store, source: store.revisionSource, revision: revision}
	epoch.auth.store = epoch.store
	epoch.auth.source = epoch.source
	epoch.auth.revision = epoch.revision
	epoch.seal = epoch
	return epoch
}

// Source returns the store instance identity.
func (e *ReplayEpoch) Source() SourceID {
	if e == nil {
		return 0
	}
	return e.source
}

// Revision returns the complete render-relevant revision.
func (e *ReplayEpoch) Revision() Revision {
	if e == nil {
		return 0
	}
	return e.revision
}

// ValidateAuthentication verifies that the epoch retains its minted identity.
func (e *ReplayEpoch) ValidateAuthentication() error {
	if e == nil || e.seal != e || e.store == nil || e.source == 0 ||
		e.store != e.auth.store || e.source != e.auth.source || e.revision != e.auth.revision {
		return errors.New("HTTP replay epoch has invalid provenance")
	}
	return nil
}

func (s *HTTPStore) replayEpochCurrentLocked(epoch *ReplayEpoch) bool {
	return epoch != nil && epoch.ValidateAuthentication() == nil && epoch.store == s &&
		epoch.source == s.revisionSource && epoch.revision == s.replayRevision
}

// VerifyReplayEpoch reports whether the complete HTTP render root is current.
func (s *HTTPStore) VerifyReplayEpoch(epoch *ReplayEpoch) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return false
	}
	return s.replayEpochCurrentLocked(epoch)
}
