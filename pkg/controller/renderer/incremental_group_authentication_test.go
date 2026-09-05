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
	"slices"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalGroupAuthenticationRejectsNilAndUnsealedIndexes(t *testing.T) {
	index := newIncrementalGroupIndex()
	require.NoError(t, index.validateAuthentication())
	assert.Empty(t, mustIncrementalGroupOutput(t, index, "missing"))

	var nilIndex *incrementalGroupIndex
	_, err := nilIndex.output("component")
	require.ErrorContains(t, err, "unavailable")
	_, err = nilIndex.replace(&incrementalInstanceResult{component: "component"}, nil)
	require.ErrorContains(t, err, "unavailable")
	_, err = nilIndex.remove("component", "source", "namespace", "name")
	require.ErrorContains(t, err, "unavailable")

	unsealed := &incrementalGroupIndex{}
	require.ErrorContains(t, unsealed.validateAuthentication(), "unavailable")
	_, err = unsealed.publishedWinners("cell")
	require.ErrorContains(t, err, "unavailable")
	_, err = unsealed.allPublishedWinners()
	require.ErrorContains(t, err, "unavailable")
}

func TestIncrementalGroupAuthenticationRejectsEquivalentRootSubstitution(t *testing.T) {
	index, _ := authenticatedIncrementalGroupFixture(t)
	auditVisits := 0
	require.NoError(t, index.validateAuthenticationWithAudit(&auditVisits))
	assert.Zero(t, auditVisits)
	tests := []struct {
		name   string
		mutate func(*incrementalGroupIndex)
	}{
		{
			name: "instances",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.instances = cloneOrderedTree(index.instances)
			},
		},
		{
			name: "contributors",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.contributors = cloneIncrementalRadixTree(index.contributors)
			},
		},
		{
			name: "publications",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.publications = cloneOrderedTree(index.publications)
			},
		},
		{
			name: "publication counts",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.publicationCounts = cloneOrderedTree(index.publicationCounts)
			},
		},
		{
			name: "events",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.events = cloneIncrementalRadixTree(index.events)
			},
		},
		{
			name: "status patches",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.status = cloneIncrementalRadixTree(index.status)
			},
		},
		{
			name: "HTTP",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.http = cloneIncrementalRadixTree(index.http)
			},
		},
		{
			name: "outputs",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.outputs = cloneIncrementalRadixTree(index.outputs)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			poisoned := *index
			test.mutate(&poisoned)
			auditVisits := 0
			err := poisoned.validateAuthenticationWithAudit(&auditVisits)
			require.ErrorContains(t, err, "authentication seal")
			assert.Positive(t, auditVisits)
		})
	}
}

func TestIncrementalGroupAuthenticationRejectsStaleSeal(t *testing.T) {
	base, _ := authenticatedIncrementalGroupFixture(t)
	added := incrementalInstanceResult{
		component: "400-added", source: "routes", namespace: "default", name: "added",
		result: publishedResult(t, publicationValue(t, "hosts", "added", "added")),
	}
	updated, err := base.replace(&added, nil)
	require.NoError(t, err)
	poisoned := *updated
	poisoned.auth = base.auth
	require.ErrorContains(t, poisoned.validateAuthentication(), "authentication seal")
	_, err = poisoned.output("100-output")
	require.ErrorContains(t, err, "authentication seal")
	_, err = poisoned.renderedEvents()
	require.ErrorContains(t, err, "authentication seal")
	_, err = poisoned.httpEffects()
	require.ErrorContains(t, err, "authentication seal")
	_, err = poisoned.statusPatchCalls()
	require.ErrorContains(t, err, "authentication seal")
	_, err = poisoned.publishedWinners("hosts")
	require.ErrorContains(t, err, "authentication seal")
	_, err = poisoned.allPublishedWinners()
	require.ErrorContains(t, err, "authentication seal")
}

func TestIncrementalGroupAuthenticationRejectsPublicationCorruption(t *testing.T) {
	index, publicationInstance := authenticatedIncrementalGroupFixture(t)
	identity := incrementalPublicationIdentityKey("hosts", "shared")
	owners, exists := index.publications.Root().Get(identity)
	require.True(t, exists)
	location, publication, exists := owners.Root().Minimum()
	require.True(t, exists)

	fieldTests := []struct {
		name   string
		mutate func(*incrementalIndexedPublication)
	}{
		{name: "owner", mutate: func(value *incrementalIndexedPublication) { value.instance.name = "other" }},
		{name: "location", mutate: func(value *incrementalIndexedPublication) { value.location = "other" }},
		{name: "cell", mutate: func(value *incrementalIndexedPublication) { value.cell = "other" }},
		{name: "key", mutate: func(value *incrementalIndexedPublication) { value.key = "other" }},
		{name: "value", mutate: func(value *incrementalIndexedPublication) { value.value = `"other"` }},
	}
	for _, test := range fieldTests {
		t.Run(test.name, func(t *testing.T) {
			changed := publication
			test.mutate(&changed)
			ownerTxn := owners.Txn()
			ownerTxn.Insert([]byte(location), changed)
			publicationTxn := index.publications.Txn()
			publicationTxn.Insert(identity, ownerTxn.Commit())
			poisoned := *index
			poisoned.publications = publicationTxn.Commit()
			_, err := poisoned.publishedWinners("hosts")
			require.ErrorContains(t, err, "does not match")
		})
	}

	t.Run("missing", func(t *testing.T) {
		publications := index.publications.Txn()
		publications.Delete(identity)
		poisoned := *index
		poisoned.publications = publications.Commit()
		_, err := poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "missing an identity")
	})

	t.Run("extra", func(t *testing.T) {
		publications := index.publications.Txn()
		publications.Insert(incrementalPublicationIdentityKey("hosts", "extra"), owners)
		poisoned := *index
		poisoned.publications = publications.Commit()
		_, err := poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "no matching result")
	})

	t.Run("malformed cached JSON", func(t *testing.T) {
		poisoned := poisonIncrementalInstanceResult(t, index, &publicationInstance, "{")
		_, err := poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "decoding incremental publication result")
	})

	t.Run("wrong digest", func(t *testing.T) {
		key := incrementalGroupInstanceKey(incrementalGroupInstanceID{
			component: publicationInstance.component,
			source:    publicationInstance.source, namespace: publicationInstance.namespace, name: publicationInstance.name,
		})
		indexed, exists := index.instances.Root().Get(key)
		require.True(t, exists)
		result, err := decodeIndexedGroupInstanceResult(&indexed)
		require.NoError(t, err)
		result.Published[0].Value = []byte(`"poison"`)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		poisoned := poisonIncrementalInstanceResult(t, index, &publicationInstance, string(encoded))
		_, err = poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "invalid digest")
	})
}

func TestIncrementalGroupAuthenticationDetachesIndexedPayloads(t *testing.T) {
	index := poisonedIncrementalGroupPayloadIndex(t)
	requireIndexedPublicationsDetached(t, index)
	requireIndexedInstanceResultDetached(t, index)
	requireIndexedWinnersDetached(t, index)
	require.NoError(t, index.validateAuthentication())
}

func poisonedIncrementalGroupPayloadIndex(t *testing.T) *incrementalGroupIndex {
	t.Helper()
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "shared", map[string]any{
		"nested": map[string]any{"values": []any{"original"}},
	})
	require.NoError(t, recorder.RecordStatusPatch(
		"default", "route", "example.test/v1", "Route",
		"uid-route", "rv-route",
		map[string]map[string]any{"rendered": {"owner": "original"}}, "component", 7,
	))
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_app"}, "backend be_app\n", "hosts", []string{"shared"},
	)
	require.NoError(t, err)
	result, err := recorder.result(token)
	require.NoError(t, err)
	result.Events = []templating.RenderedEvent{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		Type: templating.EventTypeNormal, Reason: "Accepted", Message: "original",
	}}
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route", result: result,
	}
	httpEffects := []incrementalHTTPEffect{{
		inputID: 7, snapshot: httpstore.ContentSnapshot{URL: "https://example.test", Content: "original", Found: true},
	}}
	index, err := newIncrementalGroupIndex().replace(&instance, httpEffects)
	require.NoError(t, err)

	instance.result.Published[0].Value[0] = 'x'
	instance.result.BackendPlan[0].WhenAny.Keys[0] = "poison"
	instance.result.Events[0].Message = "poison"
	instance.result.StatusPatches[0].Variants[0] = 'x'
	httpEffects[0].snapshot.Content = "poison"
	return index
}

func requireIndexedPublicationsDetached(t *testing.T, index *incrementalGroupIndex) {
	t.Helper()
	values, err := decodeIncrementalPublishedWinners(index, "hosts")
	require.NoError(t, err)
	assert.Equal(t, "original", values[0].(map[string]any)["nested"].(map[string]any)["values"].([]any)[0])
	assert.Equal(t, "original", mustIncrementalGroupEvents(t, index)[0].Message)
	assert.Equal(t, "original", mustIncrementalGroupHTTP(t, index)[0].snapshot.Content)
	statusCalls, err := index.statusPatchCalls()
	require.NoError(t, err)
	statusPatch, err := decodeIncrementalStatusPatchCall(&statusCalls[0])
	require.NoError(t, err)
	assert.Equal(t, "original", statusPatch.Variants["rendered"]["owner"])
	statusCalls[0].Variants[0] = 'x'
	freshStatusCalls, err := index.statusPatchCalls()
	require.NoError(t, err)
	freshStatusPatch, err := decodeIncrementalStatusPatchCall(&freshStatusCalls[0])
	require.NoError(t, err)
	assert.Equal(t, "original", freshStatusPatch.Variants["rendered"]["owner"])
}

func requireIndexedInstanceResultDetached(t *testing.T, index *incrementalGroupIndex) {
	t.Helper()
	instanceKey, indexed, exists := index.instances.Root().Minimum()
	require.True(t, exists)
	decoded, err := decodeIndexedGroupInstanceResult(&indexed)
	require.NoError(t, err)
	decoded.Published[0].Value[0] = 'x'
	decoded.BackendPlan[0].WhenAny.Keys[0] = "poison"
	indexedEffects := indexedHTTPEffects(indexed.httpEffects)
	indexedEffects[0].snapshot.Content = "poison"
	stored, exists := index.instances.Root().Get([]byte(instanceKey))
	require.True(t, exists)
	fresh, err := decodeIndexedGroupInstanceResult(&stored)
	require.NoError(t, err)
	assert.Equal(t, "shared", fresh.BackendPlan[0].WhenAny.Keys[0])
	assert.Equal(t, "original", indexedHTTPEffects(stored.httpEffects)[0].snapshot.Content)
}

func requireIndexedWinnersDetached(t *testing.T, index *incrementalGroupIndex) {
	t.Helper()
	winners, err := index.publishedWinners("hosts")
	require.NoError(t, err)
	expectedLocation := slices.Clone(winners[0].location)
	winners[0].location[0] ^= 0xff
	winners[0].value.Value[0] = 'x'
	freshWinners, err := index.publishedWinners("hosts")
	require.NoError(t, err)
	assert.Equal(t, expectedLocation, freshWinners[0].location)
	decodedValue, err := decodeResourceValue(freshWinners[0].value.Value)
	require.NoError(t, err)
	assert.Equal(t, "original", decodedValue.(map[string]any)["nested"].(map[string]any)["values"].([]any)[0])
}

func TestIncrementalGroupAuthenticationRejectsApplyOnPoison(t *testing.T) {
	index, instance := authenticatedIncrementalGroupFixture(t)
	poisoned := *index
	poisoned.publications = cloneOrderedTree(index.publications)

	_, err := poisoned.replace(&instance, nil)
	require.ErrorContains(t, err, "authentication seal")
	_, err = poisoned.remove(instance.component, instance.source, instance.namespace, instance.name)
	require.ErrorContains(t, err, "authentication seal")

	values, err := decodeIncrementalPublishedWinners(index, "hosts")
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.NoError(t, index.validateAuthentication())
}

func TestIncrementalGroupAuthenticationValidatesChangedPublicationPaths(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t,
			publicationValue(t, "hosts", "a", "a"),
			publicationValue(t, "hosts", "b", "b"),
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	instance.result = publishedResult(t, publicationValue(t, "hosts", "b", "new-b"))
	index, err = index.replace(&instance, nil)
	require.NoError(t, err)
	_, exists := index.publications.Root().Get(incrementalPublicationIdentityKey("hosts", "a"))
	assert.False(t, exists)
	owners, exists := index.publications.Root().Get(incrementalPublicationIdentityKey("hosts", "b"))
	require.True(t, exists)
	require.Equal(t, 1, owners.Len())
	require.NoError(t, index.validateAuthentication())
}

func BenchmarkIncrementalPublishedValuesAuthenticatedRead(b *testing.B) {
	for _, size := range []int{1, 128, 8192} {
		b.Run(fmt.Sprintf("owners-%d", size), func(b *testing.B) {
			benchmarkIncrementalPublishedValuesAuthenticatedRead(b, size)
		})
	}
}

func benchmarkIncrementalPublishedValuesAuthenticatedRead(b *testing.B, size int) {
	b.Helper()
	index := benchmarkIncrementalPublicationIndex(b, size)
	owners, exists := index.publications.Root().Get(incrementalPublicationIdentityKey("hosts", "shared"))
	if !exists || owners.Len() != size {
		b.Fatalf("indexed %d owners, want %d", owners.Len(), size)
	}
	warmValues, warmCertificate, err := index.certifiedPublishedValues("hosts")
	if err != nil || len(warmValues) != 1 {
		b.Fatalf("prime %d values: %v", len(warmValues), err)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(size), "cached-owners")
	b.ResetTimer()
	for range b.N {
		values, certificate, err := index.certifiedPublishedValues("hosts")
		if err != nil || len(values) != 1 {
			b.Fatalf("read %d values: %v", len(values), err)
		}
		if &values[0] != &warmValues[0] || certificate != warmCertificate {
			b.Fatal("warm read did not reuse its authenticated value")
		}
		incrementalPublishedValuesSink = values
	}
}

func benchmarkIncrementalPublicationIndex(b *testing.B, size int) *incrementalGroupIndex {
	b.Helper()
	index := newIncrementalGroupIndex()
	for owner := range size {
		recorder := &incrementalRecorder{}
		recorder.Publish("hosts", "shared", map[string]any{"owner": owner})
		result, err := recorder.result("")
		if err != nil {
			b.Fatal(err)
		}
		instance := incrementalInstanceResult{
			component: "component", source: "routes", namespace: "default",
			name: fmt.Sprintf("route-%08d", owner), result: result,
		}
		index, err = index.replace(&instance, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	return index
}

func authenticatedIncrementalGroupFixture(
	t *testing.T,
) (*incrementalGroupIndex, incrementalInstanceResult) {
	t.Helper()
	unique := incrementalInstanceResult{
		component: "100-output", source: "routes", namespace: "default", name: "output",
		result: uniqueResult(t, "backends", "shared", "backend shared\n"),
	}
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "shared", map[string]any{"name": "original"})
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_original"}, "backend be_original\n", "hosts", []string{"shared"},
	)
	require.NoError(t, err)
	result, err := recorder.result(token)
	require.NoError(t, err)
	publication := incrementalInstanceResult{
		component: "200-publication", source: "routes", namespace: "default", name: "publication", result: result,
	}
	effectRecorder := &incrementalRecorder{}
	require.NoError(t, effectRecorder.RecordStatusPatch(
		"default", "effect", "example.test/v1", "Route",
		"uid-effect", "rv-effect",
		map[string]map[string]any{"rendered": {"accepted": true}}, "300-effect", 3,
	))
	effectResult, err := effectRecorder.result("")
	require.NoError(t, err)
	effectResult.Events = []templating.RenderedEvent{{
		Namespace: "default", Name: "effect", APIVersion: "example.test/v1", Kind: "Route",
		Type: templating.EventTypeNormal, Reason: "Accepted", Message: "accepted",
	}}
	effect := incrementalInstanceResult{
		component: "300-effect", source: "routes", namespace: "default", name: "effect",
		result: effectResult,
	}
	index := newIncrementalGroupIndex()
	index, err = index.replace(&unique, nil)
	require.NoError(t, err)
	index, err = index.replace(&publication, nil)
	require.NoError(t, err)
	index, err = index.replace(&effect, []incrementalHTTPEffect{{
		inputID: 1, snapshot: httpstore.ContentSnapshot{URL: "https://example.test", Content: "value", Found: true},
	}})
	require.NoError(t, err)
	return index, publication
}

func cloneIncrementalRadixTree[V any](source *iradix.Tree[V]) *iradix.Tree[V] {
	txn := iradix.New[V]().Txn()
	source.Root().Walk(func(key []byte, value V) bool {
		txn.Insert(slices.Clone(key), value)
		return false
	})
	return txn.Commit()
}

func cloneOrderedTree[V any](source *persistenttree.Tree[V]) *persistenttree.Tree[V] {
	txn := persistenttree.New[V]().Txn()
	source.Root().Walk(func(key string, value V) bool {
		txn.Insert([]byte(key), value)
		return false
	})
	return txn.Commit()
}

func poisonIncrementalInstanceResult(
	t *testing.T,
	index *incrementalGroupIndex,
	instance *incrementalInstanceResult,
	encoded string,
) *incrementalGroupIndex {
	t.Helper()
	key := incrementalGroupInstanceKey(incrementalGroupInstanceID{
		component: instance.component,
		source:    instance.source, namespace: instance.namespace, name: instance.name,
	})
	indexed, exists := index.instances.Root().Get(key)
	require.True(t, exists)
	indexed.encodedResult = encoded
	instances := index.instances.Txn()
	instances.Insert(key, indexed)
	poisoned := *index
	poisoned.instances = instances.Commit()
	return &poisoned
}

var incrementalPublishedValuesSink []any
