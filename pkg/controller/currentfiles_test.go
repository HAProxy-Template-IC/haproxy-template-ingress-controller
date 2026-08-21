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
)

func setPublishedFiles(published *publishedAuxFiles, filesByGVR map[string]map[string]string) {
	const setID = "sha256:test"
	refs := make(map[string][]publishedAuxRef, len(filesByGVR))
	for gvr, files := range filesByGVR {
		resources := make(map[string]publishedAuxFile, len(files))
		for filePath, content := range files {
			name := "published-" + path.Base(filePath)
			resources[name] = publishedAuxFile{path: filePath, content: content, setID: setID}
			refs[gvr] = append(refs[gvr], publishedAuxRef{name: name, namespace: "haptic"})
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
		mapGVR: {{name: "map", namespace: "haptic"}},
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
