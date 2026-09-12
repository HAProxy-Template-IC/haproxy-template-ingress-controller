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

//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestGatewayFrontendMTLSCAReferences(t *testing.T) {
	feature := features.New("Gateway: frontend mTLS CA reference lifecycle").
		Assess("only the granted namespace supplies the client CA", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			fixture := newGatewayMTLSCAReferenceFixture(ctx, t, cfg)
			fixture.apply(ctx, t, "default-ca", "AllowInsecureFallback", true)
			fixture.wait(ctx, t, true, false)
			fixture.discover(ctx, t)
			fixture.checkBlockedOverride(ctx, t)

			for range 2 {
				fixture.createCAReferenceGrant(ctx, t)
				fixture.wait(ctx, t, true, true)
				fixture.checkStrictOverride(ctx, t)
				fixture.deleteCAReferenceGrant(ctx, t)
				fixture.wait(ctx, t, true, false)
				fixture.checkBlockedOverride(ctx, t)
			}

			fixture.createCAReferenceGrant(ctx, t)
			fixture.wait(ctx, t, true, true)
			fixture.checkStrictOverride(ctx, t)
			fixture.replaceCA(ctx, t, fixture.overrideCANamespace, nil)
			fixture.wait(ctx, t, true, false)
			fixture.checkBlockedOverride(ctx, t)
			fixture.replaceCA(ctx, t, fixture.overrideCANamespace, fixture.overrideBundle.CACertPEM)
			fixture.wait(ctx, t, true, true)
			fixture.checkStrictOverride(ctx, t)
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func newGatewayMTLSCAReferenceFixture(ctx context.Context, t *testing.T, cfg *envconf.Config) *gatewayMTLSFixture {
	t.Helper()
	fixture := newGatewayMTLSFixture(ctx, t, cfg)
	fixture.overrideCANamespace = NamespaceForTest(ctx, t, fixture.client)
	require.NotEqual(t, fixture.namespace, fixture.overrideCANamespace)
	DumpLogsOnFailure(t, fixture.overrideCANamespace)
	_, err := fixture.clientset.CoreV1().ConfigMaps(fixture.overrideCANamespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "override-ca", Namespace: fixture.overrideCANamespace},
		Data:       map[string]string{"ca.crt": string(fixture.overrideBundle.CACertPEM)},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	fixture.replaceCA(ctx, t, fixture.namespace, fixture.defaultBundle.CACertPEM)
	return fixture
}

func (f *gatewayMTLSFixture) replaceCA(ctx context.Context, t *testing.T, namespace string, pem []byte) {
	t.Helper()
	client := f.clientset.CoreV1().ConfigMaps(namespace)
	configMap, err := client.Get(ctx, "override-ca", metav1.GetOptions{})
	require.NoError(t, err)
	configMap.Data["ca.crt"] = string(pem)
	updated, err := client.Update(ctx, configMap, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Equal(t, configMap.UID, updated.UID)
	require.Equal(t, string(pem), updated.Data["ca.crt"])
}

func (f *gatewayMTLSFixture) createCAReferenceGrant(ctx context.Context, t *testing.T) {
	t.Helper()
	require.NoError(t, f.client.Resources().Create(ctx, f.caReferenceGrant()))
}

func (f *gatewayMTLSFixture) deleteCAReferenceGrant(ctx context.Context, t *testing.T) {
	t.Helper()
	require.NoError(t, f.client.Resources().Delete(ctx, f.caReferenceGrant()))
}

func (f *gatewayMTLSFixture) caReferenceGrant() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "ReferenceGrant",
		"metadata": map[string]any{"name": "allow-client-ca", "namespace": f.overrideCANamespace},
		"spec": map[string]any{
			"from": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": f.namespace}},
			"to":   []any{map[string]any{"group": "", "kind": "ConfigMap", "name": "override-ca"}},
		},
	}}
}
