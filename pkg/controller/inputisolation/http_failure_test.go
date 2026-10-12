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

package inputisolation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestCriticalHTTPFailureDoesNotBisectWatchedInputs(t *testing.T) {
	for _, explicitFailure := range []bool{false, true} {
		for _, withhold := range []bool{false, true} {
			t.Run(fmt.Sprintf("fail=%t/withhold=%t", explicitFailure, withhold), func(t *testing.T) {
				testCriticalHTTPFailure(t, explicitFailure, withhold)
			})
		}
	}
}

func testCriticalHTTPFailure(t *testing.T, explicitFailure, withhold bool) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	template := fmt.Sprintf(`{%% var content, fetchErr = http.Fetch(%q, map[string]any{"critical": true, "retries": 1}) %%}`, server.URL)
	if explicitFailure {
		template += `{% if fetchErr != nil %}{{ fail("probe: " + fetchErr.Error()) }}{% end %}`
	}
	template += "{{ content }}"
	bus, logger := testutil.NewTestBusAndLogger()
	httpComponent := controllerhttpstore.New(bus, logger, 0)
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
	require.NoError(t, err)
	cfg := &config.Config{HAProxyConfig: config.HAProxyConfig{Template: template}}
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger, HTTPStoreComponent: httpComponent,
	})
	renderPipeline := pipeline.New(&pipeline.PipelineConfig{Renderer: renderService, Logger: logger})
	calls := 0
	validator := validationFunc(func(ctx context.Context, provider stores.StoreProvider) (*pipeline.PipelineResult, error) {
		calls++
		if calls > 2 {
			return nil, context.Canceled
		}
		return renderPipeline.Execute(ctx, provider, rendercontext.RenderModeReconcile)
	})
	live := k8sstore.NewMemoryStore(2)
	for i := range 12800 {
		name := fmt.Sprintf("widget-%05d", i)
		require.NoError(t, live.Add(widget(name, "healthy"), []string{"default", name}))
	}
	selector := New(validator, map[string]config.WatchedResource{
		"widgets": {APIVersion: "example.test/v1", Resources: "widgets", IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}, logger)
	ctx := t.Context()
	if withhold {
		ctx = pipeline.WithPendingContentWithheld(ctx)
	}
	result, err := selector.Execute(ctx, stores.NewRealStoreProvider(map[string]stores.Store{"widgets": live}), rendercontext.RenderModeReconcile)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, calls, "HTTP failure cannot be repaired by bisecting Kubernetes inputs")
	require.Equal(t, withhold, pipeline.WaitsForCriticalContent(err))
	require.Equal(t, !withhold, pipeline.CriticalContentUnavailable(err))
	if !withhold {
		require.ErrorContains(t, err, "503")
		require.Equal(t, int32(2), requests.Load(), "one fetch with one configured retry")
	}
	_, rejected, ready := selector.Snapshot()
	require.False(t, ready)
	require.Empty(t, rejected)
}
