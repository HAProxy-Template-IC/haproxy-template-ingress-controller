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
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const gatewayTLSCertificateRoot = `{{- render "gateway-tls-permissions-100-gateway" default "" -}}
{{- render "gateway-tls-certificates-100-gateway" -}}
# certificates={{ incremental_values("gateway-tls-certificates", "resolved") | toJSON() }}`

func TestGatewayTLSWithoutReferenceGrantSchemaKeepsLocalCertificate(t *testing.T) {
	fixture := newGatewayFeaturePublicationFixtureWithRoot(t, gatewayTLSCertificateRoot)
	withoutGatewayReferenceGrantSchema(t, fixture)
	gateway := gatewayTLSCertificateGateway("")
	fixture.addGateway(t, gateway)
	addGatewayFeatureSecret(t, fixture, gatewayFeatureTLSSecret("server-cert", "LOCAL-CERT", "LOCAL-KEY", nil))
	local := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, local.HAProxyConfig, "LOCAL-CERT")

	require.NoError(t, fixture.gateways.Update(gatewayTLSCertificateGateway("cert-system"), []string{"default", "subject"}))
	remoteSecret := gatewayFeatureTLSSecret("server-cert", "REMOTE-CERT", "REMOTE-KEY", nil)
	remoteSecret["metadata"].(map[string]any)["namespace"] = "cert-system"
	require.NoError(t, fixture.secrets.Add(remoteSecret, []string{"cert-system", "server-cert"}))
	remote := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, remote.HAProxyConfig, "# certificates=[]")
	require.NotContains(t, remote.HAProxyConfig, "LOCAL-CERT")
	require.NotContains(t, remote.HAProxyConfig, "REMOTE-CERT")
}

func TestGatewayTLSReferenceGrantLifecycle(t *testing.T) {
	fixture := newGatewayFeaturePublicationFixtureWithRoot(t, gatewayTLSCertificateRoot)
	fixture.addGateway(t, gatewayTLSCertificateGateway("cert-system"))
	addGatewayFeatureSecret(t, fixture, gatewayFeatureTLSSecret("server-cert", "LOCAL-DECOY", "LOCAL-KEY", nil))
	secret := gatewayFeatureTLSSecret("server-cert", "REMOTE-CERT", "REMOTE-KEY", nil)
	secret["metadata"].(map[string]any)["namespace"] = "cert-system"
	require.NoError(t, fixture.secrets.Add(secret, []string{"cert-system", "server-cert"}))
	assertGatewayTLSCertificateResolution(t, fixture, false)
	grant := gatewayMTLSReferenceGrant("cert-system")
	grant["spec"].(map[string]any)["to"] = []any{map[string]any{
		"group": "", "kind": "Secret", "name": "server-cert",
	}}
	require.NoError(t, fixture.referenceGrants.Add(grant, []string{"cert-system", "allow-client-ca"}))
	baseline := assertGatewayTLSCertificateResolution(t, fixture, true)

	overlay := stores.NewOverlayStoreProvider(fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"referencegrants": stores.NewStoreOverlayForDelete("cert-system", "allow-client-ca"),
		}))
	admission, err := fixture.service.Render(t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("referencegrants", "cert-system", "allow-client-ca"))
	require.NoError(t, err)
	require.Contains(t, admission.HAProxyConfig, "# certificates=[]")
	admission.InputTransaction.Abort()
	assertRenderResultObservablesEqual(t, baseline, assertGatewayTLSCertificateResolution(t, fixture, true))

	require.NoError(t, fixture.referenceGrants.Delete("cert-system", "allow-client-ca", []string{"cert-system", "allow-client-ca"}))
	assertGatewayTLSCertificateResolution(t, fixture, false)
	require.NoError(t, fixture.referenceGrants.Add(grant, []string{"cert-system", "allow-client-ca"}))
	assertGatewayTLSCertificateResolution(t, fixture, true)
}

func gatewayTLSCertificateGateway(namespace string) map[string]any {
	gateway := gatewayMTLSOverrideGateway(nil, nil, false)
	listener := gateway["spec"].(map[string]any)["listeners"].([]any)[0].(map[string]any)
	listener["tls"] = map[string]any{"mode": "Terminate", "certificateRefs": []any{
		map[string]any{"group": "", "kind": "Secret", "name": "server-cert", "namespace": namespace},
	}}
	return gateway
}

func assertGatewayTLSCertificateResolution(t *testing.T, fixture *gatewayRouteAnalysisFixture, resolved bool) *RenderResult {
	t.Helper()
	result := renderGatewayFeaturesAndCommit(t, fixture)
	require.NotContains(t, result.HAProxyConfig, "LOCAL-DECOY")
	if resolved {
		require.Contains(t, result.HAProxyConfig, "REMOTE-CERT")
	} else {
		require.Contains(t, result.HAProxyConfig, "# certificates=[]")
	}
	oracle := newGatewayFeaturePublicationFixtureWithRoot(t, gatewayTLSCertificateRoot)
	oracle.provider = fixture.provider
	assertRenderResultObservablesEqual(t, renderGatewayFeaturesAndCommit(t, oracle), result)
	return result
}

func TestGatewayMTLSWithoutReferenceGrantSchemaKeepsLocalValidation(t *testing.T) {
	fixture := newGatewayMTLSOverrideFixture(t)
	withoutGatewayReferenceGrantSchema(t, fixture)
	validation := gatewayMTLSOverrideValidation("client-ca", "AllowValidOnly")
	fixture.addGateway(t, gatewayMTLSOverrideGateway(validation, nil, true))
	addGatewayFeatureConfigMap(t, fixture, gatewayFeatureCAConfigMap("client-ca", "LOCAL-CA"))
	local := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, local.HAProxyConfig, "# blocked={}")
	require.Contains(t, local.HAProxyConfig, "# route-blocked={}")
	require.Contains(t, local.HAProxyConfig, `"verify_mode":"required"`)
	require.Contains(t, local.HAProxyConfig, "LOCAL-CA")

	validation["caCertificateRefs"].([]any)[0].(map[string]any)["namespace"] = "ca-system"
	require.NoError(t, fixture.gateways.Update(gatewayMTLSOverrideGateway(validation, nil, true), []string{"default", "subject"}))
	remote := gatewayFeatureCAConfigMap("client-ca", "REMOTE-CA")
	remote["metadata"].(map[string]any)["namespace"] = "ca-system"
	require.NoError(t, fixture.configMaps.Add(remote, []string{"ca-system", "client-ca"}))
	blocked := renderGatewayFeaturesAndCommit(t, fixture)
	require.Contains(t, blocked.HAProxyConfig, `# blocked={"default/subject/default":true}`)
	require.Contains(t, blocked.HAProxyConfig, `# route-blocked={"default":true}`)
	require.NotContains(t, blocked.HAProxyConfig, "REMOTE-CA")
	require.NotContains(t, blocked.HAProxyConfig, "LOCAL-CA")
}

func withoutGatewayReferenceGrantSchema(t *testing.T, fixture *gatewayRouteAnalysisFixture) {
	t.Helper()
	watch := fixture.config.WatchedResources["referencegrants"]
	watch.Optional = true
	fixture.config.WatchedResources["referencegrants"] = watch
	served := gatewayRootServedResources{}
	for name := range fixture.config.WatchedResources {
		served[fixture.config.WatchedResources[name].Resources] = name != "referencegrants"
	}
	effective, resolution, err := config.ResolveEffective(fixture.config, served, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"referencegrants"}, resolution.Unavailable)
	require.Contains(t, effective.TemplateSnippets, "features-110-gateway-frontend-mtls")
	require.Contains(t, effective.TemplateSnippets, gatewayFrontendMTLSComponent)
	require.Contains(t, effective.TemplateSnippets, gatewayTLSCertificateComponent)
	require.NotContains(t, effective.TemplateSnippets, gatewayTLSPermissionsComponent)
	require.NotContains(t, effective.TemplateSnippets, gatewayFrontendCAPermissionsComponent)
	require.Contains(t, effective.AbsentIncrementalGroups, "gateway-frontend-ca-permissions")
	require.NoError(t, config.ValidateTemplateStructure(effective))
	types := gatewayRouteAnalysisSchemaTypes(t)
	delete(types.Types, "referencegrants")
	delete(types.Kinds, "referencegrants")
	engine, err := helpers.NewEngineFromConfigWithOptions(effective, nil, nil,
		helpers.BuildAdditionalDeclarations(effective, types), helpers.EngineOptions{})
	require.NoError(t, err)
	fixture.config = effective
	fixture.engine = newDynamicBindingCountingEngine(t, engine)
	fixture.service = NewRenderService(&RenderServiceConfig{
		Engine: fixture.engine, Config: effective, Logger: slog.Default(),
		Capabilities: defaultCapabilities(), TypedResourceTypes: types.Types,
	})
}

func TestGatewayMTLSReferenceGrantAndCALifecycle(t *testing.T) {
	fixture := newGatewayMTLSOverrideFixture(t)
	validation := gatewayMTLSOverrideValidation("client-ca", "AllowValidOnly")
	validation["caCertificateRefs"].([]any)[0].(map[string]any)["namespace"] = "ca-system"
	fixture.addGateway(t, gatewayMTLSOverrideGateway(validation, nil, true))
	addGatewayFeatureConfigMap(t, fixture, gatewayFeatureCAConfigMap("client-ca", "LOCAL-DECOY"))
	remoteCA := gatewayFeatureCAConfigMap("client-ca", "REMOTE-CA")
	remoteCA["metadata"].(map[string]any)["namespace"] = "ca-system"
	require.NoError(t, fixture.configMaps.Add(remoteCA, []string{"ca-system", "client-ca"}))

	assertGatewayMTLSReferenceResolution(t, fixture, false)
	grant := gatewayMTLSReferenceGrant("ca-system")
	require.NoError(t, fixture.referenceGrants.Add(grant, []string{"ca-system", "allow-client-ca"}))
	baseline := assertGatewayMTLSReferenceResolution(t, fixture, true)
	assertGatewayFeatureExecutions(t, fixture, gatewayFrontendMTLSComponent, "gateways", "subject", 2)

	unrelated := gatewayMTLSReferenceGrant("unrelated")
	require.NoError(t, fixture.referenceGrants.Add(unrelated, []string{"unrelated", "allow-client-ca"}))
	assertRenderResultObservablesEqual(t, baseline, assertGatewayMTLSReferenceResolution(t, fixture, true))
	assertGatewayFeatureExecutions(t, fixture, gatewayFrontendMTLSComponent, "gateways", "subject", 2)
	assertGatewayFeatureExecutions(t, fixture, gatewayFrontendCAPermissionsComponent, "gateways", "subject", 2)

	overlay := stores.NewOverlayStoreProvider(fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"referencegrants": stores.NewStoreOverlayForDelete("ca-system", "allow-client-ca"),
		}))
	admission, err := fixture.service.Render(t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("referencegrants", "ca-system", "allow-client-ca"))
	require.NoError(t, err)
	require.Contains(t, admission.HAProxyConfig, `# blocked={"default/subject/default":true}`)
	require.Contains(t, admission.HAProxyConfig, `# route-blocked={"default":true}`)
	admission.InputTransaction.Abort()
	assertRenderResultObservablesEqual(t, baseline, assertGatewayMTLSReferenceResolution(t, fixture, true))

	require.NoError(t, fixture.referenceGrants.Delete("ca-system", "allow-client-ca", []string{"ca-system", "allow-client-ca"}))
	assertGatewayMTLSReferenceResolution(t, fixture, false)
	require.NoError(t, fixture.referenceGrants.Add(grant, []string{"ca-system", "allow-client-ca"}))
	assertGatewayMTLSReferenceResolution(t, fixture, true)

	remoteCA["data"].(map[string]any)["ca.crt"] = ""
	require.NoError(t, fixture.configMaps.Update(remoteCA, []string{"ca-system", "client-ca"}))
	assertGatewayMTLSReferenceResolution(t, fixture, false)
	remoteCA["data"].(map[string]any)["ca.crt"] = "REMOTE-CA"
	require.NoError(t, fixture.configMaps.Update(remoteCA, []string{"ca-system", "client-ca"}))
	assertGatewayMTLSReferenceResolution(t, fixture, true)
}

func gatewayMTLSReferenceGrant(namespace string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "ReferenceGrant",
		"metadata": map[string]any{"name": "allow-client-ca", "namespace": namespace},
		"spec": map[string]any{
			"from": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": "default"}},
			"to":   []any{map[string]any{"group": "", "kind": "ConfigMap", "name": "client-ca"}},
		},
	}
}

func assertGatewayMTLSReferenceResolution(t *testing.T, fixture *gatewayRouteAnalysisFixture, resolved bool) *RenderResult {
	t.Helper()
	result := renderGatewayFeaturesAndCommit(t, fixture)
	require.NotContains(t, result.HAProxyConfig, "LOCAL-DECOY")
	if resolved {
		require.Contains(t, result.HAProxyConfig, "# blocked={}")
		require.Contains(t, result.HAProxyConfig, "# route-blocked={}")
		require.Contains(t, result.HAProxyConfig, "REMOTE-CA")
	} else {
		require.Contains(t, result.HAProxyConfig, `# blocked={"default/subject/default":true}`)
		require.Contains(t, result.HAProxyConfig, `# route-blocked={"default":true}`)
		require.NotContains(t, result.HAProxyConfig, "REMOTE-CA")
	}
	assertGatewayMTLSOverrideCold(t, fixture, result)
	return result
}
