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
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const gatewayMTLSOverrideRoot = `{%- var _, _ = shared.ComputeIfAbsent("globalFeatures", func() any {
  return map[string]any{"clientCertVerifyHosts": map[string]any{}}
}) -%}
{{- render "features-110-gateway-frontend-mtls" -}}
{%- var features = shared.Get("globalFeatures").(map[string]any) -%}
# blocked={{ features["mtlsBlockedListeners"] | toJSON() }}
# resolved={{ features["gatewayListenerMTLSConfig"] | toJSON() }}
# ca-resolutions={{ incremental_values("gateway-frontend-mtls", "resolutions") | toJSON() }}
{{- render "test-gateway-mtls-route-blocked" -}}
# trigger={{ dig_string(resources.httproutes.GetSingle("default", "trigger"), "", "metadata", "resourceVersion") }}
{%- if tostring(extraContext | dig("failAfterGatewayFeatures") | fallback(false)) == "true" -%}
{{ fail("forced failure after Gateway feature publications") }}
{%- end -%}`

func TestGatewayMTLSOverrideTracksCADeletionAndRecreation(t *testing.T) {
	fixture := newGatewayMTLSOverrideFixture(t)
	fixture.addGateway(t, gatewayMTLSOverrideGateway(
		gatewayMTLSOverrideValidation("default-ca", "AllowInsecureFallback"),
		gatewayMTLSOverrideValidation("override-ca", "AllowValidOnly"), true,
	))
	addGatewayFeatureConfigMap(t, fixture, gatewayFeatureCAConfigMap("default-ca", "DEFAULT-CA"))
	missing := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, missing.HAProxyConfig, `# blocked={"default/subject/override":true}`)
	require.Contains(t, missing.HAProxyConfig, `# route-blocked={"override":true}`)
	assertGatewayMTLSOverrideCold(t, fixture, missing)

	addGatewayFeatureConfigMap(t, fixture, gatewayFeatureCAConfigMap("override-ca", "OVERRIDE-CA"))
	resolved := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, resolved.HAProxyConfig, "# blocked={}")
	require.Contains(t, resolved.HAProxyConfig, "# route-blocked={}")
	require.Contains(t, resolved.HAProxyConfig, `"verify_mode":"required"`)
	require.Contains(t, resolved.HAProxyConfig, `"verify_mode":"optional"`)
	assertGatewayMTLSOverrideCold(t, fixture, resolved)
	assertGatewayFeatureExecutions(t, fixture, gatewayFrontendMTLSComponent, "gateways", "subject", 2)

	fixture.addHTTPRoute(t, map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
		"metadata": map[string]any{"name": "trigger", "namespace": "default", "resourceVersion": "2"},
	})
	addGatewayFeatureConfigMap(t, fixture, gatewayFeatureCAConfigMap("unrelated", "UNRELATED-CA"))
	warm := renderGatewayFeaturesAndCommit(t, fixture)
	require.NotEqual(t, resolved.HAProxyConfig, warm.HAProxyConfig)
	assertGatewayFeatureExecutions(t, fixture, gatewayFrontendMTLSComponent, "gateways", "subject", 2)
	assertGatewayMTLSOverrideCold(t, fixture, warm)

	require.NoError(t, fixture.configMaps.Delete("default", "override-ca", []string{"default", "override-ca"}))
	deleted := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, deleted.HAProxyConfig, `# blocked={"default/subject/override":true}`)
	require.Contains(t, deleted.HAProxyConfig, `# route-blocked={"override":true}`)
	assertGatewayMTLSOverrideCold(t, fixture, deleted)
	addGatewayFeatureConfigMap(t, fixture, gatewayFeatureCAConfigMap("override-ca", "REPLACED-CA"))
	recreated := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, recreated.HAProxyConfig, "# blocked={}")
	assertGatewayMTLSOverrideCold(t, fixture, recreated)
}

func TestGatewayMTLSOverridePresenceSurvivesAdmissionAndAbort(t *testing.T) {
	fixture := newGatewayMTLSOverrideFixture(t)
	defaultValidation := gatewayMTLSOverrideValidation("missing-default", "AllowValidOnly")
	fixture.addGateway(t, gatewayMTLSOverrideGateway(defaultValidation, nil, true))
	baseline := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, baseline.HAProxyConfig, `# blocked={"default/subject/default":true}`)
	require.Contains(t, baseline.HAProxyConfig, `# route-blocked={"default":true}`)
	assertGatewayMTLSOverrideCold(t, fixture, baseline)

	proposed := gatewayMTLSOverrideGateway(defaultValidation, nil, false)
	overlay := stores.NewOverlayStoreProvider(fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"gateways": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: proposed}),
		}))
	admission, err := fixture.service.Render(t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("gateways", "default", "subject"))
	require.NoError(t, err)
	require.Contains(t, admission.HAProxyConfig, `# blocked={"default/subject/default":true,"default/subject/override":true}`)
	admission.InputTransaction.Abort()
	assertRenderResultObservablesEqual(t, baseline, renderGatewayFeaturesAndCommit(t, fixture))

	require.NoError(t, fixture.gateways.Update(proposed, []string{"default", "subject"}))
	fixture.config.TemplatingSettings.ExtraContext["failAfterGatewayFeatures"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after Gateway feature publications")
	require.Nil(t, failed)
	fixture.config.TemplatingSettings.ExtraContext["failAfterGatewayFeatures"] = false
	changed := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, changed.HAProxyConfig, `# blocked={"default/subject/default":true,"default/subject/override":true}`)
	require.Contains(t, changed.HAProxyConfig, `# route-blocked={"default":true,"override":true}`)
	assertGatewayMTLSOverrideCold(t, fixture, changed)
}

func gatewayMTLSOverrideValidation(name, mode string) map[string]any {
	return map[string]any{
		"mode":              mode,
		"caCertificateRefs": []any{map[string]any{"group": "", "kind": "ConfigMap", "name": name}},
	}
}

func gatewayMTLSOverrideGateway(defaultValidation, overrideValidation map[string]any, hasOverride bool) map[string]any {
	gateway := gatewayFeatureGateway("subject", 443)
	spec := gateway["spec"].(map[string]any)
	spec["listeners"] = []any{
		map[string]any{"name": "default", "hostname": "same.example.org", "protocol": "HTTPS", "port": int64(443)},
		map[string]any{"name": "override", "hostname": "same.example.org", "protocol": "HTTPS", "port": int64(8443)},
	}
	frontend := map[string]any{"default": map[string]any{"validation": defaultValidation}}
	if hasOverride {
		tls := map[string]any{}
		if overrideValidation != nil {
			tls["validation"] = overrideValidation
		}
		frontend["perPort"] = []any{map[string]any{"port": int64(8443), "tls": tls}}
	}
	spec["tls"] = map[string]any{"frontend": frontend}
	return gateway
}

func assertGatewayMTLSOverrideCold(t *testing.T, fixture *gatewayRouteAnalysisFixture, actual *RenderResult) {
	t.Helper()
	oracle := newGatewayMTLSOverrideFixture(t)
	oracle.provider = fixture.provider
	assertRenderResultObservablesEqual(t, renderGatewayFeaturesAndCommit(t, oracle), actual)
}

func newGatewayMTLSOverrideFixture(t *testing.T) *gatewayRouteAnalysisFixture {
	t.Helper()
	snippets := loadGatewayFeaturePublicationSnippets(t)
	snippets["test-gateway-mtls-route-blocked"] = config.TemplateSnippet{
		Name:     "test-gateway-mtls-route-blocked",
		Requires: []string{"gateways"},
		Incremental: &config.IncrementalTemplate{
			Source: "gateways", Group: "test-gateway-mtls-route-blocked",
			Consumes: []string{"gateway-frontend-mtls"},
		},
		Template: `{%- import "util-gw-mtls-blocked-value" for ComputeGwMtlsBlockedValue -%}
{%- var gateway = resources.gateways.GetSingle("default", "subject") -%}
{%- var blocked = map[string]bool{} -%}
{%- if gateway != nil %}{{ ComputeGwMtlsBlockedValue(gateway, blocked) }}{% end -%}
# route-blocked={{ blocked | toJSON() }}`,
	}
	fixture := newGatewayRouteAnalysisFixtureWithTemplates(t, snippets, gatewayMTLSOverrideRoot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterGatewayFeatures"] = false
	return fixture
}
