// Copyright 2025 Philipp Hossner
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

package renderer

import (
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// SetAckedPlan records the plan the fleet confirmed it is running. Pods that
// disagree resolve to the newest ACK, which is what this call carries.
func (s *RenderService) SetAckedPlan(plan *renderplan.Plan) {
	if plan == nil {
		return
	}
	s.planMu.Lock()
	defer s.planMu.Unlock()
	s.ackedPlan = plan
}

// buildPlan turns the render into its plan and keeps it as the fallback
// current-config source until a pod ACKs one.
func (s *RenderService) buildPlan(registry *rendercontext.PlanRegistry, mode rendercontext.RenderMode, config string, aux *dataplane.AuxiliaryFiles) (*renderplan.Plan, error) {
	plan, err := registry.Plan(config, aux)
	if err != nil {
		return nil, fmt.Errorf("building the render plan: %w", err)
	}
	s.rememberPlan(mode, plan)
	return plan, nil
}

// rememberPlan keeps the newest reconcile plan as the fresh-install fallback:
// until the fleet ACKs a plan, the last render is the only description of what
// the pods were asked to run. Admission renders are proposals and must not
// displace it.
func (s *RenderService) rememberPlan(mode rendercontext.RenderMode, plan *renderplan.Plan) {
	if mode != rendercontext.RenderModeReconcile || plan == nil {
		return
	}
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if s.ackedPlan != nil {
		return
	}
	s.lastPlan = plan
}

// currentConfig is what templates read as `currentConfig`: the servers of the
// plan the fleet ACKed, or of the last reconcile render until one does. Nil
// before the first render of a fresh install, which is what a template that
// preserves server slots must treat as "nothing to preserve".
func (s *RenderService) currentConfig() *renderplan.CurrentConfig {
	s.planMu.Lock()
	plan := s.ackedPlan
	if plan == nil {
		plan = s.lastPlan
	}
	s.planMu.Unlock()
	if plan == nil {
		return nil
	}
	current := plan.CurrentConfig()
	return &current
}
