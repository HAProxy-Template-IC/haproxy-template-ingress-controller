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

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type deploymentEventCycle struct {
	occurrence *rendercycle.Occurrence
	cycle      *rendercycle.Snapshot
	output     *renderoutput.Snapshot
	status     *templating.StatusPatchSnapshot
	plan       *renderplan.Plan
	checksum   string
}

func deploymentEventCycleFixture(tb testing.TB) deploymentEventCycle {
	tb.Helper()
	artifactAuthority := renderartifact.NewAuthority()
	builder, err := renderartifact.NewBuilder(artifactAuthority, nil)
	require.NoError(tb, err)
	artifacts, err := builder.Build()
	require.NoError(tb, err)
	outputAuthority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
	require.NoError(tb, err)
	cycleAuthority, err := rendercycle.NewAuthority(outputAuthority)
	require.NoError(tb, err)
	config := "global\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0",
			TextDigest: renderplan.DigestString(config), Length: len(config),
			Text: config, TextKnown: true,
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Digest: renderplan.DigestString(config),
			Size: int64(len(config)), Content: config, ContentKnown: true,
		}},
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(outputAuthority, config, plan, artifacts, nil)
	require.NoError(tb, err)
	status, err := templating.NewStatusPatchCollector().Snapshot()
	require.NoError(tb, err)
	renderedEvents, err := templating.NewEventCollector().Snapshot()
	require.NoError(tb, err)
	renderedResources, err := templating.NewRenderedResourceCollector().Snapshot()
	require.NoError(tb, err)
	cycle, err := rendercycle.NewSnapshot(
		cycleAuthority, output, status, renderedEvents, renderedResources, nil,
	)
	require.NoError(tb, err)
	checksum, err := cycle.ContentChecksum()
	require.NoError(tb, err)
	occurrence, err := rendercycle.NewOccurrence(cycle)
	require.NoError(tb, err)
	return deploymentEventCycle{
		occurrence: occurrence, cycle: cycle, output: output, status: status,
		plan: plan, checksum: checksum,
	}
}

func TestDeploymentOccurrenceConstructorsDeriveEveryShadowFromAuthority(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	endpoints := []dataplane.Endpoint{{URL: "http://127.0.0.1:5555", PodName: "haproxy-0"}}

	scheduled, err := NewDeploymentScheduledEventWithOutputSnapshot(
		fixture.occurrence, endpoints, "runtime", "haptic", "config_validation", true,
	)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, scheduled.CycleSnapshot)
	assert.Same(t, fixture.output, scheduled.OutputSnapshot)
	assert.Equal(t, fixture.plan.ID, scheduled.PlanID)
	assert.Equal(t, fixture.checksum, scheduled.ContentChecksum)
	assert.Equal(t, "global\n", scheduled.Config)
	assert.Nil(t, scheduled.AuxiliaryFiles)
	assert.Nil(t, scheduled.Plan)

	poisonPlan := fixture.plan.Clone()
	poisonPlan.Sections[0].Text = "poisoned\n"
	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)
	result.DeploymentID = "deployment:1"
	result.Total = 1
	result.Succeeded = 1
	result.CycleSnapshot = nil
	result.OutputSnapshot = nil
	result.StatusPatchSnapshot = nil
	result.ContentChecksum = "poisoned-checksum"
	result.RenderProof = "poisoned-proof"
	result.Plan = poisonPlan
	completed, err := NewDeploymentCompletedEventWithOutputSnapshot(result)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, completed.CycleSnapshot)
	assert.Same(t, fixture.output, completed.OutputSnapshot)
	assert.Same(t, fixture.status, completed.StatusPatchSnapshot)
	assert.Equal(t, fixture.checksum, completed.ContentChecksum)
	assert.Nil(t, completed.Plan)
	assert.Equal(t, "poisoned-checksum", result.ContentChecksum)
	assert.Same(t, poisonPlan, result.Plan)

	skipped, err := NewDeploymentSkippedEventWithOutputSnapshot(
		fixture.occurrence, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, skipped.CycleSnapshot)
	assert.Same(t, fixture.output, skipped.OutputSnapshot)
	assert.Equal(t, fixture.checksum, skipped.ConfigHash)
	assert.Nil(t, skipped.Plan)

	publish, err := NewDeployedConfigPublishRequestWithCycle(
		"runtime", "haptic", fixture.occurrence,
	)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, publish.CycleSnapshot)
	assert.Same(t, fixture.output, publish.OutputSnapshot)
	assert.Equal(t, fixture.checksum, publish.ContentChecksum)
	assert.Empty(t, publish.Config)
	assert.Nil(t, publish.AuxiliaryFiles)
}

func TestDeploymentOccurrenceConstructorsRejectMissingAndCopiedOccurrence(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	copied := *fixture.occurrence

	_, err := NewDeploymentScheduledEventWithOutputSnapshot(
		&copied, nil, "runtime", "haptic", "config_validation", true,
	)
	require.Error(t, err)
	_, err = NewDeploymentResultWithOccurrence(&copied)
	require.Error(t, err)
	_, err = NewDeploymentSkippedEventWithOutputSnapshot(
		&copied, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.Error(t, err)
	_, err = NewDeploymentCompletedEventWithOutputSnapshot(&DeploymentResult{})
	require.Error(t, err)
	_, err = NewDeployedConfigPublishRequestWithCycle("runtime", "haptic", &copied)
	require.Error(t, err)

	_, err = NewDeploymentScheduledEventWithOutputSnapshot(
		nil, nil, "runtime", "haptic", "config_validation", true,
	)
	require.Error(t, err)
	_, err = NewDeploymentResultWithOccurrence(nil)
	require.Error(t, err)
	_, err = NewDeploymentSkippedEventWithOutputSnapshot(
		nil, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.Error(t, err)
	_, err = NewDeployedConfigPublishRequestWithCycle("runtime", "haptic", nil)
	require.Error(t, err)
}

func TestDeploymentCycleConstructorsIgnoreEverySplitShadow(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	copiedOutput := *fixture.output
	foreignStatus, err := templating.NewStatusPatchCollector().Snapshot()
	require.NoError(t, err)
	poisonPlan := fixture.plan.Clone()
	poisonPlan.Sections[0].Text = "poisoned\n"
	endpoints := []dataplane.Endpoint{{URL: "http://127.0.0.1:5555", PodName: "haproxy-0"}}

	scheduled, err := NewDeploymentScheduledEventWithCycle(
		fixture.occurrence, endpoints, "runtime", "haptic", "config_validation", true,
	)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, scheduled.CycleSnapshot)
	assert.Equal(t, fixture.plan.ID, scheduled.PlanID)
	assert.Equal(t, fixture.checksum, scheduled.ContentChecksum)
	assert.Same(t, fixture.output, scheduled.OutputSnapshot)
	assert.Same(t, fixture.status, scheduled.StatusPatchSnapshot)
	assert.Nil(t, scheduled.Plan)
	assert.Empty(t, scheduled.StatusPatches)

	assertDeploymentCompletedIgnoresSplitShadows(t, fixture, &copiedOutput, foreignStatus, poisonPlan)

	skipped, err := NewDeploymentSkippedEventWithCycle(
		fixture.occurrence, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, skipped.CycleSnapshot)
	assert.Equal(t, fixture.checksum, skipped.ConfigHash)
	assert.Same(t, fixture.output, skipped.OutputSnapshot)
	assert.Same(t, fixture.status, skipped.StatusPatchSnapshot)
	assert.Nil(t, skipped.Plan)
	assert.Empty(t, skipped.StatusPatches)

	publish, err := NewDeployedConfigPublishRequestWithCycle(
		"runtime", "haptic", fixture.occurrence,
	)
	require.NoError(t, err)
	publish.CycleSnapshot = nil
	publish.OutputSnapshot = &copiedOutput
	publish.Config = "poisoned\n"
	publish.AuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	publish.ContentChecksum = "poisoned-checksum"
	clone, ok := publish.CloneForSubscriber().(*DeployedConfigPublishRequest)
	require.True(t, ok)
	assert.Same(t, fixture.cycle, clone.CycleSnapshot)
	assert.Same(t, fixture.output, clone.OutputSnapshot)
	assert.Equal(t, fixture.checksum, clone.ContentChecksum)
	assert.Empty(t, clone.Config)
	assert.Nil(t, clone.AuxiliaryFiles)
}

func assertDeploymentCompletedIgnoresSplitShadows(
	t *testing.T,
	fixture deploymentEventCycle,
	copiedOutput *renderoutput.Snapshot,
	foreignStatus *templating.StatusPatchSnapshot,
	poisonPlan *renderplan.Plan,
) {
	t.Helper()
	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)
	result.CycleSnapshot = nil
	result.OutputSnapshot = copiedOutput
	result.DeploymentID = "deployment:1"
	result.Total = 1
	result.Succeeded = 1
	result.StatusPatches = []templating.StatusPatch{{Name: "poisoned"}}
	result.StatusPatchSnapshot = foreignStatus
	result.ContentChecksum = "poisoned-checksum"
	result.RenderProof = "poisoned-proof"
	result.Plan = poisonPlan
	completed, err := NewDeploymentCompletedEventWithCycle(result)
	require.NoError(t, err)
	assert.Same(t, fixture.cycle, completed.CycleSnapshot)
	assert.Equal(t, fixture.checksum, completed.ContentChecksum)
	assert.Same(t, fixture.output, completed.OutputSnapshot)
	assert.Same(t, fixture.status, completed.StatusPatchSnapshot)
	assert.Nil(t, completed.Plan)
	assert.Empty(t, completed.StatusPatches)
	assert.Same(t, copiedOutput, result.OutputSnapshot)
	assert.Same(t, fixture.status, mustCycleStatus(t, completed.CycleSnapshot))
}

func TestDeploymentCycleConstructorsRejectCopiedOccurrenceAndMissingCarrier(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	copied := *fixture.occurrence

	_, err := NewDeploymentScheduledEventWithCycle(
		&copied, nil, "runtime", "haptic", "config_validation", true,
	)
	require.Error(t, err)
	_, err = NewDeploymentCompletedEventWithCycle(&DeploymentResult{})
	require.Error(t, err)
	_, err = NewDeploymentSkippedEventWithCycle(
		&copied, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.Error(t, err)

	_, err = NewDeploymentScheduledEventWithCycle(
		nil, nil, "runtime", "haptic", "config_validation", true,
	)
	require.Error(t, err)
	_, err = NewDeploymentSkippedEventWithCycle(
		nil, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.Error(t, err)
}

func mustCycleStatus(tb testing.TB, cycle *rendercycle.Snapshot) *templating.StatusPatchSnapshot {
	tb.Helper()
	status, err := cycle.StatusPatchSnapshot()
	require.NoError(tb, err)
	return status
}
