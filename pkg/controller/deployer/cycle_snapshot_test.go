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

package deployer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func occurrenceForCycle(tb testing.TB, cycle *rendercycle.Snapshot) *rendercycle.Occurrence {
	tb.Helper()
	occurrence, err := rendercycle.NewOccurrence(cycle)
	require.NoError(tb, err)
	return occurrence
}

func templateOccurrenceEvent(
	tb testing.TB,
	occurrence *rendercycle.Occurrence,
) *events.TemplateRenderedEvent {
	tb.Helper()
	event, err := events.NewTemplateRenderedEventWithOccurrence(
		occurrence, 1, "config_change", true,
	)
	require.NoError(tb, err)
	return event
}

func scheduledOccurrenceEvent(
	tb testing.TB,
	occurrence *rendercycle.Occurrence,
	endpoints []dataplane.Endpoint,
) *events.DeploymentScheduledEvent {
	tb.Helper()
	event, err := events.NewDeploymentScheduledEventWithCycle(
		occurrence, endpoints, "runtime", "haptic", "config_validation", true,
	)
	require.NoError(tb, err)
	return event
}

func completedOccurrenceEvent(
	tb testing.TB,
	occurrence *rendercycle.Occurrence,
	deploymentID, podSetHash string,
) *events.DeploymentCompletedEvent {
	tb.Helper()
	result, err := events.NewDeploymentResultWithOccurrence(occurrence)
	require.NoError(tb, err)
	result.DeploymentID = deploymentID
	result.Total = 1
	result.Succeeded = 1
	result.PodSetHash = podSetHash
	event, err := events.NewDeploymentCompletedEventWithCycle(result)
	require.NoError(tb, err)
	return event
}

func gateOccurrenceEvent(
	tb testing.TB,
	occurrence *rendercycle.Occurrence,
	ok, refused, newest bool,
) *events.RenderGateCompletedEvent {
	tb.Helper()
	event, err := events.NewRenderGateCompletedEventWithCycle(
		occurrence, ok, refused, newest, "", !ok, 1,
	)
	require.NoError(tb, err)
	return event
}

func TestSchedulerUsesOccurrenceAndIgnoresPublicIdentityShadows(t *testing.T) {
	fixture := testutil.NewRenderCycleFixture(t)
	cycleA := fixture.Snapshot(t, "global\n# A\n", nil, nil)
	cycleB := fixture.Snapshot(t, "global\n# B\n", nil, cycleA)
	occurrenceA := occurrenceForCycle(t, cycleA)
	event := templateOccurrenceEvent(t, occurrenceA)
	event.CycleSnapshot = cycleB
	event.OutputSnapshot, _ = cycleB.OutputSnapshot()
	event.HAProxyConfig = "poisoned\n"
	event.AuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	event.Plan = exactTestPlan("poisoned", "poisoned\n")
	event.PlanID = "poisoned"
	event.ContentChecksum = "poisoned"
	event.RenderProof = "poisoned"

	bus, logger := testutil.NewTestBusAndLogger()
	scheduler := newDeploymentScheduler(bus, logger, 0, time.Second)
	scheduler.currentEndpoints = oneEndpoint()
	scheduler.handleTemplateRendered(context.Background(), event)

	assert.Same(t, occurrenceA, scheduler.lastRenderedOccurrence)
	assert.Same(t, occurrenceA, scheduler.lastValidatedOccurrence)
	require.NotNil(t, scheduler.state.pending)
	assert.Same(t, occurrenceA, scheduler.state.pending.occurrence)
	scheduled := scheduler.newScheduledEvent(scheduler.state.pending)
	require.NotNil(t, scheduled)
	propagated, err := scheduled.RenderOccurrence()
	require.NoError(t, err)
	assert.Same(t, occurrenceA, propagated)
}

func TestSchedulerCompletionRequiresSameExactOccurrence(t *testing.T) {
	fixture := testutil.NewRenderCycleFixture(t)
	cycleA := fixture.Snapshot(t, "global\n# A\n", nil, nil)
	occurrenceA := occurrenceForCycle(t, cycleA)
	repeatedA := occurrenceForCycle(t, cycleA)
	endpoints := oneEndpoint()
	podSetHash := computePodSetHash(endpoints)
	_, logger := testutil.NewTestBusAndLogger()
	scheduler := newDeploymentScheduler(testutil.NewTestBus(), logger, 0, time.Second)
	initLoopChannels(scheduler)
	scheduler.state.deployInFlight = true
	scheduler.state.activeDeploymentID = "deployment:A"
	scheduler.state.activeOccurrence = occurrenceA

	stale := completedOccurrenceEvent(t, repeatedA, "deployment:A", podSetHash)
	stale.CycleSnapshot = cycleA
	stale.RenderProof = "poisoned"
	scheduler.handleDeploymentCompleted(stale)
	assert.True(t, scheduler.state.deployInFlight)

	exact := completedOccurrenceEvent(t, occurrenceA, "deployment:A", podSetHash)
	exact.CycleSnapshot = nil
	exact.OutputSnapshot = nil
	exact.ContentChecksum = "poisoned"
	scheduler.handleDeploymentCompleted(exact)
	assert.False(t, scheduler.state.deployInFlight)
	assert.Same(t, occurrenceA, scheduler.lastDeployedOccurrence)
}

func TestSchedulerABARenderGateDistinguishesOccurrences(t *testing.T) {
	fixture := testutil.NewRenderCycleFixture(t)
	cycleA := fixture.Snapshot(t, "global\n# A\n", nil, nil)
	cycleB := fixture.Snapshot(t, "global\n# B\n", nil, cycleA)
	cycleA2 := fixture.Snapshot(t, "global\n# A\n", nil, cycleB)
	occurrenceA1 := occurrenceForCycle(t, cycleA)
	occurrenceB := occurrenceForCycle(t, cycleB)
	occurrenceA2 := occurrenceForCycle(t, cycleA2)
	bus, logger := testutil.NewTestBusAndLogger()
	scheduler := newDeploymentScheduler(bus, logger, 0, time.Second)
	scheduler.gatePinned = true

	scheduler.handleTemplateRendered(context.Background(), templateOccurrenceEvent(t, occurrenceA1))
	scheduler.handleTemplateRendered(context.Background(), templateOccurrenceEvent(t, occurrenceB))
	scheduler.handleTemplateRendered(context.Background(), templateOccurrenceEvent(t, occurrenceA2))
	scheduler.handleRenderGateCompleted(
		context.Background(), gateOccurrenceEvent(t, occurrenceA1, true, false, true),
	)
	assert.True(t, scheduler.gatePinned)
	assert.Nil(t, scheduler.lastValidatedOccurrence)

	scheduler.handleRenderGateCompleted(
		context.Background(), gateOccurrenceEvent(t, occurrenceA2, true, false, true),
	)
	assert.False(t, scheduler.gatePinned)
	assert.Same(t, occurrenceA2, scheduler.lastValidatedOccurrence)
}

func TestPlanCachePoisonsReusedAgentProofAcrossOccurrences(t *testing.T) {
	occurrenceA := mustTestOccurrence("global\n# A\n", "plan-A", nil)
	occurrenceB := mustTestOccurrence("global\n# B\n", "plan-B", nil)
	identityA, err := materializeOccurrence(occurrenceA)
	require.NoError(t, err)
	identityB, err := materializeOccurrence(occurrenceB)
	require.NoError(t, err)
	cache := newPlanCache()

	require.NoError(t, cache.BindOccurrence(
		"pod", identityA.planID, "agent:1", identityA.plan, occurrenceA,
	))
	require.Error(t, cache.BindOccurrence(
		"pod", identityB.planID, "agent:1", identityB.plan, occurrenceB,
	))
	poisonedA, err := cache.Occurrence("pod", identityA.planID, "agent:1")
	require.NoError(t, err)
	assert.Nil(t, poisonedA)
	poisonedB, err := cache.Occurrence("pod", identityB.planID, "agent:1")
	require.NoError(t, err)
	assert.Nil(t, poisonedB)
}

func TestAwaitingConvergenceRequiresSameOccurrence(t *testing.T) {
	occurrenceA := mustTestOccurrence("global\n# A\n", "plan-A", nil)
	repeatedA := mustTestOccurrence("global\n# A\n", "plan-A", nil)
	awaiting := &awaitingRender{occurrence: occurrenceA, planID: "plan-A"}
	assert.False(t, awaiting.matches(runningRender{
		plan: exactTestPlan("plan-A", "global\n# A\n"), occurrence: repeatedA,
	}))
	assert.True(t, awaiting.matches(runningRender{
		plan: exactTestPlan("plan-A", "global\n# A\n"), occurrence: occurrenceA,
	}))
}

func TestOccurrenceCarriesStatusDespitePoisonedScheduledShadows(t *testing.T) {
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "gateway.networking.k8s.io/v1", "HTTPRoute",
		map[string]map[string]any{"deployed": {"status": "ready"}},
	))
	status, err := collector.Snapshot()
	require.NoError(t, err)
	occurrence := mustTestOccurrence("global\n", "plan-status", status)
	event := scheduledOccurrenceEvent(t, occurrence, oneEndpoint())
	event.StatusPatchSnapshot, _ = templating.NewStatusPatchCollector().Snapshot()
	event.StatusPatches = nil

	propagated, err := scheduledEventOccurrence(event)
	require.NoError(t, err)
	identity, err := inspectOccurrence(propagated)
	require.NoError(t, err)
	assert.Same(t, status, identity.statusPatches)
}
