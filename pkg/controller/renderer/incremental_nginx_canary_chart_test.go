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
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	nginxCanaryProducer = "features-080-nginx-ingress-canary-coexist"
	nginxCanaryConsumer = "test-ingress-route-colocation-consumer"
)

type nginxCanaryChartLibrary struct {
	TemplateSnippets map[string]nginxCanaryChartSnippet `yaml:"templateSnippets"`
}

type nginxCanaryChartSnippet struct {
	Template    string                       `yaml:"template"`
	Requires    []string                     `yaml:"requires"`
	Incremental *nginxCanaryChartIncremental `yaml:"incremental"`
}

type nginxCanaryChartIncremental struct {
	Source  string                     `yaml:"source"`
	Group   string                     `yaml:"group"`
	Effects []config.IncrementalEffect `yaml:"effects"`
}

type nginxCanaryChartFixture struct {
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestNginxCanaryChartReusesCleanIngressesAndRemovesStaleMembership(t *testing.T) {
	fixture := newNginxCanaryChartFixture(t)
	fixture.add(t, nginxCanaryChartIngress("main", false, "v1"))
	fixture.add(t, nginxCanaryChartIngress("canary", true, "v1"))
	component := fixture.service.incremental.components[nginxCanaryProducer]
	consumer := fixture.service.incremental.components[nginxCanaryConsumer]
	mainQuery := componentQueryKey(&component, "ingresses", "default", "main")
	canaryQuery := componentQueryKey(&component, "ingresses", "default", "canary")
	mainConsumerQuery := componentQueryKey(&consumer, "ingresses", "default", "main")
	canaryConsumerQuery := componentQueryKey(&consumer, "ingresses", "default", "canary")

	assert.Equal(t, "[default/canary]", fixture.renderAndCommit(t))
	assert.Equal(t, map[string]int{"ingresses/canary": 2, "ingresses/main": 2}, fixture.engine.executionCounts())

	assert.Equal(t, "[default/canary]", fixture.renderAndCommit(t))
	assert.Equal(t, map[string]int{"ingresses/canary": 2, "ingresses/main": 2}, fixture.engine.executionCounts())
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(mainQuery).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(canaryQuery).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(mainConsumerQuery).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(canaryConsumerQuery).Executions)

	fixture.update(t, nginxCanaryChartIngress("main", false, "v2"))
	assert.Equal(t, "[default/canary]", fixture.renderAndCommit(t))
	assert.Equal(t, map[string]int{"ingresses/canary": 2, "ingresses/main": 4}, fixture.engine.executionCounts())

	fixture.update(t, nginxCanaryChartIngress("canary", false, "v2"))
	assert.Equal(t, "[]", fixture.renderAndCommit(t))
	assert.Equal(t, map[string]int{"ingresses/canary": 4, "ingresses/main": 4}, fixture.engine.executionCounts())

	fixture.update(t, nginxCanaryChartIngress("main", true, "v3"))
	assert.Equal(t, "[default/main]", fixture.renderAndCommit(t))
	assert.Equal(t, map[string]int{"ingresses/canary": 4, "ingresses/main": 6}, fixture.engine.executionCounts())

	require.NoError(t, fixture.ingresses.Delete("default", "main", []string{"default", "main"}))
	assert.Equal(t, "[]", fixture.renderAndCommit(t))
	assert.Equal(t, map[string]int{"ingresses/canary": 4, "ingresses/main": 6}, fixture.engine.executionCounts())
	_, mainCached := fixture.service.incremental.graph.Value(mainQuery)
	assert.False(t, mainCached)
	assert.Zero(t, fixture.service.incremental.graph.Counters(mainQuery))
	_, mainConsumerCached := fixture.service.incremental.graph.Value(mainConsumerQuery)
	assert.False(t, mainConsumerCached)
	assert.Zero(t, fixture.service.incremental.graph.Counters(mainConsumerQuery))
}

func newNginxCanaryChartFixture(t *testing.T) *nginxCanaryChartFixture {
	t.Helper()
	snippets := loadNginxCanaryChartSnippets(t)
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: snippets,
		HAProxyConfig: config.HAProxyConfig{Template: `{{- render "features-080-nginx-ingress-canary-coexist" -}}
[{{- render "test-ingress-route-colocation-consumer" -}}]`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	ingresses := k8sstore.NewMemoryStore(2)
	return &nginxCanaryChartFixture{
		service:   service,
		engine:    engine,
		ingresses: ingresses,
		provider:  stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": ingresses}),
	}
}

func loadNginxCanaryChartSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(
		filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts", "nginx-ingress", "30-features.yaml",
	)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var library nginxCanaryChartLibrary
	require.NoError(t, yaml.Unmarshal(content, &library))
	chartSnippet, exists := library.TemplateSnippets[nginxCanaryProducer]
	require.True(t, exists)
	require.NotNil(t, chartSnippet.Incremental)
	result := map[string]config.TemplateSnippet{
		nginxCanaryProducer: {
			Name:     nginxCanaryProducer,
			Template: chartSnippet.Template,
			Requires: chartSnippet.Requires,
			Incremental: &config.IncrementalTemplate{
				Source: chartSnippet.Incremental.Source, Group: chartSnippet.Incremental.Group,
				Effects: chartSnippet.Incremental.Effects,
			},
		},
		nginxCanaryConsumer: {
			Name:     nginxCanaryConsumer,
			Requires: []string{"ingresses"},
			Incremental: &config.IncrementalTemplate{
				Source: "ingresses", Group: "test-ingress-route-colocation-consumers",
				Consumes: []string{"ingress-route-colocation"},
			},
			Template: `{%%
var id = dig_string(item, "", "metadata", "namespace") + "/" + dig_string(item, "", "metadata", "name")
var _, found = shared.Select("ingress-route-colocation", "identities", id)
if found { show id }
%%}`,
		},
	}
	return result
}

func nginxCanaryChartIngress(name string, canary bool, revision string) map[string]any {
	annotations := map[string]any{}
	if canary {
		annotations["nginx.ingress.kubernetes.io/canary"] = "true"
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        name,
			"annotations": annotations,
		},
		"spec": map[string]any{"revision": revision},
	}
}

func (f *nginxCanaryChartFixture) add(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(ingress, []string{"default", name}))
}

func (f *nginxCanaryChartFixture) update(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Update(ingress, []string{"default", name}))
}

func (f *nginxCanaryChartFixture) renderAndCommit(t *testing.T) string {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return strings.TrimSpace(result.HAProxyConfig)
}
