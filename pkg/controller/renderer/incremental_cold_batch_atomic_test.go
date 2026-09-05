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
	"sync"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestColdGroupBatchInvalidLastResultLeavesSessionRootsUnchanged(t *testing.T) {
	component := incrementalComponent{name: "component", group: "group"}
	groupIndex := newIncrementalGroupIndex()
	session := &incrementalRenderSession{
		state: &incrementalRenderState{components: map[string]incrementalComponent{
			component.name: component,
		}},
		results:         iradix.New[incremental.ExactValueRoot]().Txn(),
		derived:         iradix.New[incrementalDerivedResource]().Txn(),
		httpEffects:     iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn(),
		retired:         iradix.New[struct{}]().Txn(),
		groupIndexes:    map[string]*incrementalGroupIndex{component.group: groupIndex},
		groupChanged:    map[string]bool{},
		freshResults:    map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted:    map[incremental.QueryKey][]incrementalHTTPEffect{},
		httpRefDeltas:   map[uint64]httpRefDelta{},
		selectorPending: map[incrementalSelectorIdentity]incremental.Input{},
		newQueries:      map[incremental.QueryKey]struct{}{},
		dirtyQueries:    map[incremental.QueryKey]struct{}{},
	}
	results := make([]incremental.ExactResult, 2)
	encodedByKey := make(map[incremental.QueryKey]string, len(results))
	resultsByKey := make(map[incremental.QueryKey]incrementalComponentResult, len(results))
	for index, name := range []string{"a", "z"} {
		key := session.registerComponentQuery(&component, "routes", "default", name)
		result := incrementalComponentResult{Text: name + "\n"}
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		encodedByKey[key] = string(encoded)
		resultsByKey[key] = result
		results[index].Key = key
	}
	graph, roots := testExactRoots(t, encodedByKey)
	session.state.graph = graph
	for index := range results {
		key := results[index].Key
		result := resultsByKey[key]
		encoded, fresh, err := newAuthenticatedFreshComponentResult(key, &result)
		require.NoError(t, err)
		require.Equal(t, encodedByKey[key], encoded)
		require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, key, roots[key]))
		session.freshResults[key] = fresh
		session.httpExecuted[key] = nil
		session.newQueries[key] = struct{}{}
		results[index].Value = roots[key]
	}
	session.freshResults[results[1].Key].encoded = "poison"

	resultRoot := session.results.Root()
	derivedRoot := session.derived.Root()
	httpRoot := session.httpEffects.Root()
	batched, err := session.applyColdGroupAdditions(component.group, results)
	require.ErrorContains(t, err, "invalid provenance")
	assert.False(t, batched)
	assert.Same(t, resultRoot, session.results.Root())
	assert.Same(t, derivedRoot, session.derived.Root())
	assert.Same(t, httpRoot, session.httpEffects.Root())
	assert.Same(t, groupIndex, session.groupIndexes[component.group])
	assert.Empty(t, session.httpRefDeltas)
	assert.Empty(t, session.selectorPending)
	assert.Len(t, session.freshResults, 2)
	assert.Len(t, session.httpExecuted, 2)
	assert.Len(t, session.newQueries, 2)
	assert.Same(t, session.freshResults[results[0].Key], session.freshResults[results[0].Key].seal)
	assert.JSONEq(t, `{"text":"a\n"}`, session.freshResults[results[0].Key].encoded)
}

func TestFreshResultAuthorityIgnoresCallerNestedPoison(t *testing.T) {
	identity := rendercontext.DerivedResourceIdentity{Resource: "routes", Namespace: "default", Name: "route"}
	tests := []struct {
		name   string
		result incrementalComponentResult
		poison func(*incrementalComponentResult)
	}{
		{
			name: "published value",
			result: incrementalComponentResult{Published: []incrementalPublishedValue{{
				Cell: "cell", Key: "key", Value: json.RawMessage(`{"value":"original"}`),
			}}},
			poison: func(result *incrementalComponentResult) { result.Published[0].Value[10] = 'p' },
		},
		{
			name: "derivation",
			result: incrementalComponentResult{Derivations: []rendercontext.DerivedResource{{
				Identity: identity, Source: []byte(`{"name":"original"}`), Value: []byte(`{"value":"original"}`),
			}}},
			poison: func(result *incrementalComponentResult) { result.Derivations[0].Value[10] = 'p' },
		},
		{
			name: "backend dependency",
			result: incrementalComponentResult{BackendPlan: []incrementalBackendPlanCall{{
				WhenAny: &incrementalBackendPlanCondition{Cell: "cell", Keys: []string{"original"}},
			}}},
			poison: func(result *incrementalComponentResult) { result.BackendPlan[0].WhenAny.Keys[0] = "poison" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := incremental.NewQueryKey("component")
			want := cloneIncrementalComponentResult(&test.result)
			root, fresh := testFreshExactResult(t, key, &test.result)
			test.poison(&test.result)

			require.NoError(t, validateAuthenticatedFreshComponentResult(fresh, key, root))
			materialized, err := materializeAuthenticatedFreshComponentResult(fresh, key, root)
			require.NoError(t, err)
			wantEncoded, err := json.Marshal(want)
			require.NoError(t, err)
			gotEncoded, err := json.Marshal(materialized)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantEncoded), string(gotEncoded))
		})
	}
}

func TestFreshResultAuthorityRejectsSameValueDifferentRoot(t *testing.T) {
	key := incremental.NewQueryKey("component")
	result := incrementalComponentResult{Text: "original\n"}
	root, fresh := testFreshExactResult(t, key, &result)
	encoded, err := root.Bytes()
	require.NoError(t, err)
	differentRoot := testExactRoot(t, key, encoded)

	require.ErrorContains(t,
		validateAuthenticatedFreshComponentResult(fresh, key, differentRoot),
		"authoritative root",
	)
	require.NoError(t, validateAuthenticatedFreshComponentResult(fresh, key, root))
}

func TestFreshResultAuthorityReturnsDetachedMaterializations(t *testing.T) {
	key := incremental.NewQueryKey("component")
	result := incrementalComponentResult{Published: []incrementalPublishedValue{{
		Cell: "cell", Key: "key", Value: json.RawMessage(`{"value":"original"}`),
	}}}
	root, fresh := testFreshExactResult(t, key, &result)

	first, err := materializeAuthenticatedFreshComponentResult(fresh, key, root)
	require.NoError(t, err)
	first.Published[0].Value[10] = 'p'
	second, err := materializeAuthenticatedFreshComponentResult(fresh, key, root)
	require.NoError(t, err)

	assert.JSONEq(t, `{"value":"original"}`, string(second.Published[0].Value))
}

func TestFreshResultEffectsCertificateBindsGeneratedPolicyAndIdentity(t *testing.T) {
	key := incremental.NewQueryKey("component")
	component := incrementalComponent{name: "component", group: "group", publishValue: true}
	recorder := &incrementalRecorder{}
	recorder.publishAfterPreflight("cell", "key", "", map[string]any{"value": "original"}, "shared.Publish")
	encoded, fresh, err := recorder.authenticatedResult(
		key, &component, "routes", "default", "route", "",
	)
	require.NoError(t, err)
	root := testExactRoot(t, key, []byte(encoded))
	require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, key, root))

	certified, err := validateAuthenticatedFreshComponentEffects(
		fresh, key, root, &component, "routes", "default", "route",
	)
	require.NoError(t, err)
	assert.True(t, certified)
	diagnostic, err := materializeAuthenticatedFreshComponentResult(fresh, key, root)
	require.NoError(t, err)
	diagnostic.Published[0].Value = json.RawMessage(`{"value":"poison"}`)
	diagnostic.PublishedDigest = "poison"
	certified, err = validateAuthenticatedFreshComponentEffects(
		fresh, key, root, &component, "routes", "default", "route",
	)
	require.NoError(t, err)
	assert.True(t, certified)

	_, err = validateAuthenticatedFreshComponentEffects(
		fresh, key, root, &component, "routes", "default", "other",
	)
	require.ErrorContains(t, err, "invalid provenance")
	otherPolicy := component
	otherPolicy.publishValue = false
	_, err = validateAuthenticatedFreshComponentEffects(
		fresh, key, root, &otherPolicy, "routes", "default", "route",
	)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestUncertifiedFreshResultStillReceivesFullEffectValidation(t *testing.T) {
	component := incrementalComponent{name: "component", group: "group", publishValue: true}
	key := incremental.NewQueryKey("component")
	result := incrementalComponentResult{
		Published: []incrementalPublishedValue{{
			Cell: "cell", Key: "key", Value: json.RawMessage(`{"value":"original"}`),
		}},
		PublishedDigest: "poison",
	}
	root, fresh := testFreshExactResult(t, key, &result)
	instance := &incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "route",
	}

	_, _, err := newIncrementalGroupIndex().addPreparedBatch([]incrementalPreparedGroupInstance{{
		instance: instance, component: &component, queryKey: key, fresh: fresh, encoded: root,
	}})
	require.ErrorContains(t, err, "invalid digest")
}

func TestFinalizedComponentInstallPreflightsEveryAuthority(t *testing.T) {
	firstKey := incremental.NewQueryKey("first")
	secondKey := incremental.NewQueryKey("second")
	firstEncoded, first, err := newAuthenticatedFreshComponentResult(
		firstKey, &incrementalComponentResult{Text: "first\n"},
	)
	require.NoError(t, err)
	secondEncoded, second, err := newAuthenticatedFreshComponentResult(
		secondKey, &incrementalComponentResult{Text: "second\n"},
	)
	require.NoError(t, err)
	copiedAuthority := *second.authority
	second.authority = &copiedAuthority
	session := &incrementalRenderSession{
		freshResults: map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted: map[incremental.QueryKey][]incrementalHTTPEffect{},
	}

	err = session.installFinalizedComponents(
		&finalizedIncrementalComponent{key: firstKey, encoded: firstEncoded, fresh: first},
		&finalizedIncrementalComponent{key: secondKey, encoded: secondEncoded, fresh: second},
	)
	require.ErrorContains(t, err, "invalid provenance")
	assert.Empty(t, session.freshResults)
	assert.Empty(t, session.httpExecuted)
	require.NoError(t, validatePendingAuthenticatedFreshComponentResult(first, firstKey))
}

func TestPreparedGroupBatchDetachesCertifiedFreshOwnership(t *testing.T) {
	component := incrementalComponent{name: "component", group: "group"}
	key := incremental.NewQueryKey("query")
	encoded, fresh, err := (&incrementalRecorder{}).authenticatedResult(
		key, &component, "routes", "default", "route", "original\n",
	)
	require.NoError(t, err)
	root := testExactRoot(t, key, []byte(encoded))
	require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, key, root))
	instance := &incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "route",
	}

	index, owned, err := newIncrementalGroupIndex().addPreparedBatch([]incrementalPreparedGroupInstance{{
		instance: instance, component: &component, queryKey: key, fresh: fresh, encoded: root,
	}})
	require.NoError(t, err)
	require.Len(t, owned, 1)
	assert.Same(t, fresh, fresh.seal)
	assert.Equal(t, "original\n", owned[0].Text)
	assert.Equal(t, "original\n", mustIncrementalGroupOutput(t, index, component.name))
	owned[0].Text = "poison\n"
	assert.Equal(t, "original\n", mustIncrementalGroupOutput(t, index, component.name))
	_, err = materializeAuthenticatedFreshComponentResult(fresh, key, root)
	require.ErrorContains(t, err, "already transferred")
	require.NoError(t, index.validateAuthentication())
}

func TestRecorderOwnedFreshResultDetachesRecorderState(t *testing.T) {
	component := incrementalComponent{name: "component", group: "group", publishValue: true}
	key := incremental.NewQueryKey("query")
	recorder := &incrementalRecorder{}
	recorder.publishAfterPreflight(
		"cell", "key", "", map[string]any{"value": "original"}, "shared.Publish",
	)
	encoded, fresh, err := recorder.authenticatedResult(
		key, &component, "routes", "default", "route", "",
	)
	require.NoError(t, err)
	root := testExactRoot(t, key, []byte(encoded))
	require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, key, root))
	recorder.published[0].Value[10] = 'p'

	result, err := materializeAuthenticatedFreshComponentResult(fresh, key, root)
	require.NoError(t, err)
	require.Len(t, result.Published, 1)
	assert.JSONEq(t, `{"value":"original"}`, string(result.Published[0].Value))
}

func TestPreparedGroupBatchInvalidLastItemDoesNotMutateCallerInstances(t *testing.T) {
	component := incrementalComponent{name: "component", group: "group"}
	candidates := make([]incrementalPreparedGroupInstance, 2)
	instances := make([]incrementalInstanceResult, 2)
	for index, name := range []string{"a", "z"} {
		key := incremental.NewQueryKey(name)
		result := incrementalComponentResult{Text: name + "\n"}
		root, fresh := testFreshExactResult(t, key, &result)
		instances[index] = incrementalInstanceResult{
			component: component.name, source: "routes", namespace: "default", name: name,
		}
		candidates[index] = incrementalPreparedGroupInstance{
			instance: &instances[index], component: &component, queryKey: key, fresh: fresh, encoded: root,
		}
	}
	candidates[1].fresh.encoded = "poison"

	_, _, err := newIncrementalGroupIndex().addPreparedBatch(candidates)
	require.ErrorContains(t, err, "invalid provenance")
	assert.Equal(t, incrementalComponentResult{}, instances[0].result)
	assert.Equal(t, incrementalComponentResult{}, instances[1].result)
	assert.Same(t, candidates[0].fresh, candidates[0].fresh.seal)
	assert.JSONEq(t, `{"text":"a\n"}`, candidates[0].fresh.encoded)
}

func TestPreparedColdGroupAdditionsRejectsOuterPoisonAtomically(t *testing.T) {
	tests := []struct {
		name   string
		poison func(*incrementalPreparedColdGroupAdditions)
	}{
		{name: "owner seal", poison: func(prepared *incrementalPreparedColdGroupAdditions) {
			prepared.seal = nil
		}},
		{name: "authority seal", poison: func(prepared *incrementalPreparedColdGroupAdditions) {
			prepared.authority.seal = nil
		}},
		{name: "authority copy", poison: func(prepared *incrementalPreparedColdGroupAdditions) {
			copied := *prepared.authority
			prepared.authority = &copied
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, results := newColdGroupBatchFixture(t, coldGroupBatchSpec{
				group: "group", component: "component", names: []string{"a", "z"},
			})
			prepared, applicable, err := session.prepareColdGroupAdditions("group", results["group"])
			require.NoError(t, err)
			require.True(t, applicable)
			before := captureColdGroupSessionState(session)

			test.poison(prepared)
			installed, err := session.installPreparedColdGroupAdditions(prepared)
			require.ErrorContains(t, err, "invalid provenance")
			assert.False(t, installed)
			assertColdGroupSessionState(t, session, "group", before)
		})
	}
}

func TestPreparedColdGroupAdditionsReauthenticatesEveryInputAtomically(t *testing.T) {
	tests := []struct {
		name       string
		poison     func(*incrementalRenderSession, []incremental.ExactResult)
		wantError  string
		afterState bool
	}{
		{
			name: "last fresh authority",
			poison: func(session *incrementalRenderSession, results []incremental.ExactResult) {
				session.freshResults[results[len(results)-1].Key].encoded = "poison"
			},
			wantError: "invalid provenance",
		},
		{
			name: "last HTTP effects",
			poison: func(session *incrementalRenderSession, results []incremental.ExactResult) {
				session.httpExecuted[results[len(results)-1].Key] = []incrementalHTTPEffect{{inputID: 7}}
			},
			wantError: "different HTTP effects",
		},
		{
			name: "last retirement",
			poison: func(session *incrementalRenderSession, results []incremental.ExactResult) {
				session.retired.Insert([]byte(results[len(results)-1].Key.Opaque()), struct{}{})
			},
			wantError:  "was retired",
			afterState: true,
		},
		{
			name: "last cached result",
			poison: func(session *incrementalRenderSession, results []incremental.ExactResult) {
				result := &results[len(results)-1]
				target, err := session.evaluatedResultTarget("group", result)
				require.NoError(t, err)
				session.results.Insert(target.key, result.Value)
			},
			wantError:  "already has a cached result",
			afterState: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, results := newColdGroupBatchFixture(t, coldGroupBatchSpec{
				group: "group", component: "component", names: []string{"a", "z"},
			})
			prepared, applicable, err := session.prepareColdGroupAdditions("group", results["group"])
			require.NoError(t, err)
			require.True(t, applicable)
			beforePoison := captureColdGroupSessionState(session)
			test.poison(session, results["group"])
			beforeInstall := beforePoison
			if test.afterState {
				beforeInstall = captureColdGroupSessionState(session)
			}

			installed, err := session.installPreparedColdGroupAdditions(prepared)
			require.ErrorContains(t, err, test.wantError)
			assert.False(t, installed)
			assertColdGroupSessionState(t, session, "group", beforeInstall)
			assert.Len(t, session.freshResults, 2)
			assert.Len(t, session.httpExecuted, 2)
			assert.Len(t, session.newQueries, 2)
		})
	}
}

func TestPreparedColdGroupAdditionsRejectsAnotherSession(t *testing.T) {
	first, firstResults := newColdGroupBatchFixture(t, coldGroupBatchSpec{
		group: "group", component: "component", names: []string{"route"},
	})
	second, _ := newColdGroupBatchFixture(t, coldGroupBatchSpec{
		group: "group", component: "component", names: []string{"route"},
	})
	prepared, applicable, err := first.prepareColdGroupAdditions("group", firstResults["group"])
	require.NoError(t, err)
	require.True(t, applicable)
	before := captureColdGroupSessionState(second)

	installed, err := second.installPreparedColdGroupAdditions(prepared)
	require.ErrorContains(t, err, "invalid provenance")
	assert.False(t, installed)
	assertColdGroupSessionState(t, second, "group", before)
}

func TestColdGroupAdditionsPrepareConcurrentlyAndInstallAtomically(t *testing.T) {
	specs := []coldGroupBatchSpec{
		{group: "first", component: "first-component", names: []string{"a", "b"}},
		{group: "second", component: "second-component", names: []string{"c", "d"}},
	}
	session, results := newColdGroupBatchFixture(t, specs...)
	prepared := make([]*incrementalPreparedColdGroupAdditions, len(specs))
	applicable := make([]bool, len(specs))
	errs := make([]error, len(specs))
	var wait sync.WaitGroup
	for index := range specs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			prepared[index], applicable[index], errs[index] = session.prepareColdGroupAdditions(
				specs[index].group, results[specs[index].group],
			)
		}()
	}
	wait.Wait()
	for index := range specs {
		require.NoError(t, errs[index])
		require.True(t, applicable[index])
	}

	for index := len(specs) - 1; index >= 0; index-- {
		installed, err := session.installPreparedColdGroupAdditions(prepared[index])
		require.NoError(t, err)
		require.True(t, installed)
	}
	resultCount := 0
	session.results.Root().Walk(func([]byte, incremental.ExactValueRoot) bool {
		resultCount++
		return false
	})
	assert.Equal(t, 4, resultCount)
	assert.Empty(t, session.freshResults)
	assert.Empty(t, session.httpExecuted)
	assert.Empty(t, session.newQueries)
	for _, spec := range specs {
		want := ""
		for _, name := range spec.names {
			want += spec.component + ":" + name + "\n"
		}
		assert.Equal(t,
			want,
			mustIncrementalGroupOutput(t, session.groupIndexes[spec.group], spec.component),
		)
	}
}

func TestRecorderFreshResultCertificateRejectsUnpublishedBackendCondition(t *testing.T) {
	component := incrementalComponent{name: "component", group: "group", backendPlan: true, publishValue: true}
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "backend"}, "backend backend\n", "cell", []string{"missing"},
	)
	require.NoError(t, err)

	_, _, err = recorder.authenticatedResult(
		incremental.NewQueryKey("query"), &component, "routes", "default", "route", token,
	)
	require.ErrorContains(t, err, "references unpublished value")
}

func TestPreparedColdBackendPlanBatchCommitsAfterSessionPreflight(t *testing.T) {
	session, results := newColdGroupBatchFixture(t, coldGroupBatchSpec{
		group: "group", component: "component", names: []string{"a", "z"}, backendPlan: true,
	})
	prepared, applicable, err := session.prepareColdGroupAdditions("group", results["group"])
	require.NoError(t, err)
	require.True(t, applicable)
	assert.Empty(t, session.preparedPlanColdBuilder.batches)
	session.freshResults[results["group"][1].Key].encoded = "poison"
	installed, err := session.installPreparedColdGroupAdditions(prepared)
	require.ErrorContains(t, err, "invalid provenance")
	assert.False(t, installed)
	assert.Empty(t, session.preparedPlanColdBuilder.batches)

	session, results = newColdGroupBatchFixture(t, coldGroupBatchSpec{
		group: "group", component: "component", names: []string{"a", "z"}, backendPlan: true,
	})
	prepared, applicable, err = session.prepareColdGroupAdditions("group", results["group"])
	require.NoError(t, err)
	require.True(t, applicable)
	installed, err = session.installPreparedColdGroupAdditions(prepared)
	require.NoError(t, err)
	assert.True(t, installed)
	assert.Len(t, session.preparedPlanColdBuilder.batches, 1)
}

type coldGroupBatchSpec struct {
	group       string
	component   string
	names       []string
	backendPlan bool
}

type coldGroupSessionState struct {
	results         *iradix.Node[incremental.ExactValueRoot]
	derived         *iradix.Node[incrementalDerivedResource]
	httpEffects     *iradix.Node[*iradix.Tree[incrementalHTTPEffect]]
	groupIndex      *incrementalGroupIndex
	httpRefDeltas   int
	selectorPending int
	groupChanged    bool
}

func captureColdGroupSessionState(
	session *incrementalRenderSession,
) coldGroupSessionState {
	const group = "group"
	return coldGroupSessionState{
		results:         session.results.Root(),
		derived:         session.derived.Root(),
		httpEffects:     session.httpEffects.Root(),
		groupIndex:      session.groupIndexes[group],
		httpRefDeltas:   len(session.httpRefDeltas),
		selectorPending: len(session.selectorPending),
		groupChanged:    session.groupChanged[group],
	}
}

func assertColdGroupSessionState(
	t *testing.T,
	session *incrementalRenderSession,
	group string,
	want coldGroupSessionState,
) {
	t.Helper()
	assert.Same(t, want.results, session.results.Root())
	assert.Same(t, want.derived, session.derived.Root())
	assert.Same(t, want.httpEffects, session.httpEffects.Root())
	assert.Same(t, want.groupIndex, session.groupIndexes[group])
	assert.Equal(t, want.httpRefDeltas, len(session.httpRefDeltas))
	assert.Equal(t, want.selectorPending, len(session.selectorPending))
	assert.Equal(t, want.groupChanged, session.groupChanged[group])
}

func newColdGroupBatchFixture(
	tb testing.TB,
	specs ...coldGroupBatchSpec,
) (session *incrementalRenderSession, resultsByGroup map[string][]incremental.ExactResult) {
	tb.Helper()
	state := &incrementalRenderState{components: map[string]incrementalComponent{}}
	session = &incrementalRenderSession{
		state:                        state,
		results:                      iradix.New[incremental.ExactValueRoot]().Txn(),
		derived:                      iradix.New[incrementalDerivedResource]().Txn(),
		httpEffects:                  iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn(),
		retired:                      iradix.New[struct{}]().Txn(),
		groupIndexes:                 map[string]*incrementalGroupIndex{},
		groupChanged:                 map[string]bool{},
		freshResults:                 map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted:                 map[incremental.QueryKey][]incrementalHTTPEffect{},
		httpRefDeltas:                map[uint64]httpRefDelta{},
		selectorPending:              map[incrementalSelectorIdentity]incremental.Input{},
		newQueries:                   map[incremental.QueryKey]struct{}{},
		dirtyQueries:                 map[incremental.QueryKey]struct{}{},
		preparedPlanBootstrapPending: true,
		statusPlanBootstrapPending:   true,
	}
	type pendingResult struct {
		key     incremental.QueryKey
		fresh   *authenticatedFreshComponentResult
		encoded string
	}
	values := map[incremental.QueryKey]string{}
	pending := map[string][]pendingResult{}
	results := map[string][]incremental.ExactResult{}
	preparedPlanGroups := make(map[string]struct{})
	for _, spec := range specs {
		component := incrementalComponent{
			name: spec.component, group: spec.group, backendPlan: spec.backendPlan,
		}
		if spec.backendPlan {
			preparedPlanGroups[spec.group] = struct{}{}
		}
		state.components[component.name] = component
		session.groupIndexes[spec.group] = newIncrementalGroupIndex()
		for _, name := range spec.names {
			key := session.registerComponentQuery(&component, "routes", "default", name)
			recorder := &incrementalRecorder{}
			text := component.name + ":" + name + "\n"
			if spec.backendPlan {
				plan := newIncrementalBackendPlanRecorder()
				var err error
				text, err = plan.Backend(
					map[string]any{"name": "backend-" + name}, "backend backend-"+name+"\n",
				)
				require.NoError(tb, err)
				recorder.plan = plan
			}
			encoded, fresh, err := recorder.authenticatedResult(
				key, &component, "routes", "default", name, text,
			)
			require.NoError(tb, err)
			values[key] = encoded
			pending[spec.group] = append(pending[spec.group], pendingResult{
				key: key, fresh: fresh, encoded: encoded,
			})
		}
	}
	graph, roots := testExactRoots(tb, values)
	state.graph = graph
	for _, spec := range specs {
		for _, item := range pending[spec.group] {
			root := roots[item.key]
			require.NoError(tb, bindAuthenticatedFreshComponentResult(item.fresh, item.key, root))
			session.freshResults[item.key] = item.fresh
			session.httpExecuted[item.key] = nil
			session.newQueries[item.key] = struct{}{}
			results[spec.group] = append(results[spec.group], incremental.ExactResult{
				Key: item.key, Value: root,
			})
		}
	}
	groups := make([]string, 0, len(preparedPlanGroups))
	for group := range preparedPlanGroups {
		groups = append(groups, group)
	}
	preparedPlanColdBuilder, err := newIncrementalPreparedPlanColdBuilder(groups, state.components)
	require.NoError(tb, err)
	session.preparedPlanColdBuilder = preparedPlanColdBuilder
	return session, results
}
