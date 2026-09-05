// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderplan

import (
	"errors"
	"slices"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

var (
	errInvalidPlanDelta       = errors.New("render plan delta is invalid")
	errInvalidPlanTransaction = errors.New("render plan transaction is invalid")
	errPlanTransactionSealed  = errors.New("render plan transaction is sealed")
	errInvalidPlanHandle      = errors.New("render plan record handle is invalid")
	errInvalidPlanGapHandle   = errors.New("render plan sequence gap handle is invalid")
	errPlanChangeConflict     = errors.New("render plan transaction repeats a record")
	errPlanRecordExists       = errors.New("render plan record is already present")
	errPlanRecordIdentity     = errors.New("render plan replacement changes record identity")
	errPlanIndexOutOfRange    = errors.New("render plan sequence index is out of range")
)

type sequenceChangeKind uint8

const (
	sequenceReplaceChange sequenceChangeKind = iota + 1
	sequenceDeleteChange
	sequenceInsertChange
)

type sequenceHandle[T any] struct {
	base  *Snapshot
	kind  snapshotCollectionKind
	entry *snapshotEntry[T]
	index int
}

type sequenceGap[T any] struct {
	base        *Snapshot
	kind        snapshotCollectionKind
	index       int
	predecessor *snapshotEntry[T]
	successor   *snapshotEntry[T]
}

// SectionHandle proves one exact section and its emission position.
type SectionHandle struct {
	value sequenceHandle[Section]
	seal  *SectionHandle
}

// SectionGapHandle proves one exact insertion gap in the section sequence.
type SectionGapHandle struct {
	value sequenceGap[Section]
	seal  *SectionGapHandle
}

// FileHandle proves one exact file and its sequence position.
type FileHandle struct {
	value sequenceHandle[File]
	seal  *FileHandle
}

// FileGapHandle proves one exact insertion gap in the file sequence.
type FileGapHandle struct {
	value sequenceGap[File]
	seal  *FileGapHandle
}

type mapHandle[T any] struct {
	base  *Snapshot
	kind  snapshotCollectionKind
	entry *snapshotEntry[T]
	key   string
}

// BackendHandle proves one exact backend record.
type BackendHandle struct {
	value mapHandle[Backend]
	seal  *BackendHandle
}

// ProfileHandle proves one exact profile record.
type ProfileHandle struct {
	value mapHandle[Profile]
	seal  *ProfileHandle
}

// MapHandle proves one exact map record.
type MapHandle struct {
	value mapHandle[Map]
	seal  *MapHandle
}

// CRTListHandle proves one exact CRT-list record.
type CRTListHandle struct {
	value mapHandle[CRTList]
	seal  *CRTListHandle
}

type sequenceChangeAuthentication[T any] struct {
	owner  *sealedSequenceChange[T]
	kind   sequenceChangeKind
	index  int
	before *snapshotEntry[T]
	after  *snapshotEntry[T]
}

type sealedSequenceChange[T any] struct {
	kind   sequenceChangeKind
	index  int
	before *snapshotEntry[T]
	after  *snapshotEntry[T]
	seal   *sealedSequenceChange[T]
	auth   sequenceChangeAuthentication[T]
}

type mapChangeAuthentication[T any] struct {
	owner  *sealedMapChange[T]
	key    string
	before *snapshotEntry[T]
	after  *snapshotEntry[T]
}

type sealedMapChange[T any] struct {
	key    string
	before *snapshotEntry[T]
	after  *snapshotEntry[T]
	seal   *sealedMapChange[T]
	auth   mapChangeAuthentication[T]
}

// SequenceChange is one ordered record transition. Nil means absent.
type SequenceChange[T any] struct {
	Index  int
	Before *T
	After  *T
}

// NamedChange is one keyed record transition. Nil means absent.
type NamedChange[T any] struct {
	Name   string
	Before *T
	After  *T
}

// Changes is the exact changed-record set carried by a Delta.
type Changes struct {
	Sections []SequenceChange[Section]
	Backends []NamedChange[Backend]
	Profiles []NamedChange[Profile]
	Maps     []NamedChange[Map]
	CRTLists []NamedChange[CRTList]
	Files    []FileChange
}

type deltaAuthentication struct {
	owner      *Delta
	authority  *Authority
	base       *Snapshot
	next       *Snapshot
	sections   []*sealedSequenceChange[Section]
	backends   []*sealedMapChange[Backend]
	profiles   []*sealedMapChange[Profile]
	maps       []*sealedMapChange[Map]
	crtLists   []*sealedMapChange[CRTList]
	files      []*sealedSequenceChange[File]
	structural bool
}

// Delta is an authenticated transition between exact plan roots.
type Delta struct {
	authority  *Authority
	base       *Snapshot
	next       *Snapshot
	sections   []*sealedSequenceChange[Section]
	backends   []*sealedMapChange[Backend]
	profiles   []*sealedMapChange[Profile]
	maps       []*sealedMapChange[Map]
	crtLists   []*sealedMapChange[CRTList]
	files      []*sealedSequenceChange[File]
	structural bool
	seal       *Delta
	auth       deltaAuthentication
}

type transactionAuthentication struct {
	owner     *Transaction
	authority *Authority
	base      *Snapshot
}

// Transaction atomically path-copies changes from one exact plan root.
type Transaction struct {
	mu         sync.Mutex
	authority  *Authority
	base       *Snapshot
	sections   map[int]*sealedSequenceChange[Section]
	backends   map[string]*sealedMapChange[Backend]
	profiles   map[string]*sealedMapChange[Profile]
	maps       map[string]*sealedMapChange[Map]
	crtLists   map[string]*sealedMapChange[CRTList]
	files      map[int]*sealedSequenceChange[File]
	structural bool
	built      *Snapshot
	delta      *Delta
	err        error
	sealed     bool
	seal       *Transaction
	auth       transactionAuthentication
}

// SectionHandle returns a proof for the section at index.
func (s *Snapshot) SectionHandle(index int) (*SectionHandle, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	handle, err := newSequenceHandle(s, s.root.sections, sectionSnapshotCollection, index)
	if err != nil {
		return nil, err
	}
	result := &SectionHandle{value: handle}
	result.seal = result
	return result, nil
}

// SectionGapHandle returns a proof for the section insertion gap at index.
func (s *Snapshot) SectionGapHandle(index int) (*SectionGapHandle, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	gap, err := newSequenceGap(s, s.root.sections, sectionSnapshotCollection, index)
	if err != nil {
		return nil, err
	}
	result := &SectionGapHandle{value: gap}
	result.seal = result
	return result, nil
}

// FileHandle returns a proof for the file at index.
func (s *Snapshot) FileHandle(index int) (*FileHandle, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	handle, err := newSequenceHandle(s, s.root.files, fileSnapshotCollection, index)
	if err != nil {
		return nil, err
	}
	result := &FileHandle{value: handle}
	result.seal = result
	return result, nil
}

// FileGapHandle returns a proof for the file insertion gap at index.
func (s *Snapshot) FileGapHandle(index int) (*FileGapHandle, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	gap, err := newSequenceGap(s, s.root.files, fileSnapshotCollection, index)
	if err != nil {
		return nil, err
	}
	result := &FileGapHandle{value: gap}
	result.seal = result
	return result, nil
}

// BackendHandle returns a proof for name when present.
func (s *Snapshot) BackendHandle(name string) (*BackendHandle, bool, error) {
	handle, found, err := newMapHandle(s, s.root.backends, backendSnapshotCollection, name)
	if err != nil || !found {
		return nil, found, err
	}
	result := &BackendHandle{value: handle}
	result.seal = result
	return result, true, nil
}

// ProfileHandle returns a proof for name when present.
func (s *Snapshot) ProfileHandle(name string) (*ProfileHandle, bool, error) {
	handle, found, err := newMapHandle(s, s.root.profiles, profileSnapshotCollection, name)
	if err != nil || !found {
		return nil, found, err
	}
	result := &ProfileHandle{value: handle}
	result.seal = result
	return result, true, nil
}

// MapHandle returns a proof for path when present.
func (s *Snapshot) MapHandle(path string) (*MapHandle, bool, error) {
	handle, found, err := newMapHandle(s, s.root.maps, mapSnapshotCollection, path)
	if err != nil || !found {
		return nil, found, err
	}
	result := &MapHandle{value: handle}
	result.seal = result
	return result, true, nil
}

// CRTListHandle returns a proof for path when present.
func (s *Snapshot) CRTListHandle(path string) (*CRTListHandle, bool, error) {
	handle, found, err := newMapHandle(s, s.root.crtLists, crtListSnapshotCollection, path)
	if err != nil || !found {
		return nil, found, err
	}
	result := &CRTListHandle{value: handle}
	result.seal = result
	return result, true, nil
}

// SectionAt returns a detached exact section by emission rank.
func (s *Snapshot) SectionAt(index int) (Section, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return Section{}, err
	}
	entry, err := snapshotSequenceEntryAt(
		s.root.sections, s.authority, sectionSnapshotCollection, index,
	)
	if err != nil {
		return Section{}, err
	}
	return ownSection(entry.value.value), nil
}

// FileAt returns a detached exact file by emission rank.
func (s *Snapshot) FileAt(index int) (File, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return File{}, err
	}
	entry, err := snapshotSequenceEntryAt(s.root.files, s.authority, fileSnapshotCollection, index)
	if err != nil {
		return File{}, err
	}
	return materializeSnapshotFileEntry(entry)
}

// BackendNamed returns a detached exact backend by name.
func (s *Snapshot) BackendNamed(name string) (Backend, bool, error) {
	return snapshotNamedValue(s, s.root.backends, backendSnapshotCollection, name, ownBackend)
}

// ProfileNamed returns a detached exact profile by name.
func (s *Snapshot) ProfileNamed(name string) (Profile, bool, error) {
	return snapshotNamedValue(s, s.root.profiles, profileSnapshotCollection, name, ownProfile)
}

// MapNamed returns a detached exact map by path.
func (s *Snapshot) MapNamed(path string) (Map, bool, error) {
	return snapshotNamedValue(s, s.root.maps, mapSnapshotCollection, path, ownMap)
}

// BeginTransaction starts an atomic edit against base.
func BeginTransaction(authority *Authority, base *Snapshot) (*Transaction, error) {
	if err := authority.ValidateSnapshot(base); err != nil {
		return nil, err
	}
	transaction := &Transaction{
		authority: authority,
		base:      base,
		sections:  make(map[int]*sealedSequenceChange[Section]),
		backends:  make(map[string]*sealedMapChange[Backend]),
		profiles:  make(map[string]*sealedMapChange[Profile]),
		maps:      make(map[string]*sealedMapChange[Map]),
		crtLists:  make(map[string]*sealedMapChange[CRTList]),
		files:     make(map[int]*sealedSequenceChange[File]),
	}
	transaction.seal = transaction
	transaction.auth = transactionAuthentication{
		owner: transaction, authority: authority, base: base,
	}
	return transaction, nil
}

// ReplaceSection changes only the exact section proven by expected.
func (t *Transaction) ReplaceSection(expected *SectionHandle, section Section) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	if expected.value.entry.value.value.Kind != section.Kind ||
		expected.value.entry.value.value.Name != section.Name {
		return t.fail(errPlanRecordIdentity)
	}
	return replaceSequenceRecord(
		t, expected.value, section, ownSection, exactSection, nil, t.sections,
	)
}

// InsertSection inserts a section at an exact gap and requires full output validation.
func (t *Transaction) InsertSection(expected *SectionGapHandle, section Section) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanGapHandle)
	}
	return insertSequenceRecord(t, expected.value, section, ownSection, t.sections)
}

// DeleteSection removes an exact section and requires full output validation.
func (t *Transaction) DeleteSection(expected *SectionHandle) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	return deleteSequenceRecord(t, expected.value, t.sections)
}

// ReplaceFile changes only the exact file proven by expected.
func (t *Transaction) ReplaceFile(expected *FileHandle, file File) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	if snapshotFileMetadata(expected.value.entry).Path != file.Path {
		return t.fail(errPlanRecordIdentity)
	}
	return t.replaceFile(expected.value, &file)
}

// ReplaceConfigFileDocument replaces the config file without materializing its content.
func (t *Transaction) ReplaceConfigFileDocument(
	expected *FileHandle,
	document rendercontent.Document,
) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	if err := document.ValidateAuthentication(); err != nil {
		return t.fail(err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateSequenceHandle(t, expected.value); err != nil {
		return t.recordError(err)
	}
	if _, exists := t.files[expected.value.index]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	before := expected.value.entry
	metadata := snapshotFileMetadata(before)
	if metadata.Path != ConfigFilePath || metadata.Kind != FileKindConfig ||
		!metadata.ReloadOnChange {
		return t.recordError(errPlanRecordIdentity)
	}
	same, err := snapshotFileMatchesDocument(before, document)
	if err != nil {
		return t.recordError(err)
	}
	if same {
		return nil
	}
	bytes, err := document.Bytes()
	if err != nil {
		return t.recordError(err)
	}
	metadata.Size = int64(bytes)
	metadata.Digest = ""
	metadata.Content = ""
	metadata.ContentKnown = true
	after, err := sealSnapshotDocumentFileEntry(
		t.authority, snapshotKey{}, &metadata, document, false,
	)
	if err != nil {
		return t.recordError(err)
	}
	t.files[expected.value.index] = sealSequenceChange(
		sequenceReplaceChange, expected.value.index, before, after,
	)
	return nil
}

// InsertFile inserts a file at an exact gap and requires full output validation.
func (t *Transaction) InsertFile(expected *FileGapHandle, file File) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanGapHandle)
	}
	return insertSequenceRecord(t, expected.value, file, ownFile, t.files)
}

// DeleteFile removes an exact file and requires full output validation.
func (t *Transaction) DeleteFile(expected *FileHandle) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	return deleteSequenceRecord(t, expected.value, t.files)
}

// ReplaceBackend changes only the exact backend proven by expected.
func (t *Transaction) ReplaceBackend(expected *BackendHandle, backend Backend) error {
	if expected == nil || expected.seal != expected || backend.Name != expected.value.key {
		return t.fail(errInvalidPlanHandle)
	}
	return replaceMapRecord(t, expected.value, backend, ownBackend, exactBackend, t.backends)
}

// InsertBackend inserts a new keyed backend and requires full output validation.
func (t *Transaction) InsertBackend(name string, backend Backend) error {
	if backend.Name != name {
		return t.fail(errPlanRecordIdentity)
	}
	return insertMapRecord(
		t, t.base.root.backends, backendSnapshotCollection, name, backend, ownBackend, t.backends,
	)
}

// DeleteBackend removes an exact backend and requires full output validation.
func (t *Transaction) DeleteBackend(expected *BackendHandle) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	return deleteMapRecord(t, expected.value, t.backends)
}

// ReplaceProfile changes only the exact profile proven by expected.
func (t *Transaction) ReplaceProfile(expected *ProfileHandle, profile Profile) error {
	if expected == nil || expected.seal != expected || profile.Name != expected.value.key {
		return t.fail(errInvalidPlanHandle)
	}
	return replaceMapRecord(t, expected.value, profile, ownProfile, exactProfile, t.profiles)
}

// InsertProfile inserts a new keyed profile and requires full output validation.
func (t *Transaction) InsertProfile(name string, profile Profile) error {
	if profile.Name != name {
		return t.fail(errPlanRecordIdentity)
	}
	return insertMapRecord(
		t, t.base.root.profiles, profileSnapshotCollection, name, profile, ownProfile, t.profiles,
	)
}

// DeleteProfile removes an exact profile and requires full output validation.
func (t *Transaction) DeleteProfile(expected *ProfileHandle) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	return deleteMapRecord(t, expected.value, t.profiles)
}

// ReplaceMap changes only the exact map proven by expected.
func (t *Transaction) ReplaceMap(expected *MapHandle, value Map) error {
	if expected == nil || expected.seal != expected || value.Path != expected.value.key {
		return t.fail(errInvalidPlanHandle)
	}
	return replaceMapRecord(t, expected.value, value, ownMap, exactMap, t.maps)
}

// InsertMap inserts a new keyed map and requires full output validation.
func (t *Transaction) InsertMap(path string, value Map) error {
	if value.Path != path {
		return t.fail(errPlanRecordIdentity)
	}
	return insertMapRecord(
		t, t.base.root.maps, mapSnapshotCollection, path, value, ownMap, t.maps,
	)
}

// DeleteMap removes an exact map and requires full output validation.
func (t *Transaction) DeleteMap(expected *MapHandle) error {
	if expected == nil || expected.seal != expected {
		return t.fail(errInvalidPlanHandle)
	}
	return deleteMapRecord(t, expected.value, t.maps)
}

// ReplaceCRTList changes only the exact CRT-list proven by expected.
func (t *Transaction) ReplaceCRTList(expected *CRTListHandle, value CRTList) error {
	if expected == nil || expected.seal != expected || value.Path != expected.value.key {
		return t.fail(errInvalidPlanHandle)
	}
	return replaceMapRecord(t, expected.value, value, ownCRTList, exactCRTList, t.crtLists)
}

// Commit seals the next snapshot and its exact base-to-next proof.
func (t *Transaction) Commit() (*Snapshot, *Delta, error) {
	if t == nil {
		return nil, nil, errInvalidPlanTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateAuthentication(); err != nil {
		return nil, nil, err
	}
	if t.sealed {
		if t.err != nil {
			return nil, nil, t.err
		}
		if err := t.delta.ValidateAuthentication(); err != nil {
			return nil, nil, err
		}
		return t.built, t.delta, nil
	}
	t.sealed = true
	if t.err != nil {
		return nil, nil, t.err
	}
	sections := sortedSequenceChanges(t.sections)
	backends := sortedMapChanges(t.backends)
	profiles := sortedMapChanges(t.profiles)
	mapsChanges := sortedMapChanges(t.maps)
	crtLists := sortedMapChanges(t.crtLists)
	files := sortedSequenceChanges(t.files)

	sectionCollection, err := applySequenceChanges(
		t.authority, t.base.root.sections, sectionSnapshotCollection, sections,
	)
	if err != nil {
		return nil, nil, t.recordError(err)
	}
	backendCollection, err := applyMapChanges(
		t.authority, t.base.root.backends, backendSnapshotCollection, backends,
	)
	if err != nil {
		return nil, nil, t.recordError(err)
	}
	profileCollection, err := applyMapChanges(
		t.authority, t.base.root.profiles, profileSnapshotCollection, profiles,
	)
	if err != nil {
		return nil, nil, t.recordError(err)
	}
	mapCollection, err := applyMapChanges(
		t.authority, t.base.root.maps, mapSnapshotCollection, mapsChanges,
	)
	if err != nil {
		return nil, nil, t.recordError(err)
	}
	crtListCollection, err := applyMapChanges(
		t.authority, t.base.root.crtLists, crtListSnapshotCollection, crtLists,
	)
	if err != nil {
		return nil, nil, t.recordError(err)
	}
	fileCollection, err := applySequenceChanges(
		t.authority, t.base.root.files, fileSnapshotCollection, files,
	)
	if err != nil {
		return nil, nil, t.recordError(err)
	}

	if sectionCollection == t.base.root.sections && backendCollection == t.base.root.backends &&
		profileCollection == t.base.root.profiles && mapCollection == t.base.root.maps &&
		crtListCollection == t.base.root.crtLists && fileCollection == t.base.root.files {
		t.built = t.base
	} else {
		root := sealDeferredPlanRoot(
			t.authority, t.base.root.schema, sectionCollection, backendCollection,
			profileCollection, mapCollection, crtListCollection, fileCollection,
		)
		t.built = sealSnapshot(t.authority, root)
	}
	t.delta = sealPlanDelta(
		t.authority, t.base, t.built, sections, backends, profiles,
		mapsChanges, crtLists, files, t.structural,
	)
	return t.built, t.delta, nil
}

// Apply returns this delta's next root only for its exact base.
func (d *Delta) Apply(base *Snapshot) (*Snapshot, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if base != d.base {
		return nil, errInvalidPlanDelta
	}
	return d.next, nil
}

// RequiresFullValidation reports structural changes not safe for local validation.
func (d *Delta) RequiresFullValidation() (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.structural, nil
}

// Changes returns detached copies of only the records changed by this delta.
func (d *Delta) Changes() (Changes, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return Changes{}, err
	}
	return Changes{
		Sections: detachSequenceChanges(d.sections, ownSection),
		Backends: detachMapChanges(d.backends, ownBackend),
		Profiles: detachMapChanges(d.profiles, ownProfile),
		Maps:     detachMapChanges(d.maps, ownMap),
		CRTLists: detachMapChanges(d.crtLists, ownCRTList),
		Files:    detachFileChanges(d.files),
	}, nil
}

// ValidateAuthentication verifies the exact transition and every changed record.
func (d *Delta) ValidateAuthentication() error {
	if err := validatePlanDeltaHeader(d); err != nil {
		return errInvalidPlanDelta
	}
	if err := d.authority.ValidateSnapshot(d.base); err != nil {
		return errors.Join(errInvalidPlanDelta, err)
	}
	if err := d.authority.ValidateSnapshot(d.next); err != nil {
		return errors.Join(errInvalidPlanDelta, err)
	}
	if err := validatePlanDeltaCollections(d); err != nil {
		return err
	}
	return nil
}

func validatePlanDeltaHeader(d *Delta) error {
	if d == nil || d.seal != d || d.authority == nil || d.base == nil || d.next == nil {
		return errInvalidPlanDelta
	}
	expected := deltaAuthentication{
		owner: d, authority: d.authority, base: d.base, next: d.next,
		sections: d.auth.sections, backends: d.auth.backends, profiles: d.auth.profiles,
		maps: d.auth.maps, crtLists: d.auth.crtLists, files: d.auth.files,
		structural: d.structural,
	}
	if d.auth.owner != expected.owner || d.auth.authority != expected.authority ||
		d.auth.base != expected.base || d.auth.next != expected.next ||
		d.auth.structural != expected.structural {
		return errInvalidPlanDelta
	}
	if !samePointers(d.auth.sections, d.sections) || !samePointers(d.auth.backends, d.backends) ||
		!samePointers(d.auth.profiles, d.profiles) || !samePointers(d.auth.maps, d.maps) ||
		!samePointers(d.auth.crtLists, d.crtLists) || !samePointers(d.auth.files, d.files) {
		return errInvalidPlanDelta
	}
	return nil
}

func validatePlanDeltaCollections(d *Delta) error {
	if d.structural != planDeltaIsStructural(d) {
		return errInvalidPlanDelta
	}
	if err := validateSequenceDelta(
		d.authority, d.base.root.sections, d.next.root.sections,
		sectionSnapshotCollection, d.sections,
	); err != nil {
		return err
	}
	if err := validateMapDelta(
		d.authority, d.base.root.backends, d.next.root.backends,
		backendSnapshotCollection, d.backends,
	); err != nil {
		return err
	}
	if err := validateMapDelta(
		d.authority, d.base.root.profiles, d.next.root.profiles,
		profileSnapshotCollection, d.profiles,
	); err != nil {
		return err
	}
	if err := validateMapDelta(
		d.authority, d.base.root.maps, d.next.root.maps,
		mapSnapshotCollection, d.maps,
	); err != nil {
		return err
	}
	if err := validateMapDelta(
		d.authority, d.base.root.crtLists, d.next.root.crtLists,
		crtListSnapshotCollection, d.crtLists,
	); err != nil {
		return err
	}
	return validateSequenceDelta(
		d.authority, d.base.root.files, d.next.root.files,
		fileSnapshotCollection, d.files,
	)
}

// SameRoot reports whether no plan record changed.
func (d *Delta) SameRoot() (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.base == d.next, nil
}

func (t *Transaction) validateAuthentication() error {
	if t == nil || t.seal != t || t.auth.owner != t || t.authority == nil ||
		t.auth.authority != t.authority || t.base == nil || t.auth.base != t.base ||
		t.sections == nil || t.backends == nil || t.profiles == nil || t.maps == nil ||
		t.crtLists == nil || t.files == nil {
		return errInvalidPlanTransaction
	}
	if err := t.authority.ValidateSnapshot(t.base); err != nil {
		return errors.Join(errInvalidPlanTransaction, err)
	}
	return nil
}

func (t *Transaction) validateOpen() error {
	if err := t.validateAuthentication(); err != nil {
		return err
	}
	if t.sealed {
		return errPlanTransactionSealed
	}
	if t.err != nil {
		return t.err
	}
	return nil
}

func (t *Transaction) fail(err error) error {
	if t == nil {
		return errInvalidPlanTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recordError(err)
}

func (t *Transaction) recordError(err error) error {
	if t.err == nil {
		t.err = err
	}
	return err
}

func newSequenceHandle[T any](
	base *Snapshot,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	index int,
) (sequenceHandle[T], error) {
	entry, err := snapshotSequenceEntryAt(collection, base.authority, kind, index)
	if err != nil {
		return sequenceHandle[T]{}, err
	}
	return sequenceHandle[T]{base: base, kind: kind, entry: entry, index: index}, nil
}

func newSequenceGap[T any](
	base *Snapshot,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	index int,
) (sequenceGap[T], error) {
	if index < 0 || index > collection.entries {
		return sequenceGap[T]{}, errPlanIndexOutOfRange
	}
	var predecessor, successor *snapshotEntry[T]
	var err error
	if index > 0 {
		predecessor, err = snapshotSequenceEntryAt(collection, base.authority, kind, index-1)
		if err != nil {
			return sequenceGap[T]{}, err
		}
	}
	if index < collection.entries {
		successor, err = snapshotSequenceEntryAt(collection, base.authority, kind, index)
		if err != nil {
			return sequenceGap[T]{}, err
		}
	}
	return sequenceGap[T]{
		base: base, kind: kind, index: index, predecessor: predecessor, successor: successor,
	}, nil
}

func newMapHandle[T any](
	base *Snapshot,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	name string,
) (mapHandle[T], bool, error) {
	if err := base.ValidateAuthentication(); err != nil {
		return mapHandle[T]{}, false, err
	}
	entry, err := findSnapshotEntry(
		base.authority, kind, collection, snapshotKey{index: -1, name: name},
	)
	if errors.Is(err, errSnapshotEntryNotFound) {
		return mapHandle[T]{}, false, nil
	}
	if err != nil {
		return mapHandle[T]{}, false, err
	}
	return mapHandle[T]{base: base, kind: kind, entry: entry, key: name}, true, nil
}

func snapshotNamedValue[T any](
	snapshot *Snapshot,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	name string,
	own func(T) T,
) (value T, found bool, err error) {
	var zero T
	if err := snapshot.ValidateAuthentication(); err != nil {
		return zero, false, err
	}
	entry, err := findSnapshotEntry(
		snapshot.authority, kind, collection, snapshotKey{index: -1, name: name},
	)
	if errors.Is(err, errSnapshotEntryNotFound) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	return own(entry.value.value), true, nil
}

func replaceSequenceRecord[T any](
	t *Transaction,
	handle sequenceHandle[T],
	value T,
	own func(T) T,
	equal func(T, T) bool,
	requiresFullValidation func(T, T) bool,
	changes map[int]*sealedSequenceChange[T],
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateSequenceHandle(t, handle); err != nil {
		return t.recordError(err)
	}
	if _, exists := changes[handle.index]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	if equal(handle.entry.value.value, value) {
		return nil
	}
	if requiresFullValidation != nil && requiresFullValidation(handle.entry.value.value, value) {
		t.structural = true
	}
	after := sealSnapshotEntry(t.authority, handle.kind, snapshotKey{}, own(value))
	changes[handle.index] = sealSequenceChange(
		sequenceReplaceChange, handle.index, handle.entry, after,
	)
	return nil
}

func (t *Transaction) replaceFile(handle sequenceHandle[File], file *File) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateSequenceHandle(t, handle); err != nil {
		return t.recordError(err)
	}
	if _, exists := t.files[handle.index]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	before, err := materializeSnapshotFileEntry(handle.entry)
	if err != nil {
		return t.recordError(err)
	}
	if exactFile(before, *file) {
		return nil
	}
	if fileReplacementStructural(&before, file) {
		t.structural = true
	}
	after := sealSnapshotEntry(t.authority, handle.kind, snapshotKey{}, ownFile(*file))
	t.files[handle.index] = sealSequenceChange(
		sequenceReplaceChange, handle.index, handle.entry, after,
	)
	return nil
}

func insertSequenceRecord[T any](
	t *Transaction,
	gap sequenceGap[T],
	value T,
	own func(T) T,
	changes map[int]*sealedSequenceChange[T],
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateSequenceGap(t, gap); err != nil {
		return t.recordError(err)
	}
	if _, exists := changes[gap.index]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	after := sealSnapshotEntry(t.authority, gap.kind, snapshotKey{}, own(value))
	changes[gap.index] = sealSequenceChange(sequenceInsertChange, gap.index, nil, after)
	t.structural = true
	return nil
}

func deleteSequenceRecord[T any](
	t *Transaction,
	handle sequenceHandle[T],
	changes map[int]*sealedSequenceChange[T],
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateSequenceHandle(t, handle); err != nil {
		return t.recordError(err)
	}
	if _, exists := changes[handle.index]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	changes[handle.index] = sealSequenceChange(
		sequenceDeleteChange, handle.index, handle.entry, nil,
	)
	t.structural = true
	return nil
}

func replaceMapRecord[T any](
	t *Transaction,
	handle mapHandle[T],
	value T,
	own func(T) T,
	equal func(T, T) bool,
	changes map[string]*sealedMapChange[T],
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateMapHandle(t, handle); err != nil {
		return t.recordError(err)
	}
	if _, exists := changes[handle.key]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	if equal(handle.entry.value.value, value) {
		return nil
	}
	key := snapshotKey{index: -1, name: handle.key}
	after := sealSnapshotEntry(t.authority, handle.kind, key, own(value))
	changes[handle.key] = sealMapChange(handle.key, handle.entry, after)
	return nil
}

func insertMapRecord[T any](
	t *Transaction,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	name string,
	value T,
	own func(T) T,
	changes map[string]*sealedMapChange[T],
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if name == "" {
		return t.recordError(errPlanRecordIdentity)
	}
	if _, exists := changes[name]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	key := snapshotKey{index: -1, name: name}
	if _, err := findSnapshotEntry(t.authority, kind, collection, key); err == nil {
		return t.recordError(errPlanRecordExists)
	} else if !errors.Is(err, errSnapshotEntryNotFound) {
		return t.recordError(err)
	}
	after := sealSnapshotEntry(t.authority, kind, key, own(value))
	changes[name] = sealMapChange(name, nil, after)
	t.structural = true
	return nil
}

func deleteMapRecord[T any](
	t *Transaction,
	handle mapHandle[T],
	changes map[string]*sealedMapChange[T],
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateMapHandle(t, handle); err != nil {
		return t.recordError(err)
	}
	if _, exists := changes[handle.key]; exists {
		return t.recordError(errPlanChangeConflict)
	}
	changes[handle.key] = sealMapChange(handle.key, handle.entry, nil)
	t.structural = true
	return nil
}

func validateSequenceHandle[T any](t *Transaction, handle sequenceHandle[T]) error {
	if handle.base != t.base {
		return errInvalidPlanHandle
	}
	collection, ok := planSequenceCollection(t.base.root, handle.kind)
	if !ok {
		return errInvalidPlanHandle
	}
	typed, ok := collection.(*snapshotCollection[T])
	if !ok {
		return errInvalidPlanHandle
	}
	entry, err := snapshotSequenceEntryAt(typed, t.authority, handle.kind, handle.index)
	if err != nil || entry != handle.entry {
		return errInvalidPlanHandle
	}
	return nil
}

func validateSequenceGap[T any](t *Transaction, gap sequenceGap[T]) error {
	if gap.base != t.base {
		return errInvalidPlanGapHandle
	}
	collection, ok := planSequenceCollection(t.base.root, gap.kind)
	if !ok {
		return errInvalidPlanGapHandle
	}
	typed, ok := collection.(*snapshotCollection[T])
	if !ok || gap.index < 0 || gap.index > typed.entries {
		return errInvalidPlanGapHandle
	}
	if gap.index > 0 {
		entry, err := snapshotSequenceEntryAt(typed, t.authority, gap.kind, gap.index-1)
		if err != nil || entry != gap.predecessor {
			return errInvalidPlanGapHandle
		}
	} else if gap.predecessor != nil {
		return errInvalidPlanGapHandle
	}
	if gap.index < typed.entries {
		entry, err := snapshotSequenceEntryAt(typed, t.authority, gap.kind, gap.index)
		if err != nil || entry != gap.successor {
			return errInvalidPlanGapHandle
		}
	} else if gap.successor != nil {
		return errInvalidPlanGapHandle
	}
	return nil
}

func planSequenceCollection(root *planRoot, kind snapshotCollectionKind) (any, bool) {
	switch kind {
	case sectionSnapshotCollection:
		return root.sections, true
	case fileSnapshotCollection:
		return root.files, true
	default:
		return nil, false
	}
}

func validateMapHandle[T any](t *Transaction, handle mapHandle[T]) error {
	if handle.base != t.base || handle.entry == nil {
		return errInvalidPlanHandle
	}
	collection, ok := planMapCollection(t.base.root, handle.kind)
	if !ok {
		return errInvalidPlanHandle
	}
	typed, ok := collection.(*snapshotCollection[T])
	if !ok {
		return errInvalidPlanHandle
	}
	entry, err := findSnapshotEntry(
		t.authority, handle.kind, typed, snapshotKey{index: -1, name: handle.key},
	)
	if err != nil || entry != handle.entry {
		return errInvalidPlanHandle
	}
	return nil
}

func planMapCollection(root *planRoot, kind snapshotCollectionKind) (any, bool) {
	switch kind {
	case backendSnapshotCollection:
		return root.backends, true
	case profileSnapshotCollection:
		return root.profiles, true
	case mapSnapshotCollection:
		return root.maps, true
	case crtListSnapshotCollection:
		return root.crtLists, true
	default:
		return nil, false
	}
}

func sealSequenceChange[T any](
	kind sequenceChangeKind,
	index int,
	before, after *snapshotEntry[T],
) *sealedSequenceChange[T] {
	change := &sealedSequenceChange[T]{kind: kind, index: index, before: before, after: after}
	change.seal = change
	change.auth = sequenceChangeAuthentication[T]{
		owner: change, kind: kind, index: index, before: before, after: after,
	}
	return change
}

func sealMapChange[T any](
	key string,
	before, after *snapshotEntry[T],
) *sealedMapChange[T] {
	change := &sealedMapChange[T]{key: key, before: before, after: after}
	change.seal = change
	change.auth = mapChangeAuthentication[T]{
		owner: change, key: key, before: before, after: after,
	}
	return change
}

func sortedSequenceChanges[T any](
	values map[int]*sealedSequenceChange[T],
) []*sealedSequenceChange[T] {
	changes := make([]*sealedSequenceChange[T], 0, len(values))
	for _, change := range values {
		changes = append(changes, change)
	}
	slices.SortFunc(changes, func(left, right *sealedSequenceChange[T]) int {
		return left.index - right.index
	})
	return changes
}

func sortedMapChanges[T any](values map[string]*sealedMapChange[T]) []*sealedMapChange[T] {
	changes := make([]*sealedMapChange[T], 0, len(values))
	for _, change := range values {
		changes = append(changes, change)
	}
	slices.SortFunc(changes, func(left, right *sealedMapChange[T]) int {
		return strings.Compare(left.key, right.key)
	})
	return changes
}

func sealPlanDelta(
	authority *Authority,
	base, next *Snapshot,
	sections []*sealedSequenceChange[Section],
	backends []*sealedMapChange[Backend],
	profiles []*sealedMapChange[Profile],
	mapsChanges []*sealedMapChange[Map],
	crtLists []*sealedMapChange[CRTList],
	files []*sealedSequenceChange[File],
	structural bool,
) *Delta {
	delta := &Delta{
		authority: authority, base: base, next: next,
		sections: slices.Clone(sections), backends: slices.Clone(backends),
		profiles: slices.Clone(profiles), maps: slices.Clone(mapsChanges),
		crtLists: slices.Clone(crtLists), files: slices.Clone(files), structural: structural,
	}
	delta.seal = delta
	delta.auth = deltaAuthentication{
		owner: delta, authority: authority, base: base, next: next,
		sections: slices.Clone(delta.sections), backends: slices.Clone(delta.backends),
		profiles: slices.Clone(delta.profiles), maps: slices.Clone(delta.maps),
		crtLists: slices.Clone(delta.crtLists), files: slices.Clone(delta.files),
		structural: structural,
	}
	return delta
}

func applySequenceChanges[T any](
	authority *Authority,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	changes []*sealedSequenceChange[T],
) (*snapshotCollection[T], error) {
	root := collection.root
	offset := 0
	for _, change := range changes {
		index := change.index + offset
		var err error
		switch change.kind {
		case sequenceReplaceChange:
			root, _, err = replaceSnapshotSequenceNode(authority, kind, root, index, change.after)
		case sequenceDeleteChange:
			root, _, err = deleteSnapshotSequenceNode(authority, kind, root, index)
			offset--
		case sequenceInsertChange:
			root, err = insertSnapshotSequenceNode(authority, kind, root, index, change.after)
			offset++
		default:
			err = errInvalidPlanDelta
		}
		if err != nil {
			return nil, err
		}
	}
	if root == collection.root {
		return collection, nil
	}
	present := collection.present || root != nil
	return sealSnapshotCollection(authority, kind, present, root), nil
}

func applyMapChanges[T any](
	authority *Authority,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	changes []*sealedMapChange[T],
) (*snapshotCollection[T], error) {
	root := collection.root
	for _, change := range changes {
		var err error
		if change.after == nil {
			root, _, err = deleteSnapshotMapNode(authority, kind, root, change.key)
		} else {
			root, _, err = putSnapshotMapNode(authority, kind, root, change.after)
		}
		if err != nil {
			return nil, err
		}
	}
	if root == collection.root {
		return collection, nil
	}
	present := collection.present || root != nil
	return sealSnapshotCollection(authority, kind, present, root), nil
}

func snapshotSequenceEntryAt[T any](
	collection *snapshotCollection[T],
	authority *Authority,
	kind snapshotCollectionKind,
	index int,
) (*snapshotEntry[T], error) {
	if err := collection.validate(authority, kind); err != nil {
		return nil, err
	}
	if index < 0 || index >= collection.entries {
		return nil, errPlanIndexOutOfRange
	}
	node := collection.root
	for node != nil {
		if err := node.validate(authority, kind); err != nil {
			return nil, err
		}
		left := snapshotNodeCount(node.left)
		switch {
		case index < left:
			node = node.left
		case index > left:
			index -= left + 1
			node = node.right
		default:
			return node.entry, nil
		}
	}
	return nil, errInvalidSnapshot
}

func replaceSnapshotSequenceNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	node *snapshotNode[T],
	index int,
	entry *snapshotEntry[T],
) (*snapshotNode[T], bool, error) {
	if node == nil || index < 0 || index >= snapshotNodeCount(node) {
		return nil, false, errPlanIndexOutOfRange
	}
	if err := node.validate(authority, kind); err != nil {
		return nil, false, err
	}
	leftCount := snapshotNodeCount(node.left)
	if index == leftCount {
		if node.entry == entry {
			return node, false, nil
		}
		return newSnapshotNode(authority, kind, entry, node.left, node.right), true, nil
	}
	left, right := node.left, node.right
	var changed bool
	var err error
	if index < leftCount {
		left, changed, err = replaceSnapshotSequenceNode(authority, kind, left, index, entry)
	} else {
		right, changed, err = replaceSnapshotSequenceNode(
			authority, kind, right, index-leftCount-1, entry,
		)
	}
	if err != nil || !changed {
		return node, changed, err
	}
	return newSnapshotNode(authority, kind, node.entry, left, right), true, nil
}

func insertSnapshotSequenceNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	node *snapshotNode[T],
	index int,
	entry *snapshotEntry[T],
) (*snapshotNode[T], error) {
	if index < 0 || index > snapshotNodeCount(node) {
		return nil, errPlanIndexOutOfRange
	}
	if node == nil {
		return newSnapshotNode(authority, kind, entry, nil, nil), nil
	}
	if err := node.validate(authority, kind); err != nil {
		return nil, err
	}
	leftCount := snapshotNodeCount(node.left)
	left, right := node.left, node.right
	var err error
	if index <= leftCount {
		left, err = insertSnapshotSequenceNode(authority, kind, left, index, entry)
	} else {
		right, err = insertSnapshotSequenceNode(
			authority, kind, right, index-leftCount-1, entry,
		)
	}
	if err != nil {
		return nil, err
	}
	return balanceSnapshotNode(authority, kind, node.entry, left, right), nil
}

func deleteSnapshotSequenceNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	node *snapshotNode[T],
	index int,
) (*snapshotNode[T], bool, error) {
	if node == nil || index < 0 || index >= snapshotNodeCount(node) {
		return nil, false, errPlanIndexOutOfRange
	}
	if err := node.validate(authority, kind); err != nil {
		return nil, false, err
	}
	leftCount := snapshotNodeCount(node.left)
	left, right := node.left, node.right
	if index == leftCount {
		if left == nil {
			return right, true, nil
		}
		if right == nil {
			return left, true, nil
		}
		var successor *snapshotEntry[T]
		var err error
		right, successor, err = deleteMinimumSnapshotNode(authority, kind, right)
		if err != nil {
			return nil, false, err
		}
		return balanceSnapshotNode(authority, kind, successor, left, right), true, nil
	}
	var changed bool
	var err error
	if index < leftCount {
		left, changed, err = deleteSnapshotSequenceNode(authority, kind, left, index)
	} else {
		right, changed, err = deleteSnapshotSequenceNode(
			authority, kind, right, index-leftCount-1,
		)
	}
	if err != nil || !changed {
		return node, changed, err
	}
	return balanceSnapshotNode(authority, kind, node.entry, left, right), true, nil
}

func putSnapshotMapNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	node *snapshotNode[T],
	entry *snapshotEntry[T],
) (*snapshotNode[T], bool, error) {
	if node == nil {
		return newSnapshotNode(authority, kind, entry, nil, nil), true, nil
	}
	if err := node.validate(authority, kind); err != nil {
		return nil, false, err
	}
	comparison := compareSnapshotKeys(entry.key, node.entry.key)
	if comparison == 0 {
		if node.entry == entry {
			return node, false, nil
		}
		return newSnapshotNode(authority, kind, entry, node.left, node.right), true, nil
	}
	left, right := node.left, node.right
	var changed bool
	var err error
	if comparison < 0 {
		left, changed, err = putSnapshotMapNode(authority, kind, left, entry)
	} else {
		right, changed, err = putSnapshotMapNode(authority, kind, right, entry)
	}
	if err != nil || !changed {
		return node, changed, err
	}
	return balanceSnapshotNode(authority, kind, node.entry, left, right), true, nil
}

func deleteSnapshotMapNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	node *snapshotNode[T],
	name string,
) (*snapshotNode[T], bool, error) {
	if node == nil {
		return nil, false, errSnapshotEntryNotFound
	}
	if err := node.validate(authority, kind); err != nil {
		return nil, false, err
	}
	key := snapshotKey{index: -1, name: name}
	comparison := compareSnapshotKeys(key, node.entry.key)
	left, right := node.left, node.right
	if comparison == 0 {
		if left == nil {
			return right, true, nil
		}
		if right == nil {
			return left, true, nil
		}
		var successor *snapshotEntry[T]
		var err error
		right, successor, err = deleteMinimumSnapshotNode(authority, kind, right)
		if err != nil {
			return nil, false, err
		}
		return balanceSnapshotNode(authority, kind, successor, left, right), true, nil
	}
	var changed bool
	var err error
	if comparison < 0 {
		left, changed, err = deleteSnapshotMapNode(authority, kind, left, name)
	} else {
		right, changed, err = deleteSnapshotMapNode(authority, kind, right, name)
	}
	if err != nil || !changed {
		return node, changed, err
	}
	return balanceSnapshotNode(authority, kind, node.entry, left, right), true, nil
}

func deleteMinimumSnapshotNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	node *snapshotNode[T],
) (*snapshotNode[T], *snapshotEntry[T], error) {
	if err := node.validate(authority, kind); err != nil {
		return nil, nil, err
	}
	if node.left == nil {
		return node.right, node.entry, nil
	}
	left, entry, err := deleteMinimumSnapshotNode(authority, kind, node.left)
	if err != nil {
		return nil, nil, err
	}
	return balanceSnapshotNode(authority, kind, node.entry, left, node.right), entry, nil
}

func balanceSnapshotNode[T any](
	authority *Authority,
	kind snapshotCollectionKind,
	entry *snapshotEntry[T],
	left, right *snapshotNode[T],
) *snapshotNode[T] {
	switch {
	case snapshotNodeHeight(left) > snapshotNodeHeight(right)+1:
		pivot := left
		if snapshotNodeHeight(pivot.left) >= snapshotNodeHeight(pivot.right) {
			newRight := newSnapshotNode(authority, kind, entry, pivot.right, right)
			return newSnapshotNode(authority, kind, pivot.entry, pivot.left, newRight)
		}
		middle := pivot.right
		newLeft := newSnapshotNode(authority, kind, pivot.entry, pivot.left, middle.left)
		newRight := newSnapshotNode(authority, kind, entry, middle.right, right)
		return newSnapshotNode(authority, kind, middle.entry, newLeft, newRight)
	case snapshotNodeHeight(right) > snapshotNodeHeight(left)+1:
		pivot := right
		if snapshotNodeHeight(pivot.right) >= snapshotNodeHeight(pivot.left) {
			newLeft := newSnapshotNode(authority, kind, entry, left, pivot.left)
			return newSnapshotNode(authority, kind, pivot.entry, newLeft, pivot.right)
		}
		middle := pivot.left
		newLeft := newSnapshotNode(authority, kind, entry, left, middle.left)
		newRight := newSnapshotNode(authority, kind, pivot.entry, middle.right, pivot.right)
		return newSnapshotNode(authority, kind, middle.entry, newLeft, newRight)
	default:
		return newSnapshotNode(authority, kind, entry, left, right)
	}
}

func validateSequenceDelta[T any](
	authority *Authority,
	base, next *snapshotCollection[T],
	kind snapshotCollectionKind,
	changes []*sealedSequenceChange[T],
) error {
	offset := 0
	previousIndex := -1
	for _, change := range changes {
		if err := validateSequenceChangeAuthentication(change, previousIndex); err != nil {
			return errInvalidPlanDelta
		}
		previousIndex = change.index
		if err := validateSequenceChangeBefore(authority, base, kind, change); err != nil {
			return err
		}
		var err error
		offset, err = validateSequenceChangeAfter(authority, next, kind, change, offset)
		if err != nil {
			return err
		}
	}
	if next.entries != base.entries+offset {
		return errInvalidPlanDelta
	}
	if len(changes) == 0 {
		if base != next {
			return errInvalidPlanDelta
		}
		return nil
	}
	return nil
}

func validateSequenceChangeAuthentication[T any](change *sealedSequenceChange[T], previousIndex int) error {
	if change == nil || change.seal != change || change.index <= previousIndex {
		return errInvalidPlanDelta
	}
	expected := sequenceChangeAuthentication[T]{
		owner: change, kind: change.kind, index: change.index,
		before: change.before, after: change.after,
	}
	if change.auth != expected {
		return errInvalidPlanDelta
	}
	return nil
}

func validateSequenceChangeBefore[T any](
	authority *Authority,
	base *snapshotCollection[T],
	kind snapshotCollectionKind,
	change *sealedSequenceChange[T],
) error {
	if change.before == nil {
		return nil
	}
	entry, err := snapshotSequenceEntryAt(base, authority, kind, change.index)
	if err != nil || entry != change.before {
		return errInvalidPlanDelta
	}
	return nil
}

func validateSequenceChangeAfter[T any](
	authority *Authority,
	next *snapshotCollection[T],
	kind snapshotCollectionKind,
	change *sealedSequenceChange[T],
	offset int,
) (int, error) {
	nextIndex := change.index + offset
	switch change.kind {
	case sequenceReplaceChange:
		if change.before == nil || change.after == nil {
			return 0, errInvalidPlanDelta
		}
	case sequenceDeleteChange:
		if change.before == nil || change.after != nil {
			return 0, errInvalidPlanDelta
		}
		return offset - 1, nil
	case sequenceInsertChange:
		if change.before != nil || change.after == nil {
			return 0, errInvalidPlanDelta
		}
		offset++
	default:
		return 0, errInvalidPlanDelta
	}
	entry, err := snapshotSequenceEntryAt(next, authority, kind, nextIndex)
	if err != nil || entry != change.after {
		return 0, errInvalidPlanDelta
	}
	return offset, nil
}

func validateMapDelta[T any](
	authority *Authority,
	base, next *snapshotCollection[T],
	kind snapshotCollectionKind,
	changes []*sealedMapChange[T],
) error {
	countDelta := 0
	previousKey := ""
	for _, change := range changes {
		if err := validateMapChangeAuthentication(change, previousKey); err != nil {
			return errInvalidPlanDelta
		}
		previousKey = change.key
		key := snapshotKey{index: -1, name: change.key}
		if err := validateMapChangeSide(authority, base, kind, key, change.before); err != nil {
			return errInvalidPlanDelta
		}
		if err := validateMapChangeSide(authority, next, kind, key, change.after); err != nil {
			return errInvalidPlanDelta
		}
		if change.before == nil {
			countDelta++
		}
		if change.after == nil {
			countDelta--
		}
	}
	if next.entries != base.entries+countDelta || len(changes) == 0 && base != next {
		return errInvalidPlanDelta
	}
	return nil
}

func validateMapChangeAuthentication[T any](change *sealedMapChange[T], previousKey string) error {
	if change == nil || change.seal != change || change.key == "" {
		return errInvalidPlanDelta
	}
	expected := mapChangeAuthentication[T]{
		owner: change, key: change.key, before: change.before, after: change.after,
	}
	if change.auth != expected {
		return errInvalidPlanDelta
	}
	if previousKey != "" && strings.Compare(previousKey, change.key) >= 0 {
		return errInvalidPlanDelta
	}
	return nil
}

func validateMapChangeSide[T any](
	authority *Authority,
	collection *snapshotCollection[T],
	kind snapshotCollectionKind,
	key snapshotKey,
	expected *snapshotEntry[T],
) error {
	entry, err := findSnapshotEntry(authority, kind, collection, key)
	if expected == nil {
		if errors.Is(err, errSnapshotEntryNotFound) {
			return nil
		}
		return errInvalidPlanDelta
	}
	if err != nil || entry != expected {
		return errInvalidPlanDelta
	}
	return nil
}

func planDeltaIsStructural(delta *Delta) bool {
	return sequenceChangesStructural(delta.sections) || sectionIdentityChangesStructural(delta.sections) ||
		mapChangesStructural(delta.backends) ||
		mapChangesStructural(delta.profiles) || mapChangesStructural(delta.maps) ||
		mapChangesStructural(delta.crtLists) || sequenceChangesStructural(delta.files) ||
		fileChangesStructural(delta.files)
}

func fileReplacementStructural(before, after *File) bool {
	return before.Path != after.Path || before.Kind != after.Kind ||
		before.ReloadOnChange != after.ReloadOnChange
}

func fileChangesStructural(changes []*sealedSequenceChange[File]) bool {
	for _, change := range changes {
		if change == nil || change.before == nil || change.after == nil {
			continue
		}
		before := snapshotFileMetadata(change.before)
		after := snapshotFileMetadata(change.after)
		if fileReplacementStructural(&before, &after) {
			return true
		}
	}
	return false
}

func detachFileChanges(changes []*sealedSequenceChange[File]) []FileChange {
	result := make([]FileChange, len(changes))
	for index, change := range changes {
		result[index] = FileChange{
			Index: change.index, Before: newFileRecord(change.before), After: newFileRecord(change.after),
		}
	}
	return result
}

func sequenceChangesStructural[T any](changes []*sealedSequenceChange[T]) bool {
	for _, change := range changes {
		if change != nil && (change.before == nil || change.after == nil) {
			return true
		}
	}
	return false
}

func mapChangesStructural[T any](changes []*sealedMapChange[T]) bool {
	for _, change := range changes {
		if change != nil && (change.before == nil || change.after == nil) {
			return true
		}
	}
	return false
}

func detachSequenceChanges[T any](
	changes []*sealedSequenceChange[T],
	own func(T) T,
) []SequenceChange[T] {
	result := make([]SequenceChange[T], len(changes))
	for index, change := range changes {
		result[index].Index = change.index
		if change.before != nil {
			value := own(change.before.value.value)
			result[index].Before = &value
		}
		if change.after != nil {
			value := own(change.after.value.value)
			result[index].After = &value
		}
	}
	return result
}

func detachMapChanges[T any](
	changes []*sealedMapChange[T],
	own func(T) T,
) []NamedChange[T] {
	result := make([]NamedChange[T], len(changes))
	for index, change := range changes {
		result[index].Name = change.key
		if change.before != nil {
			value := own(change.before.value.value)
			result[index].Before = &value
		}
		if change.after != nil {
			value := own(change.after.value.value)
			result[index].After = &value
		}
	}
	return result
}

func samePointers[T any](left, right []*T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
