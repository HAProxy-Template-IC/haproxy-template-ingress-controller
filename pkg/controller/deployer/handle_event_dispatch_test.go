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

func TestComponent_CancellationLoopRoutesRequest(t *testing.T) {
	bus := busevents.NewEventBus(10)
	c := createTestDeployer(bus)
	bus.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Start(ctx) }()
	<-c.SubscriptionReady()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	const deploymentID = "deployment-to-cancel"
	cancelInvoked, deploymentDone := installFakeDeployment(c, deploymentID)
	t.Cleanup(func() { close(deploymentDone) })

	event := events.NewDeploymentCancelRequestEvent(
		deploymentID,
		"scheduler_timeout",
		events.WithCorrelation("trace", deploymentID),
	)

	bus.Publish(event)

	select {
	case <-cancelInvoked:
	case <-time.After(time.Second):
		require.Fail(t,
			"the cancellation control loop did not dispatch the request")
	}
}
