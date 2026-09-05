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
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

const nginxRootPublicationRoot = `{%- var _, _ = shared.ComputeIfAbsent("globalFeatures", func() any {
  return map[string]any{
    "sslRedirectHosts": []any{}, "redirectHosts": []any{}, "appRootHosts": []any{},
  }
}) -%}
{{- render "features-105-nginx-ingress-ssl-redirect" -}}
{{- render "features-140-nginx-ingress-redirects" -}}
{{- render "features-145-nginx-ingress-app-root" -}}
{%- for _, value := range incremental_values("nginx-ingress-ssl-redirect-hosts", "hosts") -%}
  {%- var entry = value.(map[string]any) -%}
{{ "SSL|" }}{{ entry | dig_string("", "host") }}|{{ entry | dig_string("", "code") }}
{%- end -%}
{%- for _, value := range incremental_values("nginx-ingress-redirect-hosts", "hosts") -%}
  {%- var entry = value.(map[string]any) -%}
{{ "REDIRECT|" }}{{ entry | dig_string("", "host") }}|{{ entry | dig_string("", "location") }}|{{ entry | dig_string("", "code") }}
{%- end -%}
{%- for _, value := range incremental_values("nginx-ingress-app-root-hosts", "hosts") -%}
  {%- var entry = value.(map[string]any) -%}
{{ "APP-ROOT|" }}{{ entry | dig_string("", "host") }}|{{ entry | dig_string("", "path") }}
{%- end -%}
{{ "AUTH-URL|" }}{{ render "map-auth-url-500-nginx-ingress" }}
{{ "AUTH-SIGNIN|" }}{{ render "map-auth-signin-500-nginx-ingress" }}
{{ "AUTH-METHOD|" }}{{ render "map-auth-method-500-nginx-ingress" }}
{{ "USERLIST|" }}{{ render "global-top-700-nginx-ingress-auth" }}
{%- if tostring(extraContext | dig("failAfterReplay") | fallback(false)) == "true" -%}
  {{- fail("forced failure after nginx root publications") -}}
{%- end -%}`

var nginxRootPublicationComponents = []string{
	"nginx-ingress-ssl-redirect-publications",
	"nginx-ingress-redirect-publications",
	"nginx-ingress-app-root-publications",
	"map-auth-url-500-nginx-ingress",
	"map-auth-signin-500-nginx-ingress",
	"map-auth-method-500-nginx-ingress",
	"global-top-700-nginx-ingress-auth",
}

type nginxRootPublicationFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	secrets   *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestNginxRootPublicationsStayConstantWithInactiveIngresses(t *testing.T) {
	for _, count := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("inactive-%d", count), func(t *testing.T) {
			fixture := newNginxRootPublicationFixture(t)
			fixture.addSecret(t, nginxRootPublicationSecret("auth"))
			fixture.addIngress(t, nginxRootPublicationIngress("active", true, "v1"))
			for index := range count {
				name := fmt.Sprintf("inactive-%04d", index)
				fixture.addIngress(t, nginxRootPublicationIngress(name, false, "v1"))
			}

			coldService, coldEngine := newNginxRootPublicationService(t, fixture.config)
			cold, err := renderServiceStaticCold(t, coldService, fixture.provider)
			require.NoError(t, err)
			require.Equal(t, map[string]int{"ingresses/active": len(nginxRootPublicationComponents)}, coldEngine.executionCounts())
			cold.InputTransaction.Abort()

			first := fixture.renderAndCommit(t)
			require.Equal(t, cold.HAProxyConfig, first.HAProxyConfig)
			require.Equal(t, requireRenderEvents(t, cold), requireRenderEvents(t, first))
			require.Equal(t, map[string]int{"ingresses/active": len(nginxRootPublicationComponents)}, fixture.engine.executionCounts())
			require.Equal(t, nginxRootPublicationComponentExecutions(1), fixture.componentExecutions("active"))
			requireNginxRootPublicationOutput(t, first, "active", "v1")

			beforeWarm := fixture.engine.executionCounts()
			beforeWarmComponents := fixture.componentExecutions("active")
			warm := fixture.renderAndCommit(t)
			require.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
			require.Equal(t, requireRenderEvents(t, first), requireRenderEvents(t, warm))
			require.Equal(t, beforeWarm, fixture.engine.executionCounts())
			require.Equal(t, beforeWarmComponents, fixture.componentExecutions("active"))

			fixture.updateIngress(t, nginxRootPublicationIngress("inactive-0000", false, "v2"))
			unrelated := fixture.renderAndCommit(t)
			require.Equal(t, first.HAProxyConfig, unrelated.HAProxyConfig)
			require.Equal(t, beforeWarm, fixture.engine.executionCounts())
			require.Zero(t, fixture.engine.executionCounts()["ingresses/inactive-0000"])
			require.Equal(t, nginxRootPublicationComponentExecutions(0), fixture.componentExecutions("inactive-0000"))

			beforeActive := fixture.engine.executionCounts()["ingresses/active"]
			beforeActiveComponents := fixture.componentExecutions("active")
			fixture.updateIngress(t, nginxRootPublicationIngress("active", true, "v2"))
			changed := fixture.renderAndCommit(t)
			require.Equal(t, beforeActive+len(nginxRootPublicationComponents), fixture.engine.executionCounts()["ingresses/active"])
			for _, component := range nginxRootPublicationComponents {
				require.Equal(t, beforeActiveComponents[component]+1, fixture.componentExecutions("active")[component], component)
			}
			require.NotEqual(t, first.HAProxyConfig, changed.HAProxyConfig)
			requireNginxRootPublicationOutput(t, changed, "active", "v2")
		})
	}
}

func TestNginxRootPublicationDeletionReaddAndDuplicatePromotion(t *testing.T) {
	fixture := newNginxRootPublicationFixture(t)
	fixture.addSecret(t, nginxRootPublicationSecret("auth"))
	fixture.addIngress(t, nginxRootPublicationIngress("a", true, "v1"))
	fixture.addIngress(t, nginxRootPublicationIngress("b", true, "v1"))

	first := fixture.renderAndCommit(t)
	require.Equal(t, 1, strings.Count(first.HAProxyConfig, "userlist ni_auth_default_auth"))
	require.Contains(t, first.HAProxyConfig, "# nginx-ingress/global-top-auth (default/a)")
	require.NotContains(t, first.HAProxyConfig, "# nginx-ingress/global-top-auth (default/b)")
	require.Equal(t, len(nginxRootPublicationComponents), fixture.engine.executionCounts()["ingresses/a"])
	require.Equal(t, len(nginxRootPublicationComponents), fixture.engine.executionCounts()["ingresses/b"])

	beforeDelete := fixture.engine.executionCounts()
	fixture.deleteIngress(t, "a")
	promoted := fixture.renderAndCommit(t)
	require.Equal(t, 1, strings.Count(promoted.HAProxyConfig, "userlist ni_auth_default_auth"))
	require.NotContains(t, promoted.HAProxyConfig, "# nginx-ingress/global-top-auth (default/a)")
	require.Contains(t, promoted.HAProxyConfig, "# nginx-ingress/global-top-auth (default/b)")
	require.Equal(t, beforeDelete, fixture.engine.executionCounts())
	for _, name := range nginxRootPublicationComponents {
		component := fixture.service.incremental.components[name]
		query := componentQueryKey(&component, "ingresses", "default", "a")
		_, cached := fixture.service.incremental.graph.Value(query)
		assert.False(t, cached, name)
		assert.Zero(t, fixture.service.incremental.graph.Counters(query), name)
	}

	fixture.addIngress(t, nginxRootPublicationIngress("a", true, "v1"))
	readded := fixture.renderAndCommit(t)
	require.Equal(t, 1, strings.Count(readded.HAProxyConfig, "userlist ni_auth_default_auth"))
	require.Contains(t, readded.HAProxyConfig, "# nginx-ingress/global-top-auth (default/a)")
	require.NotContains(t, readded.HAProxyConfig, "# nginx-ingress/global-top-auth (default/b)")
	require.Equal(t, beforeDelete["ingresses/a"]+len(nginxRootPublicationComponents), fixture.engine.executionCounts()["ingresses/a"])
	require.Equal(t, beforeDelete["ingresses/b"], fixture.engine.executionCounts()["ingresses/b"])
}

func TestNginxRootPublicationFailuresCannotPoisonCommittedState(t *testing.T) {
	fixture := newNginxRootPublicationFixture(t)
	fixture.addSecret(t, nginxRootPublicationSecret("auth"))
	fixture.addIngress(t, nginxRootPublicationIngress("subject", true, "v1"))
	baseline := fixture.renderAndCommit(t)
	baselineSnapshot := fixture.service.incremental.snapshot

	fixture.updateIngress(t, nginxRootPublicationIngress("subject", true, "v2"))
	fixture.config.TemplatingSettings.ExtraContext["failAfterReplay"] = true
	failed, err := fixture.render(rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after nginx root publications")
	assert.Nil(t, failed)
	assert.Same(t, baselineSnapshot, fixture.service.incremental.snapshot)
	afterFailure := fixture.engine.executionCounts()["ingresses/subject"]

	fixture.config.TemplatingSettings.ExtraContext["failAfterReplay"] = false
	retried := fixture.renderAndCommit(t)
	require.NotEqual(t, baseline.HAProxyConfig, retried.HAProxyConfig)
	requireNginxRootPublicationOutput(t, retried, "subject", "v2")
	require.Equal(t, afterFailure+len(nginxRootPublicationComponents), fixture.engine.executionCounts()["ingresses/subject"])
	committedSnapshot := fixture.service.incremental.snapshot

	proposed := nginxRootPublicationIngress("subject", true, "admission")
	proposed["metadata"].(map[string]any)["annotations"].(map[string]any)["nginx.ingress.kubernetes.io/app-root"] = "/valid\nPOISON"
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	admission, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("ingresses", "default", "subject"),
	)
	require.ErrorContains(t, err, "must not contain control characters")
	assert.Nil(t, admission)
	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	afterAdmission := fixture.engine.executionCounts()

	afterRejected := fixture.renderAndCommit(t)
	require.Equal(t, retried.HAProxyConfig, afterRejected.HAProxyConfig)
	require.Equal(t, requireRenderEvents(t, retried), requireRenderEvents(t, afterRejected))
	require.Equal(t, afterAdmission, fixture.engine.executionCounts())
}

func newNginxRootPublicationFixture(t *testing.T) *nginxRootPublicationFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"nginxHttpRedirectCode": "308",
			"failAfterReplay":       false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1", Resources: "ingresses",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"services": {
				APIVersion: "v1", Resources: "services",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"endpoints": {
				APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices",
				IndexBy: []string{"metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"},
			},
			"secrets": {
				APIVersion: "v1", Resources: "secrets",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: loadNginxRootPublicationSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: nginxRootPublicationRoot},
	}
	raceScaleRenderTimeout(cfg)
	service, engine := newNginxRootPublicationService(t, cfg)
	ingresses := k8sstore.NewMemoryStore(2)
	secrets := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": ingresses,
		"services":  k8sstore.NewMemoryStore(2),
		"endpoints": k8sstore.NewMemoryStore(2),
		"secrets":   secrets,
	})
	return &nginxRootPublicationFixture{
		config: cfg, service: service, engine: engine,
		ingresses: ingresses, secrets: secrets, provider: provider,
	}
}

func newNginxRootPublicationService(
	t *testing.T,
	cfg *config.Config,
) (*RenderService, *dynamicBindingCountingEngine) {
	t.Helper()
	types := ingressBackendSchemaTypes(t)
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
	return service, engine
}

func loadNginxRootPublicationSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	files := []string{
		"base/library.yaml",
		"nginx-ingress/30-features.yaml",
		"nginx-ingress/40-auth-spoe.yaml",
	}
	wanted := map[string]bool{
		"util-config-injection-kind":              true,
		"features-105-nginx-ingress-ssl-redirect": true,
		"nginx-ingress-ssl-redirect-publications": true,
		"features-140-nginx-ingress-redirects":    true,
		"nginx-ingress-redirect-publications":     true,
		"features-145-nginx-ingress-app-root":     true,
		"nginx-ingress-app-root-publications":     true,
		"map-auth-url-500-nginx-ingress":          true,
		"map-auth-signin-500-nginx-ingress":       true,
		"map-auth-method-500-nginx-ingress":       true,
		"global-top-700-nginx-ingress-auth":       true,
		"util-nginx-ingress-auth-userlist":        true,
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
			snippet := config.TemplateSnippet{
				Name: name, Template: chartSnippet.Template, Requires: chartSnippet.Requires,
			}
			if chartSnippet.Incremental != nil {
				snippet.Incremental = &config.IncrementalTemplate{
					Source:            chartSnippet.Incremental.Source,
					BindingsTemplate:  chartSnippet.Incremental.BindingsTemplate,
					WhenAnyPathExists: chartSnippet.Incremental.WhenAnyPathExists,
					Group:             chartSnippet.Incremental.Group,
					Consumes:          chartSnippet.Incremental.Consumes,
					OptionalConsumes:  chartSnippet.Incremental.OptionalConsumes,
					Effects:           chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func nginxRootPublicationIngress(name string, active bool, revision string) map[string]any {
	annotations := map[string]any{}
	if active {
		annotations = map[string]any{
			"nginx.ingress.kubernetes.io/ssl-redirect":            "true",
			"nginx.ingress.kubernetes.io/permanent-redirect":      "https://permanent.example/" + revision,
			"nginx.ingress.kubernetes.io/permanent-redirect-code": "308",
			"nginx.ingress.kubernetes.io/temporal-redirect":       "https://temporal.example/" + revision,
			"nginx.ingress.kubernetes.io/temporal-redirect-code":  "307",
			"nginx.ingress.kubernetes.io/app-root":                "/app-" + revision,
			"nginx.ingress.kubernetes.io/auth-url":                "https://auth.example/check-" + revision,
			"nginx.ingress.kubernetes.io/auth-signin":             "https://login.example/start-" + revision,
			"nginx.ingress.kubernetes.io/auth-method":             "POST",
			"nginx.ingress.kubernetes.io/auth-type":               "basic",
			"nginx.ingress.kubernetes.io/auth-secret":             "auth",
			"nginx.ingress.kubernetes.io/auth-secret-type":        "auth-map",
		}
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        name,
			"annotations": annotations,
			"labels":      map[string]any{"test-revision": revision},
		},
		"spec": map[string]any{
			"rules": []any{map[string]any{
				"host": name + "-" + revision + ".example.test",
				"http": map[string]any{"paths": []any{}},
			}},
		},
	}
}

func nginxRootPublicationSecret(name string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      name,
		},
		"data": map[string]any{
			"alice": base64.StdEncoding.EncodeToString([]byte("$2y$05$abcdefghijklmnopqrstuv")),
		},
	}
}

func requireNginxRootPublicationOutput(t *testing.T, result *RenderResult, name, revision string) {
	t.Helper()
	host := name + "-" + revision + ".example.test"
	require.Contains(t, result.HAProxyConfig, "SSL|"+host+"|308")
	require.Contains(t, result.HAProxyConfig, "REDIRECT|"+host+"|https://permanent.example/"+revision+"|308")
	require.Contains(t, result.HAProxyConfig, "REDIRECT|"+host+"|https://temporal.example/"+revision+"|307")
	require.Contains(t, result.HAProxyConfig, "APP-ROOT|"+host+"|/app-"+revision)
	require.Contains(t, result.HAProxyConfig, "default/"+name+" https://auth.example/check-"+revision)
	require.Contains(t, result.HAProxyConfig, "default/"+name+" https://login.example/start-"+revision)
	require.Contains(t, result.HAProxyConfig, "default/"+name+" POST")
	require.Contains(t, result.HAProxyConfig, "userlist ni_auth_default_auth")
	require.Contains(t, result.HAProxyConfig, "user alice password '$2y$05$abcdefghijklmnopqrstuv'")
}

func nginxRootPublicationComponentExecutions(executions uint64) map[string]uint64 {
	result := make(map[string]uint64, len(nginxRootPublicationComponents))
	for _, component := range nginxRootPublicationComponents {
		result[component] = executions
	}
	return result
}

func (f *nginxRootPublicationFixture) componentExecutions(name string) map[string]uint64 {
	result := make(map[string]uint64, len(nginxRootPublicationComponents))
	for _, componentName := range nginxRootPublicationComponents {
		component := f.service.incremental.components[componentName]
		query := componentQueryKey(&component, "ingresses", "default", name)
		result[componentName] = f.service.incremental.graph.Counters(query).Executions
	}
	return result
}

func (f *nginxRootPublicationFixture) addIngress(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(resource, []string{"default", name}))
}

func (f *nginxRootPublicationFixture) updateIngress(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Update(resource, []string{"default", name}))
}

func (f *nginxRootPublicationFixture) deleteIngress(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, f.ingresses.Delete("default", name, []string{"default", name}))
}

func (f *nginxRootPublicationFixture) addSecret(t *testing.T, resource map[string]any) {
	t.Helper()
	name := resource["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.secrets.Add(resource, []string{"default", name}))
}

func (f *nginxRootPublicationFixture) render(mode rendercontext.RenderMode) (*RenderResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return f.service.Render(ctx, f.provider, mode)
}

func (f *nginxRootPublicationFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.render(rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}
