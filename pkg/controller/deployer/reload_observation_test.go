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
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

func TestApply_ReloadFollowUpObservesWithoutResending(t *testing.T) {
	for _, when := range []string{"pending", "completed", "during_state_read"} {
		t.Run(when, func(t *testing.T) {
			agent := agenttest.New(t)
			target, err := url.Parse(agent.URL())
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(target)
			var reloadDuringRead atomic.Bool
			proxy.ModifyResponse = func(response *http.Response) error {
				if response.Request.URL.Path == "/v1/state" && reloadDuringRead.CompareAndSwap(true, false) {
					agent.FirePendingReload()
				}
				return nil
			}
			server := httptest.NewServer(proxy)
			t.Cleanup(server.Close)
			endpoint := agentEndpoint(agent, "haproxy-0")
			endpoint.URL = server.URL
			bus := newTestBus(t)
			component := createTestDeployer(bus.EventBus)

			plan1, config1, aux1 := renderFor("initial", "10.0.0.1", mapEntry)
			deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
			agent.SetReloadPending(true)
			plan2, config2, aux2 := renderFor("next", "10.0.0.2", mapEntry)
			completed := deployTo(t, component, bus, plan2, config2, aux2, "config_validation", endpoint)
			require.Equal(t, 1, completed.PendingReloads)
			require.Len(t, agent.Applies(), 2)
			awaitStoredPlan(t, agent, plan2.ID)
			if when == "completed" {
				agent.FirePendingReload()
			}
			reloadDuringRead.Store(when == "during_state_read")
			reads := agent.StateReads()
			token := agent.State().AppliedToken

			completed = deployTo(t, component, bus, plan2, config2, aux2, pendingReloadFollowUpReason, endpoint)
			require.Zero(t, completed.Failed)
			require.Len(t, agent.Applies(), 2, "observation must not issue an apply across the reload")
			assert.Equal(t, reads+1, agent.StateReads())
			assert.Equal(t, token, agent.State().AppliedToken)
			assert.Zero(t, completed.ReloadsTriggered)
			if when == "completed" {
				assert.Equal(t, 1, completed.Succeeded)
				assert.Zero(t, completed.PendingReloads)
			} else {
				assert.Equal(t, 1, completed.PendingReloads)
				assert.Zero(t, completed.Succeeded)
				agent.FirePendingReload()
				completed = deployTo(t, component, bus, plan2, config2, aux2, pendingReloadFollowUpReason, endpoint)
				assert.Equal(t, 1, completed.Succeeded)
				assert.Zero(t, completed.PendingReloads)
				require.Len(t, agent.Applies(), 2)
			}
		})
	}
}

func TestApply_ReloadFollowUpAppliesChangedRender(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")
	plan1, config1, aux1 := renderFor("initial", "10.0.0.1", mapEntry)
	deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
	agent.SetReloadPending(true)

	plan2, config2, aux2 := renderFor("next", "10.0.0.2", mapEntry)
	completed := deployTo(t, component, bus, plan2, config2, aux2, pendingReloadFollowUpReason, endpoint)
	require.Zero(t, completed.Failed)
	require.Len(t, agent.Applies(), 2)
	assert.Equal(t, []string{api.OpServerSetAddr}, opKinds(agent.Applies()[1].Manifest.InPlaceOps))
	assert.Equal(t, plan2.ID, agent.State().AppliedPlanID)
}

func TestObserveReloadRequiresAcceptedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*podApply)
	}{
		{"ordinary deployment", func(a *podApply) { a.req.observeReload = false }},
		{"drift verification", func(a *podApply) { a.req.verify = true }},
		{"full recovery", func(a *podApply) { a.full = true }},
		{"baseline conflict", func(a *podApply) { a.resend = true }},
		{"different leader", func(a *podApply) { a.state.AppliedToken.LeaderEpoch++ }},
		{"invariant violation", func(a *podApply) { a.state.InvariantViolation = "tree" }},
		{"missing plan blob", func(a *podApply) { a.state.AppliedPlanStored = false }},
		{"unbound applied proof", func(a *podApply) { a.state.AppliedPlanProof = "unknown" }},
		{"unbound worker proof", func(a *podApply) { a.state.WorkerOpsPlanProof = "unknown" }},
		{"missing files", func(a *podApply) { clear(a.state.Files) }},
		{"changed file", func(a *podApply) { a.state.Files["haproxy.cfg"] = api.FileAt{Digest: "different"} }},
		{"unpromoted validation", func(a *podApply) {
			a.req.validatedPlanFor = func(string, *api.State) planReference {
				return planReference{id: a.state.AppliedPlanID, proof: a.state.AppliedPlanProof}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := newTestBus(t)
			component := createTestDeployer(bus.EventBus)
			plan, _, _ := renderFor("plan", "10.0.0.1", mapEntry)
			require.True(t, component.plans.Bind("pod", plan.ID, "applied", plan))
			state := &api.State{
				AppliedPlanID: plan.ID, AppliedPlanProof: "applied",
				WorkerOpsPlanID: plan.ID, WorkerOpsPlanProof: "applied",
				AppliedPlanStored: true, Files: map[string]api.FileAt{},
			}
			for _, file := range plan.Files {
				state.Files[file.Path] = api.FileAt{Digest: file.Digest, Size: file.Size}
			}
			attempt := &podApply{state: state, req: &deployRequest{
				plan: plan, planID: plan.ID, observeReload: true,
				validatedPlanFor: func(string, *api.State) planReference { return planReference{} },
			}}
			require.NotNil(t, component.observeReload(attempt, "pod"))
			tt.change(attempt)
			assert.Nil(t, component.observeReload(attempt, "pod"))
		})
	}
}
