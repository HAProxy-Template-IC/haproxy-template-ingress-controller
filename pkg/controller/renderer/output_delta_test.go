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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestCommitIncrementalOutputPublishesAuthenticatedChildDeltas(t *testing.T) {
	service := newOutputPublicationService(t)
	base := outputPublicationSnapshot(t, service, nil, "base")
	nextConfig := "global\n    # changed\n"
	nextArtifact := "changed\n"

	document, err := base.ConfigDocument()
	require.NoError(t, err)
	documentHandle, err := document.LeafHandle(0)
	require.NoError(t, err)
	documentTransaction, err := document.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, documentTransaction.ReplaceText(documentHandle, nextConfig))
	nextDocument, documentDelta, err := documentTransaction.Commit()
	require.NoError(t, err)

	plan, err := base.PlanSnapshot()
	require.NoError(t, err)
	sectionHandle, err := plan.SectionHandle(0)
	require.NoError(t, err)
	configHandle, err := plan.FileHandle(0)
	require.NoError(t, err)
	artifactFileHandle, err := plan.FileHandle(1)
	require.NoError(t, err)
	planTransaction, err := renderplan.BeginTransaction(service.planAuthority, plan)
	require.NoError(t, err)
	require.NoError(t, planTransaction.ReplaceSection(sectionHandle, outputDeltaSection(nextConfig)))
	require.NoError(t, planTransaction.ReplaceConfigFileDocument(configHandle, nextDocument))
	require.NoError(t, planTransaction.ReplaceFile(
		artifactFileHandle,
		outputPublicationFile("files/output.txt", renderplan.FileKindGeneral, false, nextArtifact),
	))
	_, planDelta, err := planTransaction.Commit()
	require.NoError(t, err)

	artifacts, err := base.ArtifactSnapshot()
	require.NoError(t, err)
	descriptor := outputDeltaArtifactDescriptor()
	artifactHandle, found, err := artifacts.Lookup(descriptor)
	require.NoError(t, err)
	require.True(t, found)
	artifactTransaction, err := renderartifact.BeginTransaction(service.artifactAuthority, artifacts)
	require.NoError(t, err)
	require.NoError(t, artifactTransaction.Replace(
		artifactHandle, descriptor, renderartifact.NewLiteralContent(nextArtifact),
	))
	_, artifactDelta, err := artifactTransaction.Commit()
	require.NoError(t, err)

	next, err := service.commitIncrementalOutput(base, documentDelta, planDelta, artifactDelta)
	require.NoError(t, err)
	require.NotSame(t, base, next)
	require.NoError(t, service.outputAuthority.ValidateSnapshot(next))
	assert.Equal(t, nextConfig, mustOutputDeltaConfig(t, next))
	requireOutputDeltaMatchesFullValidation(t, service, next)
}

func TestCommitIncrementalOutputReusesExactNoopRoot(t *testing.T) {
	service := newOutputPublicationService(t)
	base := outputPublicationSnapshot(t, service, nil, "base")
	deltas := noOpOutputDeltas(t, service, base)

	next, err := service.commitIncrementalOutput(
		base, deltas.document, deltas.plan, deltas.artifacts,
	)
	require.NoError(t, err)
	assert.Same(t, base, next)
}

func TestCommitIncrementalOutputRejectsMismatchedBasesAndMissingDeltas(t *testing.T) {
	service := newOutputPublicationService(t)
	base := outputPublicationSnapshot(t, service, nil, "base")
	other := outputPublicationSnapshot(t, service, base, "other")
	baseDeltas := noOpOutputDeltas(t, service, base)
	otherDeltas := noOpOutputDeltas(t, service, other)
	tests := []struct {
		name      string
		document  *rendercontent.DocumentDelta
		plan      *renderplan.Delta
		artifacts *renderartifact.Delta
	}{
		{
			name: "document base", document: baseDeltas.document,
			plan: otherDeltas.plan, artifacts: otherDeltas.artifacts,
		},
		{
			name: "plan base", document: otherDeltas.document,
			plan: baseDeltas.plan, artifacts: otherDeltas.artifacts,
		},
		{
			name: "artifact base", document: otherDeltas.document,
			plan: otherDeltas.plan, artifacts: baseDeltas.artifacts,
		},
		{
			name: "missing document", plan: otherDeltas.plan,
			artifacts: otherDeltas.artifacts,
		},
		{
			name: "missing plan", document: otherDeltas.document,
			artifacts: otherDeltas.artifacts,
		},
		{
			name: "missing artifacts", document: otherDeltas.document,
			plan: otherDeltas.plan,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.commitIncrementalOutput(
				other, test.document, test.plan, test.artifacts,
			)
			require.Error(t, err)
			require.NoError(t, service.outputAuthority.ValidateSnapshot(other))
		})
	}
}

func TestCommitIncrementalOutputRejectsUnpairedCompanionChanges(t *testing.T) {
	service := newOutputPublicationService(t)
	base := outputPublicationSnapshot(t, service, nil, "base")
	tests := []struct {
		name  string
		build func(testing.TB) outputDeltas
	}{
		{name: "document without plan", build: func(tb testing.TB) outputDeltas {
			tb.Helper()
			deltas := noOpOutputDeltas(tb, service, base)
			deltas.document = changedOutputDocumentDelta(tb, base, "global\n    # changed\n")
			return deltas
		}},
		{name: "config plan without document", build: func(tb testing.TB) outputDeltas {
			tb.Helper()
			deltas := noOpOutputDeltas(tb, service, base)
			deltas.plan = changedOutputPlanFileDelta(
				tb, service, base, 0,
				outputDeltaPlanFile(
					renderplan.ConfigFilePath, renderplan.FileKindConfig, true,
					"global\n    # changed\n",
				),
			)
			return deltas
		}},
		{name: "artifact without plan", build: func(tb testing.TB) outputDeltas {
			tb.Helper()
			deltas := noOpOutputDeltas(tb, service, base)
			deltas.artifacts = changedOutputArtifactDelta(tb, service, base, "changed\n")
			return deltas
		}},
		{name: "artifact plan without artifact", build: func(tb testing.TB) outputDeltas {
			tb.Helper()
			deltas := noOpOutputDeltas(tb, service, base)
			deltas.plan = changedOutputPlanFileDelta(
				tb, service, base, 1,
				outputDeltaPlanFile(
					"files/output.txt", renderplan.FileKindGeneral, false, "changed\n",
				),
			)
			return deltas
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deltas := test.build(t)
			_, err := service.commitIncrementalOutput(
				base, deltas.document, deltas.plan, deltas.artifacts,
			)
			require.Error(t, err)
			require.NoError(t, service.outputAuthority.ValidateSnapshot(base))
		})
	}
}

func TestCommitIncrementalOutputIsConcurrentAndBaseExact(t *testing.T) {
	service := newOutputPublicationService(t)
	base := outputPublicationSnapshot(t, service, nil, "base")
	deltas := noOpOutputDeltas(t, service, base)
	const workers = 16
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			next, err := service.commitIncrementalOutput(
				base, deltas.document, deltas.plan, deltas.artifacts,
			)
			if err == nil && next != base {
				err = fmt.Errorf("no-op publication returned root %p, want %p", next, base)
			}
			if err != nil {
				errorsByWorker <- err
			}
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		require.NoError(t, err)
	}
}

type outputDeltas struct {
	document  *rendercontent.DocumentDelta
	plan      *renderplan.Delta
	artifacts *renderartifact.Delta
}

func noOpOutputDeltas(
	tb testing.TB,
	service *RenderService,
	base *renderoutput.Snapshot,
) outputDeltas {
	tb.Helper()
	document, err := base.ConfigDocument()
	require.NoError(tb, err)
	documentTransaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	_, documentDelta, err := documentTransaction.Commit()
	require.NoError(tb, err)
	plan, err := base.PlanSnapshot()
	require.NoError(tb, err)
	planTransaction, err := renderplan.BeginTransaction(service.planAuthority, plan)
	require.NoError(tb, err)
	_, planDelta, err := planTransaction.Commit()
	require.NoError(tb, err)
	artifacts, err := base.ArtifactSnapshot()
	require.NoError(tb, err)
	artifactTransaction, err := renderartifact.BeginTransaction(service.artifactAuthority, artifacts)
	require.NoError(tb, err)
	_, artifactDelta, err := artifactTransaction.Commit()
	require.NoError(tb, err)
	return outputDeltas{document: documentDelta, plan: planDelta, artifacts: artifactDelta}
}

func changedOutputDocumentDelta(
	tb testing.TB,
	base *renderoutput.Snapshot,
	text string,
) *rendercontent.DocumentDelta {
	tb.Helper()
	document, err := base.ConfigDocument()
	require.NoError(tb, err)
	handle, err := document.LeafHandle(0)
	require.NoError(tb, err)
	transaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	require.NoError(tb, transaction.ReplaceText(handle, text))
	_, delta, err := transaction.Commit()
	require.NoError(tb, err)
	return delta
}

func changedOutputPlanFileDelta(
	tb testing.TB,
	service *RenderService,
	base *renderoutput.Snapshot,
	index int,
	file *renderplan.File,
) *renderplan.Delta {
	tb.Helper()
	require.NotNil(tb, file)
	plan, err := base.PlanSnapshot()
	require.NoError(tb, err)
	handle, err := plan.FileHandle(index)
	require.NoError(tb, err)
	transaction, err := renderplan.BeginTransaction(service.planAuthority, plan)
	require.NoError(tb, err)
	require.NoError(tb, transaction.ReplaceFile(handle, *file))
	_, delta, err := transaction.Commit()
	require.NoError(tb, err)
	return delta
}

func changedOutputArtifactDelta(
	tb testing.TB,
	service *RenderService,
	base *renderoutput.Snapshot,
	text string,
) *renderartifact.Delta {
	tb.Helper()
	artifacts, err := base.ArtifactSnapshot()
	require.NoError(tb, err)
	descriptor := outputDeltaArtifactDescriptor()
	handle, found, err := artifacts.Lookup(descriptor)
	require.NoError(tb, err)
	require.True(tb, found)
	transaction, err := renderartifact.BeginTransaction(service.artifactAuthority, artifacts)
	require.NoError(tb, err)
	require.NoError(tb, transaction.Replace(
		handle, descriptor, renderartifact.NewLiteralContent(text),
	))
	_, delta, err := transaction.Commit()
	require.NoError(tb, err)
	return delta
}

func outputDeltaSection(text string) renderplan.Section {
	return renderplan.Section{
		Kind: renderplan.SectionKindCore, Name: "core#0",
		TextDigest: renderplan.DigestString(text), Length: len(text),
		Text: text, TextKnown: true,
	}
}

func outputDeltaArtifactDescriptor() renderartifact.Descriptor {
	return renderartifact.Descriptor{
		Family: renderartifact.General, Name: "output.txt",
		Path: "files/output.txt", RuntimePath: "files/output.txt",
	}
}

func outputDeltaPlanFile(path, kind string, reload bool, content string) *renderplan.File {
	file := outputPublicationFile(path, kind, reload, content)
	return &file
}

func requireOutputDeltaMatchesFullValidation(
	tb testing.TB,
	service *RenderService,
	snapshot *renderoutput.Snapshot,
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
	rebuilt, err := renderoutput.NewSnapshotFromDocument(
		service.outputAuthority, document, plan, artifacts, nil,
	)
	require.NoError(tb, err)
	equal, err := snapshot.ExactEqual(rebuilt)
	require.NoError(tb, err)
	require.True(tb, equal)
}

func mustOutputDeltaConfig(tb testing.TB, snapshot *renderoutput.Snapshot) string {
	tb.Helper()
	config, err := snapshot.Config()
	require.NoError(tb, err)
	return config
}
