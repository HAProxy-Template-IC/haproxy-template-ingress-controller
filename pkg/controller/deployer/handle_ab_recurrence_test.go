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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// abFleet drives the real scheduler and deployer against one reload-gated fake
// pod, one render at a time, so a test can reproduce the content-addressed churn
// the deployer sees under continuous change without the async deploy loop or the
// follow-up timers deciding the outcome.
type abFleet struct {
	s         *DeploymentScheduler
	component *Component
	agent     *agenttest.Agent
	ctx       context.Context
}

// newABFleet wires a scheduler, a deployer and one fake pod running
// haproxyVersion onto bus. The deploy loop and retry timers are left off: this
// harness drives every dispatch by hand so the scheduler's skip decision — not a
// follow-up's rescue — is what a convergence assertion measures.
func newABFleet(t *testing.T, bus *deployerBus, haproxyVersion string) *abFleet {
	t.Helper()
	agent := agenttest.New(t, agenttest.WithHAProxyInfo(api.HAProxyInfo{
		Version: haproxyVersion, FullVersion: haproxyVersion + "-1", WorkerPID: 100,
	}))
	component := createTestDeployer(bus.EventBus)
	s := newDeploymentScheduler(bus.EventBus, testutil.NewTestLogger(), testutil.NoEventTimeout, testutil.VeryLongTimeout)

	ctx := context.Background()
	s.ctx = ctx
	initLoopChannels(s)
	s.schedulerMutex.Lock()
	s.retryStopped = true
	s.schedulerMutex.Unlock()
	s.mu.Lock()
	s.currentEndpoints = []dataplane.Endpoint{agentEndpoint(agent, "haproxy-0")}
	s.hasValidConfig = true
	s.mu.Unlock()

	return &abFleet{s: s, component: component, agent: agent, ctx: ctx}
}

// deploy pushes one render through the scheduler's dispatch decision and, when it
// is not skipped, runs the whole deployment against the fake pod and feeds the
// completion back. It returns whether the render reached the fleet.
func (f *abFleet) deploy(t *testing.T, bus *deployerBus, plan *renderplan.Plan, config string, aux *dataplane.AuxiliaryFiles) bool {
	t.Helper()
	s := f.s
	s.mu.Lock()
	s.lastRenderedConfig = config
	s.lastAuxiliaryFiles = aux
	s.lastContentChecksum = "checksum-" + plan.ID
	s.lastRenderedPlan = plan
	s.lastRenderedPlanID = plan.ID
	s.mu.Unlock()

	s.dispatchRender(f.ctx, "corr-"+plan.ID, false, "config_validation")

	s.schedulerMutex.Lock()
	dep := s.state.pending
	s.state.pending = nil
	s.schedulerMutex.Unlock()
	if dep == nil {
		return false // the scheduler skipped this render as unchanged
	}

	event := s.newScheduledEvent(dep)
	deploymentID := event.EventID()
	s.schedulerMutex.Lock()
	s.state.deployInFlight = true
	s.state.activeDeploymentID = deploymentID
	s.state.activeCorrelationID = dep.correlationID
	s.schedulerMutex.Unlock()

	f.component.deployToEndpoints(f.ctx, func() {}, event, deploymentID)
	completed := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, bus.Events, testutil.LongTimeout)
	s.handleDeploymentCompleted(completed)
	return true
}

func (f *abFleet) mustDeploy(t *testing.T, bus *deployerBus, what string, plan *renderplan.Plan, config string, aux *dataplane.AuxiliaryFiles) {
	t.Helper()
	if !f.deploy(t, bus, plan, config, aux) {
		t.Fatalf("%s was skipped as unchanged but had to reach the fleet", what)
	}
}

// TestScheduler_RecurringDeleteConvergesOnReloadGatedFleet reproduces the A/B/A/B
// content-addressed churn a reload-gated fleet (HAProxy < 3.4) sees when a route
// is repeatedly added and removed: the add and the delete each hash to the same
// recurring plan every cycle. A paced deploy leaves the fleet mid-transition
// without advancing the "last deployed" checksum, so a recurring render whose
// hash matches that stale checksum must NOT be dismissed as unchanged — the
// fleet has moved past it. After each cycle ends on the delete, the fleet must
// run the delete plan, never the stale add.
func TestScheduler_RecurringDeleteConvergesOnReloadGatedFleet(t *testing.T) {
	bus := newTestBus(t)
	f := newABFleet(t, bus, "3.0.0")

	// Two recurring content-addressed plans: the add carries the cycled backend,
	// the delete drops it. On < 3.4 a backend add or remove is structural, so
	// every transition between them is reload-gated.
	planAdd, cfgAdd, auxAdd := renderWithBackends("plan-add", "be_anchor", "be_cycle")
	planDel, cfgDel, auxDel := renderWithBackends("plan-del", "be_anchor")

	// The fleet starts fully deployed on the delete plan (route absent): a full,
	// unpaced deploy that records "plan-del" as the last deployed config.
	f.mustDeploy(t, bus, "initial delete", planDel, cfgDel, auxDel)
	assert.Equal(t, "plan-del", f.agent.State().RunningPlanID, "fleet starts on the delete plan")

	const cycles = 3
	for cycle := 1; cycle <= cycles; cycle++ {
		// A reload just fired for the previous state, so the pod paces the next
		// reload: applied advances but running lags until the window opens.
		f.agent.SetReloadPending(true)

		// Add the route back: a paced, structural deploy. It leaves the fleet
		// mid-transition (applied=add, running=delete) and, because reloads are
		// pending, does not advance the last-deployed checksum.
		f.mustDeploy(t, bus, "add", planAdd, cfgAdd, auxAdd)
		assert.Equal(t, "plan-add", f.agent.State().AppliedPlanID, "the add landed on the pod")

		// Remove it again — the recurring delete. Its checksum equals the fleet's
		// stale last-deployed checksum from the initial delete, but the fleet is
		// no longer on the delete plan, so this deploy is needed and must run.
		f.mustDeploy(t, bus, "recurring delete", planDel, cfgDel, auxDel)

		// The paced reload fires: the pod runs whatever it last applied.
		f.agent.FirePendingReload()
		assert.Equalf(t, "plan-del", f.agent.State().RunningPlanID,
			"cycle %d: fleet must converge on the delete plan, not the stale add", cycle)
		assert.Equalf(t, "plan-del", f.agent.State().AppliedPlanID,
			"cycle %d: the delete plan must be the pod's applied baseline", cycle)
	}
}
