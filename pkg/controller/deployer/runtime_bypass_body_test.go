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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// bodyRecordingSyncer records the body and options of each SyncRuntimeFast call.
type bodyRecordingSyncer struct {
	mu     sync.Mutex
	bodies []string
	opts   []*dataplane.SyncOptions
	// activated is echoed back as the result's ActivatedConfigChecksum, so tests
	// can tell a proof the caller CLEARED from one the syncer never produced.
	activated string
}

func (s *bodyRecordingSyncer) SyncRuntimeFast(_ context.Context, _ *dataplane.RuntimeServerUpdates, body string, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
	s.opts = append(s.opts, opts)
	return &dataplane.SyncResult{Success: true, ActivatedConfigChecksum: s.activated}, nil
}

func (s *bodyRecordingSyncer) Close() error { return nil }

func (s *bodyRecordingSyncer) recorded(t *testing.T) (string, *dataplane.SyncOptions) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.bodies, 1, "exactly one push expected")
	return s.bodies[0], s.opts[0]
}

// pushed reports whether any bypass push happened at all.
func (s *bodyRecordingSyncer) pushed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies) > 0
}

// newBodyRecordingScheduler builds a scheduler whose bypass records pushes.
func newBodyRecordingScheduler(t *testing.T) (*DeploymentScheduler, *bodyRecordingSyncer) {
	t.Helper()
	s := NewDeploymentScheduler(testutil.NewTestBus(), testutil.NewTestLogger(), 5*time.Second, 30*time.Second)
	rec := &bodyRecordingSyncer{}
	s.runtimeBypass.newSyncer = func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return rec, nil
	}
	return s, rec
}

// TestScheduler_ApplyRuntimeSubset_BodyIsBaselinePlusRuntimePatch is the
// deployer-level regression test for issue #84: the fast-track (partial)
// apply of a STRUCTURAL pending render must push the last-DISPATCHED baseline
// patched with only the runtime-eligible server lines — the pending render's
// NEW backend must NOT appear in the pushed body. Pushing the pending render
// verbatim is exactly the defect: its structural content lands on disk
// without a reload, where it clobbers a concurrent force_reload deploy's
// write (mode A) or parks un-activated until an unrelated reload (mode B).
func TestScheduler_ApplyRuntimeSubset_BodyIsBaselinePlusRuntimePatch(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)

	baselineRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	baseline := parseLaneConfig(t, baselineRaw)
	// Mixed pending render: SRV_1 address change (runtime-eligible) PLUS a
	// brand-new backend api2 (structural) — a pod rotation coalesced with
	// another tenant's structural change.
	mixedRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080") + laneStructuralExtra
	mixed := parseLaneConfig(t, mixedRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(baseline, mixed)
	require.NoError(t, err)
	require.Greater(t, updates.ServerOpCount(), 0)
	require.Greater(t, updates.StructuralOpCount(), 0)

	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = baselineRaw
	s.lastActivatedConfig = baselineRaw
	s.schedulerMutex.Unlock()

	dep := &scheduledDeployment{
		config:         mixedRaw,
		parsedConfig:   mixed,
		endpoints:      oneEndpoint(),
		lane:           laneStructural,
		runtimeUpdates: updates,
	}
	s.applyRuntimeSubset(context.Background(), dep)

	body, opts := rec.recorded(t)
	assert.Contains(t, body, "10.0.0.2:8080", "the runtime-eligible address change IS patched in")
	assert.NotContains(t, body, "api2", "the pending render's NEW backend must NOT reach the bypass body")
	assert.NotContains(t, body, "10.9.9.9", "no server line of the new backend leaks into the body")
	assert.NotEqual(t, mixedRaw, body, "the body is never the pending render")
	assert.False(t, opts.RestampVersionHeader, "a partial apply must leave the config headerless")
	require.NotNil(t, opts.RenderSuperseded, "the partial apply must carry a supersede probe")
}

// TestScheduler_ApplyRuntimeSubset_SupersededProbe verifies the partial
// apply's RenderSuperseded probe tracks the pending slot: it reports false
// while the applied dep is still the pending render and true once a newer
// render replaced it (latest-wins) — the signal that lets the
// retry-across-reload loop abandon a stale-body storm (issue #84).
func TestScheduler_ApplyRuntimeSubset_SupersededProbe(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)

	baselineRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	baseline := parseLaneConfig(t, baselineRaw)
	runtimeRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")
	runtime := parseLaneConfig(t, runtimeRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(baseline, runtime)
	require.NoError(t, err)

	dep := &scheduledDeployment{
		config:         runtimeRaw,
		parsedConfig:   runtime,
		endpoints:      oneEndpoint(),
		lane:           laneRuntimeRaw,
		runtimeUpdates: updates,
	}

	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = baselineRaw
	s.lastActivatedConfig = baselineRaw
	s.state.pending = dep // dep is the current pending render
	s.schedulerMutex.Unlock()

	s.applyRuntimeSubset(context.Background(), dep)
	_, opts := rec.recorded(t)
	require.NotNil(t, opts.RenderSuperseded)

	assert.False(t, opts.RenderSuperseded(), "dep is still the pending render — not superseded")

	// A newer render replaces the pending slot (latest-wins).
	s.schedulerMutex.Lock()
	s.state.pending = &scheduledDeployment{config: "newer"}
	s.schedulerMutex.Unlock()
	assert.True(t, opts.RenderSuperseded(), "a newer pending render supersedes dep")

	// Lost leadership clears the slot — also superseded (nothing to storm for).
	s.schedulerMutex.Lock()
	s.state.pending = nil
	s.schedulerMutex.Unlock()
	assert.True(t, opts.RenderSuperseded())
}

// TestScheduler_ApplyRuntimeSubset_NoBaselineSkips verifies the partial apply
// is skipped entirely when no dispatched baseline config exists (cold start,
// or the baseline was invalidated after a failed deploy): there is no
// activated body to patch, and the pending render must not be pushed raw.
func TestScheduler_ApplyRuntimeSubset_NoBaselineSkips(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)

	baseline := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.1:8080"))
	runtime := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080"))
	updates, err := dataplane.ComputeRuntimeServerUpdates(baseline, runtime)
	require.NoError(t, err)
	require.Greater(t, updates.ServerOpCount(), 0)

	// lastDispatchedConfig deliberately left empty.
	dep := &scheduledDeployment{
		config:         fmt.Sprintf(laneConfigBase, "10.0.0.2:8080"),
		endpoints:      oneEndpoint(),
		runtimeUpdates: updates,
	}
	s.applyRuntimeSubset(context.Background(), dep)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Empty(t, rec.bodies, "no baseline → no bypass push (the scheduled deploy converges)")
}

// TestScheduler_DispatchRuntimeRaw_BodyAndSupersededProbe verifies the
// AUTHORITATIVE runtime-raw dispatch pushes the render itself (by lane
// construction it differs from the activated baseline only in
// runtime-eligible fields, so it already satisfies the issue #84 bypass-body
// invariant), re-stamps the version header, and carries a supersede probe
// keyed on "any newer pending exists".
func TestScheduler_DispatchRuntimeRaw_BodyAndSupersededProbe(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)

	baselineRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	baseline := parseLaneConfig(t, baselineRaw)
	runtimeRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")
	runtime := parseLaneConfig(t, runtimeRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(baseline, runtime)
	require.NoError(t, err)
	require.True(t, updates.IsRuntimeEligible())

	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = baselineRaw
	s.lastActivatedConfig = baselineRaw
	s.schedulerMutex.Unlock()

	dep := &scheduledDeployment{
		config:         runtimeRaw,
		parsedConfig:   runtime,
		endpoints:      oneEndpoint(),
		lane:           laneRuntimeRaw,
		runtimeUpdates: updates,
	}
	require.True(t, s.dispatchPending(context.Background(), dep))

	body, opts := rec.recorded(t)
	assert.Equal(t, runtimeRaw, body, "the authoritative runtime-raw apply pushes the render itself")
	assert.True(t, opts.RestampVersionHeader, "the authoritative apply re-stamps the version header")
	require.NotNil(t, opts.RenderSuperseded)
	assert.False(t, opts.RenderSuperseded(), "no pending render — not superseded")

	s.schedulerMutex.Lock()
	s.state.pending = &scheduledDeployment{config: "newer"}
	s.schedulerMutex.Unlock()
	assert.True(t, opts.RenderSuperseded(), "any newer pending render supersedes the in-flight dispatch")

	// The dispatch advanced the baseline to the applied render.
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	assert.Equal(t, runtimeRaw, s.lastDispatchedConfig)
	assert.Equal(t, runtime, s.lastDispatchedParsed)
}

// TestScheduler_ApplyRuntimeSubset_NeverPatchesUnlandedStructural pins #112:
// during a structural deploy's flight, lastDispatchedConfig is the render being
// deployed but NOT yet running. The partial apply must patch the last ACTIVATED
// config instead — otherwise it writes the pending structural content to disk
// under skip_reload, HAProxy never loads it, and the next sync's empty diff
// reports success while the render stays parked.
func TestScheduler_ApplyRuntimeSubset_NeverPatchesUnlandedStructural(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)

	activatedRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")

	// A structural render is in flight: dispatched (so it is the lane-diff
	// baseline) but not yet activated on any pod.
	inFlightRaw := activatedRaw + laneStructuralExtra
	inFlight := parseLaneConfig(t, inFlightRaw)

	// The pending render adds a pod-IP rotation on top of the in-flight one.
	pendingRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080") + laneStructuralExtra
	pending := parseLaneConfig(t, pendingRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(inFlight, pending)
	require.NoError(t, err)
	require.Greater(t, updates.ServerOpCount(), 0)

	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = inFlight
	s.lastDispatchedConfig = inFlightRaw // dispatched, NOT landed
	s.lastActivatedConfig = activatedRaw // what the pods are really running
	s.schedulerMutex.Unlock()

	s.applyRuntimeSubset(context.Background(), &scheduledDeployment{
		config:         pendingRaw,
		parsedConfig:   pending,
		endpoints:      oneEndpoint(),
		lane:           laneRuntimeRaw,
		runtimeUpdates: updates,
	})

	body, _ := rec.recorded(t)
	assert.Contains(t, body, "10.0.0.2:8080", "the runtime-eligible address change is still applied")
	assert.NotContains(t, body, "api2",
		"the in-flight structural render must not be written to disk under skip_reload (#112)")
	assert.NotContains(t, body, "10.9.9.9",
		"no server line of the unlanded backend may reach disk")
}

// TestScheduler_ApplyRuntimeSubset_DeclinesWithNothingActivated pins the
// cold-start half of #112: with no config proven running there is nothing safe
// to patch, so the apply must decline rather than fall back to the dispatched
// render. The scheduled structural deploy carries the change instead.
func TestScheduler_ApplyRuntimeSubset_DeclinesWithNothingActivated(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)

	dispatchedRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080") + laneStructuralExtra
	dispatched := parseLaneConfig(t, dispatchedRaw)
	pendingRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080") + laneStructuralExtra
	pending := parseLaneConfig(t, pendingRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(dispatched, pending)
	require.NoError(t, err)
	require.Greater(t, updates.ServerOpCount(), 0)

	s.schedulerMutex.Lock()
	s.lastDispatchedParsed = dispatched
	s.lastDispatchedConfig = dispatchedRaw
	// lastActivatedConfig deliberately empty: nothing is proven running.
	s.schedulerMutex.Unlock()

	s.applyRuntimeSubset(context.Background(), &scheduledDeployment{
		config:         pendingRaw,
		parsedConfig:   pending,
		endpoints:      oneEndpoint(),
		lane:           laneRuntimeRaw,
		runtimeUpdates: updates,
	})

	assert.False(t, rec.pushed(), "no bypass push may happen with nothing activated")
}

// TestScheduler_ApplyRuntimeSubset_InFlightPatchesDispatchedNotActivated pins the
// mode-A half of issue #84, which the mode-B fix (patch the ACTIVATED config)
// re-opened: while a structural deploy is in flight it has ALREADY written its
// render to disk, so patching the older activated config pushes that render back
// off disk under skip_reload. The deploy's own post-reload read-back then finds its
// whole render missing and fails post_reload_divergence (observed in CI: 189
// structural ops, 14 backends, on a deploy whose config was correct).
//
// The body must therefore patch the in-flight DISPATCHED config, leaving only a
// runtime-eligible server difference on disk — which the read-back tolerates by
// design. Because that body's structural half is on disk but loaded by no worker
// until the in-flight reload lands, the apply must also CLEAR the activation proof
// (mode B / issue #76): the next sync then force-reloads rather than trusting an
// empty diff over parked content.
func TestScheduler_ApplyRuntimeSubset_InFlightPatchesDispatchedNotActivated(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)
	rec.activated = "proof-from-syncer"

	var mu sync.Mutex
	proofs := []string{}
	s.runtimeBypass.recordActivation = func(_ *dataplane.Endpoint, proof string) {
		mu.Lock()
		defer mu.Unlock()
		proofs = append(proofs, proof)
	}

	// The fleet is running the plain base; the in-flight structural deploy has
	// dispatched (and written) base+api2.
	activatedRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	dispatchedRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080") + laneStructuralExtra
	dispatched := parseLaneConfig(t, dispatchedRaw)

	// A pod then goes Ready: same structural shape as the in-flight render, only
	// SRV_1's address differs — a purely runtime-eligible diff against it.
	pendingRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080") + laneStructuralExtra
	pending := parseLaneConfig(t, pendingRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(dispatched, pending)
	require.NoError(t, err)
	require.Greater(t, updates.ServerOpCount(), 0, "premise: the address change is runtime-eligible")
	require.Equal(t, 0, updates.StructuralOpCount(), "premise: nothing structural vs the in-flight render")

	s.schedulerMutex.Lock()
	s.lastActivatedConfig = activatedRaw
	s.lastDispatchedParsed = dispatched
	s.lastDispatchedConfig = dispatchedRaw
	s.state.deployInFlight = true
	s.schedulerMutex.Unlock()

	s.applyRuntimeSubset(context.Background(), &scheduledDeployment{
		config:         pendingRaw,
		parsedConfig:   pending,
		endpoints:      oneEndpoint(),
		lane:           laneRuntimeRaw,
		runtimeUpdates: updates,
	})

	body, opts := rec.recorded(t)
	assert.Contains(t, body, "api2",
		"the in-flight deploy's structural content must stay on disk — patching the activated config rolls its write back (mode A)")
	assert.Contains(t, body, "10.0.0.2:8080", "the runtime-eligible address change IS patched in")
	assert.False(t, opts.RestampVersionHeader, "a partial apply must leave the config headerless")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, proofs, 1, "the apply records exactly one activation outcome")
	assert.Empty(t, proofs[0],
		"the body's structural half is parked unloaded until the in-flight reload lands, so it proves nothing (mode B)")
}

// TestScheduler_ApplyRuntimeSubset_NoDeployInFlightPatchesActivated pins the
// complement: with no deploy in flight, disk holds the activated config, so that
// is what gets patched — and the apply may record a real activation proof.
func TestScheduler_ApplyRuntimeSubset_NoDeployInFlightPatchesActivated(t *testing.T) {
	s, rec := newBodyRecordingScheduler(t)
	rec.activated = "proof-from-syncer"

	var mu sync.Mutex
	proofs := []string{}
	s.runtimeBypass.recordActivation = func(_ *dataplane.Endpoint, proof string) {
		mu.Lock()
		defer mu.Unlock()
		proofs = append(proofs, proof)
	}

	baselineRaw := fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	baseline := parseLaneConfig(t, baselineRaw)
	pendingRaw := fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")
	pending := parseLaneConfig(t, pendingRaw)
	updates, err := dataplane.ComputeRuntimeServerUpdates(baseline, pending)
	require.NoError(t, err)

	s.schedulerMutex.Lock()
	s.lastActivatedConfig = baselineRaw
	s.lastDispatchedParsed = baseline
	s.lastDispatchedConfig = baselineRaw
	s.state.deployInFlight = false
	s.schedulerMutex.Unlock()

	s.applyRuntimeSubset(context.Background(), &scheduledDeployment{
		config:         pendingRaw,
		parsedConfig:   pending,
		endpoints:      oneEndpoint(),
		lane:           laneRuntimeRaw,
		runtimeUpdates: updates,
	})

	body, _ := rec.recorded(t)
	assert.Contains(t, body, "10.0.0.2:8080")
	assert.NotContains(t, body, "api2", "no structural content enters a body pushed over the activated config")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, proofs, 1)
	assert.NotEmpty(t, proofs[0], "with disk == activated + runtime patch, the apply proves the running state")
}
