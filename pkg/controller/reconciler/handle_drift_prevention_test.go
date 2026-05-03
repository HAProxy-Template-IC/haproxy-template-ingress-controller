// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package reconciler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// handleDriftPrevention is the immediate-trigger path for the
// drift-monitor's periodic recovery wakeups. Two contracts matter:
//
//  1. Reason propagation: the published ReconciliationTriggeredEvent
//     MUST carry events.TriggerReasonDriftPrevention ("drift_prevention").
//     The DeploymentScheduler keys on this exact string to decide
//     whether to deploy cached config when validation fails (the
//     drift-recovery escape hatch). A regression that changed the
//     reason — e.g. "drift" or "" — would silently disable that
//     escape hatch: the scheduler would treat drift wakeups as
//     normal reconciliations and refuse to deploy stale cached
//     config when validation broke after a config change.
//
//  2. Debounce cancellation: any pending debounce timer MUST be
//     stopped. Drift prevention is the recovery path; if a regular
//     debounced reconciliation is still pending and fires AFTER the
//     immediate drift trigger, the deployer would receive the wrong
//     reason and skip the cached-config fallback.
//
// Both contracts describe behaviour the deployer relies on but doesn't
// enforce, so they aren't derivable from observing the function shape —
// pin them explicitly here.

func TestReconciler_HandleDriftPrevention_TriggersImmediateWithDriftPreventionReason(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Long debounce interval so a regression that fell through to
	// the debounce path would NOT produce the event within
	// NoEventTimeout — the immediate trigger is the only way the
	// test sees the event in time.
	reconciler := New(bus, logger, &Config{DebounceInterval: testutil.LongTimeout})

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	go reconciler.Start(t.Context())
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewDriftPreventionTriggeredEvent(30 * time.Minute))

	got := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, got,
		"DriftPreventionTriggeredEvent MUST trigger an immediate "+
			"ReconciliationTriggeredEvent — drift prevention is the "+
			"recovery path; if it doesn't fire, the system stays stuck "+
			"in whatever broken state caused drift to begin with")

	assert.Equal(t, events.TriggerReasonDriftPrevention, got.Reason,
		"the trigger reason MUST be exactly TriggerReasonDriftPrevention "+
			"('drift_prevention') — DeploymentScheduler keys on this exact "+
			"string to enable the cached-config fallback when validation "+
			"fails. A regression that changed the reason would silently "+
			"disable drift recovery: the scheduler would treat drift "+
			"wakeups as normal reconciliations and refuse to deploy stale "+
			"cached config when validation broke after a config change")
}

func TestReconciler_HandleDriftPrevention_CancelsPendingDebounce(t *testing.T) {
	// Setup: send a resource change to fire the leading-edge
	// trigger AND start a pending debounce timer (a second
	// resource change during refractory). Then publish the drift
	// prevention event — it MUST fire the immediate drift trigger
	// AND cancel the still-pending debounce so we don't see a
	// trailing "resource_change" / "http_resource_change"
	// reconciliation arrive afterward.
	bus, logger := testutil.NewTestBusAndLogger()

	// Short interval so the test doesn't take forever, but long
	// enough to keep the pending timer alive past the immediate
	// trigger we're testing.
	reconciler := New(bus, logger, &Config{DebounceInterval: 300 * time.Millisecond})

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	go reconciler.Start(t.Context())
	time.Sleep(testutil.StartupDelay)

	// Step 1: leading-edge fire.
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created: 1,
	}))
	first := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, first)
	require.Equal(t, "resource_change", first.Reason,
		"sanity: the first resource change must produce the leading-edge "+
			"trigger so the second change schedules a pending debounce")

	// Step 2: second change during refractory — schedules pending timer.
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified: 1,
	}))

	// Step 3: drift prevention — must fire immediately AND cancel the
	// pending timer.
	bus.Publish(events.NewDriftPreventionTriggeredEvent(30 * time.Minute))

	second := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, second)
	assert.Equal(t, events.TriggerReasonDriftPrevention, second.Reason,
		"drift prevention MUST fire immediately even when a debounce "+
			"timer is pending — it is the recovery path and cannot wait")

	// The pending debounce MUST have been cancelled. If a trailing
	// "resource_change" reconciliation arrives after the drift
	// trigger, the deployer would receive the wrong reason and skip
	// the cached-config fallback that drift recovery depends on.
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.NoEventTimeout)
}
