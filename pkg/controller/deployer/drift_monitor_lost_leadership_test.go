// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// DriftPreventionMonitor.handleLostLeadership is the leader-only
// state cleanup hook. Coverage was 0%; the existing handleEvent
// test routes DeploymentCompletedEvent but skips LostLeadershipEvent.
//
// The contract is critical because DriftPreventionMonitor is a
// leader-only component: when leadership is lost, its drift timer
// MUST stop immediately. Otherwise:
//
//   - The (now-non-leader) replica would keep firing
//     DriftPreventionTriggeredEvents at the configured cadence,
//     publishing them onto the shared EventBus.
//   - The Reconciler subscribes to those events and would trigger
//     reconciliations on a non-leader replica — at best wasted
//     work, at worst polluting deployer state.
//
// Three contracts pinned:
//
//  1. handleLostLeadership stops an active drift timer
//     (timerActive flips from true to false).
//
//  2. handleLostLeadership is safe when no timer is active
//     (rapid leader churn can deliver LostLeadershipEvent before
//     any BecameLeader has started a timer; a panic here would
//     crash the deployer goroutine).
//
//  3. Routed via handleEvent: a *LostLeadershipEvent published to
//     the dispatch table MUST trigger handleLostLeadership. The
//     existing handleEvent test only covered DeploymentCompletedEvent;
//     a regression that removed the case from the type switch
//     would silently leave the drift timer running on the non-leader.

func TestDriftPreventionMonitor_HandleLostLeadership_StopsActiveTimer(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	// Start the timer first so we can verify it gets stopped.
	monitor.resetDriftTimer()
	monitor.mu.Lock()
	require.True(t, monitor.timerActive,
		"baseline: timer must be active before handleLostLeadership for the assertion to be meaningful")
	monitor.mu.Unlock()

	monitor.handleLostLeadership()

	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	assert.False(t, monitor.timerActive,
		"handleLostLeadership MUST stop the drift timer — without this, "+
			"the non-leader replica would keep firing DriftPreventionTriggered "+
			"events on the shared EventBus, causing the Reconciler to do "+
			"work on a node that no longer holds leadership")
}

func TestDriftPreventionMonitor_HandleLostLeadership_NoTimerIsSafe(t *testing.T) {
	// Defensive: handleLostLeadership called when no timer was ever
	// started (e.g. lost leadership before the first BecameLeader,
	// which can happen during rapid transitions). MUST NOT panic.
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)
	monitor.mu.Lock()
	require.False(t, monitor.timerActive,
		"baseline: timer must be inactive for this test to be meaningful")
	require.Nil(t, monitor.driftTimer, "baseline: driftTimer pointer must be nil")
	monitor.mu.Unlock()

	require.NotPanics(t, func() { monitor.handleLostLeadership() },
		"handleLostLeadership MUST be safe when there is no active timer — "+
			"rapid leader churn can deliver LostLeadershipEvent before any "+
			"BecameLeaderEvent has started a timer; a panic here would crash "+
			"the deployer goroutine")

	// State must remain consistent.
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	assert.False(t, monitor.timerActive,
		"timerActive MUST stay false on the no-op path")
}

func TestDriftPreventionMonitor_HandleEvent_RoutesLostLeadership(t *testing.T) {
	// Pin the dispatch-table entry: LostLeadershipEvent MUST be
	// routed to handleLostLeadership. The existing
	// TestDriftPreventionMonitor_HandleEvent only covers the
	// DeploymentCompletedEvent case, so a regression that removed
	// the LostLeadershipEvent case from the type switch would
	// silently leave the drift timer running on a non-leader replica.
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	monitor.resetDriftTimer()
	monitor.mu.Lock()
	require.True(t, monitor.timerActive, "baseline: timer must be active")
	monitor.mu.Unlock()

	monitor.handleEvent(events.NewLostLeadershipEvent("test-pod", "test-reason"))

	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	assert.False(t, monitor.timerActive,
		"handleEvent MUST route *events.LostLeadershipEvent to "+
			"handleLostLeadership — without this dispatch a leadership-loss "+
			"event would silently bypass the timer-stop and the non-leader "+
			"would keep firing DriftPreventionTriggered events forever")
}
