// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Subscribe[T] and SubscribeMultiple are the two generic helpers in
// pkg/events/typed.go that wrap the universal Subscribe with a typed
// forwarder goroutine. Neither has direct test coverage, yet both are
// part of the public package API documented in pkg/events/CLAUDE.md as
// "best type safety" and "context-aware filtering" respectively.
//
// Three load-bearing contracts protect both helpers:
//
//  1. Type filtering — Subscribe[T] forwards ONLY events that match T;
//     events of other types are silently dropped (caller doesn't see
//     them). SubscribeMultiple uses string-based type matching against
//     Event.EventType(). A regression that swapped these (e.g.
//     forwarded everything) would deliver wrong-type events that the
//     caller's `for event := range` loop would crash on via panicking
//     type assertion downstream.
//
//  2. Context cancellation lifecycle — when the context cancels, the
//     forwarder goroutine MUST exit AND call bus.Unsubscribe to drop
//     the underlying universal subscription. Without the cleanup, every
//     Subscribe[T] call leaks both a goroutine and a subscription for
//     the lifetime of the bus.
//
//  3. Channel-full drop semantics — when the typed/output channel is
//     full, events are dropped silently to match universal Subscribe
//     behaviour. A regression that blocked here would let a slow
//     consumer back up the bus and stall every other subscriber.

// typedTestEvent and otherTypedTestEvent are minimal Event implementations
// used to exercise the type-filtering branches without depending on any
// pkg/controller/events domain type.
type typedTestEvent struct {
	value string
	ts    time.Time
}

func (e *typedTestEvent) EventType() string    { return "test.typed" }
func (e *typedTestEvent) Timestamp() time.Time { return e.ts }

type otherTypedTestEvent struct {
	value string
	ts    time.Time
}

func (e *otherTypedTestEvent) EventType() string    { return "test.other" }
func (e *otherTypedTestEvent) Timestamp() time.Time { return e.ts }

func TestSubscribeGeneric_OnlyForwardsMatchingType(t *testing.T) {
	bus := NewEventBus(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	typedChan := Subscribe[*typedTestEvent](ctx, bus, 10)
	require.NotNil(t, typedChan)
	bus.Start()

	// Publish a mix: matches and non-matches. The typed channel must
	// receive ONLY the matching events.
	bus.Publish(&typedTestEvent{value: "match-1"})
	bus.Publish(&otherTypedTestEvent{value: "skip-1"})
	bus.Publish(&typedTestEvent{value: "match-2"})
	bus.Publish(&otherTypedTestEvent{value: "skip-2"})

	// Read what we expect.
	got := drainTyped(typedChan, 2, 500*time.Millisecond)
	require.Len(t, got, 2,
		"typed channel must receive both matching events; "+
			"a regression that forwarded everything would deliver wrong-type "+
			"events the caller's range loop can't handle")
	assert.Equal(t, "match-1", got[0].value)
	assert.Equal(t, "match-2", got[1].value)

	// And no further events (non-matches must NOT have been forwarded).
	select {
	case extra := <-typedChan:
		t.Fatalf("typed channel received unexpected event %+v — non-matching "+
			"events must be dropped silently, not forwarded", extra)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestSubscribeGeneric_ContextCancellationUnsubscribesFromBus(t *testing.T) {
	// Cancellation MUST drop the underlying universal subscription so
	// that every Subscribe[T] call doesn't leak a goroutine + a
	// subscription for the lifetime of the bus. This is the cleanup
	// contract that lets components freely use this helper.
	bus := NewEventBus(10)
	ctx, cancel := context.WithCancel(context.Background())

	_ = Subscribe[*typedTestEvent](ctx, bus, 10)

	// Subscribe creates a universal subscription internally.
	bus.mu.RLock()
	beforeCount := len(bus.subscribers)
	bus.mu.RUnlock()
	require.Greater(t, beforeCount, 0,
		"baseline: Subscribe[T] must register an internal universal subscription")

	cancel()

	// After cancel, the goroutine should call bus.Unsubscribe and the
	// internal subscription should drop. We retry a few times because
	// the cleanup is asynchronous (happens in the forwarder goroutine).
	deadline := time.Now().Add(time.Second)
	var afterCount int
	for time.Now().Before(deadline) {
		bus.mu.RLock()
		afterCount = len(bus.subscribers)
		bus.mu.RUnlock()
		if afterCount < beforeCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.Less(t, afterCount, beforeCount,
		"after context cancellation, the internal subscription MUST be removed "+
			"from bus.subscribers — a regression that left it would leak both "+
			"a goroutine AND a subscription for the lifetime of the bus, "+
			"causing slow memory creep in components that recreate Subscribe[T] "+
			"per request")
}

func TestSubscribeMultiple_FiltersByEventTypeString(t *testing.T) {
	// SubscribeMultiple filters by the EventType() string, NOT by Go
	// type. Pin both:
	//  - matches: events whose EventType() is in the type set are forwarded
	//  - non-matches: events whose EventType() is NOT in the type set are dropped
	bus := NewEventBus(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	multiChan := SubscribeMultiple(ctx, bus, 10, "test.typed", "test.other")
	require.NotNil(t, multiChan)
	bus.Start()

	// Publish all three event types — only test.typed and test.other
	// should reach the channel; test.unknown is dropped.
	bus.Publish(&typedTestEvent{value: "yes-typed"})
	bus.Publish(&otherTypedTestEvent{value: "yes-other"})
	bus.Publish(&unknownTypeEvent{eventType: "test.unknown"})

	// Drain up to 2 events (the two matching types).
	collected := make([]Event, 0, 2)
	deadline := time.After(500 * time.Millisecond)
loop:
	for len(collected) < 2 {
		select {
		case e := <-multiChan:
			collected = append(collected, e)
		case <-deadline:
			break loop
		}
	}
	require.Len(t, collected, 2,
		"SubscribeMultiple must forward both events whose EventType() is in "+
			"the type set; a regression that mishandled the type filter would "+
			"drop matching events or forward non-matches")

	// Verify the unknown event did NOT come through.
	select {
	case extra := <-multiChan:
		t.Fatalf("SubscribeMultiple forwarded an event whose EventType() (%q) "+
			"was not in the type set — would deliver wrong-type events to "+
			"caller's range loop", extra.EventType())
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestSubscribeMultiple_ContextCancellationUnsubscribesFromBus(t *testing.T) {
	// Same lifecycle contract as Subscribe[T]: cancellation MUST drop
	// the internal universal subscription. Pinning this for both
	// generics catches a regression in either path that broke the
	// shared cleanup pattern.
	bus := NewEventBus(10)
	ctx, cancel := context.WithCancel(context.Background())

	_ = SubscribeMultiple(ctx, bus, 10, "test.typed")

	bus.mu.RLock()
	beforeCount := len(bus.subscribers)
	bus.mu.RUnlock()
	require.Greater(t, beforeCount, 0,
		"baseline: SubscribeMultiple must register an internal universal subscription")

	cancel()

	// Wait for async cleanup.
	deadline := time.Now().Add(time.Second)
	var afterCount int
	for time.Now().Before(deadline) {
		bus.mu.RLock()
		afterCount = len(bus.subscribers)
		bus.mu.RUnlock()
		if afterCount < beforeCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.Less(t, afterCount, beforeCount,
		"after context cancellation, SubscribeMultiple's internal subscription "+
			"MUST be removed — same memory-leak risk as Subscribe[T] regression")
}

func TestSubscribeMultiple_EmptyTypeListMatchesNothing(t *testing.T) {
	// Edge case: passing zero type strings creates an empty type set,
	// so no event should ever match. This is the documented behaviour
	// (each type string opts an event type IN), and a regression that
	// treated an empty set as "match everything" would silently flood
	// the channel with all bus events.
	bus := NewEventBus(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No type strings → empty type set → nothing matches.
	multiChan := SubscribeMultiple(ctx, bus, 10)
	bus.Start()

	bus.Publish(&typedTestEvent{value: "should-not-arrive"})
	bus.Publish(&otherTypedTestEvent{value: "should-not-arrive"})

	select {
	case got := <-multiChan:
		t.Fatalf("SubscribeMultiple with NO type strings forwarded an event "+
			"(%+v) — empty type set must opt OUT of every event, NOT "+
			"opt INTO all events", got)
	case <-time.After(100 * time.Millisecond):
		// expected — no events match
	}
}

// drainTyped collects up to n events from typedChan within timeout.
func drainTyped[T Event](ch <-chan T, n int, timeout time.Duration) []T {
	out := make([]T, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
	return out
}

// unknownTypeEvent has a custom EventType() that's NOT in the test set,
// used to verify SubscribeMultiple drops non-matching events.
type unknownTypeEvent struct {
	eventType string
	ts        time.Time
}

func (e *unknownTypeEvent) EventType() string    { return e.eventType }
func (e *unknownTypeEvent) Timestamp() time.Time { return e.ts }
