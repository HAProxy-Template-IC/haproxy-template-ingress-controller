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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// performPodsDiscovered has THREE early-return guards:
//
//  1. !hasValidConfig          → skip
//  2. endpointCount == 0       → skip
//  3. happy path               → scheduleOrQueue
//
// The existing TestDeploymentScheduler_HandlePodsDiscovered covers (1)
// and (3) but NOT (2). The (2) branch is load-bearing: when the
// scheduler holds a valid config but the discovery event reports zero
// endpoints — which happens during cluster-wide HAProxy churn (rolling
// the entire haproxy DaemonSet, deleting all pods, network partition
// recovery, etc.) — we must NOT call scheduleOrQueue with an empty
// endpoint list. scheduleOrQueue would happily kick off a deployment
// with zero targets, which:
//
//   - publishes a DeploymentScheduledEvent that downstream observers
//     interpret as "we deployed", skewing metrics and dashboards;
//   - races with the next HAProxyPodsDiscoveredEvent (the one that
//     reports the new endpoint set) and wedges the scheduler's
//     in-progress flag, blocking the real deployment.
//
// A regression that flipped the `endpointCount == 0` check (e.g. a
// refactor that consolidated the two early-returns and accidentally
// dropped one) would silently skew metrics on every HAProxy roll and
// is exactly the kind of bug nobody notices in CI but the on-call
// engineer hates.
func TestPerformPodsDiscovered_EmptyEndpointsWithValidConfigSkipsDeployment(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx

	// Set up state: scheduler HAS a validated config, so the first
	// guard (`!hasValidConfig`) does NOT fire. Only the empty-endpoints
	// guard should suppress the deployment.
	scheduler.mu.Lock()
	scheduler.hasValidConfig = true
	scheduler.lastValidatedConfig = "global\n  daemon\n"
	scheduler.lastValidatedAux = &dataplane.AuxiliaryFiles{}
	scheduler.mu.Unlock()

	// Discovery event reports ZERO endpoints — this is the exact
	// signal the empty-guard must catch.
	event := events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{}, 0)

	scheduler.performPodsDiscovered(ctx, event)

	// Endpoint set MUST be updated even when empty — otherwise the
	// scheduler's view of the cluster goes stale and the next valid
	// discovery would race against the previous endpoint set.
	scheduler.mu.RLock()
	if len(scheduler.currentEndpoints) != 0 {
		scheduler.mu.RUnlock()
		t.Fatalf("currentEndpoints must be updated to empty slice, got %d entries",
			len(scheduler.currentEndpoints))
	}
	scheduler.mu.RUnlock()

	// And NO DeploymentScheduledEvent must be published — that would
	// kick off a zero-target deploy that skews metrics and races the
	// real one.
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](
		t, eventChan, testutil.NoEventTimeout)
}
