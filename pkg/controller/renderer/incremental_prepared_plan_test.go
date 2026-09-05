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
	"sync"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type incrementalPreparedPlanFixture struct {
	component incrementalComponent
	index     *incrementalGroupIndex
	results   *iradix.Tree[incremental.ExactValueRoot]
	plan      *incrementalPreparedPlan
}

type countingIncrementalPreparedPlanRegistry struct {
	*rendercontext.PlanRegistry
	preparedBackendTokens int
}

func (r *countingIncrementalPreparedPlanRegistry) PreparedBackendToken(name string) (string, error) {
	r.preparedBackendTokens++
	return r.PlanRegistry.PreparedBackendToken(name)
}

func newIncrementalPreparedPlanFixture(tb testing.TB) *incrementalPreparedPlanFixture {
	tb.Helper()
	index := newIncrementalGroupIndex()
	results := iradix.New[incremental.ExactValueRoot]()
	plan, err := newIncrementalPreparedPlan(
		[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": index}, results.Root(),
	)
	require.NoError(tb, err)
	return &incrementalPreparedPlanFixture{
		component: incrementalComponent{name: "backends", group: "backends", backendPlan: true},
		index:     index,
		results:   results,
		plan:      plan,
	}
}

func (f *incrementalPreparedPlanFixture) replace(
	tb testing.TB,
	name string,
	result *incrementalComponentResult,
) {
	tb.Helper()
	id := incrementalGroupInstanceID{
		component: f.component.name, source: "routes", namespace: "default", name: name,
	}
	oldIndex := f.index
	oldRoot := f.results.Root()
	var err error
	if result == nil {
		f.index, err = f.index.remove(id.component, id.source, id.namespace, id.name)
		f.results, _, _ = f.results.Delete(incrementalGroupInstanceKey(id))
	} else {
		instance := incrementalInstanceResult{
			component: id.component, source: id.source, namespace: id.namespace, name: id.name, result: *result,
		}
		f.index, err = f.index.replace(&instance, nil)
		require.NoError(tb, err)
		encoded, encodeErr := json.Marshal(result)
		require.NoError(tb, encodeErr)
		queryKey := componentQueryKey(&f.component, id.source, id.namespace, id.name)
		f.results, _, _ = f.results.Insert(incrementalGroupInstanceKey(id), testExactRoot(tb, queryKey, encoded))
	}
	require.NoError(tb, err)
	f.plan, err = f.plan.applyGroupReplacement(
		&f.component, "backends", oldIndex, f.index, id, oldRoot, f.results.Root(),
	)
	require.NoError(tb, err)
}

func (f *incrementalPreparedPlanFixture) render(tb testing.TB) string {
	tb.Helper()
	registry := rendercontext.NewPlanRegistry(nil)
	err := f.plan.prepareRegistry(
		[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": f.index}, f.results.Root(), registry,
	)
	require.NoError(tb, err)
	output, err := f.plan.output("backends", "backends", f.results.Root(), registry)
	require.NoError(tb, err)
	config, _, err := registry.Assemble(context.Background(), output, nil)
	require.NoError(tb, err)
	return config
}

func (f *incrementalPreparedPlanFixture) prepareRegistry(
	tb testing.TB,
	authority *rendercontext.PlanTokenAuthority,
) *countingIncrementalPreparedPlanRegistry {
	tb.Helper()
	registry, err := rendercontext.NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(tb, err)
	counting := &countingIncrementalPreparedPlanRegistry{PlanRegistry: registry}
	err = f.plan.prepareRegistry(
		[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": f.index}, f.results.Root(), counting,
	)
	require.NoError(tb, err)
	return counting
}

func (f *incrementalPreparedPlanFixture) outputFragment(
	tb testing.TB,
	registry incrementalPreparedPlanRegistry,
) rendercontent.Output {
	tb.Helper()
	output, err := f.plan.outputFragment("backends", "backends", f.results.Root(), registry)
	require.NoError(tb, err)
	return output
}

func TestIncrementalPreparedPlanPromotesAndReplacesOneCandidate(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	loser := backendPlanResult(t, map[string]any{
		"name": "be_shared", "mode": "tcp", "guid": "b",
	}, "backend be_shared\n    # b\n", nil)
	winner := backendPlanResult(t, map[string]any{
		"name": "be_shared", "mode": "http", "guid": "a",
	}, "backend be_shared\n    # a\n", nil)
	updated := backendPlanResult(t, map[string]any{
		"name": "be_shared", "mode": "http", "guid": "a2",
	}, "backend be_shared\n    # a2\n", nil)

	fixture.replace(t, "b", &loser)
	fixture.replace(t, "a", &winner)
	assert.Equal(t, "backend be_shared\n    # a\n", fixture.render(t))

	fixture.replace(t, "a", &updated)
	assert.Equal(t, "backend be_shared\n    # a2\n", fixture.render(t))

	fixture.replace(t, "a", nil)
	assert.Equal(t, "backend be_shared\n    # b\n", fixture.render(t))
}

func TestIncrementalPreparedPlanOutputMemoReusesExactOutputAcrossRegistries(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)
	authority := rendercontext.NewPlanTokenAuthority()

	firstRegistry := fixture.prepareRegistry(t, authority)
	first := fixture.outputFragment(t, firstRegistry)
	secondRegistry := fixture.prepareRegistry(t, authority)
	second := fixture.outputFragment(t, secondRegistry)

	assertSameOutputRoot(t, first, second)
	assert.Equal(t, 1, firstRegistry.preparedBackendTokens)
	assert.Zero(t, secondRegistry.preparedBackendTokens)
}

func TestIncrementalPreparedPlanOutputMemoSeparatesForeignAuthorities(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)

	firstRegistry := fixture.prepareRegistry(t, rendercontext.NewPlanTokenAuthority())
	first := fixture.outputFragment(t, firstRegistry)
	secondRegistry := fixture.prepareRegistry(t, rendercontext.NewPlanTokenAuthority())
	second := fixture.outputFragment(t, secondRegistry)

	assertDifferentOutputRoot(t, first, second)
	assertSameOutputRoot(t, first, fixture.outputFragment(t, firstRegistry))
	assertSameOutputRoot(t, second, fixture.outputFragment(t, secondRegistry))
	assert.Equal(t, 1, firstRegistry.preparedBackendTokens)
	assert.Equal(t, 1, secondRegistry.preparedBackendTokens)

	firstText, err := first.String()
	require.NoError(t, err)
	firstConfig, _, err := firstRegistry.Assemble(context.Background(), firstText, nil)
	require.NoError(t, err)
	secondText, err := second.String()
	require.NoError(t, err)
	secondConfig, _, err := secondRegistry.Assemble(context.Background(), secondText, nil)
	require.NoError(t, err)
	assert.Equal(t, "backend be_app\n", firstConfig)
	assert.Equal(t, firstConfig, secondConfig)
}

func TestIncrementalPreparedPlanOutputMemoAppliesLosingInstanceDelta(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	winner := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "winner",
	}, "backend be_shared\n    # winner\n", nil)
	loser := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "loser",
	}, "backend be_shared\n    # loser\n", func(token string) string {
		return "# losing literal one\n" + token
	})
	fixture.replace(t, "a", &winner)
	fixture.replace(t, "b", &loser)
	authority := rendercontext.NewPlanTokenAuthority()

	firstSelected := fixture.plan.selected
	firstMemo := fixture.plan.outputMemo
	firstRegistry := fixture.prepareRegistry(t, authority)
	first := fixture.outputFragment(t, firstRegistry)
	assert.Equal(t, 1, firstRegistry.preparedBackendTokens)

	updatedLoser := backendPlanResult(t, map[string]any{
		"name": "be_shared", "guid": "loser",
	}, "backend be_shared\n    # loser\n", func(token string) string {
		return "# losing literal two\n" + token
	})
	fixture.replace(t, "b", &updatedLoser)

	assert.Same(t, firstSelected, fixture.plan.selected)
	assert.Same(t, firstMemo, fixture.plan.outputMemo.parent)
	assert.Equal(t, 1, fixture.plan.outputMemo.changes.Len())
	changedKey := incrementalPreparedPlanOutputKey("backends", incrementalGroupInstanceID{
		component: "backends", source: "routes", namespace: "default", name: "b",
	})
	_, changed := fixture.plan.outputMemo.changes.Root().Get(changedKey)
	assert.True(t, changed)

	secondRegistry := fixture.prepareRegistry(t, authority)
	second := fixture.outputFragment(t, secondRegistry)
	assertDifferentOutputRoot(t, first, second)
	assert.Zero(t, secondRegistry.preparedBackendTokens)

	firstText, err := first.String()
	require.NoError(t, err)
	firstConfig, _, err := firstRegistry.Assemble(context.Background(), firstText, nil)
	require.NoError(t, err)
	secondText, err := second.String()
	require.NoError(t, err)
	secondConfig, _, err := secondRegistry.Assemble(context.Background(), secondText, nil)
	require.NoError(t, err)
	assert.Equal(t, "backend be_shared\n    # winner\n# losing literal one\n", firstConfig)
	assert.Equal(t, "backend be_shared\n    # winner\n# losing literal two\n", secondConfig)
}

func TestIncrementalPreparedPlanOutputMemoRejectsShallowCopy(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)
	poisoned := *fixture.plan
	copiedMemo := *fixture.plan.outputMemo
	poisoned.outputMemo = &copiedMemo
	poisoned.authenticate()

	err := poisoned.prepareRegistry(
		[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": fixture.index},
		fixture.results.Root(), rendercontext.NewPlanRegistry(nil),
	)
	require.ErrorContains(t, err, "authentication seal")
}

func TestIncrementalPreparedPlanOutputMemoRejectsFieldSubstitution(t *testing.T) {
	tests := map[string]func(*incrementalPreparedPlanOutputMemo){
		"root": func(memo *incrementalPreparedPlanOutputMemo) {
			memo.root = iradix.New[string]().Root()
		},
		"selected": func(memo *incrementalPreparedPlanOutputMemo) {
			memo.selected = rendercontext.NewPreparedPlanSnapshot()
		},
		"parent": func(memo *incrementalPreparedPlanOutputMemo) {
			memo.parent = newIncrementalPreparedPlanOutputMemo(memo.root, memo.selected, nil, nil)
		},
		"changes": func(memo *incrementalPreparedPlanOutputMemo) {
			memo.changes = iradix.New[struct{}]()
		},
		"depth": func(memo *incrementalPreparedPlanOutputMemo) {
			memo.depth++
		},
		"entries": func(memo *incrementalPreparedPlanOutputMemo) {
			memo.entries = &sync.Map{}
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newIncrementalPreparedPlanFixture(t)
			result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
			fixture.replace(t, "app", &result)
			poison(fixture.plan.outputMemo)

			err := fixture.plan.prepareRegistry(
				[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": fixture.index},
				fixture.results.Root(), rendercontext.NewPlanRegistry(nil),
			)
			require.ErrorContains(t, err, "authentication seal")
		})
	}
}

func TestIncrementalPreparedPlanOutputMemoAcceptsCopiedCachedOutputHandle(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)
	authority := rendercontext.NewPlanTokenAuthority()
	registry := fixture.prepareRegistry(t, authority)
	output := fixture.outputFragment(t, registry)
	key := incrementalPreparedPlanOutputMemoKey{
		authority: authority,
		group:     "backends",
		component: "backends",
	}
	cached, exists := fixture.plan.outputMemo.entries.Load(key)
	require.True(t, exists)
	entry, ok := cached.(*incrementalPreparedPlanOutputMemoEntry)
	require.True(t, ok)
	require.NotNil(t, entry)
	copied := output
	entry.output = copied

	got, err := fixture.plan.outputFragment("backends", "backends", fixture.results.Root(), registry)
	require.NoError(t, err)
	assertSameOutputRoot(t, output, got)
}

func TestIncrementalPreparedPlanOutputMemoRejectsAuthenticatedCachedOutputSubstitution(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)
	authority := rendercontext.NewPlanTokenAuthority()
	registry := fixture.prepareRegistry(t, authority)
	fixture.outputFragment(t, registry)
	key := incrementalPreparedPlanOutputMemoKey{
		authority: authority,
		group:     "backends",
		component: "backends",
	}
	cached, exists := fixture.plan.outputMemo.entries.Load(key)
	require.True(t, exists)
	entry, ok := cached.(*incrementalPreparedPlanOutputMemoEntry)
	require.True(t, ok)
	require.NotNil(t, entry)
	poisoned, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "poisoned", Text: "wrong output\n"}})
	require.NoError(t, err)
	entry.output = poisoned

	_, err = fixture.plan.outputFragment("backends", "backends", fixture.results.Root(), registry)
	require.ErrorContains(t, err, "entry has invalid provenance")
}

func TestIncrementalPreparedPlanRejectsEquivalentRootSubstitution(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)

	tests := map[string]func(*incrementalPreparedPlan){
		"instances": func(poisoned *incrementalPreparedPlan) {
			poisoned.instances = cloneIncrementalRadixTree(fixture.plan.instances)
		},
		"calls": func(poisoned *incrementalPreparedPlan) {
			poisoned.calls = cloneIncrementalRadixTree(fixture.plan.calls)
		},
		"backend candidates": func(poisoned *incrementalPreparedPlan) {
			poisoned.backendCandidates = cloneIncrementalRadixTree(fixture.plan.backendCandidates)
		},
		"profile candidates": func(poisoned *incrementalPreparedPlan) {
			poisoned.profileCandidates = cloneIncrementalRadixTree(fixture.plan.profileCandidates)
		},
		"profile variants": func(poisoned *incrementalPreparedPlan) {
			poisoned.profileVariants = cloneIncrementalRadixTree(fixture.plan.profileVariants)
		},
		"standalone profiles": func(poisoned *incrementalPreparedPlan) {
			poisoned.standaloneProfiles = cloneIncrementalRadixTree(fixture.plan.standaloneProfiles)
		},
		"conditions": func(poisoned *incrementalPreparedPlan) {
			poisoned.conditions = cloneIncrementalRadixTree(fixture.plan.conditions)
		},
		"requirements": func(poisoned *incrementalPreparedPlan) {
			poisoned.requirements = cloneIncrementalRadixTree(fixture.plan.requirements)
		},
		"missing profiles": func(poisoned *incrementalPreparedPlan) {
			poisoned.missingProfiles = cloneIncrementalRadixTree(fixture.plan.missingProfiles)
		},
		"conflicting profiles": func(poisoned *incrementalPreparedPlan) {
			poisoned.conflictingProfiles = cloneIncrementalRadixTree(fixture.plan.conflictingProfiles)
		},
		"outputs": func(poisoned *incrementalPreparedPlan) {
			poisoned.outputs = cloneIncrementalRadixTree(fixture.plan.outputs)
		},
		"groups": func(poisoned *incrementalPreparedPlan) {
			poisoned.groups = cloneIncrementalRadixTree(fixture.plan.groups)
		},
		"selected": func(poisoned *incrementalPreparedPlan) {
			poisoned.selected = rendercontext.NewPreparedPlanSnapshot()
		},
		"result root": func(poisoned *incrementalPreparedPlan) {
			poisoned.resultRoot = iradix.New[incremental.ExactValueRoot]().Root()
		},
		"output memo": func(poisoned *incrementalPreparedPlan) {
			copied := *fixture.plan.outputMemo
			poisoned.outputMemo = &copied
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			poisoned := *fixture.plan
			poison(&poisoned)
			err := poisoned.prepareRegistry(
				[]string{"backends"},
				map[string]*incrementalGroupIndex{"backends": fixture.index},
				fixture.results.Root(),
				rendercontext.NewPlanRegistry(nil),
			)
			require.ErrorContains(t, err, "authentication seal")
		})
	}
}

func TestIncrementalPreparedPlanRejectsStaleGroupLink(t *testing.T) {
	fixture := newIncrementalPreparedPlanFixture(t)
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	fixture.replace(t, "app", &result)
	equivalent := newIncrementalGroupIndex()
	instance := incrementalInstanceResult{
		component: "backends", source: "routes", namespace: "default", name: "app", result: result,
	}
	var err error
	equivalent, err = equivalent.replace(&instance, nil)
	require.NoError(t, err)

	err = fixture.plan.prepareRegistry(
		[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": equivalent},
		fixture.results.Root(), rendercontext.NewPlanRegistry(nil),
	)
	require.ErrorContains(t, err, "does not match its assembly index")
}

var incrementalPreparedPlanBenchmarkSink incrementalPreparedPlanRegistry

func BenchmarkIncrementalPreparedPlanPreparation(b *testing.B) {
	for _, declarations := range []int{1, 128, 8192} {
		b.Run(fmt.Sprintf("unchanged-%d", declarations), func(b *testing.B) {
			benchmarkIncrementalPreparedPlanUnchanged(b, declarations)
		})
		b.Run(fmt.Sprintf("one-change-%d", declarations), func(b *testing.B) {
			benchmarkIncrementalPreparedPlanOneChange(b, declarations)
		})
	}
}

func benchmarkIncrementalPreparedPlanUnchanged(b *testing.B, declarations int) {
	b.Helper()
	fixture := benchmarkIncrementalPreparedPlanFixture(b, declarations)
	b.ReportAllocs()
	b.ReportMetric(float64(declarations), "declarations")
	b.ResetTimer()
	for range b.N {
		registry := rendercontext.NewPlanRegistry(nil)
		if err := fixture.plan.prepareRegistry(
			[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": fixture.index},
			fixture.results.Root(), registry,
		); err != nil {
			b.Fatal(err)
		}
		incrementalPreparedPlanBenchmarkSink = registry
	}
}

func benchmarkIncrementalPreparedPlanOneChange(b *testing.B, declarations int) {
	b.Helper()
	fixture := benchmarkIncrementalPreparedPlanFixture(b, declarations)
	results := []incrementalComponentResult{
		benchmarkBackendPlanResult(b, "be_00000000", "first"),
		benchmarkBackendPlanResult(b, "be_00000000", "second"),
	}
	b.ReportAllocs()
	b.ReportMetric(float64(declarations), "declarations")
	b.ResetTimer()
	for iteration := range b.N {
		fixture.replace(b, "item-00000000", &results[iteration%len(results)])
		registry := rendercontext.NewPlanRegistry(nil)
		if err := fixture.plan.prepareRegistry(
			[]string{"backends"}, map[string]*incrementalGroupIndex{"backends": fixture.index},
			fixture.results.Root(), registry,
		); err != nil {
			b.Fatal(err)
		}
		incrementalPreparedPlanBenchmarkSink = registry
	}
}

func benchmarkIncrementalPreparedPlanFixture(tb testing.TB, declarations int) *incrementalPreparedPlanFixture {
	tb.Helper()
	fixture := newIncrementalPreparedPlanFixture(tb)
	for index := range declarations {
		name := fmt.Sprintf("%08d", index)
		result := benchmarkBackendPlanResult(tb, "be_"+name, "initial")
		fixture.replace(tb, "item-"+name, &result)
	}
	return fixture
}

func benchmarkBackendPlanResult(tb testing.TB, name, revision string) incrementalComponentResult {
	tb.Helper()
	plan := newIncrementalBackendPlanRecorder()
	token, err := plan.Backend(map[string]any{
		"name": name, "guid": revision,
	}, "backend "+name+"\n    # "+revision+"\n")
	require.NoError(tb, err)
	result, err := (&incrementalRecorder{plan: plan}).result(token)
	require.NoError(tb, err)
	return result
}
