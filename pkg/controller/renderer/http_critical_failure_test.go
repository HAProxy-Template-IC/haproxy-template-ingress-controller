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

// Scriggo shows nothing for `{{ f() }}` when f returns an error, so a critical
// source that fails, or that a deploying render leaves out, must fail the
// render rather than render as empty.
func TestCriticalHTTPSourceFailsTheRenderHoweverTheTemplateShowsIt(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	serving := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("page"))
	}))
	t.Cleanup(serving.Close)
	tests := map[string]struct {
		url      string
		withheld bool
		main     bool
		mode     rendercontext.RenderMode
	}{
		"failed fetch in a component":               {url: failing.URL},
		"failed fetch in the main template":         {url: failing.URL, main: true},
		"unaccepted source a deploy leaves out":     {url: serving.URL, withheld: true},
		"admission rejects a failed critical fetch": {url: failing.URL, mode: rendercontext.RenderModeAdmission},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fetch := `{{ http.Fetch("` + test.url + `", map[string]any{"critical": true, "retries": 1}) }}`
			component := `{{ item | dig_string("", "metadata", "name") }}=` + "\n"
			main := `{{ render "routes" }}`
			if test.main {
				main += fetch
			} else {
				component = `{{ item | dig_string("", "metadata", "name") }}=` + fetch + "\n"
			}
			service, provider := criticalFetchService(t, component, main)
			ctx := t.Context()
			if test.withheld {
				ctx = controllerhttpstore.WithCandidatesWithheld(ctx)
			}

			mode := test.mode
			if mode == "" {
				mode = rendercontext.RenderModeReconcile
			}
			result, err := service.Render(ctx, provider, mode)

			require.Error(t, err)
			require.Nil(t, result)
			if test.withheld {
				require.ErrorIs(t, err, controllerhttpstore.ErrCandidateWithheld)
			}
		})
	}
}

func criticalFetchService(t *testing.T, component, main string) (*RenderService, stores.StoreProvider) {
	t.Helper()
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
				Template:    component,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: main},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	bus, logger := testutil.NewTestBusAndLogger()
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger,
		HTTPStoreComponent: controllerhttpstore.New(bus, logger, -time.Hour),
	})
	t.Cleanup(func() { _ = service.RetireIncrementalCache() })
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(incrementalTestResource("default", "a", nil), []string{"default", "a"}))
	return service, stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
}
