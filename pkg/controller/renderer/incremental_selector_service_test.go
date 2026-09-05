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
	"log/slog"
	"reflect"
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

const incrementalSelectorProducerTemplate = `{%%
var target = item | dig_string("", "spec", "target")
var rank = item | dig_string("", "spec", "rank")
var value = item | dig_string("", "spec", "value")
show shared.PublishRanked("targets", target, rank, map[string]any{"value": value})
%%}`

const incrementalSelectorConsumerTemplate = `{%%
var route = item | dig_string("", "metadata", "name")
var target = item | dig_string("", "spec", "target")
var selected, found = shared.Select("policies", "targets", target)
if found {
  show route + "=" + dig_string(selected, "", "value") + "\n"
} else {
  show route + "=missing\n"
}
%%}`

const incrementalSelectorValuesConsumerTemplate = `{%%
var route = item | dig_string("", "metadata", "name")
var selected = shared.SelectValues("policies", "targets")
if len(selected) == 0 {
  show route + "=missing\n"
} else {
  show route + "=" + dig_string(selected[0], "", "value") + "\n"
}
%%}`

type incrementalSelectorServiceFixture struct {
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	policies *k8sstore.MemoryStore
	routes   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newIncrementalSelectorServiceFixture(t *testing.T) *incrementalSelectorServiceFixture {
	t.Helper()
	cfg := incrementalSelectorServiceConfig(false)
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	policies := k8sstore.NewMemoryStore(2)
	routes := k8sstore.NewMemoryStore(2)
	for _, resource := range []map[string]any{
		incrementalSelectorResource("a-winner", map[string]any{"target": "service-a", "rank": "1", "value": "a1"}),
		incrementalSelectorResource("a-loser", map[string]any{"target": "service-a", "rank": "2", "value": "ignored"}),
		incrementalSelectorResource("b-winner", map[string]any{"target": "service-b", "rank": "1", "value": "b1"}),
	} {
		name := resource["metadata"].(map[string]any)["name"].(string)
		require.NoError(t, policies.Add(resource, []string{"default", name}))
	}
	for _, resource := range []map[string]any{
		incrementalSelectorResource("route-a", map[string]any{"target": "service-a"}),
		incrementalSelectorResource("route-b", map[string]any{"target": "service-b"}),
		incrementalSelectorResource("route-c", map[string]any{"target": "service-c"}),
	} {
		name := resource["metadata"].(map[string]any)["name"].(string)
		require.NoError(t, routes.Add(resource, []string{"default", name}))
	}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"policies": policies, "routes": routes})
	return &incrementalSelectorServiceFixture{
		service: NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()}),
		engine:  engine, policies: policies, routes: routes, provider: provider,
	}
}

func incrementalSelectorServiceConfig(consumerFirst bool) *config.Config {
	root := `{{ render "100-policies" }}{{ render "200-routes" }}`
	if consumerFirst {
		root = `{{ render "200-routes" }}{{ render "100-policies" }}`
	}
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"policies": {
				APIVersion: "example.test/v1", Resources: "policies",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-policies": {
				Name: "100-policies", Requires: []string{"policies"},
				Incremental: &config.IncrementalTemplate{
					Source: "policies", Group: "policies",
					Effects: []config.IncrementalEffect{config.IncrementalEffectPublishValue},
				},
				Template: incrementalSelectorProducerTemplate,
			},
			"200-routes": {
				Name: "200-routes", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "routes", Consumes: []string{"policies"},
				},
				Template: incrementalSelectorConsumerTemplate,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: root},
	}
}

func incrementalSelectorResource(name string, spec map[string]any) map[string]any {
	return incrementalTestResource("default", name, spec)
}

func (f *incrementalSelectorServiceFixture) render(t *testing.T) string {
	t.Helper()
	return renderAndCommitIncrementalCacheReady(t, f.service, f.provider)
}

func TestRenderServiceIncrementalSelectorInvalidatesExactConsumers(t *testing.T) {
	fixture := newIncrementalSelectorServiceFixture(t)
	assert.Equal(t, "route-a=a1\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	baseline := fixture.engine.executionCounts()
	assert.Equal(t, "route-a=a1\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	assert.Equal(t, baseline, fixture.engine.executionCounts())

	require.NoError(t, fixture.policies.Update(
		incrementalSelectorResource("a-winner", map[string]any{"target": "service-a", "rank": "1", "value": "a2"}),
		[]string{"default", "a-winner"},
	))
	assert.Equal(t, "route-a=a2\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	counts := fixture.engine.executionCounts()
	assert.Equal(t, baseline["policies/a-winner"]+1, counts["policies/a-winner"])
	assert.Equal(t, baseline["routes/route-a"]+1, counts["routes/route-a"])
	assert.Equal(t, baseline["routes/route-b"], counts["routes/route-b"])
	assert.Equal(t, baseline["routes/route-c"], counts["routes/route-c"])

	beforeLoser := fixture.engine.executionCounts()
	require.NoError(t, fixture.policies.Update(
		incrementalSelectorResource("a-loser", map[string]any{"target": "service-a", "rank": "2", "value": "still-ignored"}),
		[]string{"default", "a-loser"},
	))
	assert.Equal(t, "route-a=a2\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	afterLoser := fixture.engine.executionCounts()
	assert.Equal(t, beforeLoser["policies/a-loser"]+1, afterLoser["policies/a-loser"])
	assert.Equal(t, beforeLoser["routes/route-a"], afterLoser["routes/route-a"])

	beforeMissing := fixture.engine.executionCounts()
	require.NoError(t, fixture.policies.Add(
		incrementalSelectorResource("c-winner", map[string]any{"target": "service-c", "rank": "1", "value": "c1"}),
		[]string{"default", "c-winner"},
	))
	assert.Equal(t, "route-a=a2\nroute-b=b1\nroute-c=c1\n", fixture.render(t))
	afterMissing := fixture.engine.executionCounts()
	assert.Equal(t, beforeMissing["routes/route-c"]+1, afterMissing["routes/route-c"])
	assert.Equal(t, beforeMissing["routes/route-a"], afterMissing["routes/route-a"])

	beforeDeletion := fixture.engine.executionCounts()
	require.NoError(t, fixture.policies.Delete("default", "c-winner", []string{"default", "c-winner"}))
	assert.Equal(t, "route-a=a2\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	afterDeletion := fixture.engine.executionCounts()
	assert.Equal(t, beforeDeletion["routes/route-c"]+1, afterDeletion["routes/route-c"])
	assert.Equal(t, beforeDeletion["routes/route-b"], afterDeletion["routes/route-b"])

	beforePromotion := fixture.engine.executionCounts()
	require.NoError(t, fixture.policies.Delete("default", "a-winner", []string{"default", "a-winner"}))
	assert.Equal(t, "route-a=still-ignored\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	afterPromotion := fixture.engine.executionCounts()
	assert.Equal(t, beforePromotion["routes/route-a"]+1, afterPromotion["routes/route-a"])
	assert.Equal(t, beforePromotion["routes/route-b"], afterPromotion["routes/route-b"])
}

func TestRenderServiceIncrementalSelectorWinnerOwnerChangeWithSameValueDoesNotInvalidate(t *testing.T) {
	fixture := newIncrementalSelectorServiceFixture(t)
	assert.Equal(t, "route-a=a1\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	baseline := fixture.engine.executionCounts()
	require.NoError(t, fixture.policies.Add(
		incrementalSelectorResource("a-preferred", map[string]any{"target": "service-a", "rank": "0", "value": "a1"}),
		[]string{"default", "a-preferred"},
	))
	require.NoError(t, fixture.policies.Delete("default", "a-winner", []string{"default", "a-winner"}))

	assert.Equal(t, "route-a=a1\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	counts := fixture.engine.executionCounts()
	assert.Equal(t, baseline["routes/route-a"], counts["routes/route-a"])
	assert.Equal(t, baseline["routes/route-b"], counts["routes/route-b"])
	assert.Equal(t, baseline["routes/route-c"], counts["routes/route-c"])
}

func TestRenderServiceIncrementalSelectorValuesTracksObservableValues(t *testing.T) {
	cfg := incrementalSelectorValuesServiceConfig()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	policies := k8sstore.NewMemoryStore(2)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, policies.Add(
		incrementalSelectorResource("winner", map[string]any{"target": "service", "rank": "1", "value": "same"}),
		[]string{"default", "winner"},
	))
	require.NoError(t, policies.Add(
		incrementalSelectorResource("loser", map[string]any{"target": "service", "rank": "2", "value": "same"}),
		[]string{"default", "loser"},
	))
	require.NoError(t, routes.Add(
		incrementalSelectorResource("route", map[string]any{}), []string{"default", "route"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"policies": policies, "routes": routes})
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})

	assert.Equal(t, "route=same\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	baseline := engine.executionCounts()
	assert.Equal(t, "route=same\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, baseline, engine.executionCounts())

	require.NoError(t, policies.Update(
		incrementalSelectorResource("loser", map[string]any{"target": "service", "rank": "2", "value": "ignored"}),
		[]string{"default", "loser"},
	))
	assert.Equal(t, "route=same\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	afterLoser := engine.executionCounts()
	assert.Equal(t, baseline["routes/route"], afterLoser["routes/route"])

	require.NoError(t, policies.Update(
		incrementalSelectorResource("winner", map[string]any{"target": "service", "rank": "0", "value": "same"}),
		[]string{"default", "winner"},
	))
	assert.Equal(t, "route=same\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	afterRank := engine.executionCounts()
	assert.Equal(t, baseline["routes/route"], afterRank["routes/route"])

	require.NoError(t, policies.Update(
		incrementalSelectorResource("loser", map[string]any{"target": "service", "rank": "2", "value": "same"}),
		[]string{"default", "loser"},
	))
	require.NoError(t, policies.Delete("default", "winner", []string{"default", "winner"}))
	assert.Equal(t, "route=same\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	afterPromotion := engine.executionCounts()
	assert.Equal(t, baseline["routes/route"], afterPromotion["routes/route"])

	require.NoError(t, policies.Update(
		incrementalSelectorResource("loser", map[string]any{"target": "service", "rank": "2", "value": "changed"}),
		[]string{"default", "loser"},
	))
	assert.Equal(t, "route=changed\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	afterValue := engine.executionCounts()
	assert.Equal(t, afterPromotion["routes/route"]+1, afterValue["routes/route"])

	require.NoError(t, policies.Delete("default", "loser", []string{"default", "loser"}))
	assert.Equal(t, "route=missing\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	afterDelete := engine.executionCounts()
	assert.Equal(t, afterValue["routes/route"]+1, afterDelete["routes/route"])
}

func TestIncrementalSelectorValuesInputIsExactDetachedAndAuthenticated(t *testing.T) {
	winner := incrementalInstanceResult{
		component: "producer", source: "policies", namespace: "default", name: "z-winner",
		result: selectorRankedResult(t, "service", "1", "same"),
	}
	loser := incrementalInstanceResult{
		component: "producer", source: "policies", namespace: "default", name: "a-loser",
		result: selectorRankedResult(t, "service", "2", "same"),
	}
	other := incrementalInstanceResult{
		component: "producer", source: "policies", namespace: "default", name: "m-other",
		result: selectorRankedResult(t, "other", "1", "other"),
	}
	index, err := newIncrementalGroupIndex().replace(&winner, nil)
	require.NoError(t, err)
	index, err = index.replace(&loser, nil)
	require.NoError(t, err)
	index, err = index.replace(&other, nil)
	require.NoError(t, err)

	first, err := incrementalSelectorValuesInput(index, "policies", "targets")
	require.NoError(t, err)
	again, err := incrementalSelectorValuesInput(index, "policies", "targets")
	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Equal(t, `[{"value":"other"},{"value":"same"}]`, string(first.Value))
	first.Value[0] = 'x'
	detached, err := incrementalSelectorValuesInput(index, "policies", "targets")
	require.NoError(t, err)
	assert.Equal(t, `[{"value":"other"},{"value":"same"}]`, string(detached.Value))

	promoted, err := index.remove("producer", "policies", "default", "z-winner")
	require.NoError(t, err)
	afterPromotion, err := incrementalSelectorValuesInput(promoted, "policies", "targets")
	require.NoError(t, err)
	assert.Equal(t, `[{"value":"same"},{"value":"other"}]`, string(afterPromotion.Value))
	assert.NotEqual(t, detached.Revision, afterPromotion.Revision)

	poisoned := *index
	poisoned.publications = cloneOrderedTree(index.publications)
	_, err = incrementalSelectorValuesInput(&poisoned, "policies", "targets")
	require.ErrorContains(t, err, "authentication seal")
}

func incrementalSelectorValuesServiceConfig() *config.Config {
	cfg := incrementalSelectorServiceConfig(false)
	consumer := cfg.TemplateSnippets["200-routes"]
	consumer.Template = incrementalSelectorValuesConsumerTemplate
	cfg.TemplateSnippets["200-routes"] = consumer
	return cfg
}

func selectorRankedResult(t *testing.T, key, rank, value string) incrementalComponentResult {
	t.Helper()
	const cell = "targets"
	recorder := &incrementalRecorder{}
	recorder.PublishRanked(cell, key, rank, map[string]any{"value": value})
	result, err := recorder.result("")
	require.NoError(t, err)
	return result
}

func TestRenderServiceIncrementalSelectorAbortDoesNotPublish(t *testing.T) {
	fixture := newIncrementalSelectorServiceFixture(t)
	assert.Equal(t, "route-a=a1\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
	require.NoError(t, fixture.policies.Update(
		incrementalSelectorResource("a-winner", map[string]any{"target": "service-a", "rank": "1", "value": "aborted"}),
		[]string{"default", "a-winner"},
	))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "route-a=aborted\nroute-b=b1\nroute-c=missing\n", result.HAProxyConfig)
	result.InputTransaction.Abort()

	committed, err := incrementalSelectorInput(
		fixture.service.incremental.snapshot.groupIndexes["policies"],
		"policies", "targets", "service-a",
	)
	require.NoError(t, err)
	require.True(t, committed.Found)
	assert.Contains(t, string(committed.Value), "a1")
	assert.Equal(t, "route-a=aborted\nroute-b=b1\nroute-c=missing\n", fixture.render(t))
}

func TestRenderServiceIncrementalSelectorRejectsOutOfOrderConsumer(t *testing.T) {
	cfg := incrementalSelectorServiceConfig(true)
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	policies := k8sstore.NewMemoryStore(2)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, policies.Add(
		incrementalSelectorResource("winner", map[string]any{"target": "service", "rank": "1", "value": "value"}),
		[]string{"default", "winner"},
	))
	require.NoError(t, routes.Add(
		incrementalSelectorResource("route", map[string]any{"target": "service"}),
		[]string{"default", "route"},
	))
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	_, err = service.Render(t.Context(), stores.NewRealStoreProvider(map[string]stores.Store{
		"policies": policies, "routes": routes,
	}), rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, `publication group "policies" must complete its canonical root call`)
}

func TestRenderServiceIncrementalSelectorRejectsMutation(t *testing.T) {
	cfg := incrementalSelectorServiceConfig(false)
	consumer := cfg.TemplateSnippets["200-routes"]
	consumer.Template = `{% var target = item | dig_string("", "spec", "target") %}{% var selected, found = shared.Select("policies", "targets", target) %}{% if found %}{% selected.(map[string]any)["value"] = "poison" %}{% end %}`
	cfg.TemplateSnippets["200-routes"] = consumer
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	policies := k8sstore.NewMemoryStore(2)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, policies.Add(
		incrementalSelectorResource("winner", map[string]any{"target": "service", "rank": "1", "value": "value"}),
		[]string{"default", "winner"},
	))
	require.NoError(t, routes.Add(
		incrementalSelectorResource("route", map[string]any{"target": "service"}),
		[]string{"default", "route"},
	))
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	_, err = service.Render(t.Context(), stores.NewRealStoreProvider(map[string]stores.Store{
		"policies": policies, "routes": routes,
	}), rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "mutates an immutable input")
}

func TestRenderServiceIncrementalSelectorAuthenticatedOptionalProducerAbsentThenPresent(t *testing.T) {
	absent := incrementalSelectorServiceConfig(false)
	delete(absent.WatchedResources, "policies")
	delete(absent.TemplateSnippets, "100-policies")
	absent.TemplateSnippets["200-routes"].Incremental.Consumes = nil
	absent.TemplateSnippets["200-routes"].Incremental.OptionalConsumes = []string{"policies"}
	absent.AbsentIncrementalGroups = map[string]struct{}{"policies": {}}
	absent.HAProxyConfig.Template = `{{ render "200-routes" }}`
	absentDeclarations := helpers.BuildAdditionalDeclarations(absent, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	absentEngine, err := helpers.NewEngineFromConfigWithOptions(absent, nil, nil, absentDeclarations, helpers.EngineOptions{})
	require.NoError(t, err)
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalSelectorResource("route", map[string]any{"target": "service"}),
		[]string{"default", "route"},
	))
	absentProvider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
	absentService := NewRenderService(&RenderServiceConfig{Engine: absentEngine, Config: absent, Logger: slog.Default()})
	assert.Equal(t, "route=missing\n", renderAndCommitIncrementalCacheReady(t, absentService, absentProvider))

	present := incrementalSelectorServiceConfig(false)
	present.TemplateSnippets["200-routes"].Incremental.Consumes = nil
	present.TemplateSnippets["200-routes"].Incremental.OptionalConsumes = []string{"policies"}
	presentDeclarations := helpers.BuildAdditionalDeclarations(present, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	presentEngine, err := helpers.NewEngineFromConfigWithOptions(present, nil, nil, presentDeclarations, helpers.EngineOptions{})
	require.NoError(t, err)
	policies := k8sstore.NewMemoryStore(2)
	require.NoError(t, policies.Add(
		incrementalSelectorResource("winner", map[string]any{"target": "service", "rank": "1", "value": "value"}),
		[]string{"default", "winner"},
	))
	presentProvider := stores.NewRealStoreProvider(map[string]stores.Store{"policies": policies, "routes": routes})
	presentService := NewRenderService(&RenderServiceConfig{Engine: presentEngine, Config: present, Logger: slog.Default()})
	assert.Equal(t, "route=value\n", renderAndCommitIncrementalCacheReady(t, presentService, presentProvider))
}
