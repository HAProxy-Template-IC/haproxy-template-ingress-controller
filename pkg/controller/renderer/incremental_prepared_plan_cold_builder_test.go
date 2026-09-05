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
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalPreparedPlanColdBuilderMatchesExactBootstrap(t *testing.T) {
	orders := [][]string{
		{"routing", "second"},
		{"second", "routing"},
	}
	baseline := ""
	for _, order := range orders {
		t.Run(fmt.Sprintf("%v", order), func(t *testing.T) {
			fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{6, 2, 4, 0, 5, 1, 3})
			builder := stageIncrementalPreparedPlanColdFixture(t, fixture, order)
			plan, accumulated, err := builder.finalize(
				fixture.groups,
				fixture.indexes,
				fixture.components,
				fixture.results.Root(),
				fixture.graph,
			)
			require.NoError(t, err)
			assert.True(t, accumulated)
			requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, plan, fixture)
			config := renderIncrementalPreparedPlanBootstrap(t, plan, fixture)
			if baseline == "" {
				baseline = config
			}
			assert.Equal(t, baseline, config)
		})
	}
}

func TestIncrementalPreparedPlanColdBuilderPreparesGroupsConcurrently(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	builder, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(t, err)
	batches := make([]*incrementalPreparedPlanColdBatch, len(fixture.groups))
	errs := make([]error, len(fixture.groups))
	var wait sync.WaitGroup
	for groupIndex, group := range fixture.groups {
		wait.Add(1)
		go func() {
			defer wait.Done()
			additions := incrementalPreparedPlanColdFixtureGroupAdditions(fixture, group)
			batches[groupIndex], errs[groupIndex] = builder.prepareValidatedGroupAdditions(
				group, fixture.indexes[group], additions,
			)
		}()
	}
	wait.Wait()
	for groupIndex := range fixture.groups {
		require.NoError(t, errs[groupIndex])
		require.NoError(t, builder.commit(batches[groupIndex]))
	}
	plan, accumulated, err := builder.finalize(
		fixture.groups,
		fixture.indexes,
		fixture.components,
		fixture.results.Root(),
		fixture.graph,
	)
	require.NoError(t, err)
	assert.True(t, accumulated)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, plan, fixture)
}

func TestIncrementalPreparedPlanColdBuilderRejectsSwappedStructuredResult(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	builder, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(t, err)
	first := fixture.specs[0]
	second := fixture.specs[1]
	component := fixture.components[first.component]
	batch, err := builder.prepareValidatedGroupAdditions(
		component.group,
		fixture.indexes[component.group],
		[]incrementalPreparedPlanGroupAddition{{
			component: &component,
			id: incrementalGroupInstanceID{
				component: first.component,
				source:    first.source,
				namespace: first.namespace,
				name:      first.name,
			},
			result: &second.result,
		}},
	)
	require.Nil(t, batch)
	require.ErrorContains(t, err, "does not match its assembly index")
}

func TestIncrementalPreparedPlanColdBuilderFallsBackOnIncompleteCoverage(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	builder, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(t, err)
	plan, accumulated, err := builder.finalize(
		fixture.groups,
		fixture.indexes,
		fixture.components,
		fixture.results.Root(),
		fixture.graph,
	)
	require.NoError(t, err)
	assert.False(t, accumulated)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, plan, fixture)
}

func TestIncrementalPreparedPlanColdBuilderBatchIsAtomicAndBound(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	first, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(t, err)
	second, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(t, err)
	batch := prepareIncrementalPreparedPlanColdFixtureGroup(t, first, fixture, "routing")
	require.ErrorContains(t, second.commit(batch), "invalid provenance")
	require.NoError(t, first.commit(batch))
	require.ErrorContains(t, first.commit(batch), "invalid provenance")

	plan, accumulated, err := first.finalize(
		fixture.groups,
		fixture.indexes,
		fixture.components,
		fixture.results.Root(),
		fixture.graph,
	)
	require.NoError(t, err)
	assert.False(t, accumulated)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, plan, fixture)
}

func TestIncrementalPreparedPlanColdBuilderOwnsProjectedValues(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	builder, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(t, err)
	batch := prepareIncrementalPreparedPlanColdFixtureGroup(t, builder, fixture, "routing")
	require.NoError(t, builder.commit(batch))
	fixture.specs[0].result.BackendPlan[0].Backend.Backend.Name = "poisoned-after-stage"
	secondBatch := prepareIncrementalPreparedPlanColdFixtureGroup(t, builder, fixture, "second")
	require.NoError(t, builder.commit(secondBatch))

	plan, accumulated, err := builder.finalize(
		fixture.groups,
		fixture.indexes,
		fixture.components,
		fixture.results.Root(),
		fixture.graph,
	)
	require.NoError(t, err)
	assert.True(t, accumulated)
	config := renderIncrementalPreparedPlanBootstrap(t, plan, fixture)
	assert.NotContains(t, config, "poisoned-after-stage")
}

func BenchmarkIncrementalPreparedPlanColdFinalize3000(b *testing.B) {
	fixture := newIncrementalPreparedPlanColdBenchmarkFixture(b, 3000)
	b.Run("exact-rebuild", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(fixture.specs)), "roots")
		for range b.N {
			plan, err := newIncrementalPreparedPlanFromIndexes(
				fixture.groups,
				fixture.indexes,
				fixture.components,
				fixture.results.Root(),
				fixture.graph,
			)
			if err != nil {
				b.Fatal(err)
			}
			incrementalPreparedPlanBootstrapBenchmarkSink = plan
		}
	})
	b.Run("accumulated-finalize", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(fixture.specs)), "roots")
		for range b.N {
			b.StopTimer()
			builder := stageIncrementalPreparedPlanColdFixture(b, fixture, fixture.groups)
			b.StartTimer()
			benchmarkFinalizeIncrementalPreparedPlanCold(b, fixture, builder)
		}
	})
	b.Run("accumulated-total", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(fixture.specs)), "roots")
		for range b.N {
			builder := stageIncrementalPreparedPlanColdFixture(b, fixture, fixture.groups)
			benchmarkFinalizeIncrementalPreparedPlanCold(b, fixture, builder)
		}
	})
}

func benchmarkFinalizeIncrementalPreparedPlanCold(
	b *testing.B,
	fixture *incrementalPreparedPlanBootstrapFixture,
	builder *incrementalPreparedPlanColdBuilder,
) {
	b.Helper()
	plan, accumulated, err := builder.finalize(
		fixture.groups,
		fixture.indexes,
		fixture.components,
		fixture.results.Root(),
		fixture.graph,
	)
	if err != nil {
		b.Fatal(err)
	}
	if !accumulated {
		b.Fatal("prepared plan accumulator fell back")
	}
	incrementalPreparedPlanBootstrapBenchmarkSink = plan
}

func stageIncrementalPreparedPlanColdFixture(
	tb testing.TB,
	fixture *incrementalPreparedPlanBootstrapFixture,
	groups []string,
) *incrementalPreparedPlanColdBuilder {
	tb.Helper()
	builder, err := newIncrementalPreparedPlanColdBuilder(fixture.groups, fixture.components)
	require.NoError(tb, err)
	for _, group := range groups {
		batch := prepareIncrementalPreparedPlanColdFixtureGroup(tb, builder, fixture, group)
		require.NoError(tb, builder.commit(batch))
	}
	return builder
}

func prepareIncrementalPreparedPlanColdFixtureGroup(
	tb testing.TB,
	builder *incrementalPreparedPlanColdBuilder,
	fixture *incrementalPreparedPlanBootstrapFixture,
	group string,
) *incrementalPreparedPlanColdBatch {
	tb.Helper()
	additions := incrementalPreparedPlanColdFixtureGroupAdditions(fixture, group)
	batch, err := builder.prepareValidatedGroupAdditions(group, fixture.indexes[group], additions)
	require.NoError(tb, err)
	require.NotNil(tb, batch)
	return batch
}

func incrementalPreparedPlanColdFixtureGroupAdditions(
	fixture *incrementalPreparedPlanBootstrapFixture,
	group string,
) []incrementalPreparedPlanGroupAddition {
	additions := make([]incrementalPreparedPlanGroupAddition, 0)
	for specIndex := range fixture.specs {
		spec := &fixture.specs[specIndex]
		component := fixture.components[spec.component]
		if component.group != group {
			continue
		}
		componentCopy := component
		additions = append(additions, incrementalPreparedPlanGroupAddition{
			component: &componentCopy,
			id: incrementalGroupInstanceID{
				component: component.name,
				source:    spec.source,
				namespace: spec.namespace,
				name:      spec.name,
			},
			result: &spec.result,
		})
	}
	return additions
}

func newIncrementalPreparedPlanColdBenchmarkFixture(
	b *testing.B,
	declarations int,
) *incrementalPreparedPlanBootstrapFixture {
	b.Helper()
	component := incrementalComponent{name: "backends", group: "routing", backendPlan: true}
	specs := make([]incrementalPreparedPlanBootstrapSpec, declarations)
	values := make(map[incremental.QueryKey]string, declarations)
	for index := range specs {
		name := fmt.Sprintf("%08d", index)
		result := benchmarkBackendPlanResult(b, "be_"+name, "initial")
		specs[index] = incrementalPreparedPlanBootstrapSpec{
			component: component.name,
			source:    "routes",
			namespace: "default",
			name:      "item-" + name,
			result:    result,
		}
		encoded, err := json.Marshal(result)
		require.NoError(b, err)
		values[componentQueryKey(&component, specs[index].source, specs[index].namespace, specs[index].name)] =
			string(encoded)
	}
	graph, roots := testExactRoots(b, values)
	index := newIncrementalGroupIndex()
	instances := index.instances.Txn()
	resultTxn := iradix.New[incremental.ExactValueRoot]().Txn()
	for specIndex := range specs {
		spec := &specs[specIndex]
		id := incrementalGroupInstanceID{
			component: component.name,
			source:    spec.source,
			namespace: spec.namespace,
			name:      spec.name,
		}
		encoded, err := json.Marshal(spec.result)
		require.NoError(b, err)
		instances.Insert(incrementalGroupInstanceKey(id), incrementalIndexedGroupInstance{
			id: id, encodedResult: string(encoded), httpEffects: incrementalEmptyIndexedHTTPEffects,
		})
		queryKey := componentQueryKey(&component, spec.source, spec.namespace, spec.name)
		resultTxn.Insert(resultKey(&component, spec.source, spec.namespace, spec.name), roots[queryKey])
	}
	index.instances = instances.Commit()
	index.authenticate()
	return &incrementalPreparedPlanBootstrapFixture{
		groups:     []string{"routing"},
		components: map[string]incrementalComponent{component.name: component},
		specs:      specs,
		graph:      graph,
		indexes:    map[string]*incrementalGroupIndex{"routing": index},
		results:    resultTxn.Commit(),
	}
}
