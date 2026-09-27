// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestDeployerStandbyDoesNotBufferEventsAcrossLeadershipTerms(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)
	completed := bus.SubscribeTypes("standby-test-completed", 1, events.EventTypeDeploymentCompleted)
	bus.Start()
	for range 2 {
		for range EventBufferSize * 3 {
			bus.Publish(events.NewHAProxyPodsDiscoveredEvent(nil, 0))
			bus.Publish(events.NewRenderGateCompletedEvent("standby-plan", true, false, true, "", false, 0))
			bus.Publish(componentScheduledEvent(t, "global\n", "standby"))
			bus.Publish(events.NewDeploymentCancelRequestEvent("standby", "inactive term"))
		}
		require.Zero(t, bus.DroppedEventsCritical())
		ready := deployer.SubscriptionReady()
		ctx, cancel := context.WithTimeout(t.Context(), testutil.LongTimeout)
		t.Cleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- deployer.Start(ctx) }()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("deployer did not become ready")
		}
		bus.Publish(componentScheduledEvent(t, "global\n", "active term"))
		testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completed, testutil.LongTimeout)
		cancel()
		require.NoError(t, <-done)
	}
	require.Zero(t, bus.DroppedEventsCritical())
}
