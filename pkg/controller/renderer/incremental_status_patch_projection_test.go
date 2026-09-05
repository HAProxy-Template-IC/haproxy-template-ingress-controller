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

package renderer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalStatusPatchProjectionReusesOnlyUnchangedGroupRoot(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "status", source: "routes", namespace: "default", name: "route",
		result: incrementalStatusPatchProjectionResult(t, "rendered", "before"),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	before, err := index.compiledStatusPatchProjection()
	require.NoError(t, err)
	_, beforeIndexed, exists := index.status.Root().Minimum()
	require.True(t, exists)
	require.NotNil(t, beforeIndexed.prepared)
	again, err := index.compiledStatusPatchProjection()
	require.NoError(t, err)
	assert.Same(t, before, again)

	unrelated := incrementalInstanceResult{
		component: "text", source: "routes", namespace: "default", name: "other",
		result: incrementalComponentResult{Text: "changed"},
	}
	statusUnchanged, err := index.replace(&unrelated, nil)
	require.NoError(t, err)
	unchanged, err := statusUnchanged.compiledStatusPatchProjection()
	require.NoError(t, err)
	assert.Same(t, before, unchanged)
	_, unchangedIndexed, exists := statusUnchanged.status.Root().Minimum()
	require.True(t, exists)
	assert.Same(t, beforeIndexed.prepared, unchangedIndexed.prepared)

	equal, err := statusUnchanged.replace(&instance, nil)
	require.NoError(t, err)
	_, equalIndexed, exists := equal.status.Root().Minimum()
	require.True(t, exists)
	assert.Same(t, beforeIndexed.prepared, equalIndexed.prepared)

	instance.result = incrementalStatusPatchProjectionResult(t, "rendered", "after")
	changed, err := equal.replace(&instance, nil)
	require.NoError(t, err)
	after, err := changed.compiledStatusPatchProjection()
	require.NoError(t, err)
	assert.NotSame(t, before, after)
	_, changedIndexed, exists := changed.status.Root().Minimum()
	require.True(t, exists)
	assert.NotSame(t, beforeIndexed.prepared, changedIndexed.prepared)
	parentAgain, err := index.compiledStatusPatchProjection()
	require.NoError(t, err)
	assert.Same(t, before, parentAgain)
}

func TestIncrementalStatusPatchPlanReplacesOnlyChangedInstance(t *testing.T) {
	plan := templating.NewStatusPatchProjectionPlan()
	index := newIncrementalGroupIndex()
	instances := []incrementalInstanceResult{
		incrementalStatusPatchProjectionInstance(t, "route-a", "a"),
		incrementalStatusPatchProjectionInstance(t, "route-b", "b"),
	}
	for instanceIndex := range instances {
		instance := &instances[instanceIndex]
		updated, err := index.replace(instance, nil)
		require.NoError(t, err)
		plan, err = replaceIncrementalStatusPatchPlanInstance(
			plan,
			"routes",
			index,
			updated,
			incrementalGroupInstanceID{
				component: instance.component,
				source:    instance.source,
				namespace: instance.namespace,
				name:      instance.name,
			},
		)
		require.NoError(t, err)
		index = updated
	}
	before := plan

	changed := incrementalStatusPatchProjectionInstance(t, "route-a", "changed")
	updated, err := index.replace(&changed, nil)
	require.NoError(t, err)
	plan, err = replaceIncrementalStatusPatchPlanInstance(
		plan,
		"routes",
		index,
		updated,
		incrementalGroupInstanceID{
			component: changed.component,
			source:    changed.source,
			namespace: changed.namespace,
			name:      changed.name,
		},
	)
	require.NoError(t, err)
	assert.NotSame(t, before, plan)

	collector := templating.NewStatusPatchCollector()
	_, err = stageIncrementalStatusPatchPlan(
		map[string]any{"statusPatchCollector": collector},
		plan,
	)
	require.NoError(t, err)
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 2)
	assert.Equal(t, "changed", patches[0].Variants["rendered"]["owner"])
	assert.Equal(t, "b", patches[1].Variants["rendered"]["owner"])
}

func TestIncrementalStatusPatchPlanBulkBuildMatchesSequentialPlan(t *testing.T) {
	plan := templating.NewStatusPatchProjectionPlan()
	index := newIncrementalGroupIndex()
	instances := []incrementalInstanceResult{
		incrementalStatusPatchProjectionInstance(t, "route-b", "b"),
		incrementalStatusPatchProjectionInstance(t, "route-a", "a"),
	}
	for instanceIndex := range instances {
		instance := &instances[instanceIndex]
		updated, err := index.replace(instance, nil)
		require.NoError(t, err)
		plan, err = replaceIncrementalStatusPatchPlanInstance(
			plan,
			"routes",
			index,
			updated,
			incrementalGroupInstanceID{
				component: instance.component,
				source:    instance.source,
				namespace: instance.namespace,
				name:      instance.name,
			},
		)
		require.NoError(t, err)
		index = updated
	}

	bulk, err := newIncrementalStatusPatchPlanFromIndexes(map[string]*incrementalGroupIndex{
		"empty":  newIncrementalGroupIndex(),
		"routes": index,
	})
	require.NoError(t, err)
	sequentialCollector := templating.NewStatusPatchCollector()
	_, err = stageIncrementalStatusPatchPlan(
		map[string]any{"statusPatchCollector": sequentialCollector},
		plan,
	)
	require.NoError(t, err)
	bulkCollector := templating.NewStatusPatchCollector()
	_, err = stageIncrementalStatusPatchPlan(
		map[string]any{"statusPatchCollector": bulkCollector},
		bulk,
	)
	require.NoError(t, err)
	sequential, err := sequentialCollector.Patches()
	require.NoError(t, err)
	batched, err := bulkCollector.Patches()
	require.NoError(t, err)
	assert.Equal(t, sequential, batched)
}

func TestIncrementalStatusPatchPlanBulkBuildRejectsPreparedCallSubstitution(t *testing.T) {
	index := incrementalStatusPatchProjectionIndex(t, "stable")
	location, indexed, exists := index.status.Root().Minimum()
	require.True(t, exists)
	copied := *indexed.prepared
	indexed.prepared = &copied
	txn := index.status.Txn()
	txn.Insert(location, indexed)
	poisoned := *index
	poisoned.status = txn.Commit()
	poisoned.authenticate()

	plan, err := newIncrementalStatusPatchPlanFromIndexes(map[string]*incrementalGroupIndex{
		"routes": &poisoned,
	})
	require.ErrorContains(t, err, "invalid provenance")
	assert.Nil(t, plan)
}

func TestIncrementalStatusPatchPlanBulkBuildRejectsLateConflictAtomically(t *testing.T) {
	first := incrementalStatusPatchProjectionIndex(t, "first")
	second := incrementalStatusPatchProjectionIndex(t, "second")

	plan, err := newIncrementalStatusPatchPlanFromIndexes(map[string]*incrementalGroupIndex{
		"first":  first,
		"second": second,
	})
	require.ErrorContains(t, err, `phase "rendered" has conflicting groups "first" and "second"`)
	assert.Nil(t, plan)
	require.NoError(t, first.validateAuthentication())
	require.NoError(t, second.validateAuthentication())
}

func TestIncrementalStatusPatchPlanBootstrapPublishesOnlyAfterCompleteBuild(t *testing.T) {
	original := templating.NewStatusPatchProjectionPlan()
	session := &incrementalRenderSession{
		groupIndexes: map[string]*incrementalGroupIndex{
			"routes": incrementalStatusPatchProjectionIndex(t, "stable"),
		},
		statusPlan:                 original,
		statusPlanBootstrapPending: true,
	}
	require.NoError(t, session.finalizeStatusPatchPlanBootstrap())
	assert.False(t, session.statusPlanBootstrapPending)
	assert.NotSame(t, original, session.statusPlan)
	committed := session.statusPlan
	require.NoError(t, session.finalizeStatusPatchPlanBootstrap())
	assert.Same(t, committed, session.statusPlan)

	first := incrementalStatusPatchProjectionIndex(t, "first")
	second := incrementalStatusPatchProjectionIndex(t, "second")
	failed := &incrementalRenderSession{
		groupIndexes:               map[string]*incrementalGroupIndex{"first": first, "second": second},
		statusPlan:                 original,
		statusPlanBootstrapPending: true,
	}
	err := failed.finalizeStatusPatchPlanBootstrap()
	require.ErrorContains(t, err, "conflicting groups")
	assert.True(t, failed.statusPlanBootstrapPending)
	assert.Same(t, original, failed.statusPlan)
}

func BenchmarkIncrementalStatusPatchPlanOneChange3000(b *testing.B) {
	plan := templating.NewStatusPatchProjectionPlan()
	index := newIncrementalGroupIndex()
	for instanceIndex := range 3000 {
		instance := incrementalStatusPatchProjectionInstance(
			b,
			fmt.Sprintf("route-%04d", instanceIndex),
			fmt.Sprintf("owner-%04d", instanceIndex),
		)
		updated, err := index.replace(&instance, nil)
		require.NoError(b, err)
		plan, err = replaceIncrementalStatusPatchPlanInstance(
			plan,
			"routes",
			index,
			updated,
			incrementalGroupInstanceID{
				component: instance.component,
				source:    instance.source,
				namespace: instance.namespace,
				name:      instance.name,
			},
		)
		require.NoError(b, err)
		index = updated
	}
	changed := incrementalStatusPatchProjectionInstance(b, "route-1500", "changed")
	updated, err := index.replace(&changed, nil)
	require.NoError(b, err)
	id := incrementalGroupInstanceID{
		component: changed.component,
		source:    changed.source,
		namespace: changed.namespace,
		name:      changed.name,
	}
	baseCollector := templating.NewStatusPatchCollector()
	_, err = stageIncrementalStatusPatchPlan(
		map[string]any{"statusPatchCollector": baseCollector},
		plan,
	)
	require.NoError(b, err)
	previous, err := baseCollector.Snapshot()
	require.NoError(b, err)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, replaceErr := replaceIncrementalStatusPatchPlanInstance(plan, "routes", index, updated, id)
		if replaceErr != nil {
			b.Fatal(replaceErr)
		}
		collector := templating.NewStatusPatchCollector()
		if _, stageErr := stageIncrementalStatusPatchPlan(
			map[string]any{"statusPatchCollector": collector},
			next,
		); stageErr != nil {
			b.Fatal(stageErr)
		}
		if _, snapshotErr := collector.Snapshot(previous); snapshotErr != nil {
			b.Fatal(snapshotErr)
		}
	}
}

func BenchmarkIncrementalStatusPatchPlanColdBuild3000(b *testing.B) {
	index := newIncrementalGroupIndex()
	for instanceIndex := range 3000 {
		instance := incrementalStatusPatchProjectionInstance(
			b,
			fmt.Sprintf("route-%04d", instanceIndex),
			fmt.Sprintf("owner-%04d", instanceIndex),
		)
		var err error
		index, err = index.replace(&instance, nil)
		require.NoError(b, err)
	}
	b.Run("persistent", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			plan := templating.NewStatusPatchProjectionPlan()
			var buildErr error
			index.status.Root().Walk(func(_ []byte, indexed incrementalIndexedStatusPatchCall) bool {
				plan, buildErr = plan.ReplaceEntry("routes", indexed.location, indexed.prepared.projection)
				return buildErr != nil
			})
			if buildErr != nil {
				b.Fatal(buildErr)
			}
		}
	})
	b.Run("bulk", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := newIncrementalStatusPatchPlanFromIndexes(
				map[string]*incrementalGroupIndex{"routes": index},
			); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestIncrementalStatusPatchProjectionRejectsPreparedCallSubstitution(t *testing.T) {
	index := incrementalStatusPatchProjectionIndex(t, "stable")
	other := incrementalStatusPatchProjectionIndex(t, "stable")
	location, indexed, exists := index.status.Root().Minimum()
	require.True(t, exists)
	_, foreign, exists := other.status.Root().Minimum()
	require.True(t, exists)
	indexed.prepared = foreign.prepared
	txn := index.status.Txn()
	txn.Insert(location, indexed)
	poisoned := *index
	poisoned.status = txn.Commit()
	poisoned.authenticate()

	_, err := poisoned.compiledStatusPatchProjection()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalStatusPatchProjectionRejectsCopiedPreparedCall(t *testing.T) {
	index := incrementalStatusPatchProjectionIndex(t, "stable")
	location, indexed, exists := index.status.Root().Minimum()
	require.True(t, exists)
	copied := *indexed.prepared
	indexed.prepared = &copied
	txn := index.status.Txn()
	txn.Insert(location, indexed)
	poisoned := *index
	poisoned.status = txn.Commit()
	poisoned.authenticate()

	_, err := poisoned.compiledStatusPatchProjection()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalStatusPatchProjectionRejectsMemoPoison(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "status", source: "routes", namespace: "default", name: "route",
		result: incrementalStatusPatchProjectionResult(t, "rendered", "stable"),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	_, err = index.compiledStatusPatchProjection()
	require.NoError(t, err)
	require.NotNil(t, index.memo.state.status)
	index.memo.state.status.seal = nil

	_, err = index.compiledStatusPatchProjection()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalStatusPatchProjectionPreflightsConflictsBeforeCollectorMutation(t *testing.T) {
	first := incrementalStatusPatchProjectionIndex(t, "same")
	second := incrementalStatusPatchProjectionIndex(t, "same")
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "baseline"}},
	))
	collector.SetSource("default", "route", "example.test/v1", "Route", "direct", 4)

	err := replayIncrementalStatusPatches(
		map[string]any{"statusPatchCollector": collector},
		map[string]*incrementalGroupIndex{"a": first, "b": second},
	)
	require.ErrorContains(t, err, `phase "rendered" has conflicting groups "a" and "b"`)
	patches, snapshotErr := collector.Patches()
	require.NoError(t, snapshotErr)
	require.Len(t, patches, 1)
	assert.Equal(t, "baseline", patches[0].Variants["rendered"]["owner"])
	assert.Equal(t, "direct", patches[0].SourceTemplate)
}

func TestIncrementalStatusPatchProjectionOverwritesDirectBaselineWithoutConflict(t *testing.T) {
	index := incrementalStatusPatchProjectionIndex(t, "incremental")
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.RegisterWithLineage(
		"default", "route", "example.test/v1", "Route", "uid-route", "rv-1",
		map[string]map[string]any{"rendered": {"owner": "direct"}},
	))
	collector.SetSource("default", "route", "example.test/v1", "Route", "direct", 4)

	require.NoError(t, replayIncrementalStatusPatches(
		map[string]any{"statusPatchCollector": collector},
		map[string]*incrementalGroupIndex{"group": index},
	))
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "incremental", patches[0].Variants["rendered"]["owner"])
	assert.Equal(t, "direct", patches[0].SourceTemplate)
}

func TestIncrementalStatusPatchProjectionOutputCannotPoisonWarmReplay(t *testing.T) {
	index := incrementalStatusPatchProjectionIndex(t, "stable")
	render := func() []templating.StatusPatch {
		collector := templating.NewStatusPatchCollector()
		require.NoError(t, replayIncrementalStatusPatches(
			map[string]any{"statusPatchCollector": collector},
			map[string]*incrementalGroupIndex{"group": index},
		))
		patches, err := collector.Patches()
		require.NoError(t, err)
		return patches
	}
	first := render()
	first[0].Variants["rendered"]["owner"] = "poison"
	second := render()
	assert.Equal(t, "stable", second[0].Variants["rendered"]["owner"])
}

func TestIncrementalStatusPatchCallPreservesLineageAcrossBothDecoders(t *testing.T) {
	result := incrementalStatusPatchProjectionResultWithLineage(
		t, "rendered", "stable", "uid-route", "rv-17",
	)
	require.Len(t, result.StatusPatches, 1)
	call := &result.StatusPatches[0]

	decoded, err := decodeIncrementalStatusPatchCall(call)
	require.NoError(t, err)
	assert.Equal(t, "uid-route", decoded.UID)
	assert.Equal(t, "rv-17", decoded.ResourceVersion)
	projected, err := decodeIncrementalStatusPatchProjectionCall(call)
	require.NoError(t, err)
	assert.Equal(t, decoded, projected)

	changed := *call
	changed.ResourceVersion = "rv-18"
	changedDigest, err := digestIncrementalStatusPatchCalls([]incrementalStatusPatchCall{changed})
	require.NoError(t, err)
	assert.NotEqual(t, result.StatusPatchDigest, changedDigest)
}

func TestIncrementalStatusPatchReplayRejectsCrossPhaseLineageConflictAtomically(t *testing.T) {
	first := incrementalStatusPatchProjectionIndexWithLineage(
		t, "rendered", "first", "uid-route", "rv-1",
	)
	second := incrementalStatusPatchProjectionIndexWithLineage(
		t, "deployed", "second", "uid-route", "rv-2",
	)
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "baseline", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "baseline"}},
	))

	err := replayIncrementalStatusPatches(
		map[string]any{"statusPatchCollector": collector},
		map[string]*incrementalGroupIndex{"first": first, "second": second},
	)
	require.ErrorContains(t, err, "conflicting source lineage")
	patches, snapshotErr := collector.Patches()
	require.NoError(t, snapshotErr)
	require.Len(t, patches, 1)
	assert.Equal(t, "baseline", patches[0].Name)
}

func TestIncrementalStatusPatchReplayPreservesMissingLineage(t *testing.T) {
	index := incrementalStatusPatchProjectionIndexWithLineage(t, "rendered", "offline", "", "")
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, replayIncrementalStatusPatches(
		map[string]any{"statusPatchCollector": collector},
		map[string]*incrementalGroupIndex{"group": index},
	))
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Empty(t, patches[0].UID)
	assert.Empty(t, patches[0].ResourceVersion)
}

func incrementalStatusPatchProjectionIndex(tb testing.TB, owner string) *incrementalGroupIndex {
	tb.Helper()
	return incrementalStatusPatchProjectionIndexWithLineage(tb, "rendered", owner, "uid-route", "rv-1")
}

func incrementalStatusPatchProjectionIndexWithLineage(
	tb testing.TB,
	phase, owner, uid, resourceVersion string,
) *incrementalGroupIndex {
	tb.Helper()
	instance := incrementalInstanceResult{
		component: "status", source: "routes", namespace: "default", name: "route",
		result: incrementalStatusPatchProjectionResultWithLineage(tb, phase, owner, uid, resourceVersion),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(tb, err)
	return index
}

func incrementalStatusPatchProjectionResult(tb testing.TB, phase, owner string) incrementalComponentResult {
	tb.Helper()
	return incrementalStatusPatchProjectionResultWithLineage(tb, phase, owner, "uid-route", "rv-1")
}

func incrementalStatusPatchProjectionResultWithLineage(
	tb testing.TB,
	phase, owner, uid, resourceVersion string,
) incrementalComponentResult {
	tb.Helper()
	variants, err := encodeIncrementalStatusPatchVariants(map[string]map[string]any{
		phase: {"owner": owner},
	})
	require.NoError(tb, err)
	calls := []incrementalStatusPatchCall{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		UID: uid, ResourceVersion: resourceVersion,
		Variants: variants, SourceTemplate: "component", SourceLine: 7,
	}}
	digest, err := digestIncrementalStatusPatchCalls(calls)
	require.NoError(tb, err)
	return incrementalComponentResult{StatusPatches: calls, StatusPatchDigest: digest}
}

func incrementalStatusPatchProjectionInstance(
	tb testing.TB,
	name, owner string,
) incrementalInstanceResult {
	tb.Helper()
	result := incrementalStatusPatchProjectionResultWithLineage(
		tb,
		"rendered",
		owner,
		"uid-"+name,
		"rv-"+owner,
	)
	result.StatusPatches[0].Name = name
	digest, err := digestIncrementalStatusPatchCalls(result.StatusPatches)
	require.NoError(tb, err)
	result.StatusPatchDigest = digest
	return incrementalInstanceResult{
		component: "status",
		source:    "routes",
		namespace: "default",
		name:      name,
		result:    result,
	}
}
