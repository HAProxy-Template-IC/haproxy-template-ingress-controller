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
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const backendPlanTestComponent = `{%%
var value = item | dig_string("", "spec", "value")
var profile, _ = planRegistry.Profile(map[string]any{"mode": "http"})
var text = "backend be_shared from " + profile + "\n    # " + value + "\n"
var token, _ = planRegistry.Backend(map[string]any{
  "name": "be_shared",
  "profile": profile,
  "mode": "http",
  "guid": value,
}, text)
show token
%%}`

type backendPlanServiceFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	routes   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newBackendPlanServiceFixture(t *testing.T) *backendPlanServiceFixture {
	t.Helper()
	cfg := backendPlanServiceConfig()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "a", map[string]any{"value": "alpha"}),
		[]string{"default", "a"},
	))
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"value": "beta"}),
		[]string{"default", "b"},
	))
	return &backendPlanServiceFixture{
		config:   cfg,
		service:  service,
		engine:   engine,
		routes:   routes,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	}
}

func backendPlanServiceConfig() *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"backends": {
				Name:     "backends",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source:  "routes",
					Effects: []config.IncrementalEffect{config.IncrementalEffectBackendPlan},
				},
				Template: backendPlanTestComponent,
			},
		},
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n" +
				"{{ planRegistry.ProfileGroup() }}" +
				"{{ render \"backends\" }}",
		},
	}
}

func (f *backendPlanServiceFixture) render(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	return result
}

func (f *backendPlanServiceFixture) renderAndCommitCacheReady(t *testing.T) *RenderResult {
	t.Helper()
	result := f.render(t)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func TestRenderServiceIncrementalBackendPlanLifecycle(t *testing.T) {
	fixture := newBackendPlanServiceFixture(t)
	first := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, first.HAProxyConfig, "# alpha")
	assert.NotContains(t, first.HAProxyConfig, "# beta")
	assert.Equal(t, map[string]int{"routes/a": 1, "routes/b": 1}, fixture.engine.executionCounts())

	unchanged := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, first.HAProxyConfig, unchanged.HAProxyConfig)
	assert.Equal(t, map[string]int{"routes/a": 1, "routes/b": 1}, fixture.engine.executionCounts())

	require.NoError(t, fixture.routes.Update(
		incrementalTestResource("default", "b", map[string]any{"value": "beta-2"}),
		[]string{"default", "b"},
	))
	loserChanged := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, first.HAProxyConfig, loserChanged.HAProxyConfig)
	assert.Equal(t, map[string]int{"routes/a": 1, "routes/b": 2}, fixture.engine.executionCounts())

	require.NoError(t, fixture.routes.Update(
		incrementalTestResource("default", "a", map[string]any{"value": "alpha-aborted"}),
		[]string{"default", "a"},
	))
	committedSnapshot := fixture.service.incremental.snapshot
	aborted := fixture.render(t)
	assert.Contains(t, aborted.HAProxyConfig, "# alpha-aborted")
	aborted.InputTransaction.Abort()
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)

	retried := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, retried.HAProxyConfig, "# alpha-aborted")
	assert.Equal(t, 3, fixture.engine.executionCounts()["routes/a"])

	require.NoError(t, fixture.routes.Delete("default", "a", []string{"default", "a"}))
	promoted := fixture.renderAndCommitCacheReady(t)
	assert.Contains(t, promoted.HAProxyConfig, "# beta-2")
	assert.NotContains(t, promoted.HAProxyConfig, "# alpha")
	assert.Equal(t, 2, fixture.engine.executionCounts()["routes/b"])
}

func TestRenderServiceIncrementalBackendPlanConcurrentSessions(t *testing.T) {
	fixture := newBackendPlanServiceFixture(t)
	baseline := fixture.renderAndCommitCacheReady(t)

	results := make([]*RenderResult, 2)
	errors := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errors[index] = fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
		}()
	}
	wait.Wait()
	for index := range results {
		require.NoError(t, errors[index])
		require.Equal(t, baseline.HAProxyConfig, results[index].HAProxyConfig)
		require.NoError(t, results[index].InputTransaction.Commit(t.Context()))
	}
	assert.Equal(t, map[string]int{"routes/a": 1, "routes/b": 1}, fixture.engine.executionCounts())
}

func TestRenderServiceIncrementalBackendPlanStaticColdReplay(t *testing.T) {
	cfg := backendPlanServiceConfig()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"value": "beta"}),
		[]string{"default", "b"},
	))
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "a", map[string]any{"value": "alpha"}),
		[]string{"default", "a"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})

	_, output, err := renderStaticColdIncremental(t, cfg, engine, provider)
	require.NoError(t, err)
	assert.Contains(t, output, "# alpha")
	assert.NotContains(t, output, "# beta")
	assert.NotContains(t, output, "@haptic:")
}

func TestRenderServiceIncrementalBackendPlanRejectsCorruptedCache(t *testing.T) {
	tests := map[string]func(*incrementalComponentResult){
		"policy": func(result *incrementalComponentResult) {
			result.BackendPlan[1].Policy = "last"
		},
		"owner": func(result *incrementalComponentResult) {
			result.BackendPlan[0].Owners = []uint32{99}
		},
		"owner removed": func(result *incrementalComponentResult) {
			result.BackendPlan[0].Owners = nil
		},
		"output reference": func(result *incrementalComponentResult) {
			invalid := uint32(99)
			result.BackendPlanOutput[0].BackendCall = &invalid
		},
		"nested declaration": func(result *incrementalComponentResult) {
			result.BackendPlan[1].Backend.Backend.Mode = "tcp"
		},
		"effect digest": func(result *incrementalComponentResult) {
			result.BackendPlanDigest = "0000000000000000"
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newBackendPlanServiceFixture(t)
			fixture.renderAndCommitCacheReady(t)
			component := fixture.service.incremental.components["backends"]
			key := resultKey(&component, "routes", "default", "a")
			encoded, exists := fixture.service.incremental.snapshot.results.Root().Get(key)
			require.True(t, exists)
			result, err := decodeExactComponentResult(encoded)
			require.NoError(t, err)
			poison(&result)
			poisonBackendPlanCacheResult(t, fixture.service.incremental.snapshot, &component, key, &result)

			failed, renderErr := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
			require.Error(t, renderErr)
			assert.Nil(t, failed)
		})
	}

	t.Run("invalid JSON payload", func(t *testing.T) {
		fixture := newBackendPlanServiceFixture(t)
		fixture.renderAndCommitCacheReady(t)
		component := fixture.service.incremental.components["backends"]
		key := resultKey(&component, "routes", "default", "a")
		txn := fixture.service.incremental.snapshot.results.Txn()
		queryKey := componentQueryKey(&component, "routes", "default", "a")
		txn.Insert(key, testExactRoot(t, queryKey, []byte(`{"backendPlan":`)))
		fixture.service.incremental.snapshot.results = txn.Commit()

		failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.Error(t, err)
		assert.Nil(t, failed)
	})

	t.Run("result without group index", func(t *testing.T) {
		fixture := newBackendPlanServiceFixture(t)
		fixture.renderAndCommitCacheReady(t)
		component := fixture.service.incremental.components["backends"]
		index := fixture.service.incremental.snapshot.groupIndexes[component.group]
		identity := incrementalGroupInstanceID{
			component: component.name, source: "routes", namespace: "default", name: "a",
		}
		instances := index.instances.Txn()
		instances.Delete(incrementalGroupInstanceKey(identity))
		poisoned := *index
		poisoned.instances = instances.Commit()
		fixture.service.incremental.snapshot.groupIndexes[component.group] = &poisoned

		failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.ErrorContains(t, err, `incremental state snapshot group "backends" changed`)
		assert.Nil(t, failed)
	})

	t.Run("group index without result", func(t *testing.T) {
		fixture := newBackendPlanServiceFixture(t)
		fixture.renderAndCommitCacheReady(t)
		component := fixture.service.incremental.components["backends"]
		key := resultKey(&component, "routes", "default", "a")
		results := fixture.service.incremental.snapshot.results.Txn()
		results.Delete(key)
		fixture.service.incremental.snapshot.results = results.Commit()

		failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.ErrorContains(t, err, "incremental state snapshot persistent root changed")
		assert.Nil(t, failed)
	})
}

func poisonBackendPlanCacheResult(
	t *testing.T,
	snapshot *incrementalStateSnapshot,
	component *incrementalComponent,
	key []byte,
	result *incrementalComponentResult,
) {
	t.Helper()
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	results := snapshot.results.Txn()
	queryKey := componentQueryKey(component, "routes", "default", "a")
	results.Insert(key, testExactRoot(t, queryKey, encoded))
	snapshot.results = results.Commit()

	index := snapshot.groupIndexes[component.group]
	identity := incrementalGroupInstanceID{
		component: component.name, source: "routes", namespace: "default", name: "a",
	}
	indexed, exists := index.instances.Root().Get(incrementalGroupInstanceKey(identity))
	require.True(t, exists)
	indexed.encodedResult = string(encoded)
	instances := index.instances.Txn()
	instances.Insert(incrementalGroupInstanceKey(identity), indexed)
	poisoned := *index
	poisoned.instances = instances.Commit()
	snapshot.groupIndexes[component.group] = &poisoned
}

func TestRenderServiceIncrementalBackendPlanRejectsNonMainRoot(t *testing.T) {
	cfg := backendPlanServiceConfig()
	cfg.HAProxyConfig.Template = "global\n"
	cfg.Maps = map[string]config.MapFile{"backends.map": {Template: `{{ render "backends" }}`}}
	service, provider := backendPlanServiceForConfig(t, cfg)

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, `backendPlan effect must render in "haproxy.cfg"`)
	assert.Nil(t, result)
}

func TestRenderServiceIncrementalBackendPlanRejectsConditionalUnconsumedGroup(t *testing.T) {
	cfg := backendPlanServiceConfig()
	second := cfg.TemplateSnippets["backends"]
	second.Name = "zz-other"
	second.Template = strings.ReplaceAll(backendPlanTestComponent, "be_shared", "be_other")
	cfg.TemplateSnippets[second.Name] = second
	cfg.HAProxyConfig.Template = "global\n{{ planRegistry.ProfileGroup() }}{{ render \"backends\" }}"
	service, provider := backendPlanServiceForConfig(t, cfg)

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "registered sections")
	require.ErrorContains(t, err, "be_other")
	assert.Nil(t, result)
}

func backendPlanServiceForConfig(
	t *testing.T,
	cfg *config.Config,
) (*RenderService, stores.StoreProvider) {
	t.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "route", map[string]any{"value": "value"}),
		[]string{"default", "route"},
	))
	return NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()}),
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
}
