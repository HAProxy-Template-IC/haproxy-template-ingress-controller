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
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestOutputTransactionPublishesStructuralSectionChanges(t *testing.T) {
	tests := []struct {
		name   string
		change func(testing.TB, sectionOutputFixture) outputChildDeltas
		want   []string
	}{
		{
			name: "insert",
			change: func(tb testing.TB, fixture sectionOutputFixture) outputChildDeltas {
				tb.Helper()
				return insertSectionOutputDeltas(tb, fixture, 1, "inserted\n")
			},
			want: []string{"section-0\n", "inserted\n", "section-1\n", "section-2\n"},
		},
		{
			name: "delete",
			change: func(tb testing.TB, fixture sectionOutputFixture) outputChildDeltas {
				tb.Helper()
				return deleteSectionOutputDeltas(tb, fixture, 1)
			},
			want: []string{"section-0\n", "section-2\n"},
		},
		{
			name: "reorder",
			change: func(tb testing.TB, fixture sectionOutputFixture) outputChildDeltas {
				tb.Helper()
				return reorderSectionOutputDeltas(tb, fixture, 0, 3)
			},
			want: []string{"section-1\n", "section-2\n", "section-0\n"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSectionOutputFixture(t, 3)
			deltas := test.change(t, fixture)
			transaction, err := BeginTransaction(
				fixture.authority, fixture.base,
				deltas.document, deltas.plan, deltas.artifacts,
			)
			require.NoError(t, err)
			next, delta, err := transaction.Commit()
			require.NoError(t, err)
			require.NoError(t, next.ValidateAuthentication())
			require.NoError(t, delta.ValidateAuthentication())
			requireMatchesFullyValidatedSnapshot(t, fixture.authority, next)
			assert.Equal(t, joinStrings(test.want), mustOutputConfig(t, next))
			counts, err := next.Counts()
			require.NoError(t, err)
			assert.Equal(t, len(test.want), counts.Sections)
			assert.Equal(t, 1, counts.Files)
			assert.Zero(t, counts.Artifacts)
			assert.True(t, next.root.config.sectionAligned)
		})
	}
}

func TestOutputTransactionPublishesStructuralArtifactChanges(t *testing.T) {
	t.Run("insert", func(t *testing.T) {
		fixture := newScaleOutputFixture(t, 2)
		base := mustOutputSnapshot(
			t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
		)
		path := "maps/inserted.map"
		content := "inserted.example inserted-backend\n"
		descriptor := renderartifact.Descriptor{
			Family: renderartifact.Map, Path: path, RuntimePath: path,
		}
		deltas := insertMapOutputDeltas(t, &fixture, base, descriptor, content)
		next := commitOutputDeltas(t, fixture.authority, base, deltas)
		counts, err := next.Counts()
		require.NoError(t, err)
		assert.Equal(t, 3, counts.Maps)
		assert.Equal(t, 4, counts.Files)
		assert.Equal(t, 3, counts.Artifacts)
		binding, found, err := next.root.bindings.lookup(path)
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, binding.artifact)
	})

	t.Run("delete", func(t *testing.T) {
		fixture := newScaleOutputFixture(t, 3)
		base := mustOutputSnapshot(
			t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
		)
		deltas := deleteMapOutputDeltas(t, &fixture, base, 1)
		next := commitOutputDeltas(t, fixture.authority, base, deltas)
		counts, err := next.Counts()
		require.NoError(t, err)
		assert.Equal(t, 2, counts.Maps)
		assert.Equal(t, 3, counts.Files)
		assert.Equal(t, 2, counts.Artifacts)
		_, found, err := next.root.bindings.lookup(fixture.specs[1].descriptor.RuntimePath)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("file reorder", func(t *testing.T) {
		fixture := newScaleOutputFixture(t, 3)
		base := mustOutputSnapshot(
			t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
		)
		planSnapshot, err := base.PlanSnapshot()
		require.NoError(t, err)
		file := fixture.plan.Files[1]
		handle, err := planSnapshot.FileHandle(1)
		require.NoError(t, err)
		gap, err := planSnapshot.FileGapHandle(len(fixture.plan.Files))
		require.NoError(t, err)
		planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
		require.NoError(t, err)
		require.NoError(t, planTransaction.DeleteFile(handle))
		require.NoError(t, planTransaction.InsertFile(gap, file))
		_, planDelta, err := planTransaction.Commit()
		require.NoError(t, err)
		deltas := outputChildDeltas{
			document: noOpDocumentDelta(t, base), plan: planDelta,
			artifacts: noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
		}
		next := commitOutputDeltas(t, fixture.authority, base, deltas)
		counts, err := next.Counts()
		require.NoError(t, err)
		assert.Equal(t, base.root.counts, counts)
	})
}

func TestOutputTransactionRejectsPoisonedStructuralBindings(t *testing.T) {
	tests := []struct {
		name  string
		build func(testing.TB, outputFixture, *Snapshot) outputChildDeltas
		match string
	}{
		{
			name: "duplicate file path",
			build: func(tb testing.TB, fixture outputFixture, base *Snapshot) outputChildDeltas {
				tb.Helper()
				planSnapshot, err := base.PlanSnapshot()
				require.NoError(tb, err)
				gap, err := planSnapshot.FileGapHandle(len(fixture.plan.Files))
				require.NoError(tb, err)
				transaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
				require.NoError(tb, err)
				require.NoError(tb, transaction.InsertFile(gap, fixture.plan.Files[1]))
				_, delta, err := transaction.Commit()
				require.NoError(tb, err)
				return outputChildDeltas{
					document: noOpDocumentDelta(tb, base), plan: delta,
					artifacts: noOpArtifactDelta(tb, fixture.artifactAuthority, fixture.artifacts),
				}
			},
			match: "duplicated",
		},
		{
			name: "inserted file without artifact",
			build: func(tb testing.TB, fixture outputFixture, base *Snapshot) outputChildDeltas {
				tb.Helper()
				planSnapshot, err := base.PlanSnapshot()
				require.NoError(tb, err)
				gap, err := planSnapshot.FileGapHandle(len(fixture.plan.Files))
				require.NoError(tb, err)
				transaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
				require.NoError(tb, err)
				require.NoError(tb, transaction.InsertFile(gap, exactPlanFile(
					"general/extra", renderplan.FileKindGeneral, true, "extra\n",
				)))
				_, delta, err := transaction.Commit()
				require.NoError(tb, err)
				return outputChildDeltas{
					document: noOpDocumentDelta(tb, base), plan: delta,
					artifacts: noOpArtifactDelta(tb, fixture.artifactAuthority, fixture.artifacts),
				}
			},
			match: "presence differs",
		},
		{
			name: "deleted file without artifact",
			build: func(tb testing.TB, fixture outputFixture, base *Snapshot) outputChildDeltas {
				tb.Helper()
				path := fixture.specs[1].descriptor.RuntimePath
				planSnapshot, err := base.PlanSnapshot()
				require.NoError(tb, err)
				handle, err := planSnapshot.FileHandle(planFileIndex(tb, fixture.plan, path))
				require.NoError(tb, err)
				transaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
				require.NoError(tb, err)
				require.NoError(tb, transaction.DeleteFile(handle))
				_, delta, err := transaction.Commit()
				require.NoError(tb, err)
				return outputChildDeltas{
					document: noOpDocumentDelta(tb, base), plan: delta,
					artifacts: noOpArtifactDelta(tb, fixture.artifactAuthority, fixture.artifacts),
				}
			},
			match: "presence differs",
		},
		{
			name: "deleted artifact without file",
			build: func(tb testing.TB, fixture outputFixture, base *Snapshot) outputChildDeltas {
				tb.Helper()
				handle, found, err := fixture.artifacts.Lookup(fixture.specs[1].descriptor)
				require.NoError(tb, err)
				require.True(tb, found)
				transaction, err := renderartifact.BeginTransaction(
					fixture.artifactAuthority, fixture.artifacts,
				)
				require.NoError(tb, err)
				require.NoError(tb, transaction.Delete(handle))
				_, delta, err := transaction.Commit()
				require.NoError(tb, err)
				return outputChildDeltas{
					document: noOpDocumentDelta(tb, base),
					plan:     noOpPlanDelta(tb, fixture.planAuthority, base), artifacts: delta,
				}
			},
			match: "presence differs",
		},
		{
			name: "artifact without file",
			build: func(tb testing.TB, fixture outputFixture, base *Snapshot) outputChildDeltas {
				tb.Helper()
				descriptor := renderartifact.Descriptor{
					Family: renderartifact.General, Name: "extra",
					Path: "files/extra", RuntimePath: "general/extra",
				}
				transaction, err := renderartifact.BeginTransaction(
					fixture.artifactAuthority, fixture.artifacts,
				)
				require.NoError(tb, err)
				require.NoError(tb, transaction.Insert(
					descriptor, renderartifact.NewLiteralContent("extra\n"),
				))
				_, delta, err := transaction.Commit()
				require.NoError(tb, err)
				return outputChildDeltas{
					document: noOpDocumentDelta(tb, base),
					plan:     noOpPlanDelta(tb, fixture.planAuthority, base), artifacts: delta,
				}
			},
			match: "presence differs",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newScaleOutputFixture(t, 2)
			base := mustOutputSnapshot(
				t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
			)
			deltas := test.build(t, fixture, base)
			transaction, err := BeginTransaction(
				fixture.authority, base, deltas.document, deltas.plan, deltas.artifacts,
			)
			require.NoError(t, err)
			_, _, err = transaction.Commit()
			require.ErrorContains(t, err, test.match)
			require.NoError(t, base.ValidateAuthentication())
		})
	}
}

func TestOutputTransactionRejectsUnpairedStructuralSectionChanges(t *testing.T) {
	tests := []struct {
		name  string
		build func(testing.TB, sectionOutputFixture) outputChildDeltas
	}{
		{
			name: "section without document",
			build: func(tb testing.TB, fixture sectionOutputFixture) outputChildDeltas {
				tb.Helper()
				deltas := insertSectionOutputDeltas(tb, fixture, 1, "inserted\n")
				deltas.document = noOpDocumentDelta(tb, fixture.base)
				return deltas
			},
		},
		{
			name: "document without section",
			build: func(tb testing.TB, fixture sectionOutputFixture) outputChildDeltas {
				tb.Helper()
				deltas := insertSectionOutputDeltas(tb, fixture, 1, "inserted\n")
				deltas.plan = noOpPlanDelta(tb, fixture.planAuthority, fixture.base)
				return deltas
			},
		},
		{
			name: "config file unchanged",
			build: func(tb testing.TB, fixture sectionOutputFixture) outputChildDeltas {
				tb.Helper()
				deltas := insertSectionOutputDeltas(tb, fixture, 1, "inserted\n")
				planSnapshot, err := fixture.base.PlanSnapshot()
				require.NoError(tb, err)
				gap, err := planSnapshot.SectionGapHandle(1)
				require.NoError(tb, err)
				transaction, err := renderplan.BeginTransaction(
					fixture.planAuthority, planSnapshot,
				)
				require.NoError(tb, err)
				require.NoError(tb, transaction.InsertSection(
					gap, exactSection(renderplan.SectionKindCore, "inserted", "inserted\n"),
				))
				_, deltas.plan, err = transaction.Commit()
				require.NoError(tb, err)
				return deltas
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSectionOutputFixture(t, 3)
			deltas := test.build(t, fixture)
			transaction, err := BeginTransaction(
				fixture.authority, fixture.base,
				deltas.document, deltas.plan, deltas.artifacts,
			)
			require.NoError(t, err)
			_, _, err = transaction.Commit()
			require.Error(t, err)
		})
	}
}

func TestOutputTransactionValidatesStructuralBackendAndProfileBindings(t *testing.T) {
	t.Run("backend insert", func(t *testing.T) {
		fixture := newOutputFixture(t)
		document := alignedSectionDocument(t, fixture.plan.Sections)
		base, err := NewSnapshotFromDocument(
			fixture.authority, document, fixture.plan, fixture.artifacts, nil,
		)
		require.NoError(t, err)
		text := "backend inserted\n    server s1 192.0.2.1:80\n"
		section := exactSection(renderplan.SectionKindBackend, "inserted", text)
		backend := exactStructuralBackend("inserted", &section)
		documentGap, err := document.GapHandle(len(fixture.plan.Sections))
		require.NoError(t, err)
		documentTransaction, err := document.BeginTransaction()
		require.NoError(t, err)
		require.NoError(t, documentTransaction.InsertText(documentGap, text))
		nextDocument, documentDelta, err := documentTransaction.Commit()
		require.NoError(t, err)
		planSnapshot, err := base.PlanSnapshot()
		require.NoError(t, err)
		sectionGap, err := planSnapshot.SectionGapHandle(len(fixture.plan.Sections))
		require.NoError(t, err)
		configHandle, err := planSnapshot.FileHandle(planFileIndex(
			t, fixture.plan, renderplan.ConfigFilePath,
		))
		require.NoError(t, err)
		planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
		require.NoError(t, err)
		require.NoError(t, planTransaction.InsertSection(sectionGap, section))
		require.NoError(t, planTransaction.InsertBackend(backend.Name, backend))
		require.NoError(t, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
		_, planDelta, err := planTransaction.Commit()
		require.NoError(t, err)
		next := commitOutputDeltas(t, fixture.authority, base, outputChildDeltas{
			document: documentDelta, plan: planDelta,
			artifacts: noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
		})
		counts, err := next.Counts()
		require.NoError(t, err)
		assert.Equal(t, 2, counts.Backends)
	})

	t.Run("backend delete", func(t *testing.T) {
		fixture := newOutputFixture(t)
		document := alignedSectionDocument(t, fixture.plan.Sections)
		base, err := NewSnapshotFromDocument(
			fixture.authority, document, fixture.plan, fixture.artifacts, nil,
		)
		require.NoError(t, err)
		documentHandle, err := document.LeafHandle(2)
		require.NoError(t, err)
		documentTransaction, err := document.BeginTransaction()
		require.NoError(t, err)
		require.NoError(t, documentTransaction.Delete(documentHandle))
		nextDocument, documentDelta, err := documentTransaction.Commit()
		require.NoError(t, err)
		planSnapshot, err := base.PlanSnapshot()
		require.NoError(t, err)
		sectionHandle, err := planSnapshot.SectionHandle(2)
		require.NoError(t, err)
		backendHandle, found, err := planSnapshot.BackendHandle("be_app")
		require.NoError(t, err)
		require.True(t, found)
		configHandle, err := planSnapshot.FileHandle(planFileIndex(
			t, fixture.plan, renderplan.ConfigFilePath,
		))
		require.NoError(t, err)
		planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
		require.NoError(t, err)
		require.NoError(t, planTransaction.DeleteSection(sectionHandle))
		require.NoError(t, planTransaction.DeleteBackend(backendHandle))
		require.NoError(t, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
		_, planDelta, err := planTransaction.Commit()
		require.NoError(t, err)
		next := commitOutputDeltas(t, fixture.authority, base, outputChildDeltas{
			document: documentDelta, plan: planDelta,
			artifacts: noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
		})
		counts, err := next.Counts()
		require.NoError(t, err)
		assert.Zero(t, counts.Backends)
	})

	t.Run("profile insert", func(t *testing.T) {
		fixture := newOutputFixture(t)
		document := alignedSectionDocument(t, fixture.plan.Sections)
		base, err := NewSnapshotFromDocument(
			fixture.authority, document, fixture.plan, fixture.artifacts, nil,
		)
		require.NoError(t, err)
		text := "defaults inserted from defaults\n    mode tcp\n"
		section := exactSection(renderplan.SectionKindProfile, "inserted", text)
		_, body, _ := strings.Cut(text, "\n")
		profile := renderplan.Profile{
			Name: "inserted", BodyDigest: renderplan.DigestString(body), HasRules: true,
		}
		documentGap, err := document.GapHandle(len(fixture.plan.Sections))
		require.NoError(t, err)
		documentTransaction, err := document.BeginTransaction()
		require.NoError(t, err)
		require.NoError(t, documentTransaction.InsertText(documentGap, text))
		nextDocument, documentDelta, err := documentTransaction.Commit()
		require.NoError(t, err)
		planSnapshot, err := base.PlanSnapshot()
		require.NoError(t, err)
		sectionGap, err := planSnapshot.SectionGapHandle(len(fixture.plan.Sections))
		require.NoError(t, err)
		configHandle, err := planSnapshot.FileHandle(planFileIndex(
			t, fixture.plan, renderplan.ConfigFilePath,
		))
		require.NoError(t, err)
		planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
		require.NoError(t, err)
		require.NoError(t, planTransaction.InsertSection(sectionGap, section))
		require.NoError(t, planTransaction.InsertProfile(profile.Name, profile))
		require.NoError(t, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
		_, planDelta, err := planTransaction.Commit()
		require.NoError(t, err)
		next := commitOutputDeltas(t, fixture.authority, base, outputChildDeltas{
			document: documentDelta, plan: planDelta,
			artifacts: noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
		})
		counts, err := next.Counts()
		require.NoError(t, err)
		assert.Equal(t, 2, counts.Profiles)
	})

	t.Run("duplicate backend section", func(t *testing.T) {
		fixture := newOutputFixture(t)
		document := alignedSectionDocument(t, fixture.plan.Sections)
		base, err := NewSnapshotFromDocument(
			fixture.authority, document, fixture.plan, fixture.artifacts, nil,
		)
		require.NoError(t, err)
		duplicate := fixture.plan.Sections[2]
		documentGap, err := document.GapHandle(len(fixture.plan.Sections))
		require.NoError(t, err)
		documentTransaction, err := document.BeginTransaction()
		require.NoError(t, err)
		require.NoError(t, documentTransaction.InsertText(documentGap, duplicate.Text))
		nextDocument, documentDelta, err := documentTransaction.Commit()
		require.NoError(t, err)
		planSnapshot, err := base.PlanSnapshot()
		require.NoError(t, err)
		sectionGap, err := planSnapshot.SectionGapHandle(len(fixture.plan.Sections))
		require.NoError(t, err)
		configHandle, err := planSnapshot.FileHandle(planFileIndex(
			t, fixture.plan, renderplan.ConfigFilePath,
		))
		require.NoError(t, err)
		planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
		require.NoError(t, err)
		require.NoError(t, planTransaction.InsertSection(sectionGap, duplicate))
		require.NoError(t, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
		_, planDelta, err := planTransaction.Commit()
		require.NoError(t, err)
		transaction, err := BeginTransaction(
			fixture.authority, base, documentDelta, planDelta,
			noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
		)
		require.NoError(t, err)
		_, _, err = transaction.Commit()
		require.ErrorContains(t, err, "does not match its declaration")
	})

	t.Run("profile delete without section", func(t *testing.T) {
		fixture := newOutputFixture(t)
		document := alignedSectionDocument(t, fixture.plan.Sections)
		base, err := NewSnapshotFromDocument(
			fixture.authority, document, fixture.plan, fixture.artifacts, nil,
		)
		require.NoError(t, err)
		planSnapshot, err := base.PlanSnapshot()
		require.NoError(t, err)
		handle, found, err := planSnapshot.ProfileHandle("profile-a")
		require.NoError(t, err)
		require.True(t, found)
		planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
		require.NoError(t, err)
		require.NoError(t, planTransaction.DeleteProfile(handle))
		_, planDelta, err := planTransaction.Commit()
		require.NoError(t, err)
		transaction, err := BeginTransaction(
			fixture.authority, base, noOpDocumentDelta(t, base), planDelta,
			noOpArtifactDelta(t, fixture.artifactAuthority, fixture.artifacts),
		)
		require.NoError(t, err)
		_, _, err = transaction.Commit()
		require.ErrorContains(t, err, "does not match its declaration")
	})
}

func TestOutputTransactionRejectsDuplicateArtifactRuntimePath(t *testing.T) {
	fixture := newScaleOutputFixture(t, 2)
	base := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
	path := fixture.specs[0].descriptor.RuntimePath
	content := "duplicate\n"
	planSnapshot, err := base.PlanSnapshot()
	require.NoError(t, err)
	gap, err := planSnapshot.FileGapHandle(len(fixture.plan.Files))
	require.NoError(t, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(t, err)
	require.NoError(t, planTransaction.InsertFile(gap, exactPlanFile(
		path, renderplan.FileKindGeneral, true, content,
	)))
	_, planDelta, err := planTransaction.Commit()
	require.NoError(t, err)
	artifactTransaction, err := renderartifact.BeginTransaction(
		fixture.artifactAuthority, fixture.artifacts,
	)
	require.NoError(t, err)
	require.NoError(t, artifactTransaction.Insert(renderartifact.Descriptor{
		Family: renderartifact.General, Name: "duplicate",
		Path: "files/duplicate", RuntimePath: path, ReloadOnChange: true,
	}, renderartifact.NewLiteralContent(content)))
	_, artifactDelta, err := artifactTransaction.Commit()
	require.NoError(t, err)
	transaction, err := BeginTransaction(
		fixture.authority, base, noOpDocumentDelta(t, base), planDelta, artifactDelta,
	)
	require.NoError(t, err)
	_, _, err = transaction.Commit()
	require.ErrorContains(t, err, "duplicated")
}

func TestOutputStructuralDeltaRejectsTamperingAndABA(t *testing.T) {
	fixture := newSectionOutputFixture(t, 3)
	deltas := insertSectionOutputDeltas(t, fixture, 1, "inserted\n")
	transaction, err := BeginTransaction(
		fixture.authority, fixture.base, deltas.document, deltas.plan, deltas.artifacts,
	)
	require.NoError(t, err)
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	_, err = delta.Apply(next)
	require.ErrorIs(t, err, errInvalidOutputDelta)

	deleteFixture := sectionOutputFixture{
		planAuthority: fixture.planAuthority, artifactAuthority: fixture.artifactAuthority,
		authority: fixture.authority, artifacts: fixture.artifacts, base: next,
	}
	deleteFixture.document, err = next.ConfigDocument()
	require.NoError(t, err)
	planSnapshot, err := next.PlanSnapshot()
	require.NoError(t, err)
	deleteFixture.plan, err = planSnapshot.LegacyCopy()
	require.NoError(t, err)
	backDeltas := deleteSectionOutputDeltas(t, deleteFixture, 1)
	reverted := commitOutputDeltas(t, fixture.authority, next, backDeltas)
	exact, err := reverted.ExactEqual(fixture.base)
	require.NoError(t, err)
	require.True(t, exact)
	_, err = delta.Apply(reverted)
	require.ErrorIs(t, err, errInvalidOutputDelta)

	originalKey := fixture.base.root.bindings.root.key
	fixture.base.root.bindings.root.key = "tampered"
	require.ErrorIs(t, fixture.base.ValidateAuthentication(), errInvalidSnapshot)
	fixture.base.root.bindings.root.key = originalKey
	require.NoError(t, fixture.base.ValidateAuthentication())

	copiedBindings := *fixture.base.root.bindings
	originalBindings := fixture.base.root.bindings
	fixture.base.root.bindings = &copiedBindings
	fixture.base.root.auth.bindings = &copiedBindings
	require.ErrorIs(t, fixture.base.ValidateAuthentication(), errInvalidSnapshot)
	fixture.base.root.bindings = originalBindings
	fixture.base.root.auth.bindings = originalBindings
	require.NoError(t, fixture.base.ValidateAuthentication())
}

func TestOutputStructuralTransactionsAreConcurrentAndBaseExact(t *testing.T) {
	fixture := newSectionOutputFixture(t, 8)
	const workers = 16
	start := make(chan struct{})
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			deltas := insertSectionOutputDeltas(
				t, fixture, worker%8, fmt.Sprintf("worker-%d\n", worker),
			)
			transaction, err := BeginTransaction(
				fixture.authority, fixture.base,
				deltas.document, deltas.plan, deltas.artifacts,
			)
			if err == nil {
				_, _, err = transaction.Commit()
			}
			if err != nil {
				errorsByWorker <- err
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		require.NoError(t, err)
	}
	require.NoError(t, fixture.base.ValidateAuthentication())
	assert.Equal(t, 1, fixture.base.root.bindings.files)
}

func BenchmarkOutputTransactionInsertSectionOf3000(b *testing.B) {
	fixture := newSectionOutputFixture(b, 3000)
	deltas := insertSectionOutputDeltas(b, fixture, 1500, "inserted\n")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		transaction, err := BeginTransaction(
			fixture.authority, fixture.base,
			deltas.document, deltas.plan, deltas.artifacts,
		)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := transaction.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func exactStructuralBackend(
	name string,
	section *renderplan.Section,
) renderplan.Backend {
	backend := renderplan.Backend{
		Name: name, Shape: renderplan.ShapeDynamic,
		Servers: []renderplan.Server{{Name: "s1", Address: "192.0.2.1", Port: 80}},
		Body:    []string{"server s1 192.0.2.1:80"}, ContentKnown: true,
		TextDigest: section.TextDigest,
	}
	backend.BodyDigest = renderplan.DigestString(backend.Body[0])
	backend.CommentsDigest = renderplan.DigestString("")
	backend.RecordDigest = backendRecordDigest(&backend)
	return backend
}

type sectionOutputFixture struct {
	planAuthority     *renderplan.Authority
	artifactAuthority *renderartifact.Authority
	authority         *Authority
	plan              *renderplan.Plan
	document          rendercontent.Document
	artifacts         *renderartifact.Snapshot
	base              *Snapshot
}

func newSectionOutputFixture(tb testing.TB, count int) sectionOutputFixture {
	tb.Helper()
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      make([]renderplan.Section, count),
	}
	texts := make([]string, count)
	for index := range count {
		texts[index] = fmt.Sprintf("section-%d\n", index)
		plan.Sections[index] = exactSection(
			renderplan.SectionKindCore, fmt.Sprintf("core#%d", index), texts[index],
		)
	}
	document := documentFromTexts(tb, texts)
	plan.Files = []renderplan.File{exactPlanFile(
		renderplan.ConfigFilePath, renderplan.FileKindConfig, true, joinStrings(texts),
	)}
	plan.ComputeID()
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	authority, err := NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	artifacts := buildArtifactSnapshot(tb, artifactAuthority, nil, nil)
	base, err := NewSnapshotFromDocument(authority, document, plan, artifacts, nil)
	require.NoError(tb, err)
	return sectionOutputFixture{
		planAuthority: planAuthority, artifactAuthority: artifactAuthority,
		authority: authority, plan: plan, document: document, artifacts: artifacts, base: base,
	}
}

func insertSectionOutputDeltas(
	tb testing.TB,
	fixture sectionOutputFixture,
	index int,
	text string,
) outputChildDeltas {
	tb.Helper()
	documentGap, err := fixture.document.GapHandle(index)
	require.NoError(tb, err)
	documentTransaction, err := fixture.document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, documentTransaction.InsertText(documentGap, text))
	nextDocument, documentDelta, err := documentTransaction.Commit()
	require.NoError(tb, err)
	planSnapshot, err := fixture.base.PlanSnapshot()
	require.NoError(tb, err)
	sectionGap, err := planSnapshot.SectionGapHandle(index)
	require.NoError(tb, err)
	configHandle, err := planSnapshot.FileHandle(0)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.InsertSection(sectionGap, exactSection(
		renderplan.SectionKindCore, fmt.Sprintf("inserted#%d", index), text,
	)))
	require.NoError(tb, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
	nextPlan, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	return outputChildDeltas{
		document: documentDelta, plan: planDelta,
		artifacts: noOpArtifactDelta(tb, fixture.artifactAuthority, fixture.artifacts),
		nextPlan:  nextPlan, nextArtifacts: fixture.artifacts,
	}
}

func deleteSectionOutputDeltas(
	tb testing.TB,
	fixture sectionOutputFixture,
	index int,
) outputChildDeltas {
	tb.Helper()
	documentHandle, err := fixture.document.LeafHandle(index)
	require.NoError(tb, err)
	documentTransaction, err := fixture.document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, documentTransaction.Delete(documentHandle))
	nextDocument, documentDelta, err := documentTransaction.Commit()
	require.NoError(tb, err)
	planSnapshot, err := fixture.base.PlanSnapshot()
	require.NoError(tb, err)
	sectionHandle, err := planSnapshot.SectionHandle(index)
	require.NoError(tb, err)
	configHandle, err := planSnapshot.FileHandle(0)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.DeleteSection(sectionHandle))
	require.NoError(tb, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
	nextPlan, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	return outputChildDeltas{
		document: documentDelta, plan: planDelta,
		artifacts: noOpArtifactDelta(tb, fixture.artifactAuthority, fixture.artifacts),
		nextPlan:  nextPlan, nextArtifacts: fixture.artifacts,
	}
}

func reorderSectionOutputDeltas(
	tb testing.TB,
	fixture sectionOutputFixture,
	from, gapIndex int,
) outputChildDeltas {
	tb.Helper()
	documentHandle, err := fixture.document.LeafHandle(from)
	require.NoError(tb, err)
	documentGap, err := fixture.document.GapHandle(gapIndex)
	require.NoError(tb, err)
	documentTransaction, err := fixture.document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, documentTransaction.Delete(documentHandle))
	require.NoError(tb, documentTransaction.InsertText(
		documentGap, fixture.plan.Sections[from].Text,
	))
	nextDocument, documentDelta, err := documentTransaction.Commit()
	require.NoError(tb, err)
	planSnapshot, err := fixture.base.PlanSnapshot()
	require.NoError(tb, err)
	sectionHandle, err := planSnapshot.SectionHandle(from)
	require.NoError(tb, err)
	sectionGap, err := planSnapshot.SectionGapHandle(gapIndex)
	require.NoError(tb, err)
	configHandle, err := planSnapshot.FileHandle(0)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.DeleteSection(sectionHandle))
	require.NoError(tb, planTransaction.InsertSection(sectionGap, fixture.plan.Sections[from]))
	require.NoError(tb, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
	nextPlan, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	return outputChildDeltas{
		document: documentDelta, plan: planDelta,
		artifacts: noOpArtifactDelta(tb, fixture.artifactAuthority, fixture.artifacts),
		nextPlan:  nextPlan, nextArtifacts: fixture.artifacts,
	}
}

func insertMapOutputDeltas(
	tb testing.TB,
	fixture *outputFixture,
	base *Snapshot,
	descriptor renderartifact.Descriptor,
	content string,
) outputChildDeltas {
	tb.Helper()
	planSnapshot, err := base.PlanSnapshot()
	require.NoError(tb, err)
	counts, err := base.Counts()
	require.NoError(tb, err)
	gap, err := planSnapshot.FileGapHandle(counts.Files)
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.InsertMap(descriptor.RuntimePath, renderplan.Map{
		Path: descriptor.RuntimePath, Ordered: true,
		Entries: renderplan.ParseMapEntries(content),
	}))
	require.NoError(tb, planTransaction.InsertFile(gap, exactPlanFile(
		descriptor.RuntimePath, renderplan.FileKindMap, false, content,
	)))
	nextPlan, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	artifactTransaction, err := renderartifact.BeginTransaction(
		fixture.artifactAuthority, fixture.artifacts,
	)
	require.NoError(tb, err)
	require.NoError(tb, artifactTransaction.Insert(
		descriptor, renderartifact.NewLiteralContent(content),
	))
	nextArtifacts, artifactDelta, err := artifactTransaction.Commit()
	require.NoError(tb, err)
	return outputChildDeltas{
		document: noOpDocumentDelta(tb, base), plan: planDelta, artifacts: artifactDelta,
		nextPlan: nextPlan, nextArtifacts: nextArtifacts,
	}
}

func deleteMapOutputDeltas(
	tb testing.TB,
	fixture *outputFixture,
	base *Snapshot,
	index int,
) outputChildDeltas {
	tb.Helper()
	path := fixture.specs[index].descriptor.RuntimePath
	planSnapshot, err := base.PlanSnapshot()
	require.NoError(tb, err)
	mapHandle, found, err := planSnapshot.MapHandle(path)
	require.NoError(tb, err)
	require.True(tb, found)
	fileHandle, err := planSnapshot.FileHandle(planFileIndex(tb, fixture.plan, path))
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(fixture.planAuthority, planSnapshot)
	require.NoError(tb, err)
	require.NoError(tb, planTransaction.DeleteMap(mapHandle))
	require.NoError(tb, planTransaction.DeleteFile(fileHandle))
	nextPlan, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	artifactHandle, found, err := fixture.artifacts.Lookup(fixture.specs[index].descriptor)
	require.NoError(tb, err)
	require.True(tb, found)
	artifactTransaction, err := renderartifact.BeginTransaction(
		fixture.artifactAuthority, fixture.artifacts,
	)
	require.NoError(tb, err)
	require.NoError(tb, artifactTransaction.Delete(artifactHandle))
	nextArtifacts, artifactDelta, err := artifactTransaction.Commit()
	require.NoError(tb, err)
	return outputChildDeltas{
		document: noOpDocumentDelta(tb, base), plan: planDelta, artifacts: artifactDelta,
		nextPlan: nextPlan, nextArtifacts: nextArtifacts,
	}
}

func commitOutputDeltas(
	tb testing.TB,
	authority *Authority,
	base *Snapshot,
	deltas outputChildDeltas,
) *Snapshot {
	tb.Helper()
	transaction, err := BeginTransaction(
		authority, base, deltas.document, deltas.plan, deltas.artifacts,
	)
	require.NoError(tb, err)
	next, _, err := transaction.Commit()
	require.NoError(tb, err)
	requireMatchesFullyValidatedSnapshot(tb, authority, next)
	return next
}

func requireMatchesFullyValidatedSnapshot(
	tb testing.TB,
	authority *Authority,
	snapshot *Snapshot,
) {
	tb.Helper()
	document, err := snapshot.ConfigDocument()
	require.NoError(tb, err)
	planSnapshot, err := snapshot.PlanSnapshot()
	require.NoError(tb, err)
	plan, err := planSnapshot.LegacyCopy()
	require.NoError(tb, err)
	artifacts, err := snapshot.ArtifactSnapshot()
	require.NoError(tb, err)
	fullyValidated, err := NewSnapshotFromDocument(
		authority, document, plan, artifacts, nil,
	)
	require.NoError(tb, err)
	equal, err := snapshot.ExactEqual(fullyValidated)
	require.NoError(tb, err)
	require.True(tb, equal)
}

func documentFromTexts(tb testing.TB, texts []string) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	for _, text := range texts {
		var childBuilder rendercontent.DocumentBuilder
		_, err := childBuilder.WriteString(text)
		require.NoError(tb, err)
		child, err := childBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, builder.AppendDocument(child))
	}
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func joinStrings(values []string) string {
	result := ""
	for _, value := range values {
		result += value
	}
	return result
}

func mustOutputConfig(tb testing.TB, snapshot *Snapshot) string {
	tb.Helper()
	config, err := snapshot.Config()
	require.NoError(tb, err)
	return config
}
