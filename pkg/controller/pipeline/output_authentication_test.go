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

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestAuthenticateRenderOutputRequiresCycle(t *testing.T) {
	cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "global\n", nil, nil)
	output, err := cycle.OutputSnapshot()
	require.NoError(t, err)

	_, err = authenticateRenderOutput(&renderer.RenderResult{
		OutputSnapshot: output,
		HAProxyConfig:  "global\n",
	})
	require.ErrorContains(t, err, "no authenticated render cycle")
}

func TestAuthenticateRenderOutputIgnoresPoisonedShadows(t *testing.T) {
	trustedCycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "global\n", nil, nil)
	poisonedCycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "defaults\n", nil, nil)
	trustedOutput, err := trustedCycle.OutputSnapshot()
	require.NoError(t, err)
	poisonedOutput, err := poisonedCycle.OutputSnapshot()
	require.NoError(t, err)

	authenticated, err := authenticateRenderOutput(&renderer.RenderResult{
		CycleSnapshot:   trustedCycle,
		OutputSnapshot:  poisonedOutput,
		HAProxyConfig:   "poisoned\n",
		PlanID:          "poisoned-plan",
		ContentChecksum: "poisoned-checksum",
	})
	require.NoError(t, err)
	assert.Same(t, trustedCycle, authenticated.cycle)
	assert.Same(t, trustedOutput, authenticated.snapshot)
	assert.Equal(t, "global\n", authenticated.config)
}

func TestAuthenticatedPipelineResultOutputRequiresCycle(t *testing.T) {
	_, _, err := authenticatedPipelineResultOutput(&PipelineResult{})
	require.ErrorContains(t, err, "no authenticated render cycle")
}

func TestAuthenticatedPipelineResultOutputIgnoresPoisonedShadows(t *testing.T) {
	trustedCycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "global\n", nil, nil)
	poisonedCycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "defaults\n", nil, nil)
	trustedOutput, err := trustedCycle.OutputSnapshot()
	require.NoError(t, err)
	poisonedOutput, err := poisonedCycle.OutputSnapshot()
	require.NoError(t, err)
	trustedChecksum, err := trustedCycle.ContentChecksum()
	require.NoError(t, err)

	output, checksum, err := authenticatedPipelineResultOutput(&PipelineResult{
		CycleSnapshot:   trustedCycle,
		OutputSnapshot:  poisonedOutput,
		HAProxyConfig:   "poisoned\n",
		ContentChecksum: "poisoned-checksum",
	})
	require.NoError(t, err)
	assert.Same(t, trustedOutput, output)
	assert.Equal(t, trustedChecksum, checksum)
}

func TestPipelineResultMaterializersRequireCycleAndIgnorePublicShadows(t *testing.T) {
	result := &PipelineResult{}
	_, err := result.MaterializeAuxiliaryFiles()
	require.ErrorContains(t, err, "no authenticated render cycle")
	_, err = result.MaterializeStatusPatches()
	require.ErrorContains(t, err, "no authenticated render cycle")
	_, err = result.MaterializeEvents()
	require.ErrorContains(t, err, "no authenticated render cycle")
	_, err = result.MaterializeRenderedResources()
	require.ErrorContains(t, err, "no authenticated render cycle")

	result.CycleSnapshot = testutil.NewRenderCycleFixture(t).Snapshot(t, "global\n", nil, nil)
	result.AuxiliaryFiles = &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "/poison", Content: "poison"}},
	}
	result.StatusPatches = []templating.StatusPatch{{Variants: map[string]map[string]any{"poison": {"value": true}}}}
	result.Events = []templating.RenderedEvent{{Name: "poison"}}
	result.RenderedResources = []templating.RenderedResource{{Name: "poison"}}

	files, err := result.MaterializeAuxiliaryFiles()
	require.NoError(t, err)
	assert.Empty(t, files.GeneralFiles)
	patches, err := result.MaterializeStatusPatches()
	require.NoError(t, err)
	assert.Empty(t, patches)
	events, err := result.MaterializeEvents()
	require.NoError(t, err)
	assert.Empty(t, events)
	resources, err := result.MaterializeRenderedResources()
	require.NoError(t, err)
	assert.Empty(t, resources)
}
