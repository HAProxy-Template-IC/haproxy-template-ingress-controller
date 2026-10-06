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

package proposalvalidator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/inputisolation"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
)

func TestInvalidObservedInputBlocksUnrelatedChanges(t *testing.T) {
	const guard = `{% for _, item := range resources.ingresses.List() %}{% if tostring(item | dig("metadata", "annotations", "invalid")) == "true" %}{{ fail("invalid observed input") }}{% end %}{% end %}`
	live := &storetest.MockStore{}
	bad := unstructuredObj("default", "bad")
	bad.SetAnnotations(map[string]string{"invalid": "true"})
	require.NoError(t, live.Add(bad, []string{"default", "bad"}))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": live})
	p := createStoreTestPipeline(t, guard+testutil.MinimalHAProxyConfig)
	_, err := p.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "invalid observed input")

	good := unstructuredObj("default", "healthy")
	require.NoError(t, unstructured.SetNestedField(good.Object, "10.0.0.2", "spec", "endpoint"))
	require.NoError(t, live.Add(good, []string{"default", "healthy"}))
	_, err = p.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "invalid observed input")

	svc := NewService(&ServiceConfig{Pipeline: p, BaseStoreProvider: provider})
	_, verdict := svc.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForCreate(unstructuredObj("default", "unrelated")),
	})
	require.False(t, verdict.Valid)
	require.ErrorContains(t, verdict.Error, "invalid observed input")
}

func TestAcceptedInputsIsolateRenderErrorsWithoutAdmittingBadProposals(t *testing.T) {
	const template = `{% for _, item := range resources.ingresses.List() %}{% if tostring(item | dig("metadata", "annotations", "invalid")) == "true" %}{{ fail("invalid observed input") }}{% end %}
# input {{ tostring(item | dig("metadata", "name")) }} {{ tostring(item | dig("spec", "endpoint")) }}
{% end %}`
	live := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": live})
	p := createStoreTestPipeline(t, template+testutil.MinimalHAProxyConfig)
	selector := inputisolation.New(p, map[string]config.WatchedResource{
		"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}, nil)
	bad := unstructuredObj("default", "bad")
	require.NoError(t, live.Add(bad, []string{"default", "bad"}))
	_, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	bad.SetAnnotations(map[string]string{"invalid": "true"})
	require.NoError(t, live.Update(bad, []string{"default", "bad"}))
	good := unstructuredObj("default", "healthy")
	require.NoError(t, unstructured.SetNestedField(good.Object, "10.0.0.2", "spec", "endpoint"))
	require.NoError(t, live.Add(good, []string{"default", "healthy"}))
	output, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Contains(t, output.HAProxyConfig, "healthy 10.0.0.2")
	_, rejected, ready := selector.Snapshot()
	require.True(t, ready)
	require.Len(t, rejected, 1)
	require.Equal(t, "bad", rejected[0].Name)

	refreshes := 0
	svc := NewService(&ServiceConfig{
		Pipeline: p, BaseStoreProvider: provider, AcceptedStoreProvider: selector.AcceptedInputs, AdmissionStoreProvider: selector.IsolatedInputs,
		FreshStoreProvider: func(context.Context) (stores.StoreProvider, error) {
			refreshes++
			return nil, errors.New("API refresh unavailable")
		},
	})
	_, verdict := svc.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForCreate(unstructuredObj("default", "unrelated")),
	})
	require.NoError(t, verdict.Error)
	require.True(t, verdict.Valid)
	require.NotEmpty(t, verdict.Warnings)
	require.Zero(t, refreshes, "unrelated admission must not depend on a full API refresh")
	_, verdict = svc.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForUpdate(bad),
	})
	require.False(t, verdict.Valid)
	require.ErrorContains(t, verdict.Error, "invalid observed input")
}

func TestAdmissionFallbackPreservesUnacceptedValidObservedChanges(t *testing.T) {
	const claims = `{% seen := map[string]bool{} %}{% for _, item := range resources.ingresses.List() %}{% if tostring(item | dig("metadata", "annotations", "invalid")) == "true" %}{{ fail("invalid observed input") }}{% end %}{% claim := tostring(item | dig("metadata", "annotations", "claim")) %}{% if claim != "" %}{% if seen[claim] %}{{ fail("duplicate claim") }}{% end %}{% seen[claim] = true %}{% end %}{% end %}`
	for _, test := range []struct {
		name     string
		rejected bool
		repaired bool
	}{{name: "no rejection"}, {name: "rejected peer", rejected: true}, {name: "repaired rejected revision", rejected: true, repaired: true}} {
		t.Run(test.name, func(t *testing.T) {
			live := k8sstore.NewMemoryStore(2)
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": live})
			p := createStoreTestPipeline(t, claims+testutil.MinimalHAProxyConfig)
			selector := inputisolation.New(p, map[string]config.WatchedResource{"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace", "metadata.name"}}}, nil)
			_, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
			require.NoError(t, err)
			if test.rejected {
				bad := unstructuredObj("default", "bad")
				bad.SetAnnotations(map[string]string{"invalid": "true"})
				require.NoError(t, live.Add(bad, []string{"default", "bad"}))
				_, err = selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
				require.NoError(t, err)
			}
			ownerName := "owner"
			if test.repaired {
				ownerName = "bad"
			}
			owner := unstructuredObj("default", ownerName)
			owner.SetAnnotations(map[string]string{"claim": "shared"})
			require.NoError(t, live.Update(owner, []string{"default", ownerName}))
			proposed := unstructuredObj("default", "proposed")
			proposed.SetAnnotations(map[string]string{"claim": "shared"})
			svc := NewService(&ServiceConfig{Pipeline: p, BaseStoreProvider: provider, AcceptedStoreProvider: selector.AcceptedInputs, AdmissionStoreProvider: selector.IsolatedInputs, ObservedStoreProvider: selector.ObservedInputs})
			_, verdict := svc.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{"ingresses": stores.NewStoreOverlayForCreate(proposed)})
			require.False(t, verdict.Valid, "a valid observed owner must not disappear during fallback")
			require.ErrorContains(t, verdict.Error, "duplicate claim")
			proposed.SetAnnotations(map[string]string{"claim": "independent"})
			_, verdict = svc.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{"ingresses": stores.NewStoreOverlayForCreate(proposed)})
			require.NoError(t, verdict.Error)
			require.True(t, verdict.Valid, "unrelated proposals must remain admissible")
		})
	}
}
