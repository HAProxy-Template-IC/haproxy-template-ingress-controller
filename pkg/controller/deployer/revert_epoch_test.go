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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// A 409 naming a newer leader epoch is not a failed pod: this controller is no
// longer the fleet's writer, and the leader that is runs its own revert.
func TestRenderGateRefusal_StaleEpochStandsTheRevertDown(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	plan1, config1, aux1 := renderFor("plan-1", "10.0.0.1", mapEntry)
	deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
	component.SetValidatedPlan(plan1.ID)
	deployTo(t, component, bus, plan1, config1, aux1, events.TriggerReasonDriftPrevention, endpoint)

	plan2, config2, aux2 := renderFor("plan-2", "10.0.0.2", mapEntry)
	deployTo(t, component, bus, plan2, config2, aux2, "config_validation", endpoint)

	agent.ConflictOnce(conflictStaleEpoch)
	outcome := component.revertPod(context.Background(), &endpoint, plan2.ID,
		api.Token{LeaderEpoch: component.leaderEpoch(), RenderSeq: component.nextRenderSeq()})

	assert.Equal(t, revertStoodDown, outcome)
	assert.Equal(t, plan2.ID, agent.State().AppliedPlanID, "a stood-down revert writes nothing")
}
