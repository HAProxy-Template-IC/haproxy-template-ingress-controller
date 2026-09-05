// Copyright 2025 Philipp Hossner
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
)

// A pod that holds the render behind a paced reload has accepted the apply
// but does not run it. The agent never calls back, so the scheduler must come
// back by itself at the scheduled time; until then the render is not "deployed"
// for the skip-unchanged gate either.
func TestPendingReload_FollowsUpWhenTheReloadFires(t *testing.T) {
	const checksum = "checksum-of-the-paced-render"

	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("followup-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 20*time.Millisecond, 30*time.Second)
	occurrence := primeRendered(s, "validated-config", checksum, "plan-paced")
	s.mu.Lock()
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	s.dispatchRender(ctx, "corr-paced", true, "config_validation")
	sd1 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	firstOccurrence, err := sd1.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, sameOccurrence(occurrence, firstOccurrence))

	// Every pod accepted the apply, one of them holds it behind a reload due
	// shortly. Nothing failed.
	start := time.Now()
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 1, Failed: 0,
		PendingReloads: 1, PendingReloadUntil: time.Now().Add(100 * time.Millisecond),
		ContentChecksum: checksum, PodSetHash: "pods-1",
	}))

	s.mu.RLock()
	cachedOccurrence := s.lastDeployedOccurrence
	s.mu.RUnlock()
	assert.False(t, sameOccurrence(occurrence, cachedOccurrence))

	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "pending_reload_follow_up", sd2.Reason)
	followUpOccurrence, err := sd2.RenderOccurrence()
	require.NoError(t, err)
	assert.True(t, sameOccurrence(firstOccurrence, followUpOccurrence))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "the follow-up waits for the scheduled reload")
	assert.Less(t, elapsed, 5*time.Second, "and does not wait for the drift backstop")

	// The follow-up finds every pod converged: the render is deployed now.
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 2, Failed: 0, ContentChecksum: checksum, PodSetHash: "pods-1",
	}))
	s.mu.RLock()
	cachedOccurrence = s.lastDeployedOccurrence
	s.mu.RUnlock()
	assert.True(t, sameOccurrence(occurrence, cachedOccurrence))
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 300*time.Millisecond)
}

// While the fleet's paced reloads are pending, a new render is dispatched at
// once: the pods coalesce its files into the pending reload and run its
// in-place subset, so an endpoint change never waits for a reload window.
func TestPendingReload_DispatchesNewRendersAtOnce(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("hold-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 20*time.Millisecond, 30*time.Second)
	occurrence1 := primeRendered(s, "render-1", "checksum-1", "plan-1")
	s.mu.Lock()
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	s.dispatchRender(ctx, "corr-render-1", true, "config_validation")
	sd1 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	carried1, err := sd1.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, sameOccurrence(occurrence1, carried1))

	// The fleet holds render 1 behind a reload due in 1.5 s.
	pendingSince := time.Now()
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 0, Failed: 0,
		PendingReloads: 2, PendingReloadUntil: pendingSince.Add(1500 * time.Millisecond),
		ContentChecksum: "checksum-1", PodSetHash: "pods-1",
	}))

	// A render arriving inside the window goes out immediately.
	occurrence2 := primeRendered(s, "render-checksum-2", "checksum-2", "plan-2")
	s.dispatchRender(ctx, "corr-render-2", true, "config_validation")

	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Less(t, time.Since(pendingSince), 400*time.Millisecond, "a render is not held behind the pending reload")
	carried2, err := sd2.RenderOccurrence()
	require.NoError(t, err)
	assert.True(t, sameOccurrence(occurrence2, carried2))
}
