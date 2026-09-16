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
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const nginxFrontendPublicationRoot = `{{ planRegistry.ProfileGroup() }}
{{- render "features-200-ingress-mirror" -}}
{{- render "features-200-ingress-canary" -}}
{{ render "frontend-filters-820-ingress-mirror" }}
{{ render "frontend-filters-996-ingress-canary" }}
{{ render "frontend-switching-815-ingress-canary" }}
{%- if tostring(extraContext | dig("poisonRead") | fallback(false)) == "true" -%}
  {%- var files = incremental_values("ingress-canary", "files") -%}
  {%- if len(files) > 0 -%}{%- files[0].(map[string]any)["content"] = "poison" -%}{%- end -%}
{%- end -%}
{%- if tostring(extraContext | dig("failAfterReplay") | fallback(false)) == "true" -%}
  {{- fail("forced failure after nginx frontend replay") -}}
{%- end -%}`

type nginxFrontendPublicationFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

type nginxFrontendSnapshot struct {
	config string
	files  map[string]string
}

func TestNginxFrontendPublicationsMatchColdRenderAcrossChanges(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	subject := nginxFrontendIngress("subject", nginxFrontendHosts(31), true, "v1")
	fixture.addIngress(t, subject)

	first := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendColdSnapshot(t, subject), nginxFrontendResultSnapshot(t, first))
	mirror := nginxFrontendMapContent(t, first, "ing-mirror-hosts.map")
	assert.Contains(t, mirror, "host-30.example.com https|mirror.example:8443|2500|2;\n")
	assert.Contains(t, nginxFrontendMapContent(t, first, "ing-canary-weight.map"), "host-00.example.com 10\n")
	assert.Empty(t, requireAuxiliaryFiles(t, first).GeneralFiles, "no host-match file without a header pattern")
	assert.Equal(t, 2, fixture.engine.executionCounts()["ingresses/subject"])

	beforeWarm := fixture.engine.executionCounts()
	warm := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, first), nginxFrontendResultSnapshot(t, warm))
	require.Equal(t, beforeWarm, fixture.engine.executionCounts())

	fixture.addIngress(t, nginxFrontendIngress("unrelated", []string{"unrelated.example.com"}, false, "v1"))
	unrelated := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, first), nginxFrontendResultSnapshot(t, unrelated))
	require.Empty(t, fixture.engine.executionCounts()["ingresses/unrelated"])

	beforeChanged := fixture.engine.executionCounts()
	changedHosts := nginxFrontendHosts(31)
	changedHosts[0] = "changed.example.com"
	changedSubject := nginxFrontendIngress("subject", changedHosts, true, "v2")
	fixture.updateIngress(t, changedSubject)
	changed := fixture.renderAndCommit(t)
	assert.Equal(t, beforeChanged["ingresses/subject"]+2, fixture.engine.executionCounts()["ingresses/subject"])
	require.Equal(t, nginxFrontendColdSnapshot(t, changedSubject), nginxFrontendResultSnapshot(t, changed))
	assert.Equal(t, first.HAProxyConfig, changed.HAProxyConfig, "a host change is a map edit, not a config change")
	assert.NotContains(t, nginxFrontendMapContent(t, changed, "ing-mirror-hosts.map"), "host-00.example.com")
}

func TestNginxFrontendPublicationDeletionOnSharedHost(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	hosts := []string{"shared.example.com"}
	a := nginxFrontendIngress("a", hosts, true, "v1")
	b := nginxFrontendIngress("b", hosts, true, "v1")
	b["metadata"].(map[string]any)["annotations"].(map[string]any)["nginx.ingress.kubernetes.io/canary-weight"] = "90"
	fixture.addIngress(t, a)
	fixture.addIngress(t, b)

	first := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendColdSnapshot(t, a, b), nginxFrontendResultSnapshot(t, first))
	entry := "https|mirror.example:8443|2500|2;"
	assert.Contains(t, nginxFrontendMapContent(t, first, "ing-mirror-hosts.map"), "shared.example.com "+entry+entry+"\n")
	assert.Contains(t, nginxFrontendMapContent(t, first, "ing-canary-weight.map"), "shared.example.com 10\n")

	fixture.deleteIngress(t, "a")
	promoted := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendColdSnapshot(t, b), nginxFrontendResultSnapshot(t, promoted))
	assert.Contains(t, nginxFrontendMapContent(t, promoted, "ing-mirror-hosts.map"), "shared.example.com "+entry+"\n")
	assert.Contains(t, nginxFrontendMapContent(t, promoted, "ing-canary-weight.map"), "shared.example.com 90\n")

	fixture.deleteIngress(t, "b")
	empty := fixture.renderAndCommit(t)
	assert.NotContains(t, empty.HAProxyConfig, "ingress/mirror-target")
	assert.NotContains(t, empty.HAProxyConfig, "txn.canary_backend")
}

func TestNginxFrontendPublicationsStayConstantWithInactiveIngresses(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	fixture.addIngress(t, nginxFrontendIngress("subject", []string{"subject.example.com"}, true, "v1"))
	for index := range 3000 {
		name := fmt.Sprintf("inactive-%04d", index)
		fixture.addIngress(t, nginxFrontendIngress(name, []string{name + ".example.com"}, false, "v1"))
	}
	fixture.renderAndCommit(t)
	for index := range 3000 {
		assert.Zero(t, fixture.engine.executionCounts()[fmt.Sprintf("ingresses/inactive-%04d", index)])
	}

	beforeWarm := fixture.engine.executionCounts()
	fixture.renderAndCommit(t)
	require.Equal(t, beforeWarm, fixture.engine.executionCounts())

	fixture.updateIngress(t, nginxFrontendIngress("inactive-0000", []string{"changed.example.com"}, false, "v2"))
	fixture.renderAndCommit(t)
	require.Zero(t, fixture.engine.executionCounts()["ingresses/inactive-0000"])

	beforeChanged := fixture.engine.executionCounts()
	fixture.updateIngress(t, nginxFrontendIngress("subject", []string{"changed.example.com"}, true, "v2"))
	fixture.renderAndCommit(t)
	require.Equal(t, beforeChanged["ingresses/subject"]+2, fixture.engine.executionCounts()["ingresses/subject"])
}

func TestNginxFrontendFailedRootAndAdmissionCannotPoisonCache(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	baselineResource := nginxFrontendPatternIngress(nginxFrontendIngress("subject", nginxFrontendHosts(31), true, "v1"))
	fixture.addIngress(t, baselineResource)
	baseline := fixture.renderAndCommit(t)
	require.Len(t, requireAuxiliaryFiles(t, baseline).GeneralFiles, 1, "a header pattern over 30 hosts registers a host-match file")

	fixture.config.TemplatingSettings.ExtraContext["poisonRead"] = true
	poisoned, err := fixture.render(rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "template mutates an immutable input")
	assert.Nil(t, poisoned)
	fixture.config.TemplatingSettings.ExtraContext["poisonRead"] = false
	beforeWarm := fixture.engine.executionCounts()
	afterPoison := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, baseline), nginxFrontendResultSnapshot(t, afterPoison))
	require.Equal(t, beforeWarm, fixture.engine.executionCounts())

	changedResource := nginxFrontendPatternIngress(nginxFrontendIngress("subject", append([]string{"changed.example.com"}, nginxFrontendHosts(30)...), true, "v2"))
	fixture.updateIngress(t, changedResource)
	fixture.config.TemplatingSettings.ExtraContext["failAfterReplay"] = true
	failed, err := fixture.render(rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after nginx frontend replay")
	assert.Nil(t, failed)
	afterFailure := fixture.engine.executionCounts()
	fixture.config.TemplatingSettings.ExtraContext["failAfterReplay"] = false
	retried := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendColdSnapshot(t, changedResource), nginxFrontendResultSnapshot(t, retried))
	require.Equal(t, afterFailure["ingresses/subject"]+2, fixture.engine.executionCounts()["ingresses/subject"])

	invalid := nginxFrontendIngress("subject", nginxFrontendHosts(31), true, "v3")
	invalid["metadata"].(map[string]any)["annotations"].(map[string]any)["nginx.ingress.kubernetes.io/mirror-target"] = "http://"
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: invalid}),
		}),
	)
	admission, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("ingresses", "default", "subject"),
	)
	require.ErrorContains(t, err, "could not derive a host:port authority")
	assert.Nil(t, admission)
	afterAdmission := fixture.engine.executionCounts()
	baseAfterAdmission := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, retried), nginxFrontendResultSnapshot(t, baseAfterAdmission))
	require.Equal(t, afterAdmission, fixture.engine.executionCounts())
}

func newNginxFrontendPublicationFixture(t *testing.T) *nginxFrontendPublicationFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"spoaHub":    map[string]any{"mirror": map[string]any{"targetTimeoutMs": 2500, "targetRetries": 2}},
			"poisonRead": false, "failAfterReplay": false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"services":  {APIVersion: "v1", Resources: "services", IndexBy: []string{"metadata.namespace", "metadata.name"}},
			"endpoints": {APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices", IndexBy: []string{"metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"}},
			"secrets":   {APIVersion: "v1", Resources: "secrets", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
		TemplateSnippets: loadNginxFrontendPublicationSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: nginxFrontendPublicationRoot},
	}
	types := ingressBackendSchemaTypes(t)
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	ingresses := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": ingresses, "services": k8sstore.NewMemoryStore(2),
		"endpoints": k8sstore.NewMemoryStore(2), "secrets": k8sstore.NewMemoryStore(2),
	})
	return &nginxFrontendPublicationFixture{
		config: cfg, service: service, engine: engine, ingresses: ingresses, provider: provider,
	}
}

func loadNginxFrontendPublicationSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	files := []string{
		"base/library.yaml", "ingress/library.yaml", "ingress-annotations-compat/library.yaml",
		"nginx-ingress/20-frontend-filters.yaml",
	}
	wanted := map[string]bool{
		"util-ingress-helpers": true, "util-webhook-reject-or-warn": true,
		"util-validate-config-value": true, "util-config-injection-kind": true,
		"util-escape-dquote-value": true, "util-escape-logformat-value": true,
		"util-backend-name-ingress": true, "util-ingress-host-match-publication": true,
		"util-register-map": true, "util-mirror-publish": true, "util-canary-publish": true, "util-canary-lane": true,
		"features-200-ingress-mirror": true, "ingress-mirror-9999-declaration": true,
		"frontend-filters-820-ingress-mirror": true, "ingress-mirror-0555-nginx-ingress": true,
		"features-200-ingress-canary": true, "ingress-canary-9999-declaration": true,
		"frontend-filters-996-ingress-canary": true, "frontend-switching-815-ingress-canary": true,
		"ingress-canary-0780-nginx-ingress": true,
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, relativePath := range files {
		content, err := os.ReadFile(filepath.Join(chartRoot, relativePath))
		require.NoError(t, err)
		var library ingressBackendChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library))
		for name, chartSnippet := range library.TemplateSnippets {
			if !wanted[name] {
				continue
			}
			snippet := config.TemplateSnippet{Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source: chartSnippet.Incremental.Source, BindingsTemplate: chartSnippet.Incremental.BindingsTemplate,
					WhenAnyPathExists: chartSnippet.Incremental.WhenAnyPathExists,
					Group:             chartSnippet.Incremental.Group, Consumes: chartSnippet.Incremental.Consumes,
					OptionalConsumes: chartSnippet.Incremental.OptionalConsumes,
					Effects:          chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func nginxFrontendHosts(count int) []string {
	hosts := make([]string, count)
	for index := range count {
		hosts[index] = fmt.Sprintf("host-%02d.example.com", index)
	}
	return hosts
}

func nginxFrontendIngress(name string, hosts []string, active bool, revision string) map[string]any {
	annotations := map[string]any{}
	if active {
		annotations = map[string]any{
			"nginx.ingress.kubernetes.io/mirror-target":          "https://mirror.example:8443$request_uri",
			"nginx.ingress.kubernetes.io/canary":                 "true",
			"nginx.ingress.kubernetes.io/canary-by-header":       "X-Canary",
			"nginx.ingress.kubernetes.io/canary-by-header-value": "always",
			"nginx.ingress.kubernetes.io/canary-by-cookie":       "stage",
			"nginx.ingress.kubernetes.io/canary-weight":          "10",
		}
	}
	rules := make([]any, 0, len(hosts))
	for index, host := range hosts {
		paths := []any{}
		if index == 0 {
			paths = []any{map[string]any{
				"path": "/", "pathType": "Prefix",
				"backend": map[string]any{"service": map[string]any{
					"name": "app", "port": map[string]any{"name": "http"},
				}},
			}}
		}
		rules = append(rules, map[string]any{"host": host, "http": map[string]any{"paths": paths}})
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
		"metadata": map[string]any{
			"namespace": "default", "name": name, "annotations": annotations,
			"labels": map[string]any{"test-revision": revision},
		},
		"spec": map[string]any{"rules": rules},
	}
}

func nginxFrontendPatternIngress(resource map[string]any) map[string]any {
	resource["metadata"].(map[string]any)["annotations"].(map[string]any)["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = "^v[0-9]+$"
	return resource
}

func (f *nginxFrontendPublicationFixture) addIngress(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(resource, []string{"default", name}))
}

func (f *nginxFrontendPublicationFixture) updateIngress(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Update(resource, []string{"default", name}))
}

func (f *nginxFrontendPublicationFixture) deleteIngress(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, f.ingresses.Delete("default", name, []string{"default", name}))
}

func (f *nginxFrontendPublicationFixture) render(mode rendercontext.RenderMode) (*RenderResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return f.service.Render(ctx, f.provider, mode)
}

func (f *nginxFrontendPublicationFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.render(rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

// nginxFrontendColdSnapshot renders the given Ingresses on a fresh fixture: the
// oracle a warm render has to match byte for byte.
func nginxFrontendColdSnapshot(t *testing.T, resources ...map[string]any) nginxFrontendSnapshot {
	t.Helper()
	cold := newNginxFrontendPublicationFixture(t)
	for _, resource := range resources {
		cold.addIngress(t, resource)
	}
	return nginxFrontendResultSnapshot(t, cold.renderAndCommit(t))
}

func nginxFrontendResultSnapshot(t *testing.T, result *RenderResult) nginxFrontendSnapshot {
	t.Helper()
	files := requireAuxiliaryFiles(t, result)
	snapshot := nginxFrontendSnapshot{config: result.HAProxyConfig, files: map[string]string{}}
	for _, file := range files.GeneralFiles {
		snapshot.files[file.GetIdentifier()] = file.GetContent()
	}
	for _, file := range files.MapFiles {
		snapshot.files[file.Path] = file.Content
	}
	return snapshot
}

func nginxFrontendMapContent(t *testing.T, result *RenderResult, name string) string {
	t.Helper()
	for _, file := range requireAuxiliaryFiles(t, result).MapFiles {
		if path.Base(file.Path) == name {
			return file.Content
		}
	}
	require.FailNow(t, name+" is missing")
	return ""
}
