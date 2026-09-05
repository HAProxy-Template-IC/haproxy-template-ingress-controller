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

const nginxFrontendPublicationRoot = `{%- var incremental = render "frontend-filters-555-nginx-ingress-mirror" +
  render "frontend-filters-730-nginx-ingress-cors" +
  render "frontend-filters-780-nginx-ingress-canary" -%}
{%- var legacy = render "legacy-nginx-ingress-mirror" +
  render "legacy-nginx-ingress-cors-root" +
  render "legacy-nginx-ingress-canary" -%}
{{ "BEGIN-I\n" }}{{ incremental }}{{ "\nEND-I\nBEGIN-L\n" -}}
{{ legacy }}
{{ "\nEND-L\n" -}}
{%- if tostring(extraContext | dig("poisonRead") | fallback(false)) == "true" -%}
  {%- var files = incremental_values("nginx-ingress-cors", "files") -%}
  {%- if len(files) > 0 -%}{%- files[0].(map[string]any)["content"] = "poison" -%}{%- end -%}
{%- end -%}
{%- if tostring(extraContext | dig("failAfterReplay") | fallback(false)) == "true" -%}
  {{- fail("forced failure after nginx frontend replay") -}}
{%- end -%}`

const legacyNginxMirrorTemplate = `{%- import "util-ingress-helpers" for HostMatchCondition -%}
{%- import "util-validate-config-value" for ValidateConfigValue -%}
{%- import "util-webhook-reject-or-warn" for WebhookRejectOrWarn -%}
{#- Only 'mirror-target' is honoured: the plugin forces the mirrored Host to the
    target authority and always forwards the buffered body, so 'mirror-host' and
    'mirror-request-body: off' cannot be supported without a plugin change. Only
    the authority is used — the plugin re-attaches the live path/query.
    Entries accumulate in the per-request txn.gw_mirror_targets list that the
    single NOTIFY in frontend-filters-950-spoa-hub-mirror-fire fans out, so a new
    target touches neither spoe.conf nor the hub TOML. -#}
{%- for _, ingress := range resources.ingresses.List() %}
  {%- var mt = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/mirror-target"] %}
  {%- if mt != "" %}
    {%- var ns = ingress.Metadata.Namespace %}
    {%- var name = ingress.Metadata.Name %}
    {%- var key = ns + "/" + name %}
    {%- var hosts = []string{} %}
    {%- for _, rule := range ingress.Spec.Rules %}
      {%- if rule.Host != "" %}{%- hosts = append(hosts, rule.Host) %}{%- end %}
    {%- end %}
    {%- if len(hosts) == 0 %}
      {{- WebhookRejectOrWarn(ingress, "InvalidAnnotationValue", "Ingress '" + key + "' sets 'nginx.ingress.kubernetes.io/mirror-target' but defines no host; host-less / default-backend mirroring is not supported. Add a host to the Ingress rule.") -}}{%- continue %}
    {%- end %}
    {#- nginx appends the path/variable (e.g. $request_uri) with no '/' separator,
        so the authority ends at the first '/' or '$'. -#}
    {%- var scheme = "http" %}
    {%- var rest = mt %}
    {%- if hasPrefix(mt, "https://") %}
      {%- scheme = "https" %}
      {%- rest = mt[8:] %}
    {%- else if hasPrefix(mt, "http://") %}
      {%- rest = mt[7:] %}
    {%- end %}
    {%- var cut = len(rest) %}
    {%- var sl = index(rest, "/") %}
    {%- var dl = index(rest, "$") %}
    {%- if sl >= 0 && sl < cut %}{%- cut = sl %}{%- end %}
    {%- if dl >= 0 && dl < cut %}{%- cut = dl %}{%- end %}
    {%- var authority = rest[:cut] %}
    {%- if !strings_contains(authority, ":") %}
      {%- if scheme == "https" %}{%- authority = authority + ":443" %}{%- else %}{%- authority = authority + ":80" %}{%- end %}
    {%- end %}
    {%- if !regex_search(authority, "^[A-Za-z0-9._-]+:[0-9]{1,5}$") %}
      {{- fail("Invalid value '" + mt + "' for annotation 'nginx.ingress.kubernetes.io/mirror-target' on Ingress '" + key + "'. Expected scheme://host[:port][/path]; could not derive a host:port authority (got '" + authority + "').") -}}
    {%- end %}
    {#- The charset regex above still accepts :0 and :99999. -#}
    {%- var portNum = toint(authority[index(authority, ":")+1:]) %}
    {%- if portNum < 1 || portNum > 65535 %}
      {{- fail("Invalid port '" + tostring(portNum) + "' in 'nginx.ingress.kubernetes.io/mirror-target' on Ingress '" + key + "'. Port must be 1-65535.") -}}
    {%- end %}
    {%- var safeAuthority = ValidateConfigValue(authority, "nginx.ingress.kubernetes.io/mirror-target", key, false) %}
    {%- var cond = HostMatchCondition(hosts) %}
    {%- var timeoutMs = toint(extraContext | dig("spoaHub", "mirror", "targetTimeoutMs") | fallback(2000)) %}
    {%- var retriesCount = toint(extraContext | dig("spoaHub", "mirror", "targetRetries") | fallback(0)) %}
    {%- var entry = scheme + "|" + safeAuthority + "|" + tostring(timeoutMs) + "|" + tostring(retriesCount) %}
# nginx-ingress/mirror-target ({{ key }})
http-request set-var(txn.gw_mirror_targets) str({{ entry }};),concat(,txn.gw_mirror_targets,) if {{ cond }}
  {%- end %}
{%- end %}
`

const legacyNginxCanaryTemplate = `{# HAProxy takes the first matching use_backend, so emission order is the
    documented canary precedence: header > cookie > weight. #}
{%- import "util-ingress-helpers" for HostMatchCondition -%}
{%- import "util-backend-name-ingress" for BackendNameIngress -%}
{%- import "util-config-injection-kind" for ConfigInjectionKind -%}
{%- import "util-webhook-reject-or-warn" for WebhookRejectOrWarn -%}
{%- for _, ingress := range resources.ingresses.List() %}
  {%- var isCanary = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/canary"] %}
  {%- if isCanary == "true" %}
    {%- var ns = ingress.Metadata.Namespace %}
    {%- var name = ingress.Metadata.Name %}
    {%- var key = ns + "/" + name %}
      {%- var hosts = []string{} %}
      {%- var canaryBackend = "" %}
      {%- for _, rule := range ingress.Spec.Rules %}
        {%- if rule.Host != "" %}{%- hosts = append(hosts, rule.Host) %}{%- end %}
        {%- if canaryBackend == "" %}
          {%- var http = rule | dig("http") %}
          {%- if http != nil %}
            {%- var paths []any = http | dig("paths") | toSlice() %}
            {%- if len(paths) > 0 %}
              {%- canaryBackend = BackendNameIngress(ingress, paths[0]) %}
            {%- end %}
          {%- end %}
        {%- end %}
      {%- end %}
      {%- if len(hosts) > 0 && canaryBackend != "" %}
        {%%
          var cond = HostMatchCondition(hosts)
          var canaryHeader = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/canary-by-header"]
          var canaryHeaderValue = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/canary-by-header-value"]
          var canaryHeaderPattern = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"]
          var canaryCookie = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/canary-by-cookie"]
          var canaryWeight = ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/canary-weight"]
        %%}
        {#- The header/cookie NAMES and the numeric weight are emitted unquoted
            (req.hdr(<h>), req.cook(<c>), rand(100) lt <w>), so a space or quote
            there breaks out of the ACL — token context. The regex/exact match
            VALUES are single-quoted below (-m reg '<p>' / -m str '<v>'), where
            HAProxy strong-quoting passes a backslash (e.g. \d+) literally with no
            $ expansion — only a ' or a control character is a danger, so squote.
            Only the operands that will actually be emitted are checked. -#}
        {%- var canaryInj = "" %}
        {%- var canaryField = "" %}
        {%- if canaryHeader != "" %}
          {%- canaryInj = ConfigInjectionKind(canaryHeader, "token") %}
          {%- if canaryInj != "" %}{%- canaryField = "canary-by-header" %}{%- end %}
          {%- if canaryInj == "" && canaryHeaderPattern != "" %}
            {%- canaryInj = ConfigInjectionKind(canaryHeaderPattern, "squote") %}
            {%- if canaryInj != "" %}{%- canaryField = "canary-by-header-pattern" %}{%- end %}
          {%- else if canaryInj == "" && canaryHeaderValue != "" %}
            {%- canaryInj = ConfigInjectionKind(canaryHeaderValue, "squote") %}
            {%- if canaryInj != "" %}{%- canaryField = "canary-by-header-value" %}{%- end %}
          {%- end %}
        {%- end %}
        {%- if canaryInj == "" && canaryCookie != "" %}
          {%- canaryInj = ConfigInjectionKind(canaryCookie, "token") %}
          {%- if canaryInj != "" %}{%- canaryField = "canary-by-cookie" %}{%- end %}
        {%- end %}
        {%- if canaryInj == "" && canaryWeight != "" %}
          {%- canaryInj = ConfigInjectionKind(canaryWeight, "token") %}
          {%- if canaryInj != "" %}{%- canaryField = "canary-weight" %}{%- end %}
        {%- end %}
        {%- if canaryInj != "" %}
          {{- WebhookRejectOrWarn(ingress, "InvalidAnnotationValue", "Ingress '" + key + "' annotation 'nginx.ingress.kubernetes.io/" + canaryField + "' value contains " + canaryInj + ", which would break out of the canary routing condition; the canary route is not applied. Remove it from the value.") -}}
          {%- continue %}
        {%- end %}
# nginx-ingress/canary ({{ key }})
        {%- if canaryHeader != "" %}
          {%- if canaryHeaderPattern != "" %}
use_backend {{ canaryBackend }} if { req.hdr({{ canaryHeader }}) -m reg '{{ canaryHeaderPattern }}' } {{ cond }}
          {%- else if canaryHeaderValue != "" %}
use_backend {{ canaryBackend }} if { req.hdr({{ canaryHeader }}) -m str '{{ canaryHeaderValue }}' } {{ cond }}
          {%- else %}
use_backend {{ canaryBackend }} if { req.hdr({{ canaryHeader }}) -m str always } {{ cond }}
          {%- end %}
        {%- end %}
        {%- if canaryCookie != "" %}
use_backend {{ canaryBackend }} if { req.cook({{ canaryCookie }}) -m str always } {{ cond }}
        {%- end %}
        {%- if canaryWeight != "" %}
use_backend {{ canaryBackend }} if { rand(100) lt {{ canaryWeight }} } {{ cond }}
        {%- end %}
      {%- end %}
  {%- end %}
{%- end -%}
`

const legacyNginxCORSRootTemplate = `{%- import "util-emit-annotation-cors" for EmitAnnotationCORS -%}
{%- for _, ingress := range resources.ingresses.List() -%}
  {#- No enable annotation → the macro emits nothing; skip the call. -#}
  {%- if ingress.Metadata.Annotations["nginx.ingress.kubernetes.io/enable-cors"] == "" %}{%- continue %}{%- end %}
  {{ EmitAnnotationCORS(ingress,
      "nginx.ingress.kubernetes.io",
      "nginx.ingress.kubernetes.io/enable-cors",
      "1728000",
      "nginx-ingress/cors") }}
{%- end -%}
`

type nginxFrontendPublicationFixture struct {
	config    *config.Config
	service   *RenderService
	engine    *dynamicBindingCountingEngine
	ingresses *k8sstore.MemoryStore
	services  *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

type nginxFrontendSnapshot struct {
	config string
	files  map[string]string
}

func TestNginxFrontendPublicationsPreserveColdBytesAndLargeHostFiles(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	fixture.addIngress(t, nginxFrontendIngress("subject", nginxFrontendHosts(31), true, "v1"))

	first := fixture.renderAndCommit(t)
	requireNginxFrontendDifferential(t, first)
	firstFiles := requireAuxiliaryFiles(t, first)
	require.Len(t, firstFiles.GeneralFiles, 1)
	expectedContent := strings.Join(nginxFrontendHosts(31), "\n") + "\n"
	assert.Equal(t, expectedContent, firstFiles.GeneralFiles[0].GetContent())
	assert.Contains(t, first.HAProxyConfig, "-f files/host-match-")
	assert.Equal(t, 3, fixture.engine.executionCounts()["ingresses/subject"])

	beforeWarm := fixture.engine.executionCounts()
	warm := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, first), nginxFrontendResultSnapshot(t, warm))
	require.Equal(t, beforeWarm, fixture.engine.executionCounts())

	fixture.addIngress(t, nginxFrontendIngress("unrelated", []string{"unrelated.example.com"}, false, "v1"))
	unrelated := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, first), nginxFrontendResultSnapshot(t, unrelated))
	require.Empty(t, fixture.engine.executionCounts()["ingresses/unrelated"])

	fixture.updateIngress(t, nginxFrontendIngress("unrelated", []string{"changed.example.com"}, false, "v2"))
	unrelatedChanged := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, first), nginxFrontendResultSnapshot(t, unrelatedChanged))
	require.Empty(t, fixture.engine.executionCounts()["ingresses/unrelated"])

	beforeChanged := fixture.engine.executionCounts()
	changedHosts := nginxFrontendHosts(31)
	changedHosts[0] = "changed.example.com"
	fixture.updateIngress(t, nginxFrontendIngress("subject", changedHosts, true, "v2"))
	changed := fixture.renderAndCommit(t)
	requireNginxFrontendDifferential(t, changed)
	assert.Equal(t, beforeChanged["ingresses/subject"]+3, fixture.engine.executionCounts()["ingresses/subject"])
	assert.Equal(t, strings.Join(changedHosts, "\n")+"\n", requireAuxiliaryFiles(t, changed).GeneralFiles[0].GetContent())
}

func TestNginxFrontendPublicationDeletionAndFileCollisionPromotion(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	hosts := nginxFrontendHosts(31)
	fixture.addIngress(t, nginxFrontendIngress("a", hosts, true, "v1"))
	fixture.addIngress(t, nginxFrontendIngress("b", hosts, true, "v1"))

	first := fixture.renderAndCommit(t)
	requireNginxFrontendDifferential(t, first)
	require.Len(t, requireAuxiliaryFiles(t, first).GeneralFiles, 1)
	assert.Contains(t, first.HAProxyConfig, "nginx-ingress/mirror-target (default/a)")
	assert.Contains(t, first.HAProxyConfig, "nginx-ingress/mirror-target (default/b)")

	fixture.deleteIngress(t, "a")
	promoted := fixture.renderAndCommit(t)
	requireNginxFrontendDifferential(t, promoted)
	require.Len(t, requireAuxiliaryFiles(t, promoted).GeneralFiles, 1)
	assert.NotContains(t, promoted.HAProxyConfig, "(default/a)")
	assert.Contains(t, promoted.HAProxyConfig, "(default/b)")

	fixture.deleteIngress(t, "b")
	empty := fixture.renderAndCommit(t)
	requireNginxFrontendDifferential(t, empty)
	assert.Empty(t, requireAuxiliaryFiles(t, empty).GeneralFiles)
	assert.NotContains(t, empty.HAProxyConfig, "nginx-ingress/mirror-target")
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
	require.Equal(t, beforeChanged["ingresses/subject"]+3, fixture.engine.executionCounts()["ingresses/subject"])
}

func TestNginxFrontendFailedRootAndAdmissionCannotPoisonCache(t *testing.T) {
	fixture := newNginxFrontendPublicationFixture(t)
	baselineResource := nginxFrontendIngress("subject", nginxFrontendHosts(31), true, "v1")
	fixture.addIngress(t, baselineResource)
	baseline := fixture.renderAndCommit(t)

	fixture.config.TemplatingSettings.ExtraContext["poisonRead"] = true
	poisoned, err := fixture.render(rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "template mutates an immutable input")
	assert.Nil(t, poisoned)
	fixture.config.TemplatingSettings.ExtraContext["poisonRead"] = false
	beforeWarm := fixture.engine.executionCounts()
	afterPoison := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, baseline), nginxFrontendResultSnapshot(t, afterPoison))
	require.Equal(t, beforeWarm, fixture.engine.executionCounts())

	changedResource := nginxFrontendIngress("subject", append([]string{"changed.example.com"}, nginxFrontendHosts(30)...), true, "v2")
	fixture.updateIngress(t, changedResource)
	fixture.config.TemplatingSettings.ExtraContext["failAfterReplay"] = true
	failed, err := fixture.render(rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after nginx frontend replay")
	assert.Nil(t, failed)
	afterFailure := fixture.engine.executionCounts()
	fixture.config.TemplatingSettings.ExtraContext["failAfterReplay"] = false
	retried := fixture.renderAndCommit(t)
	requireNginxFrontendDifferential(t, retried)
	require.Equal(t, afterFailure["ingresses/subject"]+3, fixture.engine.executionCounts()["ingresses/subject"])

	invalid := nginxFrontendIngress("subject", nginxFrontendHosts(31), true, "v3")
	invalid["metadata"].(map[string]any)["annotations"].(map[string]any)["nginx.ingress.kubernetes.io/cors-max-age"] = "1\t2"
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
	require.ErrorContains(t, err, "would split the frontend directive")
	assert.Nil(t, admission)
	afterAdmission := fixture.engine.executionCounts()
	baseAfterAdmission := fixture.renderAndCommit(t)
	require.Equal(t, nginxFrontendResultSnapshot(t, retried), nginxFrontendResultSnapshot(t, baseAfterAdmission))
	require.Equal(t, afterAdmission, fixture.engine.executionCounts())
}

func newNginxFrontendPublicationFixture(t *testing.T) *nginxFrontendPublicationFixture {
	t.Helper()
	snippets := loadNginxFrontendPublicationSnippets(t)
	snippets["legacy-nginx-ingress-mirror"] = config.TemplateSnippet{
		Name: "legacy-nginx-ingress-mirror", Template: legacyNginxMirrorTemplate,
	}
	snippets["legacy-nginx-ingress-canary"] = config.TemplateSnippet{
		Name: "legacy-nginx-ingress-canary", Template: legacyNginxCanaryTemplate,
	}
	snippets["legacy-nginx-ingress-cors-root"] = config.TemplateSnippet{
		Name: "legacy-nginx-ingress-cors-root", Template: legacyNginxCORSRootTemplate,
	}
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
		TemplateSnippets: snippets,
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
	services := k8sstore.NewMemoryStore(2)
	endpoints := k8sstore.NewMemoryStore(2)
	secrets := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"ingresses": ingresses, "services": services, "endpoints": endpoints, "secrets": secrets,
	})
	return &nginxFrontendPublicationFixture{
		config: cfg, service: service, engine: engine, ingresses: ingresses, services: services, provider: provider,
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
		"util-ingress-annotation-cors-fragment": true, "util-emit-annotation-cors": true,
		"frontend-filters-555-nginx-ingress-mirror": true, "nginx-ingress-mirror-publications": true,
		"frontend-filters-730-nginx-ingress-cors": true, "nginx-ingress-cors-publications": true,
		"frontend-filters-780-nginx-ingress-canary": true, "nginx-ingress-canary-publications": true,
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
			"nginx.ingress.kubernetes.io/enable-cors":            "true",
			"nginx.ingress.kubernetes.io/canary":                 "true",
			"nginx.ingress.kubernetes.io/canary-by-header":       "X-Canary",
			"nginx.ingress.kubernetes.io/canary-by-header-value": "always",
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

func nginxFrontendResultSnapshot(t *testing.T, result *RenderResult) nginxFrontendSnapshot {
	t.Helper()
	snapshot := nginxFrontendSnapshot{config: result.HAProxyConfig, files: map[string]string{}}
	for _, file := range requireAuxiliaryFiles(t, result).GeneralFiles {
		snapshot.files[file.GetIdentifier()] = file.GetContent()
	}
	return snapshot
}

func requireNginxFrontendDifferential(t *testing.T, result *RenderResult) {
	t.Helper()
	text := result.HAProxyConfig
	incrementalStart := strings.Index(text, "BEGIN-I\n")
	incrementalEnd := strings.Index(text, "\nEND-I\n")
	legacyStart := strings.Index(text, "BEGIN-L\n")
	legacyEnd := strings.Index(text, "\nEND-L\n")
	require.NotEqual(t, -1, incrementalStart)
	require.NotEqual(t, -1, incrementalEnd)
	require.NotEqual(t, -1, legacyStart)
	require.NotEqual(t, -1, legacyEnd)
	incremental := text[incrementalStart+len("BEGIN-I\n") : incrementalEnd]
	legacy := text[legacyStart+len("BEGIN-L\n") : legacyEnd]
	require.Equal(t, legacy, incremental)
}
