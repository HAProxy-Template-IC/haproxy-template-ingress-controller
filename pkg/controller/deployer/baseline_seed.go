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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// seedBaseline gives a controller that has deployed nothing yet the plan the
// fleet is already running, read from the pods' own `/v1/state`. Without it the
// first render after a controller start describes servers no pod holds, and the
// slot table it computes would move every server on the next apply.
//
// It seeds at most once per leadership term, triggering a reconciliation
// because the baseline only reaches templates through a render. The one-shot is
// spent only once the fleet was actually reached: adopting a plan, or a
// non-empty set that every pod answered and none reported a plan (a genuinely
// fresh fleet — nothing to preserve). An empty pod set or a set where any read
// failed leaves the latch unset, so the next discovery retries — a term whose
// first discovery cannot reach the fleet must not forfeit the baseline for the
// rest of the term.
func (c *Component) seedBaseline(ctx context.Context, endpoints []dataplane.Endpoint) {
	if c.ackedPlans == nil || c.baselineSeeded.Load() {
		return
	}
	reached, failed := false, false
	for i := range endpoints {
		client, err := c.clients.For(&endpoints[i])
		if err != nil {
			failed = true
			continue
		}
		state, err := client.State(ctx, false)
		if err != nil {
			c.Logger().Debug("Pod did not answer the cold-start state read",
				"pod", endpoints[i].PodName, "error", err)
			failed = true
			continue
		}
		reached = true
		plan := c.plans.Baseline(state)
		if plan == nil {
			continue
		}
		c.Logger().Info("Adopted the fleet's running plan as the render baseline",
			"pod", endpoints[i].PodName, "plan", plan.ID)
		c.ackedPlans.SetAckedPlan(plan)
		c.baselineSeeded.Store(true)
		c.EventBus().Publish(events.NewReconciliationTriggeredEvent(
			"fleet_baseline_adopted", false, events.WithNewCorrelation()))
		return
	}
	// No pod reported a plan. Latch only if the whole set answered: a reachable
	// fleet that all reports no plan is fresh. An empty set, a client that
	// would not build, or any failed read means we could not tell — leave the
	// latch unset so the next discovery retries.
	if reached && !failed {
		c.baselineSeeded.Store(true)
	}
}
