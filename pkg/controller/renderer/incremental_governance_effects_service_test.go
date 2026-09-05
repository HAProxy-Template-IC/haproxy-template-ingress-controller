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
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const governanceEffectsPlanner = `{%%
var bindings = map[string]any{}
var governance = extraContext | dig("governance") | fallback(map[string]any{})
if tostring(governance | dig("enabled") | fallback(false)) == "true" {
  bindings["routes"] = map[string]any{
    "annotation": governance | dig("annotation") | fallback(""),
    "emitEvent": governance | dig("emitEvent") | fallback(false),
    "eventMessage": governance | dig("eventMessage") | fallback(""),
  }
}
show toJSON(bindings)
%%}`

const governanceEffectsComponent = `{%%
var current = deriveResource(source, item, "metadata.annotations.governed", props | dig("annotation") | fallback(""))
if tostring(props | dig("emitEvent") | fallback(false)) == "true" {
  recordEvent(current, "GovernanceApplied", props | dig_string("", "eventMessage"))
  recordEvent(current, "GovernanceApplied", props | dig_string("", "eventMessage"))
}
%%}`

const governanceEffectsConsumer = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.routes.GetSingle(namespace, name)
show name + "=" + (current | dig_string("<missing>", "metadata", "annotations", "governed")) + ":" + (current | dig_string("", "spec", "version")) + "\n"
%%}`

type governanceEffectsFixture struct {
	config              *config.Config
	service             *RenderService
	routes              *k8sstore.MemoryStore
	provider            stores.StoreProvider
	governanceQuery     incremental.QueryKey
	consumerQuery       incremental.QueryKey
	projectionQuery     incremental.QueryKey
	enabled             bool
	annotation          string
	emitEvent           bool
	eventMessage        string
	failAfterGovernance bool
}

func newGovernanceEffectsFixture(t *testing.T) *governanceEffectsFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"00-governance": {
				Name:     "00-governance",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					BindingsTemplate: governanceEffectsPlanner,
					Effects: []config.IncrementalEffect{
						config.IncrementalEffectDeriveResource,
						config.IncrementalEffectRecordEvent,
					},
				},
				Template: governanceEffectsComponent,
			},
			"10-consumer": {
				Name:     "10-consumer",
				Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes",
				},
				Template: governanceEffectsConsumer,
			},
		},
		HAProxyConfig: config.HAProxyConfig{
			Template: `{{ render "00-governance" }}{%%
if tostring(extraContext | dig("failAfterGovernance") | fallback(false)) == "true" {
  fail("forced failure after governance")
}
%%}{{ render "10-consumer" }}`,
		},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine,
		Config: cfg,
		Logger: slog.Default(),
	})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(governanceEffectsRoute("v1"), []string{"default", "route"}))
	fixture := &governanceEffectsFixture{
		config:       cfg,
		service:      service,
		routes:       routes,
		provider:     stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
		enabled:      true,
		annotation:   "alpha",
		emitEvent:    true,
		eventMessage: "first",
	}
	fixture.applySettings()
	governance := service.incremental.components["00-governance"]
	consumer := service.incremental.components["10-consumer"]
	fixture.governanceQuery = componentQueryKey(&governance, "routes", "default", "route")
	fixture.consumerQuery = componentQueryKey(&consumer, "routes", "default", "route")
	fixture.projectionQuery = derivedProjectionQueryKey("routes", "default", "route")
	return fixture
}

func governanceEffectsRoute(version string) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Example",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        "route",
			"annotations": map[string]any{},
		},
		"spec": map[string]any{"version": version},
	}
}

func (f *governanceEffectsFixture) applySettings() {
	f.config.TemplatingSettings.ExtraContext = map[string]any{
		"governance": map[string]any{
			"enabled":      f.enabled,
			"annotation":   f.annotation,
			"emitEvent":    f.emitEvent,
			"eventMessage": f.eventMessage,
		},
		"failAfterGovernance": f.failAfterGovernance,
	}
}

func (f *governanceEffectsFixture) render(t *testing.T) (*RenderResult, error) {
	t.Helper()
	f.applySettings()
	return f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
}

func (f *governanceEffectsFixture) renderAndCommitCacheReady(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.render(t)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *governanceEffectsFixture) rawStore(t *testing.T) []byte {
	t.Helper()
	items, err := f.routes.List()
	require.NoError(t, err)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	return encoded
}

func (f *governanceEffectsFixture) counters(key incremental.QueryKey) incremental.NodeCounters {
	return f.service.incremental.graph.Counters(key)
}

func assertGovernanceEvent(t *testing.T, events []templating.RenderedEvent, message string) {
	t.Helper()
	require.Len(t, events, 1)
	assert.Equal(t, templating.RenderedEvent{
		Namespace:  "default",
		Name:       "route",
		APIVersion: "example.test/v1",
		Kind:       "Example",
		Type:       "Warning",
		Reason:     "GovernanceApplied",
		Message:    message,
	}, events[0])
}

func TestRenderServiceGovernanceEffectsLifecycle(t *testing.T) {
	fixture := newGovernanceEffectsFixture(t)
	original := fixture.rawStore(t)

	first := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=alpha:v1\n", first.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, first), "first")
	assert.Equal(t, original, fixture.rawStore(t))
	assert.Equal(t, uint64(1), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(1), fixture.counters(fixture.consumerQuery).Executions)

	warm := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, warm), "first")
	assert.Equal(t, original, fixture.rawStore(t))
	assert.Equal(t, uint64(1), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(1), fixture.counters(fixture.consumerQuery).Executions)

	require.NoError(t, fixture.routes.Update(governanceEffectsRoute("v2"), []string{"default", "route"}))
	updatedRaw := fixture.rawStore(t)
	updated := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=alpha:v2\n", updated.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, updated), "first")
	assert.Equal(t, updatedRaw, fixture.rawStore(t))
	assert.Equal(t, uint64(2), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(2), fixture.counters(fixture.consumerQuery).Executions)

	fixture.annotation = "beta"
	propsChanged := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=beta:v2\n", propsChanged.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, propsChanged), "first")
	assert.Equal(t, updatedRaw, fixture.rawStore(t))
	assert.Equal(t, uint64(3), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(3), fixture.counters(fixture.consumerQuery).Executions)
}

func TestRenderServiceGovernanceEffectsSourceAndBindingRemoval(t *testing.T) {
	fixture := newGovernanceEffectsFixture(t)
	require.Equal(t, "route=alpha:v1\n", fixture.renderAndCommitCacheReady(t).HAProxyConfig)

	require.NoError(t, fixture.routes.Delete("default", "route", []string{"default", "route"}))
	removed := fixture.renderAndCommitCacheReady(t)
	assert.Empty(t, strings.TrimSpace(removed.HAProxyConfig))
	assert.Empty(t, requireRenderEvents(t, removed))
	assert.Equal(t, []byte("null"), fixture.rawStore(t))

	require.NoError(t, fixture.routes.Add(governanceEffectsRoute("v2"), []string{"default", "route"}))
	readded := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=alpha:v2\n", readded.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, readded), "first")
	raw := fixture.rawStore(t)

	fixture.enabled = false
	unbound := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=<missing>:v2\n", unbound.HAProxyConfig)
	assert.Empty(t, requireRenderEvents(t, unbound))
	assert.Equal(t, raw, fixture.rawStore(t))
}

func TestRenderServiceGovernanceEffectsTransactionsDoNotPoisonCache(t *testing.T) {
	fixture := newGovernanceEffectsFixture(t)
	require.Equal(t, "route=alpha:v1\n", fixture.renderAndCommitCacheReady(t).HAProxyConfig)

	fixture.annotation = "beta"
	aborted, err := fixture.render(t)
	require.NoError(t, err)
	require.Equal(t, "route=beta:v1\n", aborted.HAProxyConfig)
	aborted.InputTransaction.Abort()
	assert.Equal(t, uint64(1), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(1), fixture.counters(fixture.consumerQuery).Executions)

	committed := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=beta:v1\n", committed.HAProxyConfig)
	assert.Equal(t, uint64(2), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(2), fixture.counters(fixture.consumerQuery).Executions)

	fixture.annotation = "gamma"
	fixture.failAfterGovernance = true
	failed, err := fixture.render(t)
	require.ErrorContains(t, err, "forced failure after governance")
	assert.Nil(t, failed)
	assert.Equal(t, uint64(2), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(2), fixture.counters(fixture.consumerQuery).Executions)

	fixture.failAfterGovernance = false
	afterFailure := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, "route=gamma:v1\n", afterFailure.HAProxyConfig)
	assert.Equal(t, uint64(3), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(3), fixture.counters(fixture.consumerQuery).Executions)
}

func TestRenderServiceGovernanceEventOnlyChangeBackdatesProjection(t *testing.T) {
	fixture := newGovernanceEffectsFixture(t)
	first := fixture.renderAndCommitCacheReady(t)
	require.Equal(t, "route=alpha:v1\n", first.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, first), "first")
	assert.Equal(t, uint64(1), fixture.counters(fixture.projectionQuery).Executions)

	fixture.eventMessage = "second"
	changed := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, first.HAProxyConfig, changed.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, changed), "second")
	assert.Equal(t, uint64(2), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(2), fixture.counters(fixture.projectionQuery).Executions)
	assert.Equal(t, uint64(1), fixture.counters(fixture.projectionQuery).Backdates)
	assert.Equal(t, uint64(1), fixture.counters(fixture.consumerQuery).Executions)

	warm := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, changed.HAProxyConfig, warm.HAProxyConfig)
	assertGovernanceEvent(t, requireRenderEvents(t, warm), "second")
	assert.Equal(t, uint64(2), fixture.counters(fixture.governanceQuery).Executions)
	assert.Equal(t, uint64(2), fixture.counters(fixture.projectionQuery).Executions)
	assert.Equal(t, uint64(1), fixture.counters(fixture.consumerQuery).Executions)
}
