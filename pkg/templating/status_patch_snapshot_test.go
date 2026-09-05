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

	projectionstate "gitlab.com/haproxy-haptic/haptic/pkg/templating/internal/statuspatchprojection"
)

const statusPatchBenchmarkPatchCount = 3000

func newStatusPatchBenchmarkCollector(b *testing.B) *StatusPatchCollector {
	b.Helper()
	collector := NewStatusPatchCollector()
	for index := range statusPatchBenchmarkPatchCount {
		err := collector.Register(
			"default", fmt.Sprintf("route-%06d", index), "example.test/v1", "Route",
			map[string]map[string]any{
				"rendered": {"conditions": []any{map[string]any{"type": "Accepted", "generation": index}}},
				"deployed": {"conditions": []any{map[string]any{"type": "Programmed", "generation": index}}},
			},
		)
		require.NoError(b, err)
	}
	return collector
}

func BenchmarkStatusPatchCollectorResultBoundary(b *testing.B) {
	b.Run("detached", benchmarkStatusPatchDetached)
	b.Run("snapshot", benchmarkStatusPatchSnapshot)
	b.Run("snapshot-first", benchmarkStatusPatchSnapshotFirst)
	b.Run("phase", benchmarkStatusPatchPhase)
}

func benchmarkStatusPatchDetached(b *testing.B) {
	collector := newStatusPatchBenchmarkCollector(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		patches, err := collector.Patches()
		if err != nil {
			b.Fatal(err)
		}
		if len(patches) != statusPatchBenchmarkPatchCount {
			b.Fatalf("got %d patches", len(patches))
		}
	}
}

func benchmarkStatusPatchSnapshot(b *testing.B) {
	collector := newStatusPatchBenchmarkCollector(b)
	snapshot, err := collector.Snapshot()
	require.NoError(b, err)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		current, snapshotErr := collector.Snapshot()
		if snapshotErr != nil {
			b.Fatal(snapshotErr)
		}
		if current != snapshot {
			b.Fatal("snapshot identity changed")
		}
	}
}

func benchmarkStatusPatchSnapshotFirst(b *testing.B) {
	collectors := make([]*StatusPatchCollector, b.N)
	for index := range collectors {
		collectors[index] = newStatusPatchBenchmarkCollector(b)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		snapshot, err := collectors[index].Snapshot()
		if err != nil {
			b.Fatal(err)
		}
		if snapshot == nil {
			b.Fatal("snapshot is nil")
		}
	}
}

func benchmarkStatusPatchPhase(b *testing.B) {
	collector := newStatusPatchBenchmarkCollector(b)
	snapshot, err := collector.Snapshot()
	require.NoError(b, err)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		patches, phaseErr := snapshot.PatchesForPhase("rendered")
		if phaseErr != nil {
			b.Fatal(phaseErr)
		}
		if len(patches) != statusPatchBenchmarkPatchCount {
			b.Fatalf("got %d patches", len(patches))
		}
	}
}

func TestStatusPatchSnapshotSealsWithoutMaterializing(t *testing.T) {
	collector := NewStatusPatchCollector()
	replay, err := mustStatusPatchProjection(t, "cached").PrepareReplay()
	require.NoError(t, err)
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))

	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	again, err := collector.Snapshot()
	require.NoError(t, err)
	assert.Same(t, snapshot, again)
	count, err := snapshot.Len()
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	require.ErrorContains(t, collector.Register(
		"default", "later", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "later"}},
	), "sealed")
}

func TestStatusPatchSnapshotRootIdentityAndExactEquality(t *testing.T) {
	newSnapshot := func(t *testing.T, owner string) *StatusPatchSnapshot {
		t.Helper()
		collector := NewStatusPatchCollector()
		require.NoError(t, collector.Register(
			"default", "route", "example.test/v1", "Route",
			map[string]map[string]any{"rendered": {"owner": owner}},
		))
		snapshot, err := collector.Snapshot()
		require.NoError(t, err)
		return snapshot
	}

	first := newSnapshot(t, "stable")
	same, err := first.SameRoot(first)
	require.NoError(t, err)
	assert.True(t, same)

	foreignExact := newSnapshot(t, "stable")
	same, err = first.SameRoot(foreignExact)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := first.ExactEqual(foreignExact)
	require.NoError(t, err)
	assert.True(t, equal)

	changed := newSnapshot(t, "changed")
	equal, err = first.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)

	var nilSnapshot *StatusPatchSnapshot
	_, err = nilSnapshot.SameRoot(first)
	require.ErrorContains(t, err, "invalid provenance")
	_, err = first.ExactEqual(nil)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchSnapshotReusesPreviousExactRoot(t *testing.T) {
	projection := mustStatusPatchProjection(t, "stable")
	firstReplay, err := projection.PrepareReplay()
	require.NoError(t, err)
	firstCollector := NewStatusPatchCollector()
	require.NoError(t, firstCollector.ReplayProjections([]*StatusPatchProjectionReplay{firstReplay}))
	first, err := firstCollector.Snapshot()
	require.NoError(t, err)

	secondReplay, err := projection.PrepareReplay()
	require.NoError(t, err)
	secondCollector := NewStatusPatchCollector()
	require.NoError(t, secondCollector.ReplayProjections([]*StatusPatchProjectionReplay{secondReplay}))
	second, err := secondCollector.Snapshot(first)
	require.NoError(t, err)
	assert.Same(t, first, second)

	patches, err := secondCollector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "stable", patches[0].Variants["rendered"]["owner"])
	require.ErrorContains(t, secondCollector.Register(
		"default", "later", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "later"}},
	), "sealed")
}

func TestStatusPatchSnapshotDoesNotReuseChangedOrRecurringRoot(t *testing.T) {
	newSnapshot := func(t *testing.T, owner string, previous *StatusPatchSnapshot) *StatusPatchSnapshot {
		t.Helper()
		collector := NewStatusPatchCollector()
		require.NoError(t, collector.Register(
			"default", "route", "example.test/v1", "Route",
			map[string]map[string]any{"rendered": {"owner": owner}},
		))
		snapshot, err := collector.Snapshot(previous)
		require.NoError(t, err)
		return snapshot
	}

	firstA := newSnapshot(t, "a", nil)
	b := newSnapshot(t, "b", firstA)
	secondA := newSnapshot(t, "a", b)
	assert.NotSame(t, firstA, b)
	assert.NotSame(t, firstA, secondA)
	assert.NotSame(t, b, secondA)

	equal, err := firstA.ExactEqual(secondA)
	require.NoError(t, err)
	assert.True(t, equal)
}

func TestStatusPatchSnapshotLineageChangeAndRecurrenceNeverReuseRoot(t *testing.T) {
	newSnapshot := func(
		t *testing.T,
		uid, resourceVersion string,
		previous *StatusPatchSnapshot,
	) *StatusPatchSnapshot {
		t.Helper()
		collector := NewStatusPatchCollector()
		require.NoError(t, collector.RegisterWithLineage(
			"default", "route", "example.test/v1", "Route", uid, resourceVersion,
			map[string]map[string]any{"rendered": {"owner": "stable"}},
		))
		snapshot, err := collector.Snapshot(previous)
		require.NoError(t, err)
		return snapshot
	}

	firstA := newSnapshot(t, "uid-route", "rv-a", nil)
	b := newSnapshot(t, "uid-route", "rv-b", firstA)
	secondA := newSnapshot(t, "uid-route", "rv-a", b)
	assert.NotSame(t, firstA, b)
	assert.NotSame(t, firstA, secondA)
	assert.NotSame(t, b, secondA)

	equal, err := firstA.ExactEqual(secondA)
	require.NoError(t, err)
	assert.True(t, equal)
	patches, err := secondA.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "uid-route", patches[0].UID)
	assert.Equal(t, "rv-a", patches[0].ResourceVersion)
}

func TestStatusPatchSnapshotMissingLineageNeverReusesPreviousRoot(t *testing.T) {
	tests := map[string]struct {
		uid             string
		resourceVersion string
	}{
		"both missing":             {},
		"uid missing":              {resourceVersion: "rv-1"},
		"resource version missing": {uid: "uid-route"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			newSnapshot := func(previous *StatusPatchSnapshot) *StatusPatchSnapshot {
				collector := NewStatusPatchCollector()
				require.NoError(t, collector.RegisterWithLineage(
					"default", "route", "example.test/v1", "Route", test.uid, test.resourceVersion,
					map[string]map[string]any{"rendered": {"owner": "stable"}},
				))
				snapshot, err := collector.Snapshot(previous)
				require.NoError(t, err)
				return snapshot
			}

			first := newSnapshot(nil)
			second := newSnapshot(first)
			assert.NotSame(t, first, second)
			equal, err := first.ExactEqual(second)
			require.NoError(t, err)
			assert.True(t, equal)
		})
	}
}

func TestStatusPatchSnapshotRejectsCopiedPrevious(t *testing.T) {
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	previous, err := collector.Snapshot()
	require.NoError(t, err)
	copied := *previous

	next := NewStatusPatchCollector()
	require.NoError(t, next.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	_, err = next.Snapshot(&copied)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchSnapshotMaterializesOnlyRequestedPhase(t *testing.T) {
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "first", "example.test/v1", "Route",
		map[string]map[string]any{
			"rendered": {"owner": "rendered"},
			"deployed": {"owner": "deployed"},
		},
	))
	require.NoError(t, collector.Register(
		"default", "second", "example.test/v1", "Route",
		map[string]map[string]any{"deployed": {"owner": "second"}},
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	rendered, err := snapshot.PatchesForPhase("rendered")
	require.NoError(t, err)
	require.Len(t, rendered, 1)
	assert.Equal(t, "first", rendered[0].Name)
	assert.Equal(t, map[string]map[string]any{"rendered": {"owner": "rendered"}}, rendered[0].Variants)

	deployed, err := snapshot.PatchesForPhase("deployed")
	require.NoError(t, err)
	require.Len(t, deployed, 2)
	assert.Equal(t, []string{"first", "second"}, []string{deployed[0].Name, deployed[1].Name})
	deployed[0].Variants["deployed"]["owner"] = "poison"
	again, err := snapshot.PatchesForPhase("deployed")
	require.NoError(t, err)
	assert.Equal(t, "deployed", again[0].Variants["deployed"]["owner"])
}

func TestStatusPatchSnapshotRejectsCopiedSubstitutedAndMutatedState(t *testing.T) {
	firstCollector := NewStatusPatchCollector()
	require.NoError(t, firstCollector.Register(
		"default", "first", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "first"}},
	))
	first, err := firstCollector.Snapshot()
	require.NoError(t, err)
	secondCollector := NewStatusPatchCollector()
	require.NoError(t, secondCollector.Register(
		"default", "second", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "second"}},
	))
	second, err := secondCollector.Snapshot()
	require.NoError(t, err)

	copied := *first
	_, err = copied.Patches()
	require.ErrorContains(t, err, "invalid provenance")

	substituted := *first
	substituted.seal = &substituted
	substituted.collector = second.collector
	_, err = substituted.Patches()
	require.ErrorContains(t, err, "invalid provenance")

	first.count++
	_, err = first.Patches()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchSnapshotRejectsMutatedProjection(t *testing.T) {
	projection := mustStatusPatchProjection(t, "stable")
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	projection.integrity.root = nil
	_, err = snapshot.PatchesForPhase("rendered")
	require.ErrorContains(t, err, "integrity")
}

func TestStatusPatchSnapshotRejectsMutatedCompositePart(t *testing.T) {
	part := mustStatusPatchProjection(t, "stable")
	composite, err := NewStatusPatchProjectionGroup([]*StatusPatchProjection{part})
	require.NoError(t, err)
	replay, err := composite.PrepareReplay()
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	composite.integrity.root = part.root
	_, err = snapshot.PatchesForPhase("rendered")
	require.ErrorContains(t, err, "integrity")
}

func TestStatusPatchSnapshotRejectsForeignVariantSubstitution(t *testing.T) {
	newProjectedSnapshot := func(t *testing.T, owner string) (*StatusPatchCollector, *StatusPatchSnapshot) {
		t.Helper()
		projection := mustStatusPatchProjection(t, owner)
		replay, err := projection.PrepareReplay()
		require.NoError(t, err)
		collector := NewStatusPatchCollector()
		require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))
		snapshot, err := collector.Snapshot()
		require.NoError(t, err)
		return collector, snapshot
	}

	firstCollector, first := newProjectedSnapshot(t, "first")
	secondCollector, _ := newProjectedSnapshot(t, "second")
	key := newStatusPatchIdentity("default", "route", "example.test/v1", "Route")
	foreign := secondCollector.patches[key].Variants["rendered"]
	firstCollector.patches[key].Variants["rendered"] = foreign

	_, err := first.PatchesForPhase("rendered")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchSnapshotRejectsProjectedValueSubstitution(t *testing.T) {
	firstProjection := mustStatusPatchProjection(t, "first")
	firstReplay, err := firstProjection.PrepareReplay()
	require.NoError(t, err)
	secondProjection := mustStatusPatchProjection(t, "second")
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{firstReplay}))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	key := newStatusPatchIdentity("default", "route", "example.test/v1", "Route")
	variant := collector.patches[key].Variants["rendered"]
	var foreign projectionstate.PhaseView
	require.NoError(t, secondProjection.visitPatches(func(
		_ *StatusPatchProjection,
		patch projectionstate.PatchView,
	) error {
		return patch.VisitPhases(func(phase projectionstate.PhaseView) error {
			foreign = phase
			return nil
		})
	}))
	variant.projected = foreign
	collector.patches[key].Variants["rendered"] = variant

	_, err = snapshot.PatchesForPhase("rendered")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchSnapshotRejectsMetadataAndPatchSubstitution(t *testing.T) {
	newDirectSnapshot := func(t *testing.T, owner string) (*StatusPatchCollector, *StatusPatchSnapshot) {
		t.Helper()
		collector := NewStatusPatchCollector()
		require.NoError(t, collector.Register(
			"default", "route", "example.test/v1", "Route",
			map[string]map[string]any{"rendered": {"owner": owner}},
		))
		collector.SetSource("default", "route", "example.test/v1", "Route", owner, 1)
		snapshot, err := collector.Snapshot()
		require.NoError(t, err)
		return collector, snapshot
	}

	firstCollector, first := newDirectSnapshot(t, "first")
	key := newStatusPatchIdentity("default", "route", "example.test/v1", "Route")
	firstCollector.patches[key].SourceTemplate = "poison"
	_, err := first.Patches()
	require.ErrorContains(t, err, "invalid provenance")

	firstCollector, first = newDirectSnapshot(t, "first")
	secondCollector, _ := newDirectSnapshot(t, "second")
	firstCollector.patches[key] = secondCollector.patches[key]
	_, err = first.Patches()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchSnapshotConcurrentPhaseMaterializationIsDetached(t *testing.T) {
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	const workers = 32
	errorsByWorker := make(chan error, workers)
	for range workers {
		go func() {
			patches, materializeErr := snapshot.PatchesForPhase("rendered")
			if materializeErr == nil {
				patches[0].Variants["rendered"]["owner"] = "caller-local"
			}
			errorsByWorker <- materializeErr
		}()
	}
	for range workers {
		require.NoError(t, <-errorsByWorker)
	}
	patches, err := snapshot.PatchesForPhase("rendered")
	require.NoError(t, err)
	assert.Equal(t, "stable", patches[0].Variants["rendered"]["owner"])
}
