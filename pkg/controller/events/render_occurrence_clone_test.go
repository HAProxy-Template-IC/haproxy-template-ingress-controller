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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var (
	_ busevents.FanoutIsolatedEvent = (*TemplateRenderedEvent)(nil)
	_ busevents.FanoutIsolatedEvent = (*ReconciliationCompletedEvent)(nil)
	_ busevents.FanoutIsolatedEvent = (*ResourcesAppliedEvent)(nil)
	_ busevents.FanoutIsolatedEvent = (*RenderGateCompletedEvent)(nil)
	_ busevents.FanoutIsolatedEvent = (*DeploymentScheduledEvent)(nil)
	_ busevents.FanoutIsolatedEvent = (*DeploymentCompletedEvent)(nil)
	_ busevents.FanoutIsolatedEvent = (*DeploymentSkippedEvent)(nil)
)

func TestAuthenticatedSubscriberClonesRestoreAuthorityShadows(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	foreign := deploymentEventCycleFixture(t)
	proof, err := fixture.occurrence.Proof()
	require.NoError(t, err)
	foreignProof, err := foreign.occurrence.Proof()
	require.NoError(t, err)

	assertTemplateCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
	assertReconciliationCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
	assertResourcesCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
	assertGateCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
	assertScheduledCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
	assertCompletedCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
	assertSkippedCloneRestoresAuthority(t, fixture, foreign, proof, foreignProof)
}

func assertTemplateCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	templateEvent, err := NewTemplateRenderedEventWithOccurrence(
		fixture.occurrence, 1, "test", true,
	)
	require.NoError(t, err)
	templateEvent.CycleSnapshot = foreign.cycle
	templateEvent.OutputSnapshot = foreign.output
	templateEvent.RenderProof = foreignProof
	templateClone := templateEvent.CloneForSubscriber().(*TemplateRenderedEvent)
	assert.Same(t, fixture.cycle, templateClone.CycleSnapshot)
	assert.Same(t, fixture.output, templateClone.OutputSnapshot)
	assert.Same(t, fixture.status, templateClone.StatusPatchSnapshot)
	assert.Equal(t, proof, templateClone.RenderProof)
}

func assertReconciliationCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	reconciliation, err := NewReconciliationCompletedEventWithCycle(2, fixture.occurrence)
	require.NoError(t, err)
	reconciliation.CycleSnapshot = foreign.cycle
	reconciliation.RenderProof = foreignProof
	reconciliation.StatusPatchSnapshot = foreign.status
	reconciliationClone := reconciliation.CloneForSubscriber().(*ReconciliationCompletedEvent)
	assert.Same(t, fixture.cycle, reconciliationClone.CycleSnapshot)
	assert.Same(t, fixture.status, reconciliationClone.StatusPatchSnapshot)
	assert.Equal(t, proof, reconciliationClone.RenderProof)
}

func assertResourcesCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	resources, err := NewResourcesAppliedEventWithCycle(fixture.occurrence)
	require.NoError(t, err)
	resources.CycleSnapshot = foreign.cycle
	resources.RenderProof = foreignProof
	resources.StatusPatchSnapshot = foreign.status
	resourcesClone := resources.CloneForSubscriber().(*ResourcesAppliedEvent)
	assert.Same(t, fixture.cycle, resourcesClone.CycleSnapshot)
	assert.Same(t, fixture.status, resourcesClone.StatusPatchSnapshot)
	assert.Equal(t, proof, resourcesClone.RenderProof)
}

func assertGateCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	gate, err := NewRenderGateCompletedEventWithCycle(
		fixture.occurrence, true, false, true, "", false, 3,
	)
	require.NoError(t, err)
	gate.CycleSnapshot = foreign.cycle
	gate.OutputSnapshot = foreign.output
	gate.RenderProof = foreignProof
	gateClone := gate.CloneForSubscriber().(*RenderGateCompletedEvent)
	assert.Same(t, fixture.cycle, gateClone.CycleSnapshot)
	assert.Same(t, fixture.output, gateClone.OutputSnapshot)
	assert.Equal(t, proof, gateClone.RenderProof)
}

func assertScheduledCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	scheduled, err := NewDeploymentScheduledEventWithCycle(
		fixture.occurrence, nil, "runtime", "haptic", "test", true,
	)
	require.NoError(t, err)
	scheduled.CycleSnapshot = foreign.cycle
	scheduled.OutputSnapshot = foreign.output
	scheduled.StatusPatchSnapshot = foreign.status
	scheduled.RenderProof = foreignProof
	scheduledClone := scheduled.CloneForSubscriber().(*DeploymentScheduledEvent)
	assert.Same(t, fixture.cycle, scheduledClone.CycleSnapshot)
	assert.Same(t, fixture.output, scheduledClone.OutputSnapshot)
	assert.Same(t, fixture.status, scheduledClone.StatusPatchSnapshot)
	assert.Equal(t, proof, scheduledClone.RenderProof)
}

func assertCompletedCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)
	completed, err := NewDeploymentCompletedEventWithCycle(result)
	require.NoError(t, err)
	completed.CycleSnapshot = foreign.cycle
	completed.OutputSnapshot = foreign.output
	completed.StatusPatchSnapshot = foreign.status
	completed.RenderProof = foreignProof
	completedClone := completed.CloneForSubscriber().(*DeploymentCompletedEvent)
	assert.Same(t, fixture.cycle, completedClone.CycleSnapshot)
	assert.Same(t, fixture.output, completedClone.OutputSnapshot)
	assert.Same(t, fixture.status, completedClone.StatusPatchSnapshot)
	assert.Equal(t, proof, completedClone.RenderProof)
}

func assertSkippedCloneRestoresAuthority(
	t *testing.T,
	fixture, foreign deploymentEventCycle,
	proof, foreignProof string,
) {
	t.Helper()
	skipped, err := NewDeploymentSkippedEventWithCycle(
		fixture.occurrence, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.NoError(t, err)
	skipped.CycleSnapshot = foreign.cycle
	skipped.OutputSnapshot = foreign.output
	skipped.StatusPatchSnapshot = foreign.status
	skipped.RenderProof = foreignProof
	skippedClone := skipped.CloneForSubscriber().(*DeploymentSkippedEvent)
	assert.Same(t, fixture.cycle, skippedClone.CycleSnapshot)
	assert.Same(t, fixture.output, skippedClone.OutputSnapshot)
	assert.Same(t, fixture.status, skippedClone.StatusPatchSnapshot)
	assert.Equal(t, proof, skippedClone.RenderProof)
}

func TestLegacySubscriberClonesOwnMutablePayloads(t *testing.T) {
	statusPatches := []templating.StatusPatch{{
		Name: "route",
		Variants: map[string]map[string]any{
			"rendered": {"nested": map[string]any{"value": "stable"}},
		},
	}}
	renderedResources := []templating.RenderedResource{{
		Name: "service",
		Object: map[string]any{
			"spec": map[string]any{"ports": []any{map[string]any{"port": 80}}},
		},
	}}
	auxiliaryFiles := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{
		Path: "maps/routes.map", Content: "route backend\n",
	}}}
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      []renderplan.Section{{Name: "section", Text: "stable"}},
	}

	t.Run("template", func(t *testing.T) {
		event := NewTemplateRenderedEvent(
			"cfg", auxiliaryFiles, statusPatches, renderedResources, 1, 1,
			"test", "checksum", plan, "plan", true,
		)
		first := event.CloneForSubscriber().(*TemplateRenderedEvent)
		second := event.CloneForSubscriber().(*TemplateRenderedEvent)
		first.AuxiliaryFiles.MapFiles[0].Content = "poisoned"
		first.StatusPatches[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "poisoned"
		first.RenderedResources[0].Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["port"] = 81
		first.Plan.Sections[0].Text = "poisoned"
		assert.Equal(t, "route backend\n", second.AuxiliaryFiles.MapFiles[0].Content)
		assert.Equal(t, "stable", nestedStatusValue(second.StatusPatches))
		assert.Equal(t, 80, nestedResourcePort(second.RenderedResources))
		assert.Equal(t, "stable", second.Plan.Sections[0].Text)
	})

	t.Run("reconciliation and resources", func(t *testing.T) {
		reconciliation := NewReconciliationCompletedEvent(
			1, "plan", renderedResources, statusPatches,
		)
		reconciliation.Events = []templating.RenderedEvent{{Name: "stable"}}
		first := reconciliation.CloneForSubscriber().(*ReconciliationCompletedEvent)
		second := reconciliation.CloneForSubscriber().(*ReconciliationCompletedEvent)
		first.RenderedResources[0].Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["port"] = 81
		first.StatusPatches[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "poisoned"
		first.Events[0].Name = "poisoned"
		assert.Equal(t, 80, nestedResourcePort(second.RenderedResources))
		assert.Equal(t, "stable", nestedStatusValue(second.StatusPatches))
		assert.Equal(t, "stable", second.Events[0].Name)

		resources := NewResourcesAppliedEvent(statusPatches)
		resourcesFirst := resources.CloneForSubscriber().(*ResourcesAppliedEvent)
		resourcesSecond := resources.CloneForSubscriber().(*ResourcesAppliedEvent)
		resourcesFirst.StatusPatches[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "poisoned"
		assert.Equal(t, "stable", nestedStatusValue(resourcesSecond.StatusPatches))
	})

	t.Run("gate", func(t *testing.T) {
		event := NewRenderGateCompletedEventWithIdentity(
			"plan", "legacy", plan, true, false, true, "", false, 1,
		)
		first := event.CloneForSubscriber().(*RenderGateCompletedEvent)
		second := event.CloneForSubscriber().(*RenderGateCompletedEvent)
		first.Plan.Sections[0].Text = "poisoned"
		assert.Equal(t, "stable", second.Plan.Sections[0].Text)
	})

	t.Run("deployment", func(t *testing.T) {
		scheduled := NewDeploymentScheduledEvent(
			"cfg", auxiliaryFiles, []dataplane.Endpoint{{URL: "stable"}},
			"runtime", "haptic", "test", "checksum", plan, "plan",
			statusPatches, true,
		)
		scheduledFirst := scheduled.CloneForSubscriber().(*DeploymentScheduledEvent)
		scheduledSecond := scheduled.CloneForSubscriber().(*DeploymentScheduledEvent)
		scheduledFirst.AuxiliaryFiles.MapFiles[0].Content = "poisoned"
		scheduledFirst.Endpoints[0].URL = "poisoned"
		scheduledFirst.StatusPatches[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "poisoned"
		scheduledFirst.Plan.Sections[0].Text = "poisoned"
		assert.Equal(t, "route backend\n", scheduledSecond.AuxiliaryFiles.MapFiles[0].Content)
		assert.Equal(t, "stable", scheduledSecond.Endpoints[0].URL)
		assert.Equal(t, "stable", nestedStatusValue(scheduledSecond.StatusPatches))
		assert.Equal(t, "stable", scheduledSecond.Plan.Sections[0].Text)

		completed := NewDeploymentCompletedEvent(&DeploymentResult{
			OperationBreakdown: map[string]int{"stable": 1},
			StatusPatches:      statusPatches,
			Plan:               plan,
		})
		completedFirst := completed.CloneForSubscriber().(*DeploymentCompletedEvent)
		completedSecond := completed.CloneForSubscriber().(*DeploymentCompletedEvent)
		completedFirst.OperationBreakdown["stable"] = 2
		completedFirst.StatusPatches[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "poisoned"
		completedFirst.Plan.Sections[0].Text = "poisoned"
		assert.Equal(t, 1, completedSecond.OperationBreakdown["stable"])
		assert.Equal(t, "stable", nestedStatusValue(completedSecond.StatusPatches))
		assert.Equal(t, "stable", completedSecond.Plan.Sections[0].Text)

		skipped := NewDeploymentSkippedEventWithIdentity(
			1, SkipReasonConfigUnchanged, "checksum", "pods", statusPatches,
			"legacy", plan,
		)
		skippedFirst := skipped.CloneForSubscriber().(*DeploymentSkippedEvent)
		skippedSecond := skipped.CloneForSubscriber().(*DeploymentSkippedEvent)
		skippedFirst.StatusPatches[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "poisoned"
		skippedFirst.Plan.Sections[0].Text = "poisoned"
		assert.Equal(t, "stable", nestedStatusValue(skippedSecond.StatusPatches))
		assert.Equal(t, "stable", skippedSecond.Plan.Sections[0].Text)
	})
}

func nestedStatusValue(patches []templating.StatusPatch) any {
	return patches[0].Variants["rendered"]["nested"].(map[string]any)["value"]
}

func nestedResourcePort(resources []templating.RenderedResource) any {
	return resources[0].Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["port"]
}
