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
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

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

const derivedOwnerRetirementPlanner = `{{ toJSON(extraContext["ownerBindings"]) }}`

const derivedOwnerRetirementComponent = `{%%
var current = deriveResource(source, item, "metadata.annotations.governed", props | dig_string("", "annotation"))
%%}`

const derivedOwnerRetirementDependent = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
show "derived=" + (current | dig_string("<missing>", "metadata", "annotations", "governed")) + ":" + (current | dig_string("", "spec", "version")) + "\n"
%%}`

const derivedOwnerRetirementPeer = `peer={{ item | dig_string("", "spec", "version") }}
`

func TestRenderServiceDerivedOwnerBindingRetirement(t *testing.T) {
	fixture := newDerivedOwnerRetirementFixture(t)

	assert.Equal(t, "derived=alpha:v1\npeer=v1\n", fixture.renderAndCommitCacheReady(t))
	fixture.assertExecutions(t, 1, 1, 1, 1)
	fixture.assertDerivedAnnotation(t, "alpha")
	assert.Empty(t, fixture.retiredQueries())

	fixture.setOwnerBinding("")
	assert.Equal(t, "derived=<missing>:v1\npeer=v1\n", fixture.renderAndCommitCacheReady(t))
	fixture.assertExecutions(t, 2, 1, 2, 2)
	fixture.assertNoDerivation(t)
	assert.Equal(t, []string{
		fixture.projectionQuery.Opaque(),
		fixture.ownerQuery.Opaque(),
	}, fixture.retiredQueries())

	assert.Equal(t, "derived=<missing>:v1\npeer=v1\n", fixture.renderAndCommitCacheReady(t))
	assert.Equal(t, uint64(2), fixture.counters(fixture.ownerQuery).Executions)
	assert.Zero(t, fixture.counters(fixture.projectionQuery))
	assert.Equal(t, uint64(2), fixture.counters(fixture.dependentQuery).Executions)
	assert.Equal(t, uint64(2), fixture.counters(fixture.peerQuery).Executions)
	_, ownerCached := fixture.service.incremental.graph.Value(fixture.ownerQuery)
	assert.True(t, ownerCached)
	_, projectionCached := fixture.service.incremental.graph.Value(fixture.projectionQuery)
	assert.False(t, projectionCached)
	fixture.assertNoDerivation(t)
	assert.Equal(t, []string{fixture.ownerQuery.Opaque()}, fixture.retiredQueries())

	fixture.setOwnerBinding("beta")
	assert.Equal(t, "derived=beta:v1\npeer=v1\n", fixture.renderAndCommitCacheReady(t))
	assert.Equal(t, uint64(3), fixture.counters(fixture.ownerQuery).Executions)
	assert.Equal(t, uint64(1), fixture.counters(fixture.projectionQuery).Executions)
	assert.Equal(t, uint64(3), fixture.counters(fixture.dependentQuery).Executions)
	assert.Equal(t, uint64(3), fixture.counters(fixture.peerQuery).Executions)
	fixture.assertDerivedAnnotation(t, "beta")
	assert.Empty(t, fixture.retiredQueries())

	require.NoError(t, fixture.routes.Delete("default", "route", []string{"default", "route"}))
	assert.Empty(t, strings.TrimSpace(fixture.renderAndCommitCacheReady(t)))
	fixture.assertNoDerivation(t)
	assert.Equal(t, []string{
		fixture.projectionQuery.Opaque(),
		fixture.ownerQuery.Opaque(),
	}, fixture.retiredQueries())

	assert.Empty(t, strings.TrimSpace(fixture.renderAndCommitCacheReady(t)))
	assert.Equal(t, []string{fixture.ownerQuery.Opaque()}, fixture.retiredQueries())
	assert.Empty(t, strings.TrimSpace(fixture.renderAndCommitCacheReady(t)))
	assert.Empty(t, fixture.retiredQueries())
	fixture.assertRetiredStateEmpty(t)
}

func TestDerivedOwnerBindingTransitionsInvalidateCrossResourceReads(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"ownerBindings": derivedOwnerBindings(""),
		}},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"peers": {
				APIVersion: "example.test/v1", Resources: "peers",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"00-owner": {
				Name: "00-owner", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: derivedOwnerRetirementPlanner,
					Effects:          []config.IncrementalEffect{config.IncrementalEffectDeriveResource},
				},
				Template: derivedOwnerRetirementComponent,
			},
			"10-get": {
				Name: "10-get", Requires: []string{"peers", "routes"},
				Incremental: &config.IncrementalTemplate{Source: "peers"},
				Template: `{%%
var current = resources.routes.GetSingle("default", "route")
show "get=" + (current | dig_string("<missing>", "metadata", "annotations", "governed")) + "\n"
%%}`,
			},
			"20-list": {
				Name: "20-list", Requires: []string{"peers", "routes"},
				Incremental: &config.IncrementalTemplate{Source: "peers"},
				Template: `{%%
var governed = "<missing>"
for _, current := range resources.routes.List() {
  if (current | dig_string("", "metadata", "name")) == "route" {
    governed = current | dig_string("<missing>", "metadata", "annotations", "governed")
  }
}
show "list=" + governed + "\n"
%%}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{
			Template: `{{ render "00-owner" }}{{ render "10-get" }}{{ render "20-list" }}`,
		},
	}
	service := newDerivedStageService(t, cfg, nil)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(derivedOwnerRetirementRoute(), []string{"default", "route"}))
	peers := k8sstore.NewMemoryStore(2)
	require.NoError(t, peers.Add(derivedOwnerRetirementPeerResource(), []string{"default", "peer"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes, "peers": peers})
	render := func() string {
		result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		waitForIncrementalCache(t, service)
		return result.HAProxyConfig
	}
	tempComponent11 := service.incremental.components["10-get"]
	getQuery := componentQueryKey(&tempComponent11, "peers", "default", "peer")
	tempComponent12 := service.incremental.components["20-list"]
	listQuery := componentQueryKey(&tempComponent12, "peers", "default", "peer")

	assert.Equal(t, "get=<missing>\nlist=<missing>\n", render())
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(getQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(listQuery).Executions)

	cfg.TemplatingSettings.ExtraContext["ownerBindings"] = derivedOwnerBindings("beta")
	assert.Equal(t, "get=beta\nlist=beta\n", render())
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(getQuery).Executions)
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(listQuery).Executions)

	assert.Equal(t, "get=beta\nlist=beta\n", render())
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(getQuery).Executions)
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(listQuery).Executions)

	cfg.TemplatingSettings.ExtraContext["ownerBindings"] = derivedOwnerBindings("")
	assert.Equal(t, "get=<missing>\nlist=<missing>\n", render())
	assert.Equal(t, uint64(3), service.incremental.graph.Counters(getQuery).Executions)
	assert.Equal(t, uint64(3), service.incremental.graph.Counters(listQuery).Executions)
}

type derivedOwnerRetirementFixture struct {
	config          *config.Config
	service         *RenderService
	routes          *k8sstore.MemoryStore
	provider        stores.StoreProvider
	ownerQuery      incremental.QueryKey
	projectionQuery incremental.QueryKey
	dependentQuery  incremental.QueryKey
	peerQuery       incremental.QueryKey
}

func newDerivedOwnerRetirementFixture(t *testing.T) *derivedOwnerRetirementFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"ownerBindings": derivedOwnerBindings("alpha"),
		}},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"00-owner": {
				Name:     "00-owner",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: derivedOwnerRetirementPlanner,
					Effects:          []config.IncrementalEffect{config.IncrementalEffectDeriveResource},
				},
				Template: derivedOwnerRetirementComponent,
			},
			"10-dependent": {
				Name:     "10-dependent",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes",
				},
				Template: derivedOwnerRetirementDependent,
			},
			"20-peer": {
				Name:     "20-peer",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes",
				},
				Template: derivedOwnerRetirementPeer,
			},
		},
		HAProxyConfig: config.HAProxyConfig{
			Template: `{{ render "00-owner" }}{{ render "10-dependent" }}{{ render "20-peer" }}`,
		},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(derivedOwnerRetirementRoute(), []string{"default", "route"}))
	tempComponent13 := service.incremental.components["00-owner"]
	tempComponent14 := service.incremental.components["10-dependent"]
	tempComponent15 := service.incremental.components["20-peer"]
	fixture := &derivedOwnerRetirementFixture{
		config:   cfg,
		service:  service,
		routes:   routes,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	}
	fixture.ownerQuery = componentQueryKey(&tempComponent13, "routes", "default", "route")
	fixture.projectionQuery = derivedProjectionQueryKey("routes", "default", "route")
	fixture.dependentQuery = componentQueryKey(&tempComponent14, "routes", "default", "route")
	fixture.peerQuery = componentQueryKey(&tempComponent15, "routes", "default", "route")
	return fixture
}

func derivedOwnerBindings(annotation string) map[string]any {
	if annotation == "" {
		return map[string]any{}
	}
	return map[string]any{
		"routes": map[string]any{"annotation": annotation},
	}
}

func derivedOwnerRetirementRoute() map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Example",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        "route",
			"annotations": map[string]any{},
		},
		"spec": map[string]any{"version": "v1"},
	}
}

func derivedOwnerRetirementPeerResource() map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Peer",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      "peer",
		},
	}
}

func (f *derivedOwnerRetirementFixture) setOwnerBinding(annotation string) {
	f.config.TemplatingSettings.ExtraContext["ownerBindings"] = derivedOwnerBindings(annotation)
}

func (f *derivedOwnerRetirementFixture) renderAndCommitCacheReady(t *testing.T) string {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result.HAProxyConfig
}

func (f *derivedOwnerRetirementFixture) counters(key incremental.QueryKey) incremental.NodeCounters {
	return f.service.incremental.graph.Counters(key)
}

func (f *derivedOwnerRetirementFixture) assertExecutions(
	t *testing.T,
	owner, projection, dependent, peer uint64,
) {
	t.Helper()
	assert.Equal(t, owner, f.counters(f.ownerQuery).Executions)
	assert.Equal(t, projection, f.counters(f.projectionQuery).Executions)
	assert.Equal(t, dependent, f.counters(f.dependentQuery).Executions)
	assert.Equal(t, peer, f.counters(f.peerQuery).Executions)
}

func (f *derivedOwnerRetirementFixture) assertNoDerivation(t *testing.T) {
	t.Helper()
	f.service.incremental.mu.Lock()
	defer f.service.incremental.mu.Unlock()
	_, found := f.service.incremental.snapshot.derived.Get(derivedKey(rendercontext.DerivedResourceIdentity{
		Resource: "routes", Namespace: "default", Name: "route",
	}))
	assert.False(t, found)
}

func (f *derivedOwnerRetirementFixture) assertDerivedAnnotation(t *testing.T, annotation string) {
	t.Helper()
	f.service.incremental.mu.Lock()
	defer f.service.incremental.mu.Unlock()
	entry, found := f.service.incremental.snapshot.derived.Get(derivedKey(rendercontext.DerivedResourceIdentity{
		Resource: "routes", Namespace: "default", Name: "route",
	}))
	require.True(t, found)
	decoded, err := decodeResourceValue([]byte(entry.Value))
	require.NoError(t, err)
	resource, ok := decoded.(map[string]any)
	require.True(t, ok)
	metadata, ok := resource["metadata"].(map[string]any)
	require.True(t, ok)
	annotations, ok := metadata["annotations"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, annotation, annotations["governed"])
}

func (f *derivedOwnerRetirementFixture) retiredQueries() []string {
	f.service.incremental.mu.Lock()
	defer f.service.incremental.mu.Unlock()
	result := make([]string, 0, f.service.incremental.snapshot.retired.Len())
	f.service.incremental.snapshot.retired.Root().Walk(func(key []byte, _ struct{}) bool {
		result = append(result, string(key))
		return false
	})
	slices.Sort(result)
	return result
}

func (f *derivedOwnerRetirementFixture) assertRetiredStateEmpty(t *testing.T) {
	t.Helper()
	f.service.incremental.mu.Lock()
	defer f.service.incremental.mu.Unlock()
	snapshot := f.service.incremental.snapshot
	assert.Zero(t, snapshot.retired.Len())
	assert.Zero(t, snapshot.results.Len())
	assert.Zero(t, snapshot.derived.Len())
	assert.Zero(t, snapshot.members.Len())
	for _, key := range []incremental.QueryKey{
		f.ownerQuery,
		f.projectionQuery,
		f.dependentQuery,
		f.peerQuery,
	} {
		_, cached := f.service.incremental.graph.Value(key)
		assert.False(t, cached)
		assert.Zero(t, f.service.incremental.graph.Counters(key))
	}
}
