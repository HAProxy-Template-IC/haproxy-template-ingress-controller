//go:build integration

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

package integration

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/inputisolation"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestInputIsolationWithNativeHAProxy(t *testing.T) {
	const template = `global
    maxconn 10
defaults
    mode http
    timeout connect 1s
    timeout client 1s
    timeout server 1s
frontend health
    bind 127.0.0.1:18080
    http-request return status 200
{% for _, widget := range resources.widgets.List() %}
backend {{ dig_string(widget, "", "metadata", "name") }}
    {{ dig_string(widget, "", "spec", "snippet") }}
    server target {{ dig_string(widget, "", "spec", "address") }}:8080
{% end %}`
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: template},
		WatchedResources: map[string]config.WatchedResource{
			"widgets": {APIVersion: "example.test/v1", Resources: "widgets", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
	}
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, &templating.Options{
		EntryPoints: []string{"haproxy.cfg"}, Declarations: typebootstrap.BuildEngineDeclarations(&typebootstrap.Result{}, "widgets"),
	})
	require.NoError(t, err)
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	t.Cleanup(func() { require.NoError(t, renderService.RetireIncrementalCache()) })
	validator := validation.NewValidationService(&validation.ValidationServiceConfig{SkipDNSValidation: true, Logger: slog.Default()})
	p := pipeline.New(&pipeline.PipelineConfig{Renderer: renderService, Validator: validator, CommitValidator: validator, Logger: slog.Default()})
	selector := inputisolation.New(p, cfg.WatchedResources, nil)
	live := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"widgets": live})
	bad := isolationWidget("bad", "10.0.0.1", "balance roundrobin")
	require.NoError(t, live.Add(bad, []string{"default", "bad"}))
	_, err = selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	bad.Object["spec"].(map[string]any)["snippet"] = "not-a-haproxy-directive"
	require.NoError(t, live.Update(bad, []string{"default", "bad"}))
	require.NoError(t, live.Add(isolationWidget("healthy", "10.0.0.2", "balance roundrobin"), []string{"default", "healthy"}))
	_, err = p.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.Error(t, err, "the full observed config must fail real HAProxy validation")
	result, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Contains(t, result.HAProxyConfig, "10.0.0.2:8080")
	require.Contains(t, result.HAProxyConfig, "backend bad")
	require.NotContains(t, result.HAProxyConfig, "not-a-haproxy-directive")

	admission := proposalvalidator.NewService(&proposalvalidator.ServiceConfig{
		Pipeline: p, BaseStoreProvider: provider, AcceptedStoreProvider: selector.AcceptedInputs, AdmissionStoreProvider: selector.IsolatedInputs,
	})
	_, verdict := admission.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{
		"widgets": stores.NewStoreOverlayForCreate(isolationWidget("unrelated", "10.0.0.3", "balance roundrobin")),
	})
	require.True(t, verdict.Valid, "%v", verdict.Error)
	_, verdict = admission.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{
		"widgets": stores.NewStoreOverlayForUpdate(bad),
	})
	require.False(t, verdict.Valid)

	cold := inputisolation.New(p, cfg.WatchedResources, nil)
	result, err = cold.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Contains(t, result.HAProxyConfig, "backend healthy")
	require.NotContains(t, result.HAProxyConfig, "backend bad")
}

func isolationWidget(name, address, snippet string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.test/v1", "kind": "Widget",
		"metadata": map[string]any{"namespace": "default", "name": name},
		"spec":     map[string]any{"address": address, "snippet": snippet},
	}}
}
