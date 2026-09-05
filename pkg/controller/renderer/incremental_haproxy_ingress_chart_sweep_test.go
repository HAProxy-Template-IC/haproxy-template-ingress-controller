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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const haproxyIngressSweepFailure = `{%- if tostring(extraContext | dig("failAfterAuth") | fallback(false)) == "true" -%}{{ fail("forced HAProxy Ingress root failure") }}{%- end -%}`

var haproxyIngressSnippetDependencyPattern = regexp.MustCompile(`(?:import|render)\s+"([^"]+)"`)

func TestHAProxyIngressIncrementalSweepScalesWithChangedResources(t *testing.T) {
	for _, resourceCount := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("resources-%d", resourceCount), func(t *testing.T) {
			snippets, components := loadHAProxyIngressSweepSnippets(t)
			fixture := newHAProxyTechFixtureWithSnippets(t, haproxyIngressSweepRoot(components), snippets)
			for index := range resourceCount {
				fixture.addIngress(t, haproxyTechIngress(fmt.Sprintf("inactive-%04d", index), nil, "v1"))
			}
			target := fmt.Sprintf("inactive-%04d", resourceCount/2)

			cold := fixture.renderAndCommit(t)
			assert.Empty(t, fixture.engine.executionCounts())
			warm := fixture.renderAndCommit(t)
			assert.Equal(t, cold.HAProxyConfig, warm.HAProxyConfig)
			assert.Empty(t, fixture.engine.executionCounts())

			fixture.updateIngress(t, haproxyTechIngress(target, nil, "v2"))
			unrelated := fixture.renderAndCommit(t)
			assert.Equal(t, cold.HAProxyConfig, unrelated.HAProxyConfig)
			assert.Empty(t, fixture.engine.executionCounts())

			fixture.updateIngress(t, haproxyTechIngress(target, map[string]any{
				"haproxy-ingress.github.io/forwardfor": "add",
			}, "v3"))
			active := fixture.renderAndCommit(t)
			assert.Contains(t, active.HAProxyConfig, "# haproxy-ingress/forwardfor (default/"+target+")")
			assert.Equal(t, map[string]int{"ingresses/" + target: 1}, fixture.engine.executionCounts())
			assert.Equal(t, active.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
			assert.Equal(t, map[string]int{"ingresses/" + target: 1}, fixture.engine.executionCounts())
		})
	}
}

func TestHAProxyIngressIncrementalSweepMatchesDetachedLegacy(t *testing.T) {
	snippets, components := loadHAProxyIngressSweepSnippets(t)
	current := newHAProxyTechFixtureWithSnippets(t, haproxyIngressSweepRoot(components), snippets)
	legacy := newHAProxyTechFixtureWithSnippets(t, `{{- render "legacy-haproxy-ingress-forwardfor" -}}`, map[string]config.TemplateSnippet{
		"legacy-haproxy-ingress-forwardfor": {
			Name: "legacy-haproxy-ingress-forwardfor",
			Template: `{%- for _, ingress := range resources.ingresses.List() %}
  {%- var key = ingress.Metadata.Namespace + "/" + ingress.Metadata.Name %}
  {%- var forwardfor = ingress.Metadata.Annotations["haproxy-ingress.github.io/forwardfor"] %}
  {%- if forwardfor != "" && forwardfor != "ignore" %}
    {%- var cond = "{ var(txn.resource_id) -m str " + key + " }" %}
# haproxy-ingress/forwardfor ({{ key }})
    {%- if forwardfor == "ifmissing" %}
http-request set-header X-Forwarded-For %[src] if !{ req.hdr(X-Forwarded-For) -m found } {{ cond }}
    {%- else if forwardfor == "update" %}
http-request set-header X-Forwarded-For %[src] if {{ cond }}
    {%- else %}
http-request add-header X-Forwarded-For %[src] if {{ cond }}
    {%- end %}
  {%- end %}
{%- end -%}`,
		},
	})
	legacy.provider = current.provider

	current.addIngress(t, haproxyTechIngress("z", map[string]any{
		"haproxy-ingress.github.io/forwardfor": "ifmissing",
	}, "v1"))
	current.addIngress(t, haproxyTechIngress("a", map[string]any{
		"haproxy-ingress.github.io/forwardfor": "update",
	}, "v1"))
	assert.Equal(t, legacy.renderAndCommit(t).HAProxyConfig, current.renderAndCommit(t).HAProxyConfig)

	current.updateIngress(t, haproxyTechIngress("z", map[string]any{
		"haproxy-ingress.github.io/forwardfor": "add",
	}, "v2"))
	assert.Equal(t, legacy.renderAndCommit(t).HAProxyConfig, current.renderAndCommit(t).HAProxyConfig)
	current.deleteIngress(t, "a")
	assert.Equal(t, legacy.renderAndCommit(t).HAProxyConfig, current.renderAndCommit(t).HAProxyConfig)
}

func TestHAProxyIngressIncrementalSweepAbortsDoNotPoisonCache(t *testing.T) {
	snippets, components := loadHAProxyIngressSweepSnippets(t)
	fixture := newHAProxyTechFixtureWithSnippets(t, haproxyIngressSweepRoot(components), snippets)
	fixture.addIngress(t, haproxyTechIngress("subject", nil, "v1"))
	baseline := fixture.renderAndCommit(t)
	assert.Empty(t, fixture.engine.executionCounts())

	proposed := haproxyTechIngress("subject", map[string]any{
		"haproxy-ingress.github.io/forwardfor": "add",
	}, "admission")
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
	require.NoError(t, err)
	assert.Contains(t, admission.HAProxyConfig, "haproxy-ingress/forwardfor")
	assert.Equal(t, map[string]int{"ingresses/subject": 1}, fixture.engine.executionCounts())
	assert.Equal(t, baseline.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, map[string]int{"ingresses/subject": 1}, fixture.engine.executionCounts())

	fixture.updateIngress(t, proposed)
	fixture.config.TemplatingSettings.ExtraContext["failAfterAuth"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced HAProxy Ingress root failure")
	assert.Nil(t, failed)
	assert.Equal(t, map[string]int{"ingresses/subject": 2}, fixture.engine.executionCounts())

	fixture.config.TemplatingSettings.ExtraContext["failAfterAuth"] = false
	retried := fixture.renderAndCommit(t)
	assert.Contains(t, retried.HAProxyConfig, "haproxy-ingress/forwardfor")
	assert.Equal(t, map[string]int{"ingresses/subject": 3}, fixture.engine.executionCounts())
	assert.Equal(t, retried.HAProxyConfig, fixture.renderAndCommit(t).HAProxyConfig)
	assert.Equal(t, map[string]int{"ingresses/subject": 3}, fixture.engine.executionCounts())
}

func loadHAProxyIngressSweepSnippets(t *testing.T) (snippets map[string]config.TemplateSnippet, components []string) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	haproxyFiles, err := filepath.Glob(filepath.Join(chartRoot, "haproxy-ingress", "*.yaml"))
	require.NoError(t, err)
	dependencyFiles := []string{
		filepath.Join(chartRoot, "base", "library.yaml"),
		filepath.Join(chartRoot, "ingress", "library.yaml"),
		filepath.Join(chartRoot, "ingress-annotations-compat", "library.yaml"),
		filepath.Join(chartRoot, "kubernetes-backends", "library.yaml"),
		filepath.Join(chartRoot, "spoa-hub", "10-features.yaml"),
	}

	all := map[string]haproxyTechChartSnippet{}
	owners := map[string]string{}
	for _, path := range append(dependencyFiles, haproxyFiles...) {
		content, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		if strings.Contains(filepath.ToSlash(path), "/haproxy-ingress/") {
			assert.NotContains(t, string(content), "resources.ingresses.List()", path)
		}
		var library haproxyTechChartLibrary
		require.NoError(t, yaml.Unmarshal(content, &library), path)
		for name, snippet := range library.TemplateSnippets {
			all[name] = snippet
			owners[name] = path
		}
	}

	selected := map[string]bool{}
	components = []string{}
	for name, snippet := range all {
		if !strings.Contains(filepath.ToSlash(owners[name]), "/haproxy-ingress/") || snippet.Incremental == nil {
			continue
		}
		selected[name] = true
		components = append(components, name)
	}
	require.NotEmpty(t, components)
	sort.Strings(components)

	expandHAProxyIngressSweepSelection(t, all, selected, components)
	return haproxyIngressSweepConfigSnippets(all, selected), components
}

func expandHAProxyIngressSweepSelection(
	t *testing.T,
	all map[string]haproxyTechChartSnippet,
	selected map[string]bool,
	components []string,
) {
	t.Helper()
	queue := append([]string(nil), components...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		snippet, found := all[name]
		require.True(t, found, name)
		for _, source := range []string{snippet.Template, func() string {
			if snippet.Incremental == nil {
				return ""
			}
			return snippet.Incremental.BindingsTemplate
		}()} {
			for _, match := range haproxyIngressSnippetDependencyPattern.FindAllStringSubmatch(source, -1) {
				dependency := match[1]
				if selected[dependency] {
					continue
				}
				_, exists := all[dependency]
				require.True(t, exists, "%s imports missing snippet %s", name, dependency)
				selected[dependency] = true
				queue = append(queue, dependency)
			}
		}
	}
}

func haproxyIngressSweepConfigSnippets(
	all map[string]haproxyTechChartSnippet,
	selected map[string]bool,
) map[string]config.TemplateSnippet {
	result := make(map[string]config.TemplateSnippet, len(selected))
	for name := range selected {
		chartSnippet := all[name]
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
	return result
}

func haproxyIngressSweepRoot(components []string) string {
	var root strings.Builder
	for _, name := range components {
		fmt.Fprintf(&root, `{{- render %q -}}`, name)
	}
	root.WriteString(haproxyIngressSweepFailure)
	return root.String()
}
