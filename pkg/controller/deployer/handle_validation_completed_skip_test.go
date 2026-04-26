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

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// handleValidationCompleted has a load-bearing optimization branch
// that the existing TestDeploymentScheduler_HandleValidationCompleted
// does NOT cover: the canSkip path that suppresses redundant
// deployments when the rendered config and pod set both match the
// last successfully-deployed state.
//
// Two contracts pinned:
//
//  1. Skip when both content checksum AND pod-set hash match the
//     last successful deploy → NO DeploymentScheduledEvent
//     published. Without this branch every reconciliation that
//     produced unchanged output (extremely common during steady
//     state with endpoint churn that doesn't change membership)
//     would re-deploy the same config to every pod, defeating
//     the whole content-deduplication system.
//
//  2. Drift prevention bypasses the skip → DeploymentScheduledEvent
//     IS published even when the hashes match. Drift prevention
//     is the recovery path for HAProxy pods that may have drifted
//     OUT of sync with the cached state; if we skipped on hash
//     match here, drift recovery would silently never deploy and
//     the system could never self-heal from out-of-band changes.

func TestHandleValidationCompleted_SkipsWhenConfigAndPodSetUnchanged(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx

	const checksum = "stable-content-checksum"
	endpoints := []dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodNamespace: "haptic"},
		{URL: "http://10.0.0.2:5555", PodName: "pod-B", PodNamespace: "haptic"},
	}
	podSetHash := computePodSetHash(endpoints)

	// Set up state: prior deployment succeeded with the SAME
	// checksum and pod set, so the cache-hit branch must skip the
	// new deploy.
	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = checksum
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedConfigHash = checksum   // ← match
	scheduler.lastDeployedPodSetHash = podSetHash // ← match
	scheduler.lastDeployedTime = time.Now()       // ← non-zero (deployment really happened)
	scheduler.mu.Unlock()

	// Use a non-drift trigger reason so the bypass doesn't fire.
	event := events.NewValidationCompletedEvent(
		[]string{}, 100, "config_change", nil, true,
	)

	scheduler.handleValidationCompleted(ctx, event)

	// The skip branch MUST NOT publish a DeploymentScheduledEvent.
	// AssertNoEvent waits its full timeout and fails if one shows up.
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](
		t, eventChan, testutil.NoEventTimeout)
}

func TestHandleValidationCompleted_DriftPreventionBypassesSkip(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx

	const checksum = "stable-content-checksum"
	endpoints := []dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodNamespace: "haptic"},
	}
	podSetHash := computePodSetHash(endpoints)

	// Same setup as the skip test — hashes deliberately match.
	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = checksum
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedConfigHash = checksum
	scheduler.lastDeployedPodSetHash = podSetHash
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	// CRITICAL difference: triggerReason is drift_prevention. The
	// skip branch MUST be bypassed even though hashes match.
	event := events.NewValidationCompletedEvent(
		[]string{}, 100,
		events.TriggerReasonDriftPrevention,
		nil, false, // drift events are non-coalescible
	)

	scheduler.handleValidationCompleted(ctx, event)

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](
		t, eventChan, testutil.LongTimeout)
	require.NotNil(t, scheduled,
		"drift prevention MUST bypass the canSkip cache hit — the recovery "+
			"path needs to actually deploy in case HAProxy pods have drifted "+
			"OUT of sync with the cached state. A regression that respected "+
			"the cache here would silently break drift recovery and the "+
			"system could never self-heal from out-of-band changes")
}
