// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployer

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

const maxValidatedPlans = 16

type planReference struct {
	id    string
	proof string
}

type validatedPlan struct {
	id         string
	occurrence *rendercycle.Occurrence
}

// validatedPlanSet retains the exact render occurrences the controller's
// HAProxy check passed. Agent-issued plan proofs remain the external ACK used
// to resolve which occurrence a pod actually carries.
type validatedPlanSet struct {
	order []validatedPlan
}

func newValidatedPlanSet() *validatedPlanSet {
	return &validatedPlanSet{}
}

func (s *validatedPlanSet) addOccurrence(occurrence *rendercycle.Occurrence) {
	identity, err := inspectOccurrence(occurrence)
	if err != nil || identity.planID == "" {
		return
	}
	for i := range s.order {
		if sameOccurrence(s.order[i].occurrence, occurrence) {
			return
		}
	}
	s.order = append(s.order, validatedPlan{id: identity.planID, occurrence: occurrence})
	if len(s.order) > maxValidatedPlans {
		s.order = s.order[len(s.order)-maxValidatedPlans:]
	}
}

func (s *validatedPlanSet) resolve(
	id, proof string,
	plan *renderplan.Plan,
	occurrence *rendercycle.Occurrence,
) planReference {
	if id == "" || proof == "" || !exactPlan(plan, plan) || plan.ID != id || occurrence == nil {
		return planReference{}
	}
	for i := range s.order {
		candidate := &s.order[i]
		if candidate.id == id && sameOccurrence(candidate.occurrence, occurrence) {
			return planReference{id: id, proof: proof}
		}
	}
	return planReference{}
}
