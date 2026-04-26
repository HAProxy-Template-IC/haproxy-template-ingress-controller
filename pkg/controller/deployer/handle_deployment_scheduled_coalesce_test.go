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

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// handleDeploymentScheduled has a coalesce-drain post-loop that the
// existing deployer tests do NOT exercise. The flow is:
//
//  1. Process the supplied event (performDeployment).
//  2. After it returns, drain c.eventChan via coalesce.DrainLatest:
//     for runs of all-coalescible DeploymentScheduledEvents, only the
//     latest survives — older ones are superseded.
//  3. Process the latest coalescible event found.
//  4. Loop until the channel is empty.
//
// The drain path is load-bearing because deployment is single-threaded
// (deploymentInProgress flag) but the validator + scheduler upstream
// can fire many DeploymentScheduledEvents during a single deployment.
// Without coalescing, the deployer would process every queued event in
// FIFO order — meaning during a flurry it would deploy the OLDEST
// pending config first, then the next-oldest, ... and fall further and
// further behind. With coalescing, after each deployment finishes the
// deployer jumps straight to the LATEST queued config, dropping the
// stale intermediates.
//
// A regression that removed the drain loop, the supersession bookkeeping,
// or the "process latest" call at the bottom would silently re-introduce
// the FIFO backlog and the deployer would lag arbitrarily behind the
// scheduler under load.
//
// Pin the all-coalescible case (the common steady-state path): N
// coalescible events queued + one passed in → exactly TWO
// performDeployment calls (initial + latest queued). The intermediate
// queued events must be SUPERSEDED. This is the contract that protects
// the deployer from falling behind.
func TestHandleDeploymentScheduled_CoalesceDrain_LatestWins(t *testing.T) {
	bus := testutil.NewTestBus()
	completedChan := bus.Subscribe("completion-observer", 50)
	bus.Start()

	deployer := createTestDeployer(bus)

	// Inject a buffered channel as the deployer's subscription —
	// pre-fill it before calling handleDeploymentScheduled so the
	// drain loop has events to find.
	queued := make(chan busevents.Event, 10)
	deployer.eventChan = queued

	mkScheduled := func(id string) *events.DeploymentScheduledEvent {
		return events.NewDeploymentScheduledEvent(
			"global\n  daemon\n",
			nil,                    // auxFiles
			nil,                    // parsedConfig
			[]dataplane.Endpoint{}, // empty endpoints → fast deploy that only publishes DeploymentCompletedEvent
			"runtime-config",
			"haptic",
			"test",
			"",   // contentChecksum
			true, // coalescible
			events.WithCorrelation(id, id),
		)
	}

	// Pre-queue 3 coalescible events. The first two must be superseded
	// by the third on drain.
	queued <- mkScheduled("queued-A-superseded")
	queued <- mkScheduled("queued-B-superseded")
	queued <- mkScheduled("queued-C-latest")

	// Process initial event. handleDeploymentScheduled then drains the
	// channel, supersedes A & B, and processes only C.
	deployer.handleDeploymentScheduled(context.Background(),
		mkScheduled("initial"))

	// Collect all DeploymentCompletedEvents observed.
	var observed []string
	for len(completedChan) > 0 {
		ev := <-completedChan
		if completed, ok := ev.(*events.DeploymentCompletedEvent); ok {
			observed = append(observed, completed.CorrelationID())
		}
	}

	// EXACTLY 2 deployments — the initial one + the latest of the
	// drained queue. The intermediate queued events MUST be superseded.
	assert.Equal(t, []string{"initial", "queued-C-latest"}, observed,
		"handleDeploymentScheduled MUST process the initial event and then "+
			"jump straight to the latest queued coalescible event, "+
			"superseding the intermediates. A regression that drained FIFO "+
			"instead of latest-wins would leave the deployer lagging "+
			"arbitrarily behind the scheduler under load")
}
