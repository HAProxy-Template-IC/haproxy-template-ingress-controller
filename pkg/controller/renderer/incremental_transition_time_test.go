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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
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

const incrementalTransitionTimeComponent = `{%%
var name = dig_string(item, "", "metadata", "name")
var status = dig_string(item, "True", "spec", "status")
var stamp = transitionTime(dig(item, "status", "conditions"), "Accepted", status)
var target = map[string]any{
  "apiVersion": "example.test/v1", "kind": "Route",
  "metadata": map[string]any{"namespace": "default", "name": name + "-" + props["owner"].(string)},
}
statusPatch(target, map[string]any{
  "rendered": map[string]any{"timestamp": stamp},
})
%%}`

type incrementalTransitionTimeFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	routes   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func TestIncrementalTransitionTimeTransactionCapability(t *testing.T) {
	fixture := newIncrementalTransitionTimeFixture(t)
	fixture.addRoute(t, incrementalTransitionTimeResource("True", ""))
	samples := 0
	fixture.service.incremental.transitionNow = func(context.Context) (string, error) {
		samples++
		return fmt.Sprintf("2026-08-25T12:00:%02dZ", samples), nil
	}

	initial := fixture.renderAndCommit(t)
	assert.Equal(t, "2026-08-25T12:00:01Z", transitionTimePatch(t, initial, "route-a"))
	assert.Equal(t, "2026-08-25T12:00:01Z", transitionTimePatch(t, initial, "route-b"))
	assert.Equal(t, 1, samples)
	initialCounts := fixture.engine.executionCounts()

	warm := fixture.renderAndCommit(t)
	assert.Equal(t, "2026-08-25T12:00:01Z", transitionTimePatch(t, warm, "route-a"))
	assert.Equal(t, initialCounts, fixture.engine.executionCounts())
	assert.Equal(t, 1, samples)

	fixture.updateRoute(t, incrementalTransitionTimeResource(
		"True", "2026-01-02T03:04:05Z"))
	preserved := fixture.renderAndCommit(t)
	assert.Equal(t, "2026-01-02T03:04:05Z", transitionTimePatch(t, preserved, "route-a"))
	assert.Equal(t, "2026-01-02T03:04:05Z", transitionTimePatch(t, preserved, "route-b"))
	assert.Equal(t, 2, samples)
}

func TestIncrementalTransitionTimeAdmissionAndAbortNeverPublish(t *testing.T) {
	fixture := newIncrementalTransitionTimeFixture(t)
	fixture.addRoute(t, incrementalTransitionTimeResource("True", ""))
	samples := 0
	fixture.service.incremental.transitionNow = func(context.Context) (string, error) {
		samples++
		return fmt.Sprintf("2026-08-25T12:00:%02dZ", samples), nil
	}
	baseline := fixture.renderAndCommit(t)
	assert.Equal(t, "2026-08-25T12:00:01Z", transitionTimePatch(t, baseline, "route-a"))

	proposed := incrementalTransitionTimeResource("False", "")
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "route"),
	)
	require.NoError(t, err)
	assert.Equal(t, "2026-08-25T12:00:02Z", transitionTimePatch(t, admission, "route-a"))
	admission.InputTransaction.Abort()
	assert.Equal(t, "2026-08-25T12:00:01Z",
		transitionTimePatch(t, fixture.renderAndCommit(t), "route-a"))
	assert.Equal(t, 2, samples)

	fixture.updateRoute(t, proposed)
	fixture.config.TemplatingSettings.ExtraContext["failAfterStatus"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after status")
	assert.Nil(t, failed)
	fixture.config.TemplatingSettings.ExtraContext["failAfterStatus"] = false
	retried := fixture.renderAndCommit(t)
	assert.Equal(t, "2026-08-25T12:00:04Z", transitionTimePatch(t, retried, "route-a"))
	assert.Equal(t, 4, samples)
}

func TestIncrementalTransitionTimeFailureDoesNotPublishCache(t *testing.T) {
	fixture := newIncrementalTransitionTimeFixture(t)
	fixture.addRoute(t, incrementalTransitionTimeResource("True", ""))
	clockErr := errors.New("clock unavailable")
	fixture.service.incremental.transitionNow = func(context.Context) (string, error) {
		return "", clockErr
	}

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorIs(t, err, clockErr)
	assert.Nil(t, result)
	for _, componentName := range []string{"100-status-a", "200-status-b"} {
		component := fixture.service.incremental.components[componentName]
		query := componentQueryKey(&component, "routes", "default", "route")
		_, cached := fixture.service.incremental.graph.Value(query)
		assert.False(t, cached)
	}

	fixture.service.incremental.transitionNow = func(context.Context) (string, error) {
		return "2026-08-25T12:34:56Z", nil
	}
	recovered := fixture.renderAndCommit(t)
	assert.Equal(t, "2026-08-25T12:34:56Z", transitionTimePatch(t, recovered, "route-a"))

	cancelled, cancel := context.WithCancelCause(t.Context())
	cancel(clockErr)
	_, err = sampleIncrementalTransitionTime(cancelled)
	require.ErrorIs(t, err, clockErr)
}

func TestIncrementalTransitionTimeIsUnavailableToNonStatusComponents(t *testing.T) {
	cfg := incrementalTransitionTimeConfig()
	snippet := cfg.TemplateSnippets["100-status-a"]
	snippet.Incremental.Effects = nil
	cfg.TemplateSnippets = map[string]config.TemplateSnippet{"100-status-a": snippet}
	cfg.HAProxyConfig.Template = `{{ render "100-status-a" }}`
	engine := newTransitionTimeEngine(t, cfg)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(incrementalTransitionTimeResource("True", ""), []string{"default", "route"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
	samples := 0
	service.incremental.transitionNow = func(context.Context) (string, error) {
		samples++
		return "2026-08-25T12:34:56Z", nil
	}

	_, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "incremental transition time is unavailable")
	assert.Zero(t, samples)
}

func TestPinnedColdIncrementalTransitionTimeReusesWarmValue(t *testing.T) {
	samples := 0
	state := &incrementalRenderState{transitionNow: func(context.Context) (string, error) {
		samples++
		return "2026-08-25T12:34:56Z", nil
	}}
	warm := &incrementalRenderSession{state: state}
	warmValue, err := warm.incrementalTransitionTime(t.Context())
	require.NoError(t, err)
	cold := &coldIncrementalRenderer{state: state, transitionTime: warm.transitionTime}
	coldValue, err := cold.incrementalTransitionTime(t.Context())
	require.NoError(t, err)
	assert.Equal(t, warmValue, coldValue)
	assert.Equal(t, 1, samples)
}

func newIncrementalTransitionTimeFixture(t *testing.T) *incrementalTransitionTimeFixture {
	t.Helper()
	cfg := incrementalTransitionTimeConfig()
	engine := newTransitionTimeEngine(t, cfg)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	return &incrementalTransitionTimeFixture{
		config: cfg, service: service, engine: engine, routes: routes,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	}
}

func incrementalTransitionTimeConfig() *config.Config {
	snippets := map[string]config.TemplateSnippet{}
	for _, owner := range []string{"a", "b"} {
		name := map[string]string{"a": "100-status-a", "b": "200-status-b"}[owner]
		snippets[name] = config.TemplateSnippet{
			Name: name, Requires: []string{"routes"}, Template: incrementalTransitionTimeComponent,
			Incremental: &config.IncrementalTemplate{
				BindingsTemplate: `{{- toJSON(map[string]any{"routes": map[string]any{"owner": "` + owner + `"}}) -}}`,
				Group:            "transition-status",
				Effects:          []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
			},
		}
	}
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"failAfterStatus": false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: snippets,
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "100-status-a" }}{{ render "200-status-b" }}
{%- if tostring(extraContext | dig("failAfterStatus") | fallback(false)) == "true" -%}
{{ fail("forced failure after status") }}
{%- end -%}`},
	}
}

func newTransitionTimeEngine(tb testing.TB, cfg *config.Config) *dynamicBindingCountingEngine {
	tb.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(tb, err)
	return newDynamicBindingCountingEngine(tb, baseEngine)
}

func incrementalTransitionTimeResource(status, existingTimestamp string) map[string]any {
	const name = "route"
	conditions := []any{}
	if existingTimestamp != "" {
		conditions = append(conditions, map[string]any{
			"type": "Accepted", "status": status, "lastTransitionTime": existingTimestamp,
		})
	}
	return map[string]any{
		"apiVersion": "example.test/v1", "kind": "Route",
		"metadata": map[string]any{"namespace": "default", "name": name},
		"spec":     map[string]any{"status": status},
		"status":   map[string]any{"conditions": conditions},
	}
}

func (f *incrementalTransitionTimeFixture) addRoute(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.routes.Add(resource, []string{"default", name}))
}

func (f *incrementalTransitionTimeFixture) updateRoute(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.routes.Update(resource, []string{"default", name}))
}

func (f *incrementalTransitionTimeFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func transitionTimePatch(t *testing.T, result *RenderResult, name string) string {
	t.Helper()
	patches := materializedStatusPatches(t, result)
	for index := range patches {
		if patches[index].Name == name {
			return patches[index].Variants["rendered"]["timestamp"].(string)
		}
	}
	t.Fatalf("status patch %q is missing", name)
	return ""
}
