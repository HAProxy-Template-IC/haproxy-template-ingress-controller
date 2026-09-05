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
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestRenderServiceIncrementalRetainsObservedResourceWithoutRequires(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)

	assert.Equal(t, "route=v1\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.executions())
	assert.Equal(t, "route=v1\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.executions())

	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	assert.Equal(t, "route=v2\n", fixture.render(t))
	assert.Equal(t, uint64(2), fixture.executions())

	require.NoError(t, fixture.services.Add(
		incrementalTestResource("default", "unrelated", map[string]any{"value": "ignored"}),
		[]string{"default", "unrelated"},
	))
	assert.Equal(t, "route=v2\n", fixture.render(t))
	assert.Equal(t, uint64(2), fixture.executions())
}

func TestRenderServiceIncrementalTracksControllerResourceDependencies(t *testing.T) {
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
				Name:        "routes",
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{{ item | dig_string("", "metadata", "name") }}={{ len(controller.haproxy_pods.List()) }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	pods := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "route", nil),
		[]string{"default", "route"},
	))
	require.NoError(t, pods.Add(
		incrementalTestResource("default", "haproxy-0", nil),
		[]string{"default", "haproxy-0"},
	))
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), HAProxyPodStore: pods,
	})
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
	tempComponent1 := service.incremental.components["routes"]
	query := componentQueryKey(&tempComponent1, "routes", "default", "route")

	assert.Equal(t, "route=1\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(query).Executions)
	assert.Equal(t, "route=1\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(query).Executions)

	require.NoError(t, pods.Add(
		incrementalTestResource("default", "haproxy-1", nil),
		[]string{"default", "haproxy-1"},
	))
	assert.Equal(t, "route=2\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(query).Executions)

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, pods.Add(
		incrementalTestResource("default", "haproxy-2", nil),
		[]string{"default", "haproxy-2"},
	))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "route=3\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, uint64(3), service.incremental.graph.Counters(query).Executions)
}

func TestRenderServiceIncrementalIgnoresUnreadControllerResourceChanges(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	pods := k8sstore.NewMemoryStore(2)
	fixture.service.haproxyPodStore = pods
	assert.Equal(t, "route=v1\n", fixture.render(t))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, pods.Add(
		incrementalTestResource("default", "haproxy-0", nil),
		[]string{"default", "haproxy-0"},
	))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, uint64(1), fixture.executions())
}

func TestRenderServiceIncrementalUsesProductionStoreAdapters(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"routes":   &stores.TypesStoreAdapter{Inner: fixture.routes},
		"services": &stores.TypesStoreAdapter{Inner: fixture.services},
	})

	assert.Equal(t, "route=v1\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.executions())
	assert.Equal(t, "route=v1\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.executions())

	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	assert.Equal(t, "route=v2\n", fixture.render(t))
	assert.Equal(t, uint64(2), fixture.executions())
}

func TestRenderServiceIncrementalObservedResourceBackdatesABAWithoutRequires(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)

	assert.Equal(t, "route=v1\n", fixture.render(t))
	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v1"}),
		[]string{"default", "service"},
	))

	assert.Equal(t, "route=v1\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.executions())
}

func TestRenderServiceIncrementalCommitVerifiesExactAmbientScope(t *testing.T) {
	t.Run("unrelated mutation", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))
		generation := fixture.service.incremental.graph.Generation()

		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, fixture.services.Add(
			incrementalTestResource("default", "unrelated", map[string]any{"value": "ignored"}),
			[]string{"default", "unrelated"},
		))
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		assert.Equal(t, generation+1, fixture.service.incremental.graph.Generation())
		assert.Equal(t, uint64(1), fixture.executions())
		assert.Equal(t, "route=v1\n", fixture.render(t))
		assert.Equal(t, uint64(1), fixture.executions())
	})

	// A commit that accepts no external content publishes at the cursor it
	// pinned; the journal carries a mid-render change to the next render.
	t.Run("relevant mutation", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))
		generation := fixture.service.incremental.graph.Generation()
		base := fixture.service.incremental.snapshot

		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
		require.True(t, ok)
		key := resourceInputKey(&resourceInputSpec{
			resourceType: "services",
			scope:        resourceInputGet,
			keys:         []string{"default", "service"},
		})
		expected, exists, err := transaction.incremental.graphSession.ExactInput(key)
		require.NoError(t, err)
		require.True(t, exists)
		require.NoError(t, fixture.services.Update(
			incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
			[]string{"default", "service"},
		))
		verified, err := transaction.incremental.verifyResources(t.Context(), []incremental.InputRevision{{
			Key: key, Revision: expected.Revision, Found: expected.Found,
		}})
		require.NoError(t, err)
		require.True(t, verified)
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		assert.Equal(t, generation+1, fixture.service.incremental.graph.Generation())
		assert.NotSame(t, base, fixture.service.incremental.snapshot)
		assert.Equal(t, "route=v2\n", fixture.render(t))
		assert.Equal(t, uint64(2), fixture.executions())
	})

	t.Run("semantic ABA", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))

		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, fixture.services.Update(
			incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
			[]string{"default", "service"},
		))
		require.NoError(t, fixture.services.Update(
			incrementalTestResource("default", "service", map[string]any{"value": "v1"}),
			[]string{"default", "service"},
		))
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		assert.Equal(t, uint64(1), fixture.executions())
		assert.Equal(t, "route=v1\n", fixture.render(t))
		assert.Equal(t, uint64(1), fixture.executions())
	})
}

func TestRenderServiceIncrementalCommitVerifiesSourceSemantics(t *testing.T) {
	t.Run("relevant mutation", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))
		generation := fixture.service.incremental.graph.Generation()

		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, fixture.routes.Update(
			incrementalTestResource("default", "route", map[string]any{"backend": ""}),
			[]string{"default", "route"},
		))
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		assert.Equal(t, generation+1, fixture.service.incremental.graph.Generation())
		assert.Equal(t, "route=<none>\n", fixture.render(t))
		assert.Equal(t, uint64(2), fixture.executions())
	})

	t.Run("semantic ABA", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))

		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, fixture.routes.Update(
			incrementalTestResource("default", "route", map[string]any{"backend": ""}),
			[]string{"default", "route"},
		))
		require.NoError(t, fixture.routes.Update(
			incrementalTestResource("default", "route", map[string]any{"backend": "service"}),
			[]string{"default", "route"},
		))
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		assert.Equal(t, uint64(1), fixture.executions())
		assert.Equal(t, "route=v1\n", fixture.render(t))
		assert.Equal(t, uint64(1), fixture.executions())
	})
}

func TestRenderServiceIncrementalConcurrentTransactionsVerifyBeforeDiscardingCache(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))
		generation := fixture.service.incremental.graph.Generation()

		first, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		second, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, first.InputTransaction.Commit(t.Context()))
		require.NoError(t, second.InputTransaction.Commit(t.Context()))
		assert.Equal(t, generation+1, fixture.service.incremental.graph.Generation())
		assert.Equal(t, uint64(1), fixture.executions())
	})

	t.Run("relevant late mutation", func(t *testing.T) {
		fixture := newAmbientResourceFixture(t, false)
		assert.Equal(t, "route=v1\n", fixture.render(t))

		first, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		second, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, first.InputTransaction.Commit(t.Context()))
		require.NoError(t, fixture.services.Update(
			incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
			[]string{"default", "service"},
		))
		require.NoError(t, second.InputTransaction.Commit(t.Context()))
		assert.Equal(t, "route=v2\n", fixture.render(t))
	})
}

func TestRenderServiceIncrementalObservedResourceRecoversFromJournalLoss(t *testing.T) {
	fixture := newAmbientResourceFixture(t, true)

	assert.Equal(t, "route=v1\n", fixture.render(t))
	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	fixture.journal.incomplete = true

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	require.True(t, transaction.incremental.cold)
	assert.Equal(t, "route=v2\n", result.HAProxyConfig)
	fixture.journal.incomplete = false
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, fixture.service)
	assert.Equal(t, uint64(2), fixture.executions())
}

func TestRenderServiceIncrementalRetiresUnreferencedResourceCursor(t *testing.T) {
	fixture := newAmbientResourceFixture(t, true)

	assert.Equal(t, "route=v1\n", fixture.render(t))
	_, tracked := fixture.service.incremental.snapshot.cursors["services"]
	require.True(t, tracked)
	require.Equal(t, 1, ambientServicesCatalogEntries(fixture.service.incremental.snapshot))
	require.NoError(t, fixture.routes.Update(
		incrementalTestResource("default", "route", map[string]any{"backend": ""}),
		[]string{"default", "route"},
	))
	aborted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "route=<none>\n", aborted.HAProxyConfig)
	aborted.InputTransaction.Abort()
	_, tracked = fixture.service.incremental.snapshot.cursors["services"]
	require.True(t, tracked)
	require.Equal(t, 1, ambientServicesCatalogEntries(fixture.service.incremental.snapshot))

	assert.Equal(t, "route=<none>\n", fixture.render(t))
	assert.Equal(t, uint64(2), fixture.executions())
	_, tracked = fixture.service.incremental.snapshot.cursors["services"]
	require.False(t, tracked)
	require.Zero(t, ambientServicesCatalogEntries(fixture.service.incremental.snapshot))

	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	fixture.journal.incomplete = true
	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	assert.False(t, transaction.incremental.cold)
	assert.Equal(t, "route=<none>\n", result.HAProxyConfig)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, uint64(2), fixture.executions())
	_, tracked = fixture.service.incremental.snapshot.cursors["services"]
	assert.False(t, tracked)
	assert.Zero(t, ambientServicesCatalogEntries(fixture.service.incremental.snapshot))
}

func TestRenderServiceIncrementalReloadsObservedAliasWhenBindingActivates(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]any{"bindings": map[string]any{"alpha": map[string]any{}}},
		},
		WatchedResources: map[string]config.WatchedResource{
			"alpha": {
				APIVersion: "example.test/v1", Resources: "alphas",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"beta": {
				APIVersion: "example.test/v1", Resources: "betas",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"dynamic": {
				Name: "dynamic",
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: `{{ toJSON(extraContext["bindings"]) }}`,
				},
				Template: `{{ source }}/{{ item | dig_string("", "metadata", "name") }}={{ resources.beta.GetSingle("default", "ambient") | dig_string("", "spec", "value") }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "dynamic" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	alpha := k8sstore.NewMemoryStore(2)
	beta := k8sstore.NewMemoryStore(2)
	require.NoError(t, alpha.Add(incrementalTestResource("default", "a", nil), []string{"default", "a"}))
	require.NoError(t, beta.Add(
		incrementalTestResource("default", "ambient", map[string]any{"value": "v1"}),
		[]string{"default", "ambient"},
	))
	require.NoError(t, beta.Add(incrementalTestResource("default", "b", nil), []string{"default", "b"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"alpha": alpha, "beta": beta})

	assert.Equal(t, "alpha/a=v1\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	cfg.TemplatingSettings.ExtraContext["bindings"] = map[string]any{
		"alpha": map[string]any{},
		"beta":  map[string]any{},
	}
	assert.Equal(t,
		"alpha/a=v1\nbeta/ambient=v1\nbeta/b=v1\n",
		renderAndCommitIncrementalCacheReady(t, service, provider),
	)
	tempComponent2 := service.incremental.components["dynamic"]
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(
		componentQueryKey(&tempComponent2, "beta", "default", "ambient"),
	).Executions)
	tempComponent3 := service.incremental.components["dynamic"]
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(
		componentQueryKey(&tempComponent3, "beta", "default", "b"),
	).Executions)
}

func ambientServicesCatalogEntries(snapshot *incrementalStateSnapshot) int {
	count := 0
	snapshot.catalog.Root().Walk(func(key []byte, _ struct{}) bool {
		spec, valid := parseResourceInputKey(incremental.NewInputKey(string(key)))
		if valid && spec.resourceType == "services" {
			count++
		}
		return false
	})
	return count
}

func BenchmarkIncrementalNoChangeSessionAmbientCatalog(b *testing.B) {
	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprintf("%d-inputs", count), func(b *testing.B) {
			catalog := iradix.New[struct{}]().Txn()
			for index := range count {
				spec := resourceInputSpec{
					resourceType: "services",
					scope:        resourceInputGet,
					keys:         []string{fmt.Sprintf("input-%08d", index)},
				}
				catalog.Insert([]byte(resourceInputKey(&spec).Opaque()), struct{}{})
			}
			session := &incrementalRenderSession{
				bindingPlan: &incrementalBindingPlan{bySource: map[string][]incrementalComponent{}},
				cursors:     map[string]incrementalStoreCursor{"services": {}},
				members:     iradix.New[struct{}]().Txn(),
			}
			session.resetCatalog(newIncrementalResourceCatalogSnapshot(catalog.Commit()))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				session.pruneInactiveMembers()
			}
		})
	}
}

type ambientResourceFixture struct {
	service  *RenderService
	provider stores.StoreProvider
	routes   *k8sstore.MemoryStore
	services *k8sstore.MemoryStore
	journal  *incompleteIncrementalJournal
	query    incremental.QueryKey
}

func newAmbientResourceFixture(t *testing.T, incompleteJournal bool) *ambientResourceFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
			"services": {
				APIVersion: "example.test/v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name:        "routes",
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var backend = item | dig_string("", "spec", "backend")
if backend == "" {
  show name + "=<none>\n"
} else {
  var value = resources.services.GetSingle(namespace, backend) | dig_string("<missing>", "spec", "value")
  show name + "=" + value + "\n"
}
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
	routes := k8sstore.NewMemoryStore(2)
	services := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "route", map[string]any{"backend": "service"}),
		[]string{"default", "route"},
	))
	require.NoError(t, services.Add(
		incrementalTestResource("default", "service", map[string]any{"value": "v1"}),
		[]string{"default", "service"},
	))
	serviceStore := stores.Store(services)
	var journal *incompleteIncrementalJournal
	if incompleteJournal {
		journal = &incompleteIncrementalJournal{MemoryStore: services}
		serviceStore = journal
	}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes, "services": serviceStore,
	})
	tempComponent4 := service.incremental.components["routes"]
	query := componentQueryKey(&tempComponent4, "routes", "default", "route")
	return &ambientResourceFixture{
		service: service, provider: provider, routes: routes, services: services, journal: journal, query: query,
	}
}

func (f *ambientResourceFixture) render(t *testing.T) string {
	t.Helper()
	return renderAndCommitIncrementalCacheReady(t, f.service, f.provider)
}

func (f *ambientResourceFixture) executions() uint64 {
	return f.service.incremental.graph.Counters(f.query).Executions
}
