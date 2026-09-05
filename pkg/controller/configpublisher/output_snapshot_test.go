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

package configpublisher

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRenderedConfigEntryFromOutputSnapshot(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	event, snapshot, artifacts := fixture.event, fixture.snapshot, fixture.artifacts
	config, checksum := fixture.config, fixture.checksum
	event.ContentChecksum = "mutable-shadow"

	entry, err := renderedConfigEntryFromEvent(event)
	require.NoError(t, err)
	assert.Same(t, snapshot, entry.outputSnapshot)
	assert.Same(t, artifacts, entry.artifactSnapshot)
	assert.Equal(t, config, entry.config)
	assert.Equal(t, checksum, entry.contentChecksum)
	assert.Equal(t, event.PlanID, entry.planID)
	assert.Nil(t, entry.auxFiles)

	component := &Component{}
	templateConfig := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	request := component.buildPublishRequest(templateConfig, entry)
	assert.Same(t, snapshot, request.OutputSnapshot)
	assert.Empty(t, request.Config)
	assert.Nil(t, request.AuxiliaryFiles)
	assert.Nil(t, request.AuxiliaryFileSnapshot)
}

func TestRenderedConfigEntryFromDeployedOutputSnapshot(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	rendered, snapshot, artifacts := fixture.event, fixture.snapshot, fixture.artifacts
	config, checksum := fixture.config, fixture.checksum
	occurrence, err := rendered.RenderOccurrence()
	require.NoError(t, err)
	event, err := events.NewDeployedConfigPublishRequestWithCycle(
		"test-haproxycfg", "default", occurrence,
	)
	require.NoError(t, err)
	event.ContentChecksum = "mutable-shadow"

	entry, err := renderedConfigEntryFromDeployedRequest(event)
	require.NoError(t, err)
	assert.Same(t, snapshot, entry.outputSnapshot)
	assert.Same(t, artifacts, entry.artifactSnapshot)
	assert.Equal(t, config, entry.config)
	assert.Equal(t, checksum, entry.contentChecksum)
	assert.NotEmpty(t, entry.planID)
	assert.Nil(t, entry.auxFiles)

	request := (&Component{}).buildPublishRequest(
		&v1alpha1.HAProxyTemplateConfig{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}},
		entry,
	)
	assert.Same(t, snapshot, request.OutputSnapshot)
	assert.Empty(t, request.Checksum)
	assert.Empty(t, request.Config)
	assert.Nil(t, request.AuxiliaryFiles)
}

func TestRenderedConfigEntryIgnoresOutputShadows(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	base, snapshot, artifacts := fixture.event, fixture.snapshot, fixture.artifacts
	config, checksum := fixture.config, fixture.checksum
	poisonedArtifacts := controllerPublisherPoisonArtifacts(t)

	tests := []struct {
		name   string
		mutate func(*events.TemplateRenderedEvent)
	}{
		{name: "config", mutate: func(event *events.TemplateRenderedEvent) {
			event.HAProxyConfig = "evil"
		}},
		{name: "legacy auxiliary files", mutate: func(event *events.TemplateRenderedEvent) {
			event.AuxiliaryFiles = &dataplane.AuxiliaryFiles{}
		}},
		{name: "auxiliary snapshot", mutate: func(event *events.TemplateRenderedEvent) {
			event.AuxiliaryFileSnapshot = poisonedArtifacts
		}},
		{name: "plan", mutate: func(event *events.TemplateRenderedEvent) {
			event.Plan = &renderplan.Plan{}
		}},
		{name: "plan ID", mutate: func(event *events.TemplateRenderedEvent) {
			event.PlanID = "wrong"
		}},
		{name: "config bytes", mutate: func(event *events.TemplateRenderedEvent) {
			event.ConfigBytes++
		}},
		{name: "artifact count", mutate: func(event *events.TemplateRenderedEvent) {
			event.AuxiliaryFileCount++
		}},
		{name: "content checksum", mutate: func(event *events.TemplateRenderedEvent) {
			event.ContentChecksum = "wrong"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := *base
			test.mutate(&event)
			entry, err := renderedConfigEntryFromEvent(&event)
			require.NoError(t, err)
			assert.Same(t, snapshot, entry.outputSnapshot)
			assert.Same(t, artifacts, entry.artifactSnapshot)
			assert.Equal(t, config, entry.config)
			assert.Equal(t, checksum, entry.contentChecksum)
		})
	}
	_, err := renderedConfigEntryFromEvent(nil)
	require.Error(t, err)
}

func TestRenderedConfigEntryUsesCycleOutputDespitePoisonedEventCarriers(t *testing.T) {
	fixture := newControllerPublisherCycleFixture(t)
	outputA := fixture.output(t, "global\n", nil)
	outputB := fixture.output(t, "defaults\n", outputA)
	cycleA := fixture.cycle(t, outputA, nil)
	event, err := events.NewTemplateRenderedEventWithCycle(cycleA, 10, "test", true)
	require.NoError(t, err)

	event.OutputSnapshot = outputB
	event.HAProxyConfig = "poisoned config\n"
	event.AuxiliaryFiles = &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{
		Path: "maps/poison.map", Content: "poison\n",
	}}}
	event.AuxiliaryFileSnapshot = controllerPublisherPoisonArtifacts(t)
	event.ContentChecksum = "poisoned checksum"
	event.Plan = &renderplan.Plan{}
	event.PlanID = "poisoned plan"
	event.ConfigBytes = 1
	event.AuxiliaryFileCount = 999

	entry, err := renderedConfigEntryFromEvent(event)
	require.NoError(t, err)
	artifacts, err := outputA.ArtifactSnapshot()
	require.NoError(t, err)
	checksum, err := outputA.ContentChecksum()
	require.NoError(t, err)
	planID, err := outputA.PlanID()
	require.NoError(t, err)
	assert.Same(t, outputA, entry.outputSnapshot)
	assert.Same(t, artifacts, entry.artifactSnapshot)
	assert.Equal(t, "global\n", entry.config)
	assert.Equal(t, checksum, entry.contentChecksum)
	assert.Equal(t, planID, entry.planID)
	assert.Nil(t, entry.auxFiles)
}

func TestRenderedConfigEntryRejectsInvalidCycleWithoutOutputFallback(t *testing.T) {
	output, _, _ := controllerPublisherOutputFixture(t)
	_, err := renderedConfigEntryFromEvent(&events.TemplateRenderedEvent{
		CycleSnapshot:  &rendercycle.Snapshot{},
		OutputSnapshot: output,
	})
	require.ErrorContains(t, err, "authenticating render occurrence")
}

func TestRenderedConfigEntryCycleABAReversionIsNotDeduplicated(t *testing.T) {
	fixture := newControllerPublisherCycleFixture(t)
	outputA := fixture.output(t, "global\n", nil)
	outputB := fixture.output(t, "global\n  daemon\n", outputA)
	outputAAgain := fixture.output(t, "global\n", outputB)
	cycleA := fixture.cycle(t, outputA, nil)
	cycleB := fixture.cycle(t, outputB, cycleA)
	cycleAAgain := fixture.cycle(t, outputAAgain, cycleB)

	entry := func(cycle *rendercycle.Snapshot, poisonedOutput *renderoutput.Snapshot) *renderedConfigEntry {
		t.Helper()
		event, err := events.NewTemplateRenderedEventWithCycle(cycle, 0, "test", true)
		require.NoError(t, err)
		event.OutputSnapshot = poisonedOutput
		result, err := renderedConfigEntryFromEvent(event)
		require.NoError(t, err)
		return result
	}
	entryA := entry(cycleA, outputB)
	entryB := entry(cycleB, outputA)
	entryAAgain := entry(cycleAAgain, outputB)

	assert.Equal(t, "global\n", entryA.config)
	assert.Equal(t, "global\n  daemon\n", entryB.config)
	assert.Equal(t, "global\n", entryAAgain.config)
	assert.Equal(t, entryA.contentChecksum, entryAAgain.contentChecksum)
	assert.NotEqual(t, entryA.contentChecksum, entryB.contentChecksum)
	same, err := entryA.outputSnapshot.SameRoot(entryAAgain.outputSnapshot)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := entryA.outputSnapshot.ExactEqual(entryAAgain.outputSnapshot)
	require.NoError(t, err)
	assert.True(t, equal)

	component := &Component{
		logger:                testutil.NewTestLogger(),
		deployedChecksumByPod: map[podAuthorityKey]string{},
		deployedTrigger:       make(chan struct{}, 1),
	}
	for index, candidate := range []*renderedConfigEntry{entryA, entryB, entryAAgain} {
		component.enqueueDeployed(&publishWorkItem{
			correlationID: fmt.Sprintf("cycle-%d", index),
			entry:         candidate,
		})
	}
	require.Len(t, component.deployedPending, 3)
	assert.Same(t, entryA.outputSnapshot, component.deployedPending[0].entry.outputSnapshot)
	assert.Same(t, entryB.outputSnapshot, component.deployedPending[1].entry.outputSnapshot)
	assert.Same(t, entryAAgain.outputSnapshot, component.deployedPending[2].entry.outputSnapshot)

	component.lastPublishedOutputSnapshot = entryB.outputSnapshot
	component.renderedConfigs = map[string]*renderedConfigEntry{"a-again": entryAAgain}
	assert.False(t, component.skipIfAlreadyPublished(&publishWorkItem{
		correlationID: "a-again",
		entry:         entryAAgain,
	}, "skip"))
}

func TestRenderedConfigEntryRejectsMissingOccurrences(t *testing.T) {
	poison := &renderoutput.Snapshot{}
	_, err := renderedConfigEntryFromEvent(&events.TemplateRenderedEvent{OutputSnapshot: poison})
	require.ErrorContains(t, err, "authenticating render occurrence")
	_, err = renderedConfigEntryFromDeployedRequest(&events.DeployedConfigPublishRequest{OutputSnapshot: poison})
	require.ErrorContains(t, err, "authenticating deployed render occurrence")
}

func TestRenderGateVerdictUsesOccurrencePlanDespitePoisonedShadows(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	event, output := fixture.event, fixture.snapshot
	occurrence, err := event.RenderOccurrence()
	require.NoError(t, err)
	gate, err := events.NewRenderGateCompletedEventWithCycle(
		occurrence, true, false, true, "", false, 1,
	)
	require.NoError(t, err)
	planID, err := output.PlanID()
	require.NoError(t, err)

	held := &renderedConfigEntry{planID: planID, outputSnapshot: output}
	component := New(nil, busevents.NewEventBus(10), testutil.NewTestLogger())
	component.templateConfig = &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	component.hasTemplateConfig = true
	component.gatePinned = true
	component.heldRender = held
	component.heldCorrelationID = "held"

	gate.PlanID = "poisoned-plan"
	gate.OutputSnapshot = controllerPublisherPoisonOutput(t)
	gate.CycleSnapshot = &rendercycle.Snapshot{}
	gate.RenderProof = "poisoned-proof"
	gate.Plan = &renderplan.Plan{}
	component.handleRenderGateCompleted(gate)

	assert.False(t, component.gatePinned)
	assert.Nil(t, component.heldRender)
	assert.Empty(t, component.heldCorrelationID)
	assert.Equal(t, planID, component.publishedPlanID)
	select {
	case work := <-component.publishWork:
		assert.Same(t, held.outputSnapshot, work.entry.outputSnapshot)
		assert.Equal(t, held.planID, work.entry.planID)
		assert.Equal(t, "held", work.correlationID)
	default:
		t.Fatal("authenticated passing verdict did not release held render")
	}
	component.verdictMu.Lock()
	verdict := component.pendingVerdict
	component.verdictMu.Unlock()
	require.NotNil(t, verdict)
	assert.Equal(t, planID, verdict.PlanID)
}

func TestRenderGateVerdictWithoutOccurrenceCannotChangePublicationState(t *testing.T) {
	held := &renderedConfigEntry{planID: "trusted-plan"}
	component := New(nil, busevents.NewEventBus(10), testutil.NewTestLogger())
	component.templateConfig = &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	component.hasTemplateConfig = true
	component.gatePinned = true
	component.heldRender = held
	component.heldCorrelationID = "held"
	component.publishedPlanID = "published-plan"

	component.handleRenderGateCompleted(events.NewRenderGateCompletedEvent(
		"trusted-plan", true, false, true, "", false, 1,
	))

	assert.True(t, component.gatePinned)
	assert.Same(t, held, component.heldRender)
	assert.Equal(t, "held", component.heldCorrelationID)
	assert.Equal(t, "published-plan", component.publishedPlanID)
	assert.Empty(t, component.publishWork)
	component.verdictMu.Lock()
	defer component.verdictMu.Unlock()
	assert.Nil(t, component.pendingVerdict)
}

func controllerPublisherPoisonOutput(tb testing.TB) *renderoutput.Snapshot {
	tb.Helper()
	output, _, _ := controllerPublisherOutputFixture(tb)
	return output
}

func controllerPublisherPoisonArtifacts(tb testing.TB) *renderartifact.Snapshot {
	tb.Helper()
	builder, err := renderartifact.NewBuilder(renderartifact.NewAuthority(), nil)
	require.NoError(tb, err)
	require.NoError(tb, builder.Add(
		renderartifact.Descriptor{Family: renderartifact.Map, Path: "maps/poison.map"},
		renderartifact.NewLiteralContent("poison\n"),
	))
	artifacts, err := builder.Build()
	require.NoError(tb, err)
	return artifacts
}

func TestHandleTemplateRenderedUsesOutputDespitePoisonedShadows(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	event, snapshot, checksum := fixture.event, fixture.snapshot, fixture.checksum
	event.HAProxyConfig = "mixed"
	event.ContentChecksum = "wrong"

	component := New(nil, busevents.NewEventBus(10), testutil.NewTestLogger())
	component.handleTemplateRendered(event)
	require.Len(t, component.renderedConfigs, 1)
	assert.Same(t, snapshot, component.lastRender.outputSnapshot)
	assert.Equal(t, checksum, component.lastRender.contentChecksum)
	assert.Empty(t, component.publishWork)
}

func TestRenderedConfigEntryIgnoresDeployedOutputShadows(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	rendered, snapshot := fixture.event, fixture.snapshot
	config, checksum := fixture.config, fixture.checksum
	occurrence, err := rendered.RenderOccurrence()
	require.NoError(t, err)
	base, err := events.NewDeployedConfigPublishRequestWithCycle(
		"test-haproxycfg", "default", occurrence,
	)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*events.DeployedConfigPublishRequest)
	}{
		{name: "cycle", mutate: func(event *events.DeployedConfigPublishRequest) {
			event.CycleSnapshot = &rendercycle.Snapshot{}
		}},
		{name: "output", mutate: func(event *events.DeployedConfigPublishRequest) {
			event.OutputSnapshot = controllerPublisherPoisonOutput(t)
		}},
		{name: "config", mutate: func(event *events.DeployedConfigPublishRequest) {
			event.Config = "evil"
		}},
		{name: "auxiliary files", mutate: func(event *events.DeployedConfigPublishRequest) {
			event.AuxiliaryFiles = &dataplane.AuxiliaryFiles{}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := *base
			test.mutate(&event)
			event.ContentChecksum = "wrong"
			entry, err := renderedConfigEntryFromDeployedRequest(&event)
			require.NoError(t, err)
			assert.Same(t, snapshot, entry.outputSnapshot)
			assert.Equal(t, config, entry.config)
			assert.Equal(t, checksum, entry.contentChecksum)
		})
	}
	_, err = renderedConfigEntryFromDeployedRequest(nil)
	require.Error(t, err)
}

func TestRenderedConfigEntryRejectsLegacyAuxiliaryCarriersWithoutOccurrence(t *testing.T) {
	_, artifacts, _ := controllerPublisherOutputFixture(t)
	event := events.NewTemplateRenderedEvent(
		"global\n", &dataplane.AuxiliaryFiles{}, nil, nil, 0, 0, "test", "legacy", nil, "", true,
	)
	event.AuxiliaryFileSnapshot = artifacts
	_, err := renderedConfigEntryFromEvent(event)
	require.Error(t, err)
}

func TestOutputSnapshotDedupRequiresSameRoot(t *testing.T) {
	first, _, checksum := controllerPublisherOutputFixture(t)
	second, _, secondChecksum := controllerPublisherOutputFixture(t)
	require.Equal(t, checksum, secondChecksum)

	component := &Component{
		logger:                      testutil.NewTestLogger(),
		renderedConfigs:             map[string]*renderedConfigEntry{},
		lastPublishedChecksum:       checksum,
		lastPublishedOutputSnapshot: first,
	}
	differentRoot := &publishWorkItem{
		correlationID: "different-root",
		entry: &renderedConfigEntry{
			outputSnapshot: second, contentChecksum: secondChecksum,
		},
	}
	component.renderedConfigs[differentRoot.correlationID] = differentRoot.entry
	assert.False(t, component.skipIfAlreadyPublished(differentRoot, "skip"))
	assert.Contains(t, component.renderedConfigs, differentRoot.correlationID)

	sameRoot := &publishWorkItem{
		correlationID: "same-root",
		entry: &renderedConfigEntry{
			outputSnapshot: first, contentChecksum: checksum,
		},
	}
	component.renderedConfigs[sameRoot.correlationID] = sameRoot.entry
	assert.True(t, component.skipIfAlreadyPublished(sameRoot, "skip"))
	assert.NotContains(t, component.renderedConfigs, sameRoot.correlationID)
}

func TestDeployedOutputQueueDedupRequiresSameRoot(t *testing.T) {
	first, _, checksum := controllerPublisherOutputFixture(t)
	second, _, secondChecksum := controllerPublisherOutputFixture(t)
	require.Equal(t, checksum, secondChecksum)
	component := &Component{
		logger:                testutil.NewTestLogger(),
		deployedChecksumByPod: map[podAuthorityKey]string{},
		deployedTrigger:       make(chan struct{}, 1),
	}
	work := func(id string, snapshot *renderoutput.Snapshot) *publishWorkItem {
		return &publishWorkItem{
			correlationID: id,
			entry: &renderedConfigEntry{
				outputSnapshot:  snapshot,
				contentChecksum: checksum,
			},
		}
	}

	component.enqueueDeployed(work("first", first))
	component.enqueueDeployed(work("second", second))
	require.Len(t, component.deployedPending, 2)
	assert.Equal(t, "first", component.deployedPending[0].correlationID)
	assert.Equal(t, "second", component.deployedPending[1].correlationID)

	component.enqueueDeployed(work("first-replaced", first))
	require.Len(t, component.deployedPending, 2)
	assert.Equal(t, "first-replaced", component.deployedPending[0].correlationID)
	assert.Equal(t, "second", component.deployedPending[1].correlationID)
}

func TestLegacyDeployedDedupUsesExactContent(t *testing.T) {
	base := &renderedConfigEntry{
		config:          "global\n",
		contentChecksum: "shared-shadow",
		auxFiles: &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{
			Path: "maps/routes.map", Content: "a backend-a\n",
		}}},
	}
	same := cloneRenderedConfigEntry(base)
	same.contentChecksum = "different-shadow"
	assert.True(t, sameDeployedOutput(base, same))

	changedConfig := cloneRenderedConfigEntry(base)
	changedConfig.config = "defaults\n"
	assert.False(t, sameDeployedOutput(base, changedConfig))

	changedArtifact := cloneRenderedConfigEntry(base)
	changedArtifact.auxFiles.MapFiles[0].Content = "a backend-b\n"
	assert.False(t, sameDeployedOutput(base, changedArtifact))
}

func TestRenderedConfigEntryIgnoresLegacyAuxiliaryMutation(t *testing.T) {
	files := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{
		Path: "maps/routes.map", Content: "safe\n",
	}}}
	event := newControllerPublisherTemplateFixture(t).event
	event.AuxiliaryFiles = files
	entry, err := renderedConfigEntryFromEvent(event)
	require.NoError(t, err)
	files.MapFiles[0].Content = "poisoned\n"
	assert.Nil(t, entry.auxFiles)
	require.NotNil(t, entry.artifactSnapshot)
}

type controllerPublisherTemplateFixture struct {
	event     *events.TemplateRenderedEvent
	snapshot  *renderoutput.Snapshot
	artifacts *renderartifact.Snapshot
	config    string
	checksum  string
}

func newControllerPublisherTemplateFixture(tb testing.TB) controllerPublisherTemplateFixture {
	tb.Helper()
	config := "global\n"
	cycle := testutil.NewRenderCycleFixture(tb).Snapshot(tb, config, nil, nil)
	output, err := cycle.OutputSnapshot()
	require.NoError(tb, err)
	artifacts, err := output.ArtifactSnapshot()
	require.NoError(tb, err)
	checksum, err := cycle.ContentChecksum()
	require.NoError(tb, err)
	event, err := events.NewTemplateRenderedEventWithCycle(cycle, 10, "test", true)
	require.NoError(tb, err)
	return controllerPublisherTemplateFixture{
		event: event, snapshot: output, artifacts: artifacts, config: config, checksum: checksum,
	}
}

func controllerPublisherOutputFixture(
	tb testing.TB,
) (snapshot *renderoutput.Snapshot, artifacts *renderartifact.Snapshot, checksum string) {
	tb.Helper()
	artifactAuthority := renderartifact.NewAuthority()
	builder, err := renderartifact.NewBuilder(artifactAuthority, nil)
	require.NoError(tb, err)
	artifacts, err = builder.Build()
	require.NoError(tb, err)
	config := "global\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Digest: renderplan.DigestString(config), Size: int64(len(config)),
			Content: config, ContentKnown: true,
		}},
	}
	plan.ComputeID()
	authority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
	require.NoError(tb, err)
	snapshot, err = renderoutput.NewSnapshot(authority, config, plan, artifacts, nil)
	require.NoError(tb, err)
	checksum, err = snapshot.ContentChecksum()
	require.NoError(tb, err)
	return snapshot, artifacts, checksum
}

type controllerPublisherCycleFixture struct {
	outputAuthority   *renderoutput.Authority
	cycleAuthority    *rendercycle.Authority
	artifacts         *renderartifact.Snapshot
	statusPatches     *templating.StatusPatchSnapshot
	renderedEvents    *templating.RenderedEventSnapshot
	renderedResources *templating.RenderedResourceSnapshot
}

func newControllerPublisherCycleFixture(tb testing.TB) controllerPublisherCycleFixture {
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
	statusPatches, err := templating.NewStatusPatchCollector().Snapshot()
	require.NoError(tb, err)
	renderedEvents, err := templating.NewEventCollector().Snapshot()
	require.NoError(tb, err)
	renderedResources, err := templating.NewRenderedResourceCollector().Snapshot()
	require.NoError(tb, err)
	return controllerPublisherCycleFixture{
		outputAuthority: outputAuthority, cycleAuthority: cycleAuthority, artifacts: artifacts,
		statusPatches: statusPatches, renderedEvents: renderedEvents, renderedResources: renderedResources,
	}
}

func (f controllerPublisherCycleFixture) output(
	tb testing.TB,
	config string,
	previous *renderoutput.Snapshot,
) *renderoutput.Snapshot {
	tb.Helper()
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Digest: renderplan.DigestString(config), Size: int64(len(config)),
			Content: config, ContentKnown: true,
		}},
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(f.outputAuthority, config, plan, f.artifacts, previous)
	require.NoError(tb, err)
	return output
}

func (f controllerPublisherCycleFixture) cycle(
	tb testing.TB,
	output *renderoutput.Snapshot,
	previous *rendercycle.Snapshot,
) *rendercycle.Snapshot {
	tb.Helper()
	cycle, err := rendercycle.NewSnapshot(
		f.cycleAuthority,
		output,
		f.statusPatches,
		f.renderedEvents,
		f.renderedResources,
		previous,
	)
	require.NoError(tb, err)
	return cycle
}
