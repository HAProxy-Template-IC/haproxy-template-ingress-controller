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
// drift-monitor's periodic recovery wakeups. The contract that matters:
//
//	Reason propagation: the published ReconciliationTriggeredEvent
//	MUST carry events.TriggerReasonDriftPrevention ("drift_prevention").
//	The DeploymentScheduler keys on this exact string to decide
//	whether to deploy cached config when validation fails (the
//	drift-recovery escape hatch). A regression that changed the
//	reason — e.g. "drift" or "" — would silently disable that
//	escape hatch: the scheduler would treat drift wakeups as
//	normal reconciliations and refuse to deploy stale cached
//	config when validation broke after a config change.
//
// This contract describes behaviour the deployer relies on but doesn't
// enforce, so it isn't derivable from observing the function shape —
// pin it explicitly here.

func TestReconciler_HandleDriftPrevention_TriggersImmediateWithDriftPreventionReason(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

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

// There is no reconciler-level debounce anymore: every event fires its own
// trigger immediately. This test pins that drift prevention fires its
// immediate drift trigger even when it directly follows a resource_change —
// the preceding resource_change does NOT defer or swallow the drift trigger,
// and drift prevention's reason is preserved so the deployer's cached-config
// fallback stays armed.
func TestReconciler_HandleDriftPrevention_FiresImmediatelyAfterResourceChange(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	go reconciler.Start(t.Context())
	time.Sleep(testutil.StartupDelay)

	// Step 1: a resource change fires its own immediate trigger.
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created: 1,
	}))
	first := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, first)
	require.Equal(t, "resource_change", first.Reason,
		"sanity: the resource change must produce its own immediate trigger")

	// Step 2: drift prevention fires its own immediate trigger right after,
	// carrying the drift-prevention reason intact.
	bus.Publish(events.NewDriftPreventionTriggeredEvent(30 * time.Minute))

	second := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](
		t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, second)
	assert.Equal(t, events.TriggerReasonDriftPrevention, second.Reason,
		"drift prevention MUST fire immediately with its own reason even when "+
			"it directly follows a resource_change — it is the recovery path "+
			"and the deployer keys on its reason for the cached-config fallback")
}
