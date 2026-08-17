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

// DeployStack is the deploy-side component set, already wired together.
type DeployStack struct {
	Deployer     *Component
	Scheduler    *DeploymentScheduler
	DriftMonitor *DriftPreventionMonitor
}

// NewDeployStack builds the three deploy-side components and connects the
// scheduler's runtime-bypass path to the deployer's endpoint cache and to the
// metrics registry.
//
// The wiring lives here so both pod writers share one fenced observation.
//
// domainMetrics is required; the fast-path counters have no other source.
// ackedPlans may be nil; the renderer then keeps rendering from its own plans.
func NewDeployStack(
	eventBus *busevents.EventBus,
	cfg *coreconfig.Config,
	logger *slog.Logger,
	domainMetrics *metrics.Metrics,
	ackedPlans AckedPlanSink,
) *DeployStack {
	deployer := New(eventBus, logger,
		cfg.Dataplane.GetReloadVerificationTimeout(),
		cfg.Dataplane.GetSyncTimeout(),
		domainMetrics)

	scheduler := newDeploymentScheduler(eventBus, logger,
		cfg.Dataplane.GetMinDeploymentInterval(),
		cfg.Dataplane.GetDeploymentTimeout())

	deployer.ackedPlans = ackedPlans
	scheduler.runtimeBypass.ackedPlans = ackedPlans
	deployer.versionCache = scheduler.runtimeBypass.configCache
	scheduler.runtimeBypass.recordFastPath = domainMetrics.RecordRuntimeFastPath

	return &DeployStack{
		Deployer:  deployer,
		Scheduler: scheduler,
		DriftMonitor: NewDriftPreventionMonitor(eventBus, logger,
			cfg.Dataplane.GetDriftPreventionInterval()),
	}
}
