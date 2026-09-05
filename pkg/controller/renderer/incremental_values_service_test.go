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
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const publishedCompetitorComponent = `{%%
var name = item | dig_string("", "metadata", "name")
var value = item | dig_string("", "spec", "value")
show shared.Publish("hosts", "shared", map[string]any{
  "name": name,
  "source": source,
  "nested": map[string]any{"value": value},
})
%%}`

const publishedBackendComponent = `{%%
var name = item | dig_string("", "metadata", "name")
var value = item | dig_string("", "spec", "value")
show shared.Publish("hosts", "shared", map[string]any{
  "name": name,
  "source": source,
  "nested": map[string]any{"value": value},
})
var backendName = "be_" + name
var token, _ = planRegistry.BackendWhenAny(
  map[string]any{"name": backendName, "guid": value},
  "backend " + backendName + "\n    # " + value + "\n",
  "hosts",
  []string{"shared"},
)
show token
%%}`

type publishedValueServiceFixture struct {
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	routes   *k8sstore.MemoryStore
	claims   *k8sstore.MemoryStore
	others   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newPublishedValueServiceFixture(t *testing.T) *publishedValueServiceFixture {
	t.Helper()
	cfg := publishedValueServiceConfig()
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
	claims := k8sstore.NewMemoryStore(2)
	others := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(publishedValueResource("a", "alpha"), []string{"default", "a"}))
	require.NoError(t, routes.Add(publishedValueResource("b", "beta"), []string{"default", "b"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes, "claims": claims, "others": others,
	})
	return &publishedValueServiceFixture{
		service: service, engine: engine, routes: routes, claims: claims, others: others,
		provider: provider,
	}
}

func publishedValueServiceConfig() *config.Config {
	resources := map[string]config.WatchedResource{}
	for _, source := range []string{"routes", "claims", "others"} {
		resources[source] = config.WatchedResource{
			APIVersion: "example.test/v1", Resources: source,
			IndexBy: []string{"metadata.namespace", "metadata.name"},
		}
	}
	return &config.Config{
		Dataplane:        testDataplaneConfig(),
		WatchedResources: resources,
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-competitor": {
				Name: "100-competitor", Requires: []string{"claims"},
				Incremental: &config.IncrementalTemplate{
					Source: "claims", Group: "published-plans",
					Effects: []config.IncrementalEffect{config.IncrementalEffectPublishValue},
				},
				Template: publishedCompetitorComponent,
			},
			"200-backends": {
				Name: "200-backends", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "published-plans",
					Effects: []config.IncrementalEffect{
						config.IncrementalEffectPublishValue,
						config.IncrementalEffectBackendPlan,
					},
				},
				Template: publishedBackendComponent,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `global
{{ planRegistry.ProfileGroup() }}
{% for _, value := range incremental_values("published-plans", "hosts") %}# main={{ value | dig_string("", "nested", "value") }}
{% end %}{{ render "100-competitor" }}{{ render "200-backends" }}`},
		Maps: map[string]config.MapFile{
			"published.map": {Template: `{% for _, value := range incremental_values("published-plans", "hosts") %}shared {{ value | dig_string("", "nested", "value") }}
{% end %}`},
		},
		K8sResources: map[string]config.K8sResource{
			"published.yaml": {Template: `{% var values = incremental_values("published-plans", "hosts") %}{% if len(values) > 0 %}apiVersion: v1
kind: ConfigMap
metadata:
  namespace: default
  name: published
  annotations:
    value: "{{ values[0] | dig_string("", "nested", "value") }}"
{% end %}`},
		},
	}
}

func publishedValueResource(name, value string) map[string]any {
	return incrementalTestResource("default", name, map[string]any{"value": value})
}

func (f *publishedValueServiceFixture) render(t *testing.T) (*RenderResult, error) {
	t.Helper()
	return f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
}

func (f *publishedValueServiceFixture) renderAndCommitCacheReady(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.render(t)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func publishedMapContent(t *testing.T, result *RenderResult) string {
	t.Helper()
	for _, file := range requireAuxiliaryFiles(t, result).MapFiles {
		if strings.HasSuffix(file.Path, "published.map") {
			return file.Content
		}
	}
	t.Fatal("published.map was not rendered")
	return ""
}

func assertPublishedRootValues(t *testing.T, result *RenderResult, value string) {
	t.Helper()
	assert.Contains(t, result.HAProxyConfig, "# main="+value)
	assert.Equal(t, "shared "+value+"\n", publishedMapContent(t, result))
	resources := requireRenderedResources(t, result)
	require.Len(t, resources, 1)
	annotations := resources[0].Object["metadata"].(map[string]any)["annotations"].(map[string]any)
	assert.Equal(t, value, annotations["value"])
}

func TestRenderServicePublishedValuesLifecycleAndPromotion(t *testing.T) {
	fixture := newPublishedValueServiceFixture(t)
	first := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, first, "alpha")
	assert.Contains(t, first.HAProxyConfig, "backend be_a")
	assert.NotContains(t, first.HAProxyConfig, "backend be_b")
	assert.Equal(t, map[string]int{"routes/a": 1, "routes/b": 1}, fixture.engine.executionCounts())

	warm := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, map[string]int{"routes/a": 1, "routes/b": 1}, fixture.engine.executionCounts())

	require.NoError(t, fixture.routes.Update(publishedValueResource("a", "alpha-2"), []string{"default", "a"}))
	changed := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, changed, "alpha-2")
	assert.Contains(t, changed.HAProxyConfig, "# alpha-2")
	assert.Equal(t, 2, fixture.engine.executionCounts()["routes/a"])
	assert.Equal(t, 1, fixture.engine.executionCounts()["routes/b"])

	require.NoError(t, fixture.others.Add(publishedValueResource("unrelated", "ignored"), []string{"default", "unrelated"}))
	unrelated := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, changed.HAProxyConfig, unrelated.HAProxyConfig)
	assert.Equal(t, map[string]int{"routes/a": 2, "routes/b": 1}, fixture.engine.executionCounts())

	require.NoError(t, fixture.claims.Add(publishedValueResource("claim", "blocked"), []string{"default", "claim"}))
	blocked := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, blocked, "blocked")
	assert.NotContains(t, blocked.HAProxyConfig, "backend be_a")
	assert.NotContains(t, blocked.HAProxyConfig, "backend be_b")
	assert.Equal(t, 1, fixture.engine.executionCounts()["claims/claim"])
	assert.Equal(t, 2, fixture.engine.executionCounts()["routes/a"])

	require.NoError(t, fixture.claims.Delete("default", "claim", []string{"default", "claim"}))
	promotedAfterCompetitor := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, promotedAfterCompetitor, "alpha-2")
	assert.Contains(t, promotedAfterCompetitor.HAProxyConfig, "backend be_a")
	assert.Equal(t, 2, fixture.engine.executionCounts()["routes/a"])

	require.NoError(t, fixture.routes.Delete("default", "a", []string{"default", "a"}))
	promotedLoser := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, promotedLoser, "beta")
	assert.Contains(t, promotedLoser.HAProxyConfig, "backend be_b")
	assert.Equal(t, 1, fixture.engine.executionCounts()["routes/b"])
}

func TestRenderServicePublishedStatusPatchResultCannotPoisonWarmValues(t *testing.T) {
	cfg := publishedValueServiceConfig()
	cfg.HAProxyConfig.Template = `global
{{ planRegistry.ProfileGroup() }}
{%%
var values = incremental_values("published-plans", "hosts")
if len(values) > 0 {
  var target = map[string]any{
    "apiVersion": "example.test/v1", "kind": "Route",
    "metadata": map[string]any{
      "namespace": "default", "name": "shared", "uid": "uid-shared", "resourceVersion": "rv-shared",
    },
  }
  statusPatch(target, map[string]any{
    "rendered": values[0].(map[string]any),
  })
}
%%}
{{ render "100-competitor" }}{{ render "200-backends" }}`
	service, provider := publishedValueServiceForConfig(t, cfg)

	first, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	firstPatches := materializedStatusPatches(t, first)
	require.Len(t, firstPatches, 1)
	firstStatus := firstPatches[0].Variants["rendered"]
	assert.Equal(t, "value", firstStatus["nested"].(map[string]any)["value"])
	firstStatus["nested"].(map[string]any)["value"] = "poison"
	firstStatus["added"] = true

	component := service.incremental.components["200-backends"]
	query := componentQueryKey(&component, "routes", "default", "route")
	executions := service.incremental.graph.Counters(query).Executions
	warm, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, warm.InputTransaction.Commit(t.Context()))
	warmPatches := materializedStatusPatches(t, warm)
	require.Len(t, warmPatches, 1)
	warmStatus := warmPatches[0].Variants["rendered"]
	assert.Equal(t, "value", warmStatus["nested"].(map[string]any)["value"])
	assert.NotContains(t, warmStatus, "added")
	assert.Equal(t, executions, service.incremental.graph.Counters(query).Executions)
}

func TestRenderServicePublishedValuesAbortRetryAndConcurrentCommit(t *testing.T) {
	fixture := newPublishedValueServiceFixture(t)
	fixture.renderAndCommitCacheReady(t)
	require.NoError(t, fixture.routes.Update(publishedValueResource("a", "aborted"), []string{"default", "a"}))
	committedSnapshot := fixture.service.incremental.snapshot
	aborted, err := fixture.render(t)
	require.NoError(t, err)
	assertPublishedRootValues(t, aborted, "aborted")
	aborted.InputTransaction.Abort()
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)

	retried := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, retried, "aborted")
	assert.Equal(t, 3, fixture.engine.executionCounts()["routes/a"])
	require.NoError(t, fixture.routes.Update(publishedValueResource("a", "concurrent"), []string{"default", "a"}))

	results := make([]*RenderResult, 2)
	errors := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errors[index] = fixture.service.Render(
				context.Background(), fixture.provider, rendercontext.RenderModeReconcile,
			)
		}()
	}
	wait.Wait()
	for index := range results {
		require.NoError(t, errors[index])
		assertPublishedRootValues(t, results[index], "concurrent")
	}
	require.NoError(t, results[0].InputTransaction.Commit(t.Context()))
	winnerSnapshot := fixture.service.incremental.snapshot
	require.NoError(t, results[1].InputTransaction.Commit(t.Context()))
	assert.Same(t, winnerSnapshot, fixture.service.incremental.snapshot)
	winners, err := decodeIncrementalPublishedWinners(
		winnerSnapshot.groupIndexes["published-plans"], "hosts",
	)
	require.NoError(t, err)
	assert.Equal(t, "concurrent", winners[0].(map[string]any)["nested"].(map[string]any)["value"])
	executions := fixture.engine.executionCounts()["routes/a"]
	warm := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, warm, "concurrent")
	assert.Equal(t, executions, fixture.engine.executionCounts()["routes/a"])
}

func TestRenderServiceLateOlderGenerationCannotOverwriteNewerOutput(t *testing.T) {
	fixture := newPublishedValueServiceFixture(t)
	fixture.renderAndCommitCacheReady(t)

	require.NoError(t, fixture.routes.Update(
		publishedValueResource("a", "older"), []string{"default", "a"},
	))
	older, err := fixture.render(t)
	require.NoError(t, err)
	assertPublishedRootValues(t, older, "older")

	require.NoError(t, fixture.routes.Update(
		publishedValueResource("a", "newer"), []string{"default", "a"},
	))
	newer, err := fixture.render(t)
	require.NoError(t, err)
	assertPublishedRootValues(t, newer, "newer")
	require.NoError(t, newer.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, fixture.service)

	winnerSnapshot := fixture.service.incremental.snapshot
	winnerGeneration := fixture.service.incremental.graph.Generation()
	winnerPlan := fixture.service.lastPlan
	winnerOutput := fixture.service.lastOutputSnapshot
	winnerCycle := fixture.service.lastCycleSnapshot
	winnerPlanIdentity := fixture.service.lastPlanIdentity
	winnerRenderCache := fixture.service.lastRenderCache
	winnerExactCycle := fixture.service.exactCycleCandidate
	winnerOutputGeneration := fixture.service.publishedOutputGeneration
	winnerExecutions := fixture.engine.executionCounts()

	require.ErrorIs(t, older.InputTransaction.Commit(t.Context()), incremental.ErrRevisionConflict)
	assert.Same(t, winnerSnapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, winnerGeneration, fixture.service.incremental.graph.Generation())
	assert.Same(t, winnerPlan, fixture.service.lastPlan)
	assert.Same(t, winnerOutput, fixture.service.lastOutputSnapshot)
	assert.Same(t, winnerCycle, fixture.service.lastCycleSnapshot)
	assert.Same(t, winnerPlanIdentity, fixture.service.lastPlanIdentity)
	assert.Same(t, winnerRenderCache, fixture.service.lastRenderCache)
	assert.Same(t, winnerExactCycle, fixture.service.exactCycleCandidate)
	assert.Equal(t, winnerOutputGeneration, fixture.service.publishedOutputGeneration)
	assert.Equal(t, winnerExecutions, fixture.engine.executionCounts())

	warm := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, warm, "newer")
	assert.Equal(t, winnerExecutions, fixture.engine.executionCounts())
}

func TestRenderServicePublishedValuesAdmissionDoesNotPublish(t *testing.T) {
	fixture := newPublishedValueServiceFixture(t)
	baseline := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, baseline, "alpha")
	committedSnapshot := fixture.service.incremental.snapshot

	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{
				Object: publishedValueResource("a", "proposed"),
			}),
		}),
	)
	result, err := fixture.service.Render(
		t.Context(), admissionProvider, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "a"),
	)
	require.NoError(t, err)
	assertPublishedRootValues(t, result, "proposed")
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)

	executions := fixture.engine.executionCounts()["routes/a"]
	warm := fixture.renderAndCommitCacheReady(t)
	assertPublishedRootValues(t, warm, "alpha")
	assert.Equal(t, executions, fixture.engine.executionCounts()["routes/a"])
}

func TestRenderServicePublishedValuesStaticCold(t *testing.T) {
	cfg := publishedValueServiceConfig()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	claims := k8sstore.NewMemoryStore(2)
	others := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(publishedValueResource("b", "beta"), []string{"default", "b"}))
	require.NoError(t, routes.Add(publishedValueResource("a", "alpha"), []string{"default", "a"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes, "claims": claims, "others": others,
	})

	_, output, err := renderStaticColdIncremental(t, cfg, engine, provider)
	require.NoError(t, err)
	assert.Contains(t, output, "# main=alpha")
	assert.Contains(t, output, "backend be_a")
	assert.NotContains(t, output, "backend be_b")
}

func TestRenderServicePublishedValuesRejectsUnknownGroupAndCellShape(t *testing.T) {
	for name, accessor := range map[string]string{
		"unknown group": `incremental_values("missing", "hosts")`,
		"empty cell":    `incremental_values("published-plans", "")`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := publishedValueServiceConfig()
			cfg.HAProxyConfig.Template = `{{ ` + accessor + ` | toJSON() }}{{ render "100-competitor" }}{{ render "200-backends" }}`
			service, provider := publishedValueServiceForConfig(t, cfg)
			result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestRenderServicePublishedValuesRejectsPoisonedCache(t *testing.T) {
	t.Run("malformed result JSON", func(t *testing.T) {
		fixture := newPublishedValueServiceFixture(t)
		fixture.renderAndCommitCacheReady(t)
		component := fixture.service.incremental.components["200-backends"]
		key := resultKey(&component, "routes", "default", "a")
		queryKey := componentQueryKey(&component, "routes", "default", "a")
		results := fixture.service.incremental.snapshot.results.Txn()
		results.Insert(key, testExactRoot(t, queryKey, []byte(`{"published":[{"cell":"hosts","key":"shared","value":`)))
		fixture.service.incremental.snapshot.results = results.Commit()

		result, err := fixture.render(t)
		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("missing publication owner", func(t *testing.T) {
		fixture := newPublishedValueServiceFixture(t)
		fixture.renderAndCommitCacheReady(t)
		group := fixture.service.incremental.components["200-backends"].group
		index := fixture.service.incremental.snapshot.groupIndexes[group]
		identity := incrementalPublicationIdentityKey("hosts", "shared")
		owners, exists := index.publications.Root().Get(identity)
		require.True(t, exists)
		location, _, exists := owners.Root().Minimum()
		require.True(t, exists)
		ownerTxn := owners.Txn()
		ownerTxn.Delete([]byte(location))
		publications := index.publications.Txn()
		publications.Insert(identity, ownerTxn.Commit())
		poisoned := *index
		poisoned.publications = publications.Commit()
		fixture.service.incremental.snapshot.groupIndexes[group] = &poisoned

		result, err := fixture.render(t)
		require.ErrorContains(t, err, `incremental state snapshot group "published-plans" changed`)
		assert.Nil(t, result)
	})

	t.Run("poisoned publication owner", func(t *testing.T) {
		fixture := newPublishedValueServiceFixture(t)
		fixture.renderAndCommitCacheReady(t)
		group := fixture.service.incremental.components["200-backends"].group
		index := fixture.service.incremental.snapshot.groupIndexes[group]
		identity := incrementalPublicationIdentityKey("hosts", "shared")
		owners, exists := index.publications.Root().Get(identity)
		require.True(t, exists)
		location, owner, exists := owners.Root().Minimum()
		require.True(t, exists)
		owner.instance.name = "poison"
		ownerTxn := owners.Txn()
		ownerTxn.Insert([]byte(location), owner)
		publications := index.publications.Txn()
		publications.Insert(identity, ownerTxn.Commit())
		poisoned := *index
		poisoned.publications = publications.Commit()
		fixture.service.incremental.snapshot.groupIndexes[group] = &poisoned

		result, err := fixture.render(t)
		require.ErrorContains(t, err, `incremental state snapshot group "published-plans" changed`)
		assert.Nil(t, result)
	})
}

func publishedValueServiceForConfig(
	t *testing.T,
	cfg *config.Config,
) (*RenderService, stores.StoreProvider) {
	t.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	claims := k8sstore.NewMemoryStore(2)
	others := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(publishedValueResource("route", "value"), []string{"default", "route"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes, "claims": claims, "others": others,
	})
	return NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()}), provider
}

var _ templating.IncrementalValueReader = (*incrementalRenderSession)(nil)
var _ templating.IncrementalValueReader = (*coldIncrementalRenderer)(nil)
