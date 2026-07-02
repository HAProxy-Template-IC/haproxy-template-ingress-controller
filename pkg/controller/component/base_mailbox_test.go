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

// blockingRecorder is a CoalescingHandler whose HandleEvent blocks on a
// gate channel, simulating a slow apply (SSA round-trips) so events pile up
// behind it.
type blockingRecorder struct {
	mu       sync.Mutex
	received []busevents.Event
	gate     chan struct{} // one receive per HandleEvent call
	started  chan struct{} // signalled once per HandleEvent entry
}

func (h *blockingRecorder) HandleEvent(event busevents.Event) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.gate
	h.mu.Lock()
	h.received = append(h.received, event)
	h.mu.Unlock()
}

func (h *blockingRecorder) CoalescesOn() string { return events.EventTypeReconciliationTriggered }

func (h *blockingRecorder) snapshot() []busevents.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]busevents.Event, len(h.received))
	copy(out, h.received)
	return out
}

// TestBase_MailboxNeverDropsUnderBurst is the regression test for the
// publish-side drops observed in gateway-api conformance: the status
// applier's handler takes 0.6-1.8s per event, so under render churn the
// bus-side subscriber buffer (a few dozen slots) filled while the handler
// was busy and the bus DROPPED events — including non-coalescible
// deployment.completed events and the final event of a burst, leaving
// stale status until the next external trigger. In mailbox mode the intake
// goroutine empties the channel immediately, so a burst far larger than
// the buffer must land with zero bus drops: every non-coalescible event
// delivered, consecutive coalescible runs collapsed to their latest.
func TestBase_MailboxNeverDropsUnderBurst(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &blockingRecorder{
		gate:    make(chan struct{}),
		started: make(chan struct{}, 1),
	}

	const bufferSize = 8 // deliberately tiny vs the burst below
	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "mailbox-burst",
		BufferSize: bufferSize,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered, events.EventTypeBecameLeader},
	})

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()
	bus.Start()

	// First event occupies the handler (it blocks on the gate).
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}

	// Burst: 3× the buffer size in coalescible triggers, with two
	// non-coalescible BecameLeader events as run boundaries. Pre-mailbox,
	// most of this overflowed the 8-slot buffer and was dropped.
	const burst = 3 * bufferSize
	for i := 0; i < burst; i++ {
		bus.Publish(events.NewReconciliationTriggeredEvent("burst", true))
		if i == burst/3 || i == 2*burst/3 {
			bus.Publish(events.NewBecameLeaderEvent("test"))
		}
	}

	// Drops happen synchronously inside Publish when the subscriber buffer
	// is full, so the counter is final once the burst loop returns: the
	// intake goroutine must have swallowed the whole burst.
	require.Equal(t, uint64(0), bus.DroppedEventsCritical(),
		"intake must drain the channel while the handler is blocked")

	// Release the handler for all remaining dispatches.
	go func() {
		for {
			select {
			case h.gate <- struct{}{}:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Expected delivery: first, then run(burst/3) coalesced → 1, boundary,
	// run coalesced → 1, boundary, trailing run coalesced → 1.
	require.Eventually(t, func() bool {
		return len(h.snapshot()) >= 6
	}, 3*time.Second, 10*time.Millisecond, "expected 6 dispatches, got %d", len(h.snapshot()))

	base.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}

	got := h.snapshot()
	require.Len(t, got, 6)
	assert.Equal(t, uint64(0), bus.DroppedEventsCritical(), "mailbox mode must never let the bus drop")

	// Order: trigger(first), trigger(run1), leader, trigger(run2), leader, trigger(run3)
	types := make([]string, len(got))
	for i, e := range got {
		types[i] = e.EventType()
	}
	assert.Equal(t, []string{
		events.EventTypeReconciliationTriggered,
		events.EventTypeReconciliationTriggered,
		events.EventTypeBecameLeader,
		events.EventTypeReconciliationTriggered,
		events.EventTypeBecameLeader,
		events.EventTypeReconciliationTriggered,
	}, types, "non-coalescible boundaries must be preserved in arrival order")
}
