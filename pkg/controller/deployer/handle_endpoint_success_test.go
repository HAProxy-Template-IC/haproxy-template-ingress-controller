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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// handleEndpointSuccess is the per-endpoint success path inside
// the parallel deployment fan-out. It mirrors handleEndpointFailure
// but with three additional load-bearing contracts that are
// distinct from the failure path:
//
//  1. The no-op deployment optimization: when isNoOpDeployment
//     returns true (no reload, no operations), the
//     ConfigAppliedToPodEvent MUST be skipped — even with
//     runtime config set. This prevents flooding the K8s API with
//     redundant status updates for no-change reconciliations
//     during steady-state.
//
//  2. reloadsTriggered counter is conditionally incremented based
//     on syncResult.ReloadTriggered. The aggregator uses this to
//     report "deployed N pods, M reloads" — operators rely on this
//     ratio to spot config changes that require reloads vs
//     runtime-only changes.
//
//  3. backendDiffFields is captured ONLY ONCE (first writer wins).
//     This is documented invariant: the diff fields are identical
//     across endpoints because they all receive the same config,
//     so capturing per-endpoint would be redundant churn under
//     the breakdownMu lock.

func TestHandleEndpointSuccess_PublishesInstanceDeployedAndAppliedWhenNotNoOp(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	ep := &dataplane.Endpoint{
		URL:          "http://10.0.0.1:5555",
		PodName:      "haproxy-pod-1",
		PodNamespace: "haptic",
	}
	// Non-no-op result: both reload triggered AND operations present.
	syncResult := &dataplane.SyncResult{
		ReloadTriggered: true,
		Details: dataplane.DiffDetails{
			TotalOperations: 3,
		},
	}
	state := &deploymentState{
		operationBreakdown: make(map[string]int),
	}

	c.handleEndpointSuccess(
		ep, syncResult, 250, "checksum-abc", false,
		"rt-cfg-1", "haptic", "corr-1", state,
	)

	// InstanceDeployedEvent MUST always fire on success.
	deployed := testutil.WaitForEvent[*events.InstanceDeployedEvent](
		t, eventChan, testutil.LongTimeout)
	require.NotNil(t, deployed,
		"InstanceDeployedEvent MUST be published — without it, the metrics + "+
			"commentator + status pipeline never observe the per-pod success")
	assert.Equal(t, "corr-1", deployed.CorrelationID(),
		"correlation ID MUST propagate so downstream tracing can link "+
			"the per-pod result back to the deployment cycle")
	assert.True(t, deployed.ReloadRequired,
		"ReloadRequired MUST mirror syncResult.ReloadTriggered so the "+
			"aggregator's reload-vs-runtime breakdown is accurate")

	// ConfigAppliedToPodEvent fires because !isNoOp (operations > 0).
	applied := testutil.WaitForEvent[*events.ConfigAppliedToPodEvent](
		t, eventChan, testutil.LongTimeout)
	require.NotNil(t, applied,
		"ConfigAppliedToPodEvent MUST fire on a non-no-op success — the "+
			"statusapplier consumes this to record the deployed checksum "+
			"on the runtime config's status subresource")

	// State counters: success + reload + operations.
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.successCount),
		"successCount MUST be incremented")
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.reloadsTriggered),
		"reloadsTriggered MUST be incremented when syncResult.ReloadTriggered "+
			"is true — this is what powers the operator-facing "+
			"'N deploys, M reloads' summary")
	assert.Equal(t, int32(3), atomic.LoadInt32(&state.totalOperations),
		"totalOperations MUST be added from syncResult.Details.TotalOperations")
}

func TestHandleEndpointSuccess_SkipsAppliedEventOnNoOpDeployment(t *testing.T) {
	// No-op result (no reload, no operations) — even with runtime
	// config set, the ConfigAppliedToPodEvent MUST be skipped.
	// Without this branch, every steady-state reconciliation that
	// produced a deduplicated config would hammer the K8s status
	// subresource with no-change updates.
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	ep := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "p"}
	noOpResult := &dataplane.SyncResult{
		ReloadTriggered: false,
		Details:         dataplane.DiffDetails{TotalOperations: 0},
	}
	state := &deploymentState{
		operationBreakdown: make(map[string]int),
	}

	c.handleEndpointSuccess(
		ep, noOpResult, 50, "checksum-abc", false,
		"rt-cfg-1", "haptic", "corr-1", state,
	)

	// InstanceDeployedEvent MUST still fire — observability cares
	// about every deploy attempt, not just the change-producing ones.
	require.NotNil(t,
		testutil.WaitForEvent[*events.InstanceDeployedEvent](
			t, eventChan, testutil.LongTimeout),
		"InstanceDeployedEvent MUST fire even for no-op deploys so per-pod "+
			"latency metrics keep updating")

	// ConfigAppliedToPodEvent MUST be suppressed on no-op.
	testutil.AssertNoEvent[*events.ConfigAppliedToPodEvent](
		t, eventChan, testutil.NoEventTimeout)

	// Counters: success counted, reload NOT counted (false), no ops.
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.successCount))
	assert.Equal(t, int32(0), atomic.LoadInt32(&state.reloadsTriggered),
		"reloadsTriggered MUST stay 0 when syncResult.ReloadTriggered is "+
			"false — a regression that always incremented would inflate "+
			"the reload count in the operator summary")
}

func TestHandleEndpointSuccess_BackendDiffFieldsCapturedOnceFirstWriterWins(t *testing.T) {
	// Documented invariant: backendDiffFields is captured ONLY ONCE
	// because the diff fields are identical across endpoints (same
	// config). A regression that re-captured every call would race
	// other writers under breakdownMu, but more importantly would
	// risk overwriting the first endpoint's value with a later
	// endpoint's empty/different one (e.g. if the dataplane
	// returned a partial diff for a later pod).
	bus := testutil.NewTestBus()
	bus.Start()
	c := createTestDeployer(bus)

	ep1 := &dataplane.Endpoint{URL: "http://1:5555", PodName: "p1"}
	ep2 := &dataplane.Endpoint{URL: "http://2:5555", PodName: "p2"}

	first := &dataplane.SyncResult{
		ReloadTriggered: true,
		Details: dataplane.DiffDetails{
			TotalOperations:   1,
			BackendDiffFields: map[string][]string{"backend-a": {"GUID"}},
		},
	}
	second := &dataplane.SyncResult{
		ReloadTriggered: true,
		Details: dataplane.DiffDetails{
			TotalOperations:   1,
			BackendDiffFields: map[string][]string{"backend-a": {"DIFFERENT-FIELD"}},
		},
	}
	state := &deploymentState{
		operationBreakdown: make(map[string]int),
	}

	c.handleEndpointSuccess(ep1, first, 100, "k", false, "", "", "corr-1", state)
	captured := state.backendDiffFields
	require.NotEmpty(t, captured,
		"first call MUST populate backendDiffFields — it's how the "+
			"aggregator surfaces 'which backends differ' in operator output")

	c.handleEndpointSuccess(ep2, second, 100, "k", false, "", "", "corr-2", state)

	state.breakdownMu.Lock()
	defer state.breakdownMu.Unlock()
	assert.Equal(t, captured, state.backendDiffFields,
		"second call MUST NOT overwrite backendDiffFields — the value is "+
			"captured first-writer-wins under the documented invariant that "+
			"the diff fields are identical across endpoints (all receive the "+
			"same config). A regression that re-captured per call would let a "+
			"later endpoint's partial/different diff clobber the first "+
			"endpoint's authoritative value")
}
