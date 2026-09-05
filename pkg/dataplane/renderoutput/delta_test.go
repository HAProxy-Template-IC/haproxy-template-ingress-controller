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

package renderoutput

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type outputChildDeltas struct {
	document      *rendercontent.DocumentDelta
	plan          *renderplan.Delta
	artifacts     *renderartifact.Delta
	nextPlan      *renderplan.Snapshot
	nextArtifacts *renderartifact.Snapshot
}

func TestOutputTransactionPublishesMapDeltaAtomically(t *testing.T) {
	fixture := newScaleOutputFixture(t, 32)
	base := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	changedIndex := 16
	changedContent := "changed.example changed-backend\n"
	deltas := mapOutputDeltas(t, &fixture, base, changedIndex, changedContent)

	transaction, err := BeginTransaction(
		fixture.authority, base, deltas.document, deltas.plan, deltas.artifacts,
	)
	require.NoError(t, err)
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	require.NoError(t, next.ValidateAuthentication())
	require.NoError(t, delta.ValidateAuthentication())
	assert.NotSame(t, base, next)
	assert.True(t, next.root.deferredCompatibility)

	oraclePlan := fixture.plan.Clone()
	path := fixture.specs[changedIndex].descriptor.RuntimePath
	oraclePlan.Maps[path] = renderplan.Map{
		Path: path, Ordered: true, Entries: renderplan.ParseMapEntries(changedContent),
	}
	oraclePlan.Files[planFileIndex(t, oraclePlan, path)] = exactPlanFile(
		path, renderplan.FileKindMap, false, changedContent,
	)
	oraclePlan.ComputeID()
	planID, err := next.PlanID()
	require.NoError(t, err)
	assert.Equal(t, oraclePlan.ID, planID)
	wantChecksum, err := dataplane.ComputeSnapshotContentChecksum(fixture.config, deltas.nextArtifacts)
	require.NoError(t, err)
	checksum, err := next.ContentChecksum()
	require.NoError(t, err)
	assert.Equal(t, wantChecksum, checksum)

	applied, err := delta.Apply(base)
	require.NoError(t, err)
	assert.Same(t, next, applied)
	again, againDelta, err := transaction.Commit()
	require.NoError(t, err)
	assert.Same(t, next, again)
	assert.Same(t, delta, againDelta)
}

func TestOutputTransactionPublishesAlignedConfigDelta(t *testing.T) {
	fixture := newOutputFixture(t)
	document := alignedSectionDocument(t, fixture.plan.Sections)
	base, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	require.NoError(t, err)
	require.True(t, base.root.config.sectionAligned)
	change := newAlignedConfigChange(t, &fixture, base, document)

	transaction, err := BeginTransaction(
		fixture.authority, base, change.documentDelta, change.planDelta,
		noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
	)
	require.NoError(t, err)
	next, _, err := transaction.Commit()
	require.NoError(t, err)
	assert.Empty(t, next.root.config.memo.value)
	assert.True(t, next.root.config.deferredDigest)
	same, err := change.nextDocument.SameRoot(next.root.config.document)
	require.NoError(t, err)
	assert.True(t, same)
	planID, err := next.PlanID()
	require.NoError(t, err)
	assert.Equal(t, change.oracle.ID, planID)
	wantChecksum, err := dataplane.ComputeSnapshotContentChecksum(change.config, fixture.artifacts)
	require.NoError(t, err)
	checksum, err := next.ContentChecksum()
	require.NoError(t, err)
	assert.Equal(t, wantChecksum, checksum)
	assert.Empty(t, next.root.config.memo.value)
	gotConfig, err := next.Config()
	require.NoError(t, err)
	assert.Equal(t, change.config, gotConfig)
}

type alignedConfigChange struct {
	oracle        *renderplan.Plan
	config        string
	nextDocument  rendercontent.Document
	documentDelta *rendercontent.DocumentDelta
	planDelta     *renderplan.Delta
}

func newAlignedConfigChange(
	tb testing.TB,
	fixture *outputFixture,
	base *Snapshot,
	document rendercontent.Document,
) alignedConfigChange {
	tb.Helper()
	oracle := fixture.plan.Clone()
	section := oracle.Sections[2]
	section.Text = "backend be_app\n    server s1 192.0.2.10:80\n"
	section.Length = len(section.Text)
	section.TextDigest = renderplan.DigestString(section.Text)
	oracle.Sections[2] = section
	backend := oracle.Backends["be_app"]
	backend.Servers[0].Address = "192.0.2.10"
	backend.Body = []string{"server s1 192.0.2.10:80"}
	backend.BodyDigest = renderplan.DigestString(strings.Join(backend.Body, "\n"))
	backend.TextDigest = section.TextDigest
	backend.RecordDigest = backendRecordDigest(&backend)
	oracle.Backends[backend.Name] = backend
	config := oracle.Sections[0].Text + oracle.Sections[1].Text + oracle.Sections[2].Text
	configIndex := planFileIndex(tb, oracle, renderplan.ConfigFilePath)
	oracle.Files[configIndex] = exactPlanFile(
		renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config,
	)
	oracle.ComputeID()

	documentHandle, err := document.LeafHandle(2)
	require.NoError(tb, err)
	documentTransaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, documentTransaction.ReplaceText(documentHandle, section.Text))
	nextDocument, documentDelta, err := documentTransaction.Commit()
	require.NoError(tb, err)
	planSnapshot, err := base.PlanSnapshot()
	require.NoError(tb, err)
	sectionHandle, err := planSnapshot.SectionHandle(2)
	require.NoError(tb, err)
	backendHandle, found, err := planSnapshot.BackendHandle("be_app")
	require.NoError(tb, err)
	require.True(tb, found)
	fileHandle, err := planSnapshot.FileHandle(configIndex)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.ReplaceSection(sectionHandle, section))
	require.NoError(tb, planTransaction.ReplaceBackend(backendHandle, backend))
	require.NoError(tb, planTransaction.ReplaceConfigFileDocument(fileHandle, nextDocument))
	_, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	return alignedConfigChange{
		oracle: oracle, config: config, nextDocument: nextDocument,
		documentDelta: documentDelta, planDelta: planDelta,
	}
}

func TestOutputTransactionRejectsEqualBytesFromUnboundConfigDocument(t *testing.T) {
	fixture := newOutputConfigDeltaFixture(t, 16, 8)
	config, err := fixture.nextDocument.String()
	require.NoError(t, err)
	unboundDocument, err := configDocumentFromString(config)
	require.NoError(t, err)
	same, err := fixture.nextDocument.SameRoot(unboundDocument)
	require.NoError(t, err)
	assert.False(t, same)

	planSnapshot, err := fixture.base.PlanSnapshot()
	require.NoError(t, err)
	sectionHandle, err := planSnapshot.SectionHandle(fixture.changedIndex)
	require.NoError(t, err)
	fileHandle, err := planSnapshot.FileHandle(0)
	require.NoError(t, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(t, err)
	require.NoError(t, planTransaction.ReplaceSection(sectionHandle, fixture.changedSection))
	require.NoError(t, planTransaction.ReplaceConfigFileDocument(fileHandle, unboundDocument))
	_, unboundPlanDelta, err := planTransaction.Commit()
	require.NoError(t, err)

	transaction, err := BeginTransaction(
		fixture.authority, fixture.base,
		fixture.documentDelta, unboundPlanDelta, fixture.artifactDelta,
	)
	require.NoError(t, err)
	_, _, err = transaction.Commit()
	require.ErrorContains(t, err, "does not match the rendered config")
	require.NoError(t, fixture.base.ValidateAuthentication())
}

func TestOutputTransactionNoopReusesExactRoot(t *testing.T) {
	fixture := newScaleOutputFixture(t, 3)
	base := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	documentDelta := noOpDocumentDelta(t, base)
	planDelta := noOpPlanDelta(t, fixture.planAuthority, base)
	artifactDelta := noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts)
	transaction, err := BeginTransaction(
		fixture.authority, base, documentDelta, planDelta, artifactDelta,
	)
	require.NoError(t, err)
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	assert.Same(t, base, next)
	same, err := delta.SameRoot()
	require.NoError(t, err)
	assert.True(t, same)
}

func TestOutputTransactionRejectsIncompleteChanges(t *testing.T) {
	fixture := newScaleOutputFixture(t, 4)
	base := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	deltas := mapOutputDeltas(t, &fixture, base, 2, "changed.example backend\n")
	noArtifacts := noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts)
	transaction, err := BeginTransaction(
		fixture.authority, base, deltas.document, deltas.plan, noArtifacts,
	)
	require.NoError(t, err)
	_, _, err = transaction.Commit()
	require.ErrorContains(t, err, "without its artifact")
	require.NoError(t, base.ValidateAuthentication())

	noPlan := noOpPlanDelta(t, fixture.planAuthority, base)
	transaction, err = BeginTransaction(
		fixture.authority, base, deltas.document, noPlan, deltas.artifacts,
	)
	require.NoError(t, err)
	_, _, err = transaction.Commit()
	require.ErrorContains(t, err, "without its plan file")

	planSnapshot, err := base.PlanSnapshot()
	require.NoError(t, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(t, err)
	require.NoError(t, planTransaction.InsertMap("maps/inserted.map", renderplan.Map{
		Path: "maps/inserted.map",
	}))
	_, structuralPlan, err := planTransaction.Commit()
	require.NoError(t, err)
	transaction, err = BeginTransaction(
		fixture.authority, base, deltas.document, structuralPlan, noArtifacts,
	)
	require.NoError(t, err)
	_, _, err = transaction.Commit()
	require.ErrorContains(t, err, "does not match its file")
}

func TestOutputTransactionRejectsForeignCopiedTamperedAndABABindings(t *testing.T) {
	fixture := newScaleOutputFixture(t, 4)
	base := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	deltas := mapOutputDeltas(t, &fixture, base, 1, "changed.example backend\n")
	transaction, err := BeginTransaction(
		fixture.authority, base, deltas.document, deltas.plan, deltas.artifacts,
	)
	require.NoError(t, err)
	next, delta, err := transaction.Commit()
	require.NoError(t, err)

	copiedTransaction := &Transaction{
		authority: transaction.authority, base: transaction.base,
		document: transaction.document, plan: transaction.plan, artifacts: transaction.artifacts,
		nextDocument: transaction.nextDocument, nextPlan: transaction.nextPlan,
		nextArtifacts: transaction.nextArtifacts, seal: transaction, auth: transaction.auth,
	}
	_, _, err = copiedTransaction.Commit()
	require.ErrorIs(t, err, errInvalidOutputTransaction)
	copiedDelta := *delta
	require.ErrorIs(t, copiedDelta.ValidateAuthentication(), errInvalidOutputDelta)

	originalPlan := delta.plan
	delta.plan = noOpPlanDelta(t, fixture.planAuthority, base)
	require.ErrorIs(t, delta.ValidateAuthentication(), errInvalidOutputDelta)
	delta.plan = originalPlan
	require.NoError(t, delta.ValidateAuthentication())

	_, err = delta.Apply(next)
	require.ErrorIs(t, err, errInvalidOutputDelta)
	foreignFixture := newScaleOutputFixture(t, 4)
	foreignBase := mustOutputSnapshot(
		t, foreignFixture.authority, foreignFixture.config,
		foreignFixture.plan, foreignFixture.artifacts, nil,
	)
	foreignDeltas := mapOutputDeltas(t, &foreignFixture, foreignBase, 1, "foreign.example backend\n")
	_, err = BeginTransaction(
		fixture.authority, base, foreignDeltas.document, deltas.plan, deltas.artifacts,
	)
	require.ErrorIs(t, err, errInvalidOutputTransaction)
	_, err = BeginTransaction(
		fixture.authority, base, deltas.document, foreignDeltas.plan, deltas.artifacts,
	)
	require.ErrorIs(t, err, errInvalidOutputTransaction)
	_, err = BeginTransaction(
		fixture.authority, base, deltas.document, deltas.plan, foreignDeltas.artifacts,
	)
	require.ErrorIs(t, err, errInvalidOutputTransaction)
	_, err = delta.Apply(foreignBase)
	require.ErrorIs(t, err, errInvalidOutputDelta)

	assertOutputDeltaRejectsABA(t, &fixture, base, next, delta)
}

func assertOutputDeltaRejectsABA(
	tb testing.TB,
	fixture *outputFixture,
	base, next *Snapshot,
	delta *Delta,
) {
	tb.Helper()
	backPlan := fixture.plan.Clone()
	backPlanSnapshot, err := next.PlanSnapshot()
	require.NoError(tb, err)
	path := fixture.specs[1].descriptor.RuntimePath
	mapHandle, found, err := backPlanSnapshot.MapHandle(path)
	require.NoError(tb, err)
	require.True(tb, found)
	fileIndex := planFileIndex(tb, backPlan, path)
	fileHandle, err := backPlanSnapshot.FileHandle(fileIndex)
	require.NoError(tb, err)
	backPlanTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, backPlanSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, backPlanTransaction.ReplaceMap(mapHandle, backPlan.Maps[path]))
	require.NoError(tb, backPlanTransaction.ReplaceFile(fileHandle, backPlan.Files[fileIndex]))
	_, backPlanDelta, err := backPlanTransaction.Commit()
	require.NoError(tb, err)
	backArtifacts, err := next.ArtifactSnapshot()
	require.NoError(tb, err)
	artifactHandle, found, err := backArtifacts.Lookup(fixture.specs[1].descriptor)
	require.NoError(tb, err)
	require.True(tb, found)
	backArtifactTransaction, err := renderartifact.BeginTransaction(
		fixture.artifactAuthority, backArtifacts,
	)
	require.NoError(tb, err)
	require.NoError(tb, backArtifactTransaction.Replace(
		artifactHandle, fixture.specs[1].descriptor,
		renderartifact.NewLiteralContent(fixture.specs[1].content),
	))
	_, backArtifactDelta, err := backArtifactTransaction.Commit()
	require.NoError(tb, err)
	backDocumentDelta := noOpDocumentDelta(tb, next)
	backTransaction, err := BeginTransaction(
		fixture.authority, next, backDocumentDelta, backPlanDelta, backArtifactDelta,
	)
	require.NoError(tb, err)
	reverted, _, err := backTransaction.Commit()
	require.NoError(tb, err)
	exact, err := reverted.ExactEqual(base)
	require.NoError(tb, err)
	assert.True(tb, exact)
	assert.NotSame(tb, base, reverted)
	_, err = delta.Apply(reverted)
	require.ErrorIs(tb, err, errInvalidOutputDelta)
}

func TestOutputDeltaCompatibilityValuesAreConcurrencySafe(t *testing.T) {
	fixture := newScaleOutputFixture(t, 300)
	base := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	deltas := mapOutputDeltas(t, &fixture, base, 150, "changed.example backend\n")
	transaction, err := BeginTransaction(
		fixture.authority, base, deltas.document, deltas.plan, deltas.artifacts,
	)
	require.NoError(t, err)
	next, _, err := transaction.Commit()
	require.NoError(t, err)
	oraclePlan, err := deltas.nextPlan.LegacyCopy()
	require.NoError(t, err)
	wantChecksum, err := dataplane.ComputeSnapshotContentChecksum(fixture.config, deltas.nextArtifacts)
	require.NoError(t, err)

	const readers = 32
	start := make(chan struct{})
	errorsByReader := make(chan error, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			<-start
			planID, readErr := next.PlanID()
			if readErr == nil && planID != oraclePlan.ID {
				readErr = fmt.Errorf("plan ID = %q, want %q", planID, oraclePlan.ID)
			}
			checksum, checksumErr := next.ContentChecksum()
			if readErr == nil {
				readErr = checksumErr
			}
			if readErr == nil && checksum != wantChecksum {
				readErr = fmt.Errorf("checksum = %q, want %q", checksum, wantChecksum)
			}
			if readErr != nil {
				errorsByReader <- readErr
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

func TestOutputConfigDeltaCompatibilityValuesAreConcurrencySafe(t *testing.T) {
	fixture := newOutputConfigDeltaFixture(t, 300, 150)
	transaction, err := BeginTransaction(
		fixture.authority, fixture.base,
		fixture.documentDelta, fixture.planDelta, fixture.artifactDelta,
	)
	require.NoError(t, err)
	next, _, err := transaction.Commit()
	require.NoError(t, err)
	wantChecksum, err := dataplane.ComputeSnapshotContentChecksum(
		fixture.nextConfig, fixture.artifacts,
	)
	require.NoError(t, err)

	const readers = 32
	start := make(chan struct{})
	errorsByReader := make(chan error, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			<-start
			if readErr := validateOutputConfigDeltaCompatibility(
				next, &fixture, wantChecksum,
			); readErr != nil {
				errorsByReader <- readErr
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

func validateOutputConfigDeltaCompatibility(
	next *Snapshot,
	fixture *outputConfigDeltaFixture,
	wantChecksum string,
) error {
	planID, err := next.PlanID()
	if err != nil {
		return err
	}
	if planID != fixture.oracle.ID {
		return fmt.Errorf("plan ID = %q, want %q", planID, fixture.oracle.ID)
	}
	checksum, err := next.ContentChecksum()
	if err != nil {
		return err
	}
	if checksum != wantChecksum {
		return fmt.Errorf("checksum = %q, want %q", checksum, wantChecksum)
	}
	config, err := next.Config()
	if err != nil {
		return err
	}
	if config != fixture.nextConfig {
		return errors.New("config differs from oracle")
	}
	return nil
}

func BenchmarkOutputTransactionReplaceOneOf3000(b *testing.B) {
	fixture := newScaleOutputFixture(b, 3000)
	base := mustOutputSnapshot(
		b, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	deltas := mapOutputDeltas(b, &fixture, base, 1500, "changed.example backend\n")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		transaction, err := BeginTransaction(
			fixture.authority, base, deltas.document, deltas.plan, deltas.artifacts,
		)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkOutputSnapshotSink, _, err = transaction.Commit()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOutputTransactionReplaceConfigLeafOf3000(b *testing.B) {
	fixture := newOutputConfigDeltaFixture(b, 3000, 1500)
	run := func(b *testing.B, checksum bool) {
		b.Helper()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			transaction, err := BeginTransaction(
				fixture.authority, fixture.base,
				fixture.documentDelta, fixture.planDelta, fixture.artifactDelta,
			)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkOutputSnapshotSink, _, err = transaction.Commit()
			if err == nil && checksum {
				benchmarkOutputDeltaStringSink, err = benchmarkOutputSnapshotSink.ContentChecksum()
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("publish", func(b *testing.B) { run(b, false) })
	b.Run("publish-and-checksum", func(b *testing.B) { run(b, true) })
}

type outputConfigDeltaFixture struct {
	planAuthority     *renderplan.Authority
	artifactAuthority *renderartifact.Authority
	authority         *Authority
	artifacts         *renderartifact.Snapshot
	base              *Snapshot
	documentDelta     *rendercontent.DocumentDelta
	planDelta         *renderplan.Delta
	artifactDelta     *renderartifact.Delta
	nextDocument      rendercontent.Document
	nextConfig        string
	oracle            *renderplan.Plan
	changedSection    renderplan.Section
	changedIndex      int
}

type outputConfigBaseFixture struct {
	planAuthority     *renderplan.Authority
	artifactAuthority *renderartifact.Authority
	authority         *Authority
	plan              *renderplan.Plan
	document          rendercontent.Document
	artifacts         *renderartifact.Snapshot
	base              *Snapshot
}

func newOutputConfigDeltaFixture(
	tb testing.TB,
	count, changedIndex int,
) outputConfigDeltaFixture {
	tb.Helper()
	require.GreaterOrEqual(tb, changedIndex, 0)
	require.Less(tb, changedIndex, count)
	initial := newOutputConfigBaseFixture(tb, count)
	plan := initial.plan
	document := initial.document
	planAuthority := initial.planAuthority
	artifactAuthority := initial.artifactAuthority
	authority := initial.authority
	artifacts := initial.artifacts
	base := initial.base

	changedSection := plan.Sections[changedIndex]
	changedSection.Text = fmt.Sprintf("global changed-%06d value\n", changedIndex)
	changedSection.Length = len(changedSection.Text)
	changedSection.TextDigest = renderplan.DigestString(changedSection.Text)
	documentHandle, err := document.LeafHandle(changedIndex)
	require.NoError(tb, err)
	documentTransaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, documentTransaction.ReplaceText(documentHandle, changedSection.Text))
	nextDocument, documentDelta, err := documentTransaction.Commit()
	require.NoError(tb, err)

	planSnapshot, err := base.PlanSnapshot()
	require.NoError(tb, err)
	sectionHandle, err := planSnapshot.SectionHandle(changedIndex)
	require.NoError(tb, err)
	fileHandle, err := planSnapshot.FileHandle(0)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.ReplaceSection(sectionHandle, changedSection))
	require.NoError(tb, planTransaction.ReplaceConfigFileDocument(fileHandle, nextDocument))
	_, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	artifactDelta := noOpArtifactDelta(tb, artifactAuthority, artifacts)

	oracle := plan.Clone()
	oracle.Sections[changedIndex] = changedSection
	var nextConfig strings.Builder
	for index := range oracle.Sections {
		nextConfig.WriteString(oracle.Sections[index].Text)
	}
	oracle.Files[0] = exactPlanFile(
		renderplan.ConfigFilePath, renderplan.FileKindConfig, true, nextConfig.String(),
	)
	oracle.ComputeID()
	return outputConfigDeltaFixture{
		planAuthority: planAuthority, artifactAuthority: artifactAuthority,
		authority: authority, artifacts: artifacts, base: base,
		documentDelta: documentDelta, planDelta: planDelta, artifactDelta: artifactDelta,
		nextDocument: nextDocument, nextConfig: nextConfig.String(), oracle: oracle,
		changedSection: changedSection, changedIndex: changedIndex,
	}
}

func newOutputConfigBaseFixture(tb testing.TB, count int) outputConfigBaseFixture {
	tb.Helper()
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      make([]renderplan.Section, count),
	}
	var config strings.Builder
	var documentBuilder rendercontent.DocumentBuilder
	for index := range count {
		text := fmt.Sprintf("global setting-%06d value\n", index)
		plan.Sections[index] = exactSection(
			renderplan.SectionKindCore, fmt.Sprintf("core#%d", index), text,
		)
		config.WriteString(text)
		var childBuilder rendercontent.DocumentBuilder
		_, err := childBuilder.WriteString(text)
		require.NoError(tb, err)
		child, err := childBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, documentBuilder.AppendDocument(child))
	}
	document, err := documentBuilder.Build(nil)
	require.NoError(tb, err)
	plan.Files = []renderplan.File{exactPlanFile(
		renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config.String(),
	)}
	plan.ComputeID()

	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	authority, err := NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	artifacts := buildArtifactSnapshot(tb, artifactAuthority, nil, nil)
	base, err := NewSnapshotFromDocument(authority, document, plan, artifacts, nil)
	require.NoError(tb, err)
	return outputConfigBaseFixture{
		planAuthority: planAuthority, artifactAuthority: artifactAuthority,
		authority: authority, plan: plan, document: document, artifacts: artifacts, base: base,
	}
}

var benchmarkOutputDeltaStringSink string

func mapOutputDeltas(
	tb testing.TB,
	fixture *outputFixture,
	base *Snapshot,
	changedIndex int,
	changedContent string,
) outputChildDeltas {
	tb.Helper()
	documentDelta := noOpDocumentDelta(tb, base)
	planSnapshot, err := base.PlanSnapshot()
	require.NoError(tb, err)
	path := fixture.specs[changedIndex].descriptor.RuntimePath
	mapHandle, found, err := planSnapshot.MapHandle(path)
	require.NoError(tb, err)
	require.True(tb, found)
	fileIndex := planFileIndex(tb, fixture.plan, path)
	fileHandle, err := planSnapshot.FileHandle(fileIndex)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.ReplaceMap(mapHandle, renderplan.Map{
		Path: path, Ordered: true, Entries: renderplan.ParseMapEntries(changedContent),
	}))
	require.NoError(tb, planTransaction.ReplaceFile(
		fileHandle, exactPlanFile(path, renderplan.FileKindMap, false, changedContent),
	))
	nextPlan, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	artifactHandle, found, err := fixture.artifacts.Lookup(fixture.specs[changedIndex].descriptor)
	require.NoError(tb, err)
	require.True(tb, found)
	artifactTransaction, err := renderartifact.BeginTransaction(
		fixture.artifactAuthority, fixture.artifacts,
	)
	require.NoError(tb, err)
	require.NoError(tb, artifactTransaction.Replace(
		artifactHandle, fixture.specs[changedIndex].descriptor,
		renderartifact.NewLiteralContent(changedContent),
	))
	nextArtifacts, artifactDelta, err := artifactTransaction.Commit()
	require.NoError(tb, err)
	return outputChildDeltas{
		document: documentDelta, plan: planDelta, artifacts: artifactDelta,
		nextPlan: nextPlan, nextArtifacts: nextArtifacts,
	}
}

func noOpDocumentDelta(tb testing.TB, snapshot *Snapshot) *rendercontent.DocumentDelta {
	tb.Helper()
	document, err := snapshot.ConfigDocument()
	require.NoError(tb, err)
	transaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	_, delta, err := transaction.Commit()
	require.NoError(tb, err)
	return delta
}

func noOpPlanDelta(
	tb testing.TB,
	authority *renderplan.Authority,
	snapshot *Snapshot,
) *renderplan.Delta {
	tb.Helper()
	plan, err := snapshot.PlanSnapshot()
	require.NoError(tb, err)
	transaction, err := renderplan.BeginTransaction(authority, plan)
	require.NoError(tb, err)
	_, delta, err := transaction.Commit()
	require.NoError(tb, err)
	return delta
}

func noOpArtifactDelta(
	tb testing.TB,
	authority *renderartifact.Authority,
	snapshot *renderartifact.Snapshot,
) *renderartifact.Delta {
	tb.Helper()
	transaction, err := renderartifact.BeginTransaction(authority, snapshot)
	require.NoError(tb, err)
	_, delta, err := transaction.Commit()
	require.NoError(tb, err)
	return delta
}

func alignedSectionDocument(
	tb testing.TB,
	sections []renderplan.Section,
) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	for _, section := range sections {
		var childBuilder rendercontent.DocumentBuilder
		_, err := childBuilder.WriteString(section.Text)
		require.NoError(tb, err)
		child, err := childBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, builder.AppendDocument(child))
	}
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func planFileIndex(tb testing.TB, plan *renderplan.Plan, path string) int {
	tb.Helper()
	for index := range plan.Files {
		if plan.Files[index].Path == path {
			return index
		}
	}
	tb.Fatalf("plan file %q is absent", path)
	return -1
}
