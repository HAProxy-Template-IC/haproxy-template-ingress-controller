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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// planWithServer builds a plan whose single backend holds one server, so
// currentConfig() can be told apart by the address it projects.
func planWithServer(id, address string) *renderplan.Plan {
	return &renderplan.Plan{
		ID: id,
		Backends: map[string]renderplan.Backend{
			"be_app": {
				Name:    "be_app",
				Servers: []renderplan.Server{{Name: "srv1", Address: address, Port: 8080}},
			},
		},
	}
}

func serverAddress(t *testing.T, current *renderplan.CurrentConfig) string {
	t.Helper()
	require.NotNil(t, current)
	servers, ok := current.ServerIndex["be_app"]
	require.True(t, ok, "the plan's backend must appear in currentConfig")
	return servers["srv1"].Address
}

func TestCurrentConfig_RenderTimePlanIsTheFreshInstallFallback(t *testing.T) {
	service := &RenderService{}

	service.rememberPlan(rendercontext.RenderModeReconcile, planWithServer("plan-1", "10.0.0.1"))

	assert.Equal(t, "10.0.0.1", serverAddress(t, service.currentConfig()),
		"until the fleet ACKs a plan, the last reconcile render is the only "+
			"description of what the pods were asked to run")
}

func TestCurrentConfig_AckedPlanOutranksTheRenderTimeFallback(t *testing.T) {
	service := &RenderService{}
	service.rememberPlan(rendercontext.RenderModeReconcile, planWithServer("plan-1", "10.0.0.1"))

	service.SetAckedPlan(planWithServer("plan-2", "10.0.0.2"))

	assert.Equal(t, "10.0.0.2", serverAddress(t, service.currentConfig()),
		"the ACKed plan is what the fleet runs; the render-time plan is only a proposal")
}

func TestCurrentConfig_RenderTimeFallbackStopsAfterTheFirstAck(t *testing.T) {
	service := &RenderService{}
	service.SetAckedPlan(planWithServer("plan-acked", "10.0.0.2"))

	service.rememberPlan(rendercontext.RenderModeReconcile, planWithServer("plan-newer", "10.0.0.3"))

	assert.Equal(t, "10.0.0.2", serverAddress(t, service.currentConfig()),
		"a render that no pod has taken must not displace the fleet's ACK")
}

func TestCurrentConfig_AdmissionRenderNeverBecomesTheFallback(t *testing.T) {
	service := &RenderService{}
	service.rememberPlan(rendercontext.RenderModeReconcile, planWithServer("plan-1", "10.0.0.1"))

	service.rememberPlan(rendercontext.RenderModeAdmission, planWithServer("proposal", "203.0.113.9"))

	assert.Equal(t, "10.0.0.1", serverAddress(t, service.currentConfig()),
		"admission renders are proposals, not fleet state")
}

func TestCurrentConfig_SetAckedPlanIgnoresNil(t *testing.T) {
	service := &RenderService{}
	service.rememberPlan(rendercontext.RenderModeReconcile, planWithServer("plan-1", "10.0.0.1"))

	service.SetAckedPlan(nil)

	assert.Equal(t, "10.0.0.1", serverAddress(t, service.currentConfig()))
}

func TestCurrentConfig_NoPlanAndNoStoreYieldsNothing(t *testing.T) {
	service := &RenderService{}

	assert.Nil(t, service.currentConfig())
}
