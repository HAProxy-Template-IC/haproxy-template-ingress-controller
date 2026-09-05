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

const rankedFragmentProducerTemplate = `{%%
var key = item | dig_string("", "spec", "key")
var rank = item | dig_string("", "spec", "rank")
var value = item | dig_string("", "spec", "value")
show shared.PublishRanked("lines", key, rank, value + "\n")
%%}`

type rankedFragmentServiceFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	routes   *k8sstore.MemoryStore
	claims   *k8sstore.MemoryStore
	others   *k8sstore.MemoryStore
	provider stores.StoreProvider
}

func newRankedFragmentServiceFixture(t *testing.T) *rankedFragmentServiceFixture {
	t.Helper()
	cfg := rankedFragmentServiceConfig(false)
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	routes := k8sstore.NewMemoryStore(2)
	claims := k8sstore.NewMemoryStore(2)
	others := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(rankedFragmentResource("a", "shared", "200", "route-a"),
		[]string{"default", "a"}))
	require.NoError(t, routes.Add(rankedFragmentResource("b", "b", "100", "route-b"),
		[]string{"default", "b"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": routes, "claims": claims, "others": others,
	})
	return &rankedFragmentServiceFixture{
		config: cfg,
		service: NewRenderService(&RenderServiceConfig{
			Engine: engine, Config: cfg, Logger: slog.Default(),
		}),
		engine: engine, routes: routes, claims: claims, others: others, provider: provider,
	}
}

func rankedFragmentServiceConfig(readBeforeCalls bool) *config.Config {
	root := `{{ render "100-routes" }}{{ render "200-claims" }}{{ incremental_ranked_fragments("ordered", "lines") }}`
	if readBeforeCalls {
		root = `{{ render "100-routes" }}{{ incremental_ranked_fragments("ordered", "lines") }}{{ render "200-claims" }}`
	}
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"claims": {
				APIVersion: "example.test/v1", Resources: "claims",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"others": {
				APIVersion: "example.test/v1", Resources: "others",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-routes": rankedFragmentSnippet("100-routes", "routes"),
			"200-claims": rankedFragmentSnippet("200-claims", "claims"),
		},
		HAProxyConfig: config.HAProxyConfig{Template: root},
	}
}

func rankedFragmentSnippet(name, source string) config.TemplateSnippet {
	return config.TemplateSnippet{
		Name: name, Requires: []string{source}, Template: rankedFragmentProducerTemplate,
		Incremental: &config.IncrementalTemplate{
			Source: source, Group: "ordered",
			Effects: []config.IncrementalEffect{config.IncrementalEffectPublishValue},
		},
	}
}

func rankedFragmentResource(name, key, rank, value string) map[string]any {
	return incrementalTestResource("default", name, map[string]any{
		"key": key, "rank": rank, "value": value,
	})
}

func (f *rankedFragmentServiceFixture) renderAndCommit(t *testing.T) string {
	t.Helper()
	return renderAndCommitIncrementalCacheReady(t, f.service, f.provider)
}

func TestRenderServiceIncrementalRankedFragmentsLifecycle(t *testing.T) {
	fixture := newRankedFragmentServiceFixture(t)
	assert.Equal(t, "route-b\nroute-a\n", fixture.renderAndCommit(t))
	baseline := fixture.engine.executionCounts()
	assert.Equal(t, "route-b\nroute-a\n", fixture.renderAndCommit(t))
	assert.Equal(t, baseline, fixture.engine.executionCounts())

	require.NoError(t, fixture.routes.Update(
		rankedFragmentResource("a", "shared", "200", "route-a-2"), []string{"default", "a"},
	))
	assert.Equal(t, "route-b\nroute-a-2\n", fixture.renderAndCommit(t))
	afterChange := fixture.engine.executionCounts()
	assert.Equal(t, baseline["routes/a"]+1, afterChange["routes/a"])
	assert.Equal(t, baseline["routes/b"], afterChange["routes/b"])

	require.NoError(t, fixture.others.Add(
		rankedFragmentResource("other", "other", "000", "ignored"), []string{"default", "other"},
	))
	assert.Equal(t, "route-b\nroute-a-2\n", fixture.renderAndCommit(t))
	assert.Equal(t, afterChange, fixture.engine.executionCounts())

	require.NoError(t, fixture.claims.Add(
		rankedFragmentResource("winner", "shared", "050", "claim"), []string{"default", "winner"},
	))
	assert.Equal(t, "claim\nroute-b\n", fixture.renderAndCommit(t))
	afterCollision := fixture.engine.executionCounts()
	assert.Equal(t, afterChange["routes/a"], afterCollision["routes/a"])

	require.NoError(t, fixture.claims.Delete("default", "winner", []string{"default", "winner"}))
	assert.Equal(t, "route-b\nroute-a-2\n", fixture.renderAndCommit(t))
	afterPromotion := fixture.engine.executionCounts()
	assert.Equal(t, afterCollision["routes/a"], afterPromotion["routes/a"])
}

func TestRenderServiceIncrementalRankedFragmentsAbortAndAdmissionStayIsolated(t *testing.T) {
	fixture := newRankedFragmentServiceFixture(t)
	assert.Equal(t, "route-b\nroute-a\n", fixture.renderAndCommit(t))
	committed := fixture.service.incremental.snapshot

	require.NoError(t, fixture.routes.Update(
		rankedFragmentResource("a", "shared", "050", "aborted"), []string{"default", "a"},
	))
	aborted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "aborted\nroute-b\n", aborted.HAProxyConfig)
	aborted.InputTransaction.Abort()
	assert.Same(t, committed, fixture.service.incremental.snapshot)
	assert.Equal(t, "aborted\nroute-b\n", fixture.renderAndCommit(t))

	committed = fixture.service.incremental.snapshot
	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{
				Object: rankedFragmentResource("a", "shared", "001", "proposed"),
			}),
		}),
	)
	admission, err := fixture.service.Render(
		t.Context(), admissionProvider, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "a"),
	)
	require.NoError(t, err)
	assert.Equal(t, "proposed\nroute-b\n", admission.HAProxyConfig)
	require.NoError(t, admission.InputTransaction.Commit(t.Context()))
	assert.Same(t, committed, fixture.service.incremental.snapshot)
	assert.Equal(t, "aborted\nroute-b\n", fixture.renderAndCommit(t))
}

func TestRenderServiceIncrementalRankedFragmentsStaticColdMatchesWarm(t *testing.T) {
	fixture := newRankedFragmentServiceFixture(t)
	warm := fixture.renderAndCommit(t)
	_, cold, err := renderStaticColdIncremental(t, fixture.config, fixture.engine, fixture.provider)
	require.NoError(t, err)
	assert.Equal(t, warm, cold)
}

func TestRenderServiceIncrementalRankedFragmentsRequiresCanonicalCalls(t *testing.T) {
	cfg := rankedFragmentServiceConfig(true)
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": k8sstore.NewMemoryStore(2),
		"claims": k8sstore.NewMemoryStore(2),
		"others": k8sstore.NewMemoryStore(2),
	})

	_, err = service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, `incremental publication group "ordered" must complete its canonical root call before selection`)
}

var _ interface {
	IncrementalRankedFragments(context.Context, string, string) (string, error)
	IncrementalRankedFragmentsJoin(context.Context, string, string, string) (string, error)
} = (*incrementalRenderSession)(nil)
