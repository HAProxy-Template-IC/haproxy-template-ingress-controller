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

func installFakeDeployment(c *Component, deploymentID string) (cancelInvoked, done chan struct{}) {
	cancelInvoked = make(chan struct{}, 1)
	done = make(chan struct{})

	c.cancelMu.Lock()
	c.activeDeploymentID = deploymentID
	c.activeCorrelationID = "trace-" + deploymentID
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
	t.Run("cancel before start is retained for the exact deployment", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		event := events.NewDeploymentCancelRequestEvent("any-deployment", "scheduler_timeout",
			events.WithCorrelation("any-correlation", ""))

		require.NotPanics(t, func() {
			c.handleDeploymentCancelRequest(event)
		})

		ctx, cancel := c.beginDeployment(t.Context(), "any-deployment", "any-correlation")
		defer cancel()
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
		assert.Empty(t, c.pendingCancellation)
	})

	t.Run("matching deployment ID invokes the active cancel func", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		const cid = "deployment-A"
		cancelInvoked, _ := installFakeDeployment(c, cid)

		event := events.NewDeploymentCancelRequestEvent(cid, "scheduler_timeout",
			events.WithCorrelation("trace", cid))

		c.handleDeploymentCancelRequest(event)

		select {
		case <-cancelInvoked:
			// expected: the cancel func ran
		case <-time.After(time.Second):
			t.Fatal("matching cancel request must invoke activeCancelFunc")
		}
	})

	t.Run("next deployment cancellation does not abort the current deployment", func(t *testing.T) {
		bus := busevents.NewEventBus(10)
		bus.Start()
		c := createTestDeployer(bus)

		cancelInvoked, _ := installFakeDeployment(c, "current-deployment")

		event := events.NewDeploymentCancelRequestEvent("next-deployment", "endpoint_authority_changed",
			events.WithCorrelation("trace", "next-deployment"))

		c.handleDeploymentCancelRequest(event)

		select {
		case <-cancelInvoked:
			t.Fatal("a cancel for the next deployment must not abort the current deployment")
		default:
		}

		ctx, cancel := c.beginDeployment(t.Context(), "next-deployment", "trace")
		defer cancel()
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
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
		c.activeDeploymentID = "x"
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
