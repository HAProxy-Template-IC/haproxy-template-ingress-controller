// Copyright 2026 Philipp Hossner
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
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// deployerBus is a started event bus with one subscription, which is what a
// deployment's assertions read.
type deployerBus struct {
	*busevents.EventBus
	Events <-chan busevents.Event
}

func newTestBus(t *testing.T) *deployerBus {
	t.Helper()
	bus := testutil.NewTestBus()
	published := bus.Subscribe("deployer-test", 200)
	bus.Start()
	return &deployerBus{EventBus: bus, Events: published}
}

// oneEndpoint is the single-pod fleet the scheduler tests dispatch against.
func oneEndpoint() []dataplane.Endpoint {
	return []dataplane.Endpoint{{URL: "http://localhost:5555", PodName: "haproxy-0", PodUID: "uid-0"}}
}

// depFor is a pending deployment targeting these endpoints.
func depFor(endpoints []dataplane.Endpoint) *scheduledDeployment {
	return &scheduledDeployment{config: "config", endpoints: endpoints, reason: "pod_discovery"}
}

// scheduledEvent builds the deploy event the per-pod handlers read their
// target identity, checksum and correlation from.
func scheduledEvent(runtimeConfigName, runtimeConfigNamespace, correlationID string) *events.DeploymentScheduledEvent {
	return events.NewDeploymentScheduledEvent(
		"config", nil, oneEndpoint(),
		runtimeConfigName, runtimeConfigNamespace, "config_validation", "checksum-abc",
		nil, "", nil, true,
		events.WithCorrelation(correlationID, correlationID))
}
