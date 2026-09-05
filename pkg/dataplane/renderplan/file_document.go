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
	"fmt"
	"io"
	"sync"

	"github.com/cespare/xxhash/v2"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type snapshotDeferredFileAuthentication struct {
	owner       *snapshotDeferredFile
	authority   *Authority
	metadata    File
	document    rendercontent.Document
	digestKnown bool
	memo        *snapshotDeferredFileMemo
}

type snapshotDeferredFile struct {
	authority   *Authority
	metadata    File
	document    rendercontent.Document
	digestKnown bool
	memo        *snapshotDeferredFileMemo
	seal        *snapshotDeferredFile
	auth        snapshotDeferredFileAuthentication
}

type snapshotDeferredFileMemo struct {
	digestOnce sync.Once
	digest     string
	digestErr  error
	fileOnce   sync.Once
	file       File
	fileErr    error
}

// FileDescriptor is plan-file metadata available without materializing content.
type FileDescriptor struct {
	Path           string
	Kind           string
	ReloadOnChange bool
	Size           int64
}

// FileRecord is an authenticated immutable plan-file value.
type FileRecord struct {
	entry *snapshotEntry[File]
	seal  *FileRecord
}

// FileChange is one exact file transition. Nil means absent.
type FileChange struct {
	Index  int
	Before *FileRecord
	After  *FileRecord
}

// NewSnapshotWithConfigDocument owns plan while retaining config as a document.
func NewSnapshotWithConfigDocument(
	authority *Authority,
	plan *Plan,
	document rendercontent.Document,
	previous *Snapshot,
) (*Snapshot, error) {
	if err := document.ValidateAuthentication(); err != nil {
		return nil, errors.Join(errInexactSnapshotPlan, err)
	}
	index, file, err := exactConfigDocumentFile(plan, document)
	if err != nil {
		return nil, err
	}
	snapshot, err := NewSnapshot(authority, plan, previous)
	if err != nil {
		return nil, err
	}
	if snapshot == previous {
		return previous, nil
	}
	current, err := snapshotSequenceEntryAt(
		snapshot.root.files, authority, fileSnapshotCollection, index,
	)
	if err != nil {
		return nil, err
	}
	entry, err := sealSnapshotDocumentFileEntry(
		authority, current.key, &file, document, true,
	)
	if err != nil {
		return nil, err
	}
	root, _, err := replaceSnapshotSequenceNode(
		authority, fileSnapshotCollection, snapshot.root.files.root, index, entry,
	)
	if err != nil {
		return nil, err
	}
	files := sealSnapshotCollection(
		authority, fileSnapshotCollection, snapshot.root.files.present, root,
	)
	planRoot := sealPlanRoot(
		authority, snapshot.root.schema, snapshot.root.id,
		snapshot.root.sections, snapshot.root.backends, snapshot.root.profiles,
		snapshot.root.maps, snapshot.root.crtLists, files,
	)
	return sealSnapshot(authority, planRoot), nil
}

func buildSnapshotFileSequence(
	authority *Authority,
	source []File,
	previous *snapshotCollection[File],
) (*snapshotCollection[File], error) {
	present := source != nil
	exact, err := exactFileSequenceSource(authority, source, previous)
	if err != nil {
		return nil, err
	}
	if exact {
		return previous, nil
	}
	previousEntries, err := snapshotEntries(previous, authority, fileSnapshotCollection)
	if err != nil {
		return nil, err
	}
	entries := make([]*snapshotEntry[File], len(source))
	for index := range source {
		var entry *snapshotEntry[File]
		if index < len(previousEntries) {
			entry = previousEntries[index]
			previousFile, materializeErr := materializeSnapshotFileEntry(entry)
			if materializeErr != nil {
				return nil, materializeErr
			}
			if !exactFile(previousFile, source[index]) {
				entry = nil
			}
		}
		if entry == nil {
			entry = sealSnapshotEntry(
				authority, fileSnapshotCollection, snapshotKey{index: index}, ownFile(source[index]),
			)
		}
		entries[index] = entry
	}
	return buildSnapshotCollection(
		authority, fileSnapshotCollection, present, entries, previous,
	)
}

func exactFileSequenceSource(
	authority *Authority,
	source []File,
	previous *snapshotCollection[File],
) (bool, error) {
	if previous == nil || previous.present != (source != nil) || previous.entries != len(source) {
		return false, nil
	}
	if err := previous.validate(authority, fileSnapshotCollection); err != nil {
		return false, err
	}
	cursor := newSnapshotCursor(previous)
	for index := range source {
		entry, found, err := cursor.next()
		if err != nil || !found {
			return false, err
		}
		file, err := materializeSnapshotFileEntry(entry)
		if err != nil {
			return false, err
		}
		if !exactFile(file, source[index]) {
			return false, nil
		}
	}
	_, found, err := cursor.next()
	return !found, err
}

// Descriptor returns file identity and size without materializing its content.
func (r *FileRecord) Descriptor() (FileDescriptor, error) {
	if err := r.validate(); err != nil {
		return FileDescriptor{}, err
	}
	file := snapshotFileMetadata(r.entry)
	return FileDescriptor{
		Path: file.Path, Kind: file.Kind,
		ReloadOnChange: file.ReloadOnChange, Size: file.Size,
	}, nil
}

// ConfigDocument returns the retained config document when this record has one.
func (r *FileRecord) ConfigDocument() (rendercontent.Document, bool, error) {
	if err := r.validate(); err != nil {
		return rendercontent.Document{}, false, err
	}
	if r.entry.deferredFile == nil {
		return rendercontent.Document{}, false, nil
	}
	return r.entry.deferredFile.document, true, nil
}

// LegacyCopy materializes a detached compatibility file.
func (r *FileRecord) LegacyCopy() (File, error) {
	if err := r.validate(); err != nil {
		return File{}, err
	}
	return materializeSnapshotFileEntry(r.entry)
}

func (r *FileRecord) validate() error {
	if r == nil || r.seal != r || r.entry == nil {
		return errInvalidSnapshot
	}
	return r.entry.validate(r.entry.authority, fileSnapshotCollection)
}

func newFileRecord(entry *snapshotEntry[File]) *FileRecord {
	if entry == nil {
		return nil
	}
	record := &FileRecord{entry: entry}
	record.seal = record
	return record
}

func sealSnapshotDocumentFileEntry(
	authority *Authority,
	key snapshotKey,
	metadata *File,
	document rendercontent.Document,
	digestKnown bool,
) (*snapshotEntry[File], error) {
	if err := authority.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if err := document.ValidateAuthentication(); err != nil {
		return nil, err
	}
	owned := *metadata
	owned.Content = ""
	owned.ContentKnown = true
	if !digestKnown {
		owned.Digest = ""
	}
	deferred := &snapshotDeferredFile{
		authority: authority, metadata: owned, document: document,
		digestKnown: digestKnown, memo: &snapshotDeferredFileMemo{},
	}
	deferred.seal = deferred
	deferred.auth = snapshotDeferredFileAuthentication{
		owner: deferred, authority: authority, metadata: owned,
		document: document, digestKnown: digestKnown, memo: deferred.memo,
	}
	stub := owned
	stub.ContentKnown = false
	value := &snapshotValue[File]{value: stub}
	value.seal = value
	canonical := &canonicalFragment{}
	entry := &snapshotEntry[File]{
		authority: authority, kind: fileSnapshotCollection, key: key,
		value: value, deferredFile: deferred, canonical: canonical,
	}
	entry.seal = entry
	entry.auth = snapshotEntryAuthentication[File]{
		owner: entry, authority: authority, kind: fileSnapshotCollection,
		key: key, value: value, deferredFile: deferred, canonical: canonical,
	}
	return entry, nil
}

func (f *snapshotDeferredFile) validate(authority *Authority) error {
	if f == nil || f.seal != f || f.authority != authority || f.memo == nil {
		return errInvalidSnapshot
	}
	expected := snapshotDeferredFileAuthentication{
		owner: f, authority: f.authority, metadata: f.metadata,
		document: f.document, digestKnown: f.digestKnown, memo: f.memo,
	}
	if f.auth != expected || !f.metadata.ContentKnown || f.metadata.Content != "" ||
		!f.digestKnown && f.metadata.Digest != "" {
		return errInvalidSnapshot
	}
	return f.document.ValidateAuthentication()
}

func (f *snapshotDeferredFile) digestValue() (string, error) {
	if err := f.validate(f.authority); err != nil {
		return "", err
	}
	if f.digestKnown {
		return f.metadata.Digest, nil
	}
	f.memo.digestOnce.Do(func() {
		hasher := xxhash.New()
		written, err := f.document.WriteTo(hasher)
		if err != nil {
			f.memo.digestErr = err
			return
		}
		bytes, err := f.document.Bytes()
		if err != nil || written != int64(bytes) {
			f.memo.digestErr = errors.Join(errInvalidSnapshot, err)
			return
		}
		f.memo.digest = fmt.Sprintf("%016x", hasher.Sum64())
	})
	return f.memo.digest, f.memo.digestErr
}

func (f *snapshotDeferredFile) canonicalFile() (File, error) {
	digest, err := f.digestValue()
	if err != nil {
		return File{}, err
	}
	file := f.metadata
	file.Digest = digest
	file.Content = ""
	file.ContentKnown = false
	return file, nil
}

func (f *snapshotDeferredFile) legacyFile() (File, error) {
	if err := f.validate(f.authority); err != nil {
		return File{}, err
	}
	f.memo.fileOnce.Do(func() {
		content, err := f.document.String()
		if err != nil {
			f.memo.fileErr = err
			return
		}
		digest, err := f.digestValue()
		if err != nil {
			f.memo.fileErr = err
			return
		}
		f.memo.file = f.metadata
		f.memo.file.Content = content
		f.memo.file.ContentKnown = true
		f.memo.file.Digest = digest
	})
	return f.memo.file, f.memo.fileErr
}

func snapshotFileMetadata(entry *snapshotEntry[File]) File {
	if entry.deferredFile != nil {
		return entry.deferredFile.metadata
	}
	return entry.value.value
}

func snapshotDeferredFileStub(file *snapshotDeferredFile) File {
	stub := file.metadata
	stub.ContentKnown = false
	return stub
}

func snapshotFileMatchesDocument(
	entry *snapshotEntry[File],
	document rendercontent.Document,
) (bool, error) {
	if err := entry.validate(entry.authority, fileSnapshotCollection); err != nil {
		return false, err
	}
	bytes, err := document.Bytes()
	if err != nil {
		return false, err
	}
	metadata := snapshotFileMetadata(entry)
	if metadata.Size != int64(bytes) {
		return false, nil
	}
	if entry.deferredFile != nil {
		same, err := entry.deferredFile.document.SameRoot(document)
		if err != nil || !same {
			return same, err
		}
		return true, nil
	}
	writer := &snapshotExactStringWriter{expected: entry.value.value.Content}
	written, err := document.WriteTo(writer)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrShortWrite) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if written != int64(len(entry.value.value.Content)) ||
		writer.offset != len(entry.value.value.Content) {
		return false, nil
	}
	return entry.value.value.Digest == DigestString(entry.value.value.Content), nil
}

func materializeSnapshotFileEntry(entry *snapshotEntry[File]) (File, error) {
	if err := entry.validate(entry.authority, fileSnapshotCollection); err != nil {
		return File{}, err
	}
	if entry.deferredFile == nil {
		return ownFile(entry.value.value), nil
	}
	return entry.deferredFile.legacyFile()
}

func canonicalSnapshotFileEntry(entry *snapshotEntry[File]) (File, error) {
	if err := entry.validate(entry.authority, fileSnapshotCollection); err != nil {
		return File{}, err
	}
	if entry.deferredFile == nil {
		file := ownFile(entry.value.value)
		file.Content = ""
		file.ContentKnown = false
		return file, nil
	}
	return entry.deferredFile.canonicalFile()
}

func exactConfigDocumentFile(
	plan *Plan,
	document rendercontent.Document,
) (int, File, error) {
	index := -1
	var config File
	for candidate := range plan.Files {
		file := plan.Files[candidate]
		if file.Kind != FileKindConfig {
			continue
		}
		if index != -1 || file.Path != ConfigFilePath || !file.ReloadOnChange ||
			!file.ContentKnown {
			return 0, File{}, errInexactSnapshotPlan
		}
		index = candidate
		config = file
	}
	if index == -1 {
		return 0, File{}, errInexactSnapshotPlan
	}
	if config.Size != int64(len(config.Content)) ||
		config.Digest != DigestString(config.Content) {
		return 0, File{}, errInexactSnapshotPlan
	}
	writer := &snapshotExactStringWriter{expected: config.Content}
	written, err := document.WriteTo(writer)
	if err != nil || written != int64(len(config.Content)) || writer.offset != len(config.Content) {
		return 0, File{}, errors.Join(errInexactSnapshotPlan, err)
	}
	return index, config, nil
}

type snapshotExactStringWriter struct {
	expected string
	offset   int
}

func (w *snapshotExactStringWriter) Write(value []byte) (int, error) {
	if len(value) > len(w.expected)-w.offset {
		return 0, io.ErrShortWrite
	}
	for index := range value {
		if value[index] != w.expected[w.offset+index] {
			return 0, io.ErrUnexpectedEOF
		}
	}
	w.offset += len(value)
	return len(value), nil
}

func (w *snapshotExactStringWriter) WriteString(value string) (int, error) {
	if len(value) > len(w.expected)-w.offset ||
		value != w.expected[w.offset:w.offset+len(value)] {
		return 0, io.ErrUnexpectedEOF
	}
	w.offset += len(value)
	return len(value), nil
}

var _ io.StringWriter = (*snapshotExactStringWriter)(nil)

func materializeSnapshotFiles(
	collection *snapshotCollection[File],
	canonical bool,
) ([]File, error) {
	if err := collection.validate(collection.authority, fileSnapshotCollection); err != nil {
		return nil, err
	}
	if !collection.present {
		return nil, nil
	}
	result := make([]File, collection.entries)
	cursor := newSnapshotCursor(collection)
	for index := range result {
		entry, found, err := cursor.next()
		if err != nil || !found {
			return nil, errors.Join(errInvalidSnapshot, err)
		}
		if canonical {
			result[index], err = canonicalSnapshotFileEntry(entry)
		} else {
			result[index], err = materializeSnapshotFileEntry(entry)
		}
		if err != nil {
			return nil, err
		}
	}
	_, found, err := cursor.next()
	if err != nil || found {
		return nil, errors.Join(errInvalidSnapshot, err)
	}
	return result, nil
}

func exactSnapshotFileCollections(
	left, right *snapshotCollection[File],
) (bool, error) {
	if err := left.validate(left.authority, fileSnapshotCollection); err != nil {
		return false, err
	}
	if err := right.validate(right.authority, fileSnapshotCollection); err != nil {
		return false, err
	}
	if left == right {
		return true, nil
	}
	if left.present != right.present || left.entries != right.entries {
		return false, nil
	}
	leftCursor := newSnapshotCursor(left)
	rightCursor := newSnapshotCursor(right)
	for {
		leftEntry, leftFound, err := leftCursor.next()
		if err != nil {
			return false, err
		}
		rightEntry, rightFound, err := rightCursor.next()
		if err != nil || leftFound != rightFound {
			return false, errors.Join(errInvalidSnapshot, err)
		}
		if !leftFound {
			return true, nil
		}
		if leftEntry == rightEntry {
			continue
		}
		leftFile, err := materializeSnapshotFileEntry(leftEntry)
		if err != nil {
			return false, err
		}
		rightFile, err := materializeSnapshotFileEntry(rightEntry)
		if err != nil || !exactFile(leftFile, rightFile) {
			return false, err
		}
	}
}
