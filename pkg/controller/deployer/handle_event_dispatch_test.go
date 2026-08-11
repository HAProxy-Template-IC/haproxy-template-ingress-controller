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
	bus.Start()
	c := createTestDeployer(bus)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go c.runCancellationLoop(ctx, done)
	t.Cleanup(func() {
		cancel()
		<-done
	})

	const deploymentID = "deployment-to-cancel"
	cancelInvoked, _ := installFakeDeployment(c, deploymentID)

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
