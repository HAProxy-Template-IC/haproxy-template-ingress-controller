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

package metrics

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// stuckHandler is a coalescing handler that never finishes its first event,
// so everything behind it queues.
type stuckHandler struct {
	entered chan struct{}
	release chan struct{}
}

func (h *stuckHandler) HandleEvent(busevents.Event) {
	select {
	case h.entered <- struct{}{}:
	default:
	}
	<-h.release
}

func (h *stuckHandler) CoalescesOn() []string {
	return []string{events.EventTypeReconciliationTriggered}
}

// TestMailboxDepthIsScraped pins the gauge to the mailbox a component runs:
// the label is the component name and the value its queue length.
func TestMailboxDepthIsScraped(t *testing.T) {
	registry := prometheus.NewRegistry()
	NewMetrics(registry)

	bus := busevents.NewEventBus(16)
	handler := &stuckHandler{entered: make(chan struct{}, 1), release: make(chan struct{})}
	base := component.New(&component.Config{
		EventBus:   bus,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:       "scraped-mailbox",
		BufferSize: 8,
		Handler:    handler,
		EventTypes: []string{events.EventTypeReconciliationTriggered, events.EventTypeBecameLeader},
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()
	bus.Start()
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	select {
	case <-handler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler never took its first event")
	}
	bus.Publish(events.NewReconciliationTriggeredEvent("queued", true))
	bus.Publish(events.NewBecameLeaderEvent("leader"))

	depth := func() (float64, bool) {
		families, err := registry.Gather()
		require.NoError(t, err)
		for _, family := range families {
			if family.GetName() != "haptic_component_mailbox_depth" {
				continue
			}
			for _, metric := range family.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "component" && label.GetValue() == "scraped-mailbox" {
						return metric.GetGauge().GetValue(), true
					}
				}
			}
		}
		return 0, false
	}
	require.Eventually(t, func() bool {
		value, found := depth()
		return found && value == 2
	}, 2*time.Second, time.Millisecond)

	cancel()
	close(handler.release)
	<-done
	_, found := depth()
	require.False(t, found, "a stopped mailbox is no longer scraped")
}
