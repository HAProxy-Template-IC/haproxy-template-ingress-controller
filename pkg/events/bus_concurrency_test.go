// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bus mutates its subscriber slices after Start(): leader-only components
// subscribe and unsubscribe per leadership term, and every scatter-gather
// Request subscribes for the duration of one request. Three readers used to
// walk those slices without the lock the writers hold. These tests pair the
// production goroutines so -race sees the combination; without them the whole
// package passes clean while the races remain.

// TestBus_ConcurrentReplayUnsubscribeAndCount races the Start()-time replay,
// the metrics ticker's SubscriberCount(), and the subscribe/unsubscribe churn
// of leader-only components and scatter-gather against each other.
//
// UnsubscribeTyped removes an entry by overwriting it in place, so a replay
// that iterated a snapshot of the slice header read words another goroutine
// was writing.
func TestBus_ConcurrentReplayUnsubscribeAndCount(t *testing.T) {
	// Fixed iteration counts on every goroutine rather than a stop channel:
	// the replay window is microseconds, so a "run until told to stop" churn
	// can finish before the racing goroutine is even scheduled.
	const rounds = 500

	bus := NewEventBus(16)

	// Buffer size 1 so the replay fills it immediately and also walks the drop
	// path, which reads the onDrop callback that SetDropCallback writes.
	bus.SubscribeTypes("slow", 1, "replay-test")
	bus.Start()

	repeat := func(wg *sync.WaitGroup, work func()) {
		defer wg.Done()
		for range rounds {
			work()
		}
	}

	var wg sync.WaitGroup
	wg.Add(3)
	// LeaderOnly to suppress the late-subscription warning: this is exactly the
	// production caller (leader-only components, scatter-gather).
	go repeat(&wg, func() {
		ch := bus.SubscribeTypesLeaderOnly("churn", 1, "replay-test")
		bus.UnsubscribeTyped(ch)
	})
	go repeat(&wg, func() { _ = bus.SubscriberCount() })
	go repeat(&wg, func() { bus.SetDropCallback(func(DropInfo) {}) })

	// Pause/Publish/Start is the leadership-transition sequence; each Start
	// replays the buffer, which is the loop that walked the subscriber slices.
	for range rounds {
		bus.Pause()
		bus.Publish(replayEvent{value: "buffered"})
		bus.Publish(replayEvent{value: "buffered"})
		bus.Start()
	}

	wg.Wait()
}

// TestBus_PreStartBufferOverflowIsAccounted pins that a pre-start capacity drop
// reaches the same counter and callback as every other drop.
//
// It used to log a warning and return, bypassing the counter entirely — so the
// one drop path that can lose the bootstrap events published just before
// Start() was invisible to the operator alert that keys off that counter.
func TestBus_PreStartBufferOverflowIsAccounted(t *testing.T) {
	bus := NewEventBus(MaxPreStartBufferSize)

	var mu sync.Mutex
	var dropped []DropInfo
	bus.SetDropCallback(func(info DropInfo) {
		mu.Lock()
		defer mu.Unlock()
		dropped = append(dropped, info)
	})

	for range MaxPreStartBufferSize {
		bus.Publish(replayEvent{value: "fits"})
	}
	require.Equal(t, uint64(0), bus.DroppedEventsCritical(),
		"events that fit in the pre-start buffer must not be counted as drops")

	bus.Publish(replayEvent{value: "overflows"})

	assert.Equal(t, uint64(1), bus.DroppedEventsCritical(),
		"a pre-start capacity drop is a lost event and must reach "+
			"DroppedEventsCritical — the shipped drop alert keys off that counter, "+
			"so a bypassed increment means a wedged controller reporting zero drops")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, dropped, 1, "the drop callback must fire for a pre-start capacity drop")
	assert.Equal(t, PreStartBufferSubscriber, dropped[0].SubscriberName,
		"the drop must name the pre-start buffer so an operator can tell it apart "+
			"from a slow subscriber")
	assert.Equal(t, "replay-test", dropped[0].EventType,
		"DropInfo must carry the event type that was lost")
}

// TestBus_DropCallbackRunsOutsideBusLocks pins that the drop callback may call
// back into the bus.
//
// The callback is arbitrary caller-supplied code. It used to run inline under
// b.mu.RLock (Publish) and under startMu (replay), so a callback that
// subscribed deadlocked against its own publisher.
func TestBus_DropCallbackRunsOutsideBusLocks(t *testing.T) {
	bus := NewEventBus(1)
	bus.Start()

	sub := bus.Subscribe("slow", 1)
	sub2 := bus.Subscribe("filler", 1)
	bus.Publish(replayEvent{value: "fills both buffers"})

	bus.SetDropCallback(func(DropInfo) {
		// Both take b.mu — Lock for the subscribe, RLock for the count.
		bus.SubscribeTypesLeaderOnly("from-callback", 1, "replay-test")
		_ = bus.SubscriberCount()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		bus.Publish(replayEvent{value: "drops"})
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Publish deadlocked: the drop callback must run with no bus lock held, " +
			"otherwise a callback that touches the bus blocks its own publisher forever")
	}

	assert.Equal(t, uint64(2), bus.DroppedEventsCritical(),
		"both full subscribers must record a drop")
	// Keep the subscriptions referenced so the channels are not collected.
	assert.NotNil(t, sub)
	assert.NotNil(t, sub2)
}
