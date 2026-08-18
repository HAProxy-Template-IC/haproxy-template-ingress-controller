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
	s.mu.Lock()
	s.lastRenderedConfig = "validated-config"
	s.lastContentChecksum = checksum
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	s.handleValidationCompleted(ctx, events.NewValidationCompletedEvent(nil, 5, "config_change", nil, true,
		seedRenderIdentity(s)))
	sd1 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	require.Equal(t, checksum, sd1.ContentChecksum)

	// Every pod accepted the apply, one of them holds it behind a reload due
	// shortly. Nothing failed.
	start := time.Now()
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 1, Failed: 0,
		PendingReloads: 1, PendingReloadUntil: time.Now().Add(100 * time.Millisecond),
		ContentChecksum: checksum, PodSetHash: "pods-1",
	}))

	s.mu.Lock()
	cachedHash := s.lastDeployedConfigHash
	s.mu.Unlock()
	assert.NotEqual(t, checksum, cachedHash, "a render a pod still holds behind a reload is not deployed")

	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "pending_reload_follow_up", sd2.Reason)
	assert.Equal(t, sd1.ContentChecksum, sd2.ContentChecksum, "the follow-up re-drives the same render")
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "the follow-up waits for the scheduled reload")
	assert.Less(t, elapsed, 5*time.Second, "and does not wait for the drift backstop")

	// The follow-up finds every pod converged: the render is deployed now.
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 2, Failed: 0, ContentChecksum: checksum, PodSetHash: "pods-1",
	}))
	s.mu.Lock()
	cachedHash = s.lastDeployedConfigHash
	s.mu.Unlock()
	assert.Equal(t, checksum, cachedHash)
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 300*time.Millisecond)
}

// While the fleet's paced reloads are pending, new renders are held and the
// newest one is dispatched once they have fired: one converged deployment per
// pacing window instead of a fleet that stays one reload behind.
func TestPendingReload_HoldsNewRendersUntilTheWindowPasses(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("hold-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 20*time.Millisecond, 30*time.Second)
	s.mu.Lock()
	s.lastRenderedConfig = "render-1"
	s.lastContentChecksum = "checksum-1"
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	s.handleValidationCompleted(ctx, events.NewValidationCompletedEvent(nil, 5, "config_change", nil, true,
		seedRenderIdentity(s)))
	sd1 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	require.Equal(t, "checksum-1", sd1.ContentChecksum)

	// The fleet holds render 1 behind a reload due in 300 ms.
	holdStart := time.Now()
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 0, Failed: 0,
		PendingReloads: 2, PendingReloadUntil: holdStart.Add(300 * time.Millisecond),
		ContentChecksum: "checksum-1", PodSetHash: "pods-1",
	}))

	// Two more renders arrive inside the window.
	for _, checksum := range []string{"checksum-2", "checksum-3"} {
		s.mu.Lock()
		s.lastRenderedConfig = "render-" + checksum
		s.lastContentChecksum = checksum
		s.mu.Unlock()
		s.handleValidationCompleted(ctx, events.NewValidationCompletedEvent(nil, 5, "config_change", nil, true,
			seedRenderIdentity(s)))
	}

	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.GreaterOrEqual(t, time.Since(holdStart), 300*time.Millisecond, "nothing dispatches before the reloads fired")
	assert.Equal(t, "checksum-3", sd2.ContentChecksum, "the newest render is what dispatches")
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 400*time.Millisecond)
}
