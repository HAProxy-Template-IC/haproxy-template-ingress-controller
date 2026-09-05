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
	"log/slog"
	"reflect"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestRenderServicePersistentGroupIndexLifecycle(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name: "routes", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{%%
show shared.Unique(
  "routes",
  "winner",
  dig_string(item, "", "metadata", "name") + "=" + dig_string(item, "", "spec", "value") + "\n",
)
%%}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := &incompleteIncrementalJournal{MemoryStore: k8sstore.NewMemoryStore(2)}
	require.NoError(t, routes.Add(incrementalTestResource("default", "a", map[string]any{"value": "first"}), []string{"default", "a"}))
	require.NoError(t, routes.Add(incrementalTestResource("default", "b", map[string]any{"value": "backup"}), []string{"default", "b"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})

	assert.Equal(t, "a=first\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, "a=first\n", mustIncrementalGroupOutput(t, service.incremental.snapshot.groupIndexes["routes"], "routes"))
	firstOutput := mustIncrementalGroupOutputContent(
		t, service.incremental.snapshot.groupIndexes["routes"], "routes",
	)
	assert.Equal(t, "a=first\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assertSameOutputRoot(t, firstOutput, mustIncrementalGroupOutputContent(
		t, service.incremental.snapshot.groupIndexes["routes"], "routes",
	))

	require.NoError(t, routes.Update(incrementalTestResource("default", "a", map[string]any{"value": "second"}), []string{"default", "a"}))
	assert.Equal(t, "a=second\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	secondOutput := mustIncrementalGroupOutputContent(
		t, service.incremental.snapshot.groupIndexes["routes"], "routes",
	)
	assertDifferentOutputRoot(t, firstOutput, secondOutput)

	require.NoError(t, routes.Update(incrementalTestResource("default", "a", map[string]any{"value": "aborted"}), []string{"default", "a"}))
	aborted, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "a=aborted\n", aborted.HAProxyConfig)
	aborted.InputTransaction.Abort()
	assertSameOutputRoot(t, secondOutput, mustIncrementalGroupOutputContent(
		t, service.incremental.snapshot.groupIndexes["routes"], "routes",
	))
	assert.Equal(t, "a=second\n", mustIncrementalGroupOutput(t, service.incremental.snapshot.groupIndexes["routes"], "routes"))
	assert.Equal(t, "a=aborted\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assertDifferentOutputRoot(t, secondOutput, mustIncrementalGroupOutputContent(
		t, service.incremental.snapshot.groupIndexes["routes"], "routes",
	))

	require.NoError(t, routes.Delete("default", "a", []string{"default", "a"}))
	assert.Equal(t, "b=backup\n", renderAndCommitIncrementalCacheReady(t, service, provider))

	require.NoError(t, routes.Update(
		incrementalTestResource("default", "b", map[string]any{"value": "cold"}),
		[]string{"default", "b"},
	))
	routes.incomplete = true
	cold, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction := cold.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, transaction.incremental.cold)
	assert.Equal(t, "b=cold\n", cold.HAProxyConfig)
	routes.incomplete = false
	require.NoError(t, cold.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	assert.Equal(t, "b=cold\n", mustIncrementalGroupOutput(t, service.incremental.snapshot.groupIndexes["routes"], "routes"))
}

type incompleteIncrementalJournal struct {
	*k8sstore.MemoryStore
	incomplete bool
}

func (s *incompleteIncrementalJournal) ChangesSince(
	sequence uint64,
) (uint64, []stores.RevisionChange, bool) {
	current, changes, complete := s.MemoryStore.ChangesSince(sequence)
	if s.incomplete {
		return current, nil, false
	}
	return current, changes, complete
}

func TestIncrementalGroupIndexTracksHTTPOnlyEffectChange(t *testing.T) {
	component := incrementalComponent{name: "routes", group: "group"}
	result := incrementalComponentResult{Text: "unchanged\n"}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	oldEffect := incrementalHTTPEffect{
		inputID:  1,
		snapshot: httpstore.ContentSnapshot{URL: "https://old.test", Content: "same", Found: true},
	}
	newEffect := incrementalHTTPEffect{
		inputID:  2,
		snapshot: httpstore.ContentSnapshot{URL: "https://new.test", Content: "same", Found: true},
	}
	instance := incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "route", result: result,
	}
	index, err := newIncrementalGroupIndex().replace(&instance, []incrementalHTTPEffect{oldEffect})
	require.NoError(t, err)
	beforeOutput := mustIncrementalGroupOutputContent(t, index, component.name)
	key := resultKey(&component, "routes", "default", "route")
	query := componentQueryKey(&component, "routes", "default", "route")
	graph, roots := testExactRoots(t, map[incremental.QueryKey]string{query: string(encoded)})
	graphSession, err := graph.Begin()
	require.NoError(t, err)
	t.Cleanup(graphSession.Abort)
	root := roots[query]
	results := iradix.New[incremental.ExactValueRoot]().Txn()
	results.Insert(key, root)
	httpEffects := iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn()
	httpEffects.Insert(key, mustIndexedHTTPEffects(t, oldEffect))
	session := &incrementalRenderSession{
		state: &incrementalRenderState{
			components: map[string]incrementalComponent{component.name: component},
			graph:      graph,
		},
		graphSession:  graphSession,
		results:       results,
		retired:       iradix.New[struct{}]().Txn(),
		derived:       iradix.New[incrementalDerivedResource]().Txn(),
		httpEffects:   httpEffects,
		groupIndexes:  map[string]*incrementalGroupIndex{"group": index},
		groupChanged:  map[string]bool{},
		httpExecuted:  map[incremental.QueryKey][]incrementalHTTPEffect{query: {newEffect}},
		httpRefDeltas: map[uint64]httpRefDelta{},
		newQueries:    map[incremental.QueryKey]struct{}{},
		dirtyQueries:  map[incremental.QueryKey]struct{}{},
	}

	require.NoError(t, session.applyEvaluatedResult("group", &incremental.ExactResult{Key: query, Value: root}))
	assert.True(t, session.groupChanged["group"])
	assertSameOutputRoot(t, beforeOutput, mustIncrementalGroupOutputContent(t, session.groupIndexes["group"], component.name))
	assert.Equal(t, "unchanged\n", mustIncrementalGroupOutput(t, session.groupIndexes["group"], "routes"))
	assert.Equal(t, []incrementalHTTPEffect{newEffect}, mustIncrementalGroupHTTP(t, session.groupIndexes["group"]))
	assert.Equal(t, map[uint64]httpRefDelta{1: {removed: 1}, 2: {added: 1}}, session.httpRefDeltas)
}

func TestIncrementalSessionRejectsGroupIndexResultMismatch(t *testing.T) {
	component := incrementalComponent{name: "routes", group: "group"}
	result := incrementalComponentResult{Text: "route\n"}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	key := resultKey(&component, "routes", "default", "route")
	instance := incrementalInstanceResult{
		component: component.name,
		source:    "routes",
		namespace: "default",
		name:      "route",
		result:    result,
	}
	indexed, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	query := componentQueryKey(&component, "routes", "default", "route")
	graph, roots := testExactRoots(t, map[incremental.QueryKey]string{query: string(encoded)})

	tests := map[string]struct {
		results *iradix.Tree[incremental.ExactValueRoot]
		index   *incrementalGroupIndex
	}{
		"missing index instance": {
			results: func() *iradix.Tree[incremental.ExactValueRoot] {
				txn := iradix.New[incremental.ExactValueRoot]().Txn()
				txn.Insert(key, roots[query])
				return txn.Commit()
			}(),
			index: newIncrementalGroupIndex(),
		},
		"unexpected index instance": {
			results: iradix.New[incremental.ExactValueRoot](),
			index:   indexed,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			session := &incrementalRenderSession{
				state:         &incrementalRenderState{graph: graph},
				results:       test.results.Txn(),
				derived:       iradix.New[incrementalDerivedResource]().Txn(),
				httpEffects:   iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn(),
				groupIndexes:  map[string]*incrementalGroupIndex{"group": test.index},
				groupChanged:  map[string]bool{},
				httpRefDeltas: map[uint64]httpRefDelta{},
			}

			err := session.deleteResult(&component, "routes", "default", "route")
			require.ErrorContains(t, err, "assembly index does not match its result cache")
		})
	}
}

func TestIncrementalGroupIndexDropsRetiredHTTPOnlyEffect(t *testing.T) {
	component := incrementalComponent{name: "routes", group: "group"}
	result := incrementalComponentResult{Text: "retained\n"}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	empty, err := json.Marshal(incrementalComponentResult{})
	require.NoError(t, err)
	effect := incrementalHTTPEffect{
		inputID:  1,
		snapshot: httpstore.ContentSnapshot{URL: "https://old.test", Content: "old", Found: true},
	}
	instance := incrementalInstanceResult{
		component: component.name, source: "routes", namespace: "default", name: "route", result: result,
	}
	index, err := newIncrementalGroupIndex().replace(&instance, []incrementalHTTPEffect{effect})
	require.NoError(t, err)
	beforeOutput := mustIncrementalGroupOutputContent(t, index, component.name)
	key := resultKey(&component, "routes", "default", "route")
	query := componentQueryKey(&component, "routes", "default", "route")
	graph, roots := testExactRootVariants(t, query, string(encoded), string(empty))
	results := iradix.New[incremental.ExactValueRoot]().Txn()
	results.Insert(key, roots[0])
	httpEffects := iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn()
	httpEffects.Insert(key, mustIndexedHTTPEffects(t, effect))
	retired := iradix.New[struct{}]().Txn()
	retired.Insert([]byte(query.Opaque()), struct{}{})
	session := &incrementalRenderSession{
		state: &incrementalRenderState{
			components: map[string]incrementalComponent{component.name: component},
			graph:      graph,
		},
		results:       results,
		retired:       retired,
		derived:       iradix.New[incrementalDerivedResource]().Txn(),
		httpEffects:   httpEffects,
		groupIndexes:  map[string]*incrementalGroupIndex{"group": index},
		groupChanged:  map[string]bool{},
		httpExecuted:  map[incremental.QueryKey][]incrementalHTTPEffect{query: nil},
		httpRefDeltas: map[uint64]httpRefDelta{},
		newQueries:    map[incremental.QueryKey]struct{}{},
		dirtyQueries:  map[incremental.QueryKey]struct{}{},
	}

	require.NoError(t, session.applyEvaluatedResult("group", &incremental.ExactResult{Key: query, Value: roots[1]}))
	assert.True(t, session.groupChanged["group"])
	assertSameOutputRoot(t, beforeOutput, mustIncrementalGroupOutputContent(t, session.groupIndexes["group"], component.name))
	assert.Equal(t, "retained\n", mustIncrementalGroupOutput(t, session.groupIndexes["group"], "routes"))
	assert.Empty(t, mustIncrementalGroupHTTP(t, session.groupIndexes["group"]))
	assert.Equal(t, map[uint64]httpRefDelta{1: {removed: 1}}, session.httpRefDeltas)
}
