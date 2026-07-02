// Copyright 2025 Philipp Hossner
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

package component

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// coalescingRecorder is a handler that implements CoalescingHandler and
// records every event reaching its HandleEvent method, preserving order.
type coalescingRecorder struct {
	mu          sync.Mutex
	received    []busevents.Event
	coalesceOn  []string
	delay       time.Duration // simulates slow processing for the first event
	firstEvent  bool
	firstNotify chan struct{}
}

func (h *coalescingRecorder) HandleEvent(event busevents.Event) {
	h.mu.Lock()
	if !h.firstEvent {
		h.firstEvent = true
		h.mu.Unlock()
		// Signal that we've started processing the first event so the test
		// can publish more before this one returns.
		if h.firstNotify != nil {
			close(h.firstNotify)
		}
		time.Sleep(h.delay)
	} else {
		h.mu.Unlock()
	}
	h.mu.Lock()
	h.received = append(h.received, event)
	h.mu.Unlock()
}

func (h *coalescingRecorder) CoalescesOn() []string { return h.coalesceOn }

func (h *coalescingRecorder) snapshot() []busevents.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]busevents.Event, len(h.received))
	copy(out, h.received)
	return out
}

// TestBase_CoalescesIntermediate verifies that when the handler is a
// CoalescingHandler, intermediate coalescible events buffered while the
// first dispatch is in flight are skipped — only the latest is re-dispatched.
func TestBase_CoalescesIntermediate(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &coalescingRecorder{
		coalesceOn:  []string{events.EventTypeReconciliationTriggered},
		delay:       100 * time.Millisecond,
		firstNotify: make(chan struct{}),
	}

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "coalesce-test",
		BufferSize: 16,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered},
	})

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()

	bus.Start()
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))

	// Wait until the first event is being processed before publishing the
	// rest, so they buffer in eventChan rather than racing to the goroutine.
	select {
	case <-h.firstNotify:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}

	bus.Publish(events.NewReconciliationTriggeredEvent("second", true))
	bus.Publish(events.NewReconciliationTriggeredEvent("third", true))
	bus.Publish(events.NewReconciliationTriggeredEvent("fourth", true))

	// Give time for the first dispatch to finish + drain to run.
	require.Eventually(t, func() bool {
		return len(h.snapshot()) >= 2
	}, 3*time.Second, 20*time.Millisecond, "expected at least two dispatches")

	base.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}

	got := h.snapshot()
	require.Len(t, got, 2, "intermediate coalescible events should be skipped")

	first, ok := got[0].(*events.ReconciliationTriggeredEvent)
	require.True(t, ok)
	assert.Equal(t, "first", first.Reason)

	last, ok := got[1].(*events.ReconciliationTriggeredEvent)
	require.True(t, ok, "drained event should be ReconciliationTriggeredEvent")
	assert.Equal(t, "fourth", last.Reason, "only the latest of the buffered triggers should be re-dispatched")
}

// TestBase_CoalesceOnEmpty verifies that returning "" from CoalescesOn
// disables coalescing — every event is delivered.
func TestBase_CoalesceOnEmpty(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &coalescingRecorder{
		coalesceOn:  nil, // explicitly disabled
		delay:       100 * time.Millisecond,
		firstNotify: make(chan struct{}),
	}

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "coalesce-empty",
		BufferSize: 16,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered},
	})

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()

	bus.Start()
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))

	select {
	case <-h.firstNotify:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}

	bus.Publish(events.NewReconciliationTriggeredEvent("second", true))
	bus.Publish(events.NewReconciliationTriggeredEvent("third", true))

	require.Eventually(t, func() bool {
		return len(h.snapshot()) >= 3
	}, 3*time.Second, 20*time.Millisecond, "expected three dispatches when coalescing is off")

	base.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}
}

// TestBase_NonCoalescibleEventPassesThrough verifies that an event whose
// Coalescible() returns false is dispatched normally even when its type
// matches the handler's CoalescesOn target.
func TestBase_NonCoalescibleEventPassesThrough(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &coalescingRecorder{
		coalesceOn:  []string{events.EventTypeReconciliationTriggered},
		delay:       100 * time.Millisecond,
		firstNotify: make(chan struct{}),
	}

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "coalesce-non-coalescible",
		BufferSize: 16,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered},
	})

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()

	bus.Start()
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true)) // coalescible

	select {
	case <-h.firstNotify:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}

	// One coalescible (flushed at the run boundary the non-coalescible event
	// creates) and one non-coalescible (must pass through).
	bus.Publish(events.NewReconciliationTriggeredEvent("skipped", true))
	bus.Publish(events.NewReconciliationTriggeredEvent("must_arrive", false))

	require.Eventually(t, func() bool {
		return len(h.snapshot()) >= 3
	}, 3*time.Second, 20*time.Millisecond, "expected first + non-coalescible + drained-coalescible")

	base.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}

	got := h.snapshot()
	reasons := make([]string, 0, len(got))
	for _, e := range got {
		if r, ok := e.(*events.ReconciliationTriggeredEvent); ok {
			reasons = append(reasons, r.Reason)
		}
	}
	assert.Contains(t, reasons, "first")
	assert.Contains(t, reasons, "must_arrive", "non-coalescible event of the same type must not be skipped")
}
