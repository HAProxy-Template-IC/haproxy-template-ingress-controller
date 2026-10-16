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

	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
)

// A graceful stop right after a paced-reload deployment must still observe
// that reload, so the worker receipt for the plan the intent names exists,
// while no newer plan goes out.
func TestSchedulerDrainFollowsUpThePendingReloadAndDispatchesNoNewerPlan(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("drain-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 20*time.Millisecond, 30*time.Second)
	s.drainProgress = make(chan struct{}, 1)
	intended := primeRendered(s, "render-1", "checksum-1", "plan-1")
	s.mu.Lock()
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	s.dispatchRender(ctx, "corr-render-1", true, "config_validation")
	testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 1, PendingReloads: 1, PendingReloadUntil: time.Now().Add(200 * time.Millisecond),
		ContentChecksum: "checksum-1", PodSetHash: "pods-1",
	}))

	drained := make(chan error, 1)
	go func() { drained <- s.Drain(ctx) }()
	require.Eventually(t, func() bool {
		s.schedulerMutex.Lock()
		defer s.schedulerMutex.Unlock()
		return s.draining
	}, time.Second, time.Millisecond)

	primeRendered(s, "render-2", "checksum-2", "plan-2")
	s.dispatchRender(ctx, "corr-render-2", true, "config_validation")

	followUp := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	require.Equal(t, pendingReloadFollowUpReason, followUp.Reason)
	carried, err := followUp.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, samePlan(intended, carried), "the follow-up must observe the intended plan")
	select {
	case err := <-drained:
		t.Fatalf("Drain returned %v before the follow-up completed", err)
	default:
	}

	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 1, Succeeded: 1, ContentChecksum: "checksum-1", PodSetHash: "pods-1",
	}))
	require.NoError(t, <-drained)
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 300*time.Millisecond)
}

// A deployment that timed out never caches its completion, yet the deployer
// recorded its plan as the intent: the drain must admit that plan, not the
// older one that last completed.
func TestSchedulerDrainAdmitsTheLastDispatchedPlanEvenIfItTimedOut(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("drain-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 20*time.Millisecond, 30*time.Second)
	s.drainProgress = make(chan struct{}, 1)
	older := primeRendered(s, "render-1", "checksum-1", "plan-1")
	s.mu.Lock()
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	s.dispatchRender(ctx, "corr-render-1", true, "config_validation")
	testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 1, Succeeded: 1, ContentChecksum: "checksum-1", PodSetHash: "pods-1",
	}))

	intended := primeRendered(s, "render-2", "checksum-2", "plan-2")
	s.dispatchRender(ctx, "corr-render-2", true, "config_validation")
	testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	s.schedulerMutex.Lock()
	s.state.deploymentTimedOut = true
	s.schedulerMutex.Unlock()
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 1, Failed: 1, ContentChecksum: "checksum-2", PodSetHash: "pods-1",
	}))

	require.NoError(t, s.Drain(ctx))
	s.scheduleOrQueueOccurrence(ctx, older, oneEndpoint(), "validation_fallback", "corr-older", false)
	s.scheduleOrQueueOccurrence(ctx, intended, oneEndpoint(), "validation_fallback", "corr-intended", false)

	redispatched := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	carried, err := redispatched.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, samePlan(intended, carried), "the drain must admit only the plan the intent names")
}

func TestSchedulerDrainOfAnEndedTermLeavesTheSchedulerUndrained(t *testing.T) {
	s := newDeploymentScheduler(testutil.NewTestBus(), testutil.NewTestLogger(), 20*time.Millisecond, 30*time.Second)
	s.drainProgress = make(chan struct{}, 1)
	ended, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, s.Drain(ended), context.Canceled)
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	require.False(t, s.draining)
}
