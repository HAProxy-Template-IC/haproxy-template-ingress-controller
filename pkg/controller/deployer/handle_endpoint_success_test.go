// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"sync/atomic"
	"testing"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// handleEndpointSuccess is the per-pod ACK path inside the deployment fan-out.
// Its contracts:
//
//  1. Publish ConfigAppliedToPodEvent whenever the HAProxyCfg identity is
//     known — unconditionally, including for a no-op apply. Skipping the no-op
//     broke the spec.Checksum ↔ status.deployedToPods[].Checksum invariant: a
//     render whose bytes changed without changing any HAProxy object left the
//     pods recorded at the previous checksum forever.
//  2. Count the ACK, and count convergence separately. A pod whose reload is
//     only scheduled accepted the files but does not serve them yet, so it must
//     not make the fleet look converged.
//  3. Record the apply and its ops on the per-pod counters.
func ackOutcome(mode string, converged bool, ops ...api.Op) *podOutcome {
	return &podOutcome{
		result: &api.ApplyResult{
			PlanID:             "plan-1",
			OK:                 true,
			Mode:               mode,
			AppliedPlanID:      "plan-1",
			AppliedPlanProof:   "a:1",
			RunningPlanID:      "plan-1",
			RunningPlanProof:   "a:1",
			WorkerOpsPlanID:    "plan-1",
			WorkerOpsPlanProof: "a:1",
			Reload:             &api.ReloadInfo{Performed: mode == api.ResultReload, OK: true},
		},
		sent:      ops,
		converged: converged,
	}
}

func TestHandleEndpointSuccess_PublishesPodStatusWithPlanIdentity(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0", PodNamespace: "haptic"}
	state := &deploymentState{operationBreakdown: map[string]int{}}
	outcome := ackOutcome(api.ResultRuntime, true, api.Op{Kind: api.OpServerSetAddr})

	event := scheduledEvent("rt-cfg-1", "haptic", "corr-1")
	c.handleEndpointSuccess(endpoint, outcome, 250, event, state)

	applied := testutil.WaitForEvent[*events.ConfigAppliedToPodEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, applied)
	assert.Equal(t, "haproxy-0", applied.PodName)
	assert.Equal(t, deploymentContentChecksum(event), applied.Checksum)
	require.NotNil(t, applied.SyncMetadata)
	assert.Equal(t, "plan-1", applied.SyncMetadata.AppliedPlanID)
	assert.Equal(t, "plan-1", applied.SyncMetadata.RunningPlanID)
	assert.Equal(t, api.ResultRuntime, applied.SyncMetadata.Mode)
	assert.False(t, applied.SyncMetadata.ReloadTriggered)
	assert.Empty(t, applied.SyncMetadata.Error)

	assert.Equal(t, int32(1), atomic.LoadInt32(&state.ackCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.convergedCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&state.reloadsTriggered))
	assert.Equal(t, 1, state.totalOperations)
}

// A no-op apply still advances the pod's recorded checksum: the render's bytes
// may differ without changing a single HAProxy object.
func TestHandleEndpointSuccess_PublishesPodStatusForANoop(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}
	state := &deploymentState{operationBreakdown: map[string]int{}}

	c.handleEndpointSuccess(endpoint, ackOutcome(api.ResultNoop, true), 50,
		scheduledEvent("rt-cfg-1", "haptic", "corr-1"), state)

	applied := testutil.WaitForEvent[*events.ConfigAppliedToPodEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, applied)
	assert.Equal(t, api.ResultNoop, applied.SyncMetadata.Mode)
}

// A scheduled reload is an accepted apply that has not converged: the files are
// written, the worker still runs the previous plan.
func TestHandleEndpointSuccess_ScheduledReloadIsNotConverged(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}
	state := &deploymentState{operationBreakdown: map[string]int{}}

	c.handleEndpointSuccess(endpoint, ackOutcome(api.ResultScheduled, false), 50,
		scheduledEvent("rt-cfg-1", "haptic", "corr-1"), state)

	assert.Equal(t, int32(1), atomic.LoadInt32(&state.ackCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&state.convergedCount),
		"a pod waiting for its reload must not count towards fleet convergence")
}

// The pod's plan ids feed the plan cache's retention set: a plan some pod still
// runs must survive the fleet's garbage collection.
func TestHandleEndpointSuccess_RecordsReferencedPlans(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}
	state := &deploymentState{operationBreakdown: map[string]int{}}
	outcome := ackOutcome(api.ResultReload, true)
	c.handleEndpointSuccess(endpoint, outcome, 50,
		scheduledEvent("rt-cfg-1", "haptic", "corr-1"), state)

	assert.Equal(t, []planCacheKey{{authority: podKey(endpoint), proof: "a:1"}},
		c.fleetPlanRefs([]dataplane.Endpoint{*endpoint}))
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.reloadsTriggered))
}

// No HAProxyCfg identity yet (bootstrap): the status write is skipped rather
// than published with empty references, which the status applier cannot use.
func TestHandleEndpointSuccess_NoPodStatusWithoutRuntimeConfig(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}
	state := &deploymentState{operationBreakdown: map[string]int{}}

	c.handleEndpointSuccess(endpoint, ackOutcome(api.ResultReload, true), 50,
		scheduledEvent("", "", "corr-1"), state)

	testutil.AssertNoEvent[*events.ConfigAppliedToPodEvent](t, eventChan, testutil.NoEventTimeout)
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.ackCount))
}

// The per-pod counters are what an operator queries for the reload-free share
// of a rollout, so pin that an ACK moves them.
func TestHandleEndpointSuccess_RecordsApplyAndOpCounters(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}
	state := &deploymentState{operationBreakdown: map[string]int{}}
	outcome := ackOutcome(api.ResultRuntime, true,
		api.Op{Kind: api.OpBackendAdd}, api.Op{Kind: api.OpServerAdd}, api.Op{Kind: api.OpServerAdd})

	c.handleEndpointSuccess(endpoint, outcome, 50,
		scheduledEvent("rt-cfg-1", "haptic", "corr-1"), state)

	assert.Equal(t, 1.0, promtestutil.ToFloat64(
		c.metrics.AgentApplyTotal.WithLabelValues("haproxy-0", api.ResultRuntime)))
	assert.Equal(t, 1.0, promtestutil.ToFloat64(
		c.metrics.RuntimeBackendOpsTotal.WithLabelValues(api.OpBackendAdd)))
	assert.Equal(t, 2.0, promtestutil.ToFloat64(
		c.metrics.RuntimeServerOpsTotal.WithLabelValues(api.OpServerAdd)))

	state.mu.Lock()
	defer state.mu.Unlock()
	assert.Equal(t, map[string]int{api.OpBackendAdd: 1, api.OpServerAdd: 2}, state.operationBreakdown)
}

// A backend_add HAProxy refused is A5: a leftover backend still holds the name,
// the agent reloaded the desired set instead, and the fleet-level fallback
// counter names why so an operator can tell a name collision from any other
// refused op. A successful runtime apply carries no failed result and moves
// nothing.
func TestHandleEndpointSuccess_RecordsBackendFallback(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	c := createTestDeployer(bus)

	endpoint := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "haproxy-0"}

	collision := ackOutcome(api.ResultReload, false, api.Op{Kind: api.OpBackendAdd})
	collision.result.OpResults = []api.OpResult{
		{Kind: api.OpBackendAdd, OK: false, Output: "Backend 'gtw_x' name is already used by other proxy."},
	}
	c.handleEndpointSuccess(endpoint, collision, 50, scheduledEvent("rt-cfg-1", "haptic", "corr-1"),
		&deploymentState{operationBreakdown: map[string]int{}})

	// A backend_add refused for a different reason is op_rejected, not a
	// collision: the label comes from HAProxy's words, never the op kind.
	other := ackOutcome(api.ResultReload, false, api.Op{Kind: api.OpBackendAdd})
	other.result.OpResults = []api.OpResult{{Kind: api.OpBackendAdd, OK: false, Output: "unknown keyword 'frm' in backend"}}
	c.handleEndpointSuccess(endpoint, other, 50, scheduledEvent("rt-cfg-1", "haptic", "corr-1"),
		&deploymentState{operationBreakdown: map[string]int{}})

	clean := ackOutcome(api.ResultRuntime, true, api.Op{Kind: api.OpServerSetWeight})
	clean.result.OpResults = []api.OpResult{{Kind: api.OpServerSetWeight, OK: true}}
	c.handleEndpointSuccess(endpoint, clean, 50, scheduledEvent("rt-cfg-1", "haptic", "corr-1"),
		&deploymentState{operationBreakdown: map[string]int{}})

	assert.Equal(t, 1.0, promtestutil.ToFloat64(
		c.metrics.RuntimeBackendFallbackTotal.WithLabelValues("name_collision")))
	assert.Equal(t, 1.0, promtestutil.ToFloat64(
		c.metrics.RuntimeBackendFallbackTotal.WithLabelValues("op_rejected")))
}
