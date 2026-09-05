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
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func publishedResult(t *testing.T, values ...incrementalPublishedValue) incrementalComponentResult {
	t.Helper()
	recorder := &incrementalRecorder{}
	for index := range values {
		decoded, err := decodeResourceValue(values[index].Value)
		require.NoError(t, err)
		if values[index].Rank == "" {
			recorder.Publish(values[index].Cell, values[index].Key, decoded)
		} else {
			recorder.PublishRanked(values[index].Cell, values[index].Key, values[index].Rank, decoded)
		}
	}
	result, err := recorder.result("")
	require.NoError(t, err)
	return result
}

func rankedPublicationValue(t *testing.T, rank, name string) incrementalPublishedValue {
	t.Helper()
	const cell, key = "targets", "default/echo"
	value := publicationValue(t, cell, key, name)
	value.Rank = rank
	return value
}

func publicationValue(t *testing.T, cell, key, name string) incrementalPublishedValue {
	t.Helper()
	encoded, err := encodeResourceValue(map[string]any{
		"name": name,
		"nested": map[string]any{
			"values": []any{name, map[string]any{"value": name}},
		},
	})
	require.NoError(t, err)
	return incrementalPublishedValue{Cell: cell, Key: key, Value: encoded}
}

func TestIncrementalPublishRecorderDetachesAndRejectsInvalidValues(t *testing.T) {
	value := map[string]any{"nested": map[string]any{"values": []any{"original"}}}
	recorder := &incrementalRecorder{}
	recorder.Publish("hosts", "example.test", value)
	value["nested"].(map[string]any)["values"].([]any)[0] = "poison"
	result, err := recorder.result("")
	require.NoError(t, err)
	require.NoError(t, validateIncrementalInstanceResult(&result))
	decoded, err := decodeResourceValue(result.Published[0].Value)
	require.NoError(t, err)
	assert.Equal(t, "original", decoded.(map[string]any)["nested"].(map[string]any)["values"].([]any)[0])

	invalid := &incrementalRecorder{}
	invalid.Publish("hosts", "bad", make(chan struct{}))
	_, err = invalid.result("")
	require.ErrorContains(t, err, "not JSON serializable")

	for _, fields := range [][2]string{{"", "key"}, {"cell", ""}} {
		empty := &incrementalRecorder{}
		empty.Publish(fields[0], fields[1], "value")
		_, err = empty.result("")
		require.ErrorContains(t, err, "non-empty cell and key")
	}
}

func TestIncrementalPublishMixesOnlyWithLogicalBackendPlan(t *testing.T) {
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "b", map[string]any{"name": "backend"})
	recorder.Publish("hosts", "a", map[string]any{"name": "backend"})
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_app"},
		"backend be_app\n",
		"hosts",
		[]string{"b", "a", "a"},
	)
	require.NoError(t, err)
	result, err := recorder.result(" \t" + token)
	require.NoError(t, err)
	require.NoError(t, validateIncrementalInstanceResult(&result))
	assert.Equal(t, []string{"a", "b"}, result.BackendPlan[0].WhenAny.Keys)

	_, err = recorder.result("ordinary\n" + token)
	require.ErrorContains(t, err, "cannot mix shared.Publish with text")
}

func TestIncrementalBackendWhenAnyRejectsMissingPublications(t *testing.T) {
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "present", "value")
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_app"}, "backend be_app\n", "hosts", []string{"missing"},
	)
	require.NoError(t, err)
	result, err := recorder.result(token)
	require.NoError(t, err)
	require.ErrorContains(t, validateIncrementalInstanceResult(&result), "references unpublished value")

	for name, call := range map[string]func() error{
		"empty cell": func() error {
			_, callErr := plan.BackendWhenAny(map[string]any{"name": "a"}, "backend a\n", "", []string{"a"})
			return callErr
		},
		"empty keys": func() error {
			_, callErr := plan.BackendWhenAny(map[string]any{"name": "a"}, "backend a\n", "cell", nil)
			return callErr
		},
		"empty key": func() error {
			_, callErr := plan.BackendWhenAny(map[string]any{"name": "a"}, "backend a\n", "cell", []string{""})
			return callErr
		},
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, call()) })
	}
}

func TestIncrementalBackendWhenAnyCachedConditionsFailClosed(t *testing.T) {
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "a", "a")
	recorder.Publish("hosts", "b", "b")
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_app"}, "backend be_app\n", "hosts", []string{"b", "a"},
	)
	require.NoError(t, err)
	base, err := recorder.result(token)
	require.NoError(t, err)
	require.NoError(t, validateIncrementalInstanceResult(&base))

	tests := []struct {
		name   string
		mutate func(*incrementalComponentResult)
		want   string
	}{
		{
			name: "empty cell",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Cell = ""
			},
			want: "invalid condition",
		},
		{
			name: "empty keys",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Keys = nil
			},
			want: "invalid condition",
		},
		{
			name: "unordered keys",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Keys = []string{"b", "a"}
			},
			want: "not canonical",
		},
		{
			name: "duplicate keys",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Keys = []string{"a", "a"}
			},
			want: "not canonical",
		},
		{
			name: "empty key",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Keys = []string{""}
			},
			want: "not canonical",
		},
		{
			name: "unpublished key",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Keys = []string{"missing"}
			},
			want: "references unpublished value",
		},
		{
			name: "condition digest",
			mutate: func(result *incrementalComponentResult) {
				result.BackendPlan[0].WhenAny.Keys = []string{"a"}
			},
			want: "invalid digest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			poisoned := cloneIndexedComponentResult(&base)
			test.mutate(&poisoned)
			require.ErrorContains(t, validateIncrementalInstanceResult(&poisoned), test.want)
		})
	}
}

func TestIncrementalPublishedValuesWinnerOrderPromotionAndFreshDetach(t *testing.T) {
	instances := []incrementalInstanceResult{
		{component: "200-b", source: "routes", namespace: "default", name: "a", result: publishedResult(t,
			publicationValue(t, "hosts", "z", "b-z"),
		)},
		{component: "100-a", source: "routes", namespace: "default", name: "z", result: publishedResult(t,
			publicationValue(t, "hosts", "z", "a-z"),
			publicationValue(t, "hosts", "a", "a-a"),
			publicationValue(t, "other", "x", "other"),
		)},
	}
	index := newIncrementalGroupIndex()
	var err error
	for instanceIndex := range instances {
		index, err = index.replace(&instances[instanceIndex], nil)
		require.NoError(t, err)
	}
	values, err := decodeIncrementalPublishedWinners(index, "hosts")
	require.NoError(t, err)
	require.Len(t, values, 2)
	assert.Equal(t, "a-z", values[0].(map[string]any)["name"])
	assert.Equal(t, "a-a", values[1].(map[string]any)["name"])
	values[0].(map[string]any)["nested"].(map[string]any)["values"].([]any)[1].(map[string]any)["value"] = "poison"

	fresh, err := decodeIncrementalPublishedWinners(index, "hosts")
	require.NoError(t, err)
	assert.Equal(t, "a-z", fresh[0].(map[string]any)["nested"].(map[string]any)["values"].([]any)[1].(map[string]any)["value"])
	other, err := decodeIncrementalPublishedWinners(index, "other")
	require.NoError(t, err)
	require.Len(t, other, 1)
	assert.Equal(t, "other", other[0].(map[string]any)["name"])

	index, err = index.remove("100-a", "routes", "default", "z")
	require.NoError(t, err)
	promoted, err := decodeIncrementalPublishedWinners(index, "hosts")
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	assert.Equal(t, "b-z", promoted[0].(map[string]any)["name"])

	empty, err := decodeIncrementalPublishedWinners(index, "missing")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestIncrementalRankedPublishedValuesUseRankThenStableOwner(t *testing.T) {
	earlierOwner := incrementalInstanceResult{
		component: "100-owner", source: "policies", namespace: "default", name: "newer",
		result: publishedResult(t,
			rankedPublicationValue(t, "2026-08-25/default/newer", "newer"),
		),
	}
	laterOwner := incrementalInstanceResult{
		component: "200-owner", source: "policies", namespace: "default", name: "older",
		result: publishedResult(t,
			rankedPublicationValue(t, "2025-08-25/default/older", "older"),
		),
	}
	tiedOwner := incrementalInstanceResult{
		component: "300-owner", source: "policies", namespace: "default", name: "tie",
		result: publishedResult(t,
			rankedPublicationValue(t, "2025-08-25/default/older", "tie"),
		),
	}
	index := newIncrementalGroupIndex()
	var err error
	for _, instance := range []*incrementalInstanceResult{&earlierOwner, &laterOwner, &tiedOwner} {
		index, err = index.replace(instance, nil)
		require.NoError(t, err)
	}

	winners, err := decodeIncrementalPublishedWinners(index, "targets")
	require.NoError(t, err)
	require.Len(t, winners, 1)
	assert.Equal(t, "older", winners[0].(map[string]any)["name"])

	index, err = index.remove("200-owner", "policies", "default", "older")
	require.NoError(t, err)
	winners, err = decodeIncrementalPublishedWinners(index, "targets")
	require.NoError(t, err)
	require.Len(t, winners, 1)
	assert.Equal(t, "tie", winners[0].(map[string]any)["name"])

	index, err = index.remove("300-owner", "policies", "default", "tie")
	require.NoError(t, err)
	winners, err = decodeIncrementalPublishedWinners(index, "targets")
	require.NoError(t, err)
	require.Len(t, winners, 1)
	assert.Equal(t, "newer", winners[0].(map[string]any)["name"])
}

func TestIncrementalPublicationIdentityRejectsMixedRanking(t *testing.T) {
	plain := incrementalInstanceResult{
		component: "plain", source: "policies", namespace: "default", name: "plain",
		result: publishedResult(t, publicationValue(t, "targets", "default/echo", "plain")),
	}
	ranked := incrementalInstanceResult{
		component: "ranked", source: "policies", namespace: "default", name: "ranked",
		result: publishedResult(t,
			rankedPublicationValue(t, "2026/default/ranked", "ranked"),
		),
	}
	index, err := newIncrementalGroupIndex().replace(&plain, nil)
	require.NoError(t, err)
	_, err = index.replace(&ranked, nil)
	require.ErrorContains(t, err, "mixes ranked and unranked")
}

func TestIncrementalPublishedValuesIndexesFailClosedOnPoison(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, publicationValue(t, "hosts", "host", "value")),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)

	t.Run("owner reference", func(t *testing.T) {
		poisoned := *index
		identity := incrementalPublicationIdentityKey("hosts", "host")
		owners, exists := index.publications.Root().Get(identity)
		require.True(t, exists)
		location, publication, exists := owners.Root().Minimum()
		require.True(t, exists)
		publication.instance.name = "other"
		ownerTxn := owners.Txn()
		ownerTxn.Insert([]byte(location), publication)
		publicationTxn := index.publications.Txn()
		publicationTxn.Insert(identity, ownerTxn.Commit())
		poisoned.publications = publicationTxn.Commit()
		_, err = poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "does not match")
	})

	t.Run("payload digest", func(t *testing.T) {
		poisoned := *index
		key, indexed, exists := index.instances.Root().Minimum()
		require.True(t, exists)
		result, err := decodeIndexedGroupInstanceResult(&indexed)
		require.NoError(t, err)
		result.Published[0].Value = []byte(`{"name":"poison"}`)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		indexed.encodedResult = string(encoded)
		instances := index.instances.Txn()
		instances.Insert([]byte(key), indexed)
		poisoned.instances = instances.Commit()
		_, err = poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "invalid digest")
	})

	t.Run("orphan index", func(t *testing.T) {
		poisoned := *index
		publications := index.publications.Txn()
		publications.Insert(incrementalPublicationIdentityKey("hosts", "orphan"),
			persistenttree.New[incrementalIndexedPublication]())
		poisoned.publications = publications.Commit()
		_, err := poisoned.publishedWinners("hosts")
		require.ErrorContains(t, err, "empty identity")
	})
}

func TestIncrementalConditionalBackendUsesPublicationOwner(t *testing.T) {
	makeInstance := func(component, name, backend string) incrementalBackendPlanInstance {
		plan := newIncrementalBackendPlanRecorder()
		recorder := &incrementalRecorder{plan: plan}
		recorder.Publish("hosts", "shared", map[string]any{"name": name})
		token, err := plan.BackendWhenAny(
			map[string]any{"name": backend}, "backend "+backend+"\n", "hosts", []string{"shared"},
		)
		require.NoError(t, err)
		result, err := recorder.result(token)
		require.NoError(t, err)
		return incrementalBackendPlanInstance{
			group: "group",
			incrementalInstanceResult: incrementalInstanceResult{
				component: component, source: "routes", namespace: "default", name: name, result: result,
			},
		}
	}
	earlier := makeInstance("200-plan", "a", "be_a")
	later := makeInstance("200-plan", "b", "be_b")
	index := newIncrementalGroupIndex()
	for _, instance := range []*incrementalBackendPlanInstance{&later, &earlier} {
		var err error
		index, err = index.replace(&instance.incrementalInstanceResult, nil)
		require.NoError(t, err)
	}
	owners, err := incrementalBackendPlanPublicationOwnersForGroups(
		[]string{"group"}, map[string]*incrementalGroupIndex{"group": index},
	)
	require.NoError(t, err)
	outputs, err := replayIncrementalBackendPlansWithPublications(
		[]incrementalBackendPlanInstance{later, earlier}, owners, rendercontext.NewPlanRegistry(nil),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, outputs["group"]["200-plan"])

	index, err = index.remove("200-plan", "routes", "default", "a")
	require.NoError(t, err)
	owners, err = incrementalBackendPlanPublicationOwnersForGroups(
		[]string{"group"}, map[string]*incrementalGroupIndex{"group": index},
	)
	require.NoError(t, err)
	outputs, err = replayIncrementalBackendPlansWithPublications(
		[]incrementalBackendPlanInstance{later}, owners, rendercontext.NewPlanRegistry(nil),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, outputs["group"]["200-plan"])
}

func TestIncrementalConditionalBackendIncludesNonPlanCompetitorsAndIsolatesGroups(t *testing.T) {
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "shared", "plan")
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_plan"}, "backend be_plan\n", "hosts", []string{"shared"},
	)
	require.NoError(t, err)
	result, err := recorder.result(token)
	require.NoError(t, err)
	planInstance := incrementalBackendPlanInstance{
		group: "group-a",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "200-plan", source: "routes", namespace: "default", name: "route", result: result,
		},
	}
	competitor := incrementalInstanceResult{
		component: "100-competitor", source: "claims", namespace: "default", name: "claim",
		result: publishedResult(t, publicationValue(t, "hosts", "shared", "competitor")),
	}
	indexA := newIncrementalGroupIndex()
	indexA, err = indexA.replace(&planInstance.incrementalInstanceResult, nil)
	require.NoError(t, err)
	indexA, err = indexA.replace(&competitor, nil)
	require.NoError(t, err)
	other := incrementalInstanceResult{
		component: "000-other", source: "claims", namespace: "default", name: "claim",
		result: publishedResult(t, publicationValue(t, "hosts", "shared", "other")),
	}
	indexB, err := newIncrementalGroupIndex().replace(&other, nil)
	require.NoError(t, err)
	owners, err := incrementalBackendPlanPublicationOwnersForGroups(
		[]string{"group-a"}, map[string]*incrementalGroupIndex{"group-a": indexA, "group-b": indexB},
	)
	require.NoError(t, err)
	outputs, err := replayIncrementalBackendPlansWithPublications(
		[]incrementalBackendPlanInstance{planInstance}, owners, rendercontext.NewPlanRegistry(nil),
	)
	require.NoError(t, err)
	assert.Empty(t, outputs["group-a"]["200-plan"])

	indexA, err = indexA.remove("100-competitor", "claims", "default", "claim")
	require.NoError(t, err)
	owners, err = incrementalBackendPlanPublicationOwnersForGroups(
		[]string{"group-a"}, map[string]*incrementalGroupIndex{"group-a": indexA, "group-b": indexB},
	)
	require.NoError(t, err)
	outputs, err = replayIncrementalBackendPlansWithPublications(
		[]incrementalBackendPlanInstance{planInstance}, owners, rendercontext.NewPlanRegistry(nil),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, outputs["group-a"]["200-plan"])
}

func TestIncrementalConditionalBackendUsesAnyOwnedKey(t *testing.T) {
	plan := newIncrementalBackendPlanRecorder()
	recorder := &incrementalRecorder{plan: plan}
	recorder.Publish("hosts", "lost", "plan-lost")
	recorder.Publish("hosts", "owned", "plan-owned")
	token, err := plan.BackendWhenAny(
		map[string]any{"name": "be_plan"}, "backend be_plan\n", "hosts", []string{"owned", "lost"},
	)
	require.NoError(t, err)
	result, err := recorder.result(token)
	require.NoError(t, err)
	planInstance := incrementalBackendPlanInstance{
		group: "group",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "200-plan", source: "routes", namespace: "default", name: "route", result: result,
		},
	}
	lostCompetitor := incrementalInstanceResult{
		component: "100-competitor", source: "claims", namespace: "default", name: "lost",
		result: publishedResult(t, publicationValue(t, "hosts", "lost", "competitor")),
	}
	index := newIncrementalGroupIndex()
	index, err = index.replace(&planInstance.incrementalInstanceResult, nil)
	require.NoError(t, err)
	index, err = index.replace(&lostCompetitor, nil)
	require.NoError(t, err)

	owners, err := incrementalBackendPlanPublicationOwnersForGroups(
		[]string{"group"}, map[string]*incrementalGroupIndex{"group": index},
	)
	require.NoError(t, err)
	outputs, err := replayIncrementalBackendPlansWithPublications(
		[]incrementalBackendPlanInstance{planInstance}, owners, rendercontext.NewPlanRegistry(nil),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, outputs["group"]["200-plan"])

	ownedCompetitor := incrementalInstanceResult{
		component: "100-competitor", source: "claims", namespace: "default", name: "owned",
		result: publishedResult(t, publicationValue(t, "hosts", "owned", "competitor")),
	}
	index, err = index.replace(&ownedCompetitor, nil)
	require.NoError(t, err)
	owners, err = incrementalBackendPlanPublicationOwnersForGroups(
		[]string{"group"}, map[string]*incrementalGroupIndex{"group": index},
	)
	require.NoError(t, err)
	outputs, err = replayIncrementalBackendPlansWithPublications(
		[]incrementalBackendPlanInstance{planInstance}, owners, rendercontext.NewPlanRegistry(nil),
	)
	require.NoError(t, err)
	assert.Empty(t, outputs["group"]["200-plan"])
}

func TestIncrementalPublishedValuesRandomizedDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(1872))
	index := newIncrementalGroupIndex()
	instances := make(map[string]incrementalInstanceResult)
	for operation := range 250 {
		id := incrementalGroupInstanceID{
			component: []string{"100-a", "200-b", "300-c"}[random.Intn(3)],
			source:    []string{"claims", "routes"}[random.Intn(2)],
			namespace: []string{"default", "other"}[random.Intn(2)],
			name:      string(rune('a' + random.Intn(12))),
		}
		identity := string(incrementalGroupInstanceKey(id))
		var err error
		if random.Intn(4) == 0 {
			index, err = index.remove(id.component, id.source, id.namespace, id.name)
			delete(instances, identity)
		} else {
			value := publicationValue(t, "hosts", string(rune('a'+random.Intn(6))), identity)
			instance := incrementalInstanceResult{
				component: id.component, source: id.source, namespace: id.namespace, name: id.name,
				result: publishedResult(t, value),
			}
			index, err = index.replace(&instance, nil)
			instances[identity] = instance
		}
		require.NoError(t, err, "operation %d", operation)
		winners, err := index.publishedWinners("hosts")
		require.NoError(t, err)
		expected := naivePublishedWinners(instances, "hosts")
		require.Len(t, winners, len(expected))
		for winnerIndex := range expected {
			assert.Equal(t, expected[winnerIndex].instance, winners[winnerIndex].instance)
			assert.Equal(t, expected[winnerIndex].value.Key, winners[winnerIndex].value.Key)
		}
	}
}

func TestIncrementalValuesEarlyReadsDoNotRequestOrReplayGroupEffects(t *testing.T) {
	result := publishedResult(t, publicationValue(t, "hosts", "shared", "value"))
	result.Events = []templating.RenderedEvent{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Example",
		Type: "Warning", Reason: "Published", Message: "value",
	}}
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route", result: result,
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	collector := templating.NewEventCollector()
	state := &incrementalRenderState{
		components: map[string]incrementalComponent{
			"component": {name: "component", group: "group", publishValue: true, recordEvent: true},
		},
		groups: map[string][]incrementalComponent{
			"group": {{name: "component", group: "group", publishValue: true, recordEvent: true}},
		},
	}
	runtime := &incrementalRenderSession{
		state:         state,
		groupIndexes:  map[string]*incrementalGroupIndex{"group": index},
		groupReady:    map[string]bool{"group": true},
		groupChanged:  map[string]bool{},
		newQueries:    map[incremental.QueryKey]struct{}{},
		dirtyQueries:  map[incremental.QueryKey]struct{}{},
		removed:       map[incremental.QueryKey]struct{}{},
		requested:     map[string]bool{},
		calls:         map[string][]incrementalCall{},
		valueAccesses: map[string]int{},
		baseContext:   map[string]any{"recordEventCollector": collector},
	}
	for range 3 {
		values, readErr := runtime.IncrementalValues(context.Background(), "group", "hosts")
		require.NoError(t, readErr)
		require.Len(t, values, 1)
	}
	assert.False(t, runtime.requested["group"])
	assert.Empty(t, collector.Events())
	require.ErrorContains(t, runtime.ValidateIncrementalCalls(), "got 0 calls")

	rootCtx := templating.WithIncrementalScope(context.Background(), "haproxy.cfg")
	_, err = runtime.RenderIncremental(rootCtx, "component")
	require.NoError(t, err)
	assert.True(t, runtime.requested["group"])
	assert.Equal(t, result.Events, collector.Events())
	require.NoError(t, runtime.ValidateIncrementalCalls())
}

func naivePublishedWinners(
	instances map[string]incrementalInstanceResult,
	cell string,
) []incrementalPublishedWinner {
	winners := make(map[string]incrementalPublishedWinner)
	for identity := range instances {
		instance := instances[identity]
		id := incrementalGroupInstanceID{
			component: instance.component, source: instance.source, namespace: instance.namespace, name: instance.name,
		}
		for index := range instance.result.Published {
			value := instance.result.Published[index]
			if value.Cell != cell {
				continue
			}
			candidate := incrementalPublishedWinner{
				instance: id,
				location: incrementalGroupLocationKey(id, uint64(index)),
				value:    value,
			}
			current, exists := winners[value.Key]
			if !exists || slices.Compare(candidate.location, current.location) < 0 {
				winners[value.Key] = candidate
			}
		}
	}
	result := make([]incrementalPublishedWinner, 0, len(winners))
	for key := range winners {
		result = append(result, winners[key])
	}
	slices.SortFunc(result, func(left, right incrementalPublishedWinner) int {
		return slices.Compare(left.location, right.location)
	})
	return result
}
