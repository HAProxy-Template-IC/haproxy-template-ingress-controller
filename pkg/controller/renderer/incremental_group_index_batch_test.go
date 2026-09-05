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

package renderer

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalGroupBatchFixture struct {
	component incrementalComponent
	instance  incrementalInstanceResult
	http      []incrementalHTTPEffect
}

func TestPreparedGroupBatchMatchesSequentialPermutations(t *testing.T) {
	fixtures := incrementalGroupBatchFixtures(t)
	permutations := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 4, 1, 3, 0},
		{1, 3, 0, 4, 2},
	}
	for permutationIndex, permutation := range permutations {
		t.Run(string(rune('a'+permutationIndex)), func(t *testing.T) {
			sequential := newIncrementalGroupIndex()
			for _, index := range permutation {
				var err error
				instance := fixtures[index].instance
				sequential, err = sequential.replace(&instance, fixtures[index].http)
				require.NoError(t, err)
			}

			candidates := make([]incrementalPreparedGroupInstance, len(permutation))
			wantOwned := make([]incrementalComponentResult, len(permutation))
			for candidateIndex, fixtureIndex := range permutation {
				fixture := &fixtures[fixtureIndex]
				key := incremental.NewQueryKey(fixture.instance.component + "/" + fixture.instance.name)
				root, fresh := testFreshExactResult(t, key, &fixture.instance.result)
				identity := fixture.instance
				identity.result = incrementalComponentResult{}
				candidates[candidateIndex] = incrementalPreparedGroupInstance{
					instance: &identity, component: &fixture.component, queryKey: key,
					fresh: fresh, encoded: root, httpEffects: slices.Clone(fixture.http),
				}
				wantOwned[candidateIndex] = fixture.instance.result
			}
			batched, owned, err := newIncrementalGroupIndex().addPreparedBatch(candidates)
			require.NoError(t, err)
			for index := range owned {
				wantEncoded, marshalErr := json.Marshal(wantOwned[index])
				require.NoError(t, marshalErr)
				gotEncoded, marshalErr := json.Marshal(owned[index])
				require.NoError(t, marshalErr)
				assert.Equal(t, string(wantEncoded), string(gotEncoded))
			}
			assertIncrementalGroupIndexesEquivalent(t, sequential, batched, fixtures)
		})
	}
}

func TestPreparedGroupBatchPreservesOrInvalidatesWarmMemoByWinner(t *testing.T) {
	component := incrementalComponent{name: "producer", group: "group", publishValue: true}
	baseInstance := incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "m",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "base")),
	}
	base, err := newIncrementalGroupIndex().replace(&baseInstance, nil)
	require.NoError(t, err)
	baseValues, baseCertificate, err := base.certifiedPublishedValues("values")
	require.NoError(t, err)
	baseProjection, exists := base.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("values"))
	require.True(t, exists)
	baseEntry := incrementalGroupPublishedMemoEntry(t, base, "values")

	loser := incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "z",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "loser")),
	}
	losingBatch, _, err := base.addPreparedBatch([]incrementalPreparedGroupInstance{
		preparedMemoBatchCandidate(t, &component, &loser),
	})
	require.NoError(t, err)
	losingProjection, exists := losingBatch.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("values"))
	require.True(t, exists)
	assert.Same(t, baseProjection.Root(), losingProjection.Root())
	losingValues, losingCertificate, err := losingBatch.certifiedPublishedValues("values")
	require.NoError(t, err)
	assert.Same(t, &baseValues[0], &losingValues[0])
	assert.Same(t, baseCertificate, losingCertificate)
	assert.Same(t, baseEntry, incrementalGroupPublishedMemoEntry(t, losingBatch, "values"))

	winner := incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "a",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "winner")),
	}
	winningBatch, _, err := base.addPreparedBatch([]incrementalPreparedGroupInstance{
		preparedMemoBatchCandidate(t, &component, &winner),
	})
	require.NoError(t, err)
	assert.Nil(t, incrementalGroupPublishedMemoEntryIfPresent(winningBatch, "values"))
	assert.Same(t, baseEntry, incrementalGroupPublishedMemoEntry(t, base, "values"))
	winningValues, winningCertificate, err := winningBatch.certifiedPublishedValues("values")
	require.NoError(t, err)
	require.Equal(t, []any{"winner"}, winningValues)
	assert.NotSame(t, &baseValues[0], &winningValues[0])
	assert.NotSame(t, baseCertificate, winningCertificate)
	parentValues, parentCertificate, err := base.certifiedPublishedValues("values")
	require.NoError(t, err)
	assert.Same(t, &baseValues[0], &parentValues[0])
	assert.Same(t, baseCertificate, parentCertificate)
}

func TestPreparedGroupBatchAuthenticatesEmptyInput(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, memoPublicationValue(t, "values", "key", "value")),
	}
	base, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	updated, err := base.replace(&instance, nil)
	require.NoError(t, err)
	poisoned := *updated
	poisoned.memo = base.memo
	poisoned.authenticate()

	_, _, err = poisoned.addPreparedBatch(nil)
	require.ErrorContains(t, err, "authentication seal")
}

func preparedMemoBatchCandidate(
	t *testing.T,
	component *incrementalComponent,
	instance *incrementalInstanceResult,
) incrementalPreparedGroupInstance {
	t.Helper()
	key := incremental.NewQueryKey(instance.component + "/" + instance.name)
	root, fresh := testFreshExactResult(t, key, &instance.result)
	identity := *instance
	identity.result = incrementalComponentResult{}
	return incrementalPreparedGroupInstance{
		instance: &identity, component: component, queryKey: key, fresh: fresh, encoded: root,
	}
}

func incrementalGroupBatchFixtures(t *testing.T) []incrementalGroupBatchFixture {
	t.Helper()
	publicationComponent := incrementalComponent{
		name: "100-publication", group: "group", publishValue: true,
		deriveResource: true, recordEvent: true, statusPatch: true,
	}
	uniqueComponent := incrementalComponent{name: "200-unique", group: "group", recordEvent: true}
	textComponent := incrementalComponent{name: "300-text", group: "group"}
	backendComponent := incrementalComponent{name: "400-backend", group: "group", backendPlan: true}
	publicationA := incrementalPublicationEffectResult(t, "a", "200", "a")
	publicationB := incrementalPublicationEffectResult(t, "b", "100", "b")
	unique := uniqueResult(t, "unique", "shared", "unique-a\n")
	unique.Events = []templating.RenderedEvent{incrementalBatchEvent("unique")}
	text := incrementalComponentResult{Text: "text\n"}
	backend := backendPlanResult(t, map[string]any{
		"name": "be_shared", "mode": "http", "guid": "batch",
	}, "backend be_shared\n    # batch\n", nil)
	httpOne := incrementalHTTPEffect{inputID: 1, snapshot: httpstore.ContentSnapshot{
		URL: "https://one.test", Content: "one", Found: true,
	}}
	httpTwo := incrementalHTTPEffect{inputID: 2, snapshot: httpstore.ContentSnapshot{
		URL: "https://two.test", Content: "two", Found: true,
	}}
	return []incrementalGroupBatchFixture{
		{
			component: publicationComponent,
			instance: incrementalInstanceResult{
				component: publicationComponent.name, source: "routes", namespace: "default", name: "a",
				result: publicationA,
			},
			http: []incrementalHTTPEffect{httpOne},
		},
		{
			component: publicationComponent,
			instance: incrementalInstanceResult{
				component: publicationComponent.name, source: "routes", namespace: "default", name: "b",
				result: publicationB,
			},
			http: []incrementalHTTPEffect{httpOne, httpTwo},
		},
		{
			component: uniqueComponent,
			instance: incrementalInstanceResult{
				component: uniqueComponent.name, source: "routes", namespace: "default", name: "a",
				result: unique,
			},
		},
		{
			component: textComponent,
			instance: incrementalInstanceResult{
				component: textComponent.name, source: "routes", namespace: "default", name: "z",
				result: text,
			},
		},
		{
			component: backendComponent,
			instance: incrementalInstanceResult{
				component: backendComponent.name, source: "routes", namespace: "default", name: "backend",
				result: backend,
			},
		},
	}
}

func incrementalPublicationEffectResult(
	t *testing.T,
	name, rank, value string,
) incrementalComponentResult {
	t.Helper()
	recorder := &incrementalRecorder{}
	recorder.PublishRanked("ranked", "shared", rank, value)
	recorder.Publish("values", name, map[string]any{"name": value})
	require.NoError(t, recorder.RecordStatusPatch(
		"default", name, "example.test/v1", "Route",
		"uid-"+name, "rv-"+name,
		map[string]map[string]any{"rendered": {"value": value}}, "component", 1,
	))
	result, err := recorder.result("")
	require.NoError(t, err)
	result.Events = []templating.RenderedEvent{incrementalBatchEvent(name)}
	source, err := encodeResourceValue(map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": name},
	})
	require.NoError(t, err)
	derived, err := encodeResourceValue(map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": name}, "value": value,
	})
	require.NoError(t, err)
	result.Derivations = []rendercontext.DerivedResource{{
		Identity: rendercontext.DerivedResourceIdentity{Resource: "routes", Namespace: "default", Name: name},
		Source:   source, Value: derived,
	}}
	return result
}

func incrementalBatchEvent(name string) templating.RenderedEvent {
	return templating.RenderedEvent{
		Namespace: "default", Name: name, APIVersion: "example.test/v1", Kind: "Route",
		Type: templating.EventTypeNormal, Reason: "Accepted", Message: "accepted " + name,
	}
}

func assertIncrementalGroupIndexesEquivalent(
	t *testing.T,
	want, got *incrementalGroupIndex,
	fixtures []incrementalGroupBatchFixture,
) {
	t.Helper()
	require.NoError(t, want.validateAuthentication())
	require.NoError(t, got.validateAuthentication())
	components := map[string]struct{}{}
	for index := range fixtures {
		components[fixtures[index].component.name] = struct{}{}
	}
	for component := range components {
		assert.Equal(t, mustIncrementalGroupOutput(t, want, component), mustIncrementalGroupOutput(t, got, component))
	}
	wantEvents, err := want.renderedEvents()
	require.NoError(t, err)
	gotEvents, err := got.renderedEvents()
	require.NoError(t, err)
	assert.Equal(t, wantEvents, gotEvents)
	wantStatus, err := want.statusPatchCalls()
	require.NoError(t, err)
	gotStatus, err := got.statusPatchCalls()
	require.NoError(t, err)
	assert.Equal(t, wantStatus, gotStatus)
	wantHTTP, err := want.httpEffects()
	require.NoError(t, err)
	gotHTTP, err := got.httpEffects()
	require.NoError(t, err)
	assert.Equal(t, wantHTTP, gotHTTP)
	wantPublished, err := want.allPublishedWinners()
	require.NoError(t, err)
	gotPublished, err := got.allPublishedWinners()
	require.NoError(t, err)
	assert.Equal(t, wantPublished, gotPublished)
	for _, cell := range []string{"ranked", "values"} {
		wantByLocation, err := want.publishedWinners(cell)
		require.NoError(t, err)
		gotByLocation, err := got.publishedWinners(cell)
		require.NoError(t, err)
		assert.Equal(t, wantByLocation, gotByLocation)
		wantByRank, err := want.rankedPublishedWinners(cell)
		require.NoError(t, err)
		gotByRank, err := got.rankedPublishedWinners(cell)
		require.NoError(t, err)
		assert.Equal(t, wantByRank, gotByRank)
		wantCount, err := want.publishedWinnerCount(cell)
		require.NoError(t, err)
		gotCount, err := got.publishedWinnerCount(cell)
		require.NoError(t, err)
		assert.Equal(t, wantCount, gotCount)
	}
}
