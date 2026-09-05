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
	"maps"
	"slices"
	"time"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
)

type preparedHTTPCacheRoot struct {
	entries map[string]*CacheEntry
	seal    *preparedHTTPCacheRoot
}

type preparedHTTPReplayJournalRoot struct {
	entries []ReplayChange
	start   int
	seal    *preparedHTTPReplayJournalRoot
}

type preparedHTTPSemanticJournalRoot struct {
	entries []SemanticChange
	start   int
	seal    *preparedHTTPSemanticJournalRoot
}

type preparedHTTPActiveLeaseRoot struct {
	sets map[uint64]*activeLeaseState
	urls map[string]map[uint64]SourceDescriptor
	seal *preparedHTTPActiveLeaseRoot
}

type preparedHTTPCacheEntryAuthentication struct {
	entry       *CacheEntry
	value       CacheEntry
	authPointer *AuthConfig
}

type preparedHTTPActiveLeaseStateAuthentication struct {
	state          *activeLeaseState
	token          ActiveLeaseToken
	leases         *iradix.Tree[activeLeaseValue]
	leaseRoot      *iradix.Node[activeLeaseValue]
	leaseCount     int
	replay         *AcceptedReplayState
	changeRevision ActiveLeaseRevision
	pending        map[string]ActiveLeaseChange
}

type preparedHTTPStateAuthentication struct {
	cache                 map[string]*preparedHTTPCacheEntryAuthentication
	nextPendingRevision   uint64
	nextCandidateRevision Revision
	nextSourceGeneration  uint64
	semanticRevision      Revision
	replayRevision        Revision
	replayJournal         []ReplayChange
	replayJournalStart    int
	semanticJournal       []SemanticChange
	semanticJournalStart  int
	activeSets            map[uint64]preparedHTTPActiveLeaseStateAuthentication
	activeURLs            map[string]map[uint64]SourceDescriptor
	nextActiveLeaseSet    uint64
}

type preparedHTTPStoreRoots struct {
	cache                 map[string]*CacheEntry
	nextPendingRevision   uint64
	nextCandidateRevision Revision
	nextSourceGeneration  uint64
	semanticRevision      Revision
	replayRevision        Revision
	replayJournal         []ReplayChange
	replayJournalStart    int
	semanticJournal       []SemanticChange
	semanticJournalStart  int
	activeSets            map[uint64]*activeLeaseState
	activeURLs            map[string]map[uint64]SourceDescriptor
	nextActiveLeaseSet    uint64
}

type preparedHTTPRollbackCheckpoint struct {
	store     *HTTPStore
	authority chan struct{}
	roots     preparedHTTPStoreRoots
	auth      preparedHTTPStateAuthentication
	seal      *preparedHTTPRollbackCheckpoint
}

type preparedHTTPStorePublicationAuthentication struct {
	owner                 *PreparedInitialCandidateCommit
	store                 *HTTPStore
	cache                 *preparedHTTPCacheRoot
	nextPendingRevision   uint64
	nextCandidateRevision Revision
	nextSourceGeneration  uint64
	semanticRevision      Revision
	replayRevision        Revision
	replayJournal         *preparedHTTPReplayJournalRoot
	semanticJournal       *preparedHTTPSemanticJournalRoot
	active                *preparedHTTPActiveLeaseRoot
	nextActiveLeaseSet    uint64
	revisionSource        SourceID
	prepareAuthority      chan struct{}
	replayCapacity        int
	semanticCapacity      int
	base                  preparedHTTPStateAuthentication
	future                preparedHTTPStateAuthentication
}

type preparedHTTPStorePublication struct {
	owner                 *PreparedInitialCandidateCommit
	store                 *HTTPStore
	cache                 *preparedHTTPCacheRoot
	nextPendingRevision   uint64
	nextCandidateRevision Revision
	nextSourceGeneration  uint64
	semanticRevision      Revision
	replayRevision        Revision
	replayJournal         *preparedHTTPReplayJournalRoot
	semanticJournal       *preparedHTTPSemanticJournalRoot
	active                *preparedHTTPActiveLeaseRoot
	nextActiveLeaseSet    uint64
	base                  preparedHTTPStoreRoots
	auth                  preparedHTTPStorePublicationAuthentication
	seal                  *preparedHTTPStorePublication
}

func (s *HTTPStore) validatePublicationBaseLocked() error {
	if s == nil || s.cache == nil || s.activeLeaseSets == nil || s.activeLeaseURLs == nil ||
		s.revisionSource == 0 || s.prepareAuthority == nil || cap(s.prepareAuthority) != 1 ||
		len(s.prepareAuthority) != 0 {
		return errors.New("HTTP store publication state is invalid")
	}
	for url, entry := range s.cache {
		if err := validatePreparedCacheEntry(
			url, entry, s.nextPendingRevision, s.nextSourceGeneration,
			s.semanticRevision, s.replayRevision,
		); err != nil {
			return err
		}
	}
	if err := validateReplayJournal(
		s.replayJournal, s.replayJournalStart, s.replayJournalCapacity,
		s.replayRevision, s.revisionSource,
	); err != nil {
		return err
	}
	if err := validateSemanticJournal(
		s.semanticJournal, s.semanticJournalStart, s.semanticJournalCapacity,
		s.semanticRevision, s.revisionSource,
	); err != nil {
		return err
	}
	return s.validateActiveLeaseIndexLocked()
}

func validatePreparedCacheEntry(
	url string,
	entry *CacheEntry,
	nextPendingRevision uint64,
	nextSourceGeneration uint64,
	semanticRevision Revision,
	replayRevision Revision,
) error {
	if url == "" || entry == nil || entry.URL != url || entry.sourceGeneration == 0 ||
		entry.sourceGeneration > nextSourceGeneration || entry.sourceIdentity != entry.sourceDescriptor.Identity() ||
		entry.acceptedRevision > semanticRevision || entry.replayRevision > uint64(replayRevision) {
		return errors.New("prepared HTTP cache entry is invalid")
	}
	switch entry.ValidationState {
	case StateAccepted, StateRejected:
		if entry.HasPending {
			return errors.New("prepared HTTP cache entry has inconsistent validation state")
		}
	case StateValidating:
		if !entry.HasPending {
			return errors.New("prepared HTTP cache entry has inconsistent validation state")
		}
	default:
		return errors.New("prepared HTTP cache entry has an invalid validation state")
	}
	if err := validatePreparedPendingVersion(entry, nextPendingRevision); err != nil {
		return err
	}
	if err := validatePreparedAcceptedVersion(entry); err != nil {
		return err
	}
	return validatePreparedSourcePolicy(entry)
}

func validatePreparedPendingVersion(entry *CacheEntry, nextPendingRevision uint64) error {
	if entry.HasPending {
		if entry.PendingChecksum == "" || entry.PendingRevision == 0 ||
			entry.PendingRevision > nextPendingRevision || entry.ValidationStartedAt.IsZero() ||
			entry.PendingChecksum != checksum(entry.PendingContent) {
			return errors.New("prepared HTTP cache entry has an invalid pending version")
		}
	} else if entry.PendingContent != "" || entry.PendingChecksum != "" || entry.PendingRevision != 0 ||
		!entry.ValidationStartedAt.IsZero() {
		return errors.New("prepared HTTP cache entry retains an inactive pending version")
	}
	return nil
}

func validatePreparedAcceptedVersion(entry *CacheEntry) error {
	if entry.AcceptedChecksum == "" {
		if entry.AcceptedContent != "" || !entry.AcceptedTime.IsZero() || entry.acceptedRevision != 0 {
			return errors.New("prepared HTTP cache entry has an invalid accepted version")
		}
	} else if entry.AcceptedTime.IsZero() || entry.acceptedRevision == 0 ||
		entry.AcceptedChecksum != checksum(entry.AcceptedContent) {
		return errors.New("prepared HTTP cache entry has an incomplete accepted version")
	}
	return nil
}

func validatePreparedSourcePolicy(entry *CacheEntry) error {
	if entry.fixture {
		if entry.sourceDescriptor != (SourceDescriptor{}) || entry.sourceIdentity != "" ||
			entry.Options != (FetchOptions{}) || entry.Auth != nil {
			return errors.New("prepared HTTP fixture has an invalid source policy")
		}
		return nil
	}
	spec, err := normalizeSource(entry.Options, entry.Auth)
	if err != nil || spec.options != entry.Options || spec.descriptor != entry.sourceDescriptor ||
		!sameAuthConfig(spec.auth, entry.Auth) {
		return errors.New("prepared HTTP cache entry has an invalid source policy")
	}
	return nil
}

func validateReplayJournal(
	entries []ReplayChange,
	start, capacity int,
	current Revision,
	source SourceID,
) error {
	if !validJournalShape(len(entries), start, capacity) {
		return errors.New("HTTP replay journal state is invalid")
	}
	if len(entries) == 0 {
		if current != 0 && capacity != 0 {
			return errors.New("HTTP replay journal state is incomplete")
		}
		return nil
	}
	if current < Revision(len(entries)) {
		return errors.New("HTTP replay journal revision is invalid")
	}
	first := current - Revision(len(entries)) + 1
	for index := range entries {
		entry := entries[(start+index)%len(entries)]
		if entry.Revision != first+Revision(index) || entry.URL == "" ||
			entry.authentication != authenticateReplayChange(source, &entry) {
			return errors.New("HTTP replay journal contents are invalid")
		}
	}
	return nil
}

func validateSemanticJournal(
	entries []SemanticChange,
	start int,
	capacity int,
	current Revision,
	source SourceID,
) error {
	if !validJournalShape(len(entries), start, capacity) {
		return errors.New("HTTP semantic journal state is invalid")
	}
	if len(entries) == 0 {
		if current != 0 && capacity != 0 {
			return errors.New("HTTP semantic journal state is incomplete")
		}
		return nil
	}
	if current < Revision(len(entries)) {
		return errors.New("HTTP semantic journal revision is invalid")
	}
	first := current - Revision(len(entries)) + 1
	for index := range entries {
		entry := entries[(start+index)%len(entries)]
		if entry.Revision != first+Revision(index) || entry.URL == "" ||
			entry.PreviousSourceIdentity != entry.PreviousDescriptor.Identity() ||
			entry.SourceIdentity != entry.Descriptor.Identity() ||
			entry.Removed && entry.Descriptor != (SourceDescriptor{}) ||
			entry.authentication != authenticateSemanticChange(source, &entry) {
			return errors.New("HTTP semantic journal contents are invalid")
		}
	}
	return nil
}

func authenticateSemanticChange(source SourceID, change *SemanticChange) uint64 {
	auth := newHTTPJournalAuthenticator(source)
	auth.addUint64(uint64(change.Revision))
	auth.addString(change.URL)
	auth.addString(change.PreviousDescriptor.identity)
	auth.addString(change.PreviousDescriptor.canonical)
	auth.addString(change.Descriptor.identity)
	auth.addString(change.Descriptor.canonical)
	if change.Removed {
		auth.addByte(1)
	} else {
		auth.addByte(0)
	}
	return uint64(auth)
}

func authenticateReplayChange(source SourceID, change *ReplayChange) uint64 {
	auth := newHTTPJournalAuthenticator(source)
	auth.addUint64(uint64(change.Revision))
	auth.addString(change.URL)
	return uint64(auth)
}

type httpJournalAuthenticator uint64

func newHTTPJournalAuthenticator(source SourceID) httpJournalAuthenticator {
	return httpJournalAuthenticator(uint64(14695981039346656037) ^ uint64(source))
}

func (a *httpJournalAuthenticator) addByte(value byte) {
	*a ^= httpJournalAuthenticator(value)
	*a *= 1099511628211
}

func (a *httpJournalAuthenticator) addUint64(value uint64) {
	for shift := uint(0); shift < 64; shift += 8 {
		a.addByte(byte(value >> shift & 0xff))
	}
}

func (a *httpJournalAuthenticator) addString(value string) {
	a.addUint64(uint64(len(value)))
	for index := range len(value) {
		a.addByte(value[index])
	}
}

func validJournalShape(length, start, capacity int) bool {
	if capacity < 0 || length < 0 || length > capacity {
		return false
	}
	if length == 0 {
		return start == 0
	}
	if length < capacity {
		return start == 0
	}
	return start >= 0 && start < length
}

func (s *HTTPStore) validateActiveLeaseIndexLocked() error {
	return validatePreparedActiveLeaseState(
		s, s.activeLeaseSets, s.activeLeaseURLs, s.nextActiveLeaseSet,
	)
}

func validatePreparedActiveLeaseState(
	store *HTTPStore,
	sets map[uint64]*activeLeaseState,
	urls map[string]map[uint64]SourceDescriptor,
	nextActiveLeaseSet uint64,
) error {
	if store == nil || sets == nil || urls == nil {
		return errors.New("HTTP active lease state is invalid")
	}
	for setID, state := range sets {
		if err := validatePreparedActiveLeaseSet(store, urls, setID, state, nextActiveLeaseSet); err != nil {
			return err
		}
	}
	for url, descriptorBySet := range urls {
		if err := validatePreparedActiveLeaseURLEntry(sets, url, descriptorBySet); err != nil {
			return err
		}
	}
	return nil
}

func validatePreparedActiveLeaseSet(
	store *HTTPStore,
	urls map[string]map[uint64]SourceDescriptor,
	setID uint64,
	state *activeLeaseState,
	nextActiveLeaseSet uint64,
) error {
	if err := validatePreparedActiveLeaseSetIdentity(store, setID, state, nextActiveLeaseSet); err != nil {
		return err
	}
	var walkErr error
	state.leases.Root().Walk(func(key []byte, value activeLeaseValue) bool {
		url := string(key)
		descriptor, indexed := urls[url][setID]
		if url == "" || value.references == 0 || !indexed || descriptor != value.descriptor {
			walkErr = errors.New("HTTP active lease URL index is inconsistent")
			return true
		}
		return false
	})
	if walkErr != nil {
		return walkErr
	}
	return validatePreparedActiveLeasePending(state)
}

func validatePreparedActiveLeaseSetIdentity(
	store *HTTPStore,
	setID uint64,
	state *activeLeaseState,
	nextActiveLeaseSet uint64,
) error {
	if setID == 0 || setID > nextActiveLeaseSet || state == nil || state.leases == nil ||
		state.pending == nil || state.token.source != store.revisionSource ||
		state.token.setID != setID || state.token.generation == 0 || state.token.seal == nil {
		return errors.New("HTTP active lease state is invalid")
	}
	if state.replay != nil &&
		(state.replay.ValidateAuthentication() != nil || state.replay.store != store) {
		return errors.New("HTTP active replay lease state is invalid")
	}
	return nil
}

func validatePreparedActiveLeasePending(state *activeLeaseState) error {
	for url, change := range state.pending {
		value, found := state.leases.Get([]byte(url))
		if url == "" || change.URL != url || change.Revision == 0 ||
			change.Revision > state.changeRevision || !found || change.Descriptor != value.descriptor {
			return errors.New("HTTP active lease pending state is invalid")
		}
	}
	return nil
}

func validatePreparedActiveLeaseURLEntry(
	sets map[uint64]*activeLeaseState,
	url string,
	descriptorBySet map[uint64]SourceDescriptor,
) error {
	if url == "" || len(descriptorBySet) == 0 {
		return errors.New("HTTP active lease URL index is invalid")
	}
	for setID, descriptor := range descriptorBySet {
		state := sets[setID]
		if state == nil || state.leases == nil {
			return errors.New("HTTP active lease URL index is inconsistent")
		}
		value, found := state.leases.Get([]byte(url))
		if !found || value.references == 0 || value.descriptor != descriptor {
			return errors.New("HTTP active lease URL index is inconsistent")
		}
	}
	return nil
}

func (c *PreparedInitialCandidateCommit) preparePublicationLocked() (
	*preparedHTTPStorePublication,
	error,
) {
	store := c.store
	publication := &preparedHTTPStorePublication{
		owner:                 c,
		store:                 store,
		cache:                 &preparedHTTPCacheRoot{entries: maps.Clone(store.cache)},
		nextPendingRevision:   store.nextPendingRevision,
		nextCandidateRevision: store.nextCandidateRevision,
		nextSourceGeneration:  store.nextSourceGeneration,
		semanticRevision:      store.semanticRevision,
		replayRevision:        store.replayRevision,
		replayJournal: &preparedHTTPReplayJournalRoot{
			entries: slices.Clone(store.replayJournal), start: store.replayJournalStart,
		},
		semanticJournal: &preparedHTTPSemanticJournalRoot{
			entries: slices.Clone(store.semanticJournal), start: store.semanticJournalStart,
		},
		active:             clonePreparedActiveLeaseRoot(store),
		nextActiveLeaseSet: store.nextActiveLeaseSet,
		base:               currentHTTPStoreRoots(store),
	}
	publication.cache.seal = publication.cache
	publication.replayJournal.seal = publication.replayJournal
	publication.semanticJournal.seal = publication.semanticJournal
	publication.active.seal = publication.active

	publishedReplay := c.active != nil && len(c.active.publishedReplay) != 0
	if !publishedReplay {
		publication.applyActiveLeasePlan(c.active)
	}
	if err := c.applyPreparedSourcePlans(publication); err != nil {
		return nil, err
	}
	if err := c.applyPreparedCandidates(publication); err != nil {
		return nil, err
	}
	if publishedReplay {
		publication.applyActiveLeasePlan(c.active)
	}
	if publication.semanticRevision != c.watermark {
		return nil, errors.New("prepared HTTP publication produced an invalid watermark")
	}
	base, err := capturePreparedHTTPStateAuthentication(store, &publication.base)
	if err != nil {
		return nil, err
	}
	futureRoots := publication.futureRoots()
	future, err := capturePreparedHTTPStateAuthentication(store, &futureRoots)
	if err != nil {
		return nil, err
	}
	publication.auth = preparedHTTPStorePublicationAuthentication{
		owner: publication.owner, store: publication.store, cache: publication.cache,
		nextPendingRevision:   publication.nextPendingRevision,
		nextCandidateRevision: publication.nextCandidateRevision,
		nextSourceGeneration:  publication.nextSourceGeneration,
		semanticRevision:      publication.semanticRevision, replayRevision: publication.replayRevision,
		replayJournal: publication.replayJournal, semanticJournal: publication.semanticJournal,
		active: publication.active, nextActiveLeaseSet: publication.nextActiveLeaseSet,
		revisionSource:   store.revisionSource,
		prepareAuthority: store.prepareAuthority,
		replayCapacity:   store.replayJournalCapacity,
		semanticCapacity: store.semanticJournalCapacity,
		base:             base,
		future:           future,
	}
	publication.seal = publication
	return publication, nil
}

func (c *PreparedInitialCandidateCommit) applyPreparedSourcePlans(
	publication *preparedHTTPStorePublication,
) error {
	now := time.Now()
	for index := range c.sources {
		plan := &c.sources[index]
		source := plan.source
		if !source.Changed() {
			plan.publishedEntry.LastAccessTime = now
			publication.cache.entries[source.url] = plan.publishedEntry
			continue
		}
		entry := plan.publishedEntry
		entry.LastAccessTime = now
		publication.cache.entries[source.url] = entry
		publication.nextSourceGeneration = plan.generation
		if _, err := publication.recordSemanticChange(
			source.url, source.baseDescriptor, source.spec.descriptor, false,
		); err != nil {
			return err
		}
	}
	return nil
}

func (c *PreparedInitialCandidateCommit) applyPreparedCandidates(
	publication *preparedHTTPStorePublication,
) error {
	for index, candidate := range c.candidates {
		entry := c.entries[index]
		entry.AcceptedContent = candidate.content
		entry.AcceptedChecksum = candidate.contentChecksum
		entry.AcceptedTime = time.Now()
		entry.LastAccessTime = entry.AcceptedTime
		entry.ValidationState = StateAccepted
		entry.ETag = candidate.etag
		entry.LastModified = candidate.lastModified
		entry.mutationRevision++
		publication.cache.entries[candidate.url] = entry
		revision, recordErr := publication.recordSemanticChange(
			candidate.url, candidate.sourceDescriptor, candidate.sourceDescriptor, false,
		)
		if recordErr != nil {
			return recordErr
		}
		entry.acceptedRevision = revision
	}
	return nil
}

func clonePreparedActiveLeaseRoot(store *HTTPStore) *preparedHTTPActiveLeaseRoot {
	root := &preparedHTTPActiveLeaseRoot{
		sets: make(map[uint64]*activeLeaseState, len(store.activeLeaseSets)),
		urls: make(map[string]map[uint64]SourceDescriptor, len(store.activeLeaseURLs)),
	}
	for setID, state := range store.activeLeaseSets {
		cloned := *state
		cloned.pending = maps.Clone(state.pending)
		root.sets[setID] = &cloned
	}
	for url, descriptorBySet := range store.activeLeaseURLs {
		root.urls[url] = maps.Clone(descriptorBySet)
	}
	return root
}

func (p *preparedHTTPStorePublication) applyActiveLeasePlan(plan *preparedActiveLeasePlan) {
	if plan == nil || !plan.changed {
		return
	}
	for _, url := range plan.transition.Retired {
		sets := p.active.urls[url]
		delete(sets, plan.set.id)
		if len(sets) == 0 {
			delete(p.active.urls, url)
		}
	}
	p.active.sets[plan.set.id] = &activeLeaseState{
		token: plan.token, leases: plan.leases, replay: plan.replay,
		changeRevision: planSnapshotRevision(plan), pending: map[string]ActiveLeaseChange{},
	}
	for _, url := range plan.transition.Activated {
		value, _ := plan.leases.Get([]byte(url))
		sets := p.active.urls[url]
		if sets == nil {
			sets = map[uint64]SourceDescriptor{}
			p.active.urls[url] = sets
		}
		sets[plan.set.id] = value.descriptor
	}
}

func (p *preparedHTTPStorePublication) recordSemanticChange(
	url string,
	previousSource, source SourceDescriptor,
	removed bool,
) (Revision, error) {
	if url == "" || p.replayRevision == Revision(^uint64(0)) ||
		p.semanticRevision == Revision(^uint64(0)) {
		return 0, errors.New("prepared HTTP publication revision is invalid")
	}
	p.replayRevision++
	replayChange := ReplayChange{Revision: p.replayRevision, URL: url}
	replayChange.authentication = authenticateReplayChange(p.store.revisionSource, &replayChange)
	p.replayJournal.entries, p.replayJournal.start = appendReplayJournal(
		p.replayJournal.entries,
		p.replayJournal.start,
		p.store.replayJournalCapacity,
		replayChange,
	)
	if err := p.recordActiveLeaseChange(url, previousSource, source); err != nil {
		return 0, err
	}
	p.semanticRevision++
	change := SemanticChange{
		Revision: p.semanticRevision, URL: url,
		PreviousSourceIdentity: previousSource.Identity(), SourceIdentity: source.Identity(),
		PreviousDescriptor: previousSource, Descriptor: source, Removed: removed,
	}
	change.authentication = authenticateSemanticChange(p.store.revisionSource, &change)
	p.semanticJournal.entries, p.semanticJournal.start = appendSemanticJournal(
		p.semanticJournal.entries,
		p.semanticJournal.start,
		p.store.semanticJournalCapacity,
		&change,
	)
	return p.semanticRevision, nil
}

func (p *preparedHTTPStorePublication) recordActiveLeaseChange(
	url string,
	previous, next SourceDescriptor,
) error {
	for setID, descriptor := range p.active.urls[url] {
		if descriptor != previous && descriptor != next {
			continue
		}
		state := p.active.sets[setID]
		if state == nil || state.pending == nil || state.changeRevision == ActiveLeaseRevision(^uint64(0)) {
			return errors.New("prepared HTTP active lease change state is invalid")
		}
		state.changeRevision++
		state.pending[url] = ActiveLeaseChange{
			URL: url, Descriptor: descriptor, Revision: state.changeRevision,
		}
	}
	return nil
}

func appendReplayJournal(
	entries []ReplayChange,
	start, capacity int,
	change ReplayChange,
) (next []ReplayChange, nextStart int) {
	if capacity == 0 {
		return entries, start
	}
	if len(entries) < capacity {
		return append(entries, change), start
	}
	entries[start] = change
	return entries, (start + 1) % capacity
}

func appendSemanticJournal(
	entries []SemanticChange,
	start, capacity int,
	change *SemanticChange,
) (next []SemanticChange, nextStart int) {
	if capacity == 0 {
		return entries, start
	}
	if len(entries) < capacity {
		return append(entries, *change), start
	}
	entries[start] = *change
	return entries, (start + 1) % capacity
}

func currentHTTPStoreRoots(store *HTTPStore) preparedHTTPStoreRoots {
	return preparedHTTPStoreRoots{
		cache: store.cache, nextPendingRevision: store.nextPendingRevision,
		nextCandidateRevision: store.nextCandidateRevision,
		nextSourceGeneration:  store.nextSourceGeneration,
		semanticRevision:      store.semanticRevision, replayRevision: store.replayRevision,
		replayJournal: store.replayJournal, replayJournalStart: store.replayJournalStart,
		semanticJournal: store.semanticJournal, semanticJournalStart: store.semanticJournalStart,
		activeSets: store.activeLeaseSets, activeURLs: store.activeLeaseURLs,
		nextActiveLeaseSet: store.nextActiveLeaseSet,
	}
}

func (p *preparedHTTPStorePublication) futureRoots() preparedHTTPStoreRoots {
	return preparedHTTPStoreRoots{
		cache: p.cache.entries, nextPendingRevision: p.nextPendingRevision,
		nextCandidateRevision: p.nextCandidateRevision,
		nextSourceGeneration:  p.nextSourceGeneration,
		semanticRevision:      p.semanticRevision, replayRevision: p.replayRevision,
		replayJournal: p.replayJournal.entries, replayJournalStart: p.replayJournal.start,
		semanticJournal: p.semanticJournal.entries, semanticJournalStart: p.semanticJournal.start,
		activeSets: p.active.sets, activeURLs: p.active.urls,
		nextActiveLeaseSet: p.nextActiveLeaseSet,
	}
}

func capturePreparedHTTPStateAuthentication(
	store *HTTPStore,
	roots *preparedHTTPStoreRoots,
) (preparedHTTPStateAuthentication, error) {
	if store == nil || roots.cache == nil {
		return preparedHTTPStateAuthentication{}, errors.New("prepared HTTP cache state is invalid")
	}
	if err := validateReplayJournal(
		roots.replayJournal, roots.replayJournalStart, store.replayJournalCapacity,
		roots.replayRevision, store.revisionSource,
	); err != nil {
		return preparedHTTPStateAuthentication{}, err
	}
	if err := validateSemanticJournal(
		roots.semanticJournal, roots.semanticJournalStart, store.semanticJournalCapacity,
		roots.semanticRevision, store.revisionSource,
	); err != nil {
		return preparedHTTPStateAuthentication{}, err
	}
	if err := validatePreparedActiveLeaseState(
		store, roots.activeSets, roots.activeURLs, roots.nextActiveLeaseSet,
	); err != nil {
		return preparedHTTPStateAuthentication{}, err
	}
	auth := preparedHTTPStateAuthentication{
		cache:                 make(map[string]*preparedHTTPCacheEntryAuthentication, len(roots.cache)),
		nextPendingRevision:   roots.nextPendingRevision,
		nextCandidateRevision: roots.nextCandidateRevision,
		nextSourceGeneration:  roots.nextSourceGeneration,
		semanticRevision:      roots.semanticRevision,
		replayRevision:        roots.replayRevision,
		replayJournal:         slices.Clone(roots.replayJournal),
		replayJournalStart:    roots.replayJournalStart,
		semanticJournal:       slices.Clone(roots.semanticJournal),
		semanticJournalStart:  roots.semanticJournalStart,
		activeSets:            make(map[uint64]preparedHTTPActiveLeaseStateAuthentication, len(roots.activeSets)),
		activeURLs:            make(map[string]map[uint64]SourceDescriptor, len(roots.activeURLs)),
		nextActiveLeaseSet:    roots.nextActiveLeaseSet,
	}
	for url, entry := range roots.cache {
		if err := validatePreparedCacheEntry(
			url, entry, roots.nextPendingRevision, roots.nextSourceGeneration,
			roots.semanticRevision, roots.replayRevision,
		); err != nil {
			return preparedHTTPStateAuthentication{}, err
		}
		value := *entry
		value.Auth = clonePreparedAuthConfig(entry.Auth)
		auth.cache[url] = &preparedHTTPCacheEntryAuthentication{
			entry: entry, value: value, authPointer: entry.Auth,
		}
	}
	for setID, state := range roots.activeSets {
		auth.activeSets[setID] = preparedHTTPActiveLeaseStateAuthentication{
			state: state, token: state.token, leases: state.leases,
			leaseRoot: state.leases.Root(), leaseCount: state.leases.Len(), replay: state.replay,
			changeRevision: state.changeRevision, pending: maps.Clone(state.pending),
		}
	}
	for url, descriptorBySet := range roots.activeURLs {
		auth.activeURLs[url] = maps.Clone(descriptorBySet)
	}
	return auth, nil
}

func clonePreparedAuthConfig(auth *AuthConfig) *AuthConfig {
	if auth == nil {
		return nil
	}
	cloned := *auth
	cloned.Headers = maps.Clone(auth.Headers)
	return &cloned
}

func validatePreparedHTTPStateAuthentication(
	store *HTTPStore,
	roots *preparedHTTPStoreRoots,
	auth *preparedHTTPStateAuthentication,
) error {
	if store == nil || auth == nil || roots.cache == nil || !samePreparedHTTPStateShape(roots, auth) {
		return errors.New("prepared HTTP replacement state failed authentication")
	}
	if err := validateReplayJournal(
		roots.replayJournal, roots.replayJournalStart, store.replayJournalCapacity,
		roots.replayRevision, store.revisionSource,
	); err != nil {
		return err
	}
	if err := validateSemanticJournal(
		roots.semanticJournal, roots.semanticJournalStart, store.semanticJournalCapacity,
		roots.semanticRevision, store.revisionSource,
	); err != nil {
		return err
	}
	if err := validateAuthenticatedCacheEntries(roots.cache, auth); err != nil {
		return err
	}
	if roots.nextActiveLeaseSet != auth.nextActiveLeaseSet {
		return errors.New("prepared HTTP active lease authority failed authentication")
	}
	if err := validatePreparedActiveLeaseState(
		store, roots.activeSets, roots.activeURLs, roots.nextActiveLeaseSet,
	); err != nil {
		return err
	}
	return validateAuthenticatedActiveLeases(roots.activeSets, roots.activeURLs, auth)
}

func samePreparedHTTPStateShape(
	roots *preparedHTTPStoreRoots,
	auth *preparedHTTPStateAuthentication,
) bool {
	return len(roots.cache) == len(auth.cache) &&
		roots.nextPendingRevision == auth.nextPendingRevision &&
		roots.nextCandidateRevision == auth.nextCandidateRevision &&
		roots.nextSourceGeneration == auth.nextSourceGeneration &&
		roots.semanticRevision == auth.semanticRevision &&
		roots.replayRevision == auth.replayRevision &&
		roots.replayJournalStart == auth.replayJournalStart &&
		roots.semanticJournalStart == auth.semanticJournalStart &&
		slices.Equal(roots.replayJournal, auth.replayJournal) &&
		slices.Equal(roots.semanticJournal, auth.semanticJournal)
}

func validateAuthenticatedCacheEntries(
	cache map[string]*CacheEntry,
	auth *preparedHTTPStateAuthentication,
) error {
	for url, expected := range auth.cache {
		entry := cache[url]
		if entry == nil || entry != expected.entry || entry.Auth != expected.authPointer ||
			!samePreparedCacheEntry(entry, &expected.value) {
			return errors.New("prepared HTTP cache entry failed authentication")
		}
	}
	return nil
}

func validateAuthenticatedActiveLeases(
	activeSets map[uint64]*activeLeaseState,
	activeURLs map[string]map[uint64]SourceDescriptor,
	auth *preparedHTTPStateAuthentication,
) error {
	if len(activeSets) != len(auth.activeSets) || len(activeURLs) != len(auth.activeURLs) {
		return errors.New("prepared HTTP active lease state failed authentication")
	}
	for setID, expected := range auth.activeSets {
		state := activeSets[setID]
		if state == nil || state != expected.state || state.token != expected.token ||
			state.leases != expected.leases || state.leases.Root() != expected.leaseRoot ||
			state.leases.Len() != expected.leaseCount || state.replay != expected.replay ||
			state.changeRevision != expected.changeRevision ||
			!maps.Equal(state.pending, expected.pending) {
			return errors.New("prepared HTTP active lease set failed authentication")
		}
		if state.replay != nil && state.replay.ValidateAuthentication() != nil {
			return errors.New("prepared HTTP active replay state failed authentication")
		}
	}
	for url, expected := range auth.activeURLs {
		if !maps.Equal(activeURLs[url], expected) {
			return errors.New("prepared HTTP active lease index failed authentication")
		}
	}
	return nil
}

func validatePreparedHTTPStoreRoots(
	store *HTTPStore,
	roots *preparedHTTPStoreRoots,
	auth *preparedHTTPStateAuthentication,
) error {
	if roots == nil {
		return errors.New("prepared HTTP store roots are missing")
	}
	return validatePreparedHTTPStateAuthentication(store, roots, auth)
}

func newPreparedHTTPRollbackCheckpoint(
	store *HTTPStore,
	authority chan struct{},
	publication *preparedHTTPStorePublication,
) *preparedHTTPRollbackCheckpoint {
	checkpoint := &preparedHTTPRollbackCheckpoint{
		store: store, authority: authority, roots: publication.base, auth: publication.auth.base,
	}
	checkpoint.seal = checkpoint
	return checkpoint
}

func (c *preparedHTTPRollbackCheckpoint) restore(store *HTTPStore, authority chan struct{}) error {
	if err := c.validate(store, authority); err != nil {
		return err
	}
	c.roots.publish(store)
	current := currentHTTPStoreRoots(store)
	return validatePreparedHTTPStateAuthentication(store, &current, &c.auth)
}

func (c *preparedHTTPRollbackCheckpoint) validate(store *HTTPStore, authority chan struct{}) error {
	if c == nil || c.seal != c || c.store != store || c.authority != authority ||
		store == nil || authority == nil || cap(authority) != 1 || len(authority) != 0 {
		return errors.New("prepared HTTP rollback checkpoint is invalid")
	}
	return validatePreparedHTTPStoreRoots(store, &c.roots, &c.auth)
}

func (r *preparedHTTPStoreRoots) publish(store *HTTPStore) {
	store.cache = r.cache
	store.nextPendingRevision = r.nextPendingRevision
	store.nextCandidateRevision = r.nextCandidateRevision
	store.nextSourceGeneration = r.nextSourceGeneration
	store.semanticRevision = r.semanticRevision
	store.replayRevision = r.replayRevision
	store.replayJournal = r.replayJournal
	store.replayJournalStart = r.replayJournalStart
	store.semanticJournal = r.semanticJournal
	store.semanticJournalStart = r.semanticJournalStart
	store.activeLeaseSets = r.activeSets
	store.activeLeaseURLs = r.activeURLs
	store.nextActiveLeaseSet = r.nextActiveLeaseSet
}

func (p *preparedHTTPStorePublication) validateIdentity(
	owner *PreparedInitialCandidateCommit,
	store *HTTPStore,
) error {
	if !p.sealIntact(owner, store) || !p.authenticationConsistent(store) {
		return errors.New("prepared HTTP replacement state is invalid")
	}
	return validatePreparedHTTPStoreRoots(store, &p.base, &p.auth.base)
}

func (p *preparedHTTPStorePublication) sealIntact(
	owner *PreparedInitialCandidateCommit,
	store *HTTPStore,
) bool {
	return p != nil && p.seal == p && p.owner == owner && p.store == store &&
		p.cache != nil && p.cache.seal == p.cache && p.cache.entries != nil &&
		p.replayJournal != nil && p.replayJournal.seal == p.replayJournal &&
		p.semanticJournal != nil && p.semanticJournal.seal == p.semanticJournal &&
		p.active != nil && p.active.seal == p.active && p.active.sets != nil && p.active.urls != nil
}

func (p *preparedHTTPStorePublication) authenticationConsistent(store *HTTPStore) bool {
	return p.owner == p.auth.owner && p.store == p.auth.store && p.cache == p.auth.cache &&
		p.nextPendingRevision == p.auth.nextPendingRevision &&
		p.nextCandidateRevision == p.auth.nextCandidateRevision &&
		p.nextSourceGeneration == p.auth.nextSourceGeneration &&
		p.semanticRevision == p.auth.semanticRevision && p.replayRevision == p.auth.replayRevision &&
		p.replayJournal == p.auth.replayJournal && p.semanticJournal == p.auth.semanticJournal &&
		p.active == p.auth.active && p.nextActiveLeaseSet == p.auth.nextActiveLeaseSet &&
		store.revisionSource == p.auth.revisionSource &&
		store.prepareAuthority == p.auth.prepareAuthority && store.prepareAuthority != nil &&
		cap(store.prepareAuthority) == 1 && len(store.prepareAuthority) == 0 &&
		store.replayJournalCapacity == p.auth.replayCapacity &&
		store.semanticJournalCapacity == p.auth.semanticCapacity
}

func (p *preparedHTTPStorePublication) validate(
	owner *PreparedInitialCandidateCommit,
	store *HTTPStore,
) error {
	if err := p.validateIdentity(owner, store); err != nil {
		return err
	}
	current := currentHTTPStoreRoots(store)
	if err := validatePreparedHTTPStateAuthentication(store, &current, &p.auth.base); err != nil {
		return err
	}
	future := p.futureRoots()
	return validatePreparedHTTPStateAuthentication(store, &future, &p.auth.future)
}

func (p *preparedHTTPStorePublication) validatePublished(
	owner *PreparedInitialCandidateCommit,
	store *HTTPStore,
) error {
	if err := p.validateIdentity(owner, store); err != nil {
		return err
	}
	current := currentHTTPStoreRoots(store)
	return validatePreparedHTTPStateAuthentication(store, &current, &p.auth.future)
}

func (p *preparedHTTPStorePublication) publish() {
	store := p.store
	store.cache = p.cache.entries
	store.nextPendingRevision = p.nextPendingRevision
	store.nextCandidateRevision = p.nextCandidateRevision
	store.nextSourceGeneration = p.nextSourceGeneration
	store.semanticRevision = p.semanticRevision
	store.replayRevision = p.replayRevision
	store.replayJournal = p.replayJournal.entries
	store.replayJournalStart = p.replayJournal.start
	store.semanticJournal = p.semanticJournal.entries
	store.semanticJournalStart = p.semanticJournal.start
	store.activeLeaseSets = p.active.sets
	store.activeLeaseURLs = p.active.urls
	store.nextActiveLeaseSet = p.nextActiveLeaseSet
}
