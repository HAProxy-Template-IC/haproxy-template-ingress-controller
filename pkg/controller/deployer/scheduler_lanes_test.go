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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// laneConfigBase is a minimal HAProxy config whose single server keeps all
// options in default-server and only address:port + enabled on the server line,
// so an address change is a pure runtime-eligible diff.
const laneConfigBase = `global

defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s

backend api
  default-server check
  server SRV_1 %s enabled
`

// laneStructuralExtra appended to laneConfigBase adds a brand-new backend, which
// the comparator emits as a structural (reload-inducing) op.
const laneStructuralExtra = `
backend api2
  default-server check
  server SRV_1 10.9.9.9:8080 enabled
`

// parseLaneConfig parses a config string into a *parser.StructuredConfig.
func parseLaneConfig(t *testing.T, raw string) *parser.StructuredConfig {
	t.Helper()
	p, err := parser.New()
	require.NoError(t, err)
	cfg, err := p.ParseFromString(raw)
	require.NoError(t, err)
	return cfg
}

// laneRenders returns (baseline, runtimeEligibleRender, structuralRender) — a
// baseline config, a pure address-change render (runtime-eligible vs baseline),
// and an added-backend render (structural vs baseline). It also asserts the
// classification so the cases below rest on a verified premise.
func laneRenders(t *testing.T) (baseline, runtime, structural *parser.StructuredConfig) {
	t.Helper()
	baseRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	runtimeRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")
	structuralRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080") + laneStructuralExtra

	baseline = parseLaneConfig(t, baseRaw)
	runtime = parseLaneConfig(t, runtimeRaw)
	structural = parseLaneConfig(t, structuralRaw)

	rtUpd, err := dataplane.ComputeRuntimeServerUpdates(baseline, runtime)
	require.NoError(t, err)
	require.True(t, rtUpd.IsRuntimeEligible(), "address-change render must classify runtime-raw")

	stUpd, err := dataplane.ComputeRuntimeServerUpdates(baseline, structural)
	require.NoError(t, err)
	require.False(t, stUpd.IsRuntimeEligible(), "added-backend render must classify structural")

	return baseline, runtime, structural
}

// recordingRuntimeSyncer records each SyncRuntimeFast apply onto applied (one per
// endpoint per applyRuntimeRaw call), so tests can detect that the runtime-raw
// lane fired and order it against published DeploymentScheduledEvents.
type recordingRuntimeSyncer struct {
	applied chan struct{}
}

func (r *recordingRuntimeSyncer) SyncRuntimeFast(_ context.Context, _ *dataplane.RuntimeServerUpdates, _ string, _ *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
	r.applied <- struct{}{}
	return &dataplane.SyncResult{Success: true, AppliedOperations: []dataplane.AppliedOperation{{}}}, nil
}

func (r *recordingRuntimeSyncer) Close() error { return nil }

// newLaneScheduler builds a running scheduler whose runtime bypass records each
// apply onto the returned channel. The DeploymentScheduledEvent subscription is
// also returned. ctx is cancelled by the test via the returned cancel.
func newLaneScheduler(t *testing.T, minInterval time.Duration) (
	s *DeploymentScheduler,
	scheduledCh <-chan busevents.Event,
	applied chan struct{},
	cancel context.CancelFunc,
) {
	t.Helper()
	bus := testutil.NewTestBus()
	scheduledCh = bus.SubscribeTypes("lane-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	s = NewDeploymentScheduler(bus, testutil.NewTestLogger(), minInterval, 30*time.Second)
	applied = make(chan struct{}, 16)
	s.runtimeBypass.newSyncer = func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &recordingRuntimeSyncer{applied: applied}, nil
	}

	ctx, c := context.WithCancel(context.Background())
	startLoopForTest(t, s, ctx)

	return s, scheduledCh, applied, c
}

func oneEndpoint() []dataplane.Endpoint {
	return []dataplane.Endpoint{{URL: "http://localhost:5555"}}
}

// Case 1: runtime-eligible, idle, interval elapsed → runtime-raw now (no
// DeploymentScheduledEvent, an inline applyRuntimeRaw instead).
func TestSchedulerLanes_Case1_RuntimeEligibleIdle_AppliesRuntimeRawNow(t *testing.T) {
	s, scheduledCh, applied, cancel := newLaneScheduler(t, 0)
	defer cancel()

	baseline, runtime, _ := laneRenders(t)

	// Seed the dispatch baseline so the next render diffs against it (not nil →
	// not cold-start). No deploy in flight, interval elapsed (minInterval=0).
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.schedulerMutex.Unlock()

	s.scheduleOrQueue(context.Background(), "runtime-config", nil, runtime, oneEndpoint(),
		"endpoint-change", "corr-1", nil, true, "")

	// The runtime-raw apply fires inline; NO DeploymentScheduledEvent is published.
	select {
	case <-applied:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runtime-raw apply did not fire for a runtime-eligible idle render")
	}
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)

	// deployInFlight must stay false (runtime-raw does not set it) and the
	// dispatch baseline advanced to the runtime render.
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	assert.False(t, s.state.deployInFlight, "runtime-raw must not set deployInFlight")
	assert.Equal(t, runtime, s.lastDispatchedParsed, "dispatch baseline advances on runtime-raw")
}

// Case 2 (headline): structural in flight → its DeploymentScheduledEvent fires;
// then after handleDeploymentCompleted the queued runtime-raw applyRuntimeRaw
// fires. Asserts the ordering: structural event BEFORE runtime apply.
func TestSchedulerLanes_Case2_RuntimeRawAfterInFlightStructuralCompletes(t *testing.T) {
	s, scheduledCh, applied, cancel := newLaneScheduler(t, 0)
	defer cancel()

	_, _, structural := laneRenders(t)

	// Dispatch a structural render first. Cold start (nil baseline) → structural,
	// which the loop publishes and marks in-flight.
	s.scheduleOrQueue(context.Background(), "structural-config", nil, structural, oneEndpoint(),
		"structural-change", "corr-structural", nil, true, "")
	sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "structural-config", sd.Config, "the structural deploy is published first")

	// While it is in flight, schedule a runtime-eligible render. Its diff must be
	// taken against the now-dispatched structural baseline (which added api2), so
	// the runtime render keeps api2 and only changes SRV_1's address — a pure
	// runtime-eligible diff. (The loop set lastDispatchedParsed = structural when
	// it dispatched the structural deploy above.)
	s.schedulerMutex.Lock()
	require.True(t, s.state.deployInFlight, "the structural deploy must be in flight")
	require.Equal(t, structural, s.lastDispatchedParsed, "the in-flight structural render is the diff baseline")
	s.schedulerMutex.Unlock()
	structuralPlusAddr := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)

	s.scheduleOrQueue(context.Background(), "runtime-config", nil, structuralPlusAddr, oneEndpoint(),
		"endpoint-change", "corr-runtime", nil, true, "")

	// The pending render must be classified runtime-raw (diff vs structural
	// baseline is the pure address change).
	s.schedulerMutex.Lock()
	require.NotNil(t, s.state.pending, "the runtime render must be enqueued behind the in-flight deploy")
	require.Equal(t, laneRuntimeRaw, s.state.pending.lane, "diff vs the in-flight structural render is runtime-eligible")
	s.schedulerMutex.Unlock()

	// The runtime-raw apply MUST NOT have fired yet — it waits for the in-flight
	// structural deploy to complete.
	select {
	case <-applied:
		t.Fatal("runtime-raw applied while a structural deploy was still in flight (must wait)")
	case <-time.After(100 * time.Millisecond):
		// Expected — still waiting.
	}

	// Complete the in-flight structural deploy. The loop's awaitCompletion
	// unblocks, it grabs the pending runtime-raw, and applies it inline.
	s.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 1, Succeeded: 1, DurationMs: 10,
	}))

	select {
	case <-applied:
		// Expected — runtime-raw fires AFTER the structural completion.
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runtime-raw apply did not fire after the in-flight structural deploy completed")
	}

	// No second DeploymentScheduledEvent (the runtime-raw lane does not publish).
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)
}

// Case 3: runtime-eligible, deploy finished but interval NOT reached → runtime-raw
// fires within ms (IGNORES the interval), NOT after minDeploymentInterval.
func TestSchedulerLanes_Case3_RuntimeRawIgnoresInterval(t *testing.T) {
	const interval = 5 * time.Second
	s, _, applied, cancel := newLaneScheduler(t, interval)
	defer cancel()

	baseline, runtime, _ := laneRenders(t)

	// A deploy just ended → a structural render here WOULD wait 5s. Seed the
	// dispatch baseline so the runtime render classifies runtime-raw.
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "runtime-config", nil, runtime, oneEndpoint(),
		"endpoint-change", "corr-3", nil, true, "")

	select {
	case <-applied:
		elapsed := time.Since(start)
		assert.Less(t, elapsed, time.Second,
			"runtime-raw must fire within ms, ignoring the %s deployment interval", interval)
	case <-time.After(2 * time.Second):
		t.Fatal("runtime-raw apply was (wrongly) gated by the deployment interval")
	}
}

// Case 4: structural, idle/elapsed → deploy now (DeploymentScheduledEvent fires).
func TestSchedulerLanes_Case4_StructuralIdle_DeploysNow(t *testing.T) {
	s, scheduledCh, _, cancel := newLaneScheduler(t, 0)
	defer cancel()

	_, _, structural := laneRenders(t)

	// Cold start (nil baseline) → structural; idle, interval elapsed.
	s.scheduleOrQueue(context.Background(), "structural-config", nil, structural, oneEndpoint(),
		"structural-change", "corr-4", nil, true, "")

	sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "structural-config", sd.Config)

	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	assert.True(t, s.state.deployInFlight, "structural deploy sets deployInFlight")
}

// Case 5: structural, deploy in progress → enqueue; a newer structural replaces
// the enqueued one (≤1 enqueued, latest-wins). While the first deploy is in
// flight the loop is parked in awaitCompletion, so the enqueued renders stay in
// the single pending slot regardless of the interval — interval=0 keeps the
// post-completion publish prompt and the assertions deterministic.
func TestSchedulerLanes_Case5_StructuralEnqueuedLatestWins(t *testing.T) {
	s, scheduledCh, _, cancel := newLaneScheduler(t, 0)
	defer cancel()

	_, _, structural := laneRenders(t)

	// First structural deploy: cold start → publishes immediately (lastDeployment
	// EndTime is zero, so no interval wait), marks in-flight.
	s.scheduleOrQueue(context.Background(), "structural-1", nil, structural, oneEndpoint(),
		"structural-1", "corr-5a", nil, true, "")
	sd1 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "structural-1", sd1.Config)

	s.schedulerMutex.Lock()
	require.True(t, s.state.deployInFlight, "first structural deploy must be in flight")
	s.schedulerMutex.Unlock()

	// While in flight, enqueue two more structural renders. Each diffs against the
	// dispatched baseline (structural-1's render, which has backends api+api2) and
	// adds ANOTHER new backend, so each remains structural relative to it.
	// Latest-wins: only the second stays pending.
	extraBackend := func(name string) string {
		return fmt.Sprintf("\nbackend %s\n  default-server check\n  server SRV_1 10.9.9.9:8080 enabled\n", name)
	}
	structuralB := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")+laneStructuralExtra+extraBackend("api3"))
	structuralC := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")+laneStructuralExtra+extraBackend("api4"))
	s.scheduleOrQueue(context.Background(), "structural-2", nil, structuralB, oneEndpoint(),
		"structural-2", "corr-5b", nil, true, "")
	s.scheduleOrQueue(context.Background(), "structural-3", nil, structuralC, oneEndpoint(),
		"structural-3", "corr-5c", nil, true, "")

	s.schedulerMutex.Lock()
	require.NotNil(t, s.state.pending, "a structural deploy must be enqueued")
	assert.Equal(t, laneStructural, s.state.pending.lane, "the enqueued deploy is structural")
	assert.Equal(t, "structural-3", s.state.pending.config, "latest-wins: only the newest enqueued structural survives")
	s.schedulerMutex.Unlock()

	// Complete the in-flight deploy; the loop's awaitCompletion unblocks and it
	// grabs the enqueued structural-3 as the next deploy (interval=0, so no wait).
	s.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 1, Succeeded: 1, DurationMs: 10,
	}))
	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.VeryLongTimeout)
	assert.Equal(t, "structural-3", sd2.Config, "the enqueued latest-wins structural deploys next")
}

// Case 6 (the rolling-restart fix): a STRUCTURAL render that ALSO carries a
// runtime-eligible server change — a pod-IP rotation coalesced with another
// tenant's structural change, the common shape under concurrent churn — applies
// that server change immediately via runtime-raw, BEFORE the structural reload
// waits out minDeploymentInterval. Without this, the pod-IP swap is trapped
// behind the (here 5s) interval, leaving HAProxy with no usable backend server
// for ~interval seconds → the rolling-restart 503s. The structural reload itself
// stays gated (no DeploymentScheduledEvent fires within the window).
func TestSchedulerLanes_Case6_StructuralWithRuntimeSubset_AppliesPreInterval(t *testing.T) {
	const interval = 5 * time.Second
	s, scheduledCh, applied, cancel := newLaneScheduler(t, interval)
	defer cancel()

	baseline, _, _ := laneRenders(t)

	// Mixed render vs baseline: SRV_1 address change (runtime-eligible) PLUS a
	// brand-new backend (structural). This is the shape a pod-IP rotation takes
	// when it coalesces with an unrelated structural change since last dispatch.
	mixed := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)

	upd, err := dataplane.ComputeRuntimeServerUpdates(baseline, mixed)
	require.NoError(t, err)
	require.False(t, upd.IsRuntimeEligible(), "mixed render must classify structural")
	require.Greater(t, upd.ServerOpCount(), 0, "mixed render must still carry a runtime-eligible server op")
	require.Greater(t, upd.StructuralOpCount(), 0, "mixed render must carry a structural op")

	// A deploy just ended → a structural deploy here is gated for the full 5s
	// interval. Seed the baseline so the render diffs against it (not cold-start).
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "mixed-config", nil, mixed, oneEndpoint(),
		"endpoint-change+churn", "corr-6", nil, true, "")

	// The runtime-eligible subset must apply within ms, IGNORING the 5s interval
	// that still gates the structural reload.
	select {
	case <-applied:
		assert.Less(t, time.Since(start), time.Second,
			"runtime subset of a structural render must apply pre-interval, not wait the %s interval", interval)
	case <-time.After(2 * time.Second):
		t.Fatal("runtime subset of a mixed render was (wrongly) gated by the deployment interval")
	}

	// The structural reload is still gated by the interval — no deploy published.
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)

	// The render stays pending (structural), to reload once the interval elapses.
	s.schedulerMutex.Lock()
	require.NotNil(t, s.state.pending, "the structural render stays pending for the gated reload")
	assert.Equal(t, laneStructural, s.state.pending.lane)
	s.schedulerMutex.Unlock()
}

// Case 7 (the residual fix): an endpoint change that arrives WHILE the scheduler
// is already sleeping out the interval for an earlier structural render must
// apply responsively — not wait for the in-progress interval to elapse. This is
// the second-order gap behind the residual rolling-restart 503: the new pod came
// up (applied), then mid-interval the old pod left rotation, and that second
// change waited out the remaining interval, leaving SRV_1 pointed at the dead pod
// long enough to burn the connect-retry budget.
func TestSchedulerLanes_Case7_MidIntervalRuntimeChange_AppliesResponsively(t *testing.T) {
	const interval = 5 * time.Second
	s, scheduledCh, applied, cancel := newLaneScheduler(t, interval)
	defer cancel()

	baseline, _, _ := laneRenders(t)
	render1 := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)
	render2 := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.3:8080")+laneStructuralExtra+
		"\nbackend api3\n  default-server check\n  server SRV_1 10.9.9.9:8080 enabled\n")

	// A deploy just ended → structural renders here are gated for the full 5s.
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	// First structural+runtime render → the loop enters the interval wait and
	// applies its runtime subset up front.
	s.scheduleOrQueue(context.Background(), "render1", nil, render1, oneEndpoint(),
		"r1", "corr-7a", nil, true, "")
	select {
	case <-applied:
	case <-time.After(2 * time.Second):
		t.Fatal("initial pre-interval apply did not fire")
	}

	// While the loop is still mid-interval (sleeping ~5s), a NEWER structural+
	// runtime render arrives — its runtime subset must apply responsively, NOT
	// wait out the remaining interval.
	start := time.Now()
	s.scheduleOrQueue(context.Background(), "render2", nil, render2, oneEndpoint(),
		"r2", "corr-7b", nil, true, "")
	select {
	case <-applied:
		assert.Less(t, time.Since(start), time.Second,
			"a runtime change arriving mid-interval must apply responsively, not wait the remaining interval")
	case <-time.After(2 * time.Second):
		t.Fatal("mid-interval runtime change was (wrongly) gated by the deployment interval")
	}

	// The structural reload is still gated — no deploy published within the window.
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)
}

// Case 8 (the residual fix's non-gated half): the SAME structural+runtime render
// as Case 6, but the last deploy ended long enough ago that no interval remains
// (wait<=0). The runtime-eligible subset must STILL fast-apply via runtime-raw
// here — this is the gap the fix closes. Before it, applyRuntimePreInterval ran
// only on the wait>0 path (after the `if wait <= 0 { return true }` short-circuit),
// so when the deploy was NOT interval-gated the pod-IP swap rode the structural
// reload's worker-swap window instead of reaching the live worker first → the
// residual rolling-restart 503. Unlike Case 6, the structural reload here is NOT
// gated, so it dispatches immediately (DeploymentScheduledEvent fires) — the
// subset apply must land BEFORE that reload.
func TestSchedulerLanes_Case8_StructuralWaitZero_AppliesRuntimeSubset(t *testing.T) {
	const interval = 5 * time.Second
	s, scheduledCh, applied, cancel := newLaneScheduler(t, interval)
	defer cancel()

	baseline, _, _ := laneRenders(t)

	// Same mixed render as Case 6: SRV_1 address change (runtime-eligible) PLUS a
	// brand-new backend (structural).
	mixed := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)

	upd, err := dataplane.ComputeRuntimeServerUpdates(baseline, mixed)
	require.NoError(t, err)
	require.False(t, upd.IsRuntimeEligible(), "mixed render must classify structural")
	require.Greater(t, upd.ServerOpCount(), 0, "mixed render must still carry a runtime-eligible server op")
	require.Greater(t, upd.StructuralOpCount(), 0, "mixed render must carry a structural op")

	// The last deploy ended 2 intervals ago → remainingInterval returns 0, so the
	// structural deploy is NOT gated (wait<=0). Seed the baseline so the render
	// diffs against it (not cold-start).
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.state.lastDeploymentEndTime = time.Now().Add(-2 * interval)
	s.schedulerMutex.Unlock()

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "mixed-config", nil, mixed, oneEndpoint(),
		"endpoint-change+churn", "corr-8", nil, true, "")

	// The runtime-eligible subset must apply within ms even though no interval
	// gates the deploy — proving the apply is no longer trapped behind the
	// wait<=0 short-circuit.
	select {
	case <-applied:
		assert.Less(t, time.Since(start), time.Second,
			"runtime subset of a non-gated structural render must still fast-apply (wait<=0 path)")
	case <-time.After(2 * time.Second):
		t.Fatal("runtime subset was not applied on the wait<=0 path — the fix regressed")
	}

	// The structural reload is NOT gated here (wait<=0) → it dispatches. This is
	// the distinguishing behaviour vs Case 6, where the same render's reload stays
	// gated for the full interval.
	testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, time.Second)
}

// TestScheduler_ApplyRuntimePreInterval_NilPendingNoPanic guards the
// leadership-loss race: handleLostLeadership clears s.state.pending to nil, but
// waitDeployInterval's select can still pick a buffered pendingSignal before it
// observes ctx.Done() and hand that nil pending to applyRuntimePreInterval. It
// must no-op rather than dereference dep.lane (which panicked before the guard).
func TestScheduler_ApplyRuntimePreInterval_NilPendingNoPanic(t *testing.T) {
	s := NewDeploymentScheduler(testutil.NewTestBus(), testutil.NewTestLogger(), 5*time.Second, 30*time.Second)
	require.NotPanics(t, func() {
		s.applyRuntimePreInterval(context.Background(), nil)
	})
}
