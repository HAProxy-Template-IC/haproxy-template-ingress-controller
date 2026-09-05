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

package renderer

import (
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type exactCycleDerivedResolverState struct {
	state           *incrementalRenderState
	bindingPlan     string
	authState       *incrementalRenderState
	authBindingPlan string
	seal            *exactCycleDerivedResolverState
}

func (s *exactCycleDerivedResolverState) ValidateExactCycleProtocolState() error {
	if s == nil || s.seal != s || s.state == nil || s.state != s.authState ||
		s.bindingPlan != s.authBindingPlan {
		return errors.New("exact cycle derived resolver state has invalid provenance")
	}
	return nil
}

func (s *exactCycleDerivedResolverState) SameExactCycleProtocolState(
	current templating.ExactCycleProtocolState,
) (bool, error) {
	if err := s.ValidateExactCycleProtocolState(); err != nil {
		return false, err
	}
	other, ok := current.(*exactCycleDerivedResolverState)
	if !ok {
		return false, nil
	}
	if err := other.ValidateExactCycleProtocolState(); err != nil {
		return false, err
	}
	return s.state == other.state && s.bindingPlan == other.bindingPlan, nil
}

func exactCycleBindingPlanState(plan *incrementalBindingPlan) string {
	encoded := incrementalOrderedTuple()
	for index := range plan.bindings {
		binding := &plan.bindings[index]
		encoded = append(encoded, incrementalOrderedTuple(
			binding.component,
			binding.source,
			string(binding.props),
		)...)
	}
	return string(encoded)
}

func (r *incrementalDerivedResourceResolver) ExactCycleProtocolState() (
	templating.ExactCycleProtocolState,
	error,
) {
	if r == nil || r.session == nil || r.session.state == nil || r.session.base == nil ||
		r.session.bindingPlan == nil || !r.session.bindingPlanExact || !r.session.cachePublicationEnabled {
		return nil, errors.New("incremental derived resolver has no exact authenticated session")
	}
	bindingPlan := exactCycleBindingPlanState(r.session.bindingPlan)
	state := &exactCycleDerivedResolverState{
		state:           r.session.state,
		bindingPlan:     bindingPlan,
		authState:       r.session.state,
		authBindingPlan: bindingPlan,
	}
	state.seal = state
	return state, nil
}
