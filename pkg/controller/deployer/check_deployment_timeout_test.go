// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

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

// checkDeploymentTimeout is the safety net that recovers from stuck
// deployments after leadership transitions where the
// DeploymentCompletedEvent may never arrive. The
// "timeout only fires when deployInFlight" test (in scheduler_test.go)
// covers two cases (not-in-flight skip + in-flight expired). These
// tests pin the load-bearing observable side effects that test
// doesn't check:
//
//  1. zero startTime → no fire (defensive — never seen in production
//     but a regression that fired here would publish spurious cancels
//     immediately on every check during the brief window between
//     deployInFlight being set and deploymentStartTime being
//     populated).
//
//  2. elapsed <= timeout → no fire (the normal-progress branch).
//     A regression that flipped the comparison would cancel every
//     in-progress deployment on the first check.
//
//  3. expired → publishes DeploymentCancelRequestEvent with the
//     active correlation ID propagated. The deployer matches on
//     correlation ID to stop the right deployment; a regression
//     that dropped the correlation propagation would either silently
//     fail to cancel or cancel an unrelated deployment.
//
//  4. expired → publishes ReconciliationTriggeredEvent with
//     reason="deployment_timeout_recovery" so the system recovers.
//     Without this, the system would be stuck idle until the next
//     external trigger.
//
//  5. expired with empty activeCorrelationID → MUST NOT publish
//     a cancel event (event would have empty correlation ID, which
//     the deployer can't match). A regression that always published
//     would corrupt deployer state.

func TestCheckDeploymentTimeout_NoFireWhenStartTimeIsZero(t *testing.T) {
	// Defensive contract: deployInFlight=true but startTime is the
	// zero value. This shouldn't happen in production but the code
	// has an explicit guard. A regression that removed it would fire
	// the timeout immediately on every tick in the brief window
	// between deployInFlight being set and start-time assignment.
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 1*time.Millisecond)
	initLoopChannels(scheduler)

	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.deploymentStartTime = time.Time{} // ← zero, the guard
	scheduler.state.activeCorrelationID = "should-not-be-touched"
	scheduler.schedulerMutex.Unlock()

	scheduler.checkDeploymentTimeout(context.Background())

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	assert.True(t, scheduler.state.deployInFlight,
		"deployInFlight MUST remain true when startTime is zero — "+
			"a regression that removed the IsZero() guard would fire the "+
			"timeout immediately during the brief setup window")
	assert.Equal(t, "should-not-be-touched", scheduler.state.activeCorrelationID,
		"activeCorrelationID MUST be preserved when timeout doesn't fire")
}

func TestCheckDeploymentTimeout_NoFireWhenElapsedWithinTimeout(t *testing.T) {
	// Normal-progress branch: deployInFlight=true, startTime recent.
	// The timeout MUST NOT fire — that's the whole point of having a
	// timeout instead of a hard deadline.
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	initLoopChannels(scheduler)

	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.deploymentStartTime = time.Now() // just started
	scheduler.state.activeCorrelationID = "in-progress"
	scheduler.schedulerMutex.Unlock()

	scheduler.checkDeploymentTimeout(context.Background())

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	assert.True(t, scheduler.state.deployInFlight,
		"deployInFlight MUST remain true when elapsed <= timeout — "+
			"a regression that flipped the comparison would cancel every "+
			"in-progress deployment on the first check")
	assert.Equal(t, "in-progress", scheduler.state.activeCorrelationID)
}

func TestCheckDeploymentTimeout_PublishesCancelEventWithActiveCorrelationID(t *testing.T) {
	// Pin the observable side effect: when timeout fires with a
	// non-empty correlation ID, a DeploymentCancelRequestEvent MUST be
	// published with the SAME correlation ID. The deployer matches on
	// correlation ID to stop the right in-flight deployment.
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("cancel-event-watcher", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 1*time.Millisecond)
	initLoopChannels(scheduler)

	const activeCorrID = "in-flight-deployment-corr-1"
	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.deploymentStartTime = time.Now().Add(-10 * time.Second) // long expired
	scheduler.state.activeCorrelationID = activeCorrID
	scheduler.schedulerMutex.Unlock()

	scheduler.checkDeploymentTimeout(context.Background())

	// Drain events looking for the cancel + reconciliation trigger.
	cancel := testutil.WaitForEvent[*events.DeploymentCancelRequestEvent](
		t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "deployment_timeout", cancel.Reason,
		"the cancel reason MUST be 'deployment_timeout' so commentator/"+
			"metrics can attribute the recovery to the timeout safety net")
	assert.Equal(t, activeCorrID, cancel.CorrelationID(),
		"DeploymentCancelRequestEvent MUST carry the ACTIVE correlation ID — "+
			"the deployer matches on correlation to stop the right in-flight "+
			"deployment; a regression that dropped this would either silently "+
			"fail to cancel or cancel an unrelated deployment")

	// And the recovery-trigger ReconciliationTriggeredEvent.
	trigger := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "deployment_timeout_recovery", trigger.Reason,
		"after timeout recovery, a fresh ReconciliationTriggeredEvent MUST "+
			"be published with reason 'deployment_timeout_recovery' so the "+
			"system actually recovers — without this trigger the scheduler "+
			"sits idle until the next external event")
	assert.False(t, trigger.Coalescible(),
		"recovery trigger MUST NOT be coalescible — coalescing it would "+
			"defeat the recovery (the trigger could be merged into an "+
			"already-pending deployment that's also stuck)")

	// State should be reset (no longer in flight).
	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	assert.False(t, scheduler.state.deployInFlight,
		"after timeout recovery, deployInFlight MUST be cleared")
	assert.True(t, scheduler.state.deploymentStartTime.IsZero(),
		"deploymentStartTime MUST be reset so the next deployment "+
			"starts fresh")
	assert.Empty(t, scheduler.state.activeCorrelationID,
		"activeCorrelationID MUST be cleared so a future stale completion "+
			"event from the cancelled deployment doesn't get matched")
	assert.False(t, scheduler.state.lastDeploymentEndTime.IsZero(),
		"the timeout counts as a deploy-end so the loop rate-limits the next deploy from here")
}

func TestCheckDeploymentTimeout_DoesNotPublishCancelWithEmptyCorrelationID(t *testing.T) {
	// Edge case: timeout fires but activeCorrelationID is empty (e.g.
	// the deployment scheduler was forced in-flight without populating
	// the correlation ID — defensive). The cancel event MUST NOT be
	// published in this case because it would carry an empty correlation
	// ID, which the deployer can't match against any active deployment,
	// leading to corrupted deployer state.
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("cancel-empty-corr-watcher", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 1*time.Millisecond)
	initLoopChannels(scheduler)

	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.deploymentStartTime = time.Now().Add(-10 * time.Second)
	scheduler.state.activeCorrelationID = "" // ← empty, the guard
	scheduler.schedulerMutex.Unlock()

	scheduler.checkDeploymentTimeout(context.Background())

	// Drain events for a short window and classify each type. The
	// invariants are:
	//   - DeploymentCancelRequestEvent MUST NOT appear (the guard).
	//   - ReconciliationTriggeredEvent MUST appear (recovery still
	//     happens, regardless of the cancel guard).
	// Using AssertNoEvent + a separate read would consume the trigger
	// event, so do a single classifier pass.
	deadline := time.After(testutil.LongTimeout)
	var sawTrigger bool
loop:
	for {
		select {
		case ev := <-eventChan:
			if _, ok := ev.(*events.DeploymentCancelRequestEvent); ok {
				t.Fatalf("DeploymentCancelRequestEvent published with empty " +
					"correlation ID — the empty-corr guard must suppress it; " +
					"a regression that always published would corrupt deployer " +
					"state via an unmatchable cancel")
			}
			if _, ok := ev.(*events.ReconciliationTriggeredEvent); ok {
				sawTrigger = true
				// Continue draining briefly to make sure no
				// cancel sneaks in after the trigger.
				continue
			}
		case <-deadline:
			break loop
		}
	}
	require.True(t, sawTrigger,
		"recovery trigger MUST still be published even when correlation ID "+
			"is empty — the system needs to recover regardless")
}
