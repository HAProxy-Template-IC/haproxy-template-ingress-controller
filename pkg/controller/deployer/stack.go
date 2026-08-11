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
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// DeployStack is the deploy-side component set, already wired together.
type DeployStack struct {
	Deployer     *Component
	Scheduler    *DeploymentScheduler
	DriftMonitor *DriftPreventionMonitor
}

// NewDeployStack builds the three deploy-side components and connects the
// scheduler's runtime-bypass path to the deployer's activation state and to the
// metrics registry.
//
// The wiring lives here, not in the caller, because both connections were once
// optional setters: omitting either compiled, ran, and failed silently. The
// activation half is the worse one — two writers to the same pod with different
// notions of what it is running is how a config goes parked unnoticed (#112) —
// and the metrics half leaves the fast-path counters reading as an idle fast
// path rather than a broken one. Inside this package the assignment needs no
// setter, so there is no longer an API that lets a caller forget.
//
// domainMetrics is required; the fast-path counters have no other source.
func NewDeployStack(
	eventBus *busevents.EventBus,
	cfg *coreconfig.Config,
	logger *slog.Logger,
	domainMetrics *metrics.Metrics,
) *DeployStack {
	deployer := New(eventBus, logger,
		cfg.Dataplane.GetReloadVerificationTimeout(),
		cfg.Dataplane.GetSyncTimeout(),
		domainMetrics)

	scheduler := NewDeploymentScheduler(eventBus, logger,
		cfg.Dataplane.GetMinDeploymentInterval(),
		cfg.Dataplane.GetDeploymentTimeout())

	// The bypass writes to the same pods the deployer syncs, so both must share
	// one view of what each pod is proven to be running.
	scheduler.runtimeBypass.recordActivation = deployer.RecordActivation
	scheduler.runtimeBypass.retainAuthorities = deployer.RetainEndpointAuthorities
	scheduler.runtimeBypass.recordFastPath = domainMetrics.RecordRuntimeFastPath

	return &DeployStack{
		Deployer:  deployer,
		Scheduler: scheduler,
		DriftMonitor: NewDriftPreventionMonitor(eventBus, logger,
			cfg.Dataplane.GetDriftPreventionInterval()),
	}
}
