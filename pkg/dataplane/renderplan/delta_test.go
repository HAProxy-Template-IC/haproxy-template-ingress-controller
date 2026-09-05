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
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestPlanDeltaReplacesRecordsAndPreservesLegacyIdentity(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(16)
	base := mustPlanSnapshot(t, authority, source, nil)
	oracle, replaced := mutatedPlanReplacementOracle(source)

	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	applyPlanReplacements(t, base, transaction, &replaced)
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	require.NoError(t, delta.ValidateAuthentication())
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.False(t, structural)
	assert.NotSame(t, base, next)

	changes, err := delta.Changes()
	require.NoError(t, err)
	assert.Len(t, changes.Sections, 1)
	assert.Len(t, changes.Backends, 1)
	assert.Len(t, changes.Profiles, 1)
	assert.Len(t, changes.Maps, 1)
	assert.Len(t, changes.CRTLists, 1)
	assert.Len(t, changes.Files, 1)
	assert.Equal(t, 4, changes.Sections[0].Index)
	assert.Equal(t, "backend-000004", changes.Backends[0].Name)

	id, err := next.ID()
	require.NoError(t, err)
	assert.Equal(t, oracle.ID, id)
	legacy, err := next.LegacyCopy()
	require.NoError(t, err)
	assert.True(t, ExactlyEqual(oracle, legacy))
	assert.Equal(t, oracle.ID, legacy.ID)

	assert.NotSame(t, base.root.sections, next.root.sections)
	assert.NotSame(t, base.root.backends, next.root.backends)
	assert.NotSame(t, base.root.profiles, next.root.profiles)
	assert.NotSame(t, base.root.maps, next.root.maps)
	assert.NotSame(t, base.root.crtLists, next.root.crtLists)
	assert.NotSame(t, base.root.files, next.root.files)
}

type planReplacements struct {
	section     Section
	backend     Backend
	profile     Profile
	declaredMap Map
	crtList     CRTList
	file        File
}

func mutatedPlanReplacementOracle(source *Plan) (*Plan, planReplacements) {
	oracle := source.Clone()
	section := oracle.Sections[4]
	section.Text = "backend backend-000004\n    server changed 192.0.2.4:8080\n"
	section.Length = len(section.Text)
	section.TextDigest = DigestString(section.Text)
	oracle.Sections[4] = section
	backend := oracle.Backends["backend-000004"]
	backend.Servers[0].Address = "192.0.2.4"
	backend.TextDigest = section.TextDigest
	oracle.Backends[backend.Name] = backend
	profile := oracle.Profiles["profile"]
	profile.HasRules = false
	oracle.Profiles[profile.Name] = profile
	declaredMap := oracle.Maps["maps/route-000004.map"]
	declaredMap.Ordered = !declaredMap.Ordered
	oracle.Maps[declaredMap.Path] = declaredMap
	crtList := oracle.CRTLists["crt-lists/frontend-000004.list"]
	crtList.Entries[0].SNIFilters[0] = "changed.example"
	oracle.CRTLists[crtList.Path] = crtList
	file := oracle.Files[5]
	file.Content = "changed.example backend-000004\n"
	file.Size = int64(len(file.Content))
	file.Digest = DigestString(file.Content)
	oracle.Files[5] = file
	oracle.ComputeID()
	return oracle, planReplacements{
		section: section, backend: backend, profile: profile,
		declaredMap: declaredMap, crtList: crtList, file: file,
	}
}

func applyPlanReplacements(
	t *testing.T,
	base *Snapshot,
	transaction *Transaction,
	replaced *planReplacements,
) {
	t.Helper()
	sectionHandle, err := base.SectionHandle(4)
	require.NoError(t, err)
	backendHandle, found, err := base.BackendHandle("backend-000004")
	require.NoError(t, err)
	require.True(t, found)
	profileHandle, found, err := base.ProfileHandle("profile")
	require.NoError(t, err)
	require.True(t, found)
	mapHandle, found, err := base.MapHandle("maps/route-000004.map")
	require.NoError(t, err)
	require.True(t, found)
	crtListHandle, found, err := base.CRTListHandle("crt-lists/frontend-000004.list")
	require.NoError(t, err)
	require.True(t, found)
	fileHandle, err := base.FileHandle(5)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceSection(sectionHandle, replaced.section))
	require.NoError(t, transaction.ReplaceBackend(backendHandle, replaced.backend))
	require.NoError(t, transaction.ReplaceProfile(profileHandle, replaced.profile))
	require.NoError(t, transaction.ReplaceMap(mapHandle, replaced.declaredMap))
	require.NoError(t, transaction.ReplaceCRTList(crtListHandle, replaced.crtList))
	require.NoError(t, transaction.ReplaceFile(fileHandle, replaced.file))
}

func TestPlanDeltaLazyV1IDIsConcurrencySafe(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(300)
	base := mustPlanSnapshot(t, authority, source, nil)
	handle, found, err := base.BackendHandle("backend-000150")
	require.NoError(t, err)
	require.True(t, found)
	backend := ownBackend(source.Backends["backend-000150"])
	backend.Servers[0].Address = "192.0.2.150"
	oracle := source.Clone()
	oracle.Backends[backend.Name] = backend
	oracle.ComputeID()

	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceBackend(handle, backend))
	next, _, err := transaction.Commit()
	require.NoError(t, err)
	require.True(t, next.root.deferredID)

	const readers = 32
	start := make(chan struct{})
	errorsByReader := make(chan error, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			<-start
			id, readErr := next.ID()
			if readErr != nil {
				errorsByReader <- readErr
				return
			}
			if id != oracle.ID {
				errorsByReader <- fmt.Errorf("ID = %q, want %q", id, oracle.ID)
				return
			}
			legacy, readErr := next.LegacyCopy()
			if readErr != nil {
				errorsByReader <- readErr
				return
			}
			if legacy.ID != oracle.ID || !ExactlyEqual(legacy, oracle) {
				errorsByReader <- fmt.Errorf("legacy plan differs from v1 oracle")
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByReader)
	for readErr := range errorsByReader {
		require.NoError(t, readErr)
	}
}

func TestPlanConfigDocumentDeltaDefersContentAndPreservesV1Identity(t *testing.T) {
	fixture := newConfigDocumentDeltaFixture(t, 64, 32)
	fileHandle, err := fixture.base.FileHandle(0)
	require.NoError(t, err)

	noOp, err := BeginTransaction(fixture.authority, fixture.base)
	require.NoError(t, err)
	require.NoError(t, noOp.ReplaceConfigFileDocument(fileHandle, fixture.document))
	noOpNext, noOpDelta, err := noOp.Commit()
	require.NoError(t, err)
	assert.Same(t, fixture.base, noOpNext)
	same, err := noOpDelta.SameRoot()
	require.NoError(t, err)
	assert.True(t, same)

	changes, err := fixture.delta.Changes()
	require.NoError(t, err)
	require.Len(t, changes.Files, 1)
	descriptor, err := changes.Files[0].After.Descriptor()
	require.NoError(t, err)
	bytes, err := fixture.nextDocument.Bytes()
	require.NoError(t, err)
	assert.Equal(t, int64(bytes), descriptor.Size)
	retained, found, err := changes.Files[0].After.ConfigDocument()
	require.NoError(t, err)
	require.True(t, found)
	same, err = retained.SameRoot(fixture.nextDocument)
	require.NoError(t, err)
	assert.True(t, same)

	copiedRecord := *changes.Files[0].After
	_, err = copiedRecord.Descriptor()
	require.ErrorIs(t, err, errInvalidSnapshot)

	entry, err := snapshotSequenceEntryAt(
		fixture.next.root.files, fixture.authority, fileSnapshotCollection, 0,
	)
	require.NoError(t, err)
	require.NotNil(t, entry.deferredFile)
	assert.False(t, entry.deferredFile.digestKnown)
	assert.Empty(t, entry.deferredFile.memo.digest)
	assert.Equal(t, File{}, entry.deferredFile.memo.file)
	require.True(t, fixture.next.root.deferredID)

	id, err := fixture.next.ID()
	require.NoError(t, err)
	assert.Equal(t, fixture.oracle.ID, id)
	assert.NotEmpty(t, entry.deferredFile.memo.digest)
	assert.Equal(t, File{}, entry.deferredFile.memo.file)

	assertConfigDocumentLegacyCopyDetached(t, &fixture)
}

func assertConfigDocumentLegacyCopyDetached(t *testing.T, fixture *configDocumentDeltaFixture) {
	t.Helper()
	legacy, err := fixture.next.LegacyCopy()
	require.NoError(t, err)
	assert.True(t, ExactlyEqual(fixture.oracle, legacy))
	assert.Equal(t, fixture.oracle.ID, legacy.ID)
	legacy.Files[0].Content = "caller poison"
	again, err := fixture.next.LegacyCopy()
	require.NoError(t, err)
	assert.True(t, ExactlyEqual(fixture.oracle, again))

	fixture.source.Files[0].Content = "source poison"
	afterSourcePoison, err := fixture.base.LegacyCopy()
	require.NoError(t, err)
	assert.Equal(t, fixture.config, afterSourcePoison.Files[0].Content)
}

func TestPlanConfigDocumentDeltaRejectsStaleAndABAHandles(t *testing.T) {
	fixture := newConfigDocumentDeltaFixture(t, 8, 4)
	stale, err := fixture.base.FileHandle(0)
	require.NoError(t, err)
	nextHandle, err := fixture.next.FileHandle(0)
	require.NoError(t, err)
	nextSection, err := fixture.next.SectionHandle(fixture.changedIndex)
	require.NoError(t, err)

	documentHandle, err := fixture.nextDocument.LeafHandle(fixture.changedIndex)
	require.NoError(t, err)
	documentTransaction, err := fixture.nextDocument.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, documentTransaction.ReplaceText(
		documentHandle, fixture.source.Sections[fixture.changedIndex].Text,
	))
	revertedDocument, _, err := documentTransaction.Commit()
	require.NoError(t, err)

	transaction, err := BeginTransaction(fixture.authority, fixture.next)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceSection(
		nextSection, fixture.source.Sections[fixture.changedIndex],
	))
	require.NoError(t, transaction.ReplaceConfigFileDocument(nextHandle, revertedDocument))
	reverted, _, err := transaction.Commit()
	require.NoError(t, err)
	exact, err := fixture.base.ExactEqual(reverted)
	require.NoError(t, err)
	assert.True(t, exact)
	assert.NotSame(t, fixture.base, reverted)

	aba, err := BeginTransaction(fixture.authority, reverted)
	require.NoError(t, err)
	require.ErrorIs(t, aba.ReplaceConfigFileDocument(stale, fixture.nextDocument), errInvalidPlanHandle)
}

func TestPlanConfigDocumentLazyCompatibilityIsConcurrencySafe(t *testing.T) {
	fixture := newConfigDocumentDeltaFixture(t, 300, 150)

	const readers = 32
	start := make(chan struct{})
	errorsByReader := make(chan error, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			<-start
			id, err := fixture.next.ID()
			if err == nil && id != fixture.oracle.ID {
				err = fmt.Errorf("ID = %q, want %q", id, fixture.oracle.ID)
			}
			var legacy *Plan
			if err == nil {
				legacy, err = fixture.next.LegacyCopy()
			}
			if err == nil && !ExactlyEqual(fixture.oracle, legacy) {
				err = fmt.Errorf("legacy plan differs from v1 oracle")
			}
			if err != nil {
				errorsByReader <- err
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByReader)
	for err := range errorsByReader {
		require.NoError(t, err)
	}
}

func TestNewSnapshotWithConfigDocumentRejectsInexactBindings(t *testing.T) {
	source, document, _ := configDocumentPlanFixture(t, 4)
	authority := NewAuthority()

	wrongDigest := source.Clone()
	wrongDigest.Files[0].Digest = "wrong"
	_, err := NewSnapshotWithConfigDocument(authority, wrongDigest, document, nil)
	require.ErrorIs(t, err, errInexactSnapshotPlan)

	wrongSize := source.Clone()
	wrongSize.Files[0].Size++
	_, err = NewSnapshotWithConfigDocument(authority, wrongSize, document, nil)
	require.ErrorIs(t, err, errInexactSnapshotPlan)

	var builder rendercontent.DocumentBuilder
	_, err = builder.WriteString("different")
	require.NoError(t, err)
	different, err := builder.Build(nil)
	require.NoError(t, err)
	_, err = NewSnapshotWithConfigDocument(authority, source, different, nil)
	require.ErrorIs(t, err, errInexactSnapshotPlan)

	_, err = NewSnapshotWithConfigDocument(authority, source, rendercontent.Document{}, nil)
	require.Error(t, err)
}

func TestPlanDeltaNoopAndStructuralChanges(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(3)
	base := mustPlanSnapshot(t, authority, source, nil)
	handle, found, err := base.BackendHandle("backend-000001")
	require.NoError(t, err)
	require.True(t, found)

	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceBackend(handle, source.Backends["backend-000001"]))
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	assert.Same(t, base, next)
	same, err := delta.SameRoot()
	require.NoError(t, err)
	assert.True(t, same)
	changes, err := delta.Changes()
	require.NoError(t, err)
	assert.Empty(t, changes.Backends)

	gap, err := base.SectionGapHandle(1)
	require.NoError(t, err)
	transaction, err = BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.InsertSection(gap, Section{
		Kind: SectionKindCore, Name: "inserted", Text: "inserted\n", TextKnown: true,
		Length: len("inserted\n"), TextDigest: DigestString("inserted\n"),
	}))
	_, delta, err = transaction.Commit()
	require.NoError(t, err)
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.True(t, structural)
}

func TestPlanDeltaMarksFileDescriptorReplacementStructural(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(2)
	base := mustPlanSnapshot(t, authority, source, nil)
	handle, err := base.FileHandle(0)
	require.NoError(t, err)
	replacement := source.Files[0]
	replacement.ReloadOnChange = !replacement.ReloadOnChange
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceFile(handle, replacement))
	_, delta, err := transaction.Commit()
	require.NoError(t, err)
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.True(t, structural)
}

func TestPlanDeltaRejectsCopiedForeignStaleAndABAProofs(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(4)
	base := mustPlanSnapshot(t, authority, source, nil)
	handle, found, err := base.BackendHandle("backend-000002")
	require.NoError(t, err)
	require.True(t, found)
	original := ownBackend(source.Backends["backend-000002"])
	changed := ownBackend(original)
	changed.Servers[0].Address = "192.0.2.2"

	copiedHandle := *handle
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.ErrorIs(t, transaction.ReplaceBackend(&copiedHandle, changed), errInvalidPlanHandle)
	_, _, err = transaction.Commit()
	require.ErrorIs(t, err, errInvalidPlanHandle)

	foreign := mustPlanSnapshot(t, NewAuthority(), source.Clone(), nil)
	foreignHandle, found, err := foreign.BackendHandle("backend-000002")
	require.NoError(t, err)
	require.True(t, found)
	transaction, err = BeginTransaction(authority, base)
	require.NoError(t, err)
	require.ErrorIs(t, transaction.ReplaceBackend(foreignHandle, changed), errInvalidPlanHandle)

	transaction, err = BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceBackend(handle, changed))
	next, delta, err := transaction.Commit()
	require.NoError(t, err)

	assertStaleAndCopiedPlanProofsRejected(t, authority, next, handle, &changed, delta)

	changedHandle, found, err := next.BackendHandle("backend-000002")
	require.NoError(t, err)
	require.True(t, found)
	backTransaction, err := BeginTransaction(authority, next)
	require.NoError(t, err)
	require.NoError(t, backTransaction.ReplaceBackend(changedHandle, original))
	reverted, _, err := backTransaction.Commit()
	require.NoError(t, err)
	exact, err := base.ExactEqual(reverted)
	require.NoError(t, err)
	assert.True(t, exact)
	assert.NotSame(t, base, reverted)
	abaTransaction, err := BeginTransaction(authority, reverted)
	require.NoError(t, err)
	require.ErrorIs(t, abaTransaction.ReplaceBackend(handle, changed), errInvalidPlanHandle)

	originalIndex := delta.backends[0].key
	delta.backends[0].key = "tampered"
	require.ErrorIs(t, delta.ValidateAuthentication(), errInvalidPlanDelta)
	delta.backends[0].key = originalIndex
	require.NoError(t, delta.ValidateAuthentication())
}

func assertStaleAndCopiedPlanProofsRejected(
	t *testing.T,
	authority *Authority,
	next *Snapshot,
	handle *BackendHandle,
	changed *Backend,
	delta *Delta,
) {
	t.Helper()
	staleTransaction, err := BeginTransaction(authority, next)
	require.NoError(t, err)
	require.ErrorIs(t, staleTransaction.ReplaceBackend(handle, *changed), errInvalidPlanHandle)

	copiedTransaction := &Transaction{
		authority: staleTransaction.authority,
		base:      staleTransaction.base,
		sections:  staleTransaction.sections,
		backends:  staleTransaction.backends,
		profiles:  staleTransaction.profiles,
		maps:      staleTransaction.maps,
		crtLists:  staleTransaction.crtLists,
		files:     staleTransaction.files,
		seal:      staleTransaction,
		auth:      staleTransaction.auth,
	}
	_, _, err = copiedTransaction.Commit()
	require.ErrorIs(t, err, errInvalidPlanTransaction)
	copiedDelta := *delta
	require.ErrorIs(t, copiedDelta.ValidateAuthentication(), errInvalidPlanDelta)
}

func TestPlanDeltaChangesAreDetached(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(2)
	base := mustPlanSnapshot(t, authority, source, nil)
	handle, found, err := base.BackendHandle("backend-000001")
	require.NoError(t, err)
	require.True(t, found)
	backend := ownBackend(source.Backends["backend-000001"])
	backend.Servers[0].Address = "192.0.2.1"

	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceBackend(handle, backend))
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	first, err := delta.Changes()
	require.NoError(t, err)
	first.Backends[0].Name = "poison"
	first.Backends[0].After.Name = "poison"
	first.Backends[0].After.Servers[0].Address = "poison"
	second, err := delta.Changes()
	require.NoError(t, err)
	assert.Equal(t, "backend-000001", second.Backends[0].Name)
	assert.Equal(t, "backend-000001", second.Backends[0].After.Name)
	assert.Equal(t, "192.0.2.1", second.Backends[0].After.Servers[0].Address)
	legacy, err := next.LegacyCopy()
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.1", legacy.Backends["backend-000001"].Servers[0].Address)
}

func TestPlanDeltaSiblingTransactionsAreIndependent(t *testing.T) {
	authority := NewAuthority()
	source := snapshotPlanFixture(32)
	base := mustPlanSnapshot(t, authority, source, nil)

	const siblings = 16
	results := make(chan *Snapshot, siblings)
	errorsBySibling := make(chan error, siblings)
	var wait sync.WaitGroup
	wait.Add(siblings)
	for index := range siblings {
		go func() {
			defer wait.Done()
			name := fmt.Sprintf("backend-%06d", index)
			handle, found, err := base.BackendHandle(name)
			if err != nil || !found {
				errorsBySibling <- errorsOrAbsent(err)
				return
			}
			backend := ownBackend(source.Backends[name])
			backend.Servers[0].Address = fmt.Sprintf("192.0.2.%d", index)
			transaction, err := BeginTransaction(authority, base)
			if err == nil {
				err = transaction.ReplaceBackend(handle, backend)
			}
			var next *Snapshot
			if err == nil {
				next, _, err = transaction.Commit()
			}
			if err != nil {
				errorsBySibling <- err
				return
			}
			results <- next
		}()
	}
	wait.Wait()
	close(results)
	close(errorsBySibling)
	for siblingErr := range errorsBySibling {
		require.NoError(t, siblingErr)
	}
	seen := make(map[*Snapshot]struct{}, siblings)
	for result := range results {
		seen[result] = struct{}{}
	}
	assert.Len(t, seen, siblings)
}

func errorsOrAbsent(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("record is absent")
}

type configDocumentDeltaFixture struct {
	authority    *Authority
	source       *Plan
	config       string
	document     rendercontent.Document
	nextDocument rendercontent.Document
	base         *Snapshot
	next         *Snapshot
	delta        *Delta
	oracle       *Plan
	changedIndex int
}

func newConfigDocumentDeltaFixture(
	tb testing.TB,
	count, changedIndex int,
) configDocumentDeltaFixture {
	tb.Helper()
	source, document, config := configDocumentPlanFixture(tb, count)
	require.GreaterOrEqual(tb, changedIndex, 0)
	require.Less(tb, changedIndex, count)
	authority := NewAuthority()
	base, err := NewSnapshotWithConfigDocument(authority, source, document, nil)
	require.NoError(tb, err)

	section := source.Sections[changedIndex]
	section.Text = fmt.Sprintf("global changed-%06d value\n", changedIndex)
	section.Length = len(section.Text)
	section.TextDigest = DigestString(section.Text)
	documentHandle, err := document.LeafHandle(changedIndex)
	require.NoError(tb, err)
	documentTransaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, documentTransaction.ReplaceText(documentHandle, section.Text))
	nextDocument, _, err := documentTransaction.Commit()
	require.NoError(tb, err)

	sectionHandle, err := base.SectionHandle(changedIndex)
	require.NoError(tb, err)
	fileHandle, err := base.FileHandle(0)
	require.NoError(tb, err)
	transaction, err := BeginTransaction(authority, base)
	require.NoError(tb, err)
	require.NoError(tb, transaction.ReplaceSection(sectionHandle, section))
	require.NoError(tb, transaction.ReplaceConfigFileDocument(fileHandle, nextDocument))
	next, delta, err := transaction.Commit()
	require.NoError(tb, err)

	oracle := source.Clone()
	oracle.Sections[changedIndex] = section
	nextConfig, err := nextDocument.String()
	require.NoError(tb, err)
	oracle.Files[0] = configDocumentFile(nextConfig)
	oracle.ComputeID()
	return configDocumentDeltaFixture{
		authority: authority, source: source, config: config,
		document: document, nextDocument: nextDocument,
		base: base, next: next, delta: delta, oracle: oracle,
		changedIndex: changedIndex,
	}
}

func configDocumentPlanFixture(
	tb testing.TB,
	count int,
) (*Plan, rendercontent.Document, string) {
	tb.Helper()
	plan := &Plan{SchemaVersion: SchemaVersion, Sections: make([]Section, count)}
	var config strings.Builder
	var builder rendercontent.DocumentBuilder
	for index := range count {
		text := fmt.Sprintf("global setting-%06d value\n", index)
		plan.Sections[index] = Section{
			Kind: SectionKindCore, Name: fmt.Sprintf("core#%d", index),
			TextDigest: DigestString(text), Length: len(text), Text: text, TextKnown: true,
		}
		config.WriteString(text)
		var childBuilder rendercontent.DocumentBuilder
		_, err := childBuilder.WriteString(text)
		require.NoError(tb, err)
		child, err := childBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, builder.AppendDocument(child))
	}
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	value := config.String()
	plan.Files = []File{configDocumentFile(value)}
	plan.ComputeID()
	return plan, document, value
}

func configDocumentFile(content string) File {
	return File{
		Path: ConfigFilePath, Kind: FileKindConfig, ReloadOnChange: true,
		Digest: DigestString(content), Size: int64(len(content)),
		Content: content, ContentKnown: true,
	}
}

func BenchmarkPlanDeltaReplaceOneOf3000(b *testing.B) {
	authority := NewAuthority()
	source := snapshotPlanFixture(3000)
	base := mustPlanSnapshot(b, authority, source, nil)
	handle, found, err := base.BackendHandle("backend-001500")
	require.NoError(b, err)
	require.True(b, found)
	backend := ownBackend(source.Backends["backend-001500"])
	backend.Servers[0].Address = "192.0.2.150"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		transaction, beginErr := BeginTransaction(authority, base)
		if beginErr != nil {
			b.Fatal(beginErr)
		}
		if replaceErr := transaction.ReplaceBackend(handle, backend); replaceErr != nil {
			b.Fatal(replaceErr)
		}
		next, delta, commitErr := transaction.Commit()
		if commitErr != nil {
			b.Fatal(commitErr)
		}
		if next == nil || delta == nil {
			b.Fatal("nil delta result")
		}
	}
}

func BenchmarkPlanDeltaReplaceConfigDocumentLeafOf3000(b *testing.B) {
	fixture := newConfigDocumentDeltaFixture(b, 3000, 1500)
	sectionHandle, err := fixture.base.SectionHandle(fixture.changedIndex)
	require.NoError(b, err)
	fileHandle, err := fixture.base.FileHandle(0)
	require.NoError(b, err)
	section := fixture.oracle.Sections[fixture.changedIndex]

	for _, variant := range []struct{ name, compatibility string }{
		{name: "publish"},
		{name: "publish-and-v1-id", compatibility: "id"},
		{name: "publish-and-legacy-copy", compatibility: "legacy"},
	} {
		b.Run(variant.name, func(b *testing.B) {
			runPlanDeltaConfigDocumentBenchmark(
				b, &fixture, sectionHandle, fileHandle, &section, variant.compatibility,
			)
		})
	}
}

func runPlanDeltaConfigDocumentBenchmark(
	b *testing.B,
	fixture *configDocumentDeltaFixture,
	sectionHandle *SectionHandle,
	fileHandle *FileHandle,
	section *Section,
	compatibility string,
) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		transaction, beginErr := BeginTransaction(fixture.authority, fixture.base)
		if beginErr != nil {
			b.Fatal(beginErr)
		}
		if replaceErr := transaction.ReplaceSection(sectionHandle, *section); replaceErr != nil {
			b.Fatal(replaceErr)
		}
		if replaceErr := transaction.ReplaceConfigFileDocument(
			fileHandle, fixture.nextDocument,
		); replaceErr != nil {
			b.Fatal(replaceErr)
		}
		next, delta, commitErr := transaction.Commit()
		if commitErr != nil {
			b.Fatal(commitErr)
		}
		switch compatibility {
		case "id":
			benchmarkPlanDeltaIDSink, commitErr = next.ID()
		case "legacy":
			benchmarkPlanLegacySink, commitErr = next.LegacyCopy()
		default:
			benchmarkPlanSnapshotSink = next
		}
		if commitErr != nil || delta == nil {
			b.Fatal(commitErr)
		}
	}
}

var benchmarkPlanDeltaIDSink string
