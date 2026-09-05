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
	"fmt"
	"slices"
	"strings"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
)

// ActiveLeaseSet owns the HTTP dependencies of one persistent render cache.
type ActiveLeaseSet struct {
	store   *HTTPStore
	id      uint64
	initial *activeLeaseSeal
}

type activeLeaseSeal struct{}

// ActiveLeaseToken authenticates one committed lease root and acknowledgment.
type ActiveLeaseToken struct {
	source     SourceID
	setID      uint64
	generation uint64
	seal       *activeLeaseSeal
}

// Valid reports whether the token was minted for a lease set.
func (t ActiveLeaseToken) Valid() bool {
	return t.source != 0 && t.setID != 0 && t.seal != nil
}

// Source returns the store that minted the token.
func (t ActiveLeaseToken) Source() SourceID {
	return t.source
}

// ActiveLeaseRevision is monotonic within one lease set.
type ActiveLeaseRevision uint64

// ActiveLeaseChange identifies one leased declaration whose meaning changed.
type ActiveLeaseChange struct {
	URL        string
	Descriptor SourceDescriptor
	Revision   ActiveLeaseRevision
}

// ActiveLeaseUpdate changes the reference count for one exact declaration.
type ActiveLeaseUpdate struct {
	URL        string
	Descriptor SourceDescriptor
	Added      uint64
	Removed    uint64
}

// ActiveLeaseReference is one exact declaration and its complete reference count.
type ActiveLeaseReference struct {
	URL        string
	Descriptor SourceDescriptor
	References uint64
}

// ActiveLeaseCommit carries either changed reference counts or a cold replacement.
type ActiveLeaseCommit struct {
	Snapshot        *ActiveLeaseSnapshot
	Updates         []ActiveLeaseUpdate
	Replacement     []ActiveLeaseReference
	Replace         bool
	Replay          *AcceptedReplayState
	PublishedReplay []ContentSnapshot
}

// ActiveLeaseTransition reports URL-level zero-to-one and one-to-zero changes.
type ActiveLeaseTransition struct {
	Activated []string
	Retired   []string
}

type activeLeaseValue struct {
	descriptor SourceDescriptor
	references uint64
}

type activeLeaseState struct {
	token          ActiveLeaseToken
	leases         *iradix.Tree[activeLeaseValue]
	replay         *AcceptedReplayState
	changeRevision ActiveLeaseRevision
	pending        map[string]ActiveLeaseChange
}

type activeLeaseSnapshotSeal struct {
	set             *ActiveLeaseSet
	token           ActiveLeaseToken
	changeRevision  ActiveLeaseRevision
	leaseRoot       *iradix.Node[activeLeaseValue]
	replay          *AcceptedReplayState
	pendingChangeCt int
}

// ActiveLeaseSnapshot is an authenticated begin-of-render lease fence.
type ActiveLeaseSnapshot struct {
	set            *ActiveLeaseSet
	token          ActiveLeaseToken
	changeRevision ActiveLeaseRevision
	leases         *iradix.Tree[activeLeaseValue]
	replay         *AcceptedReplayState
	changes        []ActiveLeaseChange
	seal           *activeLeaseSnapshotSeal
}

// Changes returns detached relevant changes pending at the begin fence.
func (s *ActiveLeaseSnapshot) Changes() []ActiveLeaseChange {
	if s == nil {
		return nil
	}
	return slices.Clone(s.changes)
}

// HasChanges reports whether a leased declaration changed since its last publication.
func (s *ActiveLeaseSnapshot) HasChanges() bool {
	return s != nil && len(s.changes) != 0
}

// Contains reports whether the exact declaration was active at the begin fence.
func (s *ActiveLeaseSnapshot) Contains(url string, descriptor SourceDescriptor) bool {
	if s == nil || s.leases == nil {
		return false
	}
	value, found := s.leases.Get([]byte(url))
	return found && value.descriptor == descriptor && value.references != 0
}

// ContainsURL reports whether any declaration for the URL was active.
func (s *ActiveLeaseSnapshot) ContainsURL(url string) bool {
	if s == nil || s.leases == nil {
		return false
	}
	value, found := s.leases.Get([]byte(url))
	return found && value.references != 0
}

// ReplayContains reports whether the selective replay lease owns the declaration.
func (s *ActiveLeaseSnapshot) ReplayContains(url string, descriptor SourceDescriptor) bool {
	if s == nil || s.replay == nil || s.replay.ValidateAuthentication() != nil {
		return false
	}
	entry, found := s.replay.entries.Get([]byte(url))
	return found && entry.snapshot.Descriptor == descriptor
}

// Token returns the exact committed token from which this snapshot began.
func (s *ActiveLeaseSnapshot) Token() ActiveLeaseToken {
	if s == nil {
		return ActiveLeaseToken{}
	}
	return s.token
}

// NewActiveLeaseSet allocates an empty lease owner without publishing leases.
func (s *HTTPStore) NewActiveLeaseSet() (*ActiveLeaseSet, ActiveLeaseToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.publicationErrorLocked(); err != nil {
		return nil, ActiveLeaseToken{}, err
	}
	if s.nextActiveLeaseSet == ^uint64(0) {
		return nil, ActiveLeaseToken{}, errors.New("HTTP active lease identity exhausted")
	}
	s.nextActiveLeaseSet++
	set := &ActiveLeaseSet{store: s, id: s.nextActiveLeaseSet, initial: &activeLeaseSeal{}}
	token := ActiveLeaseToken{
		source: s.revisionSource,
		setID:  set.id,
		seal:   set.initial,
	}
	return set, token, nil
}

// BeginActiveLeases captures relevant changes and the exact commit fence.
func (s *ActiveLeaseSet) BeginActiveLeases(token ActiveLeaseToken) (*ActiveLeaseSnapshot, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("HTTP active lease set is unavailable")
	}
	store := s.store
	store.mu.RLock()
	defer store.mu.RUnlock()
	if err := store.validateActiveLeaseTokenLocked(s, token); err != nil {
		return nil, err
	}
	state := store.activeLeaseSets[s.id]
	leases := iradix.New[activeLeaseValue]()
	var replay *AcceptedReplayState
	changeRevision := ActiveLeaseRevision(0)
	var changes []ActiveLeaseChange
	if state != nil {
		leases = state.leases
		replay = state.replay
		changeRevision = state.changeRevision
		changes = make([]ActiveLeaseChange, 0, len(state.pending))
		for _, change := range state.pending {
			changes = append(changes, change)
		}
		slices.SortFunc(changes, compareActiveLeaseChanges)
	}
	seal := &activeLeaseSnapshotSeal{
		set:             s,
		token:           token,
		changeRevision:  changeRevision,
		leaseRoot:       leases.Root(),
		replay:          replay,
		pendingChangeCt: len(changes),
	}
	return &ActiveLeaseSnapshot{
		set:            s,
		token:          token,
		changeRevision: changeRevision,
		leases:         leases,
		replay:         replay,
		changes:        changes,
		seal:           seal,
	}, nil
}

func compareActiveLeaseChanges(left, right ActiveLeaseChange) int {
	if compared := strings.Compare(left.URL, right.URL); compared != 0 {
		return compared
	}
	return left.Descriptor.Compare(right.Descriptor)
}

func (s *HTTPStore) validateActiveLeaseTokenLocked(
	set *ActiveLeaseSet,
	token ActiveLeaseToken,
) error {
	if set == nil || set.store != s || token.source != s.revisionSource || token.setID != set.id {
		return errors.New("HTTP active lease token belongs to another authority")
	}
	state := s.activeLeaseSets[set.id]
	if state == nil {
		if token.generation != 0 || token.seal != set.initial {
			return errors.New("HTTP active lease token is not the current empty root")
		}
		return nil
	}
	if token != state.token {
		return errors.New("HTTP active lease token is stale or substituted")
	}
	return nil
}

func activeLeaseSnapshotSealIntact(s *HTTPStore, snapshot *ActiveLeaseSnapshot) bool {
	return snapshot != nil && snapshot.set != nil && snapshot.seal != nil &&
		snapshot.set.store == s && snapshot.seal.set == snapshot.set &&
		snapshot.token == snapshot.seal.token &&
		snapshot.changeRevision == snapshot.seal.changeRevision &&
		snapshot.leases != nil && snapshot.leases.Root() == snapshot.seal.leaseRoot &&
		snapshot.replay == snapshot.seal.replay &&
		len(snapshot.changes) == snapshot.seal.pendingChangeCt
}

func (s *HTTPStore) activeLeaseSnapshotCurrentLocked(
	snapshot *ActiveLeaseSnapshot,
	state *activeLeaseState,
) bool {
	currentRevision := ActiveLeaseRevision(0)
	pendingCount := 0
	if state != nil {
		currentRevision = state.changeRevision
		pendingCount = len(state.pending)
	}
	rootMatches := state != nil && snapshot.leases.Root() == state.leases.Root() ||
		state == nil && snapshot.leases.Len() == 0
	replayMatches := state != nil && acceptedReplayLeaseRootsMatch(snapshot.replay, state.replay, s) ||
		state == nil && snapshot.replay == nil
	return snapshot.changeRevision == currentRevision && rootMatches &&
		replayMatches && len(snapshot.changes) == pendingCount
}

func (s *HTTPStore) validateActiveLeaseSnapshotLocked(
	snapshot *ActiveLeaseSnapshot,
) (*activeLeaseState, error) {
	if !activeLeaseSnapshotSealIntact(s, snapshot) {
		return nil, errors.New("HTTP active lease snapshot is invalid or substituted")
	}
	if err := s.validateActiveLeaseTokenLocked(snapshot.set, snapshot.token); err != nil {
		return nil, err
	}
	state := s.activeLeaseSets[snapshot.set.id]
	if !s.activeLeaseSnapshotCurrentLocked(snapshot, state) {
		return nil, errors.New("leased HTTP content changed while the render was running")
	}
	return state, nil
}

func acceptedReplayLeaseRootsMatch(
	left, right *AcceptedReplayState,
	store *HTTPStore,
) bool {
	if left == right {
		return true
	}
	return left != nil && right != nil && left.ValidateAuthentication() == nil &&
		right.ValidateAuthentication() == nil && left.store == store && right.store == store &&
		left.source == right.source && left.root == right.root
}

type preparedActiveLeasePlan struct {
	set             *ActiveLeaseSet
	base            *activeLeaseState
	leases          *iradix.Tree[activeLeaseValue]
	replay          *AcceptedReplayState
	publishedReplay []ContentSnapshot
	token           ActiveLeaseToken
	transition      ActiveLeaseTransition
	changed         bool
}

func (s *HTTPStore) validatePreparedActiveLeasePlanLocked(plan *preparedActiveLeasePlan) error {
	if plan == nil {
		return nil
	}
	if plan.set == nil || plan.set.store != s || plan.set.id == 0 || plan.leases == nil ||
		plan.token.source != s.revisionSource || plan.token.setID != plan.set.id || plan.token.seal == nil ||
		s.activeLeaseSets[plan.set.id] != plan.base {
		return errors.New("prepared HTTP active lease publication is invalid")
	}
	current := iradix.New[activeLeaseValue]()
	if plan.base != nil {
		if plan.base.leases == nil || plan.base.pending == nil {
			return errors.New("prepared HTTP active lease base is invalid")
		}
		current = plan.base.leases
	}
	expectedTransition := compareActiveLeaseRoots(current, plan.leases)
	if !slices.Equal(expectedTransition.Activated, plan.transition.Activated) ||
		!slices.Equal(expectedTransition.Retired, plan.transition.Retired) {
		return errors.New("prepared HTTP active lease transition is invalid")
	}
	if err := s.validateActiveLeaseURLIndexLocked(plan.set.id, current); err != nil {
		return err
	}
	if plan.replay != nil && (plan.replay.ValidateAuthentication() != nil || plan.replay.store != s) {
		return errors.New("prepared HTTP active replay lease is invalid")
	}
	return s.validatePreparedActiveLeaseActivationsLocked(plan)
}

func (s *HTTPStore) validateActiveLeaseURLIndexLocked(
	setID uint64,
	current *iradix.Tree[activeLeaseValue],
) error {
	var indexErr error
	current.Root().Walk(func(key []byte, value activeLeaseValue) bool {
		descriptor, indexed := s.activeLeaseURLs[string(key)][setID]
		if value.references == 0 || !indexed || descriptor != value.descriptor {
			indexErr = errors.New("prepared HTTP active lease URL index is inconsistent")
			return true
		}
		return false
	})
	if indexErr != nil {
		return indexErr
	}
	for url, descriptorBySet := range s.activeLeaseURLs {
		descriptor, indexed := descriptorBySet[setID]
		if !indexed {
			continue
		}
		value, found := current.Get([]byte(url))
		if !found || value.references == 0 || value.descriptor != descriptor {
			return errors.New("prepared HTTP active lease URL index is inconsistent")
		}
	}
	return nil
}

func (s *HTTPStore) validatePreparedActiveLeaseActivationsLocked(plan *preparedActiveLeasePlan) error {
	for _, url := range plan.transition.Activated {
		value, found := plan.leases.Get([]byte(url))
		if !found || value.references == 0 {
			return errors.New("prepared HTTP active lease activation is invalid")
		}
		for setID, descriptor := range s.activeLeaseURLs[url] {
			if setID != plan.set.id && descriptor != value.descriptor {
				return fmt.Errorf("HTTP source %s has conflicting active declarations", url)
			}
		}
	}
	return nil
}

func (s *HTTPStore) validateActiveLeaseChangeCapacityLocked(
	sources []preparedSourcePlan,
	candidates []*InitialCandidate,
) error {
	changes := make(map[uint64]uint64)
	for index := range sources {
		source := sources[index].source
		if source.Changed() {
			if err := s.countActiveLeaseChangesLocked(changes, source.url, source.baseDescriptor, source.spec.descriptor); err != nil {
				return err
			}
		}
	}
	for _, candidate := range candidates {
		if err := s.countActiveLeaseChangesLocked(changes, candidate.url, candidate.sourceDescriptor, candidate.sourceDescriptor); err != nil {
			return err
		}
	}
	return nil
}

func (s *HTTPStore) countActiveLeaseChangesLocked(
	changes map[uint64]uint64,
	url string,
	previous, next SourceDescriptor,
) error {
	for setID, descriptor := range s.activeLeaseURLs[url] {
		if descriptor != previous && descriptor != next {
			continue
		}
		state := s.activeLeaseSets[setID]
		if state == nil || state.pending == nil {
			return errors.New("HTTP active lease URL index is inconsistent")
		}
		changes[setID]++
		if uint64(state.changeRevision) > ^uint64(0)-changes[setID] {
			return errors.New("HTTP active lease revision exhausted")
		}
	}
	return nil
}

func (s *HTTPStore) planActiveLeaseCommitLocked(
	commit *ActiveLeaseCommit,
) (*preparedActiveLeasePlan, error) {
	if len(commit.PublishedReplay) != 0 {
		return nil, errors.New("published HTTP replay leases require a prepared input publication")
	}
	state, current, currentReplay, err := s.activeLeaseCommitBaseLocked(commit)
	if err != nil {
		return nil, err
	}
	nextReplay := commit.Replay
	if nextReplay != nil {
		if acceptedReplayLeaseRootsMatch(nextReplay, currentReplay, s) &&
			currentReplay.ReplayWatermark() > nextReplay.ReplayWatermark() {
			nextReplay = currentReplay
		}
		var ok bool
		nextReplay, ok = s.advanceAcceptedReplayStateLocked(nextReplay)
		if !ok {
			return nil, errors.New("accepted HTTP replay lease changed before publication")
		}
	}
	next := current
	replayBase := currentReplay
	if commit.Replace {
		next, err = buildActiveLeaseReplacement(commit.Replacement)
		if err != nil {
			return nil, err
		}
		replayBase = nil
	}
	updates, err := acceptedReplayLeaseUpdates(replayBase, nextReplay)
	if err != nil {
		return nil, err
	}
	updates = append(updates, commit.Updates...)
	next, err = applyActiveLeaseUpdates(next, updates)
	if err != nil {
		return nil, err
	}
	transition := compareActiveLeaseRoots(current, next)
	if err := s.validatePlannedActivationsLocked(commit.Snapshot.set.id, next, transition.Activated); err != nil {
		return nil, err
	}
	rootChanged := current.Root() != next.Root()
	changed := rootChanged || currentReplay != nextReplay || len(commit.Snapshot.changes) != 0
	token := commit.Snapshot.token
	if rootChanged || len(commit.Snapshot.changes) != 0 {
		if token.generation == ^uint64(0) {
			return nil, errors.New("HTTP active lease generation exhausted")
		}
		token.generation++
		token.seal = &activeLeaseSeal{}
	}
	return &preparedActiveLeasePlan{
		set:        commit.Snapshot.set,
		base:       state,
		leases:     next,
		replay:     nextReplay,
		token:      token,
		transition: transition,
		changed:    changed,
	}, nil
}

func (s *HTTPStore) activeLeaseCommitBaseLocked(commit *ActiveLeaseCommit) (
	state *activeLeaseState,
	current *iradix.Tree[activeLeaseValue],
	currentReplay *AcceptedReplayState,
	err error,
) {
	state, err = s.validateActiveLeaseSnapshotLocked(commit.Snapshot)
	if err != nil {
		return nil, nil, nil, err
	}
	if commit.Replace && len(commit.Updates) != 0 {
		return nil, nil, nil, errors.New("HTTP active lease replacement cannot include deltas")
	}
	if !commit.Replace && len(commit.Replacement) != 0 {
		return nil, nil, nil, errors.New("HTTP active lease deltas cannot include a replacement")
	}
	current = commit.Snapshot.leases
	currentReplay = commit.Snapshot.replay
	if state != nil {
		current = state.leases
		currentReplay = state.replay
	}
	return state, current, currentReplay, nil
}

func (s *HTTPStore) validatePlannedActivationsLocked(
	setID uint64,
	next *iradix.Tree[activeLeaseValue],
	activated []string,
) error {
	for _, url := range activated {
		value, found := next.Get([]byte(url))
		if !found {
			return errors.New("HTTP active lease transition is inconsistent")
		}
		for otherSet, descriptor := range s.activeLeaseURLs[url] {
			if otherSet != setID && descriptor != value.descriptor {
				return fmt.Errorf("HTTP source %s has conflicting active declarations", url)
			}
		}
	}
	return nil
}

func (s *HTTPStore) planPublishedReplayActiveLeaseLocked(
	commit *ActiveLeaseCommit,
	snapshots []ContentSnapshot,
) (*preparedActiveLeasePlan, error) {
	if commit == nil || commit.Replay != nil || len(commit.PublishedReplay) != 0 {
		return nil, errors.New("published HTTP replay lease has an invalid transition")
	}
	state, current, currentReplay, err := s.activeLeaseCommitBaseLocked(commit)
	if err != nil {
		return nil, err
	}
	next := current
	replayBase := currentReplay
	if commit.Replace {
		next, err = buildActiveLeaseReplacement(commit.Replacement)
		if err != nil {
			return nil, err
		}
		replayBase = nil
	}
	next, err = applyActiveLeaseUpdates(next, commit.Updates)
	if err != nil {
		return nil, err
	}
	updates, err := acceptedReplayLeaseUpdatesFromSnapshots(replayBase, snapshots)
	if err != nil {
		return nil, err
	}
	next, err = applyActiveLeaseUpdates(next, updates)
	if err != nil {
		return nil, err
	}
	transition := compareActiveLeaseRoots(current, next)
	if err := s.validatePlannedActivationsLocked(commit.Snapshot.set.id, next, transition.Activated); err != nil {
		return nil, err
	}
	token := commit.Snapshot.token
	if token.generation == ^uint64(0) {
		return nil, errors.New("HTTP active lease generation exhausted")
	}
	token.generation++
	token.seal = &activeLeaseSeal{}
	return &preparedActiveLeasePlan{
		set: commit.Snapshot.set, base: state, leases: next,
		publishedReplay: slices.Clone(snapshots), token: token, transition: transition, changed: true,
	}, nil
}

func acceptedReplayLeaseUpdatesFromSnapshots(
	current *AcceptedReplayState,
	snapshots []ContentSnapshot,
) ([]ActiveLeaseUpdate, error) {
	if current != nil && current.ValidateAuthentication() != nil {
		return nil, errors.New("accepted HTTP replay lease has invalid provenance")
	}
	desired := make(map[string]SourceDescriptor, len(snapshots))
	for index := range snapshots {
		snapshot := &snapshots[index]
		if snapshot.URL == "" || !snapshot.Found || !snapshot.Cacheable ||
			snapshot.Token.Kind() != SnapshotAccepted || snapshot.Token.URL() != snapshot.URL ||
			snapshot.Token.SourceDescriptor() != snapshot.Descriptor {
			return nil, errors.New("published HTTP replay lease has an invalid snapshot")
		}
		if _, exists := desired[snapshot.URL]; exists {
			return nil, fmt.Errorf("published HTTP replay lease duplicates source %s", snapshot.URL)
		}
		desired[snapshot.URL] = snapshot.Descriptor
	}
	updates := make([]ActiveLeaseUpdate, 0, len(desired))
	if current != nil {
		current.entries.Root().Walk(func(key []byte, entry acceptedReplayStateEntry) bool {
			url := string(key)
			if descriptor, found := desired[url]; found && descriptor == entry.snapshot.Descriptor {
				delete(desired, url)
				return false
			}
			updates = append(updates, ActiveLeaseUpdate{
				URL: url, Descriptor: entry.snapshot.Descriptor, Removed: 1,
			})
			return false
		})
	}
	for url, descriptor := range desired {
		updates = append(updates, ActiveLeaseUpdate{URL: url, Descriptor: descriptor, Added: 1})
	}
	return updates, nil
}

func acceptedReplayLeaseUpdates(
	current *AcceptedReplayState,
	next *AcceptedReplayState,
) ([]ActiveLeaseUpdate, error) {
	if current == next || current != nil && next != nil && current.root == next.root {
		return nil, nil
	}
	if current != nil && current.ValidateAuthentication() != nil ||
		next != nil && next.ValidateAuthentication() != nil {
		return nil, errors.New("accepted HTTP replay lease has invalid provenance")
	}
	updates := make([]ActiveLeaseUpdate, 0)
	if current != nil {
		current.entries.Root().Walk(func(key []byte, entry acceptedReplayStateEntry) bool {
			if nextEntry, found := acceptedReplayEntry(next, key); found &&
				nextEntry.snapshot.Descriptor == entry.snapshot.Descriptor {
				return false
			}
			updates = append(updates, ActiveLeaseUpdate{
				URL: string(key), Descriptor: entry.snapshot.Descriptor, Removed: 1,
			})
			return false
		})
	}
	if next != nil {
		next.entries.Root().Walk(func(key []byte, entry acceptedReplayStateEntry) bool {
			if currentEntry, found := acceptedReplayEntry(current, key); found &&
				currentEntry.snapshot.Descriptor == entry.snapshot.Descriptor {
				return false
			}
			updates = append(updates, ActiveLeaseUpdate{
				URL: string(key), Descriptor: entry.snapshot.Descriptor, Added: 1,
			})
			return false
		})
	}
	return updates, nil
}

func acceptedReplayEntry(
	state *AcceptedReplayState,
	key []byte,
) (acceptedReplayStateEntry, bool) {
	if state == nil {
		return acceptedReplayStateEntry{}, false
	}
	return state.entries.Get(key)
}

func buildActiveLeaseReplacement(
	references []ActiveLeaseReference,
) (*iradix.Tree[activeLeaseValue], error) {
	ordered := slices.Clone(references)
	slices.SortFunc(ordered, func(left, right ActiveLeaseReference) int {
		if compared := strings.Compare(left.URL, right.URL); compared != 0 {
			return compared
		}
		return left.Descriptor.Compare(right.Descriptor)
	})
	txn := iradix.New[activeLeaseValue]().Txn()
	for index := range ordered {
		reference := &ordered[index]
		if reference.URL == "" || reference.References == 0 {
			return nil, errors.New("HTTP active lease replacement has an invalid reference")
		}
		if _, exists := txn.Get([]byte(reference.URL)); exists {
			return nil, fmt.Errorf("HTTP source %s has conflicting active declarations", reference.URL)
		}
		txn.Insert([]byte(reference.URL), activeLeaseValue{
			descriptor: reference.Descriptor,
			references: reference.References,
		})
	}
	return txn.Commit(), nil
}

func applyActiveLeaseUpdates(
	current *iradix.Tree[activeLeaseValue],
	updates []ActiveLeaseUpdate,
) (*iradix.Tree[activeLeaseValue], error) {
	if len(updates) == 0 {
		return current, nil
	}
	byURL := make(map[string][]ActiveLeaseUpdate, len(updates))
	for index := range updates {
		update := updates[index]
		if update.URL == "" || update.Added == 0 && update.Removed == 0 {
			return nil, errors.New("HTTP active lease delta is invalid")
		}
		byURL[update.URL] = append(byURL[update.URL], update)
	}
	urls := make([]string, 0, len(byURL))
	for url := range byURL {
		urls = append(urls, url)
	}
	slices.Sort(urls)
	txn := current.Txn()
	treeChanged := false
	for _, url := range urls {
		changed, err := applyActiveLeaseURLUpdates(txn, current, url, byURL[url])
		if err != nil {
			return nil, err
		}
		treeChanged = treeChanged || changed
	}
	if !treeChanged {
		return current, nil
	}
	return txn.Commit(), nil
}

func applyActiveLeaseURLUpdates(
	txn *iradix.Txn[activeLeaseValue],
	current *iradix.Tree[activeLeaseValue],
	url string,
	updates []ActiveLeaseUpdate,
) (changed bool, err error) {
	value, found := current.Get([]byte(url))
	counts := map[SourceDescriptor]uint64{}
	if found {
		counts[value.descriptor] = value.references
	}
	for _, update := range updates {
		count := counts[update.Descriptor]
		if count < update.Removed || ^uint64(0)-(count-update.Removed) < update.Added {
			return false, fmt.Errorf("HTTP source %s active reference count is inconsistent", url)
		}
		count = count - update.Removed + update.Added
		if count == 0 {
			delete(counts, update.Descriptor)
		} else {
			counts[update.Descriptor] = count
		}
	}
	if len(counts) > 1 {
		return false, fmt.Errorf("HTTP source %s has conflicting active declarations", url)
	}
	if len(counts) == 0 {
		if found {
			txn.Delete([]byte(url))
			return true, nil
		}
		return false, nil
	}
	for descriptor, references := range counts {
		if found && value.descriptor == descriptor && value.references == references {
			continue
		}
		txn.Insert([]byte(url), activeLeaseValue{descriptor: descriptor, references: references})
		changed = true
	}
	return changed, nil
}

func compareActiveLeaseRoots(
	current, next *iradix.Tree[activeLeaseValue],
) ActiveLeaseTransition {
	transition := ActiveLeaseTransition{}
	current.Root().Walk(func(key []byte, value activeLeaseValue) bool {
		nextValue, found := next.Get(key)
		if !found || nextValue.descriptor != value.descriptor {
			transition.Retired = append(transition.Retired, string(key))
		}
		return false
	})
	next.Root().Walk(func(key []byte, value activeLeaseValue) bool {
		currentValue, found := current.Get(key)
		if !found || currentValue.descriptor != value.descriptor {
			transition.Activated = append(transition.Activated, string(key))
		}
		return false
	})
	return transition
}

func planSnapshotRevision(plan *preparedActiveLeasePlan) ActiveLeaseRevision {
	if plan == nil || plan.set == nil {
		return 0
	}
	if plan.base != nil {
		return plan.base.changeRevision
	}
	return 0
}

func (s *HTTPStore) unregisterActiveLeaseRootLocked(
	setID uint64,
	root *iradix.Tree[activeLeaseValue],
) {
	root.Root().Walk(func(key []byte, _ activeLeaseValue) bool {
		s.unregisterActiveLeaseURLLocked(setID, string(key))
		return false
	})
}

func (s *HTTPStore) unregisterActiveLeaseURLLocked(setID uint64, url string) {
	sets := s.activeLeaseURLs[url]
	delete(sets, setID)
	if len(sets) == 0 {
		delete(s.activeLeaseURLs, url)
	}
}

func (s *HTTPStore) recordActiveLeaseChangeLocked(
	url string,
	previous, next SourceDescriptor,
) {
	for setID, descriptor := range s.activeLeaseURLs[url] {
		if descriptor != previous && descriptor != next {
			continue
		}
		state := s.activeLeaseSets[setID]
		if state == nil {
			panic("HTTP active lease URL index is inconsistent")
		}
		if state.changeRevision == ActiveLeaseRevision(^uint64(0)) {
			panic("HTTP active lease revision exhausted")
		}
		state.changeRevision++
		state.pending[url] = ActiveLeaseChange{
			URL:        url,
			Descriptor: descriptor,
			Revision:   state.changeRevision,
		}
	}
}

func activeLeasePlanReferencesTransition(
	plan *preparedActiveLeasePlan,
	url string,
	previous, next SourceDescriptor,
) bool {
	if plan == nil || plan.leases == nil {
		return false
	}
	value, found := plan.leases.Get([]byte(url))
	return found && (value.descriptor == previous || value.descriptor == next)
}

// HasActiveLease reports whether any cache currently leases the URL.
func (s *HTTPStore) HasActiveLease(url string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.publicationErrorLocked() != nil {
		return false
	}
	return len(s.activeLeaseURLs[url]) != 0
}

// RetireActiveLeases removes a complete cache lease set.
func (s *ActiveLeaseSet) RetireActiveLeases(token ActiveLeaseToken) ([]string, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("HTTP active lease set is unavailable")
	}
	store := s.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.validateActiveLeaseTokenLocked(s, token); err != nil {
		return nil, err
	}
	state := store.activeLeaseSets[s.id]
	if state == nil {
		return nil, nil
	}
	urls := make([]string, 0, state.leases.Len())
	state.leases.Root().Walk(func(key []byte, _ activeLeaseValue) bool {
		urls = append(urls, string(key))
		return false
	})
	store.unregisterActiveLeaseRootLocked(s.id, state.leases)
	delete(store.activeLeaseSets, s.id)
	return urls, nil
}
