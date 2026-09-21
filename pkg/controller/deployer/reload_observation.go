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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// A reload follow-up observes an accepted plan; resending it can race the pacer.
func (c *Component) observeReload(attempt *podApply, authority string) *podOutcome {
	req, state := attempt.req, attempt.state
	if !req.observeReload || req.verify || attempt.full || attempt.resend ||
		state.AppliedToken.LeaderEpoch != req.token.LeaderEpoch ||
		state.InvariantViolation != "" || !state.HoldsAppliedPlan() ||
		!req.isPlan(c.plans.Baseline(authority, state)) || !measuredHoldsPlan(state, req.plan) {
		return nil
	}
	validated := req.validatedPlanFor(authority, state)
	if validated.id != "" && (validated.id != state.LKGPlanID || validated.proof != state.LKGPlanProof) {
		return nil
	}
	worker := c.plans.Plan(authority, state.WorkerOpsPlanID, state.WorkerOpsPlanProof)
	if worker == nil || (state.ReloadPendingAt == "" && !req.isPlan(worker)) {
		return nil
	}
	result := &api.ApplyResult{
		PlanID: req.planID, OK: true, Mode: api.ResultNoop,
		AppliedPlanID: state.AppliedPlanID, AppliedPlanProof: state.AppliedPlanProof,
		RunningPlanID: state.RunningPlanID, RunningPlanProof: state.RunningPlanProof,
		WorkerOpsPlanID: state.WorkerOpsPlanID, WorkerOpsPlanProof: state.WorkerOpsPlanProof,
		AppliedToken: state.AppliedToken,
		LKGPlanID:    state.LKGPlanID, LKGPlanProof: state.LKGPlanProof,
		HAProxy: state.HAProxy, At: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if state.ReloadPendingAt != "" {
		result.Mode = api.ResultScheduled
		result.Reload = &api.ReloadInfo{ScheduledAt: state.ReloadPendingAt}
	}
	return &podOutcome{result: result, converged: result.Mode != api.ResultScheduled, observed: true}
}
