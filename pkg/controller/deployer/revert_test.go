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
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// applyModes lists the modes an agent was asked for, so a test can assert that
// a revert was or was not sent.
func applyModes(agent *agenttest.Agent) []string {
	applies := agent.Applies()
	modes := make([]string, 0, len(applies))
	for i := range applies {
		modes = append(modes, applies[i].Manifest.Mode)
	}
	return modes
}

// A pod that took the refused plan at runtime holds a file set its own HAProxy
// never loaded. That is exactly what the revert is for.
func TestRenderGateRefusal_RevertsThePodCarryingTheRefusedPlan(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	// plan-1 lands with a reload, is validated, and becomes the pod's LKG.
	plan1, config1, aux1 := renderFor("plan-1", "10.0.0.1", mapEntry)
	initial := deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
	validated, err := initial.RenderOccurrence()
	require.NoError(t, err)
	component.SetValidatedOccurrence(validated)
	deployTo(t, component, bus, plan1, config1, aux1, events.TriggerReasonDriftPrevention, endpoint)
	require.Equal(t, plan1.ID, agent.State().LKGPlanID)

	// plan-2 is a runtime-only change: the file set is on disk but no reload
	// has read it.
	plan2, config2, aux2 := renderFor("plan-2", "10.0.0.2", mapEntry)
	refused := deployTo(t, component, bus, plan2, config2, aux2, "config_validation", endpoint)
	state := agent.State()
	require.Equal(t, plan2.ID, state.AppliedPlanID)
	require.Equal(t, plan1.ID, state.RunningPlanID, "a runtime apply never advances the running plan")

	component.handleRenderGateCompleted(context.Background(),
		renderGateForCompletion(t, refused, false, true, "unknown keyword"))

	reverted := agent.State()
	assert.Equal(t, plan1.ID, reverted.AppliedPlanID, "the pod must be back on the plan HAProxy accepted")
	assert.Equal(t, plan1.ID, reverted.RunningPlanID)
	assert.Contains(t, applyModes(agent), api.ModeRevertLKG)
}

// A pod whose own binary reloaded the plan is stronger evidence than the
// controller image's `haproxy -c`. Reverting it would drop a config that
// demonstrably loads.
func TestRenderGateRefusal_LeavesAPodThatReloadedThePlanAlone(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	plan, config, aux := renderFor("plan-1", "10.0.0.1", mapEntry)
	applied := deployTo(t, component, bus, plan, config, aux, "config_validation", endpoint)
	require.Equal(t, plan.ID, agent.State().RunningPlanID, "the first apply reloads")

	before := len(agent.Applies())
	component.handleRenderGateCompleted(context.Background(),
		renderGateForCompletion(t, applied, false, true, "unknown keyword"))

	assert.Equal(t, plan.ID, agent.State().RunningPlanID)
	assert.NotContains(t, applyModes(agent)[before:], api.ModeRevertLKG,
		"a pod whose own HAProxy loaded the plan must not be reverted")
}

// A gate that could not run says nothing about the config the pods hold, so it
// must not undo a working fleet.
func TestRenderGateFailure_WithoutAHAProxyVerdictNeverReverts(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	plan1, config1, aux1 := renderFor("plan-1", "10.0.0.1", mapEntry)
	deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
	plan2, config2, aux2 := renderFor("plan-2", "10.0.0.2", mapEntry)
	refused := deployTo(t, component, bus, plan2, config2, aux2, "config_validation", endpoint)

	before := len(agent.Applies())
	component.handleRenderGateCompleted(context.Background(),
		renderGateForCompletion(t, refused, false, false, "read-only file system"))

	assert.Len(t, agent.Applies(), before, "an unavailable gate must not touch the fleet")
	assert.Equal(t, plan2.ID, agent.State().AppliedPlanID)
}

// The revert is scoped: only the pod that actually carries the refused plan is
// asked to roll back.
func TestRenderGateRefusal_RevertsOnlyTheAffectedPods(t *testing.T) {
	carrying := agenttest.New(t)
	clean := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	carryingEndpoint := agentEndpoint(carrying, "haproxy-0")
	cleanEndpoint := agentEndpoint(clean, "haproxy-1")

	plan1, config1, aux1 := renderFor("plan-1", "10.0.0.1", mapEntry)
	initial := deployTo(t, component, bus, plan1, config1, aux1, "config_validation", carryingEndpoint, cleanEndpoint)
	validated, err := initial.RenderOccurrence()
	require.NoError(t, err)
	component.SetValidatedOccurrence(validated)
	deployTo(t, component, bus, plan1, config1, aux1, events.TriggerReasonDriftPrevention,
		carryingEndpoint, cleanEndpoint)

	// Only the first pod is given plan-2.
	plan2, config2, aux2 := renderFor("plan-2", "10.0.0.2", mapEntry)
	refused := deployTo(t, component, bus, plan2, config2, aux2, "config_validation", carryingEndpoint)

	// The fleet the revert searches is the last one deployed to; put both pods
	// back in it so the untouched pod is visited and deliberately skipped.
	component.recordFleet([]dataplane.Endpoint{carryingEndpoint, cleanEndpoint})

	cleanBefore := len(clean.Applies())
	component.handleRenderGateCompleted(context.Background(),
		renderGateForCompletion(t, refused, false, true, "unknown keyword"))

	assert.Equal(t, plan1.ID, carrying.State().AppliedPlanID, "the carrying pod reverts")
	assert.Len(t, clean.Applies(), cleanBefore, "a pod that never got the plan is not touched")
}

// A pass names the plan agents may promote their rollback baseline to; the next
// apply carries it.
func TestRenderGatePass_NamesTheValidatedPlan(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	plan, config, aux := renderFor("plan-1", "10.0.0.1", mapEntry)
	applied := deployTo(t, component, bus, plan, config, aux, "config_validation", endpoint)
	require.Empty(t, agent.Applies()[0].Manifest.ValidatedPlanID,
		"nothing is named validated before the gate answers")

	component.handleRenderGateCompleted(context.Background(),
		renderGateForCompletion(t, applied, true, false, ""))
	deployTo(t, component, bus, plan, config, aux, events.TriggerReasonDriftPrevention, endpoint)

	applies := agent.Applies()
	assert.Equal(t, plan.ID, applies[len(applies)-1].Manifest.ValidatedPlanID,
		"the drift apply carries the validated plan, so LKG promotion never waits longer than one interval")
	assert.Equal(t, plan.ID, agent.State().LKGPlanID)
}
