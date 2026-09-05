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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const incrementalStatusPatchOwnerA = `{%%
if dig_string(item, "", "metadata", "name") == "a" {
  var status = map[string]any{"owner": dig_string(item, "", "spec", "value")}
  var target = map[string]any{
    "apiVersion": "example.test/v1", "kind": "Route",
    "metadata": map[string]any{
      "namespace": "default", "name": "shared", "uid": "uid-shared", "resourceVersion": "rv-shared",
    },
  }
  statusPatch(target, map[string]any{"rendered": status})
  status["owner"] = "mutated-after-registration"
}
%%}`

const incrementalStatusPatchOwnerB = `{%%
if dig_string(item, "", "metadata", "name") == "b" {
  var target = map[string]any{
    "apiVersion": "example.test/v1", "kind": "Route",
    "metadata": map[string]any{
      "namespace": "default", "name": "shared", "uid": "uid-shared", "resourceVersion": "rv-shared",
    },
  }
  statusPatch(target, map[string]any{
    "rendered": map[string]any{"owner": dig_string(item, "", "spec", "value")},
  })
}
%%}`

type statusPatchServiceFixture struct {
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	routes   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newStatusPatchServiceFixture(t *testing.T) *statusPatchServiceFixture {
	t.Helper()
	cfg := incrementalStatusPatchServiceConfig()
	engine := newStatusPatchServiceEngine(t, cfg)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(statusPatchResource("a", "owner-a"), []string{"default", "a"}))
	require.NoError(t, routes.Add(statusPatchResource("b", "owner-b"), []string{"default", "b"}))
	return &statusPatchServiceFixture{
		service: service,
		engine:  engine,
		routes:  routes,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{
			"routes": routes,
		}),
	}
}

func incrementalStatusPatchServiceConfig() *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-owner-a": {
				Name: "100-owner-a", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "status-owners",
					Effects: []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
				},
				Template: incrementalStatusPatchOwnerA,
			},
			"200-owner-b": {
				Name: "200-owner-b", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "status-owners",
					Effects: []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
				},
				Template: incrementalStatusPatchOwnerB,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `global
{{ render "100-owner-a" }}{{ render "200-owner-b" }}`},
	}
}

func newStatusPatchServiceEngine(tb testing.TB, cfg *config.Config) *dynamicBindingCountingEngine {
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

func TestRenderServiceStatusPatchSameGroupOwnersLifecycle(t *testing.T) {
	fixture := newStatusPatchServiceFixture(t)

	initial := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, initial, "owner-b")
	initialPatches := materializedStatusPatches(t, initial)
	assert.Equal(t, "uid-shared", initialPatches[0].UID)
	assert.Equal(t, "rv-shared", initialPatches[0].ResourceVersion)
	assert.Equal(t, helpers.IncrementalEntryPointName("100-owner-a"), initialPatches[0].SourceTemplate)
	tempComponent76 := fixture.service.incremental.components["100-owner-a"]
	aQuery := componentQueryKey(
		&tempComponent76, "routes", "default", "a",
	)
	tempComponent77 := fixture.service.incremental.components["200-owner-b"]
	bQuery := componentQueryKey(
		&tempComponent77, "routes", "default", "b",
	)
	aCounters := fixture.service.incremental.graph.Counters(aQuery)
	bCounters := fixture.service.incremental.graph.Counters(bQuery)
	initialPatches[0].Variants["rendered"]["owner"] = "mutated-result"

	unchanged := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, unchanged, "owner-b")
	assert.Equal(t, aCounters, fixture.service.incremental.graph.Counters(aQuery))
	assert.Equal(t, bCounters, fixture.service.incremental.graph.Counters(bQuery))

	require.NoError(t, fixture.routes.Update(statusPatchResource("b", "aborted"), []string{"default", "b"}))
	aborted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assertStatusPatchOwner(t, aborted, "aborted")
	aborted.InputTransaction.Abort()
	assert.Equal(t, bCounters, fixture.service.incremental.graph.Counters(bQuery))

	retried := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, retried, "aborted")
	assert.Equal(t, bCounters.Executions+1, fixture.service.incremental.graph.Counters(bQuery).Executions)

	require.NoError(t, fixture.routes.Delete("default", "b", []string{"default", "b"}))
	promoted := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, promoted, "owner-a")
	promotedPatches := materializedStatusPatches(t, promoted)
	assert.NotEqual(t, "mutated-after-registration", promotedPatches[0].Variants["rendered"]["owner"])
}

func TestRenderServiceStatusPatchPlanCommitAbortAndRecurrence(t *testing.T) {
	fixture := newStatusPatchServiceFixture(t)
	initial := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, initial, "owner-b")
	planA1 := fixture.service.incremental.snapshot.statusPlan
	require.NoError(t, planA1.ValidateAuthentication())

	unchanged := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, unchanged, "owner-b")
	assert.Same(t, planA1, fixture.service.incremental.snapshot.statusPlan)
	assert.Same(t, initial.StatusPatchSnapshot, unchanged.StatusPatchSnapshot)

	require.NoError(t, fixture.routes.Update(statusPatchResource("b", "owner-c"), []string{"default", "b"}))
	aborted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assertStatusPatchOwner(t, aborted, "owner-c")
	aborted.InputTransaction.Abort()
	assert.Same(t, planA1, fixture.service.incremental.snapshot.statusPlan)

	committedB := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, committedB, "owner-c")
	planB := fixture.service.incremental.snapshot.statusPlan
	assert.NotSame(t, planA1, planB)
	require.NoError(t, planB.ValidateAuthentication())

	require.NoError(t, fixture.routes.Update(statusPatchResource("b", "owner-b"), []string{"default", "b"}))
	committedA2 := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, committedA2, "owner-b")
	planA2 := fixture.service.incremental.snapshot.statusPlan
	assert.NotSame(t, planA1, planA2)
	assert.NotSame(t, planB, planA2)
	assert.NotSame(t, initial.StatusPatchSnapshot, committedA2.StatusPatchSnapshot)
}

func TestRenderServiceStatusPatchPlanFailsClosedAfterStateSubstitution(t *testing.T) {
	fixture := newStatusPatchServiceFixture(t)
	fixture.renderAndCommitCacheReady(t, fixture.provider)
	snapshot := fixture.service.incremental.snapshot
	original := snapshot.statusPlan
	snapshot.statusPlan = templating.NewStatusPatchProjectionPlan()
	t.Cleanup(func() { snapshot.statusPlan = original })

	_, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "incremental state snapshot status plan changed")
}

func TestRenderServiceStatusPatchAdmissionDoesNotPublish(t *testing.T) {
	fixture := newStatusPatchServiceFixture(t)
	committed := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, committed, "owner-b")
	tempComponent78 := fixture.service.incremental.components["200-owner-b"]
	bQuery := componentQueryKey(
		&tempComponent78, "routes", "default", "b",
	)
	committedCounters := fixture.service.incremental.graph.Counters(bQuery)
	committedPlan := fixture.service.incremental.snapshot.statusPlan

	proposed := statusPatchResource("b", "admission")
	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission := fixture.renderAndCommitMode(
		t, admissionProvider, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "b"),
	)
	assertStatusPatchOwner(t, admission, "admission")
	assert.Equal(t, committedCounters, fixture.service.incremental.graph.Counters(bQuery))
	assert.Same(t, committedPlan, fixture.service.incremental.snapshot.statusPlan)

	reconcile := fixture.renderAndCommitCacheReady(t, fixture.provider)
	assertStatusPatchOwner(t, reconcile, "owner-b")
	assert.Equal(t, committedCounters, fixture.service.incremental.graph.Counters(bQuery))
}

func TestRenderServiceStatusPatchColdAndWarmUseOneGlobalOrder(t *testing.T) {
	cfg := crossRootStatusPatchConfig(false)
	engine := newStatusPatchServiceEngine(t, cfg)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(statusPatchResource("a", "value"), []string{"default", "a"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})

	warm, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, warm.InputTransaction.Commit(t.Context()))
	cold, err := renderServiceStaticCold(t, service, provider)
	require.NoError(t, err)
	cold.InputTransaction.Abort()

	warmPatches := materializedStatusPatches(t, warm)
	coldPatches := materializedStatusPatches(t, cold)
	require.Len(t, warmPatches, 1)
	require.Len(t, coldPatches, 1)
	assert.Equal(t, coldPatches, warmPatches)
	assert.Equal(t, "main", warmPatches[0].Variants["rendered"]["root"])
	assert.Equal(t, "file", warmPatches[0].Variants["deployed"]["root"])
	assert.Equal(t, helpers.IncrementalEntryPointName("z-main"), warmPatches[0].SourceTemplate)
}

func TestRenderServiceStatusPatchRejectsCrossGroupPhaseConflictAcrossRoots(t *testing.T) {
	cfg := crossRootStatusPatchConfig(true)
	engine := newStatusPatchServiceEngine(t, cfg)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(statusPatchResource("a", "value"), []string{"default", "a"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})

	_, warmErr := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, warmErr, `phase "rendered" has conflicting groups "a-group" and "z-group"`)
	_, coldErr := renderServiceStaticCold(t, service, provider)
	require.ErrorContains(t, coldErr, `phase "rendered" has conflicting groups "a-group" and "z-group"`)
}

func crossRootStatusPatchConfig(conflict bool) *config.Config {
	filePhase := "deployed"
	if conflict {
		filePhase = "rendered"
	}
	mainTemplate := `{%%
var target = map[string]any{
  "apiVersion": "example.test/v1", "kind": "Route",
  "metadata": map[string]any{
    "namespace": "default", "name": "shared", "uid": "uid-shared", "resourceVersion": "rv-shared",
  },
}
statusPatch(target, map[string]any{
  "rendered": map[string]any{"root": "main"},
})
%%}`
	fileTemplate := `{%%
var target = map[string]any{
  "apiVersion": "example.test/v1", "kind": "Route",
  "metadata": map[string]any{
    "namespace": "default", "name": "shared", "uid": "uid-shared", "resourceVersion": "rv-shared",
  },
}
statusPatch(target, map[string]any{
  "` + filePhase + `": map[string]any{"root": "file"},
})
%%}`
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"z-main": {
				Name: "z-main", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "a-group",
					Effects: []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
				},
				Template: mainTemplate,
			},
			"a-file": {
				Name: "a-file", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{
					Source: "routes", Group: "z-group",
					Effects: []config.IncrementalEffect{config.IncrementalEffectStatusPatch},
				},
				Template: fileTemplate,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `global
{{ render "z-main" }}`},
		Files: map[string]config.GeneralFile{
			"000-status.txt": {Template: `{{ render "a-file" }}`},
		},
	}
}

func (f *statusPatchServiceFixture) renderAndCommitCacheReady(
	t *testing.T,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result := f.renderAndCommitMode(t, provider, rendercontext.RenderModeReconcile)
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *statusPatchServiceFixture) renderAndCommitMode(
	t *testing.T,
	provider stores.StoreProvider,
	mode rendercontext.RenderMode,
	opts ...rendercontext.Option,
) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), provider, mode, opts...)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	return result
}

func assertStatusPatchOwner(t *testing.T, result *RenderResult, owner string) {
	t.Helper()
	patches := materializedStatusPatches(t, result)
	require.Len(t, patches, 1)
	assert.Equal(t, owner, patches[0].Variants["rendered"]["owner"])
}

func statusPatchResource(name, value string) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"namespace":       "default",
			"name":            name,
			"uid":             "uid-" + name,
			"resourceVersion": "rv-" + value,
		},
		"spec": map[string]any{"value": value},
	}
}
