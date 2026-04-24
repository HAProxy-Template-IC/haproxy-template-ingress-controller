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
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// recordingHandler counts events it has seen and optionally panics on the
// first delivery so tests can inspect recovery behaviour.
type recordingHandler struct {
	received   atomic.Int32
	armPanic   atomic.Bool
	panicFired atomic.Bool
	observed   chan struct{}
	observeOne bool

	panicHook func(recovered any, event busevents.Event)
}

func (h *recordingHandler) HandleEvent(event busevents.Event) {
	h.received.Add(1)
	if h.armPanic.CompareAndSwap(true, false) {
		h.panicFired.Store(true)
		panic("boom")
	}
	if h.observed != nil && !h.observeOne {
		h.observeOne = true
		close(h.observed)
	}
	_ = event
}

func (h *recordingHandler) HandlePanic(recovered any, event busevents.Event) {
	if h.panicHook != nil {
		h.panicHook(recovered, event)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBase_PanicRecovery asserts that a panic in HandleEvent does not stop
// the loop and that subsequent events still arrive.
func TestBase_PanicRecovery(t *testing.T) {
	bus := busevents.NewEventBus(16)

	h := &recordingHandler{observed: make(chan struct{})}
	h.armPanic.Store(true)

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "base-test",
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
	bus.Publish(events.NewConfigResourceChangedEvent(nil))
	bus.Publish(events.NewConfigResourceChangedEvent(nil))

	select {
	case <-h.observed:
	case <-time.After(2 * time.Second):
		t.Fatal("base stopped processing events after panic")
	}

	assert.True(t, h.panicFired.Load(), "first event must have panicked")
	assert.GreaterOrEqual(t, h.received.Load(), int32(2),
		"at least two events should reach the handler")

	base.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}
}

// TestBase_PanicHandlerInvoked asserts that a handler implementing
// PanicHandler receives HandlePanic after a panic, with the offending event.
func TestBase_PanicHandlerInvoked(t *testing.T) {
	bus := busevents.NewEventBus(16)

	var panicArg atomic.Value
	var panicEvent atomic.Value

	h := &recordingHandler{
		observed: make(chan struct{}),
		panicHook: func(recovered any, event busevents.Event) {
			panicArg.Store(recovered)
			panicEvent.Store(event)
		},
	}
	h.armPanic.Store(true)

	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "base-panic-handler",
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
	bus.Publish(events.NewConfigResourceChangedEvent(nil))
	bus.Publish(events.NewConfigResourceChangedEvent(nil))

	select {
	case <-h.observed:
	case <-time.After(2 * time.Second):
		t.Fatal("base stopped processing events after panic")
	}

	if got := panicArg.Load(); got == nil || got.(string) != "boom" {
		t.Fatalf("panic handler did not see the panic value, got %v", got)
	}
	if got := panicEvent.Load(); got == nil {
		t.Fatal("panic handler did not see the offending event")
	}

	base.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("base failed to shut down")
	}
}
