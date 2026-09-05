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

package proposalvalidator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestSameRenderedContentRejectsChecksumCollision(t *testing.T) {
	leftFiles := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "a", Content: "bc"}},
	}
	rightFiles := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "ab", Content: "c"}},
	}
	left := exactPipelineResult(t, leftFiles)
	right := exactPipelineResult(t, rightFiles)
	left.ContentChecksum = dataplane.ComputeContentChecksum("config", leftFiles)
	right.ContentChecksum = dataplane.ComputeContentChecksum("config", rightFiles)
	require.Equal(t, left.ContentChecksum, right.ContentChecksum)
	assert.False(t, sameRenderedContent(left, right))
	assert.True(t, sameRenderedContent(left, left))
}

func TestSameRenderedContentAuthenticatesAuxiliarySnapshots(t *testing.T) {
	left := exactPipelineResult(t, mapAuxiliaryFiles("content"))
	sameForeign := exactPipelineResult(t, mapAuxiliaryFiles("content"))
	changed := exactPipelineResult(t, mapAuxiliaryFiles("changed"))
	poisoned := exactPipelineResult(t, mapAuxiliaryFiles("content"))
	poisoned.HAProxyConfig = "poison"
	poisoned.AuxiliaryFiles = mapAuxiliaryFiles("poison")
	poisoned.OutputSnapshot, _ = changed.CycleSnapshot.OutputSnapshot()
	copiedCycle := *left.CycleSnapshot

	tests := []struct {
		name  string
		left  *pipeline.PipelineResult
		right *pipeline.PipelineResult
		want  bool
	}{
		{
			name:  "same root",
			left:  left,
			right: left,
			want:  true,
		},
		{
			name:  "foreign exact bytes",
			left:  left,
			right: sameForeign,
			want:  true,
		},
		{
			name:  "changed bytes despite matching checksum",
			left:  left,
			right: changed,
		},
		{
			name:  "poisoned public shadows",
			left:  poisoned,
			right: sameForeign,
			want:  true,
		},
		{
			name:  "missing cycle",
			left:  &pipeline.PipelineResult{HAProxyConfig: "config"},
			right: left,
		},
		{
			name:  "copied cycle",
			left:  &pipeline.PipelineResult{CycleSnapshot: &copiedCycle},
			right: left,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.left.ContentChecksum = "forced-collision"
			test.right.ContentChecksum = "forced-collision"
			assert.Equal(t, test.want, sameRenderedContent(test.left, test.right))
		})
	}
}

func mapAuxiliaryFiles(content string) *dataplane.AuxiliaryFiles {
	return &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "routes.map", Content: content}},
	}
}

func exactPipelineResult(
	t *testing.T,
	auxFiles *dataplane.AuxiliaryFiles,
) *pipeline.PipelineResult {
	t.Helper()
	const config = "config"
	fixture := testutil.NewRenderCycleFixture(t)
	artifacts := fixture.Artifacts(t, auxFiles, nil)
	plan := exactPipelinePlan(config, auxFiles)
	cycle := fixture.SnapshotWithEffects(t, config, plan, artifacts, nil, nil, nil, nil)
	output, err := cycle.OutputSnapshot()
	require.NoError(t, err)
	checksum, err := cycle.ContentChecksum()
	require.NoError(t, err)
	return &pipeline.PipelineResult{
		CycleSnapshot:         cycle,
		OutputSnapshot:        output,
		HAProxyConfig:         config,
		AuxiliaryFileSnapshot: artifacts,
		ContentChecksum:       checksum,
	}
}

func exactPipelinePlan(config string, auxFiles *dataplane.AuxiliaryFiles) *renderplan.Plan {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Maps: make(map[string]renderplan.Map),
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Content: config, ContentKnown: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)),
		}},
	}
	if auxFiles != nil {
		for index := range auxFiles.MapFiles {
			file := auxFiles.MapFiles[index]
			plan.Maps[file.Path] = renderplan.Map{
				Path: file.Path, Ordered: true, Entries: renderplan.ParseMapEntries(file.Content),
			}
			plan.Files = append(plan.Files, renderplan.File{
				Path: file.Path, Kind: renderplan.FileKindMap,
				Content: file.Content, ContentKnown: true,
				Digest: renderplan.DigestString(file.Content), Size: int64(len(file.Content)),
			})
		}
	}
	plan.ComputeID()
	return plan
}
