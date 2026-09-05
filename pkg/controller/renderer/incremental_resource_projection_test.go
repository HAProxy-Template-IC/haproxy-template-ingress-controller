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

func TestResourceProjectionTracksExactSelectionWithoutScriggoExecution(t *testing.T) {
	fixture := newResourceProjectionFixture(t, "selected")
	fixture.add(t, "selected", "one")
	fixture.add(t, "unrelated", "other")

	assert.Equal(t, "selected=one\n", fixture.render(t))
	query := fixture.query(t, "selected")
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Empty(t, fixture.engine.executionCounts())

	fixture.add(t, "later", "ignored")
	assert.Equal(t, "selected=one\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)

	fixture.update(t, "selected", "two")
	assert.Equal(t, "selected=two\n", fixture.render(t))
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(query).Executions)

	require.NoError(t, fixture.store.Delete("default", "selected", []string{"default", "selected"}))
	assert.Empty(t, strings.TrimSpace(fixture.render(t)))
	assert.Equal(t, uint64(3), fixture.service.incremental.graph.Counters(query).Executions)

	fixture.add(t, "selected", "two")
	assert.Equal(t, "selected=two\n", fixture.render(t))
	assert.Equal(t, uint64(4), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Empty(t, fixture.engine.executionCounts())
}

func TestResourceProjectionBindingChangeRetiresOldSelection(t *testing.T) {
	fixture := newResourceProjectionFixture(t, "first")
	fixture.add(t, "first", "one")
	fixture.add(t, "second", "two")

	assert.Equal(t, "first=one\n", fixture.render(t))
	oldQuery := fixture.query(t, "first")
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(oldQuery).Executions)

	fixture.selectName("second")
	assert.Equal(t, "second=two\n", fixture.render(t))
	newQuery := fixture.query(t, "second")
	assert.NotEqual(t, oldQuery, newQuery)
	_, oldCached := fixture.service.incremental.graph.Value(oldQuery)
	assert.False(t, oldCached)
	assert.Zero(t, fixture.service.incremental.graph.Counters(oldQuery))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(newQuery).Executions)
	assert.Empty(t, fixture.engine.executionCounts())
}

func TestResourceProjectionRemainsDemandDrivenAlongsideColdCarrierGroup(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"demandProjection": false,
			"projectionBindings": map[string]any{
				"objects": map[string]any{
					"cell": "selected", "key": "selected",
					"keys": []any{"default", "selected"},
				},
			},
		}},
		WatchedResources: map[string]config.WatchedResource{
			"objects": {
				APIVersion: "example.test/v1",
				Resources:  "objects",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"proactive-object": {
				Name:     "proactive-object",
				Requires: []string{"objects"},
				Incremental: &config.IncrementalTemplate{
					Source: "objects", Group: "proactive-objects",
				},
				Template: `proactive={{ item | dig_string("", "metadata", "name") }}
`,
			},
			"selected-object": {
				Name:     "selected-object",
				Requires: []string{"objects"},
				Incremental: &config.IncrementalTemplate{
					Mode:             config.IncrementalModeResourceProjection,
					BindingsTemplate: `{{ toJSON(extraContext["projectionBindings"]) }}`,
					Group:            "selected-objects",
					Effects:          []config.IncrementalEffect{config.IncrementalEffectPublishValue},
				},
				Template: `{{ fail("resource projection entered Scriggo") }}`,
			},
			"selected-consumer": {
				Name:     "selected-consumer",
				Requires: []string{"objects"},
				Incremental: &config.IncrementalTemplate{
					Source: "objects", Group: "selected-consumers",
					Consumes: []string{"selected-objects"},
				},
				Template: `{%- var selected, found = shared.Select("selected-objects", "selected", "selected") -%}
{%- if found %}consumer={{ selected | dig_string("", "spec", "value") }}
{%- end -%}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{- render "proactive-object" -}}
{%- if extraContext | dig("demandProjection") | fallback(false) -%}
{{- render "selected-object" -}}
{{- render "selected-consumer" -}}
{%- for _, value := range incremental_values("selected-objects", "selected") %}
selected={{ value | dig_string("", "spec", "value") }}
{%- end -%}
{%- end -%}`},
	}
	require.NoError(t, config.ValidateTemplateStructure(cfg))
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	_, carrierAvailable := engine.(templating.IncrementalComponentVectorCarrierWavesRenderer)
	require.True(t, carrierAvailable)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(),
	})
	store := k8sstore.NewMemoryStore(2)
	require.NoError(t, store.Add(resourceProjectionObject("selected", "one"), []string{"default", "selected"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"objects": store})
	render := func() string {
		result, renderErr := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
		require.NoError(t, renderErr)
		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		waitForIncrementalCache(t, service)
		return result.HAProxyConfig
	}

	assert.Equal(t, "proactive=selected\n", render())
	proactive := service.incremental.components["proactive-object"]
	proactiveQuery := componentQueryKey(&proactive, "objects", "default", "selected")
	consumer := service.incremental.components["selected-consumer"]
	consumerQuery := componentQueryKey(&consumer, "objects", "default", "selected")
	projection := service.incremental.components["selected-object"]
	props, err := json.Marshal(map[string]any{
		"cell": "selected", "key": "selected", "keys": []any{"default", "selected"},
	})
	require.NoError(t, err)
	projectionQuery, _, _, err := incrementalResourceProjectionQueryKey(&projection, incrementalBinding{
		component: projection.name, source: "objects", props: props,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(proactiveQuery).Executions)
	assert.Zero(t, service.incremental.graph.Counters(consumerQuery).Executions)
	assert.Zero(t, service.incremental.graph.Counters(projectionQuery).Executions)
	service.incremental.mu.Lock()
	assert.True(t, service.incremental.snapshot.groupReady["proactive-objects"])
	assert.False(t, service.incremental.snapshot.groupReady["selected-objects"])
	assert.False(t, service.incremental.snapshot.groupReady["selected-consumers"])
	service.incremental.mu.Unlock()

	cfg.TemplatingSettings.ExtraContext["demandProjection"] = true
	assert.Equal(t, "proactive=selected\nconsumer=one\nselected=one\n", render())
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(proactiveQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(consumerQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(projectionQuery).Executions)
	service.incremental.mu.Lock()
	assert.True(t, service.incremental.snapshot.groupReady["selected-objects"])
	assert.True(t, service.incremental.snapshot.groupReady["selected-consumers"])
	service.incremental.mu.Unlock()

	assertResourceProjectionRemountsWithoutReexecution(
		t, cfg, service, store, render, consumerQuery, projectionQuery,
	)
}

func assertResourceProjectionRemountsWithoutReexecution(
	t *testing.T,
	cfg *config.Config,
	service *RenderService,
	store *k8sstore.MemoryStore,
	render func() string,
	consumerQuery, projectionQuery incremental.QueryKey,
) {
	t.Helper()
	cfg.TemplatingSettings.ExtraContext["demandProjection"] = false
	assert.Equal(t, "proactive=selected\n", render())
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(consumerQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(projectionQuery).Executions)
	cfg.TemplatingSettings.ExtraContext["demandProjection"] = true
	assert.Equal(t, "proactive=selected\nconsumer=one\nselected=one\n", render())
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(consumerQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(projectionQuery).Executions)

	cfg.TemplatingSettings.ExtraContext["demandProjection"] = false
	require.NoError(t, store.Update(
		resourceProjectionObject("selected", "two"), []string{"default", "selected"},
	))
	assert.Equal(t, "proactive=selected\n", render())
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(consumerQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(projectionQuery).Executions)
	cfg.TemplatingSettings.ExtraContext["demandProjection"] = true
	assert.Equal(t, "proactive=selected\nconsumer=two\nselected=two\n", render())
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(consumerQuery).Executions)
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(projectionQuery).Executions)
}

func TestResourceProjectionRetainsExactResultWhileUnmounted(t *testing.T) {
	fixture := newResourceProjectionFixture(t, "selected")
	fixture.add(t, "selected", "one")

	assert.Equal(t, "selected=one\n", fixture.render(t))
	query := fixture.query(t, "selected")
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)

	fixture.config.TemplatingSettings.ExtraContext["mount"] = false
	assert.Empty(t, strings.TrimSpace(fixture.render(t)))
	_, cached := fixture.service.incremental.graph.Value(query)
	assert.True(t, cached)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)

	fixture.config.TemplatingSettings.ExtraContext["mount"] = true
	assert.Equal(t, "selected=one\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)

	fixture.config.TemplatingSettings.ExtraContext["mount"] = false
	assert.Empty(t, strings.TrimSpace(fixture.render(t)))

	fixture.update(t, "selected", "two")
	assert.Empty(t, strings.TrimSpace(fixture.render(t)))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(query).Executions)

	fixture.config.TemplatingSettings.ExtraContext["mount"] = true
	assert.Equal(t, "selected=two\n", fixture.render(t))
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(query).Executions)
	assert.Empty(t, fixture.engine.executionCounts())
}

func TestResourceProjectionRejectsAmbiguousSelection(t *testing.T) {
	fixture := newResourceProjectionFixture(t, "unused")
	fixture.add(t, "first", "one")
	fixture.add(t, "second", "two")
	fixture.config.TemplatingSettings.ExtraContext["bindings"] = map[string]any{
		"objects": map[string]any{
			"cell": "selected",
			"key":  "default",
			"keys": []any{"default"},
		},
	}

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "matched 2 resources; expected at most one")
	assert.Nil(t, result)
	assert.Empty(t, fixture.engine.executionCounts())
}

func TestDecodeIncrementalResourceProjectionFailsClosed(t *testing.T) {
	tests := map[string]string{
		"unknown field":         `{"cell":"selected","key":"k","keys":["k"],"other":true}`,
		"noncanonical":          `{"keys":["k"],"key":"k","cell":"selected"}`,
		"empty keys":            `{"cell":"selected","key":"k","keys":[]}`,
		"empty key member":      `{"cell":"selected","key":"k","keys":[""]}`,
		"empty cell":            `{"cell":"","key":"k","keys":["k"]}`,
		"empty publication key": `{"cell":"selected","key":"","keys":["k"]}`,
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeIncrementalResourceProjection([]byte(encoded))
			require.Error(t, err)
		})
	}
}

type resourceProjectionFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	store    *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newResourceProjectionFixture(t *testing.T, selected string) *resourceProjectionFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane:          testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{}},
		WatchedResources: map[string]config.WatchedResource{
			"objects": {
				APIVersion: "example.test/v1",
				Resources:  "objects",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"selected-object": {
				Name:     "selected-object",
				Requires: []string{"objects"},
				Incremental: &config.IncrementalTemplate{
					Mode:             config.IncrementalModeResourceProjection,
					BindingsTemplate: `{{ toJSON(extraContext["bindings"]) }}`,
					Group:            "selected-objects",
					Effects:          []config.IncrementalEffect{config.IncrementalEffectPublishValue},
				},
				Template: `{{ fail("resource projection entered Scriggo") }}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{%- if tostring(extraContext["mount"]) != "false" -%}
{{- render "selected-object" -}}
{%- for _, value := range incremental_values("selected-objects", "selected") -%}
{{ value | dig_string("", "metadata", "name") }}={{ value | dig_string("", "spec", "value") }}
{%- end -%}
{%- end -%}`},
	}
	fixture := &resourceProjectionFixture{config: cfg}
	fixture.selectName(selected)
	require.NoError(t, config.ValidateTemplateStructure(cfg))
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	fixture.engine = newDynamicBindingCountingEngine(t, baseEngine)
	fixture.service = NewRenderService(&RenderServiceConfig{
		Engine: fixture.engine, Config: cfg, Logger: slog.Default(),
	})
	fixture.store = k8sstore.NewMemoryStore(2)
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{"objects": fixture.store})
	return fixture
}

func (f *resourceProjectionFixture) selectName(name string) {
	f.config.TemplatingSettings.ExtraContext["bindings"] = map[string]any{
		"objects": map[string]any{
			"cell": "selected",
			"key":  name,
			"keys": []any{"default", name},
		},
	}
}

func (f *resourceProjectionFixture) add(t *testing.T, name, value string) {
	t.Helper()
	require.NoError(t, f.store.Add(resourceProjectionObject(name, value), []string{"default", name}))
}

func (f *resourceProjectionFixture) update(t *testing.T, name, value string) {
	t.Helper()
	require.NoError(t, f.store.Update(resourceProjectionObject(name, value), []string{"default", name}))
}

func resourceProjectionObject(name, value string) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Object",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      name,
		},
		"spec": map[string]any{"value": value},
	}
}

func (f *resourceProjectionFixture) render(t *testing.T) string {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result.HAProxyConfig
}

func (f *resourceProjectionFixture) query(t *testing.T, name string) incremental.QueryKey {
	t.Helper()
	props, err := json.Marshal(map[string]any{
		"cell": "selected",
		"key":  name,
		"keys": []any{"default", name},
	})
	require.NoError(t, err)
	component := f.service.incremental.components["selected-object"]
	query, _, _, err := incrementalResourceProjectionQueryKey(&component, incrementalBinding{
		component: component.name,
		source:    "objects",
		props:     props,
	})
	require.NoError(t, err)
	return query
}
