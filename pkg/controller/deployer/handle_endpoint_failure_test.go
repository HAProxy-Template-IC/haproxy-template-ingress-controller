// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// handleEndpointFailure is the per-endpoint failure path inside a
// parallel deployment fan-out. Coverage was 0%; the function had
// no direct tests despite being on every operator's alerting path.
//
// Three load-bearing contracts:
//
//  1. Always publishes InstanceDeploymentFailedEvent with the
//     endpoint, error message, retryable=true, AND the correlation
//     ID propagated as both correlation and request ID. This is
//     how the metrics + commentator + status machinery learn about
//     a failed instance; a regression that dropped the publish
//     would silently make per-pod failure counters stop updating.
//
//  2. Conditionally publishes ConfigAppliedToPodEvent ONLY when
//     BOTH runtimeConfigName AND runtimeConfigNamespace are set.
//     The status applier consumes this event to record the failure
//     in the runtime config's status; without the conditional, the
//     bootstrap path (no runtime config yet) would publish events
//     with empty references and cause the status applier to no-op
//     or error log. With the conditional we still get the failed
//     InstanceDeploymentFailedEvent but skip the status update.
//
//  3. failureCount is atomically incremented. The aggregator at
//     the end of the fan-out reads this to compute the deployment
//     summary; under the parallel-execution model nothing else
//     would update it, so a regression here would make the summary
//     report all-success even when N instances failed.

func TestHandleEndpointFailure_PublishesFailureEventWithCorrelation(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	const (
		corrID    = "test-correlation-id"
		errMsg    = "dataplane returned 500"
		runtimeNs = "haptic"
		runtimeNm = "rt-cfg-1"
	)
	ep := &dataplane.Endpoint{
		URL:          "http://10.0.0.1:5555",
		PodName:      "haproxy-pod-1",
		PodNamespace: runtimeNs,
	}
	state := &deploymentState{}

	c.handleEndpointFailure(
		ep, errors.New(errMsg), 100, "checksum-abc", false,
		runtimeNm, runtimeNs, corrID, state,
	)

	failed := testutil.WaitForEvent[*events.InstanceDeploymentFailedEvent](
		t, eventChan, testutil.LongTimeout)
	require.NotNil(t, failed,
		"InstanceDeploymentFailedEvent MUST be published — without it, "+
			"per-pod failure counters in the metrics + commentator stop "+
			"updating and operators lose all visibility into instance-"+
			"level failures")
	assert.Contains(t, failed.Error, errMsg,
		"the failure event MUST carry the underlying error message — "+
			"a regression that swallowed it would force operators to "+
			"correlate alerts with controller logs by timestamp alone")
	assert.Equal(t, corrID, failed.CorrelationID(),
		"CorrelationID MUST propagate so downstream events tied to this "+
			"deployment cycle can be linked together for tracing")
	assert.True(t, failed.Retryable,
		"endpoint failures MUST be marked retryable — non-retryable "+
			"failures bypass the deployer's retry path entirely, so a "+
			"regression here would silently turn transient errors permanent")

	// Failure counter MUST be incremented (atomic, single-test
	// runs serial so we can read directly).
	assert.Equal(t, int32(1), atomic.LoadInt32(&state.failureCount),
		"state.failureCount MUST be incremented — the aggregator reads "+
			"this to build the deployment summary; without the increment "+
			"the summary reports all-success even when instances failed")
}

func TestHandleEndpointFailure_PublishesAppliedEventWhenRuntimeConfigSet(t *testing.T) {
	// When BOTH runtimeConfigName and runtimeConfigNamespace are
	// set, the status update event MUST also be published so the
	// statusapplier can record the failure on the runtime config's
	// status subresource.
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	ep := &dataplane.Endpoint{
		URL:          "http://10.0.0.1:5555",
		PodName:      "haproxy-pod-1",
		PodNamespace: "haptic",
	}
	state := &deploymentState{}

	c.handleEndpointFailure(
		ep, errors.New("boom"), 100, "checksum-abc", false,
		"rt-cfg-1", "haptic", "corr-1", state,
	)

	// Drain BOTH events. Order is implementation-defined within a
	// single handler so use a typed wait for each.
	require.NotNil(t,
		testutil.WaitForEvent[*events.InstanceDeploymentFailedEvent](
			t, eventChan, testutil.LongTimeout),
		"InstanceDeploymentFailedEvent MUST always come first/eventually")

	applied := testutil.WaitForEvent[*events.ConfigAppliedToPodEvent](
		t, eventChan, testutil.LongTimeout)
	require.NotNil(t, applied,
		"ConfigAppliedToPodEvent MUST be published when runtimeConfig "+
			"name+namespace are set — the statusapplier consumes this to "+
			"record the failure in the runtime config's status; without "+
			"it the status reports the failed deployment as still "+
			"in-progress (or never updates)")
	assert.NotNil(t, applied.SyncMetadata,
		"the published event MUST carry SyncMetadata so the failure "+
			"reason reaches the status subresource")
	assert.Equal(t, "boom", applied.SyncMetadata.Error,
		"SyncMetadata.Error MUST contain the underlying error string")
}

func TestHandleEndpointFailure_NoAppliedEventWhenRuntimeConfigEmpty(t *testing.T) {
	// During the bootstrap window the runtime config name/namespace
	// can be empty (no CRD published yet). In that case the failure
	// event still fires but the per-pod status update MUST be
	// skipped — publishing it with empty references would force the
	// statusapplier to no-op or error-log per pod, swamping the
	// operator's log feed during startup.
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()
	c := createTestDeployer(bus)

	ep := &dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "p"}
	state := &deploymentState{}

	c.handleEndpointFailure(
		ep, errors.New("boom"), 100, "checksum-abc", false,
		"", "", // ← runtime config empty
		"corr-1", state,
	)

	// InstanceDeploymentFailedEvent MUST still fire.
	require.NotNil(t,
		testutil.WaitForEvent[*events.InstanceDeploymentFailedEvent](
			t, eventChan, testutil.LongTimeout),
		"the failure event MUST always fire so metrics/commentator see "+
			"the failed instance even before the runtime config exists")

	// ConfigAppliedToPodEvent MUST NOT fire on the empty-config branch.
	testutil.AssertNoEvent[*events.ConfigAppliedToPodEvent](
		t, eventChan, testutil.NoEventTimeout)
}
