// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fanOut is the inner event-fanout primitive that
// powers both the Start()-time pre-start buffer flush AND the every-day
// hot path of Publish. It has FIVE distinct branches and zero direct
// test coverage:
//
//   1. Universal subscriber, channel has space → event sent
//   2. Universal subscriber, channel full       → event dropped + reported
//   3. Typed subscriber, filter accepts event   → event sent
//   4. Typed subscriber, filter rejects event   → event silently SKIPPED
//   5. Typed subscriber, channel full           → event dropped + reported
//
// Three branches are particularly load-bearing:
//
// (b) The drop branch is the EVENT-BUS BACKPRESSURE mechanism — without
//     it, slow subscribers would block the entire publish path and
//     cascade failure. A regression that blocked instead of dropped
//     would deadlock the bus the moment any subscriber's channel
//     filled.
//
// (d) The typed-filter REJECT branch is what lets typed subscriptions
//     scale: filtered events must never reach the channel. A regression
//     that delivered every event regardless of the filter would defeat
//     the whole purpose of typed subscriptions and overwhelm filtered
//     subscribers' buffers.
//
// (b)/(e) Drop accounting distinguishes lossy (silent) drops
//     from critical (callback-triggering) drops. The lossy flag must
//     be honored on the per-subscriber level — a regression that
//     ignored the flag would either spam onDrop callbacks for
//     observability subscribers (alert-fatigue) or hide critical drops
//     from the operator.

// replayEvent is a tiny test-only event type — package-private so it
// doesn't pollute the production type registry.
type replayEvent struct{ value string }

func (e replayEvent) EventType() string    { return "replay-test" }
func (e replayEvent) Timestamp() time.Time { return time.Time{} }

// fanOutAndReport installs subs on the bus and runs the exact fan-out plus
// drop-reporting sequence Publish and the Start()-time replay both use.
func fanOutAndReport(b *EventBus, event Event, subs []subscriber, typed []*typedSubscription) {
	b.subscribers = subs
	b.typedSubscribers = typed

	_, drops := b.fanOut(event)

	b.reportDrops(drops)
}

func TestFanOut_UniversalSubscriberReceivesEvent(t *testing.T) {
	bus := NewEventBus(0) // pre-start buffer size irrelevant
	subs := []subscriber{
		{ch: make(chan Event, 1), name: "test-sub", bufferSize: 1, lossy: false},
	}

	fanOutAndReport(bus, replayEvent{value: "v1"}, subs, nil)

	select {
	case got := <-subs[0].ch:
		assert.Equal(t, "v1", got.(replayEvent).value,
			"the event must arrive verbatim on the subscriber channel")
	default:
		t.Fatal("subscriber channel must contain the replayed event; the universal-subscriber send branch is the most basic contract this function provides")
	}
}

func TestFanOut_FullChannelDropsAndCallsOnDrop(t *testing.T) {
	bus := NewEventBus(0)

	// Set up the drop callback to capture invocations. A regression
	// that swapped lossy semantics or silently dropped without
	// invoking the callback would leave operators blind.
	var dropCount atomic.Int32
	var lastDrop DropInfo
	bus.SetDropCallback(func(info DropInfo) {
		lastDrop = info
		dropCount.Add(1)
	})

	// Pre-fill the channel so the next send must drop.
	subs := []subscriber{
		{ch: make(chan Event, 1), name: "slow-sub", bufferSize: 1, lossy: false},
	}
	subs[0].ch <- replayEvent{value: "first"} // fills buffer

	fanOutAndReport(bus, replayEvent{value: "second"}, subs, nil)

	// (1) Critical drop counter incremented exactly once.
	assert.Equal(t, uint64(1), bus.DroppedEventsCritical(),
		"a non-lossy subscriber whose buffer is full must increment "+
			"DroppedEventsCritical — without this counter, operators have no "+
			"visibility into bus backpressure")
	assert.Equal(t, uint64(0), bus.DroppedEventsObservability(),
		"non-lossy drops must NOT count toward the observability bucket — "+
			"those buckets must stay separated for accurate alerting")

	// (2) The onDrop callback fired with the right context.
	require.Equal(t, int32(1), dropCount.Load(),
		"the onDrop callback MUST fire on critical drops — without it the "+
			"controller's drop alerts (which subscribe via SetDropCallback) "+
			"would be silent on real backpressure incidents")
	assert.Equal(t, "replay-test", lastDrop.EventType,
		"DropInfo must carry the event type so operators can identify which "+
			"event stream is overloaded")
	assert.Equal(t, "slow-sub", lastDrop.SubscriberName,
		"DropInfo must carry the subscriber name so operators can identify "+
			"which component is the slow consumer")
}

func TestFanOut_LossySubscriberDropsAreSilent(t *testing.T) {
	// Lossy subscribers are observability-style consumers (commentator,
	// metrics) where occasional drops are expected. Their drops must
	// count toward DroppedEventsObservability and NOT trigger the
	// onDrop callback. A regression that flipped this would either
	// fire alert-fatigue from healthy commentator drops or hide
	// real critical drops in the wrong bucket.
	bus := NewEventBus(0)

	var dropCallbackCalled atomic.Int32
	bus.SetDropCallback(func(_ DropInfo) {
		dropCallbackCalled.Add(1)
	})

	subs := []subscriber{
		{ch: make(chan Event, 1), name: "commentator", bufferSize: 1, lossy: true},
	}
	subs[0].ch <- replayEvent{value: "first"} // fill buffer

	fanOutAndReport(bus, replayEvent{value: "second"}, subs, nil)

	assert.Equal(t, uint64(1), bus.DroppedEventsObservability(),
		"lossy subscriber drop must increment DroppedEventsObservability")
	assert.Equal(t, uint64(0), bus.DroppedEventsCritical(),
		"lossy drops must NOT count as critical — a regression that lumped "+
			"all drops into the critical bucket would wake on-call for normal "+
			"commentator backpressure")
	assert.Equal(t, int32(0), dropCallbackCalled.Load(),
		"the onDrop callback must NOT fire for lossy drops — it's reserved "+
			"for critical drops to avoid alert-fatigue")
}

func TestFanOut_TypedSubscriberFiltersByFunc(t *testing.T) {
	bus := NewEventBus(0)

	// Two typed subscribers: one accepts replayEvent, the other
	// rejects everything. Pinning both at once proves the filter
	// dispatch is per-subscriber, not global.
	accepting := &typedSubscription{
		eventTypesStr: "replay-test",
		outputChan:    make(chan Event, 1),
		filterFunc:    func(e Event) bool { return e.EventType() == "replay-test" },
		name:          "accepting-sub",
		bufferSize:    1,
	}
	rejecting := &typedSubscription{
		eventTypesStr: "never-matches",
		outputChan:    make(chan Event, 1),
		filterFunc:    func(_ Event) bool { return false },
		name:          "rejecting-sub",
		bufferSize:    1,
	}

	fanOutAndReport(bus, replayEvent{value: "v"}, nil, []*typedSubscription{accepting, rejecting})

	// Accepting subscriber receives the event.
	select {
	case got := <-accepting.outputChan:
		assert.Equal(t, "v", got.(replayEvent).value,
			"the filter-accepting typed subscriber must receive the event")
	default:
		t.Fatal("accepting subscriber's channel is empty — the filter-true branch failed")
	}

	// Rejecting subscriber's channel stays empty. This is the
	// load-bearing assertion: a regression that delivered every event
	// regardless of the filter would defeat typed subscriptions and
	// overwhelm filtered subscribers' buffers under high event volume.
	select {
	case got := <-rejecting.outputChan:
		t.Fatalf("rejecting subscriber received an event it filtered out: %+v — "+
			"a regression that delivered every event regardless of filterFunc "+
			"would break typed-subscription scaling", got)
	default:
		// expected: empty channel
	}
}

func TestFanOut_TypedSubscriberFullChannelDropsAndRecords(t *testing.T) {
	bus := NewEventBus(0)

	var dropCallbackCalled atomic.Int32
	var lastDrop DropInfo
	bus.SetDropCallback(func(info DropInfo) {
		lastDrop = info
		dropCallbackCalled.Add(1)
	})

	typedSub := &typedSubscription{
		eventTypesStr: "replay-test",
		outputChan:    make(chan Event, 1),
		filterFunc:    func(_ Event) bool { return true },
		name:          "slow-typed-sub",
		bufferSize:    1,
	}
	typedSub.outputChan <- replayEvent{value: "first"} // fill buffer

	fanOutAndReport(bus, replayEvent{value: "second"}, nil, []*typedSubscription{typedSub})

	assert.Equal(t, uint64(1), bus.DroppedEventsCritical(),
		"typed subscriber drop must count as critical")
	assert.Equal(t, int32(1), dropCallbackCalled.Load(),
		"typed subscriber drop must fire onDrop")
	assert.Equal(t, "slow-typed-sub", lastDrop.SubscriberName,
		"DropInfo must carry the typed subscriber's name")
	assert.Equal(t, "replay-test", lastDrop.EventTypes,
		"DropInfo must carry the typed subscription's event-types-string so "+
			"operators can see WHICH typed subscription dropped (universal "+
			"subscribers would have empty EventTypes)")
}
