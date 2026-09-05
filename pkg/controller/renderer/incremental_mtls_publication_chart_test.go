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

var mtlsPublicationComponents = []string{
	"mtls-host-policies-100-haproxy-ingress",
	"mtls-host-policies-200-haptic-annotations",
	"mtls-host-policies-300-nginx-ingress",
	"mtls-host-policies-400-haptic-annotations-error",
	"mtls-host-policies-500-haproxy-ingress-error",
	"mtls-host-policies-600-nginx-ingress-error",
}

const mtlsPublicationChartRoot = `{{- render "mtls-host-policies-100-haproxy-ingress" -}}
{{- render "mtls-host-policies-200-haptic-annotations" -}}
{{- render "mtls-host-policies-300-nginx-ingress" -}}
{{- render "mtls-host-policies-400-haptic-annotations-error" -}}
{{- render "mtls-host-policies-500-haproxy-ingress-error" -}}
{{- render "mtls-host-policies-600-nginx-ingress-error" -}}
{%- var verifyWinners = map[string]any{} -%}
{%- for _, value := range incremental_values("mtls-host-policies", "verifyHosts") -%}
  {%- var record = value.(map[string]any) -%}
{{ "\n" }}verify-occurrence={{ tostring(record["host"]) }}|{{ tostring(record["filename"]) }}|{{ tostring(record["verifyMode"]) }}
  {%- verifyWinners[tostring(record["host"])] = record -%}
{%- end -%}
{%- for _, host := range keys(verifyWinners) -%}
  {%- var record = verifyWinners[host].(map[string]any) -%}
{{ "\n" }}verify-winner={{ host }}|{{ tostring(record["filename"]) }}|{{ tostring(record["verifyMode"]) }}
{%- end -%}
{%- for _, value := range incremental_values("mtls-host-policies", "errorHosts") -%}
  {%- var record = value.(map[string]any) -%}
{{ "\n" }}error-occurrence={{ tostring(record["host"]) }}|{{ tostring(record["location"]) }}
{%- end -%}
{%- var blocked = map[string]bool{} -%}
{%- for _, value := range incremental_values("mtls-host-policies", "blockedHosts") -%}
  {%- var record = value.(map[string]any) -%}
  {%- blocked[tostring(record["host"])] = true -%}
{%- end -%}
{%- for _, host := range keys(blocked) -%}
{{ "\n" }}blocked={{ host }}
{%- end -%}
{%- for _, value := range incremental_values("mtls-host-policies", "files") -%}
  {%- var record = value.(map[string]any) -%}
  {%- var filename = tostring(record["filename"]) -%}
{{ "\n" }}file={{ filename }}|{{ tostring(record["content"]) }}
  {%- var _, registerErr = fileRegistry.Register("file", filename, tostring(record["content"])) -%}
  {%- if registerErr != nil -%}{{ fail(tostring(registerErr)) }}{%- end -%}
{%- end -%}
{%- for _, warning := range incremental_values("mtls-host-policies", "warnings-haproxy-ingress") -%}
{{ tostring(warning) }}
{%- end -%}
{%- for _, warning := range incremental_values("mtls-host-policies", "warnings-nginx-ingress") -%}
{{ tostring(warning) }}
{%- end -%}
{%- if tostring(extraContext | dig("failAfterReplay") | fallback(false)) == "true" -%}
  {{- fail("forced failure after mTLS replay") -}}
{%- end -%}`

type mtlsPublicationLibrary struct {
	TemplateSnippets map[string]mtlsPublicationSnippet `yaml:"templateSnippets"`
}

type mtlsPublicationSnippet struct {
	Template    string                      `yaml:"template"`
	Requires    []string                    `yaml:"requires"`
	Incremental *mtlsPublicationIncremental `yaml:"incremental"`
}

type mtlsPublicationIncremental struct {
	Source            string                     `yaml:"source"`
	WhenAnyPathExists []string                   `yaml:"whenAnyPathExists"`
	Group             string                     `yaml:"group"`
	Effects           []config.IncrementalEffect `yaml:"effects"`
}

type mtlsPublicationMetadata struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Annotations map[string]string `json:"annotations"`
}

type mtlsPublicationIngress struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   mtlsPublicationMetadata    `json:"metadata"`
	Spec       mtlsPublicationIngressSpec `json:"spec"`
}

type mtlsPublicationIngressSpec struct {
	Rules []mtlsPublicationIngressRule `json:"rules"`
}

type mtlsPublicationIngressRule struct {
	Host string `json:"host"`
}

type mtlsPublicationSecret struct {
	Metadata mtlsPublicationMetadata `json:"metadata"`
	Data     map[string]string       `json:"data"`
}

type mtlsPublicationFixture struct {
	config    *config.Config
	service   *RenderService
	ingresses *k8sstore.MemoryStore
	secrets   *k8sstore.MemoryStore
	provider  stores.StoreProvider
}

func TestMTLSPublicationsPreserveOccurrenceOrderAndPromoteOnDelete(t *testing.T) {
	fixture := newMTLSPublicationFixture(t)
	fixture.addSecret(t, "default", "ha-ca", "SEEtQ0E=")
	fixture.addSecret(t, "default", "haptic-a-ca", "SEFQVElDLUE=")
	fixture.addSecret(t, "default", "haptic-z-ca", "SEFQVElDLVo=")
	fixture.addSecret(t, "default", "nginx-ca", "TkdJTlgtQ0E=")
	fixture.addIngress(t, mtlsPublicationIngressResource("a-haproxy", map[string]string{
		"haproxy-ingress.github.io/auth-tls-secret":     "ha-ca",
		"haproxy-ingress.github.io/auth-tls-error-page": "https://errors.example/haproxy",
	}, "shared.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("b-haptic", map[string]string{
		"haproxy-haptic.org/auth-tls-secret":     "haptic-a-ca",
		"haproxy-haptic.org/auth-tls-error-page": "https://errors.example/haptic-a",
	}, "shared.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("z-haptic", map[string]string{
		"haproxy-haptic.org/auth-tls-secret":        "haptic-z-ca",
		"haproxy-haptic.org/auth-tls-verify-client": "optional",
		"haproxy-haptic.org/auth-tls-error-page":    "https://errors.example/haptic-z",
	}, "shared.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("m-blocked", map[string]string{
		"haproxy-haptic.org/auth-tls-secret": "missing-ca",
	}, "blocked.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("m-haproxy-missing", map[string]string{
		"haproxy-ingress.github.io/auth-tls-secret": "missing-ca",
	}, "haproxy-missing.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("m-nginx-missing", map[string]string{
		"nginx.ingress.kubernetes.io/auth-tls-secret": "missing-ca",
	}, "nginx-missing.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("z-nginx", map[string]string{
		"nginx.ingress.kubernetes.io/auth-tls-secret":     "nginx-ca",
		"nginx.ingress.kubernetes.io/auth-tls-error-page": "https://errors.example/nginx",
	}, "shared.example"))

	first := fixture.renderAndCommit(t)
	assertOrderedSubstrings(t, first.HAProxyConfig,
		"verify-occurrence=shared.example|default-ha-ca-client-ca.pem|required",
		"verify-occurrence=shared.example|default-haptic-a-ca-client-ca.pem|required",
		"verify-occurrence=shared.example|default-haptic-z-ca-client-ca.pem|optional",
		"verify-occurrence=shared.example|default-nginx-ca-client-ca.pem|required",
		"verify-winner=shared.example|default-nginx-ca-client-ca.pem|required",
		"error-occurrence=shared.example|https://errors.example/haptic-a",
		"error-occurrence=shared.example|https://errors.example/haptic-z",
		"error-occurrence=shared.example|https://errors.example/haproxy",
		"error-occurrence=shared.example|https://errors.example/nginx",
	)
	assert.Contains(t, first.HAProxyConfig, "file=default-haptic-a-ca-client-ca.pem|HAPTIC-A")
	assert.Contains(t, first.HAProxyConfig, "file=default-haptic-z-ca-client-ca.pem|HAPTIC-Z")
	assert.Contains(t, first.HAProxyConfig, "blocked=blocked.example")
	assert.Contains(t, first.HAProxyConfig, "haproxy-missing not found at render time")
	assert.Contains(t, first.HAProxyConfig, "nginx-missing not found at render time")

	warm := fixture.renderAndCommit(t)
	assert.Equal(t, first.HAProxyConfig, warm.HAProxyConfig)
	before := fixture.executions("b-haptic")
	fixture.deleteIngress(t, "z-haptic")
	promoted := fixture.renderAndCommit(t)
	assert.NotContains(t, promoted.HAProxyConfig, "default-haptic-z-ca-client-ca.pem")
	assert.Equal(t, before, fixture.executions("b-haptic"))
	assert.Contains(t, promoted.HAProxyConfig, "verify-occurrence=shared.example|default-haptic-a-ca-client-ca.pem|required")
}

func TestMTLSPublicationFileConflictFailsWithoutPoisoning(t *testing.T) {
	fixture := newMTLSPublicationFixture(t)
	fixture.addSecret(t, "a-b", "c", "U0FNRQ==")
	fixture.addSecret(t, "a", "b-c", "U0FNRQ==")
	fixture.addIngress(t, mtlsPublicationIngressResource("a", map[string]string{
		"haproxy-haptic.org/auth-tls-secret": "a-b/c",
	}, "conflict.example"))
	fixture.addIngress(t, mtlsPublicationIngressResource("z", map[string]string{
		"haproxy-haptic.org/auth-tls-secret": "a/b-c",
	}, "conflict.example"))

	baseline := fixture.renderAndCommit(t)
	assert.Equal(t, 2, strings.Count(baseline.HAProxyConfig, "file=a-b-c-client-ca.pem|SAME"))
	fixture.updateSecret(t, "a", "b-c", "RElGRkVSRU5U")
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "a-b-c-client-ca.pem")
	assert.Nil(t, failed)

	fixture.updateSecret(t, "a", "b-c", "U0FNRQ==")
	recovered := fixture.renderAndCommit(t)
	assert.Equal(t, baseline.HAProxyConfig, recovered.HAProxyConfig)
}

func TestMTLSAdmissionOverlayCannotPoisonReconcilePublications(t *testing.T) {
	fixture := newMTLSPublicationFixture(t)
	fixture.addSecret(t, "default", "stable-ca", "U1RBQkxF")
	stable := mtlsPublicationIngressResource("subject", map[string]string{
		"haproxy-haptic.org/auth-tls-secret": "stable-ca",
	}, "stable.example")
	fixture.addIngress(t, stable)
	baseline := fixture.renderAndCommit(t)
	baselineCounters := fixture.executions("subject")

	proposed := mtlsPublicationIngressResource("subject", map[string]string{
		"haproxy-haptic.org/auth-tls-secret":        "stable-ca",
		"haproxy-haptic.org/auth-tls-verify-client": "invalid",
	}, "poison.example")
	overlay := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}),
	)
	failed, err := fixture.service.Render(
		t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("ingresses", "default", "subject"),
	)
	require.ErrorContains(t, err, "auth-tls-verify-client=invalid")
	assert.Nil(t, failed)
	assert.Equal(t, baselineCounters,
		fixture.executions("subject"))

	after := fixture.renderAndCommit(t)
	assert.Equal(t, baseline.HAProxyConfig, after.HAProxyConfig)
	assert.NotContains(t, after.HAProxyConfig, "poison.example")
}

func newMTLSPublicationFixture(t *testing.T) *mtlsPublicationFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"failAfterReplay": false,
		}},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1", Resources: "ingresses",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"secrets": {
				APIVersion: "v1", Resources: "secrets",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: loadMTLSPublicationSnippets(t),
		HAProxyConfig:    config.HAProxyConfig{Template: mtlsPublicationChartRoot},
	}
	types := &typebootstrap.Result{
		Types: map[string]reflect.Type{
			"ingresses": reflect.TypeOf(mtlsPublicationIngress{}),
			"secrets":   reflect.TypeOf(mtlsPublicationSecret{}),
		},
		Kinds:  map[string]string{"ingresses": "Ingress", "secrets": "Secret"},
		Errors: map[string]error{},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, types)
	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
		TypedResourceTypes: types.Types,
	})
	ingresses := k8sstore.NewMemoryStore(2)
	secrets := k8sstore.NewMemoryStore(2)
	return &mtlsPublicationFixture{
		config: cfg, service: service, ingresses: ingresses, secrets: secrets,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{
			"ingresses": ingresses, "secrets": secrets,
		}),
	}
}

func loadMTLSPublicationSnippets(t *testing.T) map[string]config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts")
	wanted := map[string]bool{"util-webhook-reject-or-warn": true}
	for _, name := range mtlsPublicationComponents {
		wanted[name] = true
	}
	result := make(map[string]config.TemplateSnippet, len(wanted))
	for _, file := range []string{
		"base/library.yaml",
		"haproxy-ingress/40-features.yaml",
		"haptic-annotations/50-auth-spoe.yaml",
		"nginx-ingress/30-features.yaml",
	} {
		content, err := os.ReadFile(filepath.Join(chartRoot, file))
		require.NoError(t, err)
		var library mtlsPublicationLibrary
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
					WhenAnyPathExists: chartSnippet.Incremental.WhenAnyPathExists,
					Group:             chartSnippet.Incremental.Group,
					Effects:           chartSnippet.Incremental.Effects,
				}
			}
			result[name] = snippet
		}
	}
	require.Len(t, result, len(wanted))
	return result
}

func mtlsPublicationIngressResource(name string, annotations map[string]string, host string) map[string]any {
	annotationValues := make(map[string]any, len(annotations))
	for key, value := range annotations {
		annotationValues[key] = value
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
		"metadata": map[string]any{
			"namespace": "default", "name": name, "annotations": annotationValues,
		},
		"spec": map[string]any{"rules": []any{map[string]any{"host": host}}},
	}
}

func (f *mtlsPublicationFixture) addIngress(t *testing.T, ingress map[string]any) {
	t.Helper()
	name := ingress["metadata"].(map[string]any)["name"].(string)
	require.NoError(t, f.ingresses.Add(ingress, []string{"default", name}))
}

func (f *mtlsPublicationFixture) deleteIngress(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, f.ingresses.Delete("default", name, []string{"default", name}))
}

func (f *mtlsPublicationFixture) addSecret(t *testing.T, namespace, name, ca string) {
	t.Helper()
	secret := mtlsPublicationSecretResource(namespace, name, ca)
	require.NoError(t, f.secrets.Add(secret, []string{namespace, name}))
}

func (f *mtlsPublicationFixture) updateSecret(t *testing.T, namespace, name, ca string) {
	t.Helper()
	secret := mtlsPublicationSecretResource(namespace, name, ca)
	require.NoError(t, f.secrets.Update(secret, []string{namespace, name}))
}

func mtlsPublicationSecretResource(namespace, name, ca string) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"data":     map[string]any{"ca.crt": ca},
	}
}

func (f *mtlsPublicationFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *mtlsPublicationFixture) executions(ingress string) uint64 {
	const componentName = "mtls-host-policies-200-haptic-annotations"
	component := f.service.incremental.components[componentName]
	query := componentQueryKey(&component, "ingresses", "default", ingress)
	return f.service.incremental.graph.Counters(query).Executions
}
