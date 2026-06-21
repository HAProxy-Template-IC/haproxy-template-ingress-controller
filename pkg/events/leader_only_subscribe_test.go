// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EventBus has THREE late-subscription warning contracts that protect the
// system from bugs caused by late subscribers (which silently miss the
// pre-start buffer):
//
//  1. Subscribe / SubscribeTypes called AFTER Start() — must emit a WARN
//     log so a late-subscription bug is visible.
//  2. SubscribeTypesLeaderOnly called after Start() — must NOT emit the
//     WARN, because leader-only components are intentionally late (they
//     only run after winning leader election). A regression that flipped
//     this would flood operator logs every time leadership transitions
//     happen (~every pod restart).
//  3. SubscribeLossy called after Start() — also must emit the WARN.
//     Lossy semantics are about drop behaviour during overload, NOT
//     about being a leader-only late subscriber. Confusing the two
//     would either mute legitimate late-subscription bugs (if lossy
//     suppressed warnings) or flood logs from observability components
//     (if lossy were treated as leader-only).
//
// These tests pin all three contracts using a slog handler that captures
// warning output so we can assert on the exact warning text.

// captureSlog swaps the default slog logger to a buffer-backed handler for
// the duration of the test, returning a function that drains and returns
// the captured output. Restores the original logger via t.Cleanup.
func captureSlog(t *testing.T) (drain func() string) {
	t.Helper()

	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	var buf bytes.Buffer
	var mu sync.Mutex
	handler := slog.NewTextHandler(syncWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	slog.SetDefault(slog.New(handler))

	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// syncWriter wraps a bytes.Buffer with a mutex so concurrent slog writes
// (the EventBus background goroutine emits late-subscription warnings
// from the subscription path) don't race the test's read.
type syncWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (s syncWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(b)
}

func TestSubscribe_AfterStart_WarnsButLeaderOnlyDoesNot(t *testing.T) {
	// Pin the universal-subscription warning fork. Both Subscribe and
	// SubscribeLossy must warn after Start() — neither is a leader-only
	// late subscriber. (The only leader-only variant is the typed
	// SubscribeTypesLeaderOnly, exercised in the typed-path test below.)
	tests := []struct {
		name       string
		subscribe  func(b *EventBus) <-chan Event
		wantWarn   bool
		wantSubstr string // must appear in WARN output if wantWarn is true
	}{
		{
			name: "Subscribe after Start → WARN emitted",
			subscribe: func(b *EventBus) <-chan Event {
				return b.Subscribe("test-late", 10)
			},
			wantWarn:   true,
			wantSubstr: "Subscription after EventBus.Start()",
		},
		{
			name: "SubscribeLossy after Start → WARN (lossy is NOT leader-only)",
			subscribe: func(b *EventBus) <-chan Event {
				return b.SubscribeLossy("test-lossy", 10)
			},
			wantWarn:   true,
			wantSubstr: "Subscription after EventBus.Start()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drain := captureSlog(t)

			bus := NewEventBus(10)
			bus.Start()

			ch := tt.subscribe(bus)
			require.NotNil(t, ch,
				"subscribe must return a non-nil channel even when warning")

			out := drain()
			if tt.wantWarn {
				assert.Contains(t, out, tt.wantSubstr,
					"WARN must be emitted with documented prefix so log scrapers "+
						"can detect late-subscription bugs; a regression that "+
						"silenced this would hide subscription-timing bugs at runtime")
				assert.Contains(t, out, "level=WARN",
					"the late-subscription log must be at WARN level so it surfaces "+
						"in default operator log filters")
			} else {
				assert.NotContains(t, out, "Subscription after EventBus.Start()",
					"leader-only subscriptions intentionally happen AFTER Start() "+
						"(triggered by BecameLeaderEvent); a regression that emitted "+
						"the warning here would flood operator logs every leader "+
						"election")
			}
		})
	}
}

func TestSubscribeTypes_AfterStart_WarnsButLeaderOnlyDoesNot(t *testing.T) {
	// Same fork as above but for the typed-subscription path. The two
	// paths share subscriptionCallerInfo but go through separate
	// subscribeInternal/subscribeTypesInternal functions, so a
	// regression in one doesn't surface in the other.
	tests := []struct {
		name       string
		subscribe  func(b *EventBus) <-chan Event
		wantWarn   bool
		wantSubstr string
	}{
		{
			name: "SubscribeTypes after Start → WARN with 'Typed subscription' prefix",
			subscribe: func(b *EventBus) <-chan Event {
				return b.SubscribeTypes("typed-late", 10, "x.y", "y.z")
			},
			wantWarn: true,
			// The typed path uses a DIFFERENT message than the universal
			// path so log scrapers can distinguish them.
			wantSubstr: "Typed subscription after EventBus.Start()",
		},
		{
			name: "SubscribeTypesLeaderOnly after Start → silent",
			subscribe: func(b *EventBus) <-chan Event {
				return b.SubscribeTypesLeaderOnly("typed-leader", 10, "x.y")
			},
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drain := captureSlog(t)

			bus := NewEventBus(10)
			bus.Start()

			ch := tt.subscribe(bus)
			require.NotNil(t, ch)

			out := drain()
			if tt.wantWarn {
				assert.Contains(t, out, tt.wantSubstr,
					"WARN must be emitted with the documented typed-subscription "+
						"prefix — distinct from the universal Subscribe message — "+
						"so log scrapers can attribute the warning to the right path")
				// The typed warning logs the event_types so operators see
				// which subscription is too late. Pin this side-channel
				// info so a refactor that dropped it doesn't silently
				// reduce debuggability.
				assert.Contains(t, out, "event_types",
					"the typed late-subscription warning MUST log event_types so "+
						"operators can identify which subscription was created late")
			} else {
				assert.NotContains(t, out, "Typed subscription after EventBus.Start()",
					"leader-only typed subscription must be silent after Start()")
			}
		})
	}
}

func TestSubscribe_BeforeStart_NeverWarns(t *testing.T) {
	// Sanity test for the negative direction: subscribing BEFORE Start()
	// must never emit the late-subscription warning, regardless of which
	// Subscribe* variant is used. This is the normal path that the vast
	// majority of components take, and a regression that warned here
	// would flood every controller startup.
	tests := []struct {
		name      string
		subscribe func(b *EventBus) <-chan Event
	}{
		{"Subscribe", func(b *EventBus) <-chan Event { return b.Subscribe("a", 10) }},
		{"SubscribeLossy", func(b *EventBus) <-chan Event { return b.SubscribeLossy("a", 10) }},
		{"SubscribeTypes", func(b *EventBus) <-chan Event { return b.SubscribeTypes("a", 10, "x") }},
		{"SubscribeTypesLeaderOnly", func(b *EventBus) <-chan Event { return b.SubscribeTypesLeaderOnly("a", 10, "x") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drain := captureSlog(t)

			bus := NewEventBus(10)
			ch := tt.subscribe(bus)
			require.NotNil(t, ch)

			// Start AFTER subscribe → not the late-subscription path.
			bus.Start()

			out := drain()
			assert.NotContains(t, out, "EventBus.Start()",
				"subscribing BEFORE Start() is the normal path and MUST NOT emit "+
					"the late-subscription warning — a regression here would flood "+
					"every controller startup with false-positive WARNs")
		})
	}
}

func TestUnsubscribeTyped_RemovesSubscriptionPreventingMemoryLeak(t *testing.T) {
	// UnsubscribeTyped is the cleanup helper for typed subscriptions
	// (different list from regular Subscribe). The contract is:
	//
	//  1. Calling it removes the subscription from typedSubscribers so
	//     the bus stops forwarding events to that channel.
	//  2. It's safe to call multiple times — duplicate calls are no-ops.
	//
	// A regression that removed the wrong entry, or no entry, would
	// either lose unrelated subscriptions or leak the channel forever
	// (memory creep over the bus's lifetime).
	bus := NewEventBus(10)

	keepCh := bus.SubscribeTypes("keep", 5, "x.y")
	dropCh := bus.SubscribeTypes("drop", 5, "x.y")

	// Verify both subscriptions are tracked.
	bus.mu.RLock()
	require.Len(t, bus.typedSubscribers, 2,
		"both typed subscriptions must be tracked before Unsubscribe")
	bus.mu.RUnlock()

	bus.UnsubscribeTyped(dropCh)

	// Capture state under the lock, then release it BEFORE calling
	// bus.Start()/bus.Publish(). Both methods acquire the same RWMutex
	// internally; Go's sync.RWMutex prohibits recursive read-locking
	// and may deadlock if a writer is waiting.
	bus.mu.RLock()
	gotLen := len(bus.typedSubscribers)
	var survivorName string
	if gotLen == 1 {
		survivorName = bus.typedSubscribers[0].name
	}
	bus.mu.RUnlock()

	assert.Equal(t, 1, gotLen,
		"after UnsubscribeTyped(dropCh), only one typed subscription should remain — "+
			"a regression that removed the wrong entry (or no entry) would either "+
			"lose unrelated subscriptions or leak channels indefinitely")
	if gotLen == 1 {
		// Pin BOTH the name field AND the channel identity. The name check
		// alone could pass if both subscriptions happened to use the same
		// name (defensive against future test mistakes). The channel
		// identity check confirms UnsubscribeTyped removed the right entry.
		assert.Equal(t, "keep", survivorName,
			"the surviving subscription MUST be 'keep' — UnsubscribeTyped that "+
				"swap-removed the wrong index would leave 'drop' alive and lose 'keep'")
		// Pump an event and assert it lands on keepCh — the only way that
		// happens is if the surviving outputChan IS keepCh, so this is a
		// more portable identity check than direct chan comparison (which
		// requires matching directionality).
		bus.Start()
		bus.Publish(&fakeEvent{eventType: "x.y"})
		select {
		case <-keepCh:
			// expected — confirms keep's channel survived
		case <-time.After(time.Second):
			t.Fatal("keep channel did not receive published event after Unsubscribe(drop) — " +
				"UnsubscribeTyped likely removed the wrong index, leaving drop alive and keep orphaned")
		}
	}

	// Idempotency: calling Unsubscribe a second time on the same channel
	// must NOT panic and must NOT remove anything else.
	assert.NotPanics(t, func() { bus.UnsubscribeTyped(dropCh) },
		"UnsubscribeTyped MUST be safe to call multiple times on the same channel — "+
			"duplicate calls in cleanup paths (defer + explicit Stop) are common")

	bus.mu.RLock()
	assert.Len(t, bus.typedSubscribers, 1,
		"second UnsubscribeTyped on already-removed channel must be a no-op")
	bus.mu.RUnlock()
}

func TestUnsubscribeTyped_StopsEventDelivery(t *testing.T) {
	// End-to-end behaviour: after UnsubscribeTyped, the channel must
	// stop receiving events. This is the contract that prevents leaked
	// goroutines from continuing to consume bus capacity after they're
	// supposed to be done.
	bus := NewEventBus(10)
	ch := bus.SubscribeTypes("test", 10, "test.event")
	bus.Start()

	bus.Publish(&fakeEvent{eventType: "test.event"})
	select {
	case <-ch:
		// expected — confirms baseline event delivery works
	case <-time.After(time.Second):
		t.Fatal("baseline test failed: typed subscription did not receive event before Unsubscribe")
	}

	bus.UnsubscribeTyped(ch)

	// After unsubscribe, a published event must NOT reach this channel.
	bus.Publish(&fakeEvent{eventType: "test.event"})

	select {
	case <-ch:
		t.Fatal("UnsubscribeTyped contract violated: channel received event AFTER unsubscribe — " +
			"this would leak goroutines and let stopped components keep consuming bus capacity")
	case <-time.After(50 * time.Millisecond):
		// expected — no event delivered after unsubscribe
	}
}

// fakeEvent is a minimal Event for tests in this file — kept local so we
// don't depend on any specific domain event type from pkg/controller/events.
type fakeEvent struct {
	eventType string
	ts        time.Time
}

func (e *fakeEvent) EventType() string    { return e.eventType }
func (e *fakeEvent) Timestamp() time.Time { return e.ts }
