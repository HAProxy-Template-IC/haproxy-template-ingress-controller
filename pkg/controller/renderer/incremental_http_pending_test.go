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
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// A component tells content waiting for acceptance apart from a failed fetch
// through http.Pending, so it can stay silent instead of reporting a failure.
func TestComponentSeesWithheldContentAsPending(t *testing.T) {
	pages := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("page"))
	}))
	t.Cleanup(pages.Close)
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {APIVersion: "example.test/v1", Resources: "routes", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name:        "routes",
				Requires:    []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{%- var url = item | dig_string("", "spec", "url") -%}
{%- var page, _ = http.Fetch(url, map[string]any{"retries": 1, "timeout": "1s"}) -%}
{{ item | dig_string("", "metadata", "name") }}={{ page }} pending={{ http.Pending(url) }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	bus, logger := testutil.NewTestBusAndLogger()
	component := controllerhttpstore.New(bus, logger, -time.Hour)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger, HTTPStoreComponent: component,
	})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "a", map[string]any{"url": pages.URL + "/a"}),
		[]string{"default", "a"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
	renderAndCommitIncrementalCacheReady(t, service, provider)
	renderAndCommitIncrementalCacheReady(t, service, provider)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"url": pages.URL + "/b"}),
		[]string{"default", "b"},
	))

	withheld, err := service.Render(controllerhttpstore.WithCandidatesWithheld(t.Context()), provider,
		rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Contains(t, withheld.HAProxyConfig, "a=page pending=false")
	require.Contains(t, withheld.HAProxyConfig, "b= pending=true")
	require.NoError(t, withheld.InputTransaction.Commit(t.Context()))

	accepting, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Contains(t, accepting.HAProxyConfig, "b=page pending=false")
	require.NoError(t, accepting.InputTransaction.Commit(t.Context()))
	require.NoError(t, service.RetireIncrementalCache())
}
