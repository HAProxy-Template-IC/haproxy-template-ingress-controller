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

package deployer

import (
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// AckedPlanSink receives the plan the fleet confirmed it is running. The
// renderer implements it; the deployer calls it after a deployment lands.
type AckedPlanSink interface {
	SetAckedPlan(*renderplan.Plan)
}

// RenderInputs is what the deploy side feeds back into the next render: the
// plan the fleet ACKed and the capabilities its HAProxy version supports.
// The renderer implements both.
type RenderInputs interface {
	AckedPlanSink
	FleetCapabilitiesSink
}

// DeployStack is the deploy-side component set, already wired together.
type DeployStack struct {
	Deployer     *Component
	Scheduler    *DeploymentScheduler
	DriftMonitor *DriftPreventionMonitor
}

// NewDeployStack builds the three deploy-side components and connects them to
// the metrics registry, the leadership fence and the render inputs.
//
// The wiring lives here so a caller cannot forget one of the connections.
// domainMetrics is required. renderInputs and fence may be nil: the renderer
// then keeps rendering from its own plans and the local HAProxy probe, and
// applies are fenced at epoch zero (single writer, leader election disabled).
func NewDeployStack(
	eventBus *busevents.EventBus,
	cfg *coreconfig.Config,
	logger *slog.Logger,
	domainMetrics *metrics.Metrics,
	renderInputs RenderInputs,
	fence LeadershipFence,
) *DeployStack {
	deployer := New(eventBus, logger, cfg.Dataplane.GetSyncTimeout(), domainMetrics)
	deployer.fence = fence

	scheduler := newDeploymentScheduler(eventBus, logger,
		cfg.Dataplane.GetMinDeploymentInterval(),
		cfg.Dataplane.GetDeploymentTimeout())

	if renderInputs != nil {
		deployer.ackedPlans = renderInputs
		scheduler.capabilities = renderInputs
	}

	return &DeployStack{
		Deployer:  deployer,
		Scheduler: scheduler,
		DriftMonitor: NewDriftPreventionMonitor(eventBus, logger,
			cfg.Dataplane.GetDriftPreventionInterval()),
	}
}
