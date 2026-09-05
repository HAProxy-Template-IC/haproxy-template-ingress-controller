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

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// RenderCycleFixture owns one authenticated test lineage.
type RenderCycleFixture struct {
	cycleAuthority    *rendercycle.Authority
	outputAuthority   *renderoutput.Authority
	artifactAuthority *renderartifact.Authority
	artifacts         *renderartifact.Snapshot
	events            *templating.RenderedEventSnapshot
	resources         *templating.RenderedResourceSnapshot
}

// NewRenderCycleFixture creates one authenticated lineage with empty artifacts.
func NewRenderCycleFixture(tb testing.TB) *RenderCycleFixture {
	tb.Helper()
	artifactAuthority := renderartifact.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
	require.NoError(tb, err)
	cycleAuthority, err := rendercycle.NewAuthority(outputAuthority)
	require.NoError(tb, err)
	artifactBuilder, err := renderartifact.NewBuilder(artifactAuthority, nil)
	require.NoError(tb, err)
	artifacts, err := artifactBuilder.Build()
	require.NoError(tb, err)
	events, err := templating.NewEventCollector().Snapshot()
	require.NoError(tb, err)
	resources, err := templating.NewRenderedResourceCollector().Snapshot()
	require.NoError(tb, err)
	return &RenderCycleFixture{
		cycleAuthority: cycleAuthority, outputAuthority: outputAuthority,
		artifactAuthority: artifactAuthority, artifacts: artifacts,
		events: events, resources: resources,
	}
}

// Snapshot seals one output and status set after previous in this lineage.
func (f *RenderCycleFixture) Snapshot(
	tb testing.TB,
	config string,
	status *templating.StatusPatchSnapshot,
	previous *rendercycle.Snapshot,
) *rendercycle.Snapshot {
	tb.Helper()
	return f.SnapshotWithEffects(
		tb, config, nil, nil, status, nil, nil, previous,
	)
}

// Artifacts seals auxiliary files in this fixture's output lineage.
func (f *RenderCycleFixture) Artifacts(
	tb testing.TB,
	files *dataplane.AuxiliaryFiles,
	previous *renderartifact.Snapshot,
) *renderartifact.Snapshot {
	tb.Helper()
	artifacts, err := dataplane.BuildAuxiliaryFileSnapshot(
		f.artifactAuthority, previous, files,
	)
	require.NoError(tb, err)
	return artifacts
}

// SnapshotWithEffects seals one exact test output and effect set.
func (f *RenderCycleFixture) SnapshotWithEffects(
	tb testing.TB,
	config string,
	plan *renderplan.Plan,
	artifacts *renderartifact.Snapshot,
	status *templating.StatusPatchSnapshot,
	events *templating.RenderedEventSnapshot,
	resources *templating.RenderedResourceSnapshot,
	previous *rendercycle.Snapshot,
) *rendercycle.Snapshot {
	tb.Helper()
	if status == nil {
		var err error
		status, err = templating.NewStatusPatchCollector().Snapshot()
		require.NoError(tb, err)
	}
	if events == nil {
		events = f.events
	}
	if resources == nil {
		resources = f.resources
	}
	if artifacts == nil {
		artifacts = f.artifacts
	}
	var previousOutput *renderoutput.Snapshot
	if previous != nil {
		var err error
		previousOutput, err = previous.OutputSnapshot()
		require.NoError(tb, err)
	}
	if plan == nil {
		plan = &renderplan.Plan{
			SchemaVersion: renderplan.SchemaVersion,
			Sections: []renderplan.Section{{
				Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
				TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
			}},
			Files: []renderplan.File{{
				Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
				ReloadOnChange: true, Content: config, ContentKnown: true,
				Digest: renderplan.DigestString(config), Size: int64(len(config)),
			}},
		}
		plan.ComputeID()
	}
	output, err := renderoutput.NewSnapshot(
		f.outputAuthority, config, plan, artifacts, previousOutput,
	)
	require.NoError(tb, err)
	snapshot, err := rendercycle.NewSnapshot(
		f.cycleAuthority, output, status, events, resources, previous,
	)
	require.NoError(tb, err)
	return snapshot
}
