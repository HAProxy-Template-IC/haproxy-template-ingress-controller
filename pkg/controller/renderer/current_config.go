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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// rememberPlan keeps the newest reconcile plan so the next render can read its
// servers as `currentConfig`. Admission renders are proposals and must not
// displace the state the fleet was last rendered from.
func (s *RenderService) rememberPlan(mode rendercontext.RenderMode, plan *renderplan.Plan) {
	if mode != rendercontext.RenderModeReconcile || plan == nil {
		return
	}
	s.planMu.Lock()
	defer s.planMu.Unlock()
	s.lastPlan = plan
}

// currentConfig is what templates read as `currentConfig`: the servers of the
// last reconcile plan, filled in from the deployed HAProxyCfg for every backend
// the plan does not describe. Until the chart macros declare their backends
// (they do not yet), the plan contributes nothing and the store is the only
// source.
func (s *RenderService) currentConfig() *renderplan.CurrentConfig {
	var fromStore *renderplan.CurrentConfig
	if s.currentConfigStore != nil {
		fromStore = s.currentConfigStore.CurrentConfig()
	}

	s.planMu.Lock()
	plan := s.lastPlan
	s.planMu.Unlock()
	if plan == nil {
		return fromStore
	}

	current := plan.CurrentConfig()
	if fromStore == nil {
		return &current
	}
	for backend, servers := range fromStore.ServerIndex {
		if _, described := current.ServerIndex[backend]; !described {
			current.ServerIndex[backend] = servers
		}
	}
	return &current
}
