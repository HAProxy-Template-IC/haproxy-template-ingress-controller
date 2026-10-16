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

package httpstore_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/helpers"
	controllerhttpstore "github.com/haproxy-haptic/HAPTIC/pkg/controller/httpstore"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/pipeline"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercontext"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/renderer"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/typebootstrap"
	"github.com/haproxy-haptic/HAPTIC/pkg/core/config"
	busevents "github.com/haproxy-haptic/HAPTIC/pkg/events"
	k8sstore "github.com/haproxy-haptic/HAPTIC/pkg/k8s/store"
	"github.com/haproxy-haptic/HAPTIC/pkg/stores"
)

type outageHarness struct {
	component *controllerhttpstore.Component
	triggers  <-chan busevents.Event
	pipeline  *pipeline.Pipeline
	provider  stores.StoreProvider
	routes    *k8sstore.MemoryStore
	requests  atomic.Int64
	up        atomic.Bool
	url       string
}

// newOutageHarness renders one route whose line needs a critical source with
// an hour-long refresh interval, so only ProbeOutagesNow refetches it.
func newOutageHarness(t *testing.T) *outageHarness {
	t.Helper()
	h := &outageHarness{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.requests.Add(1)
		if !h.up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("page"))
	}))
	t.Cleanup(server.Close)
	h.url = server.URL
	cfg := &config.Config{
		Dataplane: config.DataplaneConfig{
			MapsDir: "/etc/haproxy/maps", SSLCertsDir: "/etc/haproxy/ssl", GeneralStorageDir: "/etc/haproxy/files",
		},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {APIVersion: "example.test/v1", Resources: "routes", IndexBy: []string{"metadata.namespace", "metadata.name"}},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name:        "routes",
				Requires:    []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{{ item | dig_string("", "metadata", "name") }}={{ http.Fetch(item | dig_string("", "spec", "url"), ` +
					`map[string]any{"retries": 1, "timeout": "1s", "interval": "1h", "critical": true}) }}` +
					`{{ item | dig_string("", "spec", "suffix") }}` + "\n",
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
	h.component = controllerhttpstore.New(bus, logger, 0)
	service := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger, HTTPStoreComponent: h.component,
		ColdRendersRunToCompletion: true,
	})
	t.Cleanup(func() { _ = service.RetireIncrementalCache() })
	h.pipeline = pipeline.New(&pipeline.PipelineConfig{Renderer: service, Logger: logger})
	h.routes = k8sstore.NewMemoryStore(2)
	require.NoError(t, h.routes.Add(h.route(""), []string{"default", "a"}))
	h.provider = stores.NewRealStoreProvider(map[string]stores.Store{"routes": h.routes})
	h.triggers = bus.SubscribeTypes("outage-harness", 16, events.EventTypeReconciliationTriggered)
	bus.Start()
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = h.component.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	require.Eventually(t, h.component.Started, testutil.EventTimeout, time.Millisecond)
	return h
}

func (h *outageHarness) route(suffix string) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Route",
		"metadata":   map[string]any{"namespace": "default", "name": "a"},
		"spec":       map[string]any{"url": h.url, "suffix": suffix},
	}
}

// render runs one reconcile render as a follower trigger does.
func (h *outageHarness) render(t *testing.T) (*pipeline.PipelineResult, error) {
	t.Helper()
	return h.pipeline.Execute(t.Context(), h.provider, rendercontext.RenderModeReconcile)
}

func (h *outageHarness) changeRoute(t *testing.T, suffix string) {
	t.Helper()
	require.NoError(t, h.routes.Update(h.route(suffix), []string{"default", "a"}))
}

// A replica without the critical source's content renders once, then neither
// renders nor fetches again for triggers until the source's own refetch.
func TestUnavailableCriticalSourceDoesNotRenderOrFetchPerTrigger(t *testing.T) {
	h := newOutageHarness(t)

	_, err := h.render(t)
	require.True(t, pipeline.CriticalContentUnavailable(err), "the first render reports the unavailable source: %v", err)
	require.False(t, pipeline.RenderHeld(err), "the first render runs")
	fetched := h.requests.Load()
	require.Equal(t, int64(2), fetched, "one fetch with one retry")

	for i := range 10 {
		h.changeRoute(t, "-"+string(rune('a'+i)))
		_, err := h.render(t)
		require.True(t, pipeline.RenderHeld(err), "trigger %d must not render again: %v", i, err)
		require.True(t, pipeline.CriticalContentUnavailable(err), "a held render reports the same failure")
	}
	assert.Equal(t, fetched, h.requests.Load(), "held renders must not fetch")

	// The refetch fails while renders were skipped: one render re-checks the
	// changed inputs without fetching again.
	h.component.ProbeOutagesNow()
	require.Equal(t, fetched+2, h.requests.Load(), "the source's own refetch")
	recheck := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, h.triggers, testutil.EventTimeout)
	assert.Equal(t, "critical_http_content_recheck", recheck.Reason)
	_, err = h.render(t)
	require.True(t, pipeline.CriticalContentUnavailable(err))
	require.False(t, pipeline.RenderHeld(err), "skipped inputs are re-checked once per refetch")
	assert.Equal(t, fetched+2, h.requests.Load(), "the re-check reuses the refetch's failure")

	// Nothing was skipped since the re-check, so another failed refetch
	// leaves renders paused.
	h.component.ProbeOutagesNow()
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, h.triggers, testutil.NoEventTimeout)
	_, err = h.render(t)
	require.True(t, pipeline.RenderHeld(err))
}

func TestRecoveredCriticalSourceTriggersOneSuccessfulRender(t *testing.T) {
	h := newOutageHarness(t)
	_, err := h.render(t)
	require.True(t, pipeline.CriticalContentUnavailable(err))
	_, err = h.render(t)
	require.True(t, pipeline.RenderHeld(err))

	h.up.Store(true)
	before := h.requests.Load()
	h.component.ProbeOutagesNow()
	require.Equal(t, before+1, h.requests.Load(), "the refetch reaches the source")
	recovered := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, h.triggers, testutil.EventTimeout)
	assert.Equal(t, "critical_http_content_available", recovered.Reason)

	result, err := h.render(t)
	require.NoError(t, err, "the render after recovery succeeds")
	assert.Equal(t, "a=page\n", result.HAProxyConfig)
	assert.Equal(t, before+1, h.requests.Load(), "the render adopts the refetched content")
	content, accepted := h.component.GetStore().Get(h.url)
	require.True(t, accepted, "the successful render accepts the content")
	assert.Equal(t, "page", content)
	assert.Zero(t, h.component.RecoveredInputs(), "accepted content no longer waits in the recovery set")

	h.changeRoute(t, "!")
	result, err = h.render(t)
	require.NoError(t, err)
	assert.Equal(t, "a=page!\n", result.HAProxyConfig)
	assert.Equal(t, before+1, h.requests.Load(), "accepted content is not fetched again")
}
