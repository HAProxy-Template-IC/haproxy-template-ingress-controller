// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func patchNames(patches []StatusPatch) []string {
	names := make([]string, 0, len(patches))
	for index := range patches {
		names = append(names, patches[index].Name)
	}
	return names
}

// TestChangedPatchesReportOnlyWhatDiffers pins the delta to the patches the
// previous snapshot did not carry in the same form, and pins each one to the
// full materialization of the same snapshot.
func TestChangedPatchesReportOnlyWhatDiffers(t *testing.T) {
	register := func(t *testing.T, collector *StatusPatchCollector, name, resourceVersion, owner string) {
		t.Helper()
		require.NoError(t, collector.RegisterWithLineage(
			"default", name, "example.test/v1", "Route", "uid-"+name, resourceVersion,
			map[string]map[string]any{
				"rendered": {"owner": owner},
				"deployed": {"owner": owner, "phase": "deployed"},
			},
		))
	}
	first := NewStatusPatchCollector()
	for _, name := range []string{"a", "b", "c", "d"} {
		register(t, first, name, "rv-1", "same")
	}
	previous, err := first.Snapshot()
	require.NoError(t, err)

	second := NewStatusPatchCollector()
	register(t, second, "a", "rv-1", "same")    // unchanged
	register(t, second, "b", "rv-1", "changed") // a variant changed
	register(t, second, "c", "rv-2", "same")    // the object moved on
	register(t, second, "e", "rv-1", "same")    // new; d was removed
	current, err := second.Snapshot(previous)
	require.NoError(t, err)
	require.NotSame(t, previous, current)

	changed, err := current.ChangedPatchesForPhase(previous, "rendered")
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c", "e"}, patchNames(changed))
	full, err := current.PatchesForPhase("rendered")
	require.NoError(t, err)
	byName := map[string]StatusPatch{}
	for _, patch := range full {
		byName[patch.Name] = patch
	}
	for _, patch := range changed {
		assert.Equal(t, byName[patch.Name], patch, "changed patch %s equals its full materialization", patch.Name)
		assert.Len(t, patch.Variants, 1, "only the requested phase is materialized")
	}

	all, err := current.ChangedPatchesForPhase(nil, "deployed")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "e"}, patchNames(all), "without a previous snapshot every patch is a change")

	none, err := current.ChangedPatchesForPhase(current, "rendered")
	require.NoError(t, err)
	assert.Empty(t, none, "a snapshot changes nothing against itself")

	_, err = current.ChangedPatchesForPhase(previous, "")
	require.Error(t, err)
	copied := *previous
	_, err = current.ChangedPatchesForPhase(&copied, "rendered")
	require.ErrorContains(t, err, "previous snapshot")
}

// TestChangedPatchesReportOnlyChangedProjections pins the delta for projected
// patches: a plan replayed on the same root changes nothing, a replaced entry
// changes its own target only, and a target the previous plan did not project
// is a change.
func TestChangedPatchesReportOnlyChangedProjections(t *testing.T) {
	stable := mustPlanProjection(t, "stable", "uid-stable", "rv", "rendered", "stable")
	moving := mustPlanProjection(t, "moving", "uid-moving", "rv", "rendered", "one")
	plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "001", stable)
	require.NoError(t, err)
	plan, err = plan.ReplaceEntry("routes", "002", moving)
	require.NoError(t, err)

	snapshotPlan := func(t *testing.T, plan *StatusPatchProjectionPlan, previous *StatusPatchSnapshot) *StatusPatchSnapshot {
		t.Helper()
		replay, err := plan.PrepareReplay()
		require.NoError(t, err)
		collector := NewStatusPatchCollector()
		require.NoError(t, collector.ReplayProjectionPlan(replay))
		snapshot, err := collector.Snapshot(previous)
		require.NoError(t, err)
		return snapshot
	}
	previous := snapshotPlan(t, plan, nil)
	all, err := previous.ChangedPatchesForPhase(nil, "rendered")
	require.NoError(t, err)
	assert.Equal(t, []string{"stable", "moving"}, patchNames(all))

	same := snapshotPlan(t, plan, previous)
	assert.Same(t, previous, same, "the same plan on the same root reuses the snapshot")
	none, err := same.ChangedPatchesForPhase(previous, "rendered")
	require.NoError(t, err)
	assert.Empty(t, none)

	replaced, err := plan.ReplaceEntry("routes", "002",
		mustPlanProjection(t, "moving", "uid-moving", "rv", "rendered", "two"))
	require.NoError(t, err)
	next := snapshotPlan(t, replaced, previous)
	changed, err := next.ChangedPatchesForPhase(previous, "rendered")
	require.NoError(t, err)
	require.Equal(t, []string{"moving"}, patchNames(changed))
	assert.Equal(t, "two", changed[0].Variants["rendered"]["owner"])

	grown, err := replaced.ReplaceEntry("routes", "003",
		mustPlanProjection(t, "added", "uid-added", "rv", "rendered", "new"))
	require.NoError(t, err)
	after := snapshotPlan(t, grown, next)
	changed, err = after.ChangedPatchesForPhase(next, "rendered")
	require.NoError(t, err)
	assert.Equal(t, []string{"added"}, patchNames(changed))
}

// newLineageBenchmarkCollector registers fleet-sized patches with lineage,
// changing only the patch at index changed.
func newLineageBenchmarkCollector(b *testing.B, changed int) *StatusPatchCollector {
	b.Helper()
	collector := NewStatusPatchCollector()
	for index := range statusPatchBenchmarkPatchCount {
		generation := index
		if index == changed {
			generation = -1
		}
		err := collector.RegisterWithLineage(
			"default", fmt.Sprintf("route-%06d", index), "example.test/v1", "Route",
			fmt.Sprintf("uid-%06d", index), "rv-1",
			map[string]map[string]any{
				"rendered": {"conditions": []any{map[string]any{"type": "Accepted", "generation": generation}}},
				"deployed": {"conditions": []any{map[string]any{"type": "Programmed", "generation": generation}}},
			},
		)
		require.NoError(b, err)
	}
	return collector
}

// newPlanBenchmarkSnapshot seals a fleet-sized projection plan with one entry
// replaced relative to plan, the shape the incremental render produces.
func newPlanBenchmarkPlan(b *testing.B) *StatusPatchProjectionPlan {
	b.Helper()
	plan := NewStatusPatchProjectionPlan()
	for index := range statusPatchBenchmarkPatchCount {
		name := fmt.Sprintf("route-%06d", index)
		var err error
		plan, err = plan.ReplaceEntry("routes", name, mustPlanProjection(b, name, "uid-"+name, "rv-1", "rendered", "same"))
		require.NoError(b, err)
	}
	return plan
}

func snapshotBenchmarkPlan(b *testing.B, plan *StatusPatchProjectionPlan, previous *StatusPatchSnapshot) *StatusPatchSnapshot {
	b.Helper()
	replay, err := plan.PrepareReplay()
	require.NoError(b, err)
	collector := NewStatusPatchCollector()
	require.NoError(b, collector.ReplayProjectionPlan(replay))
	snapshot, err := collector.Snapshot(previous)
	require.NoError(b, err)
	return snapshot
}

// benchmarkDeltaAgainstFull runs the delta of one changed patch and the full
// materialization of the same snapshot.
func benchmarkDeltaAgainstFull(b *testing.B, prefix string, previous, snapshot *StatusPatchSnapshot) {
	b.Helper()
	b.Run(prefix+"changed", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			changed, err := snapshot.ChangedPatchesForPhase(previous, "rendered")
			if err != nil {
				b.Fatal(err)
			}
			if len(changed) != 1 {
				b.Fatalf("got %d changed patches", len(changed))
			}
		}
	})
	b.Run(prefix+"all", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			all, err := snapshot.PatchesForPhase("rendered")
			if err != nil {
				b.Fatal(err)
			}
			if len(all) != statusPatchBenchmarkPatchCount {
				b.Fatalf("got %d patches", len(all))
			}
		}
	})
}

// BenchmarkStatusPatchChangedPatches prices the delta of one changed patch in
// a fleet-sized snapshot against materializing the whole phase, for a replayed
// projection plan and for patches registered with detached values.
func BenchmarkStatusPatchChangedPatches(b *testing.B) {
	plan := newPlanBenchmarkPlan(b)
	previousPlan := snapshotBenchmarkPlan(b, plan, nil)
	replaced, err := plan.ReplaceEntry("routes", "route-000007",
		mustPlanProjection(b, "route-000007", "uid-route-000007", "rv-2", "rendered", "changed"))
	require.NoError(b, err)
	benchmarkDeltaAgainstFull(b, "projected-", previousPlan, snapshotBenchmarkPlan(b, replaced, previousPlan))

	previous, err := newLineageBenchmarkCollector(b, -1).Snapshot()
	require.NoError(b, err)
	snapshot, err := newLineageBenchmarkCollector(b, 7).Snapshot(previous)
	require.NoError(b, err)
	benchmarkDeltaAgainstFull(b, "detached-", previous, snapshot)
}
