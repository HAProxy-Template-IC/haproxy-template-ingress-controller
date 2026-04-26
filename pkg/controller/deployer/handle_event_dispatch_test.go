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
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// handleEvent is the deployer's two-case dispatch table. The
// existing TestComponent_HandleEvent covers the happy paths for
// non-deployment events (silent ignore) and DeploymentScheduledEvent
// (routed to handleDeploymentScheduled). The DeploymentCancelRequestEvent
// dispatch was uncovered: a regression that removed the cancel case
// from the type switch would silently drop EVERY scheduler timeout
// recovery — deployments stuck past the scheduler's timeout would
// stay running forever (the scheduler publishes the cancel request,
// but the deployer would no longer act on it).
//
// This file pins the cancel-event dispatch by going through
// handleEvent (not handleDeploymentCancelRequest directly, which
// is already tested in deployment_lifecycle_test.go). The proof of
// dispatch is the side effect: with a fake deployment installed,
// the cancel func MUST be invoked.

func TestComponent_HandleEvent_RoutesDeploymentCancelRequest(t *testing.T) {
	bus := busevents.NewEventBus(10)
	bus.Start()
	c := createTestDeployer(bus)

	const cid = "deployment-to-cancel"
	cancelInvoked, _ := installFakeDeployment(c, cid)

	event := events.NewDeploymentCancelRequestEvent(
		"scheduler_timeout",
		events.WithCorrelation(cid, ""),
	)

	c.handleEvent(context.Background(), event)

	select {
	case <-cancelInvoked:
		// expected: dispatch routed the event to handleDeploymentCancelRequest,
		// which matched the correlation ID and invoked the active cancel func.
	case <-time.After(time.Second):
		require.Fail(t,
			"DeploymentCancelRequestEvent MUST be routed by handleEvent's "+
				"type switch to handleDeploymentCancelRequest. A regression "+
				"that removed the cancel case would silently drop EVERY "+
				"scheduler timeout recovery — stuck deployments would stay "+
				"running forever (scheduler keeps publishing cancels but the "+
				"deployer no longer acts on them)")
	}
}
