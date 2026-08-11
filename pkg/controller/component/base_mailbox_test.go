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
	"context"
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
	entered  int
	gate     chan struct{} // one receive per HandleEvent call
	started  chan struct{} // signalled once per HandleEvent entry
}

func (h *blockingRecorder) HandleEvent(event busevents.Event) {
	h.mu.Lock()
	h.entered++
	h.mu.Unlock()
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.gate
	h.mu.Lock()
	h.received = append(h.received, event)
	h.mu.Unlock()
}

// startedCount returns how many events have entered the handler (including
// the one currently blocked on the gate).
func (h *blockingRecorder) startedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entered
}

func (h *blockingRecorder) CoalescesOn() []string {
	return []string{events.EventTypeReconciliationTriggered}
}

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
	// most of this overflowed the 8-slot buffer and was dropped. The
	// publisher waits for the intake goroutine to absorb each event before
	// sending the next: the property under test is "the intake drains the
	// channel while the handler is blocked", NOT "the intake goroutine wins
	// every scheduling race against a tight publish loop" — on contended CI
	// runners the latter is not guaranteed and made the unpaced version of
	// this test flaky.
	const burst = 3 * bufferSize
	published := 1 // the "first" event above
	publish := func(e busevents.Event) {
		bus.Publish(e)
		published++
		require.Eventually(t, func() bool {
			return h.startedCount()+base.mailboxAbsorbed() >= published
		}, 2*time.Second, time.Millisecond,
			"intake must absorb event %d while the handler is blocked", published)
	}
	for i := 0; i < burst; i++ {
		publish(events.NewReconciliationTriggeredEvent("burst", true))
		if i == burst/3 || i == 2*burst/3 {
			publish(events.NewBecameLeaderEvent("test"))
		}
	}

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

// TestBase_MailboxDoesNotReplayAcrossRestarts pins the leadership-term
// boundary contract for mailbox components: events the intake goroutine
// already moved into the mailbox queue during a previous Start (leadership
// term) must NOT be dispatched after the component is stopped and started
// again — they describe the previous term's state, exactly like the buffered
// channel events FlushPending discards. Regression test for the stale-replay
// gap where startMailbox reused the old queue.
func TestBase_MailboxDoesNotReplayAcrossRestarts(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &blockingRecorder{
		gate:    make(chan struct{}),
		started: make(chan struct{}, 1),
	}

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "mailbox-restart",
		BufferSize: 16,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered, events.EventTypeBecameLeader},
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()
	bus.Start()

	// Occupy the handler, then queue events that land in the mailbox.
	bus.Publish(events.NewReconciliationTriggeredEvent("term1-dispatched", true))
	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}
	bus.Publish(events.NewBecameLeaderEvent("term1-queued-a"))
	bus.Publish(events.NewBecameLeaderEvent("term1-queued-b"))
	require.Eventually(t, func() bool { return base.mailboxAbsorbed() == 2 },
		2*time.Second, time.Millisecond, "intake must absorb the term-1 events")

	// End term 1: cancel first, then release the in-flight handler — the
	// worker must exit at its shutdown check instead of grinding the queue.
	cancel()
	go func() { h.gate <- struct{}{} }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}

	// Term 2: flush (as leader-only components do) and start again with a
	// fresh context — mirroring lifecycle.Registry, which re-calls Start on
	// the same instance and shuts down via context cancellation (stopCh is
	// only for explicit Stop and stays untouched across terms).
	base.FlushPending()
	done2 := make(chan struct{})
	go func() {
		_ = base.Start(t.Context())
		close(done2)
	}()
	bus.Publish(events.NewReconciliationTriggeredEvent("term2", true))

	// Only the term-2 event may arrive; the two term-1 queued events must not.
	go func() {
		for {
			select {
			case h.gate <- struct{}{}:
			case <-done2:
				return
			}
		}
	}()
	require.Eventually(t, func() bool { return len(h.snapshot()) >= 2 },
		3*time.Second, 10*time.Millisecond)
	base.Stop()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down after term 2")
	}

	got := h.snapshot()
	for _, e := range got {
		if e.EventType() == events.EventTypeBecameLeader {
			t.Fatalf("term-1 mailbox event replayed into term 2: %v", e)
		}
	}
}

func TestBase_MailboxStartWaitsForIntake(t *testing.T) {
	bus := busevents.NewEventBus(4)
	h := &blockingRecorder{
		gate:    make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "mailbox-intake-join",
		BufferSize: 4,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered},
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()
	bus.Start()
	require.Equal(t, 1, bus.Publish(events.NewReconciliationTriggeredEvent("warmup", true)))
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("mailbox worker did not start")
	}
	h.gate <- struct{}{}
	require.Eventually(t, func() bool { return len(h.snapshot()) == 1 },
		time.Second, time.Millisecond, "mailbox worker did not finish its warmup event")

	base.mbMu.Lock()
	locked := true
	defer func() {
		cancel()
		if locked {
			base.mbMu.Unlock()
		}
	}()

	require.Equal(t, 1, bus.Publish(events.NewReconciliationTriggeredEvent("blocked-intake", true)))
	require.Eventually(t, func() bool { return len(base.eventChan) == 0 },
		time.Second, time.Millisecond, "mailbox intake did not receive the event")

	cancel()
	select {
	case <-done:
		t.Fatal("Base.Start returned while its mailbox intake goroutine was still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	base.mbMu.Unlock()
	locked = false
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Base.Start did not return after its mailbox intake goroutine exited")
	}
}
