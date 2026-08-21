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

package deployer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// gateLatchScheduler is a scheduler with endpoints and a running deploy loop,
// ready to be driven by renders and gate verdicts.
func gateLatchScheduler(t *testing.T) (*DeploymentScheduler, <-chan busevents.Event, context.Context) {
	t.Helper()
	bus := testutil.NewTestBus()
	scheduled := bus.SubscribeTypes("gate-latch-test", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startLoopForTest(t, scheduler, ctx)

	scheduler.mu.Lock()
	scheduler.currentEndpoints = []dataplane.Endpoint{{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}}
	scheduler.mu.Unlock()

	return scheduler, scheduled, ctx
}

func renderEvent(planID string) *events.TemplateRenderedEvent {
	return events.NewTemplateRenderedEvent(
		"config-"+planID, &dataplane.AuxiliaryFiles{}, nil, nil, 0, 1, "config_change",
		"checksum-"+planID, &renderplan.Plan{ID: planID}, planID, true,
		events.WithCorrelation("corr-"+planID, "cause-"+planID),
	)
}

// While the gate holds renders, the scheduler must not dispatch: the fleet
// keeps serving the last configuration HAProxy accepted.
func TestScheduler_HoldsRendersWhileTheGateIsPinned(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")

	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", false, true, true, "boom", false, 5))
	scheduler.handleEvent(ctx, renderEvent("plan-2"))

	requireNothingScheduled(t, scheduled)
}

// The pass that names the held render releases it, and only it: a verdict for a
// plan the scheduler has already superseded must not dispatch the newer,
// unchecked render.
func TestScheduler_GatePassReleasesOnlyTheRenderItNames(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")
	// Free the deploy loop so a later dispatch is not waiting on this one.
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", false, true, true, "boom", false, 5))

	// Two renders arrive while pinned; the verdict names the older one.
	scheduler.handleEvent(ctx, renderEvent("plan-2"))
	scheduler.handleEvent(ctx, renderEvent("plan-3"))
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-2", true, false, true, "", false, 5))
	requireNothingScheduled(t, scheduled)

	// The verdict for the newest render is what dispatches it.
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-3", true, false, true, "", false, 5))
	requireScheduled(t, scheduled, "plan-3")
}

// Leadership loss resets the latch, so a new term starts optimistic.
func TestScheduler_LostLeadershipClearsThePin(t *testing.T) {
	scheduler, _, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", false, true, true, "boom", false, 5))
	scheduler.mu.RLock()
	require.True(t, scheduler.gatePinned)
	scheduler.mu.RUnlock()

	scheduler.handleLostLeadership(events.NewLostLeadershipEvent("pod", "test"))

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	assert.False(t, scheduler.gatePinned,
		"the agents' own last-known-good set protects the fleet, so a new leader starts optimistic")
}

// The paths that re-send "the config the fleet runs" — pod discovery, the
// validation fallback, the retry timers — must not re-send a render the gate
// refused. They read the last dispatched render, and under the optimistic gate
// that render has not been judged yet, so a refusal has to take it back.
//
// Without this, a pod joining the fleet (or any deploy retry) during the pinned
// window re-applies the refused plan to pods the deployer is concurrently
// reverting away from it.
func TestScheduler_PodDiscoveryWhilePinnedSendsTheAcceptedRender(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", true, false, true, "", false, 5))
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	// plan-2 goes out optimistically and is then refused.
	scheduler.handleEvent(ctx, renderEvent("plan-2"))
	requireScheduled(t, scheduled, "plan-2")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-2", false, true, true, "boom", false, 5))

	// A pod joins. It must be given the render HAProxy accepted, not the one it
	// refused.
	scheduler.handleEvent(ctx, events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"},
		{URL: "http://10.0.0.2:5555", PodName: "haproxy-1"},
	}, 2))

	requireScheduled(t, scheduled, "plan-1")
}

// A refusal that names a plan the scheduler has already superseded says nothing
// about the newer one, so it must not drag the fleet back a render.
func TestScheduler_RefusalOfASupersededPlanKeepsTheNewerRender(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", true, false, true, "", false, 5))
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	scheduler.handleEvent(ctx, renderEvent("plan-2"))
	requireScheduled(t, scheduled, "plan-2")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	// A late verdict for plan-1, which plan-2 has superseded.
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", false, true, true, "boom", false, 5))

	scheduler.mu.RLock()
	deployable := scheduler.lastValidatedPlanID
	scheduler.mu.RUnlock()
	assert.Equal(t, "plan-2", deployable,
		"a verdict for a superseded plan must not roll the fleet back off the newer render")
}

// A gate that could not run carries no HAProxy verdict, so it holds the next
// render without taking the live one away: the pods keep it, and every path
// that re-sends "the config the fleet runs" must keep sending it too.
func TestScheduler_GateThatCouldNotRunKeepsTheDeployableRender(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", true, false, true, "", false, 5))
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	scheduler.handleEvent(ctx, renderEvent("plan-2"))
	requireScheduled(t, scheduled, "plan-2")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent(
		"plan-2", false, false, true, "creating temp directory: read-only file system", false, 5))

	scheduler.mu.RLock()
	deployable := scheduler.lastValidatedPlanID
	pinned := scheduler.gatePinned
	scheduler.mu.RUnlock()
	assert.Equal(t, "plan-2", deployable,
		"without HAProxy's verdict there is no evidence against the render the fleet runs")
	assert.True(t, pinned, "nothing judged the render, so the gate still holds")

	// A pod joining is given that same render, not the one before it.
	scheduler.handleEvent(ctx, events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"},
		{URL: "http://10.0.0.2:5555", PodName: "haproxy-1"},
	}, 2))
	requireScheduled(t, scheduled, "plan-2")
}

// A refusal has to reach the deployment already queued behind the one in
// flight, or the deploy loop publishes the refused render right after the
// scoped revert has taken it off the pods.
func TestScheduler_RefusalDropsTheDeploymentQueuedForThatPlan(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")

	// plan-1's deployment is still in flight, so plan-2 queues behind it.
	scheduler.handleEvent(ctx, renderEvent("plan-2"))
	requireNothingScheduled(t, scheduled)

	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-2", false, true, true, "boom", false, 5))

	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))
	requireNothingScheduled(t, scheduled)
}

// The render a pass released is what the fleet runs, so it is what the next
// refusal rolls back to — not the render accepted before the incident.
func TestScheduler_ReleasedRenderIsTheRollbackTarget(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-1", false, true, true, "boom", false, 5))

	// plan-2 is held, then released by the pass that names it.
	scheduler.handleEvent(ctx, renderEvent("plan-2"))
	requireNothingScheduled(t, scheduled)
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-2", true, false, true, "", false, 5))
	requireScheduled(t, scheduled, "plan-2")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	// plan-3 goes out optimistically and is refused.
	scheduler.handleEvent(ctx, renderEvent("plan-3"))
	requireScheduled(t, scheduled, "plan-3")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))
	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-3", false, true, true, "boom", false, 5))

	scheduler.mu.RLock()
	deployable := scheduler.lastValidatedPlanID
	scheduler.mu.RUnlock()
	assert.Equal(t, "plan-2", deployable,
		"the rollback lands on the released render, not on the one before the first refusal")
}

// A verdict for a plan the fleet has moved past is the gate answering for a
// straggler pod; it must leave the scheduler's latch alone.
func TestScheduler_SupersededVerdictDoesNotMoveTheLatch(t *testing.T) {
	scheduler, scheduled, ctx := gateLatchScheduler(t)

	scheduler.handleEvent(ctx, renderEvent("plan-1"))
	requireScheduled(t, scheduled, "plan-1")
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler,
		&events.DeploymentResult{Total: 1, Succeeded: 1}))

	scheduler.handleEvent(ctx, events.NewRenderGateCompletedEvent("plan-0", false, true, false, "boom", false, 5))

	scheduler.mu.RLock()
	pinned := scheduler.gatePinned
	deployable := scheduler.lastValidatedPlanID
	scheduler.mu.RUnlock()
	assert.False(t, pinned, "a straggler's refusal must not close the gate on the current render")
	assert.Equal(t, "plan-1", deployable)
}

func requireScheduled(t *testing.T, scheduled <-chan busevents.Event, planID string) {
	t.Helper()
	select {
	case event := <-scheduled:
		deployment, ok := event.(*events.DeploymentScheduledEvent)
		require.True(t, ok)
		require.Equal(t, planID, deployment.PlanID)
	case <-time.After(testutil.LongTimeout):
		t.Fatalf("expected %s to be dispatched", planID)
	}
}

func requireNothingScheduled(t *testing.T, scheduled <-chan busevents.Event) {
	t.Helper()
	select {
	case event := <-scheduled:
		t.Fatalf("a held render must not be dispatched, got %#v", event)
	case <-time.After(testutil.NoEventTimeout):
	}
}
