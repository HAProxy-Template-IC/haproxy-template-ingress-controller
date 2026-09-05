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

package warmer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type recordingPipeline struct {
	mu      sync.Mutex
	calls   []rendercontext.RenderMode
	options []int
	result  *pipeline.PipelineResult
	err     error
	// gate, when set, holds every Execute until released so a burst of
	// triggers can pile up behind one render; entered reports each arrival.
	gate    chan struct{}
	entered chan struct{}
	ran     chan struct{}
}

func (p *recordingPipeline) Execute(
	_ context.Context, _ stores.StoreProvider, mode rendercontext.RenderMode, opts ...rendercontext.Option,
) (*pipeline.PipelineResult, error) {
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.gate != nil {
		<-p.gate
	}
	p.mu.Lock()
	p.calls = append(p.calls, mode)
	p.options = append(p.options, len(opts))
	p.mu.Unlock()
	if p.ran != nil {
		p.ran <- struct{}{}
	}
	return p.result, p.err
}

func (p *recordingPipeline) executions() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

type harness struct {
	bus       *busevents.EventBus
	pipeline  *recordingPipeline
	metrics   *metrics.Metrics
	published <-chan busevents.Event
	files     func() (map[string]string, error)
}

func newHarness(t *testing.T, p *recordingPipeline) *harness {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	h := &harness{
		bus:      bus,
		pipeline: p,
		metrics:  metrics.NewMetrics(prometheus.NewRegistry()),
		files:    func() (map[string]string, error) { return map[string]string{"maps/a.map": "x"}, nil },
	}
	h.published = bus.SubscribeTypes("test", 100,
		events.EventTypeReconciliationStarted,
		events.EventTypeTemplateRendered,
		events.EventTypeReconciliationCompleted,
		events.EventTypeReconciliationFailed,
	)
	component := New(&Config{
		EventBus:      bus,
		Pipeline:      p,
		StoreProvider: stores.NewRealStoreProvider(nil),
		CurrentFiles:  func() (map[string]string, error) { return h.files() },
		Metrics:       h.metrics,
		Logger:        logger,
	})
	bus.Start()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = component.Start(ctx) }()
	time.Sleep(testutil.StartupDelay)
	return h
}

func (h *harness) waitForRender(t *testing.T) {
	t.Helper()
	select {
	case <-h.pipeline.ran:
	case <-time.After(testutil.EventTimeout):
		t.Fatal("timed out waiting for the warmer to render")
	}
}

func (h *harness) assertNoRender(t *testing.T) {
	t.Helper()
	select {
	case <-h.pipeline.ran:
		t.Fatal("the warmer rendered while it must not")
	case <-time.After(testutil.NoEventTimeout):
	}
}

func warmResult() *pipeline.PipelineResult {
	return &pipeline.PipelineResult{RenderDurationMs: 3, CacheState: "warm", CacheBuildMs: 2}
}

func TestWarmerRendersOnFollowerAndPublishesNothing(t *testing.T) {
	p := &recordingPipeline{result: warmResult(), ran: make(chan struct{}, 8)}
	h := newHarness(t, p)

	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	h.waitForRender(t)

	p.mu.Lock()
	require.Equal(t, []rendercontext.RenderMode{rendercontext.RenderModeReconcile}, p.calls)
	require.Equal(t, []int{1}, p.options, "the render carries the published currentFiles")
	p.mu.Unlock()
	testutil.AssertNoEvent[busevents.Event](t, h.published, testutil.NoEventTimeout)
	assert.Equal(t, 1.0, promtestutil.ToFloat64(h.metrics.RenderTotal.WithLabelValues("warm")))
}

func TestWarmerCoalescesABurstOfTriggers(t *testing.T) {
	p := &recordingPipeline{
		result: warmResult(), ran: make(chan struct{}, 8), gate: make(chan struct{}), entered: make(chan struct{}, 8),
	}
	h := newHarness(t, p)

	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	select {
	case <-p.entered:
	case <-time.After(testutil.EventTimeout):
		t.Fatal("the first render never started")
	}
	for range 5 {
		h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	}
	time.Sleep(testutil.StartupDelay)
	close(p.gate)
	h.waitForRender(t)
	h.waitForRender(t)
	h.assertNoRender(t)
	assert.Equal(t, 2, p.executions(), "one render for the first trigger, one for the coalesced rest")
}

func TestWarmerStandsDownWhileLeader(t *testing.T) {
	p := &recordingPipeline{result: warmResult(), ran: make(chan struct{}, 8)}
	h := newHarness(t, p)

	h.bus.Publish(events.NewBecameLeaderEvent("this-replica"))
	h.bus.Publish(events.NewReconciliationTriggeredEvent("became_leader", false))
	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	h.assertNoRender(t)

	h.bus.Publish(events.NewLostLeadershipEvent("this-replica", "lease_lost"))
	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	h.waitForRender(t)
	assert.Equal(t, 1, p.executions())
}

func TestWarmerSkipsWhenCurrentFilesAreUnavailable(t *testing.T) {
	p := &recordingPipeline{result: warmResult(), ran: make(chan struct{}, 8)}
	h := newHarness(t, p)
	h.files = func() (map[string]string, error) { return nil, errors.New("published set is ambiguous") }

	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	h.assertNoRender(t)
	testutil.AssertNoEvent[busevents.Event](t, h.published, testutil.NoEventTimeout)
}

func TestWarmerSurvivesAFailedRender(t *testing.T) {
	p := &recordingPipeline{err: errors.New("render exploded"), ran: make(chan struct{}, 8)}
	h := newHarness(t, p)

	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	h.waitForRender(t)
	h.bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	h.waitForRender(t)
	testutil.AssertNoEvent[busevents.Event](t, h.published, testutil.NoEventTimeout)
	assert.Equal(t, 0.0, promtestutil.ToFloat64(h.metrics.RenderTotal.WithLabelValues("warm")))
}
