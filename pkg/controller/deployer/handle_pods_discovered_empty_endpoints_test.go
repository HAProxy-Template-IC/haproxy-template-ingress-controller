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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	var closes atomic.Int32
	scheduler.runtimeBypass.newSyncer = func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{
			closes: &closes,
			sync:   func() (*dataplane.SyncResult, error) { return &dataplane.SyncResult{Success: true}, nil },
		}, nil
	}
	ctx := context.Background()
	scheduler.ctx = ctx
	oldEndpoints := []dataplane.Endpoint{{URL: "http://old", PodUID: "uid-old"}}
	scheduler.runtimeBypass.applyRuntimeRaw(ctx, depFor(oldEndpoints), bypassPush{body: "config"})
	scheduler.schedulerMutex.Lock()
	scheduler.state.pending = depFor(oldEndpoints)
	scheduler.lastDispatchedConfig = "old-config"
	scheduler.lastDispatchedPodSetHash = computePodSetHash(oldEndpoints)
	scheduler.lastActivatedConfig = "old-config"
	scheduler.schedulerMutex.Unlock()

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
	if got := closes.Load(); got != 1 {
		t.Fatalf("empty fleet must close the retired runtime client, got %d closes", got)
	}
	scheduler.schedulerMutex.Lock()
	if scheduler.state.pending != nil || scheduler.lastDispatchedConfig != "" || scheduler.lastActivatedConfig != "" {
		scheduler.schedulerMutex.Unlock()
		t.Fatal("empty fleet must retire pending work and endpoint-bound baselines")
	}
	scheduler.schedulerMutex.Unlock()

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

func TestPerformPodsDiscovered_StructuralReplacementEvictsRuntimeClient(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()

	var closes atomic.Int32
	scheduler.runtimeBypass.newSyncer = func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{
			closes: &closes,
			sync:   func() (*dataplane.SyncResult, error) { return &dataplane.SyncResult{Success: true}, nil },
		}, nil
	}
	oldEndpoint := dataplane.Endpoint{URL: "http://same", PodName: "haproxy-0", PodNamespace: "haptic", PodUID: "uid-old"}
	scheduler.runtimeBypass.applyRuntimeRaw(ctx, depFor([]dataplane.Endpoint{oldEndpoint}), bypassPush{body: "config"})

	scheduler.mu.Lock()
	scheduler.hasValidConfig = true
	scheduler.lastValidatedConfig = "global\n  daemon\n"
	scheduler.lastValidatedAux = &dataplane.AuxiliaryFiles{}
	scheduler.mu.Unlock()
	replacement := oldEndpoint
	replacement.PodUID = "uid-new"
	scheduler.performPodsDiscovered(ctx, events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{replacement}, 1))

	if got := closes.Load(); got != 1 {
		t.Fatalf("structural replacement must close the predecessor runtime client, got %d closes", got)
	}
	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	if scheduler.state.pending == nil || scheduler.state.pending.lane != laneStructural {
		t.Fatal("replacement discovery must remain a structural deployment")
	}
}

func TestPerformPodsDiscovered_CancelsDeploymentForRetiredAuthority(t *testing.T) {
	bus := testutil.NewTestBus()
	cancelCh := bus.SubscribeTypes("authority-cancellation", 1, events.EventTypeDeploymentCancelRequest)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	oldEndpoint := dataplane.Endpoint{URL: "http://same", PodName: "haproxy-0", PodNamespace: "haptic", PodUID: "uid-old"}
	scheduler.runtimeBypass.replaceEndpointAuthorities([]dataplane.Endpoint{oldEndpoint})
	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.activeDeploymentID = "deployment-old"
	scheduler.state.activeCorrelationID = "correlation-old"
	scheduler.schedulerMutex.Unlock()

	replacement := oldEndpoint
	replacement.PodUID = "uid-new"
	scheduler.performPodsDiscovered(t.Context(), events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{replacement}, 1))

	cancel := testutil.WaitForEvent[*events.DeploymentCancelRequestEvent](t, cancelCh, testutil.EventTimeout)
	assert.Equal(t, "deployment-old", cancel.DeploymentID)
	assert.Equal(t, "endpoint_authority_changed", cancel.Reason)
	assert.Equal(t, "correlation-old", cancel.CorrelationID())
}
