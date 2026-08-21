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

package rendergate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// scriptedChecker answers each config with the verdict the test scripted for
// it, records the order it was asked in, and can hold one config's check open
// so a test can pile renders up behind a check in flight.
type scriptedChecker struct {
	mu       sync.Mutex
	verdicts map[string]error
	seen     []string
	entered  chan string
	hold     string
	release  chan struct{}
}

func newScriptedChecker() *scriptedChecker {
	return &scriptedChecker{
		verdicts: map[string]error{},
		entered:  make(chan string, 32),
		release:  make(chan struct{}),
	}
}

func (c *scriptedChecker) answer(config string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verdicts[config] = err
}

// holdOn makes the next check of config block until releaseHold is called.
func (c *scriptedChecker) holdOn(config string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hold = config
}

func (c *scriptedChecker) releaseHold() {
	close(c.release)
}

func (c *scriptedChecker) Check(ctx context.Context, config string, _ *dataplane.AuxiliaryFiles, _ string) error {
	c.mu.Lock()
	err := c.verdicts[config]
	c.seen = append(c.seen, config)
	held := c.hold == config
	c.mu.Unlock()
	c.entered <- config
	if held {
		select {
		case <-c.release:
		case <-ctx.Done():
		}
	}
	return err
}

func (c *scriptedChecker) checked() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}

// refusal is what HAProxy's own rejection looks like once it reaches the gate.
func refusal(message string) error {
	return fmt.Errorf("%w: %s", dataplane.ErrHAProxyRefused, message)
}

type gateHarness struct {
	bus       *busevents.EventBus
	verdicts  <-chan busevents.Event
	checker   *scriptedChecker
	metrics   *metrics.Metrics
	registry  *prometheus.Registry
	component *Component
}

func newGateHarness(t *testing.T) *gateHarness {
	t.Helper()
	bus := testutil.NewTestBus()
	verdicts := bus.SubscribeTypes("gate-test", 64, events.EventTypeRenderGateCompleted)
	registry := prometheus.NewRegistry()
	domainMetrics := metrics.NewMetrics(registry)
	checker := newScriptedChecker()
	component := New(&Config{
		EventBus: bus,
		Logger:   testutil.NewTestLogger(),
		Checker:  checker,
		Metrics:  domainMetrics,
	})
	bus.Start()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = component.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-component.SubscriptionReady():
	case <-time.After(testutil.LongTimeout):
		t.Fatal("render gate did not start")
	}

	return &gateHarness{
		bus: bus, verdicts: verdicts, checker: checker,
		metrics: domainMetrics, registry: registry, component: component,
	}
}

func (h *gateHarness) render(planID, config string) {
	h.bus.Publish(events.NewTemplateRenderedEvent(
		config, &dataplane.AuxiliaryFiles{}, nil, nil, 0, 1, "config_change",
		"checksum-"+planID, nil, planID, true,
		events.WithCorrelation("corr-"+planID, "cause-"+planID),
	))
}

func (h *gateHarness) verdict(t *testing.T) *events.RenderGateCompletedEvent {
	t.Helper()
	return testutil.WaitForEvent[*events.RenderGateCompletedEvent](t, h.verdicts, testutil.LongTimeout)
}

// awaitNewest blocks until the event loop has recorded planID as the render to
// check next. Publishing is asynchronous, so a test that releases a held check
// before the burst has landed would race its own setup.
func (h *gateHarness) awaitNewest(t *testing.T, planID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		h.component.mu.Lock()
		defer h.component.mu.Unlock()
		return h.component.newest != nil && h.component.newest.planID == planID
	}, testutil.LongTimeout, time.Millisecond)
}

// awaitAppliedPlan blocks until the gate knows some pod holds planID.
func (h *gateHarness) awaitAppliedPlan(t *testing.T, planID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		h.component.mu.Lock()
		defer h.component.mu.Unlock()
		for _, applied := range h.component.appliedByPod {
			if applied == planID {
				return true
			}
		}
		return false
	}, testutil.LongTimeout, time.Millisecond)
}

// The latch is the whole contract: a pass keeps the gate open, the first
// refusal closes it, a refusal while closed pins the fleet, and a pass reopens
// it. Only the second consecutive refusal reports Pinned, because only then is
// nothing new reaching the pods.
func TestRenderGate_LatchTransitions(t *testing.T) {
	h := newGateHarness(t)

	steps := []struct {
		name        string
		planID      string
		err         error
		wantOK      bool
		wantRefused bool
		wantPinned  bool
	}{
		{name: "optimistic pass", planID: "plan-1", wantOK: true},
		{name: "optimistic refusal closes the gate", planID: "plan-2", err: refusal("unknown keyword"), wantRefused: true},
		{name: "refusal while closed pins the fleet", planID: "plan-3", err: refusal("still broken"), wantRefused: true, wantPinned: true},
		{name: "pass reopens the gate", planID: "plan-4", wantOK: true},
		{name: "gate that cannot run closes it without a refusal", planID: "plan-5", err: errors.New("read-only file system")},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			config := "config-" + step.planID
			h.checker.answer(config, step.err)
			h.render(step.planID, config)

			verdict := h.verdict(t)
			assert.Equal(t, step.planID, verdict.PlanID)
			assert.Equal(t, step.wantOK, verdict.OK)
			assert.Equal(t, step.wantRefused, verdict.Refused,
				"only HAProxy's own verdict may revert the fleet, so it must be distinguishable")
			assert.Equal(t, step.wantPinned, verdict.Pinned)
		})
	}

	assert.Equal(t, float64(2), promtestutil.ToFloat64(h.metrics.ConfigRejectedTotal.WithLabelValues("haproxy")),
		"a gate that could not run is not a config rejection")
}

// The gauge tracks the pinned state so an alert can fire on it, and clears when
// a render passes.
func TestRenderGate_PinnedGaugeTracksTheLatch(t *testing.T) {
	h := newGateHarness(t)

	h.checker.answer("bad", refusal("boom"))
	h.render("plan-1", "bad")
	require.False(t, h.verdict(t).Pinned)
	assert.Equal(t, float64(0), promtestutil.ToFloat64(h.metrics.ConfigPinned))

	h.render("plan-2", "bad")
	require.True(t, h.verdict(t).Pinned)
	assert.Equal(t, float64(1), promtestutil.ToFloat64(h.metrics.ConfigPinned))

	h.checker.answer("good", nil)
	h.render("plan-3", "good")
	require.True(t, h.verdict(t).OK)
	assert.Equal(t, float64(0), promtestutil.ToFloat64(h.metrics.ConfigPinned))
}

// A burst of renders costs one check: the gate always validates the newest,
// never every intermediate one, and never the same plan twice.
func TestRenderGate_CoalescesToNewest(t *testing.T) {
	h := newGateHarness(t)

	// Hold the first check so the burst piles up behind it.
	h.checker.holdOn("config-plan-1")
	h.checker.answer("config-plan-1", nil)
	h.render("plan-1", "config-plan-1")
	<-h.checker.entered

	for _, planID := range []string{"plan-2", "plan-3", "plan-4"} {
		h.checker.answer("config-"+planID, nil)
		h.render(planID, "config-"+planID)
	}
	h.awaitNewest(t, "plan-4")
	h.checker.releaseHold()

	first := h.verdict(t)
	require.Equal(t, "plan-1", first.PlanID)
	second := h.verdict(t)
	assert.Equal(t, "plan-4", second.PlanID, "the gate validates the newest render, not the queue")

	testutil.AssertNoEvent[*events.RenderGateCompletedEvent](t, h.verdicts, testutil.NoEventTimeout)
	assert.Equal(t, []string{"config-plan-1", "config-plan-4"}, h.checker.checked())
}

// A superseded render some pod still runs is validated too: the fleet's
// exposure is what the pods hold, not what the newest render says.
func TestRenderGate_ValidatesSupersededPlansPodsStillRun(t *testing.T) {
	h := newGateHarness(t)

	h.checker.holdOn("config-plan-1")
	h.checker.answer("config-plan-1", nil)
	h.render("plan-1", "config-plan-1")
	<-h.checker.entered

	// plan-2 is superseded by plan-3 while the gate is busy, but a pod applied it.
	h.checker.answer("config-plan-2", refusal("plan-2 does not load"))
	h.checker.answer("config-plan-3", nil)
	h.render("plan-2", "config-plan-2")
	h.render("plan-3", "config-plan-3")
	h.bus.Publish(events.NewConfigAppliedToPodEvent(
		"rt-cfg", "haptic", "haproxy-0", "haptic", "uid-0", "runtime-0", "checksum", false,
		&events.SyncMetadata{AppliedPlanID: "plan-2"},
	))
	h.awaitNewest(t, "plan-3")
	h.awaitAppliedPlan(t, "plan-2")
	h.checker.releaseHold()

	seen := map[string]*events.RenderGateCompletedEvent{}
	for range 3 {
		verdict := h.verdict(t)
		seen[verdict.PlanID] = verdict
	}
	require.Contains(t, seen, "plan-3", "the newest render is always checked")
	require.Contains(t, seen, "plan-2", "a plan a pod still runs must be checked even once superseded")
	assert.False(t, seen["plan-2"].OK)
	assert.True(t, seen["plan-2"].Refused)
}

// A leadership change resets the latch: the new leader starts optimistic
// because the agents' own last-known-good set already protects the fleet.
func TestRenderGate_LeadershipChangeResetsTheLatch(t *testing.T) {
	h := newGateHarness(t)

	h.checker.answer("bad", refusal("boom"))
	h.render("plan-1", "bad")
	require.False(t, h.verdict(t).OK)

	h.component.reset()

	h.render("plan-2", "bad")
	verdict := h.verdict(t)
	assert.False(t, verdict.OK)
	assert.False(t, verdict.Pinned,
		"a fresh term's first refusal closes the gate, it does not report a fleet that was never pinned")
}

// Above the cap only renders no pod reports applied are evicted; one some pod
// still runs survives even when that keeps the set above the cap.
func TestRenderGate_TrimNeverEvictsAPlanAPodRuns(t *testing.T) {
	c := &Component{appliedByPod: map[string]string{}}
	for i := range maxRetainedRenders + 2 {
		planID := fmt.Sprintf("plan-%d", i)
		c.superseded = append(c.superseded, &render{planID: planID})
		c.appliedByPod[fmt.Sprintf("haptic/haproxy-%d", i)] = planID
	}

	c.trimSupersededLocked()
	require.Len(t, c.superseded, maxRetainedRenders+2,
		"every retained render is still some pod's applied plan")

	delete(c.appliedByPod, "haptic/haproxy-0")
	delete(c.appliedByPod, "haptic/haproxy-3")
	c.trimSupersededLocked()
	ids := make([]string, 0, len(c.superseded))
	for _, r := range c.superseded {
		ids = append(ids, r.planID)
	}
	assert.Equal(t, []string{"plan-1", "plan-2", "plan-4", "plan-5"}, ids,
		"the oldest non-running renders go first, running ones stay")
}

// A render without a plan is not gated: the deployer cannot apply it, and there
// would be nothing to revert to.
func TestRenderGate_IgnoresRendersWithoutAPlan(t *testing.T) {
	h := newGateHarness(t)

	h.bus.Publish(events.NewTemplateRenderedEvent(
		"config", &dataplane.AuxiliaryFiles{}, nil, nil, 0, 1, "", "checksum", nil, "", true))

	testutil.AssertNoEvent[*events.RenderGateCompletedEvent](t, h.verdicts, testutil.NoEventTimeout)
	assert.Empty(t, h.checker.checked())
}
