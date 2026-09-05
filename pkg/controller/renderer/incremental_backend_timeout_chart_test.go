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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

var backendTimeoutComponents = []string{
	"map-backend-timeout-100-haproxytech-timeouts",
	"map-backend-timeout-600-haproxy-ingress-timeouts",
	"map-backend-timeout-700-nginx-timeouts",
	"map-backend-timeout-800-haptic-timeouts",
}

type backendTimeoutChartLibrary struct {
	TemplateSnippets map[string]backendTimeoutChartSnippet `yaml:"templateSnippets"`
}

type backendTimeoutChartSnippet struct {
	Template    string                          `yaml:"template"`
	Requires    []string                        `yaml:"requires"`
	Incremental *backendTimeoutChartIncremental `yaml:"incremental"`
}

type backendTimeoutChartIncremental struct {
	Source            string                     `yaml:"source"`
	WhenAnyPathExists []string                   `yaml:"whenAnyPathExists"`
	Group             string                     `yaml:"group"`
	Effects           []config.IncrementalEffect `yaml:"effects"`
}

type backendTimeoutChartFixture struct {
	service   *RenderService
	ingresses *k8sstore.MemoryStore
	services  *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestBackendTimeoutChartReusesCleanIngressesAndTransfersWinners(t *testing.T) {
	fixture := newBackendTimeoutChartFixture(t)
	require.NoError(t, fixture.services.Add(
		backendTimeoutChartService("echo", "http"),
		[]string{"default", "echo"},
	))
	fixture.add(t, backendTimeoutChartIngress("a-first", map[string]any{
		"haproxy-haptic.org/timeout-server": "20s",
	}))
	fixture.add(t, backendTimeoutChartIngress("z-last", map[string]any{
		"haproxy.org/timeout-server": "10s",
	}))

	assert.Equal(t, "be_echo_http|server 10000", fixture.renderAndCommitCacheReady(t).HAProxyConfig)
	fixture.assertOnlyComponentExecution(t, "a-first", "map-backend-timeout-800-haptic-timeouts")
	fixture.assertOnlyComponentExecution(t, "z-last", "map-backend-timeout-100-haproxytech-timeouts")

	assert.Equal(t, "be_echo_http|server 10000", fixture.renderAndCommitCacheReady(t).HAProxyConfig)
	fixture.assertOnlyComponentExecution(t, "a-first", "map-backend-timeout-800-haptic-timeouts")
	fixture.assertOnlyComponentExecution(t, "z-last", "map-backend-timeout-100-haproxytech-timeouts")

	require.NoError(t, fixture.services.Add(
		backendTimeoutChartService("unread", "other"),
		[]string{"default", "unread"},
	))
	assert.Equal(t, "be_echo_http|server 10000", fixture.renderAndCommitCacheReady(t).HAProxyConfig)
	fixture.assertOnlyComponentExecution(t, "a-first", "map-backend-timeout-800-haptic-timeouts")
	fixture.assertOnlyComponentExecution(t, "z-last", "map-backend-timeout-100-haproxytech-timeouts")

	require.NoError(t, fixture.services.Update(
		backendTimeoutChartService("echo", "web"),
		[]string{"default", "echo"},
	))
	assert.Equal(t, "be_echo_web|server 10000", fixture.renderAndCommitCacheReady(t).HAProxyConfig)
	fixture.assertComponentExecutions(t, "map-backend-timeout-800-haptic-timeouts", "a-first", 2)
	fixture.assertComponentExecutions(t, "map-backend-timeout-100-haproxytech-timeouts", "z-last", 2)
	fixture.assertComponentExecutions(t, "map-backend-timeout-600-haproxy-ingress-timeouts", "a-first", 0)
	fixture.assertComponentExecutions(t, "map-backend-timeout-600-haproxy-ingress-timeouts", "z-last", 0)
	fixture.assertComponentExecutions(t, "map-backend-timeout-700-nginx-timeouts", "a-first", 1)
	fixture.assertComponentExecutions(t, "map-backend-timeout-700-nginx-timeouts", "z-last", 1)
	fixture.assertComponentExecutions(t, "map-backend-timeout-100-haproxytech-timeouts", "a-first", 0)
	fixture.assertComponentExecutions(t, "map-backend-timeout-800-haptic-timeouts", "z-last", 0)

	fixture.update(t, backendTimeoutChartIngress("z-last", map[string]any{}))
	assert.Equal(t, "be_echo_web|server 20000", fixture.renderAndCommitCacheReady(t).HAProxyConfig)
	fixture.assertComponentExecutions(t, "map-backend-timeout-800-haptic-timeouts", "a-first", 2)
	fixture.assertComponentExecutions(t, "map-backend-timeout-100-haproxytech-timeouts", "z-last", 0)
	fixture.assertComponentExecutions(t, "map-backend-timeout-600-haproxy-ingress-timeouts", "z-last", 0)
	fixture.assertComponentExecutions(t, "map-backend-timeout-700-nginx-timeouts", "z-last", 2)

	require.NoError(t, fixture.ingresses.Delete("default", "a-first", []string{"default", "a-first"}))
	assert.Empty(t, fixture.renderAndCommitCacheReady(t).HAProxyConfig)
	for _, componentName := range backendTimeoutComponents {
		component := fixture.service.incremental.components[componentName]
		query := componentQueryKey(&component, "ingresses", "default", "a-first")
		_, cached := fixture.service.incremental.graph.Value(query)
		assert.False(t, cached, componentName)
		assert.Zero(t, fixture.service.incremental.graph.Counters(query), componentName)
	}
}

func TestBackendTimeoutChartReplaysEventsAndAdmissionAbortDoesNotPublish(t *testing.T) {
	fixture := newBackendTimeoutChartFixture(t)
	invalid := backendTimeoutChartIngress("invalid", map[string]any{
		"nginx.ingress.kubernetes.io/proxy-read-timeout": "not-seconds",
	})
	fixture.add(t, invalid)

	result := fixture.renderAndCommitCacheReady(t)
	renderedEvents := requireRenderEvents(t, result)
	require.Len(t, renderedEvents, 1)
	assert.Equal(t, "InvalidTimeout", renderedEvents[0].Reason)
	fixture.assertOnlyComponentExecution(t, "invalid", "map-backend-timeout-700-nginx-timeouts")

	result = fixture.renderAndCommitCacheReady(t)
	require.Len(t, requireRenderEvents(t, result), 1)
	fixture.assertOnlyComponentExecution(t, "invalid", "map-backend-timeout-700-nginx-timeouts")

	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: invalid}),
		}),
	)
	_, err := fixture.service.Render(
		t.Context(),
		admissionProvider,
		rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("ingresses", "default", "invalid"),
	)
	require.ErrorContains(t, err, "is not a whole number of seconds")

	result = fixture.renderAndCommitCacheReady(t)
	require.Len(t, requireRenderEvents(t, result), 1)
	fixture.assertOnlyComponentExecution(t, "invalid", "map-backend-timeout-700-nginx-timeouts")
}

func newBackendTimeoutChartFixture(t *testing.T) *backendTimeoutChartFixture {
	t.Helper()
	snippets := loadBackendTimeoutChartSnippets(t)
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: snippets,
		HAProxyConfig: config.HAProxyConfig{
			Template: `{{ render_glob "map-backend-timeout-*" }}`,
		},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	ingresses := k8sstore.NewMemoryStore(2)
	services := k8sstore.NewMemoryStore(2)
	return &backendTimeoutChartFixture{
		service:   service,
		ingresses: ingresses,
		services:  services,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{
			"ingresses": ingresses,
			"services":  services,
		}),
	}
}

func loadBackendTimeoutChartSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	files := []string{
		"base/library.yaml",
		"ingress/library.yaml",
		"haproxytech/library.yaml",
		"haproxy-ingress/20-backend-directives.yaml",
		"nginx-ingress/10-backend-directives.yaml",
		"haptic-annotations/20-backend-directives.yaml",
	}
	wanted := map[string]bool{
		"util-haproxy-duration":        true,
		"util-ingress-backend-timeout": true,
	}
	for _, name := range backendTimeoutComponents {
		wanted[name] = true
	}
	result := make(map[string]config.TemplateSnippet, len(wanted)+1)
	for _, relativePath := range files {
		content, err := os.ReadFile(filepath.Join(chartRoot, relativePath))
		require.NoError(t, err)
		var library backendTimeoutChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source:            chartSnippet.Incremental.Source,
					WhenAnyPathExists: chartSnippet.Incremental.WhenAnyPathExists,
					Group:             chartSnippet.Incremental.Group,
					Effects:           chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	result["util-backend-name-ingress"] = config.TemplateSnippet{
		Name: "util-backend-name-ingress",
		Template: `{% macro BackendNameIngress(ingress any, path any) string %}{%%
var namespace = dig_string(ingress, "", "metadata", "namespace")
var service = dig_string(path, "", "backend", "service", "name")
var svc = resources.services.GetSingle(namespace, service)
var port = dig_string(svc, "", "spec", "portName")
show "be_" + service + "_" + port
%%}{% end %}`,
	}
	return result
}

func backendTimeoutChartService(name, portName string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      name,
		},
		"spec": map[string]any{"portName": portName},
	}
}

func backendTimeoutChartIngress(name string, annotations map[string]any) map[string]any {
	path := map[string]any{
		"backend": map[string]any{
			"service": map[string]any{
				"name": "echo",
				"port": map[string]any{"number": int64(80)},
			},
		},
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        name,
			"annotations": annotations,
		},
		"spec": map[string]any{
			"rules": []any{
				map[string]any{
					"http": map[string]any{
						"paths": []any{path},
					},
				},
			},
		},
	}
}

func (f *backendTimeoutChartFixture) add(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(ingress, []string{"default", name}))
}

func (f *backendTimeoutChartFixture) update(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Update(ingress, []string{"default", name}))
}

func (f *backendTimeoutChartFixture) renderAndCommitCacheReady(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	result.HAProxyConfig = strings.TrimSpace(result.HAProxyConfig)
	return result
}

func (f *backendTimeoutChartFixture) assertOnlyComponentExecution(
	t *testing.T,
	name, activeComponent string,
) {
	t.Helper()
	for _, componentName := range backendTimeoutComponents {
		expected := uint64(0)
		if componentName == "map-backend-timeout-700-nginx-timeouts" {
			expected = 1
		}
		if componentName == activeComponent {
			expected = 1
		}
		component := f.service.incremental.components[componentName]
		query := componentQueryKey(&component, "ingresses", "default", name)
		assert.Equal(t, expected, f.service.incremental.graph.Counters(query).Executions, componentName)
	}
}

func (f *backendTimeoutChartFixture) assertComponentExecutions(
	t *testing.T,
	componentName, name string,
	expected uint64,
) {
	t.Helper()
	component := f.service.incremental.components[componentName]
	query := componentQueryKey(&component, "ingresses", "default", name)
	assert.Equal(t, expected, f.service.incremental.graph.Counters(query).Executions, componentName)
}
