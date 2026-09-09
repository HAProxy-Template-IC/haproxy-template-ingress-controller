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

package templating

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func selectionPatch(name, phase, owner string) StatusPatch {
	return StatusPatch{
		Namespace: "default", Name: name, APIVersion: "example.test/v1", Kind: "Route",
		UID: "uid-" + name, ResourceVersion: "rv",
		Variants:       map[string]map[string]any{phase: {"owner": owner}},
		SourceTemplate: owner, SourceLine: 7,
	}
}

func selectionProjection(tb testing.TB, patches ...StatusPatch) *StatusPatchProjection {
	tb.Helper()
	projected, err := NewStatusPatchProjection(patches)
	require.NoError(tb, err)
	return projected
}

func selectionSnapshot(tb testing.TB, plan *StatusPatchProjectionPlan, direct ...StatusPatch) *StatusPatchSnapshot {
	tb.Helper()
	collector := NewStatusPatchCollector()
	for index := range direct {
		patch := &direct[index]
		require.NoError(tb, collector.RegisterWithLineage(
			patch.Namespace, patch.Name, patch.APIVersion, patch.Kind, patch.UID, patch.ResourceVersion, patch.Variants,
		))
		collector.SetSource(patch.Namespace, patch.Name, patch.APIVersion, patch.Kind, patch.SourceTemplate, patch.SourceLine)
	}
	if plan != nil {
		replay, err := plan.PrepareReplay()
		require.NoError(tb, err)
		require.NoError(tb, collector.ReplayProjectionPlan(replay))
	}
	snapshot, err := collector.Snapshot()
	require.NoError(tb, err)
	return snapshot
}

func TestChangedPatchesIncludeExposedContributions(t *testing.T) {
	direct := selectionPatch("route", "rendered", "direct")
	first := selectionProjection(t, selectionPatch("route", "rendered", "first"))
	last := selectionProjection(t, selectionPatch("route", "rendered", "last"))
	plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "001", first)
	require.NoError(t, err)
	plan, err = plan.ReplaceEntry("routes", "002", last)
	require.NoError(t, err)
	withoutLast, err := plan.ReplaceEntry("routes", "002", nil)
	require.NoError(t, err)
	empty, err := withoutLast.ReplaceEntry("routes", "001", nil)
	require.NoError(t, err)

	tests := []struct {
		name     string
		previous *StatusPatchSnapshot
		current  *StatusPatchSnapshot
	}{
		{"earlier projection", selectionSnapshot(t, plan), selectionSnapshot(t, withoutLast)},
		{"direct behind projection", selectionSnapshot(t, withoutLast, direct), selectionSnapshot(t, empty, direct)},
		{"direct without plan", selectionSnapshot(t, plan, direct), selectionSnapshot(t, nil, direct)},
		{"removed direct source", selectionSnapshot(t, plan, direct), selectionSnapshot(t, plan)},
		{"added plan", selectionSnapshot(t, nil, direct), selectionSnapshot(t, plan, direct)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed, err := test.current.ChangedPatchesForPhase(test.previous, "rendered")
			require.NoError(t, err)
			full, err := test.current.PatchesForPhase("rendered")
			require.NoError(t, err)
			require.Len(t, full, 1)
			assert.Equal(t, full, changed)
		})
	}
}

func TestSelectedPatchesPreserveCompositeReplayOrder(t *testing.T) {
	first := selectionProjection(t,
		selectionPatch("z-last-name", "rendered", "first"),
		selectionPatch("a-first-name", "rendered", "first"),
	)
	second := selectionProjection(t, selectionPatch("z-last-name", "rendered", "second"))
	composite, err := NewStatusPatchProjectionGroup([]*StatusPatchProjection{first, second})
	require.NoError(t, err)
	plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "001", composite)
	require.NoError(t, err)
	last := selectionProjection(t, selectionPatch("a-first-name", "rendered", "last"))
	plan, err = plan.ReplaceEntry("routes", "002", last)
	require.NoError(t, err)
	snapshot := selectionSnapshot(t, plan)
	full, err := snapshot.PatchesForPhase("rendered")
	require.NoError(t, err)
	require.Len(t, full, 2)
	assert.Equal(t, []string{"z-last-name", "a-first-name"}, patchNames(full))
	targets := []StatusPatchTarget{full[1].Target(), full[0].Target(), full[1].Target(), {Name: "absent"}}
	for range 20 {
		selected, err := snapshot.PatchesForPhaseTargets("rendered", targets)
		require.NoError(t, err)
		assert.Equal(t, full, selected)
		selected[0].Variants["rendered"]["owner"] = "poison"
	}
	missingPhase, err := snapshot.PatchesForPhaseTargets("deployed", targets)
	require.NoError(t, err)
	assert.Empty(t, missingPhase)
	_, err = snapshot.PatchesForPhaseTargets("", targets)
	require.Error(t, err)
}

func TestChangedPatchesAcrossSkippedAndBranchedPlans(t *testing.T) {
	plan := NewStatusPatchProjectionPlan()
	for _, name := range []string{"a", "b", "c"} {
		var err error
		plan, err = plan.ReplaceEntry("routes", name, selectionProjection(t, selectionPatch(name, "rendered", "initial")))
		require.NoError(t, err)
	}
	first := selectionSnapshot(t, plan)
	changedB, err := plan.ReplaceEntry("routes", "b", selectionProjection(t, selectionPatch("b", "rendered", "changed")))
	require.NoError(t, err)
	removedC, err := changedB.ReplaceEntry("routes", "c", nil)
	require.NoError(t, err)
	addedD, err := removedC.ReplaceEntry("routes", "d", selectionProjection(t, selectionPatch("d", "rendered", "new")))
	require.NoError(t, err)
	latest := selectionSnapshot(t, addedD)
	changed, err := latest.ChangedPatchesForPhase(first, "rendered")
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "d"}, patchNames(changed))
	branch, err := plan.ReplaceEntry("routes", "a", selectionProjection(t, selectionPatch("a", "rendered", "branch")))
	require.NoError(t, err)
	branched := selectionSnapshot(t, branch)
	changed, err = branched.ChangedPatchesForPhase(latest, "rendered")
	require.NoError(t, err)
	full, err := branched.PatchesForPhase("rendered")
	require.NoError(t, err)
	assert.Equal(t, full, changed)
}

func TestChangedPatchesIgnoreRestoredAndRebuiltExactContributions(t *testing.T) {
	projected := selectionProjection(t, selectionPatch("route", "rendered", "stable"))
	entries := []StatusPatchProjectionPlanEntry{{Group: "routes", Entry: "route", Projection: projected}}
	plan, err := NewStatusPatchProjectionPlanFromEntries(entries)
	require.NoError(t, err)
	previous := selectionSnapshot(t, plan)
	removed, err := plan.ReplaceEntry("routes", "route", nil)
	require.NoError(t, err)
	restored, err := removed.ReplaceEntry("routes", "route", projected)
	require.NoError(t, err)
	rebuilt, err := NewStatusPatchProjectionPlanFromEntries(entries)
	require.NoError(t, err)
	for _, current := range []*StatusPatchProjectionPlan{restored, rebuilt} {
		changed, err := selectionSnapshot(t, current).ChangedPatchesForPhase(previous, "rendered")
		require.NoError(t, err)
		assert.Empty(t, changed)
	}
}

func TestChangedPatchesRespectRemovedPhases(t *testing.T) {
	rendered := selectionProjection(t, selectionPatch("route", "rendered", "rendered"))
	deployed := selectionProjection(t, selectionPatch("route", "deployed", "deployed"))
	plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "001", rendered)
	require.NoError(t, err)
	plan, err = plan.ReplaceEntry("routes", "002", deployed)
	require.NoError(t, err)
	previous := selectionSnapshot(t, plan)
	removed, err := plan.ReplaceEntry("routes", "001", nil)
	require.NoError(t, err)
	current := selectionSnapshot(t, removed)
	changed, err := current.ChangedPatchesForPhase(previous, "rendered")
	require.NoError(t, err)
	assert.Empty(t, changed)
	changed, err = current.ChangedPatchesForPhase(previous, "deployed")
	require.NoError(t, err)
	full, err := current.PatchesForPhase("deployed")
	require.NoError(t, err)
	require.Len(t, full, 1)
	assert.Equal(t, full, changed)
}

func TestSelectedPatchesRejectInvalidProvenance(t *testing.T) {
	patch := selectionPatch("route", "rendered", "source")
	targets := []StatusPatchTarget{patch.Target()}
	t.Run("copied snapshot", func(t *testing.T) {
		snapshot := *selectionSnapshot(t, nil, patch)
		_, err := snapshot.PatchesForPhaseTargets("rendered", targets)
		require.ErrorContains(t, err, "provenance")
	})
	t.Run("direct source", func(t *testing.T) {
		snapshot := selectionSnapshot(t, nil, patch)
		key := newStatusPatchIdentity(patch.Namespace, patch.Name, patch.APIVersion, patch.Kind)
		snapshot.collector.patches[key].SourceTemplate = "poison"
		_, err := snapshot.PatchesForPhaseTargets("rendered", targets)
		require.ErrorContains(t, err, "provenance")
	})
	t.Run("projected root", func(t *testing.T) {
		projected := selectionProjection(t, patch)
		plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "route", projected)
		require.NoError(t, err)
		snapshot := selectionSnapshot(t, plan)
		projected.integrity.root = nil
		_, err = snapshot.PatchesForPhaseTargets("rendered", targets)
		require.Error(t, err)
	})
	t.Run("composite leaf", func(t *testing.T) {
		leaf := selectionProjection(t, patch)
		composite, err := NewStatusPatchProjectionGroup([]*StatusPatchProjection{leaf})
		require.NoError(t, err)
		plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "route", composite)
		require.NoError(t, err)
		snapshot := selectionSnapshot(t, plan)
		leaf.integrity.root = nil
		_, err = snapshot.PatchesForPhaseTargets("rendered", targets)
		require.ErrorContains(t, err, "leaf provenance")
	})
}

func BenchmarkProjectedStatusDeltaByFleetSize(b *testing.B) {
	for _, count := range []int{300, 3000, 10000} {
		b.Run(fmt.Sprintf("targets=%d", count), func(b *testing.B) {
			entries := make([]StatusPatchProjectionPlanEntry, count)
			for index := range entries {
				name := fmt.Sprintf("route-%06d", index)
				entries[index] = StatusPatchProjectionPlanEntry{
					Group: "routes", Entry: name,
					Projection: selectionProjection(b, selectionPatch(name, "rendered", "initial")),
				}
			}
			plan, err := NewStatusPatchProjectionPlanFromEntries(entries)
			require.NoError(b, err)
			previous := selectionSnapshot(b, plan)
			replaced, err := plan.ReplaceEntry("routes", "route-000007",
				selectionProjection(b, selectionPatch("route-000007", "rendered", "changed")))
			require.NoError(b, err)
			current := selectionSnapshot(b, replaced)
			b.ReportAllocs()
			for b.Loop() {
				changed, err := current.ChangedPatchesForPhase(previous, "rendered")
				if err != nil || len(changed) != 1 {
					b.Fatalf("expected one changed patch, got %d: %v", len(changed), err)
				}
			}
		})
	}
}
