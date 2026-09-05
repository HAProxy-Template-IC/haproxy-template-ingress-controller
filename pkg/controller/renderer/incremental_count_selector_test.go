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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const incrementalCountSelectorConsumerTemplate = `{%%
var route = item | dig_string("", "metadata", "name")
show route + "=" + tostring(shared.Count("policies", "targets")) + "\n"
%%}`

type incrementalCountSelectorFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	policies *k8sstore.MemoryStore
	routes   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func TestIncrementalPublicationCountTracksWinnersAndRejectsCorruption(t *testing.T) {
	winner := incrementalInstanceResult{
		component: "producer", source: "policies", namespace: "default", name: "winner",
		result: selectorRankedResult(t, "shared", "1", "same"),
	}
	loser := incrementalInstanceResult{
		component: "producer", source: "policies", namespace: "default", name: "loser",
		result: selectorRankedResult(t, "shared", "2", "same"),
	}
	other := incrementalInstanceResult{
		component: "producer", source: "policies", namespace: "default", name: "other",
		result: selectorRankedResult(t, "other", "1", "other"),
	}
	index, err := newIncrementalGroupIndex().replace(&winner, nil)
	require.NoError(t, err)
	index, err = index.replace(&loser, nil)
	require.NoError(t, err)
	index, err = index.replace(&other, nil)
	require.NoError(t, err)

	count, err := index.publishedWinnerCount("targets")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	beforePromotion, err := incrementalSelectorCountInput(index, "policies", "targets")
	require.NoError(t, err)

	promoted, err := index.remove("producer", "policies", "default", "winner")
	require.NoError(t, err)
	afterPromotion, err := incrementalSelectorCountInput(promoted, "policies", "targets")
	require.NoError(t, err)
	assert.Equal(t, beforePromotion, afterPromotion)

	deleted, err := promoted.remove("producer", "policies", "default", "loser")
	require.NoError(t, err)
	afterDelete, err := incrementalSelectorCountInput(deleted, "policies", "targets")
	require.NoError(t, err)
	assert.NotEqual(t, afterPromotion.Revision, afterDelete.Revision)
	assert.Equal(t, 1, mustDecodeIncrementalSelectorCount(t, afterDelete.Value))

	poisoned := *index
	countTxn := index.publicationCounts.Txn()
	countTxn.Insert(incrementalOrderedTuple("targets"), 99)
	poisoned.publicationCounts = countTxn.Commit()
	_, err = poisoned.publishedWinnerCount("targets")
	require.ErrorContains(t, err, "count index does not match")
}

func TestRenderServiceIncrementalCountSelectorLifecycleABAAndPromotion(t *testing.T) {
	fixture := newIncrementalCountSelectorFixture(t)
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	baseline := fixture.engine.executionCounts()
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	assert.Equal(t, baseline, fixture.engine.executionCounts())

	require.NoError(t, fixture.policies.Update(
		incrementalSelectorResource("a-loser", map[string]any{
			"target": "service-a", "rank": "2", "value": "changed-loser",
		}), []string{"default", "a-loser"},
	))
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	afterLoser := fixture.engine.executionCounts()
	assert.Equal(t, baseline["routes/route-a"], afterLoser["routes/route-a"])

	require.NoError(t, fixture.policies.Add(
		incrementalSelectorResource("a-preferred", map[string]any{
			"target": "service-a", "rank": "0", "value": "same",
		}), []string{"default", "a-preferred"},
	))
	require.NoError(t, fixture.policies.Delete("default", "a-winner", []string{"default", "a-winner"}))
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	afterPromotion := fixture.engine.executionCounts()
	assert.Equal(t, afterLoser["routes/route-a"], afterPromotion["routes/route-a"])

	require.NoError(t, fixture.policies.Add(
		incrementalSelectorResource("transient", map[string]any{
			"target": "service-transient", "rank": "1", "value": "transient",
		}), []string{"default", "transient"},
	))
	require.NoError(t, fixture.policies.Delete("default", "transient", []string{"default", "transient"}))
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	afterABA := fixture.engine.executionCounts()
	assert.Equal(t, afterPromotion["routes/route-a"], afterABA["routes/route-a"])

	require.NoError(t, fixture.policies.Add(
		incrementalSelectorResource("new", map[string]any{
			"target": "service-new", "rank": "1", "value": "new",
		}), []string{"default", "new"},
	))
	assert.Equal(t, "route-a=3\nroute-b=3\nroute-c=3\n", fixture.renderAndCommit(t))
	afterAdd := fixture.engine.executionCounts()
	assert.Equal(t, afterABA["routes/route-a"]+1, afterAdd["routes/route-a"])
	require.NoError(t, fixture.policies.Delete("default", "new", []string{"default", "new"}))
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	assert.Equal(t, afterAdd["routes/route-a"]+1, fixture.engine.executionCounts()["routes/route-a"])
}

func TestRenderServiceIncrementalCountSelectorAbortAdmissionAndColdStayIsolated(t *testing.T) {
	fixture := newIncrementalCountSelectorFixture(t)
	live := fixture.renderAndCommit(t)
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", live)
	committedSnapshot := fixture.service.incremental.snapshot

	cold, err := renderServiceStaticCold(t, fixture.service, fixture.provider)
	require.NoError(t, err)
	assert.Equal(t, live, cold.HAProxyConfig)
	cold.InputTransaction.Abort()

	proposed := incrementalSelectorResource("proposed", map[string]any{
		"target": "service-proposed", "rank": "1", "value": "proposed",
	})
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"policies": stores.NewStoreOverlayForCreate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("policies", "default", "proposed"),
	)
	require.NoError(t, err)
	assert.Equal(t, "route-a=3\nroute-b=3\nroute-c=3\n", admission.HAProxyConfig)
	require.NoError(t, admission.InputTransaction.Commit(t.Context()))
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, live, fixture.renderAndCommit(t))

	require.NoError(t, fixture.policies.Add(proposed, []string{"default", "proposed"}))
	committedSnapshot = fixture.service.incremental.snapshot
	fixture.config.TemplatingSettings.ExtraContext["failAfterCount"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after count")
	assert.Nil(t, failed)
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterCount"] = false
	assert.Equal(t, "route-a=3\nroute-b=3\nroute-c=3\n", fixture.renderAndCommit(t))
}

func TestRenderServiceIncrementalCountSelectorConcurrentLosers(t *testing.T) {
	fixture := newIncrementalCountSelectorFixture(t)
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	baseline := fixture.engine.executionCounts()
	for index := range 32 {
		name := "loser-" + tostringInt(index)
		require.NoError(t, fixture.policies.Add(
			incrementalSelectorResource(name, map[string]any{
				"target": "service-a", "rank": "9", "value": name,
			}), []string{"default", name},
		))
	}

	results := make([]*RenderResult, 8)
	errors := make([]error, len(results))
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
		assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", results[index].HAProxyConfig)
		results[index].InputTransaction.Abort()
	}
	assert.Equal(t, "route-a=2\nroute-b=2\nroute-c=2\n", fixture.renderAndCommit(t))
	after := fixture.engine.executionCounts()
	for _, route := range []string{"route-a", "route-b", "route-c"} {
		assert.Equal(t, baseline["routes/"+route], after["routes/"+route])
	}
}

func TestRenderServiceIncrementalCountSelectorMissingOptionalAndUnauthorized(t *testing.T) {
	optional := incrementalCountSelectorConfig()
	delete(optional.WatchedResources, "policies")
	delete(optional.TemplateSnippets, "100-policies")
	consumer := optional.TemplateSnippets["200-routes"]
	consumer.Incremental.Consumes = nil
	consumer.Incremental.OptionalConsumes = []string{"policies"}
	optional.TemplateSnippets["200-routes"] = consumer
	optional.AbsentIncrementalGroups = map[string]struct{}{"policies": {}}
	optional.HAProxyConfig.Template = `{{ render "200-routes" }}`
	service, routes := countSelectorService(t, optional, nil)
	require.NoError(t, routes.Add(
		incrementalSelectorResource("route", map[string]any{}), []string{"default", "route"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
	assert.Equal(t, "route=0\n", renderAndCommitIncrementalCacheReady(t, service, provider))

	unauthorized := incrementalCountSelectorConfig()
	consumer = unauthorized.TemplateSnippets["200-routes"]
	consumer.Incremental.Consumes = nil
	unauthorized.TemplateSnippets["200-routes"] = consumer
	unauthorizedService, unauthorizedRoutes := countSelectorService(t, unauthorized, nil)
	policies := k8sstore.NewMemoryStore(2)
	require.NoError(t, unauthorizedRoutes.Add(
		incrementalSelectorResource("route", map[string]any{}), []string{"default", "route"},
	))
	_, err := unauthorizedService.Render(t.Context(), stores.NewRealStoreProvider(map[string]stores.Store{
		"policies": policies, "routes": unauthorizedRoutes,
	}), rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, `did not declare publication group "policies"`)
}

func newIncrementalCountSelectorFixture(t *testing.T) *incrementalCountSelectorFixture {
	t.Helper()
	cfg := incrementalCountSelectorConfig()
	service, routes := countSelectorService(t, cfg, nil)
	engine := service.engine.(*dynamicBindingCountingEngine)
	policies := k8sstore.NewMemoryStore(2)
	for _, resource := range []map[string]any{
		incrementalSelectorResource("a-winner", map[string]any{"target": "service-a", "rank": "1", "value": "a1"}),
		incrementalSelectorResource("a-loser", map[string]any{"target": "service-a", "rank": "2", "value": "ignored"}),
		incrementalSelectorResource("b-winner", map[string]any{"target": "service-b", "rank": "1", "value": "b1"}),
	} {
		name := resource["metadata"].(map[string]any)["name"].(string)
		require.NoError(t, policies.Add(resource, []string{"default", name}))
	}
	for _, name := range []string{"route-a", "route-b", "route-c"} {
		require.NoError(t, routes.Add(
			incrementalSelectorResource(name, map[string]any{}), []string{"default", name},
		))
	}
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"policies": policies, "routes": routes})
	return &incrementalCountSelectorFixture{
		config: cfg, service: service, engine: engine, policies: policies, routes: routes, provider: provider,
	}
}

func incrementalCountSelectorConfig() *config.Config {
	cfg := incrementalSelectorServiceConfig(false)
	consumer := cfg.TemplateSnippets["200-routes"]
	consumer.Template = incrementalCountSelectorConsumerTemplate
	cfg.TemplateSnippets["200-routes"] = consumer
	cfg.TemplatingSettings.ExtraContext = map[string]any{"failAfterCount": false}
	cfg.HAProxyConfig.Template = `{{ render "100-policies" }}{{ render "200-routes" }}{%%
if tostring(extraContext | dig("failAfterCount") | fallback(false)) == "true" {
  fail("forced failure after count")
}
%%}`
	return cfg
}

func countSelectorService(
	t *testing.T,
	cfg *config.Config,
	engine *dynamicBindingCountingEngine,
) (*RenderService, *k8sstore.MemoryStore) {
	t.Helper()
	if engine == nil {
		declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
			Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
		})
		baseEngine, err := helpers.NewEngineFromConfigWithOptions(
			cfg, nil, nil, declarations, helpers.EngineOptions{},
		)
		require.NoError(t, err)
		engine = newDynamicBindingCountingEngine(t, baseEngine)
	}
	return NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(),
	}), k8sstore.NewMemoryStore(2)
}

func (f *incrementalCountSelectorFixture) renderAndCommit(t *testing.T) string {
	t.Helper()
	return renderAndCommitIncrementalCacheReady(t, f.service, f.provider)
}

func mustDecodeIncrementalSelectorCount(t *testing.T, value []byte) int {
	t.Helper()
	count, err := decodeIncrementalSelectorCount(value)
	require.NoError(t, err)
	return count
}

func tostringInt(value int) string {
	const digits = "0123456789"
	if value < 10 {
		return string(digits[value])
	}
	return string(digits[value/10]) + string(digits[value%10])
}
