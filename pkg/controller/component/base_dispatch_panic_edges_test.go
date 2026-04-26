// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package component

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Base.dispatch is the per-event recovery boundary that keeps a
// single bad event from tearing down the component goroutine. The
// existing PanicRecovery / PanicHandlerInvoked tests cover the
// "handler implements PanicHandler" case, but two load-bearing
// edges were uncovered:
//
//  1. Handler does NOT implement PanicHandler → recover, log,
//     return without invoking any hook. Many production
//     components register HandleEvent only (no panic hook). A
//     regression that required PanicHandler unconditionally would
//     either crash on the type assertion or skip the recovery —
//     either way one bad event would kill the goroutine.
//
//  2. PanicHandler.HandlePanic itself panics → nested recover
//     keeps the event loop alive. The deferred-defer is the
//     outer-recover's last-ditch protection: a panic in the
//     panic-hook would otherwise propagate past the dispatch
//     defer and crash the goroutine, exactly the failure mode
//     the outer recover is supposed to prevent.

// noPanicHandlerHandler intentionally does NOT implement
// PanicHandler. recordingHandler in base_test.go DOES implement
// it (even with nil hook), so we need a distinct type to exercise
// the "type assertion fails" branch.
type noPanicHandlerHandler struct {
	received   atomic.Int32
	armPanic   atomic.Bool
	panicFired atomic.Bool
	observed   chan struct{}
	observeOne bool
}

func (h *noPanicHandlerHandler) HandleEvent(event busevents.Event) {
	h.received.Add(1)
	if h.armPanic.CompareAndSwap(true, false) {
		h.panicFired.Store(true)
		panic("boom-no-hook")
	}
	if h.observed != nil && !h.observeOne {
		h.observeOne = true
		close(h.observed)
	}
	_ = event
}

// Compile-time check: noPanicHandlerHandler MUST implement
// EventHandler but MUST NOT implement PanicHandler. If this stops
// compiling because the interface gained methods, the test's
// premise needs re-evaluation.
var _ EventHandler = (*noPanicHandlerHandler)(nil)

func TestBase_Dispatch_HandlerWithoutPanicHandlerStillRecovers(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &noPanicHandlerHandler{observed: make(chan struct{})}
	h.armPanic.Store(true)

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "base-no-panic-hook",
		BufferSize: 16,
		Handler:    h,
		EventTypes: []string{events.EventTypeConfigResourceChanged},
	})

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()

	bus.Start()
	bus.Publish(events.NewConfigResourceChangedEvent(nil)) // first: panics
	bus.Publish(events.NewConfigResourceChangedEvent(nil)) // second: must reach handler

	select {
	case <-h.observed:
		// expected: loop survived the panic and processed the second event
	case <-time.After(2 * time.Second):
		t.Fatal("event loop died after panic in non-PanicHandler handler — " +
			"the dispatch recovery MUST work even when the handler doesn't " +
			"implement PanicHandler; otherwise components without explicit " +
			"panic hooks would crash on every panic")
	}

	require.True(t, h.panicFired.Load(),
		"baseline: the first event must have actually panicked")
	assert.GreaterOrEqual(t, h.received.Load(), int32(2),
		"both events should reach the handler (the second proves loop survived)")

	base.Stop()
	<-done
}

// nestedPanicHandler implements both Handler AND PanicHandler.
// HandleEvent panics; HandlePanic ALSO panics. The dispatch's
// deferred-defer must catch the inner panic and keep the event
// loop alive.
type nestedPanicHandler struct {
	received       atomic.Int32
	innerPanicSeen atomic.Bool
	observed       chan struct{}
	observeOne     bool
	armPanic       atomic.Bool
}

func (h *nestedPanicHandler) HandleEvent(event busevents.Event) {
	h.received.Add(1)
	if h.armPanic.CompareAndSwap(true, false) {
		panic("inner-boom")
	}
	if h.observed != nil && !h.observeOne {
		h.observeOne = true
		close(h.observed)
	}
	_ = event
}

func (h *nestedPanicHandler) HandlePanic(recovered any, _ busevents.Event) {
	h.innerPanicSeen.Store(true)
	// Re-panic inside the panic hook — the outer dispatch's
	// nested recover must catch this.
	panic("hook-panic: " + asString(recovered))
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestBase_Dispatch_PanicHandlerThatPanicsItselfDoesNotKillLoop(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &nestedPanicHandler{observed: make(chan struct{})}
	h.armPanic.Store(true)

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "base-nested-panic",
		BufferSize: 16,
		Handler:    h,
		EventTypes: []string{events.EventTypeConfigResourceChanged},
	})

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()

	bus.Start()
	bus.Publish(events.NewConfigResourceChangedEvent(nil)) // first: HandleEvent panics
	bus.Publish(events.NewConfigResourceChangedEvent(nil)) // second: must reach handler

	select {
	case <-h.observed:
		// expected: loop survived BOTH the inner HandleEvent panic AND the
		// nested HandlePanic panic.
	case <-time.After(2 * time.Second):
		t.Fatal("event loop died when PanicHandler.HandlePanic itself panicked — " +
			"the dispatch's deferred-defer is the last-ditch protection " +
			"against this. Without it a buggy panic hook would defeat the " +
			"whole point of the outer recover")
	}

	require.True(t, h.innerPanicSeen.Load(),
		"baseline: HandlePanic must have been invoked (otherwise we're not testing the nested-panic branch)")

	base.Stop()
	<-done
}
