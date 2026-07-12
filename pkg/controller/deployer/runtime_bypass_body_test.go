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
}

func (s *bodyRecordingSyncer) SyncRuntimeFast(_ context.Context, _ *dataplane.RuntimeServerUpdates, body string, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
	s.opts = append(s.opts, opts)
	return &dataplane.SyncResult{Success: true}, nil
}

func (s *bodyRecordingSyncer) Close() error { return nil }

func (s *bodyRecordingSyncer) recorded(t *testing.T) (string, *dataplane.SyncOptions) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.bodies, 1, "exactly one push expected")
	return s.bodies[0], s.opts[0]
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
