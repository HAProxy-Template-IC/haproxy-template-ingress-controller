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
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type incrementalPreparedPlanBootstrapSpec struct {
	component string
	source    string
	namespace string
	name      string
	result    incrementalComponentResult
}

type incrementalPreparedPlanBootstrapFixture struct {
	groups     []string
	components map[string]incrementalComponent
	specs      []incrementalPreparedPlanBootstrapSpec
	graph      *incremental.Graph
	indexes    map[string]*incrementalGroupIndex
	results    *iradix.Tree[incremental.ExactValueRoot]
	sequential *incrementalPreparedPlan
}

type incrementalPreparedPlanBootstrapState struct {
	instances           map[string]string
	calls               map[string]string
	backendCandidates   map[string]string
	profileCandidates   map[string]string
	profileVariants     map[string]struct{}
	standaloneProfiles  map[string]struct{}
	conditions          map[string]struct{}
	requirements        map[string]string
	missingProfiles     map[string]string
	conflictingProfiles map[string]string
	outputs             map[string]string
}

var incrementalPreparedPlanBootstrapBenchmarkSink *incrementalPreparedPlan

func TestIncrementalPreparedPlanBootstrapMatchesSequentialPermutations(t *testing.T) {
	orders := [][]int{
		{0, 1, 2, 3, 4, 5, 6},
		{6, 5, 4, 3, 2, 1, 0},
		{3, 0, 6, 2, 5, 1, 4},
		{1, 4, 0, 5, 2, 6, 3},
	}
	baseline := ""
	for index, order := range orders {
		t.Run(fmt.Sprintf("order-%d", index), func(t *testing.T) {
			fixture := newIncrementalPreparedPlanBootstrapFixture(t, order)
			bulk, err := newIncrementalPreparedPlanFromIndexes(
				fixture.groups,
				fixture.indexes,
				fixture.components,
				fixture.results.Root(),
				fixture.graph,
			)
			require.NoError(t, err)
			requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, bulk, fixture)
			config := renderIncrementalPreparedPlanBootstrap(t, bulk, fixture)
			if baseline == "" {
				baseline = config
			}
			assert.Equal(t, baseline, config)
		})
	}
}

func TestIncrementalPreparedPlanBootstrapSupportsWarmTransitions(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	bulk, err := newIncrementalPreparedPlanFromIndexes(
		fixture.groups, fixture.indexes, fixture.components, fixture.results.Root(), fixture.graph,
	)
	require.NoError(t, err)

	updatedWinner := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "winner-updated",
	}, "backend be_shared\n    # winner-updated\n", nil)
	applyIncrementalPreparedPlanBootstrapTransition(t, fixture, &bulk, "a", &updatedWinner)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, bulk, fixture)
	assert.Contains(t, renderIncrementalPreparedPlanBootstrap(t, bulk, fixture), "winner-updated")

	applyIncrementalPreparedPlanBootstrapTransition(t, fixture, &bulk, "a", nil)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, bulk, fixture)
	config := renderIncrementalPreparedPlanBootstrap(t, bulk, fixture)
	assert.Contains(t, config, "# loser")
	assert.NotContains(t, config, "winner-updated")

	earlier := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "earlier",
	}, "backend be_shared\n    # earlier\n", nil)
	applyIncrementalPreparedPlanBootstrapTransition(t, fixture, &bulk, "0", &earlier)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, bulk, fixture)
	config = renderIncrementalPreparedPlanBootstrap(t, bulk, fixture)
	assert.Contains(t, config, "# earlier")
	assert.NotContains(t, config, "# loser")

	late := backendPlanResult(t, map[string]any{
		"name": "be_late", "guid": "late",
	}, "backend be_late\n", nil)
	applyIncrementalPreparedPlanBootstrapTransition(t, fixture, &bulk, "z", &late)
	requireIncrementalPreparedPlansEquivalent(t, fixture.sequential, bulk, fixture)
	assert.Contains(t, renderIncrementalPreparedPlanBootstrap(t, bulk, fixture), "backend be_late")
}

func TestIncrementalPreparedPlanBootstrapRejectsPoisonedProvenance(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})

	t.Run("wrong query with equal payload", func(t *testing.T) {
		left := fixture.specs[5]
		right := fixture.specs[6]
		leftComponent := fixture.components[left.component]
		rightComponent := fixture.components[right.component]
		leftKey := resultKey(&leftComponent, left.source, left.namespace, left.name)
		rightRoot, exists := fixture.results.Root().Get(
			resultKey(&rightComponent, right.source, right.namespace, right.name),
		)
		require.True(t, exists)
		poisoned, _, _ := fixture.results.Insert(leftKey, rightRoot)
		_, err := newIncrementalPreparedPlanFromIndexes(
			fixture.groups, fixture.indexes, fixture.components, poisoned.Root(), fixture.graph,
		)
		require.ErrorContains(t, err, "belongs to another query")
	})

	t.Run("foreign graph with equal payload", func(t *testing.T) {
		spec := fixture.specs[5]
		component := fixture.components[spec.component]
		key := resultKey(&component, spec.source, spec.namespace, spec.name)
		encoded, err := json.Marshal(spec.result)
		require.NoError(t, err)
		foreign := testExactRoot(
			t, componentQueryKey(&component, spec.source, spec.namespace, spec.name), encoded,
		)
		poisoned, _, _ := fixture.results.Insert(key, foreign)
		_, err = newIncrementalPreparedPlanFromIndexes(
			fixture.groups, fixture.indexes, fixture.components, poisoned.Root(), fixture.graph,
		)
		require.ErrorContains(t, err, "belongs to another query")
	})

	t.Run("missing result", func(t *testing.T) {
		spec := fixture.specs[0]
		component := fixture.components[spec.component]
		poisoned, _, _ := fixture.results.Delete(
			resultKey(&component, spec.source, spec.namespace, spec.name),
		)
		_, err := newIncrementalPreparedPlanFromIndexes(
			fixture.groups, fixture.indexes, fixture.components, poisoned.Root(), fixture.graph,
		)
		require.ErrorContains(t, err, "does not match its result cache")
	})

	t.Run("equivalent group root substitution", func(t *testing.T) {
		copied := *fixture.indexes["routing"]
		copied.instances = cloneOrderedTree(copied.instances)
		indexes := cloneIncrementalPreparedPlanBootstrapIndexes(fixture.indexes)
		indexes["routing"] = &copied
		_, err := newIncrementalPreparedPlanFromIndexes(
			fixture.groups, indexes, fixture.components, fixture.results.Root(), fixture.graph,
		)
		require.ErrorContains(t, err, "authentication seal")
	})

	t.Run("result root substitution after publication", func(t *testing.T) {
		equivalent := cloneIncrementalRadixTree(fixture.results)
		plan, err := newIncrementalPreparedPlanFromIndexes(
			fixture.groups, fixture.indexes, fixture.components, equivalent.Root(), fixture.graph,
		)
		require.NoError(t, err)
		require.NoError(t, plan.validateAuthentication(equivalent.Root()))
		require.ErrorContains(t, plan.validateAuthentication(fixture.results.Root()), "authentication seal")
	})

	t.Run("group pointer substitution after publication", func(t *testing.T) {
		plan, err := newIncrementalPreparedPlanFromIndexes(
			fixture.groups, fixture.indexes, fixture.components, fixture.results.Root(), fixture.graph,
		)
		require.NoError(t, err)
		copied := *fixture.indexes["routing"]
		indexes := cloneIncrementalPreparedPlanBootstrapIndexes(fixture.indexes)
		indexes["routing"] = &copied
		err = plan.prepareRegistry(
			fixture.groups, indexes, fixture.results.Root(), rendercontext.NewPlanRegistry(nil),
		)
		require.ErrorContains(t, err, "does not match its assembly index")
	})
}

func TestIncrementalPreparedPlanBootstrapRejectsValidWrongValueRoot(t *testing.T) {
	component := incrementalComponent{name: "backends", group: "routing", backendPlan: true}
	id := incrementalGroupInstanceID{
		component: component.name, source: "routes", namespace: "default", name: "app",
	}
	first := backendPlanResult(t, map[string]any{
		"name": "be_app", "guid": "first",
	}, "backend be_app\n    # first\n", nil)
	second := backendPlanResult(t, map[string]any{
		"name": "be_app", "guid": "second",
	}, "backend be_app\n    # second\n", nil)
	firstEncoded, err := json.Marshal(first)
	require.NoError(t, err)
	secondEncoded, err := json.Marshal(second)
	require.NoError(t, err)
	queryKey := componentQueryKey(&component, id.source, id.namespace, id.name)
	graph, roots := testExactRootVariants(t, queryKey, string(firstEncoded), string(secondEncoded))
	index, err := newIncrementalGroupIndex().replace(&incrementalInstanceResult{
		component: id.component, source: id.source, namespace: id.namespace, name: id.name, result: first,
	}, nil)
	require.NoError(t, err)
	results, _, _ := iradix.New[incremental.ExactValueRoot]().Insert(
		resultKey(&component, id.source, id.namespace, id.name), roots[1],
	)
	_, err = newIncrementalPreparedPlanFromIndexes(
		[]string{"routing"},
		map[string]*incrementalGroupIndex{"routing": index},
		map[string]incrementalComponent{component.name: component},
		results.Root(),
		graph,
	)
	require.ErrorContains(t, err, "does not match its result cache")
}

func TestIncrementalPreparedPlanBootstrapLateFailureIsAtomic(t *testing.T) {
	component := incrementalComponent{name: "backends", group: "routing", backendPlan: true}
	valid := backendPlanResult(t, map[string]any{"name": "be_valid"}, "backend be_valid\n", nil)
	invalid := backendPlanResult(t, map[string]any{"name": "be_invalid"}, "backend be_invalid\n", nil)
	invalid.BackendPlanDigest = "poisoned"
	specs := []incrementalPreparedPlanBootstrapSpec{
		{component: component.name, source: "routes", namespace: "default", name: "a", result: valid},
		{component: component.name, source: "routes", namespace: "default", name: "z", result: invalid},
	}
	values := make(map[incremental.QueryKey]string, len(specs))
	for index := range specs {
		spec := &specs[index]
		encoded, err := json.Marshal(spec.result)
		require.NoError(t, err)
		values[componentQueryKey(&component, spec.source, spec.namespace, spec.name)] = string(encoded)
	}
	graph, roots := testExactRoots(t, values)
	index, err := newIncrementalGroupIndex().replace(&incrementalInstanceResult{
		component: component.name,
		source:    specs[0].source,
		namespace: specs[0].namespace,
		name:      specs[0].name,
		result:    specs[0].result,
	}, nil)
	require.NoError(t, err)
	poisonedIndex := *index
	invalidID := incrementalGroupInstanceID{
		component: component.name, source: specs[1].source, namespace: specs[1].namespace, name: specs[1].name,
	}
	invalidEncoded, err := json.Marshal(specs[1].result)
	require.NoError(t, err)
	poisonedIndex.instances, _, _ = poisonedIndex.instances.Insert(
		incrementalGroupInstanceKey(invalidID),
		incrementalIndexedGroupInstance{
			id: invalidID, encodedResult: string(invalidEncoded), httpEffects: incrementalEmptyIndexedHTTPEffects,
		},
	)
	poisonedIndex.authenticate()
	results := iradix.New[incremental.ExactValueRoot]().Txn()
	for specIndex := range specs {
		spec := &specs[specIndex]
		queryKey := componentQueryKey(&component, spec.source, spec.namespace, spec.name)
		results.Insert(resultKey(&component, spec.source, spec.namespace, spec.name), roots[queryKey])
	}
	instancesRoot := poisonedIndex.instances
	resultTree := results.Commit()
	resultRoot := resultTree.Root()
	plan, err := newIncrementalPreparedPlanFromIndexes(
		[]string{"routing"},
		map[string]*incrementalGroupIndex{"routing": &poisonedIndex},
		map[string]incrementalComponent{component.name: component},
		resultTree.Root(),
		graph,
	)
	require.Nil(t, plan)
	require.ErrorContains(t, err, "invalid digest")
	assert.Same(t, instancesRoot, poisonedIndex.instances)
	assert.Same(t, resultRoot, resultTree.Root())
}

func TestIncrementalPreparedPlanBootstrapKeepsMissingProfileFailure(t *testing.T) {
	component := incrementalComponent{name: "backends", group: "routing", backendPlan: true}
	result := backendPlanResult(t, map[string]any{
		"name": "be_app", "profile": "undeclared-profile",
	}, "backend be_app\n", nil)
	fixture := newIncrementalPreparedPlanBootstrapFixtureForSpecs(
		t,
		[]string{"routing"},
		map[string]incrementalComponent{component.name: component},
		[]incrementalPreparedPlanBootstrapSpec{{
			component: component.name, source: "routes", namespace: "default", name: "app", result: result,
		}},
		[]int{0},
	)
	bulk, err := newIncrementalPreparedPlanFromIndexes(
		fixture.groups, fixture.indexes, fixture.components, fixture.results.Root(), fixture.graph,
	)
	require.NoError(t, err)
	require.NoError(t, bulk.validateAuthentication(fixture.results.Root()))
	err = bulk.prepareRegistry(
		fixture.groups, fixture.indexes, fixture.results.Root(), rendercontext.NewPlanRegistry(nil),
	)
	require.ErrorContains(t, err, `winning backend references undeclared profile "undeclared-profile"`)
}

func TestIncrementalPreparedPlanBootstrapRejectsIncompleteAuthority(t *testing.T) {
	fixture := newIncrementalPreparedPlanBootstrapFixture(t, []int{0, 1, 2, 3, 4, 5, 6})
	tests := map[string]func() ([]string, map[string]*incrementalGroupIndex, map[string]incrementalComponent, *iradix.Node[incremental.ExactValueRoot], incrementalPreparedPlanExactRootValidator){
		"duplicate group": func() ([]string, map[string]*incrementalGroupIndex, map[string]incrementalComponent, *iradix.Node[incremental.ExactValueRoot], incrementalPreparedPlanExactRootValidator) {
			return []string{"routing", "routing"}, fixture.indexes, fixture.components, fixture.results.Root(), fixture.graph
		},
		"missing group index": func() ([]string, map[string]*incrementalGroupIndex, map[string]incrementalComponent, *iradix.Node[incremental.ExactValueRoot], incrementalPreparedPlanExactRootValidator) {
			indexes := cloneIncrementalPreparedPlanBootstrapIndexes(fixture.indexes)
			delete(indexes, "routing")
			return fixture.groups, indexes, fixture.components, fixture.results.Root(), fixture.graph
		},
		"untracked backend group": func() ([]string, map[string]*incrementalGroupIndex, map[string]incrementalComponent, *iradix.Node[incremental.ExactValueRoot], incrementalPreparedPlanExactRootValidator) {
			return []string{"routing"}, fixture.indexes, fixture.components, fixture.results.Root(), fixture.graph
		},
		"nil result root": func() ([]string, map[string]*incrementalGroupIndex, map[string]incrementalComponent, *iradix.Node[incremental.ExactValueRoot], incrementalPreparedPlanExactRootValidator) {
			return fixture.groups, fixture.indexes, fixture.components, nil, fixture.graph
		},
		"nil result authority": func() ([]string, map[string]*incrementalGroupIndex, map[string]incrementalComponent, *iradix.Node[incremental.ExactValueRoot], incrementalPreparedPlanExactRootValidator) {
			return fixture.groups, fixture.indexes, fixture.components, fixture.results.Root(), nil
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			groups, indexes, components, results, validator := prepare()
			plan, err := newIncrementalPreparedPlanFromIndexes(
				groups, indexes, components, results, validator,
			)
			require.Nil(t, plan)
			require.Error(t, err)
		})
	}
}

func BenchmarkIncrementalPreparedPlanBootstrap3000(b *testing.B) {
	const declarations = 3000
	component := incrementalComponent{name: "backends", group: "routing", backendPlan: true}
	results := make([]incrementalComponentResult, declarations)
	ids := make([]incrementalGroupInstanceID, declarations)
	encoded := make([]string, declarations)
	values := make(map[incremental.QueryKey]string, declarations)
	for index := range declarations {
		name := fmt.Sprintf("%08d", index)
		results[index] = benchmarkBackendPlanResult(b, "be_"+name, "initial")
		ids[index] = incrementalGroupInstanceID{
			component: component.name, source: "routes", namespace: "default", name: "item-" + name,
		}
		value, err := json.Marshal(results[index])
		require.NoError(b, err)
		encoded[index] = string(value)
		values[componentQueryKey(&component, ids[index].source, ids[index].namespace, ids[index].name)] =
			encoded[index]
	}
	graph, roots := testExactRoots(b, values)
	index := newIncrementalGroupIndex()
	instances := index.instances.Txn()
	resultTxn := iradix.New[incremental.ExactValueRoot]().Txn()
	additions := make([]incrementalPreparedPlanGroupAddition, declarations)
	for resultIndex := range declarations {
		id := ids[resultIndex]
		instances.Insert(incrementalGroupInstanceKey(id), incrementalIndexedGroupInstance{
			id: id, encodedResult: encoded[resultIndex], httpEffects: incrementalEmptyIndexedHTTPEffects,
		})
		queryKey := componentQueryKey(&component, id.source, id.namespace, id.name)
		resultTxn.Insert(resultKey(&component, id.source, id.namespace, id.name), roots[queryKey])
		additions[resultIndex] = incrementalPreparedPlanGroupAddition{
			component: &component, id: id, result: &results[resultIndex],
		}
	}
	index.instances = instances.Commit()
	index.authenticate()
	resultTree := resultTxn.Commit()
	groups := []string{"routing"}
	indexes := map[string]*incrementalGroupIndex{"routing": index}
	components := map[string]incrementalComponent{component.name: component}
	emptyIndex := newIncrementalGroupIndex()
	emptyResultTree := iradix.New[incremental.ExactValueRoot]()
	base, err := newIncrementalPreparedPlan(
		groups, map[string]*incrementalGroupIndex{"routing": emptyIndex}, emptyResultTree.Root(),
	)
	require.NoError(b, err)

	b.Run("legacy-persistent", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(declarations, "roots")
		for range b.N {
			plan, applyErr := base.applyGroupAdditions(
				"routing", emptyIndex, index, additions, emptyResultTree.Root(), resultTree.Root(),
			)
			if applyErr != nil {
				b.Fatal(applyErr)
			}
			incrementalPreparedPlanBootstrapBenchmarkSink = plan
		}
	})

	b.Run("bulk-from-final-indexes", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(declarations, "roots")
		for range b.N {
			plan, buildErr := newIncrementalPreparedPlanFromIndexes(
				groups, indexes, components, resultTree.Root(), graph,
			)
			if buildErr != nil {
				b.Fatal(buildErr)
			}
			incrementalPreparedPlanBootstrapBenchmarkSink = plan
		}
	})
}

func newIncrementalPreparedPlanBootstrapFixture(
	t *testing.T,
	order []int,
) *incrementalPreparedPlanBootstrapFixture {
	t.Helper()
	components := map[string]incrementalComponent{
		"100-backends": {
			name: "100-backends", group: "routing", backendPlan: true,
		},
		"200-profiled": {
			name: "200-profiled", group: "routing", backendPlan: true,
		},
		"250-conditional": {
			name: "250-conditional", group: "routing", backendPlan: true, publishValue: true,
		},
		"300-observer": {
			name: "300-observer", group: "routing",
		},
		"400-second": {
			name: "400-second", group: "second", backendPlan: true,
		},
	}
	winner := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "winner",
	}, "backend be_shared\n    # winner\n", nil)
	loser := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "loser",
	}, "backend be_shared\n    # loser\n", func(token string) string {
		return "# losing literal\n" + token
	})
	profiled, _ := profiledBackendPlanResult(t, "http", "be_profiled", "backend be_profiled\n")
	conditionalWinner := incrementalPreparedPlanBootstrapConditionalResult(t, "be_cond_a", "a")
	conditionalLoser := incrementalPreparedPlanBootstrapConditionalResult(t, "be_cond_b", "b")
	second := backendPlanResult(t, map[string]any{"name": "be_second"}, "backend be_second\n", nil)
	specs := []incrementalPreparedPlanBootstrapSpec{
		{component: "100-backends", source: "routes", namespace: "default", name: "a", result: winner},
		{component: "100-backends", source: "routes", namespace: "default", name: "b", result: loser},
		{component: "200-profiled", source: "routes", namespace: "default", name: "profiled", result: profiled},
		{component: "250-conditional", source: "routes", namespace: "default", name: "a", result: conditionalWinner},
		{component: "250-conditional", source: "routes", namespace: "default", name: "b", result: conditionalLoser},
		{component: "300-observer", source: "routes", namespace: "default", name: "side-a"},
		{component: "300-observer", source: "routes", namespace: "default", name: "side-b"},
		{component: "400-second", source: "routes", namespace: "default", name: "second", result: second},
	}
	return newIncrementalPreparedPlanBootstrapFixtureForSpecs(
		t, []string{"routing", "second"}, components, specs, append(order, 7),
	)
}

func newIncrementalPreparedPlanBootstrapFixtureForSpecs(
	t *testing.T,
	groups []string,
	components map[string]incrementalComponent,
	specs []incrementalPreparedPlanBootstrapSpec,
	order []int,
) *incrementalPreparedPlanBootstrapFixture {
	t.Helper()
	require.Len(t, order, len(specs))
	values := make(map[incremental.QueryKey]string, len(specs))
	for index := range specs {
		spec := &specs[index]
		component := components[spec.component]
		encoded, err := json.Marshal(spec.result)
		require.NoError(t, err)
		values[componentQueryKey(&component, spec.source, spec.namespace, spec.name)] = string(encoded)
	}
	graph, roots := testExactRoots(t, values)
	indexes := make(map[string]*incrementalGroupIndex, len(groups))
	for _, group := range groups {
		indexes[group] = newIncrementalGroupIndex()
	}
	results := iradix.New[incremental.ExactValueRoot]()
	plan, err := newIncrementalPreparedPlan(groups, indexes, results.Root())
	require.NoError(t, err)
	for _, specIndex := range order {
		require.Less(t, specIndex, len(specs))
		spec := &specs[specIndex]
		component := components[spec.component]
		id := incrementalGroupInstanceID{
			component: component.name, source: spec.source, namespace: spec.namespace, name: spec.name,
		}
		oldIndex := indexes[component.group]
		updated, replaceErr := oldIndex.replace(&incrementalInstanceResult{
			component: component.name,
			source:    spec.source,
			namespace: spec.namespace,
			name:      spec.name,
			result:    spec.result,
		}, nil)
		require.NoError(t, replaceErr)
		oldRoot := results.Root()
		queryKey := componentQueryKey(&component, spec.source, spec.namespace, spec.name)
		results, _, _ = results.Insert(
			resultKey(&component, spec.source, spec.namespace, spec.name), roots[queryKey],
		)
		plan, err = plan.applyGroupReplacement(
			&component, component.group, oldIndex, updated, id, oldRoot, results.Root(),
		)
		require.NoError(t, err)
		indexes[component.group] = updated
	}
	return &incrementalPreparedPlanBootstrapFixture{
		groups:     slices.Clone(groups),
		components: components,
		specs:      specs,
		graph:      graph,
		indexes:    indexes,
		results:    results,
		sequential: plan,
	}
}

func incrementalPreparedPlanBootstrapConditionalResult(
	t *testing.T,
	backend, rank string,
) incrementalComponentResult {
	t.Helper()
	plan := newIncrementalBackendPlanRecorder()
	token, err := plan.BackendWhenAny(
		map[string]any{"name": backend}, "backend "+backend+"\n", "gate", []string{"enabled"},
	)
	require.NoError(t, err)
	recorder := &incrementalRecorder{plan: plan}
	recorder.publishAfterPreflight(
		"gate", "enabled", rank, map[string]any{"rank": rank}, "shared.PublishRanked",
	)
	result, err := recorder.result(token)
	require.NoError(t, err)
	require.NoError(t, validateIncrementalInstanceResult(&result))
	return result
}

func applyIncrementalPreparedPlanBootstrapTransition(
	t *testing.T,
	fixture *incrementalPreparedPlanBootstrapFixture,
	bulk **incrementalPreparedPlan,
	name string,
	result *incrementalComponentResult,
) {
	t.Helper()
	const componentName = "100-backends"
	component := fixture.components[componentName]
	id := incrementalGroupInstanceID{
		component: component.name, source: "routes", namespace: "default", name: name,
	}
	oldIndex := fixture.indexes[component.group]
	oldRoot := fixture.results.Root()
	var updated *incrementalGroupIndex
	var err error
	if result == nil {
		updated, err = oldIndex.remove(id.component, id.source, id.namespace, id.name)
		fixture.results, _, _ = fixture.results.Delete(
			resultKey(&component, id.source, id.namespace, id.name),
		)
	} else {
		updated, err = oldIndex.replace(&incrementalInstanceResult{
			component: id.component,
			source:    id.source,
			namespace: id.namespace,
			name:      id.name,
			result:    *result,
		}, nil)
		encoded, encodeErr := json.Marshal(result)
		require.NoError(t, encodeErr)
		root := testExactRoot(t, componentQueryKey(&component, id.source, id.namespace, id.name), encoded)
		fixture.results, _, _ = fixture.results.Insert(
			resultKey(&component, id.source, id.namespace, id.name), root,
		)
	}
	require.NoError(t, err)
	fixture.sequential, err = fixture.sequential.applyGroupReplacement(
		&component, component.group, oldIndex, updated, id, oldRoot, fixture.results.Root(),
	)
	require.NoError(t, err)
	*bulk, err = (*bulk).applyGroupReplacement(
		&component, component.group, oldIndex, updated, id, oldRoot, fixture.results.Root(),
	)
	require.NoError(t, err)
	fixture.indexes[component.group] = updated
}

func requireIncrementalPreparedPlansEquivalent(
	t *testing.T,
	left, right *incrementalPreparedPlan,
	fixture *incrementalPreparedPlanBootstrapFixture,
) {
	t.Helper()
	require.NoError(t, left.validateAuthentication(fixture.results.Root()))
	require.NoError(t, right.validateAuthentication(fixture.results.Root()))
	assert.Equal(t, incrementalPreparedPlanBootstrapSnapshot(left), incrementalPreparedPlanBootstrapSnapshot(right))
	for _, group := range fixture.groups {
		leftIndex, leftExists := left.groups.Root().Get([]byte(group))
		rightIndex, rightExists := right.groups.Root().Get([]byte(group))
		require.Equal(t, leftExists, rightExists)
		if leftExists {
			assert.Same(t, fixture.indexes[group], leftIndex)
			assert.Same(t, leftIndex, rightIndex)
		}
	}
	assert.Equal(
		t,
		renderIncrementalPreparedPlanBootstrap(t, left, fixture),
		renderIncrementalPreparedPlanBootstrap(t, right, fixture),
	)
}

func incrementalPreparedPlanBootstrapSnapshot(
	plan *incrementalPreparedPlan,
) incrementalPreparedPlanBootstrapState {
	return incrementalPreparedPlanBootstrapState{
		instances:           incrementalPreparedPlanBootstrapFlatSnapshot(plan.instances),
		calls:               incrementalPreparedPlanBootstrapFlatSnapshot(plan.calls),
		backendCandidates:   incrementalPreparedPlanBootstrapNestedValueSnapshot(plan.backendCandidates),
		profileCandidates:   incrementalPreparedPlanBootstrapNestedValueSnapshot(plan.profileCandidates),
		profileVariants:     incrementalPreparedPlanBootstrapVariantSnapshot(plan.profileVariants),
		standaloneProfiles:  incrementalPreparedPlanBootstrapNestedSetSnapshot(plan.standaloneProfiles),
		conditions:          incrementalPreparedPlanBootstrapNestedSetSnapshot(plan.conditions),
		requirements:        incrementalPreparedPlanBootstrapNestedValueSnapshot(plan.requirements),
		missingProfiles:     incrementalPreparedPlanBootstrapFlatSnapshot(plan.missingProfiles),
		conflictingProfiles: incrementalPreparedPlanBootstrapFlatSnapshot(plan.conflictingProfiles),
		outputs:             incrementalPreparedPlanBootstrapFlatSnapshot(plan.outputs),
	}
}

func incrementalPreparedPlanBootstrapFlatSnapshot(
	tree *iradix.Tree[string],
) map[string]string {
	result := make(map[string]string, tree.Len())
	tree.Root().Walk(func(key []byte, value string) bool {
		result[string(key)] = value
		return false
	})
	return result
}

func incrementalPreparedPlanBootstrapNestedValueSnapshot(
	tree *iradix.Tree[*iradix.Tree[string]],
) map[string]string {
	result := make(map[string]string)
	tree.Root().Walk(func(outer []byte, inner *iradix.Tree[string]) bool {
		inner.Root().Walk(func(key []byte, value string) bool {
			result[string(incrementalOrderedTuple(string(outer), string(key)))] = value
			return false
		})
		return false
	})
	return result
}

func incrementalPreparedPlanBootstrapNestedSetSnapshot(
	tree *iradix.Tree[*iradix.Tree[struct{}]],
) map[string]struct{} {
	result := make(map[string]struct{})
	tree.Root().Walk(func(outer []byte, inner *iradix.Tree[struct{}]) bool {
		inner.Root().Walk(func(key []byte, _ struct{}) bool {
			result[string(incrementalOrderedTuple(string(outer), string(key)))] = struct{}{}
			return false
		})
		return false
	})
	return result
}

func incrementalPreparedPlanBootstrapVariantSnapshot(
	tree *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]],
) map[string]struct{} {
	result := make(map[string]struct{})
	tree.Root().Walk(func(profile []byte, variants *iradix.Tree[*iradix.Tree[struct{}]]) bool {
		variants.Root().Walk(func(text []byte, locations *iradix.Tree[struct{}]) bool {
			locations.Root().Walk(func(location []byte, _ struct{}) bool {
				result[string(incrementalOrderedTuple(
					string(profile), string(text), string(location),
				))] = struct{}{}
				return false
			})
			return false
		})
		return false
	})
	return result
}

func renderIncrementalPreparedPlanBootstrap(
	t *testing.T,
	plan *incrementalPreparedPlan,
	fixture *incrementalPreparedPlanBootstrapFixture,
) string {
	t.Helper()
	registry := rendercontext.NewPlanRegistry(nil)
	require.NoError(t, plan.prepareRegistry(
		fixture.groups, fixture.indexes, fixture.results.Root(), registry,
	))
	componentNames := make([]string, 0)
	for name := range fixture.components {
		if fixture.components[name].backendPlan {
			componentNames = append(componentNames, name)
		}
	}
	slices.Sort(componentNames)
	output := ""
	for _, name := range componentNames {
		component := fixture.components[name]
		text, err := plan.output(component.group, name, fixture.results.Root(), registry)
		require.NoError(t, err)
		output += text
	}
	config, _, err := registry.Assemble(context.Background(), registry.ProfileGroup()+output, nil)
	require.NoError(t, err)
	return config
}

func cloneIncrementalPreparedPlanBootstrapIndexes(
	source map[string]*incrementalGroupIndex,
) map[string]*incrementalGroupIndex {
	result := make(map[string]*incrementalGroupIndex, len(source))
	for group, index := range source {
		result[group] = index
	}
	return result
}
