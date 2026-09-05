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
	"fmt"
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

const wafCatalogPublicationComponent = "haptic-waf-selfservice-catalog-publications"
const wafTrustedCatalogPublicationComponent = "haptic-waf-trusted-catalog-publications"

const wafCatalogPublicationRoot = `{{- render "haptic-waf-selfservice-catalog-publications" -}}
{%%
var incremental = []any{}
for _, catalogAny := range incremental_values("haptic-waf-selfservice-catalogs", "catalogs") {
  var catalog = catalogAny.(map[string]any)
  incremental = append(incremental, map[string]any{
    "namespace": catalog["namespace"],
    "name": catalog["name"],
    "policyYAML": catalog["policyYAML"],
  })
}
var selfService = extraContext | dig("waf", "policies", "selfService") | fallback(map[string]any{})
var enabled = tostring(selfService | dig("enabled") | fallback(false)) == "true"
var configMapName = selfService | dig("configMapName") | fallback("waf-policies") | tostring()
var dataKey = selfService | dig("key") | fallback("policies.yaml") | tostring()
var legacy = []any{}
if enabled {
  var byNamespace = map[string]*resources.waf_selfservice_catalogs.T{}
  var namespaces = []any{}
  for _, cm := range resources.waf_selfservice_catalogs.List() {
    if cm.Metadata.Name != configMapName { continue }
    if byNamespace[cm.Metadata.Namespace] == nil {
      byNamespace[cm.Metadata.Namespace] = cm
      namespaces = append(namespaces, cm.Metadata.Namespace)
    }
  }
  for _, namespaceAny := range sort_strings(namespaces) {
    var namespace = tostring(namespaceAny)
    var cm = byNamespace[namespace]
    legacy = append(legacy, map[string]any{
      "namespace": namespace,
      "name": cm.Metadata.Name,
      "policyYAML": cm.Data[dataKey],
    })
  }
}
%%}
{{ "I\n" }}{{ toJSON(incremental) }}{{ "\nL\n" }}{{ toJSON(legacy) }}
{%- if tostring(extraContext | dig("failAfterCatalogs") | fallback(false)) == "true" -%}
{{- fail("forced failure after WAF catalog publications") -}}
{%- end -%}`

const wafTrustedCatalogPublicationRoot = `{{- render "haptic-waf-trusted-catalog-publications" -}}
{%%
var incremental = []any{}
for _, catalogAny := range incremental_values("haptic-waf-trusted-catalogs", "catalogs") {
  var catalog = catalogAny.(map[string]any)
  incremental = append(incremental, map[string]any{
    "refName": catalog["refName"],
    "namespace": catalog["namespace"],
    "name": catalog["name"],
    "key": catalog["key"],
    "policyYAML": catalog["policyYAML"],
  })
}
var refsAny = extraContext | dig("waf", "policies", "configMapRefs") | fallback(map[string]any{})
var refs, refsOK = refsAny.(map[string]any)
if !refsOK { fail("trusted refs malformed") }
var legacy = []any{}
for _, refName := range keys(refs) {
  var ref = refs[refName].(map[string]any)
  var namespace = extraContext | dig("controllerNamespace") | fallback("") | tostring()
  if ref["namespace"] != nil { namespace = ref["namespace"].(string) }
  var name = tostring(ref["name"])
  var key = "policies.yaml"
  if ref["key"] != nil { key = ref["key"].(string) }
  var cm = resources.configmaps.GetSingle(namespace, name)
  if cm != nil {
    legacy = append(legacy, map[string]any{
      "refName": refName, "namespace": namespace, "name": name,
      "key": key, "policyYAML": cm.Data[key],
    })
  }
}
%%}
{{ "I\n" }}{{ toJSON(incremental) }}{{ "\nL\n" }}{{ toJSON(legacy) }}
{%- if tostring(extraContext | dig("failAfterCatalogs") | fallback(false)) == "true" -%}
{{- fail("forced failure after trusted WAF catalog publications") -}}
{%- end -%}`

type wafCatalogChartLibrary struct {
	TemplateSnippets map[string]wafCatalogChartSnippet `yaml:"templateSnippets"`
}

type wafCatalogChartSnippet struct {
	Template    string                      `yaml:"template"`
	Requires    []string                    `yaml:"requires"`
	Incremental *wafCatalogChartIncremental `yaml:"incremental"`
}

type wafCatalogChartIncremental struct {
	Source           string                     `yaml:"source"`
	BindingsTemplate string                     `yaml:"bindingsTemplate"`
	Group            string                     `yaml:"group"`
	Effects          []config.IncrementalEffect `yaml:"effects"`
}

type wafCatalogConfigMap struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   wafCatalogMetadata `json:"metadata"`
	Data       map[string]string  `json:"data"`
}

type wafCatalogMetadata struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type wafCatalogFixture struct {
	config   *config.Config
	service  *RenderService
	engine   *dynamicBindingCountingEngine
	catalogs *k8sstore.MemoryStore
	provider stores.StoreProvider
}

type wafTrustedCatalogFixture struct {
	config     *config.Config
	service    *RenderService
	engine     *dynamicBindingCountingEngine
	configMaps *k8sstore.MemoryStore
	provider   stores.StoreProvider
}

func TestWAFTrustedCatalogPublicationsTrackExactTargetAndIgnoreUnrelatedOutput(t *testing.T) {
	fixture := newWAFTrustedCatalogFixture(t)
	fixture.add(t, wafCatalogResource("controller", "trusted", "v1"))
	fixture.add(t, wafCatalogResource("other", "unrelated", "noise-1"))

	cold := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, cold)
	assert.Contains(t, cold.HAProxyConfig, "policy-v1")
	coldCounts := fixture.engine.executionCounts()
	assert.Equal(t, 1, coldCounts["configmaps/trusted"])
	assert.Equal(t, 1, coldCounts["configmaps/unrelated"])

	warm := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, warm)
	assert.Equal(t, cold.HAProxyConfig, warm.HAProxyConfig)
	assert.Equal(t, coldCounts, fixture.engine.executionCounts())

	fixture.update(t, wafCatalogResource("other", "unrelated", "noise-2"))
	unrelated := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, unrelated)
	assert.Equal(t, cold.HAProxyConfig, unrelated.HAProxyConfig)
	assert.Equal(t, 1, fixture.engine.executionCounts()["configmaps/trusted"])
	assert.Equal(t, 2, fixture.engine.executionCounts()["configmaps/unrelated"])

	fixture.update(t, wafCatalogResource("controller", "trusted", "v2"))
	changed := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, changed)
	assert.Contains(t, changed.HAProxyConfig, "policy-v2")
	assert.NotEqual(t, cold.HAProxyConfig, changed.HAProxyConfig)
	assert.Equal(t, 2, fixture.engine.executionCounts()["configmaps/trusted"])
}

func TestWAFTrustedCatalogPublicationsDetectDeleteRecreateAndAbort(t *testing.T) {
	fixture := newWAFTrustedCatalogFixture(t)
	fixture.add(t, wafCatalogResource("controller", "trusted", "stable"))
	baseline := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, baseline)
	committedSnapshot := fixture.service.incremental.snapshot

	fixture.delete(t, "controller", "trusted")
	deleted := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, deleted)
	assert.NotContains(t, deleted.HAProxyConfig, "policy-stable")
	component := fixture.service.incremental.components[wafTrustedCatalogPublicationComponent]
	query := componentQueryKey(&component, "configmaps", "controller", "trusted")
	_, cached := fixture.service.incremental.graph.Value(query)
	assert.False(t, cached)

	fixture.add(t, wafCatalogResource("controller", "trusted", "stable"))
	recreated := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, recreated)
	assert.Equal(t, baseline.HAProxyConfig, recreated.HAProxyConfig)
	assert.Equal(t, 2, fixture.engine.executionCounts()["configmaps/trusted"])
	recreatedSnapshot := fixture.service.incremental.snapshot

	fixture.update(t, wafCatalogResource("controller", "trusted", "aborted"))
	fixture.config.TemplatingSettings.ExtraContext["failAfterCatalogs"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after trusted WAF catalog publications")
	assert.Nil(t, failed)
	assert.Same(t, recreatedSnapshot, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterCatalogs"] = false
	retried := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, retried)
	assert.Contains(t, retried.HAProxyConfig, "policy-aborted")
	assert.NotSame(t, committedSnapshot, fixture.service.incremental.snapshot)
}

func TestWAFTrustedCatalogPublicationPropsSelectExactConfigMap(t *testing.T) {
	fixture := newWAFTrustedCatalogFixture(t)
	fixture.add(t, wafCatalogResource("controller", "trusted", "primary"))
	fixture.add(t, wafCatalogResource("controller", "alternate", "alternate"))

	baseline := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, baseline)
	assert.Contains(t, baseline.HAProxyConfig, "policy-primary")
	assert.NotContains(t, baseline.HAProxyConfig, "policy-alternate")
	baselineCounts := fixture.engine.executionCounts()

	fixture.config.TemplatingSettings.ExtraContext["unused"] = "changed"
	assert.Equal(t, baseline.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, baselineCounts, fixture.engine.executionCounts())

	refs := fixture.config.TemplatingSettings.ExtraContext["waf"].(map[string]any)["policies"].(map[string]any)["configMapRefs"].(map[string]any)
	refs["trusted"].(map[string]any)["name"] = "alternate"
	selected := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, selected)
	assert.Contains(t, selected.HAProxyConfig, "policy-alternate")
	assert.NotContains(t, selected.HAProxyConfig, "policy-primary")
	selectedCounts := fixture.engine.executionCounts()
	assert.NotEqual(t, baselineCounts, selectedCounts)
	assert.Equal(t, selected.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, selectedCounts, fixture.engine.executionCounts())
}

func TestWAFTrustedCatalogMalformedBindingsFailAtAuthoritativeValidation(t *testing.T) {
	fixture := newWAFTrustedCatalogFixture(t)
	fixture.config.TemplatingSettings.ExtraContext["waf"].(map[string]any)["policies"] = map[string]any{
		"configMapRefs": "invalid",
	}

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "trusted refs malformed")
	assert.Nil(t, result)
	assert.Empty(t, fixture.engine.executionCounts())
}

func TestWAFSelfServiceCatalogPublicationsScaleAndStayChangeLocal(t *testing.T) {
	for _, catalogCount := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("catalogs=%d", catalogCount), func(t *testing.T) {
			fixture := newWAFCatalogFixture(t)
			for index := range catalogCount {
				namespace := fmt.Sprintf("team-%06d", index)
				fixture.add(t, wafCatalogResource(namespace, "waf-policies", "v1"))
			}

			cold := fixture.renderAndCommit(t)
			fixture.requireDifferential(t, cold)
			assert.Equal(t, uint64(1), fixture.executions("team-000000", "waf-policies"))
			assert.Equal(t, uint64(1), fixture.executions(
				fmt.Sprintf("team-%06d", catalogCount-1), "waf-policies"))

			warm := fixture.renderAndCommit(t)
			fixture.requireDifferential(t, warm)
			assert.Equal(t, cold.HAProxyConfig, warm.HAProxyConfig)
			assert.Equal(t, uint64(1), fixture.executions("team-000000", "waf-policies"))

			fixture.update(t, wafCatalogResource("team-000000", "waf-policies", "v2"))
			changed := fixture.renderAndCommit(t)
			fixture.requireDifferential(t, changed)
			assert.NotEqual(t, cold.HAProxyConfig, changed.HAProxyConfig)
			assert.Equal(t, uint64(2), fixture.executions("team-000000", "waf-policies"))
			assert.Equal(t, uint64(1), fixture.executions(
				fmt.Sprintf("team-%06d", catalogCount-1), "waf-policies"))
		})
	}
}

func TestWAFSelfServiceCatalogPublicationSelectionAndDisabledStateStayExact(t *testing.T) {
	fixture := newWAFCatalogFixture(t)
	fixture.add(t, wafCatalogResource("team-a", "waf-policies", "primary"))
	fixture.add(t, wafCatalogResource("team-a", "alternate", "alternate"))

	primary := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, primary)
	assert.Contains(t, primary.HAProxyConfig, "policy-primary")
	assert.NotContains(t, primary.HAProxyConfig, "policy-alternate")
	assert.Equal(t, uint64(1), fixture.executions("team-a", "waf-policies"))
	assert.Equal(t, uint64(1), fixture.executions("team-a", "alternate"))

	selfService := fixture.selfServiceSettings()
	selfService["enabled"] = false
	disabled := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, disabled)
	assert.Contains(t, disabled.HAProxyConfig, "I\n[]\nL\n[]")
	beforeDisabledChange := fixture.engine.executionCounts()
	fixture.update(t, wafCatalogResource("team-a", "waf-policies", "disabled-change"))
	assert.Equal(t, disabled.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, beforeDisabledChange, fixture.engine.executionCounts())

	selfService["enabled"] = true
	selfService["configMapName"] = "alternate"
	selected := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, selected)
	assert.Contains(t, selected.HAProxyConfig, "policy-alternate")
	assert.NotContains(t, selected.HAProxyConfig, "policy-disabled-change")

	fixture.delete(t, "team-a", "alternate")
	removed := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, removed)
	assert.Contains(t, removed.HAProxyConfig, "I\n[]\nL\n[]")
	component := fixture.service.incremental.components[wafCatalogPublicationComponent]
	query := componentQueryKey(&component, "waf_selfservice_catalogs", "team-a", "alternate")
	_, cached := fixture.service.incremental.graph.Value(query)
	assert.False(t, cached)
	assert.Zero(t, fixture.service.incremental.graph.Counters(query))
}

func TestWAFSelfServiceCatalogPublicationsAbortAdmissionAndColdStayIsolated(t *testing.T) {
	fixture := newWAFCatalogFixture(t)
	fixture.add(t, wafCatalogResource("team-a", "waf-policies", "stable"))
	baseline := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, baseline)
	committedSnapshot := fixture.service.incremental.snapshot

	cold, err := renderServiceStaticCold(t, fixture.service, fixture.provider)
	require.NoError(t, err)
	assert.Equal(t, baseline.HAProxyConfig, cold.HAProxyConfig)
	cold.InputTransaction.Abort()
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)

	proposed := wafCatalogResource("team-a", "waf-policies", "admission")
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"waf_selfservice_catalogs": stores.NewStoreOverlayForUpdate(
				&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("waf_selfservice_catalogs", "team-a", "waf-policies"),
	)
	require.NoError(t, err)
	fixture.requireDifferential(t, admission)
	assert.Contains(t, admission.HAProxyConfig, "policy-admission")
	require.NoError(t, admission.InputTransaction.Commit(t.Context()))
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, baseline.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
	committedSnapshot = fixture.service.incremental.snapshot

	fixture.update(t, wafCatalogResource("team-a", "waf-policies", "retry"))
	fixture.config.TemplatingSettings.ExtraContext["failAfterCatalogs"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after WAF catalog publications")
	assert.Nil(t, failed)
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterCatalogs"] = false
	retried := fixture.renderAndCommit(t)
	fixture.requireDifferential(t, retried)
	assert.Contains(t, retried.HAProxyConfig, "policy-retry")

	fresh := newWAFCatalogFixture(t)
	fresh.add(t, wafCatalogResource("team-a", "waf-policies", "retry"))
	oracle := fresh.renderAndCommit(t)
	assert.Equal(t, oracle.HAProxyConfig, retried.HAProxyConfig)
}

func newWAFCatalogFixture(t *testing.T) *wafCatalogFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"failAfterCatalogs": false,
			"waf": map[string]any{"policies": map[string]any{"selfService": map[string]any{
				"enabled": true,
			}}},
		}},
		WatchedResources: map[string]config.WatchedResource{
			"waf_selfservice_catalogs": {
				APIVersion: "v1", Resources: "configmaps",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: loadWAFCatalogPublicationSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: wafCatalogPublicationRoot},
	}
	raceScaleRenderTimeout(cfg)
	types := &typebootstrap.Result{
		Types: map[string]reflect.Type{
			"waf_selfservice_catalogs": reflect.TypeOf(wafCatalogConfigMap{}),
		},
		Kinds:  map[string]string{"waf_selfservice_catalogs": "ConfigMap"},
		Errors: map[string]error{},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	catalogs := k8sstore.NewMemoryStore(2)
	return &wafCatalogFixture{
		config: cfg, service: service, engine: engine, catalogs: catalogs,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{
			"waf_selfservice_catalogs": catalogs,
		}),
	}
}

func newWAFTrustedCatalogFixture(t *testing.T) *wafTrustedCatalogFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"controllerNamespace": "controller",
			"failAfterCatalogs":   false,
			"waf": map[string]any{"policies": map[string]any{"configMapRefs": map[string]any{
				"trusted": map[string]any{"name": "trusted"},
			}}},
		}},
		WatchedResources: map[string]config.WatchedResource{
			"configmaps": {
				APIVersion: "v1", Resources: "configmaps",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: loadWAFTrustedCatalogPublicationSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: wafTrustedCatalogPublicationRoot},
	}
	raceScaleRenderTimeout(cfg)
	types := &typebootstrap.Result{
		Types:  map[string]reflect.Type{"configmaps": reflect.TypeOf(wafCatalogConfigMap{})},
		Kinds:  map[string]string{"configmaps": "ConfigMap"},
		Errors: map[string]error{},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	configMaps := k8sstore.NewMemoryStore(2)
	return &wafTrustedCatalogFixture{
		config: cfg, service: service, engine: engine, configMaps: configMaps,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"configmaps": configMaps}),
	}
}

func loadWAFCatalogPublicationSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	wanted := map[string]bool{
		wafCatalogPublicationComponent: true,
		"util-waf-governance":          true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, path := range []string{
		"haptic-annotations/83-waf-policies.yaml",
		"ingress-annotations-compat/library.yaml",
	} {
		content, err := os.ReadFile(filepath.Join(chartRoot, path))
		require.NoError(t, err)
		var library wafCatalogChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{
				Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires,
			}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source:           chartSnippet.Incremental.Source,
					BindingsTemplate: chartSnippet.Incremental.BindingsTemplate,
					Group:            chartSnippet.Incremental.Group,
					Effects:          chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func loadWAFTrustedCatalogPublicationSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts",
		"haptic-annotations", "83-waf-policies.yaml")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var library wafCatalogChartLibrary
	require.NoError(t, yaml.Unmarshal(content, &library))
	chartSnippet, found := library.TemplateSnippets[wafTrustedCatalogPublicationComponent]
	require.True(t, found)
	require.NotNil(t, chartSnippet.Incremental)
	return map[string]config.TemplateSnippet{
		wafTrustedCatalogPublicationComponent: {
			Name:     wafTrustedCatalogPublicationComponent,
			Template: chartSnippet.Template,
			Requires: chartSnippet.Requires,
			Incremental: &config.IncrementalTemplate{
				Source: chartSnippet.Incremental.Source, BindingsTemplate: chartSnippet.Incremental.BindingsTemplate,
				Group: chartSnippet.Incremental.Group, Effects: chartSnippet.Incremental.Effects,
			},
		},
	}
}

func wafCatalogResource(namespace, name, revision string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"namespace": namespace,
			"name":      name,
		},
		"data": map[string]any{
			"policies.yaml": "policy-" + revision + ":\n  requestBody:\n    mode: none\n",
			"revision":      revision,
		},
	}
}

func (f *wafCatalogFixture) selfServiceSettings() map[string]any {
	waf := f.config.TemplatingSettings.ExtraContext["waf"].(map[string]any)
	policies := waf["policies"].(map[string]any)
	return policies["selfService"].(map[string]any)
}

func (f *wafCatalogFixture) add(t *testing.T, catalog map[string]any) {
	t.Helper()
	metadata := catalog["metadata"].(map[string]any)
	require.NoError(t, f.catalogs.Add(catalog, []string{
		metadata["namespace"].(string), metadata["name"].(string),
	}))
}

func (f *wafCatalogFixture) update(t *testing.T, catalog map[string]any) {
	t.Helper()
	metadata := catalog["metadata"].(map[string]any)
	require.NoError(t, f.catalogs.Update(catalog, []string{
		metadata["namespace"].(string), metadata["name"].(string),
	}))
}

func (f *wafCatalogFixture) delete(t *testing.T, namespace, name string) {
	t.Helper()
	require.NoError(t, f.catalogs.Delete(namespace, name, []string{namespace, name}))
}

func (f *wafCatalogFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *wafCatalogFixture) executions(namespace, name string) uint64 {
	component := f.service.incremental.components[wafCatalogPublicationComponent]
	query := componentQueryKey(&component, "waf_selfservice_catalogs", namespace, name)
	return f.service.incremental.graph.Counters(query).Executions
}

func (f *wafCatalogFixture) requireDifferential(t *testing.T, result *RenderResult) {
	t.Helper()
	trimmed := strings.TrimSpace(result.HAProxyConfig)
	require.True(t, strings.HasPrefix(trimmed, "I\n"), trimmed)
	parts := strings.Split(strings.TrimPrefix(trimmed, "I\n"), "\nL\n")
	require.Len(t, parts, 2, trimmed)
	assert.JSONEq(t, strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0]))
}

func (f *wafTrustedCatalogFixture) add(t *testing.T, catalog map[string]any) {
	t.Helper()
	metadata := catalog["metadata"].(map[string]any)
	require.NoError(t, f.configMaps.Add(catalog, []string{
		metadata["namespace"].(string), metadata["name"].(string),
	}))
}

func (f *wafTrustedCatalogFixture) update(t *testing.T, catalog map[string]any) {
	t.Helper()
	metadata := catalog["metadata"].(map[string]any)
	require.NoError(t, f.configMaps.Update(catalog, []string{
		metadata["namespace"].(string), metadata["name"].(string),
	}))
}

func (f *wafTrustedCatalogFixture) delete(t *testing.T, namespace, name string) {
	t.Helper()
	require.NoError(t, f.configMaps.Delete(namespace, name, []string{namespace, name}))
}

func (f *wafTrustedCatalogFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *wafTrustedCatalogFixture) requireDifferential(t *testing.T, result *RenderResult) {
	t.Helper()
	trimmed := strings.TrimSpace(result.HAProxyConfig)
	require.True(t, strings.HasPrefix(trimmed, "I\n"), trimmed)
	parts := strings.Split(strings.TrimPrefix(trimmed, "I\n"), "\nL\n")
	require.Len(t, parts, 2, trimmed)
	assert.JSONEq(t, strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0]))
}
