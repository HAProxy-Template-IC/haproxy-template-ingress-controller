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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const customCRDStatusTemplate = `{%%
var route = resources.routes.GetSingle(
  dig_string(item, "", "metadata", "namespace"),
  dig_string(item, "", "metadata", "name"),
)
if route.spec.backend.port == nil {
  fail("Route backend.port is required for this status template")
}
statusPatch(route, map[string]any{
  "rendered": map[string]any{"address": route.spec.backend.address},
  "deployed": map[string]any{"address": route.spec.backend.address, "port": *route.spec.backend.port},
})
%%}`

func newSchemaStatusCustomCRDFixture(t *testing.T) *customCRDChartFixture {
	t.Helper()
	var resourceSchema spec.Schema
	require.NoError(t, json.Unmarshal([]byte(`{
  "type":"object",
  "properties":{
    "apiVersion":{"type":"string"}, "kind":{"type":"string"},
    "metadata":{"type":"object"},
    "spec":{"type":"object","properties":{
      "backend":{"type":"object","properties":{
        "address":{"type":"string"},"port":{"type":"integer"}
      }},
      "requestHeaders":{"type":"array","items":{"type":"object","properties":{
        "name":{"type":"string"},"value":{"type":"string"}
      }}}
    }}
  }
}`), &resourceSchema))
	gvk := schema.GroupVersionKind{Group: "haptic-example.org", Version: "v1", Kind: "Route"}
	types, err := typebootstrap.Bootstrap(t.Context(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{{Name: "routes", GVK: gvk}},
		Fetcher:   schemafetcher.NewMapFetcher(map[schema.GroupVersionKind]*spec.Schema{gvk: &resourceSchema}),
		Logger:    slog.Default(),
	})
	require.NoError(t, err)
	require.Empty(t, types.Errors)
	require.Contains(t, types.Types, "routes")
	require.Equal(t, reflect.Struct, types.Types["routes"].Kind())
	cfg := customCRDChartConfig(t)
	cfg.TemplateSnippets["custom-route-status"] = config.TemplateSnippet{
		Name: "custom-route-status", Requires: []string{"routes"},
		Incremental: &config.IncrementalTemplate{
			Source: "routes", Effects: []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
		},
		Template: customCRDStatusTemplate,
	}
	cfg.HAProxyConfig.Template = `{{ render "custom-route-status" }}` + customCRDChartRoot
	return newCustomCRDChartFixtureWithConfig(t, cfg, types)
}

func schemaStatusCustomRoute(name, uid, revision, address, header string) map[string]any {
	route := customCRDRoute(name, address, 8080, header, "value-"+revision)
	metadata := route["metadata"].(map[string]any)
	metadata["uid"] = uid
	metadata["resourceVersion"] = revision
	return route
}

func assertSchemaStatusCustomResult(
	t *testing.T,
	mode rendercontext.RenderMode,
	got *RenderResult,
	routes ...map[string]any,
) {
	t.Helper()
	oracle := newSchemaStatusCustomCRDFixture(t)
	byName := make(map[string]map[string]any, len(routes))
	for _, route := range routes {
		oracle.addRoute(t, route)
		byName[route["metadata"].(map[string]any)["name"].(string)] = route
	}
	want, err := oracle.service.Render(t.Context(), oracle.provider, mode)
	require.NoError(t, err)
	require.NotNil(t, want.InputTransaction)
	t.Cleanup(want.InputTransaction.Abort)
	assertCustomCRDObservableEqual(t, want, got)
	patches := materializedStatusPatches(t, got)
	require.Len(t, patches, len(routes))
	for i := range patches {
		patch := &patches[i]
		route, found := byName[patch.Name]
		require.True(t, found, "unexpected status target %s", patch.Name)
		metadata := route["metadata"].(map[string]any)
		assert.Equal(t, metadata["uid"], patch.UID)
		assert.Equal(t, metadata["resourceVersion"], patch.ResourceVersion)
		require.Len(t, patch.Variants, 2)
		address := route["spec"].(map[string]any)["backend"].(map[string]any)["address"]
		assert.Equal(t, address, patch.Variants["rendered"]["address"])
		assert.Equal(t, address, patch.Variants["deployed"]["address"])
		assert.EqualValues(t, route["spec"].(map[string]any)["backend"].(map[string]any)["port"], patch.Variants["deployed"]["port"])
	}
}

func TestSchemaDefinedCustomCRDMembershipAndStatusMatchCold(t *testing.T) {
	fixture := newSchemaStatusCustomCRDFixture(t)
	a := schemaStatusCustomRoute("a", "uid-a", "1", "10.0.0.1", "X-Env")
	b := schemaStatusCustomRoute("b", "uid-b", "1", "10.0.0.2", "x-env")
	updated := schemaStatusCustomRoute("a", "uid-a", "2", "10.0.0.3", "X-Env")
	skipped := schemaStatusCustomRoute("a", "uid-a", "4", "10.0.0.4", "X-Env")
	recreated := schemaStatusCustomRoute("a", "replacement-a", "1", "10.0.0.5", "X-Env")
	for _, step := range []struct {
		name    string
		mutate  func(*testing.T)
		current []map[string]any
	}{
		{name: "empty", mutate: func(*testing.T) {}},
		{name: "first", mutate: func(t *testing.T) {
			t.Helper()
			fixture.addRoute(t, a)
		}, current: []map[string]any{a}},
		{name: "second", mutate: func(t *testing.T) {
			t.Helper()
			fixture.addRoute(t, b)
		}, current: []map[string]any{a, b}},
		{name: "update", mutate: func(t *testing.T) {
			t.Helper()
			fixture.updateRoute(t, updated)
		}, current: []map[string]any{updated, b}},
		{name: "skipped-changes", mutate: func(t *testing.T) {
			t.Helper()
			fixture.updateRoute(t, schemaStatusCustomRoute("a", "uid-a", "3", "10.0.0.9", "X-Env"))
			fixture.updateRoute(t, skipped)
		}, current: []map[string]any{skipped, b}},
		{name: "delete-winner", mutate: func(t *testing.T) {
			t.Helper()
			fixture.deleteRoute(t, "a")
		}, current: []map[string]any{b}},
		{name: "recreate-winner", mutate: func(t *testing.T) {
			t.Helper()
			fixture.addRoute(t, recreated)
		}, current: []map[string]any{recreated, b}},
	} {
		t.Run(step.name, func(t *testing.T) {
			step.mutate(t)
			assertSchemaStatusCustomResult(t, rendercontext.RenderModeReconcile, fixture.renderAndCommitCacheReady(t), step.current...)
			assertSchemaStatusCustomResult(t, rendercontext.RenderModeReconcile, fixture.renderAndCommitCacheReady(t), step.current...)
		})
	}

	before := fixture.service.incremental.snapshot
	failedRoute := schemaStatusCustomRoute("a", "replacement-a", "2", "10.0.0.6", "X-Env")
	fixture.updateRoute(t, failedRoute)
	fixture.config.TemplatingSettings.ExtraContext["failAfterCustomRoutes"] = true
	failed, err := fixture.render(t)
	require.ErrorContains(t, err, "forced failure after custom routes")
	require.Nil(t, failed)
	require.Same(t, before, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterCustomRoutes"] = false
	assertSchemaStatusCustomResult(t, rendercontext.RenderModeReconcile, fixture.renderAndCommitCacheReady(t), failedRoute, b)
}

func TestSchemaDefinedCustomCRDAdmissionStatusCannotPublish(t *testing.T) {
	fixture := newSchemaStatusCustomCRDFixture(t)
	current := schemaStatusCustomRoute("a", "uid-a", "1", "10.0.0.1", "X-Env")
	fixture.addRoute(t, current)
	baseline := fixture.renderAndCommitCacheReady(t)
	before := fixture.service.incremental.snapshot
	proposed := schemaStatusCustomRoute("a", "uid-a", "2", "10.0.0.2", "X-Env")
	provider := stores.NewOverlayStoreProvider(fixture.provider, stores.NewValidationContext(
		map[string]*stores.StoreOverlay{"routes": stores.NewStoreOverlayForUpdate(
			&unstructured.Unstructured{Object: proposed},
		)},
	))
	result, err := fixture.service.Render(t.Context(), provider, rendercontext.RenderModeAdmission)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	t.Cleanup(result.InputTransaction.Abort)
	assertSchemaStatusCustomResult(t, rendercontext.RenderModeAdmission, result, proposed)
	result.InputTransaction.Abort()
	require.Same(t, before, fixture.service.incremental.snapshot)
	assertCustomCRDObservableEqual(t, baseline, fixture.renderAndCommitCacheReady(t))
}
