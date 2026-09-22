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

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestMaterializePreviewIncludesSnapshotOutputs(t *testing.T) {
	files, err := dataplane.BuildAuxiliaryFileSnapshot(renderartifact.NewAuthority(), nil, &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "routes.map", Content: "shop.example.com backend_shop"}},
	})
	require.NoError(t, err)
	patches := templating.NewStatusPatchCollector()
	require.NoError(t, patches.Register("default", "sample", "example.com/v1", "Example", map[string]map[string]any{
		"rendered": {"ready": true},
	}))
	statusSnapshot, err := patches.Snapshot()
	require.NoError(t, err)
	events := templating.NewEventCollector()
	require.NoError(t, events.Register("default", "sample", "example.com/v1", "Example", "Normal", "Ready", "Ready"))
	eventSnapshot, err := events.Snapshot()
	require.NoError(t, err)
	resources := templating.NewRenderedResourceCollector()
	require.NoError(t, resources.Register("example.com/v1", "Example", "default", "sample", map[string]any{"spec": map[string]any{"enabled": true}}))
	resourceSnapshot, err := resources.Snapshot()
	require.NoError(t, err)
	rendered := &renderer.RenderResult{
		HAProxyConfig: "global\n", DurationMs: 12,
		Plan: &renderplan.Plan{ID: "preview-plan"}, AuxiliaryFileSnapshot: files,
		StatusPatchSnapshot: statusSnapshot, EventSnapshot: eventSnapshot, RenderedResourceSnapshot: resourceSnapshot,
	}
	preview, err := materializePreview(rendered)
	require.NoError(t, err)
	assert.Equal(t, "global\n", preview.HAProxyConfig)
	assert.Equal(t, int64(12), preview.DurationMs)
	assert.Equal(t, "preview-plan", preview.Plan.ID)
	require.Len(t, preview.AuxiliaryFiles.MapFiles, 1)
	assert.Equal(t, "shop.example.com backend_shop", preview.AuxiliaryFiles.MapFiles[0].Content)
	require.Len(t, preview.StatusPatches, 1)
	assert.Equal(t, true, preview.StatusPatches[0].Variants["rendered"]["ready"])
	require.Len(t, preview.Events, 1)
	assert.Equal(t, "Ready", preview.Events[0].Reason)
	require.Len(t, preview.RenderedResources, 1)
	assert.Equal(t, "sample", preview.RenderedResources[0].Name)
	assert.Nil(t, rendered.AuxiliaryFiles)
	assert.Nil(t, rendered.StatusPatches)
}

func TestMaterializePreviewRejectsInvalidSnapshot(t *testing.T) {
	preview, err := materializePreview(&renderer.RenderResult{AuxiliaryFileSnapshot: &renderartifact.Snapshot{}})
	require.ErrorContains(t, err, "reading rendered files")
	assert.Nil(t, preview)
}

func TestMaterializePreviewIncludesSnapshotPlan(t *testing.T) {
	config := "global\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      []renderplan.Section{{Kind: renderplan.SectionKindCore, Name: "core#0", TextDigest: renderplan.DigestString(config), Length: len(config), Text: config, TextKnown: true}},
		Files:         []renderplan.File{{Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig, ReloadOnChange: true, Digest: renderplan.DigestString(config), Size: int64(len(config)), Content: config, ContentKnown: true}},
	}
	plan.ComputeID()
	artifacts := renderartifact.NewAuthority()
	authority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifacts)
	require.NoError(t, err)
	files, err := dataplane.BuildAuxiliaryFileSnapshot(artifacts, nil, &dataplane.AuxiliaryFiles{})
	require.NoError(t, err)
	snapshot, err := renderoutput.NewSnapshot(authority, config, plan, files, nil)
	require.NoError(t, err)
	rendered := &renderer.RenderResult{HAProxyConfig: config, OutputSnapshot: snapshot}
	preview, err := materializePreview(rendered)
	require.NoError(t, err)
	require.NotNil(t, preview.Plan)
	assert.Equal(t, plan.ID, preview.Plan.ID)
	assert.Nil(t, rendered.Plan)
}
