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

// The deployer coalesces DeploymentScheduledEvents via component.Base's
// CoalescingHandler hook (CoalescesOn in component.go). The flow is:
//
//  1. The event loop dispatches one event (performDeployment).
//  2. After the dispatch returns, Base drains the subscription channel:
//     for runs of all-coalescible DeploymentScheduledEvents, only the
//     latest survives — older ones are superseded.
//  3. The latest coalescible event found is dispatched.
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
// A regression that removed the CoalescesOn hook (or returned the wrong
// event type from it) would silently re-introduce the FIFO backlog and
// the deployer would lag arbitrarily behind the scheduler under load.
//
// Pin the all-coalescible case (the common steady-state path): one event
// dispatched + N coalescible events queued → exactly TWO
// performDeployment calls (initial + latest queued). The intermediate
// queued events must be SUPERSEDED. This is the contract that protects
// the deployer from falling behind.
func TestHandleDeploymentScheduled_CoalesceDrain_LatestWins(t *testing.T) {
	bus := testutil.NewTestBus()
	completedChan := bus.Subscribe("completion-observer", 50)

	// createTestDeployer subscribes the component via component.Base.
	deployer := createTestDeployer(bus)
	bus.Start()

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
			nil,  // statusPatches
			true, // coalescible
			events.WithCorrelation(id, id),
		)
	}

	// Queue 4 coalescible events in the component's subscription buffer
	// BEFORE the event loop starts. The loop dispatches the first, then
	// Base's coalescing drain supersedes A & B and dispatches only C.
	bus.Publish(mkScheduled("initial"))
	bus.Publish(mkScheduled("queued-A-superseded"))
	bus.Publish(mkScheduled("queued-B-superseded"))
	bus.Publish(mkScheduled("queued-C-latest"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Drive Base.Start directly, bypassing Component.Start: the component's
	// Start flushes pre-buffered events (leadership-term boundary), but this
	// test deliberately pre-buffers to exercise the coalescing drain — the
	// flush contract has its own test (TestStart_FlushesStaleEventsFromPreviousTerm).
	deployer.ctx = ctx
	go deployer.Base.Start(ctx)

	// Collect the DeploymentCompletedEvents observed.
	first := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.EventTimeout)
	second := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.EventTimeout)

	// EXACTLY 2 deployments — the initial one + the latest of the
	// drained queue. The intermediate queued events MUST be superseded.
	assert.Equal(t, []string{"initial", "queued-C-latest"},
		[]string{first.CorrelationID(), second.CorrelationID()},
		"the deployer MUST process the first dispatched event and then "+
			"jump straight to the latest queued coalescible event, "+
			"superseding the intermediates. A regression that drained FIFO "+
			"instead of latest-wins would leave the deployer lagging "+
			"arbitrarily behind the scheduler under load")

	// No third deployment: A and B were superseded, never deployed.
	testutil.AssertNoEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.NoEventTimeout)
}

// The deployer's subscription persists across leadership terms (it embeds
// component.Base, which subscribes at construction). Events buffered while
// the deployer was NOT running — i.e. scheduled deployments from a previous
// leadership term — must be discarded at Start, not replayed: a stale
// render pushed into a new term would overwrite the fresh state until the
// next reconcile corrected it.
func TestStart_FlushesStaleEventsFromPreviousTerm(t *testing.T) {
	bus := testutil.NewTestBus()
	completedChan := bus.Subscribe("completion-observer", 50)

	deployer := createTestDeployer(bus)
	bus.Start()

	// A "previous term" event, buffered before Start.
	stale := events.NewDeploymentScheduledEvent(
		"global\n  daemon\n", nil, nil, []dataplane.Endpoint{},
		"runtime-config", "haptic", "test", "", nil, true,
		events.WithCorrelation("stale-prev-term", "stale-prev-term"),
	)
	bus.Publish(stale)

	// Let the bus route it into the deployer's subscription buffer before
	// Start flushes; otherwise the flush could race the routing goroutine
	// and the test would pass vacuously.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go deployer.Start(ctx)

	// The stale event must NOT produce a deployment.
	testutil.AssertNoEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.NoEventTimeout)

	// A current-term event still deploys.
	fresh := events.NewDeploymentScheduledEvent(
		"global\n  daemon\n", nil, nil, []dataplane.Endpoint{},
		"runtime-config", "haptic", "test", "", nil, true,
		events.WithCorrelation("fresh-current-term", "fresh-current-term"),
	)
	bus.Publish(fresh)
	completed := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.EventTimeout)
	assert.Equal(t, "fresh-current-term", completed.CorrelationID(),
		"the current term's scheduled deployment must still be processed after the flush")
}
