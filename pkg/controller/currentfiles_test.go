// Copyright 2025 Philipp Hossner
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

package controller

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func setPublishedFiles(published *publishedAuxFiles, filesByGVR map[string]map[string]string) {
	const setID = "sha256:test"
	refs := make(map[string][]publishedAuxRef, len(filesByGVR))
	for gvr, files := range filesByGVR {
		referenceField := ""
		for _, kind := range publishedAuxCRDList() {
			if kind.gvr.String() == gvr {
				referenceField = kind.referenceFields[0]
				break
			}
		}
		if referenceField == "" {
			panic("unknown published auxiliary GVR: " + gvr)
		}
		resources := make(map[string]publishedAuxFile, len(files))
		for filePath, content := range files {
			name := "published-" + path.Base(filePath)
			resources[name] = publishedAuxFile{path: filePath, content: content, setID: setID}
			refs[referenceField] = append(refs[referenceField], publishedAuxRef{name: name, namespace: "haptic"})
		}
		published.setForGVR(gvr, resources)
	}
	published.setCommit(&publishedAuxCommit{setID: setID, refs: refs})
}

func currentFilesSnapshot(t *testing.T, authority *currentFilesAuthority, generation uint64) map[string]string {
	t.Helper()
	files, err := authority.Snapshot(generation)
	require.NoError(t, err)
	return files
}

func currentFilesPublishedSnapshot(t *testing.T, authority *currentFilesAuthority) map[string]string {
	t.Helper()
	files, err := authority.publishedSnapshot()
	require.NoError(t, err)
	return files
}

func buildCurrentFilesArtifactSnapshot(
	t *testing.T,
	files *dataplane.AuxiliaryFiles,
) *renderartifact.Snapshot {
	t.Helper()
	snapshot, err := dataplane.BuildAuxiliaryFileSnapshot(renderartifact.NewAuthority(), nil, files)
	require.NoError(t, err)
	return snapshot
}

func TestCurrentFilesAuthorityAcceptsOutputWithinTerm(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"ticket.keys": "published"},
		haproxyMapFileGVR.String():     {"hosts.map": "example.test backend"},
	})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()

	assert.Equal(t, map[string]string{
		"ticket.keys": "published",
		"hosts.map":   "example.test backend",
	}, currentFilesSnapshot(t, authority, generation))
	publishedSnapshot := currentFilesPublishedSnapshot(t, authority)
	publishedSnapshot["ticket.keys"] = "mutated"
	assert.Equal(t, "published", currentFilesPublishedSnapshot(t, authority)["ticket.keys"])
	authority.Accept(generation, "plan-test", &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/ticket.keys", Content: "accepted"}},
	})

	got := currentFilesSnapshot(t, authority, generation)
	assert.Equal(t, map[string]string{"ticket.keys": "accepted"}, got)
	got["ticket.keys"] = "mutated"
	assert.Equal(t, "accepted", currentFilesSnapshot(t, authority, generation)["ticket.keys"])
}

func TestCurrentFilesExactSourceTracksOnlyProjectedMapSemantics(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	generation := authority.BeginTerm()
	build := func(mapContent, generalContent, certificate string) *renderartifact.Snapshot {
		return buildCurrentFilesArtifactSnapshot(t, &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/shared", Content: mapContent}},
			GeneralFiles: []auxiliaryfiles.GeneralFile{{
				Filename: "shared", Path: "general/shared", Content: generalContent,
			}},
			SSLCertificates: []auxiliaryfiles.SSLCertificate{{
				Path: "ssl/certificate.pem", Content: certificate,
			}},
		})
	}

	require.NoError(t, authority.AcceptSnapshot(generation, "first", build("loser-a", "winner", "cert-a")))
	first, err := authority.ExactSource(generation)
	require.NoError(t, err)
	require.NoError(t, authority.AcceptSnapshot(generation, "second", build("loser-b", "winner", "cert-b")))
	semanticallyEqual, err := authority.ExactSource(generation)
	require.NoError(t, err)

	same, err := first.SameRoot(semanticallyEqual)
	require.NoError(t, err)
	assert.True(t, same)
	files, err := semanticallyEqual.MaterializeCurrentAuxFiles()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"shared": "winner"}, files)

	require.NoError(t, authority.AcceptSnapshot(generation, "third", build("loser-b", "changed", "cert-b")))
	changed, err := authority.ExactSource(generation)
	require.NoError(t, err)
	same, err = semanticallyEqual.SameRoot(changed)
	require.NoError(t, err)
	assert.False(t, same)
}

// The baseline the next render reads back has to describe what the fleet runs.
// A render HAProxy refused was taken off the pods, so its files go with it.
func TestCurrentFilesAuthorityRollsBackARefusedRendersFiles(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"ticket.keys": "published"},
	})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()

	acceptGeneralFile := func(planID, content string) {
		authority.Accept(generation, planID, &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/ticket.keys", Content: content}},
		})
	}

	acceptGeneralFile("plan-1", "accepted")
	authority.Confirm(generation, "plan-1")
	assert.Equal(t, "accepted", currentFilesSnapshot(t, authority, generation)["ticket.keys"])

	acceptGeneralFile("plan-2", "refused")
	authority.Rollback(generation, "plan-2")
	assert.Equal(t, "accepted", currentFilesSnapshot(t, authority, generation)["ticket.keys"],
		"a refused render's files must not become what the next render reads back")

	// A verdict naming a plan the baseline has moved past settles nothing.
	acceptGeneralFile("plan-3", "provisional")
	authority.Rollback(generation, "plan-2")
	assert.Equal(t, "provisional", currentFilesSnapshot(t, authority, generation)["ticket.keys"])
}

// With nothing confirmed yet, a refusal falls back to the published snapshot
// rather than keeping the files HAProxy rejected.
func TestCurrentFilesAuthorityRollsBackToPublishedBeforeAnyConfirmation(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"ticket.keys": "published"},
	})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()

	authority.Accept(generation, "plan-1", &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/ticket.keys", Content: "refused"}},
	})
	authority.Rollback(generation, "plan-1")

	assert.Equal(t, "published", currentFilesSnapshot(t, authority, generation)["ticket.keys"])
}

func TestCurrentFilesAuthorityKeepsAcceptedOutputAcrossLegacyMutationInTerm(t *testing.T) {
	mapGVR := haproxyMapFileGVR.String()
	published := newPublishedAuxFiles("haptic")
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "published"},
	})
	published.setCommit(&publishedAuxCommit{refs: map[string][]publishedAuxRef{
		"mapFiles": {{name: "map", namespace: "haptic"}},
	}})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()
	authority.Accept(generation, "plan-test", &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/routes.map", Content: "accepted"}},
	})
	assert.Equal(t, "accepted", currentFilesSnapshot(t, authority, generation)["routes.map"])

	// A retired leader's late legacy write is absorbed, not latched: the term's
	// own accepted output stays authoritative and the published view follows.
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy-mutated"},
	})
	assert.Equal(t, "accepted", currentFilesSnapshot(t, authority, generation)["routes.map"])
	assert.Equal(t, "legacy-mutated", currentFilesPublishedSnapshot(t, authority)["routes.map"])

	authority.EndTerm(generation)
	published.setForGVR(mapGVR, map[string]publishedAuxFile{
		"map": {path: "maps/routes.map", content: "legacy-mutated-again"},
	})
	files, err := authority.Snapshot(generation)
	require.ErrorContains(t, err, "legacy auxiliary publication changed without a set ID")
	assert.Nil(t, files)
}

func TestCurrentFilesAuthorityRejectsRetiredTermOutput(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"ticket.keys": "first-published"},
	})
	authority := newCurrentFilesAuthority(published)
	first := authority.BeginTerm()
	authority.Accept(first, "plan-test", &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/ticket.keys", Content: "first-accepted"}},
	})

	second := authority.BeginTerm()
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"ticket.keys": "second-published"},
	})
	authority.Accept(first, "plan-test", &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/ticket.keys", Content: "late-first"}},
	})

	assert.Equal(t, "second-published", currentFilesSnapshot(t, authority, second)["ticket.keys"])
	authority.EndTerm(first)
	assert.Equal(t, "second-published", currentFilesSnapshot(t, authority, second)["ticket.keys"])
}

func TestCurrentFilesAuthorityAcceptedEmptyOutputOverridesPublished(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"removed.file": "published"},
	})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()

	authority.Accept(generation, "plan-test", nil)
	assert.Empty(t, currentFilesSnapshot(t, authority, generation))

	authority.EndTerm(generation)
	assert.Equal(t, "published", currentFilesSnapshot(t, authority, generation)["removed.file"])
}

func TestCurrentFilesAuthorityAcceptsAuthenticatedSnapshot(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	generation := authority.BeginTerm()
	reload := true
	input := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/routes.map", Content: "map"}},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "ticket.keys", Path: "general/ticket.keys", Content: "general", ReloadOnPush: &reload},
			{Filename: "backend-ca.pem", Path: "general/backend-ca.pem", Content: "general-ca", IsCaFile: true},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "ssl/certificate.pem", Content: "certificate"}},
		SSLCaFiles:      []auxiliaryfiles.SSLCaFile{{Path: "ssl/ca.pem", Content: "ca"}},
		CRTListFiles:    []auxiliaryfiles.CRTListFile{{Path: "crt-lists/frontend.list", Content: "crt-list"}},
	}
	snapshot := buildCurrentFilesArtifactSnapshot(t, input)

	require.NoError(t, authority.AcceptSnapshot(generation, "plan-1", snapshot))
	require.Same(t, snapshot, authority.acceptedSnapshot)
	input.MapFiles[0].Content = "mutated"
	input.GeneralFiles[0].Content = "mutated"
	input.CRTListFiles[0].Content = "mutated"

	want := map[string]string{
		"routes.map":    "map",
		"ticket.keys":   "general",
		"frontend.list": "crt-list",
	}
	got := currentFilesSnapshot(t, authority, generation)
	assert.Equal(t, want, got)
	got["routes.map"] = "mutated"
	assert.Equal(t, want, currentFilesSnapshot(t, authority, generation))
}

func TestCurrentFilesAuthorityRejectsNilAndUnauthenticatedSnapshots(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	generation := authority.BeginTerm()

	require.ErrorContains(t, authority.AcceptSnapshot(generation, "plan-1", nil), "snapshot is nil")
	require.ErrorContains(t,
		authority.AcceptSnapshot(generation, "plan-1", &renderartifact.Snapshot{}),
		"snapshot is invalid",
	)
	assert.False(t, authority.hasAccepted)
	assert.Nil(t, authority.acceptedSnapshot)
}

func TestCurrentFilesAuthorityAuthenticatedEmptySnapshotOverridesPublished(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	setPublishedFiles(published, map[string]map[string]string{
		haproxyGeneralFileGVR.String(): {"removed.file": "published"},
	})
	authority := newCurrentFilesAuthority(published)
	generation := authority.BeginTerm()
	empty := buildCurrentFilesArtifactSnapshot(t, &dataplane.AuxiliaryFiles{})

	require.NoError(t, authority.AcceptSnapshot(generation, "plan-1", empty))
	assert.Empty(t, currentFilesSnapshot(t, authority, generation))
	require.Same(t, empty, authority.acceptedSnapshot)
}

func TestCurrentFilesAuthoritySnapshotRootsSettleByPointer(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	generation := authority.BeginTerm()
	confirmed := buildCurrentFilesArtifactSnapshot(t, &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/routes.map", Content: "confirmed"}},
	})
	refused := buildCurrentFilesArtifactSnapshot(t, &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/routes.map", Content: "refused"}},
	})

	require.NoError(t, authority.AcceptSnapshot(generation, "plan-1", confirmed))
	authority.Confirm(generation, "plan-1")
	require.Same(t, confirmed, authority.confirmedSnapshot)
	require.Same(t, confirmed, authority.acceptedSnapshot)

	require.NoError(t, authority.AcceptSnapshot(generation, "plan-2", refused))
	require.Same(t, refused, authority.acceptedSnapshot)
	authority.Rollback(generation, "plan-2")
	require.Same(t, confirmed, authority.acceptedSnapshot)
	assert.Equal(t, "confirmed", currentFilesSnapshot(t, authority, generation)["routes.map"])
}

func TestCurrentFilesAuthorityRejectsSnapshotFromStaleTerm(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	stale := authority.BeginTerm()
	current := authority.BeginTerm()
	snapshot := buildCurrentFilesArtifactSnapshot(t, &dataplane.AuxiliaryFiles{})

	require.ErrorContains(t, authority.AcceptSnapshot(stale, "plan-stale", snapshot), "is not active")
	assert.False(t, authority.hasAccepted)
	require.NoError(t, authority.AcceptSnapshot(current, "plan-current", snapshot))
	require.Same(t, snapshot, authority.acceptedSnapshot)

	authority.EndTerm(current)
	require.ErrorContains(t, authority.AcceptSnapshot(current, "plan-late", snapshot), "is not active")
}

func TestCurrentFilesAuthoritySettlesOnlyExactOutputRoot(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	generation := authority.BeginTerm()
	first := buildCurrentFilesOutput(t, "first")
	second := buildCurrentFilesOutput(t, "second")

	require.NoError(t, authority.AcceptOutput(generation, first))
	require.NoError(t, authority.ConfirmOutput(generation, second))
	assert.False(t, authority.hasConfirmed)
	firstPlanID, err := first.PlanID()
	require.NoError(t, err)
	authority.Confirm(generation, firstPlanID)
	assert.False(t, authority.hasConfirmed)

	require.NoError(t, authority.ConfirmOutput(generation, first))
	assert.True(t, authority.hasConfirmed)
	assert.Same(t, first, authority.confirmedOutput)
	assert.Equal(t, "first", currentFilesSnapshot(t, authority, generation)["routes.map"])

	require.NoError(t, authority.AcceptOutput(generation, second))
	require.NoError(t, authority.RollbackOutput(generation, first))
	assert.Same(t, second, authority.acceptedOutput)
	assert.Equal(t, "second", currentFilesSnapshot(t, authority, generation)["routes.map"])
	secondPlanID, err := second.PlanID()
	require.NoError(t, err)
	authority.Rollback(generation, secondPlanID)
	assert.Same(t, second, authority.acceptedOutput)

	require.NoError(t, authority.RollbackOutput(generation, second))
	assert.Same(t, first, authority.acceptedOutput)
	assert.Equal(t, "first", currentFilesSnapshot(t, authority, generation)["routes.map"])
}

func TestCurrentFilesAuthorityRejectsCopiedOutput(t *testing.T) {
	authority := newCurrentFilesAuthority(nil)
	generation := authority.BeginTerm()
	output := buildCurrentFilesOutput(t, "safe")
	copied := *output

	require.Error(t, authority.AcceptOutput(generation, &copied))
	assert.False(t, authority.hasAccepted)
	require.NoError(t, authority.AcceptOutput(generation, output))
	require.Error(t, authority.ConfirmOutput(generation, &copied))
	assert.False(t, authority.hasConfirmed)
	require.Error(t, authority.RollbackOutput(generation, &copied))
	assert.Same(t, output, authority.acceptedOutput)
}

func buildCurrentFilesOutput(tb testing.TB, content string) *renderoutput.Snapshot {
	tb.Helper()
	artifactAuthority := renderartifact.NewAuthority()
	files := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{
		Path: "maps/routes.map", Content: content,
	}}}
	artifacts, err := dataplane.BuildAuxiliaryFileSnapshot(artifactAuthority, nil, files)
	require.NoError(tb, err)
	config := "global\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0",
			TextDigest: renderplan.DigestString(config), Length: len(config),
			Text: config, TextKnown: true,
		}},
		Maps: map[string]renderplan.Map{"maps/routes.map": {
			Path: "maps/routes.map", Ordered: true,
			Entries: renderplan.ParseMapEntries(content),
		}},
		Files: []renderplan.File{
			{
				Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
				Digest: renderplan.DigestString(config), Size: int64(len(config)),
				ReloadOnChange: true, Content: config, ContentKnown: true,
			},
			{
				Path: "maps/routes.map", Kind: renderplan.FileKindMap,
				Digest: renderplan.DigestString(content), Size: int64(len(content)),
				Content: content, ContentKnown: true,
			},
		},
	}
	plan.ComputeID()
	planAuthority := renderplan.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	output, err := renderoutput.NewSnapshot(outputAuthority, config, plan, artifacts, nil)
	require.NoError(tb, err)
	return output
}
