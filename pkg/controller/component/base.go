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

// Package component provides a shared event-loop scaffold for controller
// components that subscribe on construction and dispatch one event at a
// time. It consolidates the logic that previously lived in
// pkg/controller/resourceloader and pkg/controller/validator (BaseLoader and
// BaseValidator respectively).
//
// Consumers embed *Base and implement EventHandler. Components that need a
// domain-specific response to a panic (e.g. scatter-gather responders)
// additionally implement PanicHandler.
package component

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// EventHandler dispatches a single event received on the subscription
// channel. Implementations should not block the goroutine for longer than
// the processing budget documented on the event.
type EventHandler interface {
	HandleEvent(event busevents.Event)
}

// PanicHandler is an optional interface that a component implements when it
// needs to publish a domain-specific response if event handling panics. The
// base always logs the panic and keeps the loop alive regardless of whether
// this interface is implemented.
type PanicHandler interface {
	HandlePanic(recovered any, event busevents.Event)
}

// Base is a reusable event-loop implementation. It subscribes on
// construction (so components are guaranteed to receive events published
// after EventBus.Start()), wraps each dispatch in a recover, and supports
// graceful shutdown via either a cancelled context or an explicit Stop.
type Base struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event
	logger    *slog.Logger
	name      string
	handler   EventHandler
	stopCh    chan struct{}
	stopOnce  sync.Once
}

// Config wires up a new Base.
//
// If EventTypes is empty the component receives every event on the bus; if
// it is non-empty the component receives only the listed types (preferred
// for components that only react to a handful of events).
type Config struct {
	EventBus   *busevents.EventBus
	Logger     *slog.Logger
	Name       string
	BufferSize int
	Handler    EventHandler
	EventTypes []string
}

// New subscribes to the EventBus and returns a Base ready to Start. The
// logger is annotated with `component=<name>` before being stored. Config
// is taken by pointer because the struct is large enough that the linter
// flags by-value passing.
func New(cfg *Config) *Base {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var eventChan <-chan busevents.Event
	if len(cfg.EventTypes) == 0 {
		eventChan = cfg.EventBus.Subscribe(cfg.Name, cfg.BufferSize)
	} else {
		eventChan = cfg.EventBus.SubscribeTypes(cfg.Name, cfg.BufferSize, cfg.EventTypes...)
	}

	return &Base{
		eventBus:  cfg.EventBus,
		eventChan: eventChan,
		logger:    logger.With("component", cfg.Name),
		name:      cfg.Name,
		handler:   cfg.Handler,
		stopCh:    make(chan struct{}),
	}
}

// Start drives the event loop until the context is cancelled or Stop is
// called. Returns nil on graceful shutdown.
func (b *Base) Start(ctx context.Context) error {
	b.logger.Debug(b.name + " starting")

	for {
		select {
		case <-ctx.Done():
			b.logger.Info(b.name+" shutting down", "reason", ctx.Err())
			return nil
		case <-b.stopCh:
			b.logger.Info(b.name + " shutting down")
			return nil
		case event := <-b.eventChan:
			b.dispatch(event)
		}
	}
}

// dispatch forwards event to the handler, recovering panics so a single bad
// event cannot tear down the goroutine. Components that implement
// PanicHandler receive a callback after the panic is logged.
func (b *Base) dispatch(event busevents.Event) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		b.logger.Error(b.name+" panicked during event handling",
			"panic", r,
			"event_type", fmt.Sprintf("%T", event))

		ph, ok := b.handler.(PanicHandler)
		if !ok {
			return
		}
		// Guard the recovery hook so a panicking hook cannot re-panic the
		// goroutine — the whole point of the outer recover is that the loop
		// must stay alive.
		func() {
			defer func() {
				if rr := recover(); rr != nil {
					b.logger.Error(b.name+" panic handler itself panicked",
						"panic", rr)
				}
			}()
			ph.HandlePanic(r, event)
		}()
	}()
	b.handler.HandleEvent(event)
}

// Stop signals the event loop to exit. Safe to call multiple times.
func (b *Base) Stop() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
	})
}

// EventBus returns the bus the component subscribed to, for use in handler
// implementations that need to publish response events.
func (b *Base) EventBus() *busevents.EventBus { return b.eventBus }

// Logger returns the component-annotated logger.
func (b *Base) Logger() *slog.Logger { return b.logger }

// Name returns the component name supplied at construction.
func (b *Base) Name() string { return b.name }
