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

// maxValidatedPlans bounds the passed-plan set. Verdicts arrive for the newest
// render plus the superseded ones pods still run, and the gate retains only a
// handful of those, so a set this size covers every plan any pod can be on
// while a converged fleet uses one entry.
const maxValidatedPlans = 16

// validatedPlanSet remembers which plans the render gate passed, in arrival
// order, so a pod's manifest can name the plan THAT pod applied.
//
// The agent promotes its last-known-good set only when the manifest's
// validated plan equals the plan it has applied, so a single "newest validated"
// value would stall promotion on every pod that is not on that exact plan —
// and a verdict for a superseded plan (which the gate checks for the revert's
// sake) would push the value backwards. A set has neither problem: membership
// only grows, and nothing a pod already earned is taken away.
type validatedPlanSet struct {
	// order is oldest-first; newest is the last entry, which is what a pod
	// whose own plan has no verdict is told.
	order  []string
	member map[string]struct{}
}

func newValidatedPlanSet() *validatedPlanSet {
	return &validatedPlanSet{member: map[string]struct{}{}}
}

// add records a plan the gate passed. Re-recording one keeps its position: the
// order only tracks which plan is newest, and a re-verdict does not make a plan
// newer than renders that followed it.
func (s *validatedPlanSet) add(planID string) {
	if planID == "" {
		return
	}
	if _, known := s.member[planID]; known {
		return
	}
	s.member[planID] = struct{}{}
	s.order = append(s.order, planID)
	for len(s.order) > maxValidatedPlans {
		delete(s.member, s.order[0])
		s.order = s.order[1:]
	}
}

// resolve returns what a pod reporting appliedPlanID should be told: its own
// plan when that passed, otherwise the newest passed plan.
func (s *validatedPlanSet) resolve(appliedPlanID string) string {
	if _, passed := s.member[appliedPlanID]; passed {
		return appliedPlanID
	}
	if len(s.order) == 0 {
		return ""
	}
	return s.order[len(s.order)-1]
}
