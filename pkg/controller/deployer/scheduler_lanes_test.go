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
	"errors"
	"fmt"
	"sync"
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

// laneTwoServers carries two servers so a diff can mix a runtime-eligible field
// change on one with a reload-only change on the other.
const laneTwoServers = `global

defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s

backend api
  default-server check
  server SRV_1 %s enabled
  server SRV_2 10.0.1.1:8080 enabled%s
`

// A reload-only server change riding along with a runtime-eligible one must
// classify the whole diff as structural. DiffSummary.StructuralOperations()
// subtracts EVERY modified server, so it reported 0 here and the deployer took
// the runtime-raw lane: the full render was written to disk with skip_reload,
// only the eligible field reached the live worker, and the render was recorded
// as activated — leaving disk and memory permanently divergent.
func TestSchedulerLanes_MixedServerFields_ClassifyStructural(t *testing.T) {
	baseline := parseLaneConfig(t, fmt.Sprintf(laneTwoServers, "10.0.0.1:8080", ""))
	// SRV_1's address is runtime-eligible; SRV_2 gaining `ssl verify none` is not.
	mixed := parseLaneConfig(t, fmt.Sprintf(laneTwoServers, "10.0.0.2:8080", " ssl verify none"))

	upd, err := dataplane.ComputeRuntimeServerUpdates(baseline, mixed)
	require.NoError(t, err)

	require.Positive(t, upd.ServerOpCount(), "SRV_1's address change is runtime-eligible")
	require.Positive(t, upd.StructuralOpCount(), "SRV_2's ssl change needs a reload")
	require.False(t, upd.IsRuntimeEligible(),
		"a diff carrying any reload-only change must not take the runtime-raw lane")
}

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

	s = newDeploymentScheduler(bus, testutil.NewTestLogger(), minInterval, 30*time.Second)
	s.lastDispatchedPodSetHash = computePodSetHash(oneEndpoint())
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

func TestSchedulerLanes_SameURLReplacementForcesStructural(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	baseline, _, _ := laneRenders(t)
	oldEndpoint := dataplane.Endpoint{URL: "http://localhost:5555", PodName: "haproxy-0", PodUID: "uid-old"}
	replacement := oldEndpoint
	replacement.PodUID = "uid-new"

	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = "config"
	s.lastDispatchedPodSetHash = computePodSetHash([]dataplane.Endpoint{oldEndpoint})
	s.schedulerMutex.Unlock()
	s.scheduleOrQueue(t.Context(), "config", nil, baseline, []dataplane.Endpoint{replacement},
		"pod_discovery", "replacement", nil, true, "checksum", nil, "")

	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	require.NotNil(t, s.state.pending)
	assert.Equal(t, laneStructural, s.state.pending.lane)
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
		"endpoint-change", "corr-1", nil, true, "", nil, "")

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

func TestSchedulerLanes_IncompleteRuntimeApplyFallsBackToStructural(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	baseline, runtime, _ := laneRenders(t)
	endpoints := oneEndpoint()

	s.runtimeBypass.replaceEndpointAuthorities(endpoints)
	s.runtimeBypass.newSyncer = func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return nil, errors.New("dial refused")
	}
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.lastDispatchedPodSetHash = computePodSetHash(endpoints)
	s.lastActivatedConfig = s.lastDispatchedConfig
	s.schedulerMutex.Unlock()

	dep := &scheduledDeployment{
		config:       fmt.Sprintf(laneConfigBase, "10.0.0.2:8080"),
		parsedConfig: runtime,
		endpoints:    endpoints,
		lane:         laneRuntimeRaw,
	}
	require.True(t, s.dispatchPending(t.Context(), dep))

	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	assert.Nil(t, s.lastDispatchedParsed)
	assert.Empty(t, s.lastActivatedConfig)
	require.Same(t, dep, s.state.pending)
	assert.Equal(t, laneStructural, dep.lane)
}

// Case 2 (headline — the #55 fix): a runtime-eligible render that arrives WHILE a
// structural deploy is in flight has its server subset applied to the live workers
// IMMEDIATELY (a partial runtime apply), not queued behind the whole in-flight
// deploy's execution. This is the residual rolling-restart 503: under cross-tenant
// churn a structural reload can take ~1s to execute, and a pod that goes Ready in
// that window must get its reserved-slot address onto HAProxy in ~ms — it cannot
// wait for the unrelated deploy to finish. MUST FAIL without the awaitCompletion
// interleave (the old select-only awaitCompletion never consumed pendingSignal, so
// the apply only fired AFTER completion).
func TestSchedulerLanes_Case2_RuntimeSubsetAppliesDuringInFlightStructural(t *testing.T) {
	s, scheduledCh, applied, cancel := newLaneScheduler(t, 0)
	defer cancel()

	_, _, structural := laneRenders(t)

	// The fleet is already running the base config. Without this the scheduler is
	// at cold start, where nothing is activated and the partial apply correctly
	// declines — patching the in-flight structural render instead is #112, and
	// this test asserts below that it does not happen.
	s.schedulerMutex.Lock()
	s.lastActivatedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.schedulerMutex.Unlock()

	// Dispatch a structural render first. Cold start (nil baseline) → structural,
	// which the loop publishes and marks in-flight (loop parks in awaitCompletion).
	s.scheduleOrQueue(context.Background(), "structural-config", nil, structural, oneEndpoint(),
		"structural-change", "corr-structural", nil, true, "", nil, "")
	sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "structural-config", sd.Config, "the structural deploy is published first")

	// While it is in flight, schedule a runtime-eligible render. Its diff is taken
	// against the now-dispatched structural baseline (which added api2), so the
	// render keeps api2 and only changes SRV_1's address — a pure runtime-eligible
	// diff → laneRuntimeRaw. (The loop set lastDispatchedParsed = structural when it
	// dispatched the structural deploy above.)
	s.schedulerMutex.Lock()
	require.True(t, s.state.deployInFlight, "the structural deploy must be in flight")
	require.Equal(t, structural, s.lastDispatchedParsed, "the in-flight structural render is the diff baseline")
	s.schedulerMutex.Unlock()
	structuralPlusAddr := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "runtime-config", nil, structuralPlusAddr, oneEndpoint(),
		"endpoint-change", "corr-runtime", nil, true, "", nil, "")

	// The runtime subset must apply within ms WHILE the structural deploy is still
	// in flight — BEFORE we signal its completion below. (Without the fix, nothing
	// fires here and the test times out.)
	select {
	case <-applied:
		assert.Less(t, time.Since(start), time.Second,
			"the in-flight runtime subset must apply in ~ms, not wait for the structural deploy to complete")
	case <-time.After(2 * time.Second):
		t.Fatal("runtime subset was not applied while the structural deploy was in flight (the #55 gap)")
	}

	// The partial apply must NOT have completed the structural deploy or published a
	// second deploy: the in-flight structural deploy still owns completion + CR.
	s.schedulerMutex.Lock()
	assert.True(t, s.state.deployInFlight, "the partial in-flight apply must not clear deployInFlight")
	require.NotNil(t, s.state.pending, "the runtime render stays pending — the partial apply does not consume it")
	assert.Equal(t, laneRuntimeRaw, s.state.pending.lane, "diff vs the in-flight structural render is runtime-eligible")
	s.schedulerMutex.Unlock()
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)

	// Complete the in-flight structural deploy. awaitCompletion returns, the loop
	// grabs the still-pending runtime-raw render and dispatches it authoritatively
	// (a second, full applyRuntimeRaw).
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 1, Succeeded: 1, DurationMs: 10,
	}))

	select {
	case <-applied:
		// Expected — the authoritative runtime-raw dispatch after completion.
	case <-time.After(testutil.LongTimeout):
		t.Fatal("the pending runtime-raw render did not dispatch after the structural deploy completed")
	}

	// The runtime-raw lane never publishes a DeploymentScheduledEvent.
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
		"endpoint-change", "corr-3", nil, true, "", nil, "")

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
		"structural-change", "corr-4", nil, true, "", nil, "")

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
		"structural-1", "corr-5a", nil, true, "", nil, "")
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
		"structural-2", "corr-5b", nil, true, "", nil, "")
	s.scheduleOrQueue(context.Background(), "structural-3", nil, structuralC, oneEndpoint(),
		"structural-3", "corr-5c", nil, true, "", nil, "")

	s.schedulerMutex.Lock()
	require.NotNil(t, s.state.pending, "a structural deploy must be enqueued")
	assert.Equal(t, laneStructural, s.state.pending.lane, "the enqueued deploy is structural")
	assert.Equal(t, "structural-3", s.state.pending.config, "latest-wins: only the newest enqueued structural survives")
	s.schedulerMutex.Unlock()

	// Complete the in-flight deploy; the loop's awaitCompletion unblocks and it
	// grabs the enqueued structural-3 as the next deploy (interval=0, so no wait).
	s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
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
	// interval. Seed the baseline (parsed + raw text, written together like
	// dispatchPending does) so the render diffs against it (not cold-start)
	// and the fast-track apply can build its baseline-derived body.
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.lastActivatedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "mixed-config", nil, mixed, oneEndpoint(),
		"endpoint-change+churn", "corr-6", nil, true, "", nil, "")

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
	// Parsed + raw baseline seeded together, like dispatchPending writes them.
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.lastActivatedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	// First structural+runtime render → the loop enters the interval wait and
	// applies its runtime subset up front.
	s.scheduleOrQueue(context.Background(), "render1", nil, render1, oneEndpoint(),
		"r1", "corr-7a", nil, true, "", nil, "")
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
		"r2", "corr-7b", nil, true, "", nil, "")
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
// here — this is the gap the fix closes. Before it, the pre-interval apply ran
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
	// structural deploy is NOT gated (wait<=0). Seed the baseline (parsed + raw,
	// written together like dispatchPending does) so the render diffs against it
	// (not cold-start) and the fast-track apply can build its body.
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.lastActivatedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.state.lastDeploymentEndTime = time.Now().Add(-2 * interval)
	s.schedulerMutex.Unlock()

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "mixed-config", nil, mixed, oneEndpoint(),
		"endpoint-change+churn", "corr-8", nil, true, "", nil, "")

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

// TestScheduler_ApplyRuntimeSubset_NilOrZeroOpNoOp guards the two no-op paths of
// the fast-track apply shared by both deploy-loop wait points (waitDeployInterval
// and awaitCompletion): a nil pending (handleLostLeadership clears s.state.pending
// concurrently with a buffered pendingSignal that the select can still pick before
// it observes ctx.Done()) and a pending whose diff carries no runtime-eligible
// server op (ServerOpCount 0, nil runtimeUpdates). Both must no-op rather than
// panic or attempt a real apply.
func TestScheduler_ApplyRuntimeSubset_NilOrZeroOpNoOp(t *testing.T) {
	s := newDeploymentScheduler(testutil.NewTestBus(), testutil.NewTestLogger(), 5*time.Second, 30*time.Second)
	require.NotPanics(t, func() {
		s.applyRuntimeSubset(context.Background(), nil)
		s.applyRuntimeSubset(context.Background(), &scheduledDeployment{}) // nil runtimeUpdates → ServerOpCount 0
	})
}

// Case 9 (race guard for the awaitCompletion interleave): while the loop is parked
// in awaitCompletion for an in-flight structural deploy, the completion signal and
// a newer runtime render can land concurrently. Whichever the select picks, the
// loop must converge — apply the runtime subset and dispatch the pending render —
// without eating THIS deploy's completion (the reason awaitCompletion must NOT
// drain s.completed in the pendingSignal branch) or deadlocking. Run -race -count.
func TestSchedulerLanes_Case9_CompletionAndPendingRace(t *testing.T) {
	s, scheduledCh, applied, cancel := newLaneScheduler(t, 0)
	defer cancel()

	_, _, structural := laneRenders(t)

	// Dispatch a structural render; the loop marks it in-flight and parks in
	// awaitCompletion.
	s.scheduleOrQueue(context.Background(), "structural-config", nil, structural, oneEndpoint(),
		"structural-change", "corr-structural", nil, true, "", nil, "")
	testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	s.schedulerMutex.Lock()
	require.True(t, s.state.deployInFlight, "the structural deploy must be in flight")
	s.schedulerMutex.Unlock()

	// A runtime-eligible render diffed against the in-flight structural baseline.
	runtimeRender := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)

	// Fire the completion and the new render from two goroutines released together,
	// so the awaitCompletion select races them.
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-startGate
		s.handleDeploymentCompleted(completionForActiveDeployment(s, &events.DeploymentResult{
			Total: 1, Succeeded: 1, DurationMs: 10,
		}))
	}()
	go func() {
		defer wg.Done()
		<-startGate
		s.scheduleOrQueue(context.Background(), "runtime-config", nil, runtimeRender, oneEndpoint(),
			"endpoint-change", "corr-runtime", nil, true, "", nil, "")
	}()
	close(startGate)
	wg.Wait()

	// Convergence: the runtime subset is applied at least once (partial in-flight
	// and/or the authoritative post-completion dispatch)...
	select {
	case <-applied:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("no runtime apply — the loop ate the completion or deadlocked in awaitCompletion")
	}

	// ...and the loop progresses past awaitCompletion and consumes the pending
	// render (grab+clear → pending nil), proving the completion was not eaten.
	require.Eventually(t, func() bool {
		s.schedulerMutex.Lock()
		defer s.schedulerMutex.Unlock()
		return s.state.pending == nil && !s.state.deployInFlight
	}, testutil.LongTimeout, 5*time.Millisecond,
		"the loop must consume the pending render and finish the in-flight deploy")
}

// Case 10 (the residual rolling-restart fix the version-stress exposed): a
// runtime-raw render that arrives WHILE the loop is sleeping out
// minDeploymentInterval (entered for an earlier STRUCTURAL pending) must have its
// server subset applied to the live workers IMMEDIATELY, not wait out the interval.
//
// This is the gap that produced ~1-in-4 rolling-restart 503s (worse on slower
// HAProxy builds like the 3.0 image): once a cross-tenant structural deploy
// completes, lastDispatchedParsed has advanced to it, so a newly-Ready pod's render
// diffs PURELY runtime-eligible (laneRuntimeRaw). The prior pre-interval apply was
// gated on laneStructural, so it SKIPPED that render during the interval sleep — the
// new pod's slot fill only reached HAProxy when the interval elapsed (~1.4s late),
// by which time the dying old slot had exhausted `option redispatch` → SC-- 503. The
// shared applyRuntimeSubset is now lane-independent; this test times out without
// that fix.
func TestSchedulerLanes_Case10_RuntimeRawDuringStructuralInterval_AppliesImmediately(t *testing.T) {
	const interval = 5 * time.Second
	s, scheduledCh, applied, cancel := newLaneScheduler(t, interval)
	defer cancel()

	_, _, structural := laneRenders(t) // laneConfigBase(10.0.0.1) + api2

	// Simulate "a structural deploy just completed": the dispatch baseline
	// (parsed + raw, written together like dispatchPending does) has advanced
	// to the structural config and the interval window is open.
	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = structural
	s.lastDispatchedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080") + laneStructuralExtra
	s.lastActivatedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080") + laneStructuralExtra
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	// A NEW structural render (adds api3) becomes pending → diff vs the structural
	// baseline is a brand-new backend → laneStructural → the loop enters
	// waitDeployInterval and sleeps out the ~5s interval (NOT dispatched in-window).
	structuralPlus := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")+laneStructuralExtra+
		"\nbackend api3\n  default-server check\n  server SRV_1 10.9.9.9:8080 enabled\n")
	s.scheduleOrQueue(context.Background(), "structural-2", nil, structuralPlus, oneEndpoint(),
		"structural-2", "corr-10a", nil, true, "", nil, "")
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 200*time.Millisecond)
	s.schedulerMutex.Lock()
	require.NotNil(t, s.state.pending, "the structural render is pending")
	require.Equal(t, laneStructural, s.state.pending.lane, "and gated (sleeping out the interval)")
	s.schedulerMutex.Unlock()

	// Now a newly-Ready pod arrives as a RUNTIME-RAW render (SRV_1 address change vs
	// the advanced structural baseline — purely runtime-eligible). It overwrites
	// pending (latest-wins) while the loop is still sleeping the structural interval.
	runtimeRender := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)
	upd, err := dataplane.ComputeRuntimeServerUpdates(structural, runtimeRender)
	require.NoError(t, err)
	require.True(t, upd.IsRuntimeEligible(), "the mid-interval render must classify runtime-raw vs the advanced baseline")

	start := time.Now()
	s.scheduleOrQueue(context.Background(), "runtime-config", nil, runtimeRender, oneEndpoint(),
		"endpoint-change", "corr-10b", nil, true, "", nil, "")
	s.schedulerMutex.Lock()
	require.Equal(t, laneRuntimeRaw, s.state.pending.lane, "the new-pod render is runtime-raw")
	s.schedulerMutex.Unlock()

	// Its server subset must reach the workers within ms — NOT wait out the ~5s
	// interval the loop is sleeping for the structural pending.
	select {
	case <-applied:
		assert.Less(t, time.Since(start), time.Second,
			"a runtime-raw render arriving during the structural interval sleep must apply immediately")
	case <-time.After(2 * time.Second):
		t.Fatal("runtime-raw subset was trapped behind the structural interval sleep (the residual 503 gap)")
	}

	// The structural reload stays gated by the interval — no deploy published in-window
	// (and the runtime-raw lane never publishes a DeploymentScheduledEvent).
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 100*time.Millisecond)
}
