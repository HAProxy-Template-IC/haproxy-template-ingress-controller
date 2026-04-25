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
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// handleDeploymentCancelRequest is the safety latch the DeploymentScheduler
// uses to abort an in-flight deployment by correlation ID. Two contracts
// must hold:
//
//  1. The cancel only fires when the request's correlation ID matches the
//     deployment in progress. A mismatched cancel must be a NO-OP, otherwise
//     a stale timeout request from a previous reconciliation would silently
//     abort the new deployment that just started.
//  2. When no deployment is in progress (the all-replica state), the cancel
//     is also a no-op. Otherwise a stray cancel during pod startup would
//     panic on a nil cancel func.
//
// cancelActiveDeployment is the unconditional shutdown path used during
// graceful shutdown. It must:
//
//   - No-op when there's nothing active (cancel func is nil).
//   - Call the active cancel func when set.
//   - Wait for the deploymentDone signal if present (so leases / locks are
//     released before the controller exits).

// installFakeDeployment plants synthetic in-flight state on the Component so
// the cancel paths have something to act on. Returns the channel callers can
// inspect to verify whether the cancel func was invoked.
func installFakeDeployment(c *Component, correlationID string) (cancelInvoked, done chan struct{}) {
	cancelInvoked = make(chan struct{}, 1)
	done = make(chan struct{})

	c.cancelMu.Lock()
	c.activeCorrelationID = correlationID
	c.activeCancelFunc = func() {
		// Non-blocking send so multiple cancel calls don't deadlock.
		select {
		case cancelInvoked <- struct{}{}:
		default:
		}
	}
	c.deploymentDone = done
	c.cancelMu.Unlock()

	return cancelInvoked, done
}

func TestHandleDeploymentCancelRequest(t *testing.T) {
	t.Run("no active deployment is a no-op (no panic on nil cancel func)", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		// No deployment in progress — cancel must NOT panic, must NOT
		// publish anything, must NOT touch internal state.
		event := events.NewDeploymentCancelRequestEvent("scheduler_timeout",
			events.WithCorrelation("any-correlation", ""))

		require.NotPanics(t, func() {
			c.handleDeploymentCancelRequest(event)
		})

		// State must remain pristine after the no-op path.
		c.cancelMu.Lock()
		defer c.cancelMu.Unlock()
		assert.Empty(t, c.activeCorrelationID)
		assert.Nil(t, c.activeCancelFunc)
	})

	t.Run("matching correlation ID invokes the active cancel func", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		const cid = "deployment-A"
		cancelInvoked, _ := installFakeDeployment(c, cid)

		// Build the request as the scheduler would: WithCorrelation
		// stores the correlation ID for the deployment we want killed.
		event := events.NewDeploymentCancelRequestEvent("scheduler_timeout",
			events.WithCorrelation(cid, ""))

		c.handleDeploymentCancelRequest(event)

		select {
		case <-cancelInvoked:
			// expected: the cancel func ran
		case <-time.After(time.Second):
			t.Fatal("matching cancel request must invoke activeCancelFunc")
		}
	})

	t.Run("mismatched correlation ID is a no-op (does not abort current deployment)", func(t *testing.T) {
		// This is the most important branch: a stale cancel from a
		// previous reconciliation must NOT abort the deployment that
		// just started under a fresh correlation ID. Otherwise a
		// timer overshoot would silently kill healthy reconciliations.
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		cancelInvoked, _ := installFakeDeployment(c, "current-deployment")

		// Request targets a different (stale) correlation ID.
		event := events.NewDeploymentCancelRequestEvent("scheduler_timeout",
			events.WithCorrelation("stale-deployment", ""))

		c.handleDeploymentCancelRequest(event)

		// Give any (incorrect) async cancel a chance to fire.
		select {
		case <-cancelInvoked:
			t.Fatal("mismatched correlation ID must NOT invoke activeCancelFunc; " +
				"otherwise stale scheduler timeouts would abort fresh deployments")
		case <-time.After(50 * time.Millisecond):
			// expected: no cancel
		}
	})
}

func TestCancelActiveDeployment(t *testing.T) {
	t.Run("no active deployment is a no-op", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		// No state set up — must NOT panic on the nil activeCancelFunc.
		require.NotPanics(t, func() {
			c.cancelActiveDeployment("test-reason")
		})
	})

	t.Run("active deployment: cancel func is invoked and we wait for deploymentDone", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		cancelInvoked, done := installFakeDeployment(c, "shutdown-deployment")

		// Run cancelActiveDeployment in a goroutine since it BLOCKS on
		// deploymentDone being closed. This is the critical contract:
		// cancelActiveDeployment must not return until the deployment
		// goroutine signals completion, so leases get released before
		// the controller exits.
		returned := make(chan struct{})
		go func() {
			c.cancelActiveDeployment("test-reason")
			close(returned)
		}()

		// 1. The cancel func MUST fire promptly.
		select {
		case <-cancelInvoked:
		case <-time.After(time.Second):
			t.Fatal("cancelActiveDeployment must invoke activeCancelFunc")
		}

		// 2. cancelActiveDeployment MUST be still blocked at this
		// point, waiting for deploymentDone. Verifying by checking
		// it has NOT returned yet.
		select {
		case <-returned:
			t.Fatal("cancelActiveDeployment returned before deploymentDone closed; " +
				"this would let the controller exit before in-flight state is released")
		case <-time.After(50 * time.Millisecond):
			// expected: still blocked
		}

		// 3. Closing deploymentDone must unblock cancelActiveDeployment.
		close(done)

		select {
		case <-returned:
			// expected: returned now that deploymentDone is closed
		case <-time.After(time.Second):
			t.Fatal("cancelActiveDeployment did not return after deploymentDone closed")
		}
	})

	t.Run("active deployment with no deploymentDone channel returns immediately", func(t *testing.T) {
		// When deploymentDone is nil (e.g. cancel during a tiny window
		// between cancel-func registration and channel creation),
		// cancelActiveDeployment must NOT block forever — it returns
		// after firing the cancel func.
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		cancelInvoked := make(chan struct{}, 1)
		c.cancelMu.Lock()
		c.activeCorrelationID = "x"
		c.activeCancelFunc = func() { cancelInvoked <- struct{}{} }
		c.deploymentDone = nil // explicitly nil
		c.cancelMu.Unlock()

		// Use a deadline so a regression that re-introduced a wait on
		// a nil channel would hang the test instead of falsely passing.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		returned := make(chan struct{})
		go func() {
			c.cancelActiveDeployment("test-reason")
			close(returned)
		}()

		select {
		case <-cancelInvoked:
		case <-ctx.Done():
			t.Fatal("activeCancelFunc must fire even when deploymentDone is nil")
		}

		select {
		case <-returned:
			// expected: returns immediately, no deploymentDone wait
		case <-ctx.Done():
			t.Fatal("cancelActiveDeployment hung waiting on a nil deploymentDone channel")
		}
	})
}
