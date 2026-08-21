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
// CoalescingHandler hook (CoalescesOn in component.go). Base runs the
// component in mailbox mode: an intake goroutine drains the subscription
// channel immediately, collapsing uninterrupted runs of coalescible
// DeploymentScheduledEvents to their latest element; the worker then
// dispatches from the queue.
//
// The coalescing is load-bearing because deployment is single-threaded
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
// That hook is pinned DIRECTLY below (no timing involved); the mailbox's
// collapsing machinery itself is exercised deterministically by the
// component package's own tests (TestBase_MailboxNeverDropsUnderBurst
// and friends), which have the introspection helpers to pace on intake
// absorption.
//
// The behavioral half here deliberately asserts only the
// scheduling-independent contract: the deployer must END on the LATEST
// event of a queued burst and then go quiet. It must NOT assert
// "exactly one dispatch": the mailbox intake and the worker run
// concurrently, so the worker may legitimately dequeue a partial run
// (e.g. dispatch B) while the intake is still absorbing C from the
// subscription channel — this test's instant fake deploy (empty
// endpoints) makes that interleaving reachable under a loaded scheduler,
// and CI reproduced it (main pipeline 2649293230). Latest-wins means
// "never fall behind and always converge on the newest", not "atomic
// absorption of everything ever published".
func TestHandleDeploymentScheduled_CoalesceDrain_LatestWins(t *testing.T) {
	// The load-bearing wiring, pinned without any scheduling dependence:
	// the deployer coalesces deployment.scheduled and ONLY
	// deployment.scheduled (completed events clear the single-threaded
	// in-flight bookkeeping and must be seen individually).
	deployerForContract := createTestDeployer(testutil.NewTestBus())
	assert.Equal(t, []string{events.EventTypeDeploymentScheduled}, deployerForContract.CoalescesOn(),
		"the deployer must coalesce exactly deployment.scheduled — removing "+
			"the hook re-introduces the FIFO backlog; adding deployment.completed "+
			"would break the in-flight bookkeeping")

	bus := testutil.NewTestBus()
	completedChan := bus.Subscribe("completion-observer", 50)

	// createTestDeployer subscribes the component via component.Base.
	deployer := createTestDeployer(bus)
	bus.Start()

	mkScheduled := func(id string) *events.DeploymentScheduledEvent {
		return events.NewDeploymentScheduledEvent(
			"global\n  daemon\n",
			nil,                    // auxFiles
			[]dataplane.Endpoint{}, // empty endpoints → fast deploy that only publishes DeploymentCompletedEvent
			"runtime-config",
			"haptic",
			"test",
			"",   // contentChecksum
			nil,  // plan
			"",   // planID
			nil,  // statusPatches
			true, // coalescible
			events.WithCorrelation(id, id),
		)
	}

	// Queue 4 coalescible events in the component's subscription buffer
	// BEFORE the event loop starts (Publish delivers synchronously). The
	// mailbox collapses whatever run the intake has absorbed by the time
	// the worker dequeues; the latest (C) is always the final dispatch.
	published := []string{"initial-superseded", "queued-A-superseded", "queued-B-superseded", "queued-C-latest"}
	for _, id := range published {
		bus.Publish(mkScheduled(id))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Drive Base.Start directly, bypassing Component.Start: the component's
	// Start flushes pre-buffered events (leadership-term boundary), but this
	// test deliberately pre-buffers to exercise the coalescing drain — the
	// flush contract has its own test (TestStart_FlushesStaleEventsFromPreviousTerm).
	deployer.ctx = ctx
	go deployer.Base.Start(ctx)

	// Collect completions until the latest (C) lands. Superseded
	// intermediates MAY dispatch under adversarial scheduling (see the
	// doc comment), but every completion must be one of the published
	// burst, and C must arrive.
	burst := map[string]bool{}
	for _, id := range published {
		burst[id] = true
	}
	var last string
	for last != "queued-C-latest" {
		completed := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.EventTimeout)
		last = completed.CorrelationID()
		if !burst[last] {
			t.Fatalf("completion for unknown correlation %q (published burst: %v)", last, published)
		}
	}

	// Quiescence: once the latest dispatched, nothing further may deploy —
	// in particular no FIFO-style trailing replays of superseded events.
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
		"global\n  daemon\n", nil, []dataplane.Endpoint{},
		"runtime-config", "haptic", "test", "", nil, "", nil, true,
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
		"global\n  daemon\n", nil, []dataplane.Endpoint{},
		"runtime-config", "haptic", "test", "", nil, "", nil, true,
		events.WithCorrelation("fresh-current-term", "fresh-current-term"),
	)
	bus.Publish(fresh)
	completed := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completedChan, testutil.EventTimeout)
	assert.Equal(t, "fresh-current-term", completed.CorrelationID(),
		"the current term's scheduled deployment must still be processed after the flush")
}
