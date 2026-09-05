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
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	haproxyIngressAuthURLComponent    = "map-auth-url-500-haproxy-ingress"
	haproxyIngressAuthSigninComponent = "map-auth-signin-500-haproxy-ingress"
	haproxyIngressAuthMethodComponent = "map-auth-method-500-haproxy-ingress"
)

var haproxyIngressAuthMapComponents = []string{
	haproxyIngressAuthURLComponent,
	haproxyIngressAuthSigninComponent,
	haproxyIngressAuthMethodComponent,
}

type haproxyIngressAuthMapChartLibrary struct {
	TemplateSnippets map[string]haproxyIngressAuthMapChartSnippet `yaml:"templateSnippets"`
}

type haproxyIngressAuthMapChartSnippet struct {
	Template    string                                 `yaml:"template"`
	Requires    []string                               `yaml:"requires"`
	Incremental *haproxyIngressAuthMapChartIncremental `yaml:"incremental"`
}

type haproxyIngressAuthMapChartIncremental struct {
	Source  string                     `yaml:"source"`
	Group   string                     `yaml:"group"`
	Effects []config.IncrementalEffect `yaml:"effects"`
}

type haproxyIngressAuthMapChartFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestHaproxyIngressAuthMapChartReusesCleanIngressesAndRetiresDeletedRows(t *testing.T) {
	fixture := newHaproxyIngressAuthMapChartFixture(t, false)
	fixture.add(t, haproxyIngressAuthMapIngress("z-last", map[string]any{
		"haproxy-ingress.github.io/auth-url":    "https://auth.example/z",
		"haproxy-ingress.github.io/auth-signin": "https://login.example/z",
		"haproxy-ingress.github.io/auth-method": "POST",
	}, "v1"))
	fixture.add(t, haproxyIngressAuthMapIngress("a-first", map[string]any{
		"haproxy-ingress.github.io/auth-url":        "https://auth.example/a",
		"haproxy-ingress.github.io/auth-signin":     "https://login.example/a",
		"haproxy-ingress.github.io/auth-method":     "GET",
		"haproxy-ingress.github.io/ssl-passthrough": "true",
	}, "v1"))

	first := fixture.renderAndCommit(t)
	assert.Equal(t, haproxyIngressAuthMapExpectedBoth, first.HAProxyConfig)
	firstEvents := requireRenderEvents(t, first)
	assert.Equal(t, []templating.RenderedEvent{haproxyIngressAuthIgnoredEvent("a-first")}, firstEvents)
	fixture.assertExecutions(t, "a-first", 1)
	fixture.assertExecutions(t, "z-last", 1)

	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, firstEvents, requireRenderEvents(t, warm))
	fixture.assertExecutions(t, "a-first", 1)
	fixture.assertExecutions(t, "z-last", 1)

	fixture.update(t, haproxyIngressAuthMapIngress("z-last", map[string]any{
		"haproxy-ingress.github.io/auth-url":    "https://auth.example/z-v2",
		"haproxy-ingress.github.io/auth-signin": "https://login.example/z",
		"haproxy-ingress.github.io/auth-method": "POST",
	}, "v2"))
	changed := fixture.renderAndCommit(t)
	assert.Contains(t, changed.HAProxyConfig, "default/z-last https://auth.example/z-v2")
	assert.NotContains(t, changed.HAProxyConfig, "default/z-last https://auth.example/z|")
	fixture.assertExecutions(t, "a-first", 1)
	fixture.assertExecutions(t, "z-last", 2)

	fixture.update(t, haproxyIngressAuthMapIngress("a-first", map[string]any{}, "v2"))
	removed := fixture.renderAndCommit(t)
	assert.Equal(t, haproxyIngressAuthMapExpectedLast, removed.HAProxyConfig)
	assert.Empty(t, requireRenderEvents(t, removed))
	fixture.assertExecutions(t, "a-first", 2)
	fixture.assertExecutions(t, "z-last", 2)

	require.NoError(t, fixture.ingresses.Delete("default", "z-last", []string{"default", "z-last"}))
	assert.Equal(t, "U|S|M\n", fixture.renderAndCommit(t).HAProxyConfig)
	for _, componentName := range haproxyIngressAuthMapComponents {
		component := fixture.service.incremental.components[componentName]
		query := componentQueryKey(&component, "ingresses", "default", "z-last")
		_, cached := fixture.service.incremental.graph.Value(query)
		assert.False(t, cached, componentName)
		assert.Zero(t, fixture.service.incremental.graph.Counters(query), componentName)
	}
}

func TestHaproxyIngressAuthMapChartSkipsInvalidRowsAndReplaysEvents(t *testing.T) {
	fixture := newHaproxyIngressAuthMapChartFixture(t, false)
	fixture.add(t, haproxyIngressAuthMapIngress("stable", map[string]any{
		"haproxy-ingress.github.io/auth-url":    "https://auth.example/stable",
		"haproxy-ingress.github.io/auth-signin": "https://login.example/stable",
		"haproxy-ingress.github.io/auth-method": "HEAD",
	}, "v1"))
	fixture.add(t, haproxyIngressAuthMapIngress("invalid", map[string]any{
		"haproxy-ingress.github.io/auth-url":    "https://auth.example/ok\nPOISON",
		"haproxy-ingress.github.io/auth-signin": "https://login.example/ok\nPOISON",
		"haproxy-ingress.github.io/auth-method": "GET\nPOISON",
	}, "v1"))

	first := fixture.renderAndCommit(t)
	assert.Equal(t, haproxyIngressAuthMapExpectedStable, first.HAProxyConfig)
	firstEvents := requireRenderEvents(t, first)
	assert.Equal(t, []templating.RenderedEvent{
		haproxyIngressInvalidAuthMapEvent("invalid", "auth-method", "InvalidAuthMethod", "auth-method", "the method override"),
		haproxyIngressInvalidAuthMapEvent("invalid", "auth-signin", "InvalidAuthSignin", "auth-signin", "the sign-in redirect"),
		haproxyIngressInvalidAuthMapEvent("invalid", "auth-url", "InvalidAuthURL", "auth-url", "external auth"),
	}, firstEvents)
	fixture.assertExecutions(t, "invalid", 1)

	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, firstEvents, requireRenderEvents(t, warm))
	fixture.assertExecutions(t, "invalid", 1)
}

func TestHaproxyIngressAuthMapChartAdmissionFailuresDoNotPoisonCache(t *testing.T) {
	tests := map[string]string{
		"haproxy-ingress.github.io/auth-url":    "auth-url map",
		"haproxy-ingress.github.io/auth-signin": "auth-signin map",
		"haproxy-ingress.github.io/auth-method": "auth-method map",
	}
	for annotation, messagePart := range tests {
		t.Run(annotation, func(t *testing.T) {
			fixture := newHaproxyIngressAuthMapChartFixture(t, false)
			baselineIngress := haproxyIngressAuthMapIngress("subject", map[string]any{
				"haproxy-ingress.github.io/auth-url":    "https://auth.example/subject",
				"haproxy-ingress.github.io/auth-signin": "https://login.example/subject",
				"haproxy-ingress.github.io/auth-method": "PUT",
			}, "v1")
			fixture.add(t, baselineIngress)
			baseline := fixture.renderAndCommit(t)
			baselineEvents := requireRenderEvents(t, baseline)
			baselineCounters := fixture.counters("subject")

			proposedAnnotations := map[string]any{
				"haproxy-ingress.github.io/auth-url":    "https://auth.example/subject",
				"haproxy-ingress.github.io/auth-signin": "https://login.example/subject",
				"haproxy-ingress.github.io/auth-method": "PUT",
			}
			proposedAnnotations[annotation] = "valid-prefix\nPOISON"
			proposed := haproxyIngressAuthMapIngress("subject", proposedAnnotations, "admission")
			admissionProvider := stores.NewOverlayStoreProvider(
				fixture.provider,
				stores.NewValidationContext(map[string]*stores.StoreOverlay{
					"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
				}),
			)
			failed, err := fixture.service.Render(
				t.Context(),
				admissionProvider,
				rendercontext.RenderModeAdmission,
				rendercontext.WithAdmissionSubject("ingresses", "default", "subject"),
			)
			require.ErrorContains(t, err, messagePart)
			assert.Nil(t, failed)
			assert.Equal(t, baselineCounters, fixture.counters("subject"))

			executionsAfterFailure := fixture.engine.executionCounts()
			afterFailure := fixture.renderAndCommit(t)
			assert.Equal(t, baseline.HAProxyConfig, afterFailure.HAProxyConfig)
			assert.Equal(t, baselineEvents, requireRenderEvents(t, afterFailure))
			assert.Equal(t, executionsAfterFailure, fixture.engine.executionCounts())
			assert.Equal(t, baselineCounters, fixture.counters("subject"))
		})
	}
}

func TestHaproxyIngressAuthMapChartFailedRootRenderDoesNotPublishScratchRows(t *testing.T) {
	fixture := newHaproxyIngressAuthMapChartFixture(t, false)
	fixture.add(t, haproxyIngressAuthMapIngress("subject", map[string]any{
		"haproxy-ingress.github.io/auth-url":    "https://auth.example/v1",
		"haproxy-ingress.github.io/auth-signin": "https://login.example/v1",
		"haproxy-ingress.github.io/auth-method": "GET",
	}, "v1"))
	baseline := fixture.renderAndCommit(t)
	baselineCounters := fixture.counters("subject")

	fixture.update(t, haproxyIngressAuthMapIngress("subject", map[string]any{
		"haproxy-ingress.github.io/auth-url":    "https://auth.example/v2",
		"haproxy-ingress.github.io/auth-signin": "https://login.example/v2",
		"haproxy-ingress.github.io/auth-method": "POST",
	}, "v2"))
	fixture.config.TemplatingSettings.ExtraContext["failAfterAuthMaps"] = true
	beforeFailure := fixture.engine.executionCounts()["ingresses/subject"]
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after auth maps")
	assert.Nil(t, failed)
	assert.Equal(t, baselineCounters, fixture.counters("subject"))
	assert.Equal(t, beforeFailure+len(haproxyIngressAuthMapComponents), fixture.engine.executionCounts()["ingresses/subject"])

	fixture.config.TemplatingSettings.ExtraContext["failAfterAuthMaps"] = false
	beforeRetry := fixture.engine.executionCounts()["ingresses/subject"]
	retried := fixture.renderAndCommit(t)
	assert.NotEqual(t, baseline.HAProxyConfig, retried.HAProxyConfig)
	assert.Contains(t, retried.HAProxyConfig, "https://auth.example/v2")
	assert.Contains(t, retried.HAProxyConfig, "https://login.example/v2")
	assert.Contains(t, retried.HAProxyConfig, "default/subject POST")
	assert.Equal(t, beforeRetry+len(haproxyIngressAuthMapComponents), fixture.engine.executionCounts()["ingresses/subject"])
	fixture.assertExecutions(t, "subject", 2)
}

func TestHaproxyIngressAuthMapChartKeepsFirstContributorForDuplicateIdentity(t *testing.T) {
	fixture := newHaproxyIngressAuthMapChartFixture(t, true)
	fixture.add(t, haproxyIngressAuthMapIngress("subject", map[string]any{
		"haproxy-ingress.github.io/auth-url": "https://auth.example/first",
		"example.test/auth-url":              "https://auth.example/later",
	}, "v1"))

	first := fixture.renderAndCommit(t)
	assert.Equal(t, "U\ndefault/subject https://auth.example/first|S|M\n", first.HAProxyConfig)
	assert.NotContains(t, first.HAProxyConfig, "later")

	fixture.update(t, haproxyIngressAuthMapIngress("subject", map[string]any{
		"haproxy-ingress.github.io/auth-url": "https://auth.example/first",
		"example.test/auth-url":              "https://auth.example/later-v2",
	}, "v2"))
	assert.Equal(t, first.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)

	fixture.update(t, haproxyIngressAuthMapIngress("subject", map[string]any{
		"example.test/auth-url": "https://auth.example/later-v2",
	}, "v3"))
	assert.Equal(t, "U\ndefault/subject https://auth.example/later-v2|S|M\n", fixture.renderAndCommit(t).HAProxyConfig)
}

func newHaproxyIngressAuthMapChartFixture(
	t *testing.T,
	includeLaterURLContributor bool,
) *haproxyIngressAuthMapChartFixture {
	t.Helper()
	snippets := loadHaproxyIngressAuthMapChartSnippets(t)
	if includeLaterURLContributor {
		snippets["map-auth-url-510-test-later"] = config.TemplateSnippet{
			Name:     "map-auth-url-510-test-later",
			Requires: []string{"ingresses"},
			Incremental: &config.IncrementalTemplate{
				Source: "ingresses",
				Group:  "map-auth-url-ingress",
			},
			Template: `{%%
var value = dig_string(item, "", "metadata", "annotations", "example.test/auth-url")
if value != "" {
  var key = dig_string(item, "", "metadata", "namespace") + "/" + dig_string(item, "", "metadata", "name")
  show shared.Unique("map-auth-url-ingress", key, "\n" + key + " " + value)
}
%%}`,
		}
	}
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"failAfterAuthMaps": false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: snippets,
		HAProxyConfig: config.HAProxyConfig{Template: `U{{ render_glob "map-auth-url-*" }}|S{{ render "map-auth-signin-500-haproxy-ingress" }}|M{{ render "map-auth-method-500-haproxy-ingress" }}{%%
if tostring(extraContext | dig("failAfterAuthMaps") | fallback(false)) == "true" {
  fail("forced failure after auth maps")
}
%%}`},
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
	return &haproxyIngressAuthMapChartFixture{
		config:    cfg,
		service:   service,
		engine:    engine,
		ingresses: ingresses,
		provider:  stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": ingresses}),
	}
}

func loadHaproxyIngressAuthMapChartSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	files := []string{"base/library.yaml", "haproxy-ingress/50-auth-spoe.yaml"}
	wanted := map[string]bool{"util-config-injection-kind": true}
	for _, name := range haproxyIngressAuthMapComponents {
		wanted[name] = true
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, relativePath := range files {
		content, err := os.ReadFile(filepath.Join(chartRoot, relativePath))
		require.NoError(t, err)
		var library haproxyIngressAuthMapChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source:  chartSnippet.Incremental.Source,
					Group:   chartSnippet.Incremental.Group,
					Effects: chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func haproxyIngressAuthMapIngress(name string, annotations map[string]any, revision string) map[string]any {
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

func (f *haproxyIngressAuthMapChartFixture) add(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(ingress, []string{"default", name}))
}

func (f *haproxyIngressAuthMapChartFixture) update(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Update(ingress, []string{"default", name}))
}

func (f *haproxyIngressAuthMapChartFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *haproxyIngressAuthMapChartFixture) counters(name string) map[string]uint64 {
	result := make(map[string]uint64, len(haproxyIngressAuthMapComponents))
	for _, componentName := range haproxyIngressAuthMapComponents {
		component := f.service.incremental.components[componentName]
		query := componentQueryKey(&component, "ingresses", "default", name)
		result[componentName] = f.service.incremental.graph.Counters(query).Executions
	}
	return result
}

func (f *haproxyIngressAuthMapChartFixture) assertExecutions(t *testing.T, name string, expected uint64) {
	t.Helper()
	for componentName, executions := range f.counters(name) {
		assert.Equal(t, expected, executions, componentName)
	}
}

func haproxyIngressAuthIgnoredEvent(name string) templating.RenderedEvent {
	return templating.RenderedEvent{
		Namespace:  "default",
		Name:       name,
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Type:       templating.EventTypeWarning,
		Reason:     "AuthIgnored",
		Message: "haproxy-ingress.github.io/auth-url is set together with ssl-passthrough; external auth " +
			"cannot run on SSL-passthrough (L4) traffic and is ignored for this Ingress",
	}
}

func haproxyIngressInvalidAuthMapEvent(
	name, annotation, reason, mapName, effect string,
) templating.RenderedEvent {
	return templating.RenderedEvent{
		Namespace:  "default",
		Name:       name,
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Type:       templating.EventTypeWarning,
		Reason:     reason,
		Message: "Ingress default/" + name + " annotation 'haproxy-ingress.github.io/" + annotation +
			"' contains a control character, which would inject an entry into the " + mapName + " map; " + effect +
			" is not applied. Remove control characters from the value.",
	}
}

const haproxyIngressAuthMapExpectedBoth = "U\ndefault/a-first https://auth.example/a\n" +
	"default/z-last https://auth.example/z|S\ndefault/a-first https://login.example/a\n" +
	"default/z-last https://login.example/z|M\ndefault/a-first GET\ndefault/z-last POST\n"

const haproxyIngressAuthMapExpectedLast = "U\ndefault/z-last https://auth.example/z-v2|S\n" +
	"default/z-last https://login.example/z|M\ndefault/z-last POST\n"

const haproxyIngressAuthMapExpectedStable = "U\ndefault/stable https://auth.example/stable|S\n" +
	"default/stable https://login.example/stable|M\ndefault/stable HEAD\n"
