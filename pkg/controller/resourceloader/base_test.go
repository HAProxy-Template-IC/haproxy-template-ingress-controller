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

package resourceloader

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

// panickyProcessor records received events and optionally panics on the first
// event so we can verify the base loop keeps running afterwards.
type panickyProcessor struct {
	received    atomic.Int32
	panicOnce   atomic.Bool
	panicArmed  atomic.Bool
	lastEvent   atomic.Pointer[busevents.Event]
	observed    chan struct{}
	observeOnce bool
}

func (p *panickyProcessor) ProcessEvent(event busevents.Event) {
	// Record the event so the test can observe recovery via subsequent deliveries.
	p.received.Add(1)
	p.lastEvent.Store(&event)

	if p.panicArmed.CompareAndSwap(true, false) {
		p.panicOnce.Store(true)
		panic("boom")
	}

	if p.observed != nil && !p.observeOnce {
		p.observeOnce = true
		close(p.observed)
	}
}

// TestBaseLoader_PanicRecovery proves the loader keeps processing events after
// a processor panic.
func TestBaseLoader_PanicRecovery(t *testing.T) {
	bus := busevents.NewEventBus(16)
	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := &panickyProcessor{observed: make(chan struct{})}
	p.panicArmed.Store(true)

	loader := NewBaseLoader(
		bus,
		discardLogger,
		"loader-test",
		16,
		p,
		events.EventTypeConfigResourceChanged,
	)

	ctx, cancel := t.Context(), func() {}
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = loader.Start(ctx)
		close(done)
	}()

	bus.Start()
	// First event triggers a panic inside the processor.
	bus.Publish(events.NewConfigResourceChangedEvent(nil))
	// Second event must still be delivered: this is the regression guard.
	bus.Publish(events.NewConfigResourceChangedEvent(nil))

	select {
	case <-p.observed:
		// good: second event landed after the panic
	case <-time.After(2 * time.Second):
		t.Fatal("loader stopped processing events after panic")
	}

	assert.True(t, p.panicOnce.Load(), "first event should have panicked")
	assert.GreaterOrEqual(t, p.received.Load(), int32(2), "expected at least two events processed")

	loader.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loader failed to shut down")
	}
}
