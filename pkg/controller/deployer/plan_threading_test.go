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
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// recordingPlanSink captures what the deployer reports the fleet is running,
// and what capabilities the fleet's HAProxy version supports.
type recordingPlanSink struct {
	plans        []*renderplan.Plan
	capabilities []dataplane.Capabilities
}

func (s *recordingPlanSink) SetAckedPlan(plan *renderplan.Plan) {
	s.plans = append(s.plans, plan)
}

func (s *recordingPlanSink) SetCapabilities(capabilities dataplane.Capabilities) {
	s.capabilities = append(s.capabilities, capabilities)
}

func renderedEventWithPlan(config string, plan *renderplan.Plan) *events.TemplateRenderedEvent {
	planID := ""
	if plan != nil {
		planID = plan.ID
	}
	return events.NewTemplateRenderedEvent(
		config,
		&dataplane.AuxiliaryFiles{},
		nil, // statusPatches
		nil, // renderedResources
		0,   // auxFileCount
		1,   // durationMs
		"",  // triggerReason
		"checksum-"+config,
		plan,
		planID,
		true, // coalescible
	)
}

// scheduleFromRender routes a render and its matching verdict through the
// scheduler and returns the deployment it parked (no deploy loop runs here).
func scheduleFromRender(t *testing.T, rendered *events.TemplateRenderedEvent) *scheduledDeployment {
	t.Helper()

	bus := testutil.NewTestBus()
	bus.Start()
	ctx := context.Background()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	scheduler.ctx = ctx
	scheduler.handleEvent(ctx, events.NewHAProxyPodsDiscoveredEvent(
		[]dataplane.Endpoint{{URL: "http://127.0.0.1:5555", PodName: "haproxy-1"}}, 1))
	scheduler.handleEvent(ctx, rendered)
	scheduler.handleEvent(ctx, events.NewValidationCompletedEvent(nil, 10, "", nil, true,
		events.PropagateCorrelation(rendered)))

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	require.NotNil(t, scheduler.state.pending, "the validated render must be scheduled")
	return scheduler.state.pending
}

func TestScheduler_PlanTravelsWithTheRenderItDescribes(t *testing.T) {
	plan := &renderplan.Plan{ID: "plan-abc"}

	pending := scheduleFromRender(t, renderedEventWithPlan("global\n  daemon\n", plan))

	assert.Same(t, plan, pending.plan,
		"the plan must travel with the config it describes, like the content checksum")
	assert.Equal(t, "plan-abc", pending.planID)
}

func TestScheduler_ScheduledEventCarriesThePlan(t *testing.T) {
	plan := &renderplan.Plan{ID: "plan-abc"}
	bus := testutil.NewTestBus()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	event := scheduler.newScheduledEvent(&scheduledDeployment{
		config: "global\n  daemon\n",
		plan:   plan,
		planID: plan.ID,
	})

	assert.Same(t, plan, event.Plan)
	assert.Equal(t, "plan-abc", event.PlanID)
}

func TestScheduler_RenderWithoutAPlanSchedulesNothingNil(t *testing.T) {
	pending := scheduleFromRender(t, renderedEventWithPlan("global\n  daemon\n", nil))

	assert.Nil(t, pending.plan, "a planless render must schedule, carrying no plan")
	assert.Empty(t, pending.planID)
}

func TestRecordFleetAck_ReportsThePlanAfterOnePodTookIt(t *testing.T) {
	sink := &recordingPlanSink{}
	component := createTestDeployer(testutil.NewTestBus())
	component.ackedPlans = sink
	plan := &renderplan.Plan{ID: "plan-abc"}

	component.recordFleetAck(plan, 1)

	require.Len(t, sink.plans, 1)
	assert.Same(t, plan, sink.plans[0])
}

func TestRecordFleetAck_StaysSilentWhenNothingLanded(t *testing.T) {
	sink := &recordingPlanSink{}
	component := createTestDeployer(testutil.NewTestBus())
	component.ackedPlans = sink

	component.recordFleetAck(&renderplan.Plan{ID: "plan-abc"}, 0)
	component.recordFleetAck(nil, 3)

	assert.Empty(t, sink.plans,
		"a deploy that reached no pod says nothing about what the fleet runs")
}

func TestDeployToEndpoints_UnreachablePodAcksNothing(t *testing.T) {
	sink := &recordingPlanSink{}
	bus := testutil.NewTestBus()
	bus.Start()
	component := createTestDeployer(bus)
	component.ackedPlans = sink

	// Port 1 refuses connections, so every pod's state read fails.
	plan := &renderplan.Plan{ID: "plan-abc"}
	event := events.NewDeploymentScheduledEvent("global\n  daemon\n", nil, nil,
		[]dataplane.Endpoint{{URL: "http://127.0.0.1:1", PodName: "haproxy-1"}},
		"", "", "test", "checksum", plan, plan.ID, nil, true)
	component.deployToEndpoints(context.Background(), func() {}, event, "deployment-1")

	assert.Empty(t, sink.plans, "a failed deployment must not claim the fleet runs the plan")
}

func TestNewDeployStack_WiresTheAckedPlanSink(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	sink := &recordingPlanSink{}

	stack := NewDeployStack(bus, &coreconfig.Config{}, logger,
		metrics.NewMetrics(prometheus.NewRegistry()), sink, nil)

	assert.Same(t, sink, stack.Deployer.ackedPlans,
		"without the sink the renderer keeps rendering from its own plans forever")
	assert.Same(t, sink, stack.Scheduler.capabilities,
		"without the sink the renderer keeps rendering against the controller image's own HAProxy")
}
