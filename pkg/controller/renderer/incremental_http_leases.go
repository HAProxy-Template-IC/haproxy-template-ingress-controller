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
	"errors"
	"fmt"
	"slices"
	"strings"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// RetireIncrementalCache releases persistent inputs owned by this service.
func (s *RenderService) RetireIncrementalCache() error {
	if s == nil || s.incremental == nil {
		return nil
	}
	s.incremental.cache.shutdown()
	return s.incremental.retireHTTPLeases(s.httpStoreComponent)
}

func (s *incrementalRenderState) retireHTTPLeases(component *controllerhttpstore.Component) error {
	s.httpLifecycleMu.Lock()
	defer s.httpLifecycleMu.Unlock()
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return nil
	}
	s.retiring = true
	leaseSet := s.httpLeaseSet
	token := s.snapshot.httpCursor.token
	if !token.Valid() {
		token = s.httpInitial
	}
	s.mu.Unlock()
	if leaseSet != nil {
		if component == nil {
			return fmt.Errorf("incremental HTTP cache has leases without an authority")
		}
		if !token.Valid() {
			return fmt.Errorf("incremental HTTP cache has no retirement token")
		}
		if err := component.RetireActiveLeases(leaseSet, token); err != nil {
			return fmt.Errorf("retiring incremental HTTP leases: %w", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpLeaseSet != leaseSet {
		return errors.New("incremental HTTP lease authority changed during retirement")
	}
	s.httpLeaseSet = nil
	s.httpInitial = httpstore.ActiveLeaseToken{}
	s.retiring = false
	s.retired = true
	return nil
}

func (r *incrementalRenderSession) activeLeaseCommit() (*httpstore.ActiveLeaseCommit, bool, error) {
	if r.httpComponent == nil {
		r.state.httpMu.Lock()
		defer r.state.httpMu.Unlock()
		if len(r.state.httpRefs) != 0 {
			return nil, false, fmt.Errorf("incremental HTTP cache has dependencies without an active lease authority")
		}
		return nil, false, nil
	}
	if r.httpLease == nil {
		return nil, false, fmt.Errorf("incremental HTTP cache has no active lease fence")
	}
	r.mu.Lock()
	replay := r.exactCycleHTTPLease
	publishedReplay := slices.Clone(r.exactCycleHTTPPublishedLease)
	r.mu.Unlock()
	if replay != nil && len(publishedReplay) != 0 {
		return nil, false, fmt.Errorf("incremental HTTP cache has conflicting exact replay leases")
	}
	r.httpMu.Lock()
	defer r.httpMu.Unlock()
	r.state.httpMu.Lock()
	defer r.state.httpMu.Unlock()
	if r.cold {
		commit, err := r.coldActiveLeaseCommitLocked()
		if commit != nil {
			commit.Replay = replay
			commit.PublishedReplay = publishedReplay
		}
		return commit, commit != nil, err
	}
	updates := make([]httpstore.ActiveLeaseUpdate, 0, len(r.httpRefDeltas))
	for id, delta := range r.httpRefDeltas {
		spec, err := r.state.validateHTTPInputLocked(id)
		if err != nil {
			return nil, false, err
		}
		updates = append(updates, httpstore.ActiveLeaseUpdate{
			URL:        spec.url,
			Descriptor: spec.descriptor,
			Added:      delta.added,
			Removed:    delta.removed,
		})
	}
	sortActiveLeaseUpdates(updates)
	return &httpstore.ActiveLeaseCommit{
		Snapshot: r.httpLease, Updates: updates, Replay: replay, PublishedReplay: publishedReplay,
	}, true, nil
}

func (r *incrementalRenderSession) exactCycleOutputActiveLeaseCommit() (
	*httpstore.ActiveLeaseCommit,
	bool,
	error,
) {
	r.mu.Lock()
	enabled := r.fullCold || r.exactCycleOutputOnlyReplay
	replay := r.exactCycleHTTPLease
	publishedReplay := slices.Clone(r.exactCycleHTTPPublishedLease)
	r.mu.Unlock()
	if !enabled || replay == nil && len(publishedReplay) == 0 {
		return nil, false, nil
	}
	if replay != nil && len(publishedReplay) != 0 {
		return nil, false, fmt.Errorf("incremental HTTP cache has conflicting exact replay leases")
	}
	if r.httpComponent == nil || r.httpLease == nil {
		return nil, false, fmt.Errorf("exact output HTTP cache has no active lease authority")
	}
	return &httpstore.ActiveLeaseCommit{
		Snapshot: r.httpLease, Replay: replay, PublishedReplay: publishedReplay,
	}, true, nil
}

func (r *incrementalRenderSession) prepareExactCycleHTTPLeaseSnapshotLocked(
	token httpstore.ActiveLeaseToken,
) (*incrementalStateSnapshot, error) {
	if r.state == nil || r.state.snapshot == nil || !token.Valid() {
		return nil, fmt.Errorf("exact output HTTP cache has no authenticated lease token")
	}
	next := *r.state.snapshot
	next.httpCursor = incrementalHTTPCursor{token: token}
	authenticateIncrementalStatusPatchPlan(&next)
	authenticateIncrementalStateSnapshot(&next)
	return &next, nil
}

func (r *incrementalRenderSession) coldActiveLeaseCommitLocked() (*httpstore.ActiveLeaseCommit, error) {
	refs, err := countHTTPRefsRoot(r.httpEffects.Root())
	if err != nil {
		return nil, err
	}
	replacement := make([]httpstore.ActiveLeaseReference, 0, len(refs))
	for id, references := range refs {
		spec, specErr := r.state.validateHTTPInputLocked(id)
		if specErr != nil {
			return nil, specErr
		}
		replacement = append(replacement, httpstore.ActiveLeaseReference{
			URL:        spec.url,
			Descriptor: spec.descriptor,
			References: references,
		})
	}
	slices.SortFunc(replacement, func(left, right httpstore.ActiveLeaseReference) int {
		if compared := strings.Compare(left.URL, right.URL); compared != 0 {
			return compared
		}
		return left.Descriptor.Compare(right.Descriptor)
	})
	return &httpstore.ActiveLeaseCommit{
		Snapshot: r.httpLease, Replacement: replacement, Replace: true,
	}, nil
}

func countHTTPRefsRoot(root *iradix.Node[*iradix.Tree[incrementalHTTPEffect]]) (map[uint64]uint64, error) {
	if root == nil {
		return nil, errors.New("incremental HTTP effect root is unavailable")
	}
	refs := map[uint64]uint64{}
	var countErr error
	root.Walk(func(_ []byte, effects *iradix.Tree[incrementalHTTPEffect]) bool {
		if err := validateIndexedHTTPEffects(effects); err != nil {
			countErr = err
			return true
		}
		effects.Root().Walk(func(_ []byte, effect incrementalHTTPEffect) bool {
			if refs[effect.inputID] == ^uint64(0) {
				countErr = fmt.Errorf("incremental HTTP input %d reference count is exhausted", effect.inputID)
				return true
			}
			refs[effect.inputID]++
			return false
		})
		return countErr != nil
	})
	return refs, countErr
}

func sortActiveLeaseUpdates(updates []httpstore.ActiveLeaseUpdate) {
	slices.SortFunc(updates, func(left, right httpstore.ActiveLeaseUpdate) int {
		if compared := strings.Compare(left.URL, right.URL); compared != 0 {
			return compared
		}
		return left.Descriptor.Compare(right.Descriptor)
	})
}
