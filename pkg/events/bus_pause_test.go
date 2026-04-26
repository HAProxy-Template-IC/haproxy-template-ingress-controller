// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EventBus.Pause is the leadership-transition coordination primitive
// that swaps the bus back to buffering mode so events published
// during the transition window can be replayed once the new leader
// has its components subscribed. It had ZERO direct test coverage,
// despite being the central piece of the Pause→Publish→Start cycle
// that drives state replay.
//
// Three load-bearing contracts:
//
//  1. Pause when started → flips started=false AND allocates a
//     fresh preStartBuffer. The fresh buffer is critical: leaving
//     a stale buffer would replay events from a PREVIOUS pause
//     cycle on the next Start(), polluting the new leader's view
//     of recent state.
//
//  2. Pause when already paused → idempotent no-op. Rapid leader
//     churn (e.g. losing leadership during a leadership-transition
//     window) can deliver back-to-back Pause calls; the second one
//     MUST NOT wipe a buffer that's already been collecting events.
//
//  3. Pause→Publish→Start round-trip → events published while
//     paused are replayed to existing subscribers when Start fires.
//     This is the whole reason Pause exists.

func TestEventBus_Pause_FlipsStartedAndResetsBuffer(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	bus.Start()
	require.True(t, bus.started, "baseline: bus must be started for the test to be meaningful")

	bus.Pause()

	assert.False(t, bus.started,
		"Pause MUST set started=false so subsequent Publish calls "+
			"buffer instead of dispatching — the whole point of Pause "+
			"is to switch back to buffering mode for leadership transitions")
	assert.NotNil(t, bus.preStartBuffer,
		"Pause MUST allocate a fresh preStartBuffer — leaving a nil "+
			"buffer would crash the next Publish call when it tries to "+
			"append to it")
	assert.Empty(t, bus.preStartBuffer,
		"the fresh buffer MUST be empty — a regression that reused a "+
			"previous Pause cycle's buffer would replay stale events on "+
			"the next Start, polluting the new leader's view of recent state")
}

func TestEventBus_Pause_IsIdempotentWhenAlreadyPaused(t *testing.T) {
	t.Parallel()
	bus := NewEventBus(100)
	// Note: NewEventBus returns a bus in the un-started (paused-equivalent)
	// state. So a Pause call on a brand-new bus exercises the "already
	// paused" branch.
	require.False(t, bus.started,
		"baseline: a fresh bus must NOT be started")

	// Seed the buffer with one event so we can verify the second
	// Pause doesn't wipe it.
	bus.Publish(testEvent{message: "buffered-before-second-pause"})
	require.Len(t, bus.preStartBuffer, 1,
		"sanity: the publish before the second Pause must have buffered one event")

	bus.Pause() // Should be a no-op since we're not started.

	assert.False(t, bus.started,
		"second Pause must leave started=false (idempotent — no flip)")
	require.Len(t, bus.preStartBuffer, 1,
		"second Pause MUST NOT reset the existing buffer — rapid leader "+
			"churn (Pause → Pause without an intervening Start) would "+
			"otherwise wipe the events the next leader needs to replay")
	got, ok := bus.preStartBuffer[0].(testEvent)
	require.True(t, ok)
	assert.Equal(t, "buffered-before-second-pause", got.message,
		"the buffered event MUST survive the second Pause untouched")
}

func TestEventBus_Pause_PublishStartReplaysBufferedEvents(t *testing.T) {
	// End-to-end contract: Pause-Publish-Start replays the events
	// to subscribers that existed BEFORE the Pause call. This
	// matches the leadership-transition scenario:
	//   1. Pause the bus during transition
	//   2. Publish events during the window (e.g. BecameLeaderEvent)
	//   3. Resume — buffered events fire to subscribers
	t.Parallel()

	bus := NewEventBus(100)
	sub := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Drain any pre-existing events to isolate the test.
	drain(sub)

	bus.Pause()

	const message = "published-while-paused"
	sent := bus.Publish(testEvent{message: message})
	assert.Equal(t, 0, sent,
		"Publish during pause must report 0 immediate sends — events "+
			"are buffered, not dispatched until Start")

	// Verify nothing made it through synchronously.
	select {
	case ev := <-sub:
		t.Fatalf("event MUST NOT be delivered while paused, got: %#v", ev)
	case <-time.After(20 * time.Millisecond):
		// Expected — paused buses don't dispatch.
	}

	bus.Start()

	// Now the buffered event must fire.
	select {
	case ev := <-sub:
		got, ok := ev.(testEvent)
		require.True(t, ok, "expected testEvent, got %T", ev)
		assert.Equal(t, message, got.message,
			"the event published during pause MUST be replayed verbatim "+
				"on Start — that's the entire point of the Pause/Publish/Start "+
				"contract for leadership transitions")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected buffered event to fire after Start, got nothing")
	}
}

// drain empties a channel without blocking. Used to isolate tests
// from any startup events that might have been published before the
// test's own actions.
func drain(ch <-chan Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
