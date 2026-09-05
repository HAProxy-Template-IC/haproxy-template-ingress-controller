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
// The (2) branch is load-bearing: when the scheduler holds a valid config but
// the discovery event reports zero endpoints — which happens during
// cluster-wide HAProxy churn (rolling the whole fleet, deleting all pods,
// network partition recovery) — we must NOT call scheduleOrQueue with an empty
// endpoint list. That would publish a DeploymentScheduledEvent downstream
// observers read as "we deployed", and race the next discovery event.
func TestPerformPodsDiscovered_EmptyEndpointsWithValidConfigSkipsDeployment(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx
	oldEndpoints := []dataplane.Endpoint{{URL: "http://old", PodUID: "uid-old"}}
	scheduler.schedulerMutex.Lock()
	scheduler.state.pending = depFor(oldEndpoints)
	scheduler.lastPodSetHash = computePodSetHash(oldEndpoints)
	scheduler.schedulerMutex.Unlock()

	primeValidated(scheduler, "global\n  daemon\n", "checksum", "plan")

	scheduler.performPodsDiscovered(ctx, events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{}, 0))

	scheduler.schedulerMutex.Lock()
	assert.Nil(t, scheduler.state.pending, "an empty fleet must retire pending work")
	scheduler.schedulerMutex.Unlock()

	// The endpoint set MUST be updated even when empty — otherwise the
	// scheduler's view goes stale and the next discovery races the old set.
	scheduler.mu.RLock()
	assert.Empty(t, scheduler.currentEndpoints)
	scheduler.mu.RUnlock()

	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.NoEventTimeout)
}

// A pod replaced under the same URL is a different authority: the deploy in
// flight targets a pod that no longer exists, so it must be cancelled and the
// replacement set deployed as a whole.
func TestPerformPodsDiscovered_CancelsDeploymentForRetiredAuthority(t *testing.T) {
	bus := testutil.NewTestBus()
	cancelCh := bus.SubscribeTypes("authority-cancellation", 1, events.EventTypeDeploymentCancelRequest)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	oldEndpoint := dataplane.Endpoint{URL: "http://same", PodName: "haproxy-0", PodNamespace: "haptic", PodUID: "uid-old"}
	scheduler.schedulerMutex.Lock()
	scheduler.lastPodSetHash = computePodSetHash([]dataplane.Endpoint{oldEndpoint})
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
