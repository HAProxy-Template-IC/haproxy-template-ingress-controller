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
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// fullFailure builds a DeploymentCompletedEvent for a fully-failed deploy of the
// given content checksum: both HAProxy pods failed (Total=2, Succeeded=0), the
// case that arms the fast retry.
func fullFailure(checksum string) *events.DeploymentCompletedEvent {
	return events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total:           2,
		Succeeded:       0,
		Failed:          2,
		ContentChecksum: checksum,
	})
}

// newFailureRetryScheduler builds a running scheduler (deploy loop started) with
// a DeploymentScheduledEvent subscription and the last-validated cache primed so
// the fast-retry timer's rescheduleLastValidated has a render + endpoints to
// re-dispatch. parsedConfig is left nil, so every (re)dispatch classifies
// structural and publishes a DeploymentScheduledEvent (the runtime-raw lane
// never publishes one — that would make the events unobservable).
func newFailureRetryScheduler(t *testing.T, minInterval time.Duration, checksum string) (
	s *DeploymentScheduler,
	scheduledCh <-chan busevents.Event,
	cancel context.CancelFunc,
) {
	t.Helper()
	bus := testutil.NewTestBus()
	scheduledCh = bus.SubscribeTypes("retry-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s = NewDeploymentScheduler(bus, testutil.NewTestLogger(), minInterval, 30*time.Second)

	// Prime the last-validated cache directly (no ValidationCompleted dispatch),
	// so the FIRST published DeploymentScheduledEvent is a fast retry.
	s.mu.Lock()
	s.lastValidatedConfig = "validated-config"
	s.lastValidatedContentChecksum = checksum
	s.hasValidConfig = true
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()

	ctx, c := context.WithCancel(context.Background())
	startLoopForTest(t, s, ctx)

	return s, scheduledCh, c
}

// TestDeployFailureRetry_ReschedulesAfterFullFailure is the repro. A retryable
// per-pod deploy failure (e.g. a transient DPA transaction-version conflict) is
// otherwise only re-driven by the 60s DriftPreventionMonitor backstop, so the
// first retry of a transiently-failed deploy can wait up to a full minute — and a
// Gateway can sit Programmed!=True for that long.
//
// The test feeds one ValidationCompleted (exactly one DeploymentScheduledEvent),
// then a FULLY-failed DeploymentCompleted, and keeps the cluster quiescent (no
// further events). PRE-FIX no second dispatch arrives (the stall). POST-FIX the
// fast-retry timer re-dispatches the SAME last-validated render within the first
// backoff window.
func TestDeployFailureRetry_ReschedulesAfterFullFailure(t *testing.T) {
	const checksum = "checksum-of-the-failing-render"

	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("retry-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	// Small interval → short first backoff (base<<0 == interval) so the fast
	// retry lands in tens of ms; still far under the 60s drift backstop the
	// pre-fix code relies on.
	const interval = 20 * time.Millisecond
	s := NewDeploymentScheduler(bus, testutil.NewTestLogger(), interval, 30*time.Second)

	// Prime the render cache + endpoints so ValidationCompleted schedules a deploy.
	s.mu.Lock()
	s.lastRenderedConfig = "validated-config"
	s.lastContentChecksum = checksum
	s.currentEndpoints = oneEndpoint()
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, s, ctx)

	// One ValidationCompleted → exactly one DeploymentScheduledEvent.
	s.handleValidationCompleted(ctx, events.NewValidationCompletedEvent(nil, 5, "config_change", nil, true,
		seedRenderIdentity(s)))
	sd1 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	require.Equal(t, "config_validation", sd1.Reason)
	require.Equal(t, checksum, sd1.ContentChecksum)
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)

	// The deploy comes back FULLY failed; the cluster stays quiescent (we publish
	// nothing else). Pre-fix only the 60s drift backstop re-drives this, so no
	// second dispatch arrives.
	start := time.Now()
	s.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 2, Succeeded: 0, Failed: 2, ContentChecksum: checksum,
	}))

	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "deploy_failure_retry", sd2.Reason,
		"a fully-failed deploy must self-reschedule via the fast-retry path")
	assert.Equal(t, sd1.ContentChecksum, sd2.ContentChecksum,
		"the fast retry re-dispatches the SAME last-validated render")
	assert.Less(t, time.Since(start), 500*time.Millisecond,
		"the retry must fire on the fast backoff (tens of ms), not the 60s drift backstop")
}

// TestDeployFailureRetry_StopsAfterMaxRetries pins the cap: repeated identical
// failing completions reschedule at most maxDeployFailureRetries times, then hand
// off to the 60s drift backstop — a permanently-wedged deploy must not spin.
func TestDeployFailureRetry_StopsAfterMaxRetries(t *testing.T) {
	const checksum = "wedged-render"
	const interval = 5 * time.Millisecond
	s, scheduledCh, cancel := newFailureRetryScheduler(t, interval, checksum)
	defer cancel()

	// Each identical failing completion triggers exactly one reschedule, up to the
	// cap. Pace on the observable reschedule event so the test is
	// scheduling-independent.
	for i := 1; i <= maxDeployFailureRetries; i++ {
		s.handleDeploymentCompleted(fullFailure(checksum))
		sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
		require.Equalf(t, "deploy_failure_retry", sd.Reason, "reschedule %d", i)
	}

	// The budget for this checksum is now spent: a further identical failure must
	// NOT reschedule (no hot loop) — the drift backstop takes over.
	s.handleDeploymentCompleted(fullFailure(checksum))
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 200*time.Millisecond)
}

// TestDeployFailureRetry_NewChecksumResetsBudget pins that a NEW render (a
// different ContentChecksum) earns a fresh fast-retry budget after an earlier
// render's budget was exhausted — self-heal resumes for the new content.
func TestDeployFailureRetry_NewChecksumResetsBudget(t *testing.T) {
	const checksumA = "wedged-render-A"
	const interval = 5 * time.Millisecond
	s, scheduledCh, cancel := newFailureRetryScheduler(t, interval, checksumA)
	defer cancel()

	// Exhaust the budget for checksum A.
	for i := 1; i <= maxDeployFailureRetries; i++ {
		s.handleDeploymentCompleted(fullFailure(checksumA))
		testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	}
	// Confirm exhaustion: a further A failure does not reschedule.
	s.handleDeploymentCompleted(fullFailure(checksumA))
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 200*time.Millisecond)

	// A NEW render (different checksum) resets the budget → reschedules resume.
	s.handleDeploymentCompleted(fullFailure("fresh-render-B"))
	sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "deploy_failure_retry", sd.Reason,
		"a new render's checksum must reset the fast-retry budget and resume rescheduling")
}

// TestDeployFailureRetry_SuccessCancelsPendingRetry pins that a fully-successful
// completion cancels an armed fast-retry timer — a stale retry from an earlier
// failure must not fire once the config has converged.
func TestDeployFailureRetry_SuccessCancelsPendingRetry(t *testing.T) {
	const checksum = "transiently-failing-render"
	// Larger interval → larger first backoff (100ms), giving a window to cancel
	// before the timer fires.
	const interval = 100 * time.Millisecond
	s, scheduledCh, cancel := newFailureRetryScheduler(t, interval, checksum)
	defer cancel()

	// A failing completion arms the retry timer (backoff == interval == 100ms).
	s.handleDeploymentCompleted(fullFailure(checksum))

	// Before it fires, a fully-successful completion must cancel it.
	s.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 2, Succeeded: 2, Failed: 0, ContentChecksum: checksum,
	}))

	// The cancelled timer must never re-dispatch (wait comfortably past the 100ms
	// backoff it would otherwise have fired at).
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 400*time.Millisecond)
}

// TestDeployFailureRetry_PartialFailureAlsoReschedules pins that a PARTIAL failure
// (one pod took the config, one didn't — Succeeded>0 && Failed>0) also arms the
// fast retry, so the un-converged pod is re-driven. The retry gates on Failed>0,
// independent of Succeeded; statusapplier separately keeps the "deployed" status
// for the succeeded instance (see statusapplier's partial-success test).
func TestDeployFailureRetry_PartialFailureAlsoReschedules(t *testing.T) {
	const checksum = "partially-failing-render"
	const interval = 5 * time.Millisecond
	s, scheduledCh, cancel := newFailureRetryScheduler(t, interval, checksum)
	defer cancel()

	s.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 2, Succeeded: 1, Failed: 1, ContentChecksum: checksum,
	}))

	sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "deploy_failure_retry", sd.Reason,
		"a partial failure (Failed>0) must arm the fast retry to re-drive the un-converged pods")
}
